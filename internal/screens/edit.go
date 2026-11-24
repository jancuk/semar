package screens

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
)

// Replace rewrites the locator of one element in the text of a screens file. Only the text of that
// element changes: the rest of the file stays as it is, byte for byte, with its order, its comments,
// and its layout. The new text is checked before it is returned.
func Replace(data []byte, app, screen, element string, loc domain.Locator) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("the screens file does not parse: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("the screens file is empty")
	}
	node := child(child(child(doc.Content[0], "screens"), screen), element)
	if node == nil {
		node = child(child(doc.Content[0], "common"), element)
	}
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("screen %s has no element %s in the screens file", screen, element)
	}
	start, err := offsetOf(data, node.Line, node.Column)
	if err != nil {
		return nil, err
	}
	var from, to int
	var text string
	if node.Style&yaml.FlowStyle != 0 {
		end, err := flowEnd(data, start)
		if err != nil {
			return nil, err
		}
		from, to, text = start, end, flowText(loc)
	} else {
		from, to, text = start, blockEnd(data, node), blockText(loc, node.Column-1)
	}
	out := append(append(append([]byte{}, data[:from]...), text...), data[to:]...)
	if _, err := Parse(out, Path(app), app); err != nil {
		return nil, fmt.Errorf("the edited screens file is not valid: %w", err)
	}
	return out, nil
}

// pairs lists the fields of a locator that are set, in the order that a file writes them.
func pairs(loc domain.Locator) [][2]string {
	var out [][2]string
	for _, f := range [][2]string{
		{"testId", loc.TestID}, {"role", loc.Role}, {"name", loc.Name}, {"text", loc.Text}, {"css", loc.CSS},
		{"id", loc.ID}, {"desc", loc.Desc}, {"class", loc.Class}, {"xpath", loc.XPath}, {"within", loc.Within},
		{"hasText", loc.HasText},
	} {
		if f[1] != "" {
			out = append(out, f)
		}
	}
	if loc.Index != 0 {
		out = append(out, [2]string{"index", strconv.Itoa(loc.Index)})
	}
	return out
}

func scalar(v string) string {
	if quoteStyle(v) != 0 {
		return strconv.Quote(v)
	}
	return v
}

// pairText is a field of a locator as the file writes it. The index is a number and stays unquoted: a
// quoted index does not decode into one.
func pairText(p [2]string) string {
	if p[0] == "index" {
		return p[0] + ": " + p[1]
	}
	return p[0] + ": " + scalar(p[1])
}

func flowText(loc domain.Locator) string {
	var parts []string
	for _, p := range pairs(loc) {
		parts = append(parts, pairText(p))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func blockText(loc domain.Locator, indent int) string {
	var lines []string
	for i, p := range pairs(loc) {
		line := pairText(p)
		if i > 0 {
			line = strings.Repeat(" ", indent) + line
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// offsetOf is the byte offset of a position that the parser gives as a line and a column, both from 1.
func offsetOf(data []byte, line, column int) (int, error) {
	off := 0
	for l := 1; l < line; l++ {
		i := bytes.IndexByte(data[off:], '\n')
		if i < 0 {
			return 0, errors.New("the position of the element is not in the file")
		}
		off += i + 1
	}
	for c := 1; c < column; c++ {
		_, n := utf8.DecodeRune(data[off:])
		if n == 0 {
			return 0, errors.New("the position of the element is not in the file")
		}
		off += n
	}
	return off, nil
}

// flowEnd is the offset after the } that closes the { at start. A brace inside a quoted value does not count.
func flowEnd(data []byte, start int) (int, error) {
	if start >= len(data) || data[start] != '{' {
		return 0, errors.New("the element is not written as { ... }")
	}
	depth := 0
	for i := start; i < len(data); i++ {
		switch c := data[i]; c {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i + 1, nil
			}
		case '"':
			for i++; i < len(data) && data[i] != '"'; i++ {
				if data[i] == '\\' {
					i++
				}
			}
		case '\'':
			for i++; i < len(data); i++ {
				if data[i] == '\'' {
					if i+1 < len(data) && data[i+1] == '\'' {
						i++
						continue
					}
					break
				}
			}
		}
	}
	return 0, errors.New("the element has no closing }")
}

// blockEnd is the offset at the end of the last line of a block mapping.
func blockEnd(data []byte, n *yaml.Node) int {
	last := n.Line
	for _, c := range n.Content {
		last = max(last, c.Line)
	}
	off := 0
	for l := 1; l <= last; l++ {
		i := bytes.IndexByte(data[off:], '\n')
		if i < 0 {
			return len(data)
		}
		off += i + 1
	}
	return off - 1
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
		ID:     keep(file.ID, resolved.ID, candidate.ID),
		Desc:   keep(file.Desc, resolved.Desc, candidate.Desc),
		Class:  keep(file.Class, resolved.Class, candidate.Class),
		XPath:  keep(file.XPath, resolved.XPath, candidate.XPath),
		Within: file.Within,
		// The index is the candidate's own, and a text to filter by is kept like the other fields.
		HasText: keep(file.HasText, resolved.HasText, candidate.HasText),
		Index:   candidate.Index,
	}
}
