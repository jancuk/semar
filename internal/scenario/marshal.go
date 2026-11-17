package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
)

// Marshal writes s as block YAML that Parse reads back to the same value, with the same expectation
// hash. It refuses a value that no YAML text can carry through Parse, and the error names the field.
// An empty apiVersion or kind is written as the value that Parse requires.
func Marshal(s domain.Scenario) ([]byte, error) {
	e := &enc{}
	if s.APIVersion == "" {
		s.APIVersion = apiVersion
	}
	if s.Kind == "" {
		s.Kind = kind
	}
	if s.APIVersion != apiVersion {
		return nil, fmt.Errorf("scenario apiVersion is %q, want %s", s.APIVersion, apiVersion)
	}
	if s.Kind != kind {
		return nil, fmt.Errorf("scenario kind is %q, want %s", s.Kind, kind)
	}

	want := s
	root := mapping()
	put(root, "apiVersion", e.str("apiVersion", s.APIVersion))
	put(root, "kind", e.str("kind", s.Kind))
	put(root, "id", e.str("id", string(s.ID)))
	put(root, "title", e.str("title", s.Title))
	put(root, "ticket", e.str("ticket", s.Ticket))
	put(root, "intent", e.str("intent", s.Intent))
	put(root, "surface", e.str("surface", string(s.Surface)))
	put(root, "authProfile", e.str("authProfile", s.AuthProfile))
	if len(s.Given) > 0 {
		given := sequence()
		want.Given = make([]domain.Given, len(s.Given))
		for i, g := range s.Given {
			var n *yaml.Node
			n, want.Given[i] = e.given(fmt.Sprintf("given[%d]", i), g)
			given.Content = append(given.Content, n)
		}
		put(root, "given", given)
	} else {
		want.Given = nil
	}
	if s.Steps != nil {
		steps := sequence()
		want.Steps = make([]domain.Step, len(s.Steps))
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
		return nil, fmt.Errorf("write scenario %s: %w", s.ID, err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("write scenario %s: %w", s.ID, err)
	}

	got, err := Parse(buf.Bytes(), "scenario "+string(s.ID))
	if err != nil {
		return nil, fmt.Errorf("scenario %s cannot be written as YAML that Parse accepts: %w", s.ID, err)
	}
	if !reflect.DeepEqual(got, want) {
		return nil, fmt.Errorf("scenario %s cannot be written: Parse would read it back as a different value", s.ID)
	}
	if domain.ExpectHash(got) != domain.ExpectHash(s) {
		return nil, fmt.Errorf("scenario %s cannot be written: its expectation hash would change", s.ID)
	}
	return buf.Bytes(), nil
}

// enc builds the YAML tree and keeps the first error. want, built beside it, is the value that
// Parse must give back.
type enc struct{ err error }

func (e *enc) fail(path, format string, args ...any) {
	if e.err == nil {
		e.err = fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...))
	}
}

func (e *enc) given(path string, g domain.Given) (*yaml.Node, domain.Given) {
	want := g
	n := mapping()
	put(n, "fixture", e.str(path+".fixture", string(g.Fixture)))
	put(n, "as", e.str(path+".as", g.As))
	want.With = nil
	if len(g.With) > 0 {
		with := mapping()
		for _, k := range slices.Sorted(maps.Keys(g.With)) {
			put2(with, e.str(path+" with key "+strconv.Quote(k), k), e.str(path+".with."+k, g.With[k]))
		}
		put(n, "with", with)
		want.With = maps.Clone(g.With)
	}
	if g.Lease != "" {
		put(n, "lease", e.str(path+".lease", string(g.Lease)))
	}
	return n, want
}

