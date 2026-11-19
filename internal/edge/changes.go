package edge

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/openapi"
)

// maxValueLength is the longest text that a case writes for a limit of length. A longer one would fill
// a feature file and a request with filler.
const maxValueLength = 2000

const (
	notListed = "semar-not-a-listed-value"
	noSuchID  = "semar-no-such-id"
	noSuchUID = "00000000-0000-0000-0000-000000000000"
)

// idName is the name of a field that holds an ID, as the plan reads it elsewhere.
var idName = regexp.MustCompile(`(?:^id$|Id$|ID$|_id$)`)

// target is a value that a case may change: a field of the JSON body or a query parameter.
type target struct {
	name     string
	typ      string
	enum     []string
	limits   openapi.Limits
	inBody   bool
	required bool
}

func targets(op openapi.Operation, base domain.APIRequest) []target {
	var out []target
	if m, ok := base.JSON.(map[string]any); ok && m != nil {
		for _, f := range op.Body {
			out = append(out, target{f.Name, f.Type, f.Enum, f.Limits, true, f.Required})
		}
	}
	for _, p := range op.Params {
		if p.In == "query" {
			out = append(out, target{p.Name, p.Type, p.Enum, p.Limits, false, p.Required})
		}
	}
	return out
}

// set puts a value into the body or the query of a request. A query value is text.
func (t target) set(r *domain.APIRequest, v any) {
	if t.inBody {
		r.JSON.(map[string]any)[t.name] = v
		return
	}
	if r.Query == nil {
		r.Query = map[string]string{}
	}
	switch x := v.(type) {
	case string:
		r.Query[t.name] = x
	default:
		r.Query[t.name] = fmt.Sprint(x)
	}
}

func requiredFields(op openapi.Operation, base domain.APIRequest) []change {
	m, ok := base.JSON.(map[string]any)
	if !ok {
		return nil
	}
	var out []change
	for _, f := range op.Body {
		if _, sent := m[f.Name]; !f.Required || !sent {
			continue
		}
		name := f.Name
		out = append(out, change{
			on: name, title: "without the required field " + name,
			apply: func(r *domain.APIRequest) { delete(r.JSON.(map[string]any), name) },
		})
	}
	return out
}

// wrongOf is a value of another JSON type than the document says, and the name of its type.
func wrongOf(typ string) (v any, got string, ok bool) {
	switch typ {
	case "string":
		return json.Number("12345"), "a number", true
	case "integer", "number", "boolean", "array", "object":
		return "semar-wrong-type", "a string", true
	}
	return nil, "", false
}

func wrongTypes(op openapi.Operation, base domain.APIRequest) []change {
	m, ok := base.JSON.(map[string]any)
	if !ok {
		return nil
	}
	var out []change
	for _, f := range op.Body {
		v, got, ok := wrongOf(f.Type)
		if _, sent := m[f.Name]; !ok || !sent {
			continue
		}
		t := target{name: f.Name, inBody: true}
		out = append(out, change{
			on: f.Name, title: fmt.Sprintf("with %s as %s, and the document says %s", f.Name, got, f.Type),
			apply: func(r *domain.APIRequest) { t.set(r, v) },
		})
	}
	return out
}

func isText(typ string) bool   { return typ == "" || typ == "string" }
func isNumber(typ string) bool { return typ == "" || typ == "integer" || typ == "number" }

func outOfRange(op openapi.Operation, base domain.APIRequest) []change {
	var out []change
	for _, t := range targets(op, base) {
		l := t.limits
		if n := l.MinLength; n != nil && *n > 0 && isText(t.typ) {
			out = append(out, textChange(t, *n-1, ".minLength", fmt.Sprintf("with %s shorter than minLength %d", t.name, *n)))
		}
		if n := l.MaxLength; n != nil && isText(t.typ) {
			out = append(out, textChange(t, *n+1, ".maxLength", fmt.Sprintf("with %s longer than maxLength %d", t.name, *n)))
		}
		if f := l.Minimum; f != nil && isNumber(t.typ) {
			out = append(out, numberChange(t, *f-1, ".minimum", fmt.Sprintf("with %s below the minimum %s", t.name, number(*f))))
		}
		if f := l.Maximum; f != nil && isNumber(t.typ) {
			out = append(out, numberChange(t, *f+1, ".maximum", fmt.Sprintf("with %s above the maximum %s", t.name, number(*f))))
		}
	}
	return out
}

