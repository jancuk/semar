package script

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
)

const (
	apiVersion = "semar/v1"
	kind       = "Script"
	maxDepth   = 200
)

// Marshal writes s as block YAML that Parse reads back to the same value. It refuses a value that
// no YAML text can carry through Parse, and the error names the field. An empty apiVersion or kind
// is written as the value that Parse requires. A request body holds JSON values: a json.Number is
// written as its own text, and a number that no float64 or integer holds exactly is refused.
func Marshal(s domain.Script) ([]byte, error) {
	e := &enc{}
	if s.APIVersion == "" {
		s.APIVersion = apiVersion
	}
	if s.Kind == "" {
		s.Kind = kind
	}
	if s.APIVersion != apiVersion {
		return nil, fmt.Errorf("script apiVersion is %q, want %s", s.APIVersion, apiVersion)
	}
	if s.Kind != kind {
		return nil, fmt.Errorf("script kind is %q, want %s", s.Kind, kind)
	}

	want := s
	root := mapping()
	put(root, "apiVersion", e.str("apiVersion", s.APIVersion))
	put(root, "kind", e.str("kind", s.Kind))
	put(root, "scenario", e.str("scenario", string(s.Scenario)))
	put(root, "expectHash", e.quoted("expectHash", string(s.ExpectHash)))
	if s.ShapeHash != "" {
		put(root, "shapeHash", e.quoted("shapeHash", string(s.ShapeHash)))
	}
	if !s.StoredAt.IsZero() {
		s.StoredAt = s.StoredAt.UTC()
		want.StoredAt = s.StoredAt
		put(root, "storedAt", plain(s.StoredAt.Format(time.RFC3339Nano)))
	}
	ref := mapping()
	put(ref, "instance", e.str("authoredBy.instance", s.AuthoredBy.Instance))
	put(ref, "model", e.str("authoredBy.model", s.AuthoredBy.Model))
	put(ref, "harness", e.str("authoredBy.harness", s.AuthoredBy.Harness))
	put(root, "authoredBy", ref)
	if s.Steps != nil {
		steps := sequence()
		want.Steps = make([]domain.ScriptStep, len(s.Steps))
		for i, st := range s.Steps {
			var n *yaml.Node
			n, want.Steps[i] = e.step(fmt.Sprintf("steps[%d]", i), st)
			steps.Content = append(steps.Content, n)
		}
		put(root, "steps", steps)
	}
	if e.err != nil {
		return nil, e.err
	}

	var buf bytes.Buffer
	w := yaml.NewEncoder(&buf)
	w.SetIndent(2)
	if err := w.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}); err != nil {
		return nil, fmt.Errorf("write script %s: %w", s.Scenario, err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("write script %s: %w", s.Scenario, err)
	}

	got, err := Parse(buf.Bytes(), "script "+string(s.Scenario))
	if err != nil {
		return nil, fmt.Errorf("script %s cannot be written as YAML that Parse accepts: %w", s.Scenario, err)
	}
	if !reflect.DeepEqual(got, want) {
		return nil, fmt.Errorf("script %s cannot be written: Parse would read it back as a different value", s.Scenario)
	}
	return buf.Bytes(), nil
}

// enc builds the YAML tree and keeps the first error.
type enc struct{ err error }

func (e *enc) fail(path, format string, args ...any) {
	if e.err == nil {
		e.err = fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...))
	}
}

