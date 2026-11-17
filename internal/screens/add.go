package screens

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
)

// Element is a named locator of a screen.
type Element struct {
	Name    string
	Locator domain.Locator
}

// Addition is the elements to put on one screen, in order. The screen may be new.
type Addition struct {
	Screen   string
	Elements []Element
}

// New is the text of a screens file that holds only the additions.
func New(app string, adds []Addition) ([]byte, error) {
	var b strings.Builder
	b.WriteString("apiVersion: semar/v1\nkind: Screens\napp: " + app + "\nscreens:\n")
	for _, a := range adds {
		b.WriteString("  " + a.Screen + ":\n")
		for _, e := range a.Elements {
			b.WriteString("    " + e.Name + ": " + flowText(e.Locator) + "\n")
		}
	}
	out := []byte(b.String())
	if _, err := Parse(out, Path(app), app); err != nil {
		return nil, fmt.Errorf("the new screens file is not valid: %w", err)
	}
	return out, nil
}

// Add puts the elements in the text of a screens file. An element goes below the last element of its
// screen, and a new screen goes below the last screen. The rest of the file stays as it is, byte for
// byte, with its order, its comments, and its layout. An element that the screen has is an error:
// the caller adds only what is new. The new text is checked before it is returned.
func Add(data []byte, app string, adds []Addition) ([]byte, error) {
	out := data
	for _, a := range adds {
		var err error
		if out, err = addOne(out, a); err != nil {
			return nil, err
		}
	}
	if _, err := Parse(out, Path(app), app); err != nil {
		return nil, fmt.Errorf("the screens file with the new elements is not valid: %w", err)
	}
	return out, nil
}

func addOne(data []byte, a Addition) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("the screens file does not parse: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, errors.New("the screens file is empty")
	}
	screens := child(doc.Content[0], "screens")
	if screens == nil || screens.Kind != yaml.MappingNode || len(screens.Content) < 2 || screens.Style&yaml.FlowStyle != 0 {
		return nil, errors.New("the screens of the file are not written as a list of screens that Semar can add to: add the elements by hand")
	}
	nl := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		nl = "\r\n"
	}

	var at int
	var text strings.Builder
	if sn := child(screens, a.Screen); sn != nil {
		if sn.Kind != yaml.MappingNode || sn.Style&yaml.FlowStyle != 0 || len(sn.Content) < 2 {
			return nil, fmt.Errorf("screen %s is not written as a list of elements that Semar can add to: add the elements by hand", a.Screen)
		}
		for _, e := range a.Elements {
			if child(sn, e.Name) != nil {
				return nil, fmt.Errorf("screen %s already has the element %s", a.Screen, e.Name)
			}
		}
		end, err := valueEnd(data, sn.Content[len(sn.Content)-1])
		if err != nil {
			return nil, err
		}
		at = lineEnd(data, end, nl)
		indent := strings.Repeat(" ", sn.Content[0].Column-1)
		for _, e := range a.Elements {
			text.WriteString(nl + indent + e.Name + ": " + flowText(e.Locator))
		}
	} else {
		last := screens.Content[len(screens.Content)-1]
		screenIndent := screens.Content[len(screens.Content)-2].Column - 1
		elementIndent := screenIndent + 2
		if last.Kind == yaml.MappingNode && len(last.Content) > 0 {
			elementIndent = last.Content[0].Column - 1
		}
		end, err := valueEnd(data, last)
		if err != nil {
			return nil, err
		}
		at = lineEnd(data, end, nl)
		text.WriteString(nl + strings.Repeat(" ", screenIndent) + a.Screen + ":")
		for _, e := range a.Elements {
			text.WriteString(nl + strings.Repeat(" ", elementIndent) + e.Name + ": " + flowText(e.Locator))
		}
	}
	return append(append(append([]byte{}, data[:at]...), text.String()...), data[at:]...), nil
}

// valueEnd is the offset just after the last character of a value.
func valueEnd(data []byte, n *yaml.Node) (int, error) {
	if n.Kind == yaml.MappingNode && n.Style&yaml.FlowStyle != 0 {
		start, err := offsetOf(data, n.Line, n.Column)
		if err != nil {
			return 0, err
		}
		return flowEnd(data, start)
	}
	return lineEnd(data, offsetAfterLine(data, lastLine(n)), "\n"), nil
}

// lastLine is the greatest line that the node or anything below it starts on.
func lastLine(n *yaml.Node) int {
	last := n.Line
	for _, c := range n.Content {
		last = max(last, lastLine(c))
	}
	return last
}

// offsetAfterLine is the offset of the start of the line that follows the given one, or the end of the data.
func offsetAfterLine(data []byte, line int) int {
	off := 0
	for l := 1; l <= line; l++ {
		i := bytes.IndexByte(data[off:], '\n')
		if i < 0 {
			return len(data)
		}
		off += i + 1
	}
	return off
}

// lineEnd is the offset where the line that holds off ends, before its line break.
func lineEnd(data []byte, off int, nl string) int {
	if off > 0 && off <= len(data) && data[off-1] == '\n' {
		// off is the start of the next line: the line that ended there is the one that is meant.
		off--
		if nl == "\r\n" && off > 0 && data[off-1] == '\r' {
			off--
		}
		return off
	}
	i := bytes.IndexByte(data[min(off, len(data)):], '\n')
	if i < 0 {
		return len(data)
	}
	end := off + i
	if nl == "\r\n" && end > 0 && data[end-1] == '\r' {
		end--
	}
	return end
}
