package android

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/engine/mobile"
)

// Limits of the screen source. The server can be a farm that Semar does not control, and an app can draw
// a very large tree.
const (
	maxSourceBytes = 8 << 20
	maxNodes       = 20000
	maxDepth       = 200
	maxAttrBytes   = 4096
)

var boundsPattern = regexp.MustCompile(`^\[(-?\d{1,6}),(-?\d{1,6})\]\[(-?\d{1,6}),(-?\d{1,6})\]$`)

// ParseSource reads the XML that the UiAutomator2 server gives for /source into a finished tree. The tag
// of an element is its class, and the attributes are the native ones: text, content-desc, resource-id,
// bounds, enabled, checked, and the rest. It never panics: a document that is not XML, that is larger
// than 8 MiB, that holds more than 20,000 elements, or that is nested more than 200 deep is an error.
func ParseSource(src []byte) (*mobile.Tree, error) {
	if len(src) > maxSourceBytes {
		return nil, fmt.Errorf("the screen source is larger than %d MiB", maxSourceBytes>>20)
	}
	sum := sha256.Sum256(src)
	dec := xml.NewDecoder(bytes.NewReader(src))
	dec.Strict = false
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }

	tree := &mobile.Tree{Digest: hex.EncodeToString(sum[:])}
	var stack []*mobile.Node
	nodes := 0
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			if nodes > 0 {
				break // a source that was cut short still shows what it showed
			}
			return nil, errors.New("the screen source is not XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if nodes++; nodes > maxNodes {
				return nil, fmt.Errorf("the screen source has more than %d elements", maxNodes)
			}
			if len(stack) >= maxDepth {
				return nil, fmt.Errorf("the screen source is nested more than %d levels", maxDepth)
			}
			n := nodeOf(t)
			if len(stack) == 0 {
				if tree.Root != nil {
					return nil, errors.New("the screen source has more than one root")
				}
				tree.Root = n
				tree.Width, tree.Height = sizeOf(t)
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if tree.Root == nil {
		return nil, errors.New("the screen source holds no element")
	}
	if tree.Width <= 0 || tree.Height <= 0 {
		// The size of the screen is that of the largest box that a top-level element has.
		for _, k := range tree.Root.Children {
			tree.Width, tree.Height = max(tree.Width, k.Bounds.Right), max(tree.Height, k.Bounds.Bottom)
		}
	}
	tree.Finish()
	return tree, nil
}

// nodeOf reads one element. An empty attribute is left out of Attrs, which keeps a tree of thousands of
// nodes small.
func nodeOf(t xml.StartElement) *mobile.Node {
	n := &mobile.Node{Class: t.Name.Local, Attrs: map[string]string{}}
	for _, a := range t.Attr {
		name, val := a.Name.Local, a.Value
		if len(val) > maxAttrBytes {
			val = strings.ToValidUTF8(val[:maxAttrBytes], "")
		}
		if val == "" {
			continue
		}
		n.Attrs[name] = val
		switch name {
		case "class":
			n.Class = val
		case "text":
			n.Text = val
		case "content-desc":
			n.Desc = val
		case "resource-id":
			n.ID = val
		case "hint":
			n.Hint = val
		case "package":
			n.Package = val
		case "bounds":
			if m := boundsPattern.FindStringSubmatch(val); m != nil {
				n.Bounds = mobile.Rect{Left: atoi(m[1]), Top: atoi(m[2]), Right: atoi(m[3]), Bottom: atoi(m[4])}
			}
		case "enabled":
			n.Enabled = val == "true"
		case "checked":
			n.Checked = val == "true"
		case "selected":
			n.Selected = val == "true"
		case "focused":
			n.Focused = val == "true"
		case "password":
			n.Password = val == "true"
		case "scrollable":
			n.Scrollable = val == "true"
		case "displayed":
			n.Hidden = val == "false"
		}
	}
	n.Role = roleOf(n.Class, n.Attrs["heading"] == "true")
	return n
}

func atoi(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}

// sizeOf reads the size of the screen from the hierarchy element, which some servers give.
func sizeOf(t xml.StartElement) (w, h int) {
	for _, a := range t.Attr {
		switch a.Name.Local {
		case "width":
			w = atoi(a.Value)
		case "height":
			h = atoi(a.Value)
		}
	}
	return w, h
}

// roleOf maps an Android class to the role that locators and repairs use. A class that an app derives
// from a widget is read by its last word: AppCompatButton, MaterialButton, and TextInputEditText.
func roleOf(class string, heading bool) string {
	simple := class
	if i := strings.LastIndexAny(simple, ".$"); i >= 0 {
		simple = simple[i+1:]
	}
	switch {
	case strings.HasSuffix(simple, "ImageButton"), strings.HasSuffix(simple, "Button") && !strings.HasSuffix(simple, "RadioButton") && !strings.HasSuffix(simple, "ToggleButton"):
		return "button"
	case strings.HasSuffix(simple, "EditText"), simple == "AutoCompleteTextView", simple == "MultiAutoCompleteTextView":
		return "textbox"
	case strings.HasSuffix(simple, "CheckBox"):
		return "checkbox"
	case strings.HasSuffix(simple, "Switch"), strings.HasSuffix(simple, "SwitchCompat"), strings.HasSuffix(simple, "ToggleButton"):
		return "switch"
	case strings.HasSuffix(simple, "RadioButton"):
		return "radio"
	case strings.HasSuffix(simple, "ImageView"):
		return "img"
	case strings.HasSuffix(simple, "Spinner"):
		return "combobox"
	case strings.HasSuffix(simple, "SeekBar"):
		return "slider"
	case strings.HasSuffix(simple, "ProgressBar"):
		return "progressbar"
	case strings.HasSuffix(simple, "RecyclerView"), strings.HasSuffix(simple, "ListView"):
		return "list"
	case strings.HasSuffix(simple, "WebView"):
		return "document"
	case strings.HasSuffix(simple, "TextView"):
		if heading {
			return "heading"
		}
		return "text"
	}
	return ""
}
