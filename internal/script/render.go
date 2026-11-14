package script

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

var (
	variablePattern = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)
	namePattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
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
	if strings.Contains(variablePattern.ReplaceAllString(tpl, ""), "{{") {
		return "", fmt.Errorf("malformed variable in %q", tpl)
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

func checkPathValue(name, val string) error {
	reason := ""
	switch {
	case val == "":
		reason = "it is empty"
	case val == "." || strings.Contains(val, ".."):
		reason = "it holds a dot segment"
	case strings.ContainsAny(val, "/\\?#%"):
		reason = "it holds one of / \\ ? # %"
	case strings.ContainsAny(val, "{}"):
		reason = "it holds a brace"
	case strings.IndexFunc(val, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0:
		reason = "it holds a control character"
	}
	if reason != "" {
		return fmt.Errorf("variable %s cannot be used in a path: %s", name, reason)
	}
	return nil
}

func checkHeaderValue(name, val string) error {
	if strings.ContainsAny(val, "\r\n") {
		return fmt.Errorf("variable %s cannot be used in a header: it holds a line break", name)
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

// variables lists the names used in a request, in no particular order.
func variables(req domain.APIRequest) []string {
	var names []string
	add := func(s string) {
		for _, m := range variablePattern.FindAllStringSubmatch(s, -1) {
			names = append(names, m[1])
		}
	}
	add(req.Path)
	for _, v := range req.Query {
		add(v)
	}
	for _, v := range req.Headers {
		add(v)
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			add(x)
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
	return names
}