// textChange sets a text of n filler characters.
func textChange(t target, n int, bound, title string) change {
	ch := change{on: t.name + bound, title: title}
	if n > maxValueLength {
		ch.skip = ReasonTooLong
		return ch
	}
	text := strings.Repeat("a", n)
	ch.apply = func(r *domain.APIRequest) { t.set(r, text) }
	return ch
}

func numberChange(t target, f float64, bound, title string) change {
	v := json.Number(number(f))
	return change{on: t.name + bound, title: title, apply: func(r *domain.APIRequest) {
		if t.inBody {
			t.set(r, v)
			return
		}
		t.set(r, string(v))
	}}
}

// number is f as the shortest decimal text, with no exponent.
func number(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func notInEnum(op openapi.Operation, base domain.APIRequest) []change {
	var out []change
	for _, t := range targets(op, base) {
		if len(t.enum) == 0 {
			continue
		}
		var v any = outside(t.enum)
		if !isText(t.typ) && t.typ != "boolean" {
			v = json.Number(outsideNumber(t.enum))
		}
		out = append(out, change{
			on: t.name, title: "with " + t.name + " outside its enum",
			apply: func(r *domain.APIRequest) {
				if s, ok := v.(json.Number); ok && !t.inBody {
					t.set(r, string(s))
					return
				}
				t.set(r, v)
			},
		})
	}
	return out
}

func outside(enum []string) string {
	v := notListed
	for slices.Contains(enum, v) {
		v += "x"
	}
	return v
}

func outsideNumber(enum []string) string {
	for n := 999999; ; n++ {
		if s := strconv.Itoa(n); !slices.Contains(enum, s) {
			return s
		}
	}
}

// unknownIDs changes the value of one path parameter that holds an ID to one that no record has. The
// segment of the request that stands for the parameter is found from the end of the path, so a prefix
// of the document does not matter.
func unknownIDs(op openapi.Operation, base domain.APIRequest) []change {
	tpl := segments(op.Path)
	got := segments(base.Path)
	if len(got) < len(tpl) {
		return nil
	}
	got = got[len(got)-len(tpl):]
	var out []change
	for i, seg := range tpl {
		name, ok := strings.CutSuffix(strings.TrimPrefix(seg, "{"), "}")
		if !ok || !strings.HasPrefix(seg, "{") {
			continue
		}
		p, _ := paramOf(op, name)
		if !idName.MatchString(name) && p.Format != "uuid" {
			continue
		}
		value := noSuchID
		switch {
		case p.Format == "uuid":
			value = noSuchUID
		case p.Type == "integer" || p.Type == "number":
			value = "999999999"
		}
		if got[i] == value {
			continue
		}
		at := len(segments(base.Path)) - len(tpl) + i
		out = append(out, change{
			on: name, title: "with an unknown " + name,
			apply: func(r *domain.APIRequest) {
				segs := segments(r.Path)
				segs[at] = value
				r.Path = "/" + strings.Join(segs, "/")
			},
		})
	}
	return out
}

func paramOf(op openapi.Operation, name string) (openapi.Param, bool) {
	for _, p := range op.Params {
		if p.In == "path" && p.Name == name {
			return p, true
		}
	}
	return openapi.Param{}, false
}

func segments(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
}

func noSession(openapi.Operation, domain.APIRequest) []change {
	return []change{{title: "with no session", noSession: true}}
}

func cloneRequest(r domain.APIRequest) domain.APIRequest {
	out := r
	out.Query = maps.Clone(r.Query)
	out.Headers = maps.Clone(r.Headers)
	out.JSON = cloneValue(r.JSON)
	return out
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if x == nil {
			return x
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = cloneValue(e)
		}
		return out
	case []any:
		if x == nil {
			return x
		}
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneValue(e)
		}
		return out
	}
	return v
}