func (e *enc) step(path string, st domain.Step) (*yaml.Node, domain.Step) {
	want := st
	n := mapping()
	put(n, "id", e.str(path+".id", string(st.ID)))
	if st.Setup {
		put(n, "setup", plain("true"))
	}
	put(n, "action", e.str(path+".action", st.Action))
	if st.As != "" {
		put(n, "as", e.str(path+".as", st.As))
	}
	if st.Hint != nil {
		h := mapping()
		put(h, "method", e.str(path+".hint.method", st.Hint.Method))
		put(h, "path", e.str(path+".hint.path", st.Hint.Path))
		put(n, "hint", h)
	}
	want.Expect = nil
	if len(st.Expect) > 0 {
		seq := sequence()
		want.Expect = make([]domain.Expectation, len(st.Expect))
		for i, ex := range st.Expect {
			p := fmt.Sprintf("%s.expect[%d]", path, i)
			x := mapping()
			var c *yaml.Node
			c, want.Expect[i] = e.check(p+".check", ex)
			put(x, "check", c)
			put(x, "source", e.str(p+".source", string(ex.Source)))
			if ex.Cite != "" {
				put(x, "cite", e.str(p+".cite", ex.Cite))
			}
			if ex.By != "" {
				put(x, "by", e.str(p+".by", ex.By))
			}
			seq.Content = append(seq.Content, x)
		}
		put(n, "expect", seq)
	}
	return n, want
}

func (e *enc) check(path string, ex domain.Expectation) (*yaml.Node, domain.Expectation) {
	c := ex.Check
	want := ex
	n := mapping()
	if c.Status != nil {
		put(n, "status", plain(strconv.Itoa(*c.Status)))
	}
	if c.JSONPath != "" {
		put(n, "jsonpath", e.str(path+".jsonpath", c.JSONPath))
	}
	if c.Header != "" {
		put(n, "header", e.str(path+".header", c.Header))
	}
	if c.Element != "" {
		put(n, "element", e.str(path+".element", c.Element))
	}
	operand := func(key string, v *any) {
		if *v == nil {
			return
		}
		node, norm := e.value(path+"."+key, *v, 0)
		put(n, key, node)
		*v = norm
	}
	operand("equals", &want.Check.Equals)
	operand("notEquals", &want.Check.NotEquals)
	operand("contains", &want.Check.Contains)
	if c.Exists != nil {
		put(n, "exists", plain(strconv.FormatBool(*c.Exists)))
	}
	for _, b := range []struct {
		key string
		val *float64
	}{{"greater", c.Greater}, {"less", c.Less}} {
		if b.val == nil {
			continue
		}
		if math.IsNaN(*b.val) || math.IsInf(*b.val, 0) {
			e.fail(path+"."+b.key, "%v is not a finite number", *b.val)
			continue
		}
		put(n, b.key, plain(floatText(*b.val, false)))
	}
	if c.Count != nil {
		put(n, "count", plain(strconv.Itoa(*c.Count)))
	}
	if c.EqualsPath != "" {
		put(n, "equalsPath", e.str(path+".equalsPath", c.EqualsPath))
	}
	return n, want
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
		return plain(floatText(x, true)), x
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

// number writes a JSON number as its own text. The expectation hash covers the number as
// encoding/json writes the value that Parse reads, so a text that differs from it is refused.
func (e *enc) number(path string, n json.Number) (*yaml.Node, any) {
	text := string(n)
	if !jsonNumber.MatchString(text) {
		e.fail(path, "%q is not a JSON number", text)
		return plain("null"), nil
	}
	var value any
	if !strings.ContainsAny(text, ".eE") {
		if i, err := strconv.ParseInt(text, 10, 64); err == nil {
			value = int(i)
		} else if u, err := strconv.ParseUint(text, 10, 64); err == nil {
			value = u
		} else {
			e.fail(path, "%s is a whole number too wide to be held exactly", text)
			return plain("null"), nil
		}
	} else {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(f, 0) {
			e.fail(path, "%s is outside the range of a number", text)
			return plain("null"), nil
		}
		if decimalKey(text) != decimalKey(strconv.FormatFloat(f, 'e', -1, 64)) {
			e.fail(path, "%s cannot be held exactly: it would be read as %s", text, strconv.FormatFloat(f, 'g', -1, 64))
			return plain("null"), nil
		}
		value = f
	}
	if stored, err := json.Marshal(value); err != nil || string(stored) != text {
		e.fail(path, "the number %s is stored as %s, which changes the expectation hash: write %s", text, stored, stored)
		return plain("null"), nil
	}
	return plain(text), value
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