func (e *enc) step(path string, st domain.ScriptStep) (*yaml.Node, domain.ScriptStep) {
	want := st
	n := mapping()
	put(n, "step", e.str(path+".step", string(st.Step)))
	if st.Request != nil {
		r := *st.Request
		rn := mapping()
		put(rn, "service", e.str(path+".request.service", r.Service))
		put(rn, "method", e.str(path+".request.method", r.Method))
		put(rn, "path", e.str(path+".request.path", r.Path))
		r.Query = e.strings(path+".request.query", rn, "query", r.Query)
		r.Headers = e.strings(path+".request.headers", rn, "headers", r.Headers)
		if r.JSON != nil {
			node, norm := e.value(path+".request.json", r.JSON, 0)
			put(rn, "json", node)
			r.JSON = norm
		}
		put(n, "request", rn)
		want.Request = &r
	}
	if st.Web != nil {
		w := *st.Web
		wn := mapping()
		for _, f := range []struct{ key, val string }{
			{"service", w.Service}, {"app", w.App}, {"do", string(w.Do)}, {"path", w.Path},
			{"screen", w.Screen}, {"element", w.Element}, {"to", w.To}, {"value", w.Value}, {"secret", w.Secret},
		} {
			if f.val != "" {
				put(wn, f.key, e.str(path+".web."+f.key, f.val))
			}
		}
		put(n, "web", wn)
		want.Web = &w
	}
	want.Captures = nil
	if len(st.Captures) > 0 {
		seq := sequence()
		want.Captures = st.Captures
		for i, c := range st.Captures {
			p := fmt.Sprintf("%s.captures[%d]", path, i)
			cn := mapping()
			put(cn, "name", e.str(p+".name", c.Name))
			if c.JSONPath != "" {
				put(cn, "jsonpath", e.str(p+".jsonpath", c.JSONPath))
			}
			if c.Element != "" {
				put(cn, "element", e.str(p+".element", c.Element))
			}
			seq.Content = append(seq.Content, cn)
		}
		put(n, "captures", seq)
	}
	return n, want
}

// strings writes a map of text values under key, with its keys sorted, and returns what Parse reads back.
func (e *enc) strings(path string, parent *yaml.Node, key string, m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	n := mapping()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		put2(n, e.str(path+" key "+strconv.Quote(k), k), e.str(path+"."+k, m[k]))
	}
	put(parent, key, n)
	return m
}

