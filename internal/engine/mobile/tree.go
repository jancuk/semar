package mobile

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
)

// Rect is a box on the screen, in pixels. Right and Bottom are the first pixel outside.
type Rect struct{ Left, Top, Right, Bottom int }

func (r Rect) Width() int  { return r.Right - r.Left }
func (r Rect) Height() int { return r.Bottom - r.Top }

// Empty reports whether the box holds no pixel.
func (r Rect) Empty() bool { return r.Width() <= 0 || r.Height() <= 0 }

func (r Rect) Center() Point { return Point{(r.Left + r.Right) / 2, (r.Top + r.Bottom) / 2} }

// Intersect is the part that both boxes cover. It is empty when they do not touch.
func (r Rect) Intersect(o Rect) Rect {
	out := Rect{max(r.Left, o.Left), max(r.Top, o.Top), min(r.Right, o.Right), min(r.Bottom, o.Bottom)}
	if out.Empty() {
		return Rect{}
	}
	return out
}

// Node is one element of the native UI tree. A platform fills the fields it knows and calls
// Tree.Finish, which derives Text, Visible, and the links between the nodes.
type Node struct {
	// ID is the resource id on Android and the accessibility identifier on iOS.
	ID string
	// Desc is the content description on Android and the label on iOS.
	Desc string
	// Text is the own text of the node when the platform gives the node. Finish replaces it with the
	// text that a person reads in the node: its own, else that of the nodes inside it, else Desc.
	Text string
	Hint string
	// Class is the native class, such as android.widget.Button.
	Class string
	// Role is the platform-neutral role: button, textbox, checkbox, switch, radio, img, text,
	// heading, combobox, slider, progressbar, list, document, or empty.
	Role    string
	Package string

	Enabled, Checked, Selected, Focused, Password, Scrollable bool
	// Hidden is set when the platform says the node is not displayed.
	Hidden bool
	// Visible is derived by Finish: shown, with an area, on the screen, and not inside a hidden node.
	Visible bool
	Bounds  Rect
	// Attrs holds the native attributes by their native names, for attribute checks and xpath. A
	// password field has no text attribute.
	Attrs    map[string]string
	Parent   *Node
	Children []*Node
}

// Tree is what one read of the screen gave.
type Tree struct {
	Root *Node
	// Width and Height are the size of the screen. Zero when the platform does not say.
	Width, Height int
	// Digest tells two reads apart: equal digests mean that the screen did not change. A platform that
	// parses a document sets it to the digest of the bytes. Finish sets it when it is empty.
	Digest string
}

// Walk calls fn for every node in document order, and stops when fn returns false.
func (t *Tree) Walk(fn func(*Node) bool) {
	if t == nil || t.Root == nil {
		return
	}
	var walk func(*Node) bool
	walk = func(n *Node) bool {
		if !fn(n) {
			return false
		}
		for _, k := range n.Children {
			if !walk(k) {
				return false
			}
		}
		return true
	}
	walk(t.Root)
}

// Count is the number of nodes.
func (t *Tree) Count() int {
	n := 0
	t.Walk(func(*Node) bool { n++; return true })
	return n
}

// Finish links each node to its parent and derives what every platform shares:
//
//   - A password field has no text, and its text attribute is dropped.
//   - A hint that the field shows in place of its text is not text.
//   - The text of a node is its own text, else the text of the visible nodes inside it joined by a
//     space, else its Desc. Spaces are collapsed.
//   - A node is visible when it is not hidden, has an area, touches the screen (when the screen
//     size is known), and is inside no hidden node.
//
// Finish is safe to call again.
func (t *Tree) Finish() {
	if t == nil || t.Root == nil {
		return
	}
	t.finish(t.Root, nil, false)
	if t.Digest == "" {
		t.Digest = t.digest()
	}
}

func (t *Tree) finish(n, parent *Node, hiddenAbove bool) {
	n.Parent = parent
	own := collapse(n.Text)
	switch {
	case n.Password:
		own = ""
		delete(n.Attrs, "text")
	case n.Hint != "" && own == collapse(n.Hint):
		own = ""
	}
	hidden := hiddenAbove || n.Hidden
	n.Visible = !hidden && !n.Bounds.Empty()
	if n.Visible && t.Width > 0 && t.Height > 0 {
		n.Visible = !n.Bounds.Intersect(Rect{0, 0, t.Width, t.Height}).Empty()
	}
	var inner []string
	for _, k := range n.Children {
		t.finish(k, n, hidden)
		if k.Visible && k.Text != "" && !k.Password {
			inner = append(inner, k.Text)
		}
	}
	switch {
	case own != "":
		n.Text = own
	case len(inner) > 0:
		n.Text = strings.Join(inner, " ")
	default:
		n.Text = collapse(n.Desc)
	}
	if n.Password {
		n.Text = ""
	}
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// digest is a digest of what the nodes hold, for a tree that a platform did not digest.
func (t *Tree) digest() string {
	h := sha256.New()
	t.Walk(func(n *Node) bool {
		h.Write([]byte(strings.Join([]string{n.ID, n.Desc, n.Text, n.Hint, n.Class, n.Role,
			flags(n), itoa(n.Bounds.Left), itoa(n.Bounds.Top), itoa(n.Bounds.Right), itoa(n.Bounds.Bottom)}, "\x00") + "\x01"))
		for _, k := range slices.Sorted(mapKeys(n.Attrs)) {
			h.Write([]byte(k + "=" + n.Attrs[k] + "\x02"))
		}
		return true
	})
	return hex.EncodeToString(h.Sum(nil))
}

func mapKeys(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func flags(n *Node) string {
	var b [8]byte
	for i, f := range []bool{n.Enabled, n.Checked, n.Selected, n.Focused, n.Password, n.Scrollable, n.Hidden, n.Visible} {
		b[i] = '0'
		if f {
			b[i] = '1'
		}
	}
	return string(b[:])
}

func itoa(n int) string { return strconv.Itoa(n) }

// AccName is the name that a person or a screen reader gives the node: its Desc, else its text, else
// its hint.
func (n *Node) AccName() string {
	switch {
	case n.Desc != "":
		return collapse(n.Desc)
	case n.Text != "":
		return n.Text
	}
	return collapse(n.Hint)
}
