package script

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

var (
	// A dotted name is <as>.<output>, run.id, or data.<key>. A capture name has no dot.
	variablePattern = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*)\}\}`)
	namePattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// Render returns a copy of req with every {{name}} replaced in the path, the query values,
// the header values, and the string values of the JSON body. Variables hold text, so a JSON
// string that is one variable stays a string. A variable's value is inserted into the path
// as one segment, so a captured value cannot change which endpoint is called.
func Render(req domain.APIRequest, vars map[string]string) (domain.APIRequest, error) {
	out := req
	var err error
	if out.Path, err = substitute(req.Path, vars, checkPathValue); err != nil {
		return domain.APIRequest{}, fmt.Errorf("path: %w", err)
	}
	if err = checkRenderedPath(req.Path, out.Path); err != nil {
		return domain.APIRequest{}, fmt.Errorf("path: %w", err)
	}
	if out.Query, err = renderMap("query", req.Query, vars, nil); err != nil {
		return domain.APIRequest{}, err
	}
	if out.Headers, err = renderMap("header", req.Headers, vars, checkHeaderValue); err != nil {
		return domain.APIRequest{}, err
	}
	if out.JSON, err = renderJSON("json", req.JSON, vars); err != nil {
		return domain.APIRequest{}, err
	}
	return out, nil
}

func substitute(tpl string, vars map[string]string, check func(name, val string) error) (string, error) {
	if err := checkPlaceholders(tpl); err != nil {
		return "", err
	}
	var err error
	out := variablePattern.ReplaceAllStringFunc(tpl, func(m string) string {
		name := variablePattern.FindStringSubmatch(m)[1]
		val, ok := vars[name]
		switch {
		case err != nil:
		case !ok:
			err = fmt.Errorf("unknown variable %s", name)
		case check != nil:
			if cerr := check(name, val); cerr != nil {
				err = cerr
			}
		}
		return val
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// checkPlaceholders is the one rule for a malformed variable, used by Render and Validate:
// after every well-formed {{name}} is removed, no "{{" may remain.
func checkPlaceholders(tpl string) error {
	if strings.Contains(variablePattern.ReplaceAllString(tpl, ""), "{{") {
		return fmt.Errorf("malformed variable in %q", tpl)
	}
	return nil
}

// percentBeforeVariable matches a variable that directly follows a % or a % and one hex digit,
// where the value could finish a percent escape.
var percentBeforeVariable = regexp.MustCompile(`%[0-9A-Fa-f]?\{\{`)

// checkPathValue allows only the unreserved characters of RFC 3986, and never "." or "..".
// The error names the variable and does not print the value.
func checkPathValue(name, val string) error {
	reason := ""
	switch {
	case val == "":
		reason = "it is empty"
	case val == "." || val == "..":
		reason = "it is a dot segment"
	case strings.IndexFunc(val, func(r rune) bool { return !isUnreserved(r) }) >= 0:
		reason = "it holds a character other than a letter, a digit, - . _ ~"
	}
	if reason != "" {
		return fmt.Errorf("variable %s cannot be used in a path: %s", name, reason)
	}
	return nil
}

func isUnreserved(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_' || r == '~'
}

// checkPath reports why a request path cannot be sent as it is: no leading slash, a "?" or "#",
// a backslash, an empty segment other than after the last slash, a "." or ".." segment also after
// percent-decoding, an encoded slash or backslash, or an encoded percent sign. A backslash is sent
// as %5C, and %25 decodes to a "%" that can start a dot or a slash on a server that decodes twice.
func checkPath(p string) error {
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("path %q must start with /", p)
	}
	if strings.Contains(p, "?") {
		return fmt.Errorf("path %q holds ?", p)
	}
	if strings.Contains(p, "#") {
		return fmt.Errorf("path %q holds #", p)
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("path %q holds a backslash", p)
	}
	if strings.Contains(p, "%25") {
		return fmt.Errorf("path %q holds an encoded percent sign", p)
	}
	body := p[1:]
	if body == "" {
		return nil
	}
	segs := strings.Split(body, "/")
	for i, seg := range segs {
		if seg == "" {
			if i == len(segs)-1 && i > 0 {
				continue
			}
			return fmt.Errorf("path %q holds an empty segment", p)
		}
		lower := strings.ToLower(seg)
		if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
			return fmt.Errorf("path %q holds an encoded slash or backslash", p)
		}
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			decoded = strings.ReplaceAll(lower, "%2e", ".")
		}
		if decoded == "." || decoded == ".." {
			return fmt.Errorf("path %q holds a dot segment", p)
		}
	}
	return nil
}

// checkRenderedPath applies checkPath to the result of a substitution and requires
// that it has as many segments as the template.
func checkRenderedPath(tpl, rendered string) error {
	if err := checkPath(rendered); err != nil {
		return fmt.Errorf("%w (template %q)", err, tpl)
	}
	if strings.Count(rendered, "/") != strings.Count(tpl, "/") {
		return fmt.Errorf("path %q has a different number of segments than %q", rendered, tpl)
	}
	return nil
}

func checkHeaderValue(name, val string) error {
	if strings.ContainsFunc(val, illegalInHeaderValue) {
		return fmt.Errorf("variable %s cannot be used in a header: it holds a control character", name)
	}
	return nil
}

// renderMap renders the values in the order of their keys, so that the same request
// always fails on the same key.
func renderMap(kind string, m map[string]string, vars map[string]string, check func(name, val string) error) (map[string]string, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		r, err := substitute(m[k], vars, check)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", kind, k, err)
		}
		out[k] = r
	}
	return out, nil
}

func renderJSON(where string, v any, vars map[string]string) (any, error) {
	switch x := v.(type) {
	case string:
		r, err := substitute(x, vars, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		return r, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for _, k := range slices.Sorted(maps.Keys(x)) {
			r, err := renderJSON(where+"."+k, x[k], vars)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := renderJSON(fmt.Sprintf("%s[%d]", where, i), e, vars)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

// template is one string of a request in which a {{variable}} is replaced, and where it is:
// "path", "query page", "header X-A", or a JSON location such as "json items[0].id".
type template struct{ where, text string }

// templates lists the path, the query values and the header values in the order of their
// keys, and the string values of the JSON body in the order of their keys, so that the
// problems of a request always come in the same order.
func templates(req domain.APIRequest) []template {
	out := []template{{"path", req.Path}}
	for _, k := range slices.Sorted(maps.Keys(req.Query)) {
		out = append(out, template{"query " + k, req.Query[k]})
	}
	for _, k := range slices.Sorted(maps.Keys(req.Headers)) {
		out = append(out, template{"header " + k, req.Headers[k]})
	}
	var walk func(where string, v any)
	walk = func(where string, v any) {
		switch x := v.(type) {
		case string:
			out = append(out, template{where, x})
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(x)) {
				walk(where+"."+k, x[k])
			}
		case []any:
			for i, e := range x {
				walk(fmt.Sprintf("%s[%d]", where, i), e)
			}
		}
	}
	walk("json", req.JSON)
	return out
}

// templateStrings is the text of templates, in the same order.
func templateStrings(req domain.APIRequest) []string {
	ts := templates(req)
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.text
	}
	return out
}

// templateProblems reports every malformed variable of a request, each with where it is.
func templateProblems(req domain.APIRequest) []string {
	var out []string
	for _, t := range templates(req) {
		if err := checkPlaceholders(t.text); err != nil {
			out = append(out, t.where+": "+err.Error())
		}
	}
	return out
}

// variables lists the names used in a request, in the order of templates.
func variables(req domain.APIRequest) []string {
	var names []string
	for _, s := range templateStrings(req) {
		for _, m := range variablePattern.FindAllStringSubmatch(s, -1) {
			names = append(names, m[1])
		}
	}
	return names
}

// RenderWeb returns a copy of w with every {{name}} replaced in the path and the value. A variable's
// value is inserted into the path as one segment, as in a request.
func RenderWeb(w domain.WebAction, vars map[string]string) (domain.WebAction, error) {
	out := w
	var err error
	if w.Path != "" {
		if out.Path, err = substitute(w.Path, vars, checkPathValue); err != nil {
			return domain.WebAction{}, fmt.Errorf("path: %w", err)
		}
		if err = checkRenderedPath(w.Path, out.Path); err != nil {
			return domain.WebAction{}, fmt.Errorf("path: %w", err)
		}
	}
	if out.Value, err = substitute(w.Value, vars, nil); err != nil {
		return domain.WebAction{}, fmt.Errorf("value: %w", err)
	}
	return out, nil
}

// RenderLocator returns a copy of l with every {{name}} replaced in its fields. A name without a
// value is an error that names the variable and the field.
func RenderLocator(l domain.Locator, vars map[string]string) (domain.Locator, error) {
	out := l
	for _, f := range []struct {
		name string
		to   *string
		from string
	}{{"testId", &out.TestID, l.TestID}, {"role", &out.Role, l.Role}, {"name", &out.Name, l.Name}, {"text", &out.Text, l.Text}, {"css", &out.CSS, l.CSS}} {
		v, err := substitute(f.from, vars, nil)
		if err != nil {
			return domain.Locator{}, fmt.Errorf("%s: %w", f.name, err)
		}
		*f.to = v
	}
	return out, nil
}

// LocatorVariables lists the variable names in the fields of a locator, in order and without repeats.
func LocatorVariables(l domain.Locator) []string {
	var names []string
	for _, text := range []string{l.TestID, l.Role, l.Name, l.Text, l.CSS} {
		for _, m := range variablePattern.FindAllStringSubmatch(text, -1) {
			if !slices.Contains(names, m[1]) {
				names = append(names, m[1])
			}
		}
	}
	return names
}
