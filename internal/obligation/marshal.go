package obligation

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v4"
)

// Marshal writes f as block YAML that Parse reads back to the same file. Every text goes
// through the YAML encoder, so a text cannot add a key. It refuses a text that is not UTF-8.
func Marshal(f File) ([]byte, error) {
	var bad error
	str := func(path, s string) *yaml.Node {
		if !utf8.ValidString(s) {
			if bad == nil {
				bad = fmt.Errorf("%s: the text is not valid UTF-8", path)
			}
			return scalar("", yaml.DoubleQuotedStyle)
		}
		if plainSafe(s) {
			return scalar(s, 0)
		}
		return scalar(s, yaml.DoubleQuotedStyle)
	}
	strs := func(path string, items []string, flow bool) *yaml.Node {
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		if flow || len(items) == 0 {
			seq.Style = yaml.FlowStyle
		}
		for i, s := range items {
			seq.Content = append(seq.Content, str(fmt.Sprintf("%s[%d]", path, i), s))
		}
		return seq
	}

	root := &yaml.Node{Kind: yaml.MappingNode}
	put(root, "apiVersion", str("apiVersion", f.APIVersion))
	put(root, "kind", str("kind", f.Kind))
	if f.Ticket != "" {
		put(root, "ticket", str("ticket", f.Ticket))
	}
	list := &yaml.Node{Kind: yaml.SequenceNode}
	if len(f.Obligations) == 0 {
		list.Style = yaml.FlowStyle
	}
	for i, o := range f.Obligations {
		p := fmt.Sprintf("obligations[%d]", i)
		n := &yaml.Node{Kind: yaml.MappingNode}
		put(n, "id", str(p+".id", o.ID))
		put(n, "title", str(p+".title", o.Title))
		put(n, "source", str(p+".source", string(o.Source)))
		if o.Cite != "" {
			put(n, "cite", str(p+".cite", o.Cite))
		}
		if o.Ticket != "" {
			put(n, "ticket", str(p+".ticket", o.Ticket))
		}
		if len(o.Covers) > 0 {
			covers := make([]string, len(o.Covers))
			for j, c := range o.Covers {
				covers[j] = string(c)
			}
			put(n, "covers", strs(p+".covers", covers, true))
		}
		if c := o.Criterion; c != nil {
			cn := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
			put(cn, "number", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(c.Number)})
			put(cn, "key", str(p+".criterion.key", c.Key))
			put(n, "criterion", cn)
		}
		if len(o.Journeys) > 0 {
			put(n, "journeys", strs(p+".journeys", o.Journeys, true))
		}
		if o.Gap != "" {
			put(n, "gap", str(p+".gap", o.Gap))
		}
		if m := o.Manual; m != nil {
			mn := &yaml.Node{Kind: yaml.MappingNode}
			put(mn, "kind", str(p+".manual.kind", m.Kind))
			if m.Surface != "" {
				put(mn, "surface", str(p+".manual.surface", m.Surface))
			}
			put(mn, "text", str(p+".manual.text", m.Text))
			put(mn, "steps", strs(p+".manual.steps", m.Steps, false))
			put(mn, "expected", str(p+".manual.expected", m.Expected))
			put(mn, "writtenBy", str(p+".manual.writtenBy", m.WrittenBy))
			put(n, "manual", mn)
		}
		if len(o.Retired) > 0 {
			rs := &yaml.Node{Kind: yaml.SequenceNode}
			for j, r := range o.Retired {
				rp := fmt.Sprintf("%s.retired[%d]", p, j)
				rn := &yaml.Node{Kind: yaml.MappingNode}
				put(rn, "scenario", str(rp+".scenario", string(r.Scenario)))
				put(rn, "reason", str(rp+".reason", r.Reason))
				put(rn, "user", str(rp+".user", r.User))
				put(rn, "day", str(rp+".day", r.Day))
				rs.Content = append(rs.Content, rn)
			}
			put(n, "retired", rs)
		}
		list.Content = append(list.Content, n)
	}
	put(root, "obligations", list)
	if bad != nil {
		return nil, bad
	}

	var buf bytes.Buffer
	w := yaml.NewEncoder(&buf)
	w.SetIndent(2)
	if err := w.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}); err != nil {
		return nil, fmt.Errorf("write obligations: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("write obligations: %w", err)
	}

	got, err := Parse(buf.Bytes(), "obligations")
	if err != nil {
		return nil, fmt.Errorf("the obligations cannot be written as YAML that Parse accepts: %w", err)
	}
	if !sameFile(got, f) {
		return nil, fmt.Errorf("the obligations cannot be written: Parse would read them back as different values")
	}
	return buf.Bytes(), nil
}

func scalar(s string, style yaml.Style) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: s, Style: style}
}

func put(n *yaml.Node, key string, v *yaml.Node) {
	n.Content = append(n.Content, scalar(key, 0), v)
}

var plainText = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 _./()-]*$`)

var reserved = []string{"true", "false", "null", "yes", "no", "on", "off", "y", "n", "inf", "infinity", "nan"}

// plainSafe holds only text that every YAML reader takes as a string.
func plainSafe(s string) bool {
	return plainText.MatchString(s) && !strings.HasSuffix(s, " ") && !slices.Contains(reserved, strings.ToLower(s))
}

func sameFile(a, b File) bool {
	if a.APIVersion != b.APIVersion || a.Kind != b.Kind || a.Ticket != b.Ticket || len(a.Obligations) != len(b.Obligations) {
		return false
	}
	for i := range a.Obligations {
		if !sameObligation(a.Obligations[i], b.Obligations[i]) {
			return false
		}
	}
	return true
}

func sameObligation(a, b Obligation) bool {
	if a.ID != b.ID || a.Title != b.Title || a.Source != b.Source || a.Cite != b.Cite || a.Ticket != b.Ticket || a.Gap != b.Gap || !equalRef(a.Criterion, b.Criterion) ||
		!slices.Equal(a.Covers, b.Covers) || !slices.Equal(a.Retired, b.Retired) || !slices.Equal(a.Journeys, b.Journeys) || (a.Manual == nil) != (b.Manual == nil) {
		return false
	}
	if a.Manual == nil {
		return true
	}
	x, y := *a.Manual, *b.Manual
	return x.Kind == y.Kind && x.Surface == y.Surface && x.Text == y.Text && x.Expected == y.Expected && x.WrittenBy == y.WrittenBy && slices.Equal(x.Steps, y.Steps)
}

func equalRef(a, b *CriterionRef) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
