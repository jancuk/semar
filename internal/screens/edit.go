package screens

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
)

// Replace rewrites the locator of one element in the text of a screens file and keeps the rest of
// the file, its order and its comments. The new text is checked before it is returned.
func Replace(data []byte, app, screen, element string, loc domain.Locator) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("the screens file does not parse: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("the screens file is empty")
	}
	root := doc.Content[0]
	node := child(child(child(root, "screens"), screen), element)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("screen %s has no element %s in the screens file", screen, element)
	}
	// The new mapping keeps the old style, so a locator written on one line stays on one line.
	fresh := &yaml.Node{Kind: yaml.MappingNode, Style: node.Style, HeadComment: node.HeadComment, LineComment: node.LineComment, FootComment: node.FootComment}
	for _, f := range []struct{ key, val string }{
		{"testId", loc.TestID}, {"role", loc.Role}, {"name", loc.Name}, {"text", loc.Text}, {"css", loc.CSS}, {"within", loc.Within},
	} {
		if f.val == "" {
			continue
		}
		fresh.Content = append(fresh.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: f.key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: f.val, Style: quoteStyle(f.val)})
	}
	*node = *fresh

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if _, err := Parse(out, Path(app), app); err != nil {
		return nil, fmt.Errorf("the edited screens file is not valid: %w", err)
	}
	return out, nil
}

// quoteStyle double-quotes a value that a plain scalar would not carry through the parser unchanged.
func quoteStyle(v string) yaml.Style {
	if strings.ContainsAny(v, ":#{}[],&*!|>'\"%@`\n") || v != strings.TrimSpace(v) {
		return yaml.DoubleQuotedStyle
	}
	switch strings.ToLower(v) {
	case "true", "false", "null", "yes", "no", "on", "off", "~":
		return yaml.DoubleQuotedStyle
	}
	if strings.IndexFunc(v, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
		return yaml.DoubleQuotedStyle
	}
	return 0
}

func child(n *yaml.Node, key string) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// Repaired is the locator that a repair writes. A field of the candidate that equals what the old
// locator looked for, once its variables were filled in, keeps the text of the file, so a name such as
// {{title}} stays a variable. Every other field takes the candidate's value, and within stays as it was.
func Repaired(file, resolved, candidate domain.Locator) domain.Locator {
	keep := func(fileVal, resolvedVal, candVal string) string {
		if candVal == resolvedVal {
			return fileVal
		}
		return candVal
	}
	return domain.Locator{
		TestID: keep(file.TestID, resolved.TestID, candidate.TestID),
		Role:   keep(file.Role, resolved.Role, candidate.Role),
		Name:   keep(file.Name, resolved.Name, candidate.Name),
		Text:   keep(file.Text, resolved.Text, candidate.Text),
		CSS:    keep(file.CSS, resolved.CSS, candidate.CSS),
		Within: file.Within,
	}
}

// Change is one element that differs between two versions of a screens file.
type Change struct {
	Screen, Element string
	Kind            string // added, removed, or changed
	// SameRoleAndName is true for a changed element whose role and name are the same: only the way
	// of finding it moved, so the repair followed an element that moved.
	SameRoleAndName bool
	Old, New        domain.Locator
}

// Changes lists what a person has not seen of the screens file: the elements that differ from the
// version that was approved before, in order.
func Changes(old, cur domain.Screens) []Change {
	var out []Change
	names := slices.Concat(old.ScreenNames(), cur.ScreenNames())
	slices.Sort(names)
	names = slices.Compact(names)
	for _, sn := range names {
		elements := slices.Concat(old.ElementNames(sn), cur.ElementNames(sn))
		slices.Sort(elements)
		elements = slices.Compact(elements)
		for _, en := range elements {
			o, hadOld := old.Locator(sn, en)
			n, hasNew := cur.Locator(sn, en)
			c := Change{Screen: sn, Element: en, Old: o, New: n}
			switch {
			case !hadOld:
				c.Kind = "added"
			case !hasNew:
				c.Kind = "removed"
			case o == n:
				continue
			default:
				c.Kind = "changed"
				c.SameRoleAndName = o.Role == n.Role && o.Name == n.Name
			}
			out = append(out, c)
		}
	}
	return out
}
