package script

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/jancuk/semar/internal/domain"
)

var (
	variablePattern = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]*)\}\}`)
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
	if out.Query, err = renderMap(req.Query, vars, nil); err != nil {
		return domain.APIRequest{}, fmt.Errorf("query: %w", err)
	}
	if out.Headers, err = renderMap(req.Headers, vars, checkHeaderValue); err != nil {
		return domain.APIRequest{}, fmt.Errorf("headers: %w", err)
	}
	if out.JSON, err = renderJSON(req.JSON, vars); err != nil {
		return domain.APIRequest{}, fmt.Errorf("json: %w", err)
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
// an empty segment other than after the last slash, a "." or ".." segment also after percent-decoding,
// or an encoded slash or backslash.
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
	if strings.IndexFunc(val, unicode.IsControl) >= 0 {
		return fmt.Errorf("variable %s cannot be used in a header: it holds a control character", name)
	}
	return nil
}

func renderMap(m map[string]string, vars map[string]string, check func(name, val string) error) (map[string]string, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		r, err := substitute(v, vars, check)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = r
	}
	return out, nil
}

func renderJSON(v any, vars map[string]string) (any, error) {
	switch x := v.(type) {
	case string:
		return substitute(x, vars, nil)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			r, err := renderJSON(e, vars)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := renderJSON(e, vars)
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

// templateStrings lists every string of a request in which a {{variable}} is replaced:
// the path, the query values, the header values, and the string values of the JSON body.
func templateStrings(req domain.APIRequest) []string {
	out := []string{req.Path}
	for _, v := range req.Query {
		out = append(out, v)
	}
	for _, v := range req.Headers {
		out = append(out, v)
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			out = append(out, x)
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(req.JSON)
	return out
}

// variables lists the names used in a request, in no particular order.
func variables(req domain.APIRequest) []string {
	var names []string
	for _, s := range templateStrings(req) {
		for _, m := range variablePattern.FindAllStringSubmatch(s, -1) {
			names = append(names, m[1])
		}
	}
	return names
}
