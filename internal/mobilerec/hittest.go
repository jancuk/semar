package mobilerec

import (
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/engine/mobile"
)

// clickableRoles are the roles of a node that a person taps even when the app did not mark it clickable.
var clickableRoles = []string{"button", "checkbox", "switch", "radio", "combobox", "link", "tab", "menuitem"}

// Clickable reports whether the node takes a tap.
func Clickable(n *mobile.Node) bool {
	if n == nil {
		return false
	}
	if n.Attrs["clickable"] == "true" || n.Attrs["long-clickable"] == "true" {
		return true
	}
	return slices.Contains(clickableRoles, n.Role)
}

// Editable reports whether the node takes text.
func Editable(n *mobile.Node) bool {
	if n == nil {
		return false
	}
	return n.Role == "textbox" || n.Attrs["editable"] == "true" || strings.HasSuffix(n.Class, "EditText") || strings.HasSuffix(n.Class, "TextField")
}

// HitTest gives the smallest visible node whose box holds (x, y). Of two boxes of the same size the
// one deeper in the tree wins, because it is drawn on top.
func HitTest(t *mobile.Tree, x, y int) *mobile.Node {
	var best *mobile.Node
	area := func(n *mobile.Node) int { return n.Bounds.Width() * n.Bounds.Height() }
	t.Walk(func(n *mobile.Node) bool {
		b := n.Bounds
		if !n.Visible || x < b.Left || x >= b.Right || y < b.Top || y >= b.Bottom {
			return true
		}
		if best == nil || area(n) <= area(best) {
			best = n
		}
		return true
	})
	return best
}

// ClimbClickable gives the nearest node, the node itself first, that takes a tap, or nil.
func ClimbClickable(n *mobile.Node) *mobile.Node {
	for p := n; p != nil; p = p.Parent {
		if Clickable(p) {
			return p
		}
	}
	return nil
}

// tapAccepts says whether a locator that resolved to got finds the element that a tap on target means.
// The target of a tap is the nearest clickable node: a locator may find that node, or a node inside it
// whose center is inside it and that has no clickable node of its own on the way, so that touching the
// center of the found node still taps the target.
func tapAccepts(target, got *mobile.Node) bool {
	if got == target {
		return true
	}
	if got == nil || target == nil {
		return false
	}
	c := got.Bounds.Center()
	b := target.Bounds
	if c.X < b.Left || c.X >= b.Right || c.Y < b.Top || c.Y >= b.Bottom {
		return false
	}
	for p := got; p != nil; p = p.Parent {
		if p == target {
			return true
		}
		if Clickable(p) {
			return false
		}
	}
	return false
}