func (e *enc) quoted(path, s string) *yaml.Node {
	n := e.str(path, s)
	if n.Style == 0 {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

// value returns the node for v and the value that Parse gives back for it.
func (e *enc) value(path string, v any, depth int) (*yaml.Node, any) {
	if depth > maxDepth {
		e.fail(path, "nested more than %d levels", maxDepth)
		return plain("null"), nil
	}
	switch x := v.(type) {
	case nil:
		return plain("null"), nil
	case bool:
		return plain(strconv.FormatBool(x)), x
	case string:
		return e.str(path, x), x
	case int:
		return plain(strconv.Itoa(x)), x
	case int64:
		return plain(strconv.FormatInt(x, 10)), int(x)
	case uint64:
		if x <= math.MaxInt64 {
			return plain(strconv.FormatUint(x, 10)), int(x)
		}
		return plain(strconv.FormatUint(x, 10)), x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			e.fail(path, "%v is not a finite number", x)
			return plain("null"), nil
		}
		return plain(floatText(x)), x
	case json.Number:
		return e.number(path, x)
	case map[string]any:
		if x == nil {
			e.fail(path, "a nil object cannot be told from null")
			return plain("null"), nil
		}
		n, out := mapping(), make(map[string]any, len(x))
		if len(x) == 0 {
			n.Style = yaml.FlowStyle
		}
		for _, k := range sortedKeys(x) {
			node, norm := e.value(path+"."+k, x[k], depth+1)
			put2(n, e.str(path+" key "+strconv.Quote(k), k), node)
			out[k] = norm
		}
		return n, out
	case []any:
		if x == nil {
			e.fail(path, "a nil list cannot be told from null")
			return plain("null"), nil
		}
		n, out := sequence(), make([]any, len(x))
		if len(x) == 0 {
			n.Style = yaml.FlowStyle
		}
		for i, el := range x {
			node, norm := e.value(fmt.Sprintf("%s[%d]", path, i), el, depth+1)
			n.Content = append(n.Content, node)
			out[i] = norm
		}
		return n, out
	}
	e.fail(path, "a value of type %T cannot be written", v)
	return plain("null"), nil
}

var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// number writes a JSON number as its own text when YAML reads that text as the same number.
// A whole number is written in its plain form, so "-0" becomes 0.
func (e *enc) number(path string, n json.Number) (*yaml.Node, any) {
	text := string(n)
	if !jsonNumber.MatchString(text) {
		e.fail(path, "%q is not a JSON number", text)
		return plain("null"), nil
	}
	if !strings.ContainsAny(text, ".eE") {
		if i, err := strconv.ParseInt(text, 10, 64); err == nil {
			return plain(strconv.FormatInt(i, 10)), int(i)
		}
		if u, err := strconv.ParseUint(text, 10, 64); err == nil {
			return plain(strconv.FormatUint(u, 10)), u
		}
		e.fail(path, "%s is a whole number too wide to be held exactly", text)
		return plain("null"), nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(f, 0) {
		e.fail(path, "%s is outside the range of a number", text)
		return plain("null"), nil
	}
	if decimalKey(text) != decimalKey(strconv.FormatFloat(f, 'e', -1, 64)) {
		e.fail(path, "%s cannot be held exactly: it would be read as %s", text, strconv.FormatFloat(f, 'g', -1, 64))
		return plain("null"), nil
	}
	return plain(text), f
}

var decimalParts = regexp.MustCompile(`^(-?)([0-9]+)(?:\.([0-9]*))?(?:[eE]([-+]?[0-9]+))?$`)

// decimalKey names the value of a decimal text so that two texts of one value have one key.
func decimalKey(text string) string {
	m := decimalParts.FindStringSubmatch(text)
	if m == nil {
		return "?" + text
	}
	exp := 0
	if m[4] != "" {
		n, err := strconv.Atoi(m[4])
		if err != nil {
			return "?" + text
		}
		exp = n
	}
	digits := strings.TrimLeft(m[2]+m[3], "0")
	exp -= len(m[3])
	trimmed := strings.TrimRight(digits, "0")
	exp += len(digits) - len(trimmed)
	if trimmed == "" {
		return "0"
	}
	return m[1] + trimmed + "e" + strconv.Itoa(exp)
}

// floatText is the shortest decimal that reads back as f. A text that has no point or exponent
// gets ".0" so that YAML reads a float and not an integer.
func floatText(f float64) string {
	var s string
	if a := math.Abs(f); a == 0 || (a >= 1e-6 && a < 1e21) {
		s = strconv.FormatFloat(f, 'f', -1, 64)
	} else {
		s = strconv.FormatFloat(f, 'e', -1, 64)
		if i := strings.IndexByte(s, 'e'); i >= 0 {
			exp := strings.TrimLeft(s[i+2:], "0")
			if exp == "" {
				exp = "0"
			}
			s = s[:i+2] + exp
		}
	}
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

var reserved = []string{"true", "false", "null", "yes", "no", "on", "off", "y", "n", "inf", "infinity", "nan"}

// plainSafe holds only text that every YAML reader takes as a string: it cannot be read as a
// number, a boolean, a null, a date, or a mapping or list marker, and it has no ": " or " #".
func plainSafe(s string) bool {
	if s == "" || s[len(s)-1] == ' ' || slices.Contains(reserved, strings.ToLower(s)) {
		return false
	}
	for i, r := range s {
		switch {
		case unicode.IsLetter(r):
		case i > 0 && (unicode.IsDigit(r) || strings.ContainsRune(" _-./()+$'@%", r)):
		case i == 0 && r == '/':
		default:
			return false
		}
	}
	return true
}

func multiline(s string) bool {
	if !strings.Contains(s, "\n") || s[0] == ' ' || s[0] == '\n' || strings.Contains(s, " \n") {
		return false
	}
	for _, r := range s {
		if r != '\n' && !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func (e *enc) str(path, s string) *yaml.Node {
	if !utf8.ValidString(s) {
		e.fail(path, "the text is not valid UTF-8")
		return plain("")
	}
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: s, Style: yaml.DoubleQuotedStyle}
	switch {
	case plainSafe(s):
		n.Style = 0
	case multiline(s):
		n.Style = yaml.LiteralStyle
	}
	return n
}

func plain(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: s} }

func mapping() *yaml.Node  { return &yaml.Node{Kind: yaml.MappingNode} }
func sequence() *yaml.Node { return &yaml.Node{Kind: yaml.SequenceNode} }

func put(n *yaml.Node, key string, v *yaml.Node) { put2(n, plain(key), v) }
func put2(n, k, v *yaml.Node)                    { n.Content = append(n.Content, k, v) }

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
