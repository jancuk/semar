package mobilemcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mobilerec"
)

// refEntry is what a ref remembers about the element it named: enough to find it again when the screen
// was read again.
type refEntry struct {
	digest        string
	identity      string // mobilerec.Identity: class, ids, and the box
	loose         string // the same without the box
	bounds        mobile.Rect
	windowPackage string
	windowKind    mobile.WindowKind
	// pseudo is true for the one entry that stands for a keyboard that draws its keys itself: there is no
	// node to find again, and the place is the box of the keyboard window.
	pseudo bool
}

// RefMap holds the refs e1..eN of the last listing of the screen.
type RefMap struct {
	digest  string
	entries []refEntry
}

// NewRefMap makes an empty map.
func NewRefMap() *RefMap { return &RefMap{} }

// Reset replaces the refs with those of a new listing. The index of an entry plus one is its number.
func (r *RefMap) Reset(digest string, entries []refEntry) {
	r.digest = digest
	r.entries = entries
}

// Invalidate forgets every ref, as after a flow that changed the screen outside the tools. The next
// listing makes new ones.
func (r *RefMap) Invalidate() { r.Reset("", nil) }

// Len is how many refs there are.
func (r *RefMap) Len() int { return len(r.entries) }

// ParseRef reads e7 or @e7 (the letter is not case sensitive) and gives 7.
func ParseRef(s string) (int, error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "@")
	if len(t) < 2 || (t[0] != 'e' && t[0] != 'E') {
		return 0, fmt.Errorf("%q is not a ref: a ref looks like e3", truncateText(s, 20))
	}
	n, err := strconv.Atoi(t[1:])
	if err != nil || n < 1 || strconv.Itoa(n) != t[1:] {
		return 0, fmt.Errorf("%q is not a ref: a ref looks like e3", truncateText(s, 20))
	}
	return n, nil
}

// Lookup gives the entry of ref number n.
func (r *RefMap) Lookup(n int) (refEntry, bool) {
	if n < 1 || n > len(r.entries) {
		return refEntry{}, false
	}
	return r.entries[n-1], true
}

// loose is the identity of a node without its box, which is what survives a layout that moved.
func loose(n *mobile.Node) string {
	return strings.Join([]string{n.Class, n.ID, strings.Join(strings.Fields(n.Desc), " "), n.Hint}, "\x00")
}

// Refind looks for the element of e in a tree that was read after the listing. It looks only inside the
// windows of the same package, so that a key of the keyboard is never taken for a button of the app. The
// text of a node is not part of the identity, so a key of the keyboard stays the same key while the field
// that it types in changes. One node with the same class, id, description, and hint is the element. With
// several (a key and its preview popup, say), the one whose box is the same wins. Of the keys of a keyboard
// that moved, with none of the same box, the one that overlaps the old box most wins if it clearly beats
// the others. In any other window, and with none of those, the ref is lost.
func Refind(t *mobile.Tree, hints mobile.WindowHints, e refEntry) (*mobile.Node, bool) {
	ws := mobile.Windows(t, hints)
	var same []*mobile.Node
	t.Walk(func(n *mobile.Node) bool {
		if !n.Visible || n.Bounds.Empty() || loose(n) != e.loose {
			return true
		}
		if w := mobile.WindowOf(n, ws); w != nil && w.Package == e.windowPackage {
			same = append(same, n)
		}
		return true
	})
	switch len(same) {
	case 0:
		return nil, false
	case 1:
		return same[0], true
	}
	var exact []*mobile.Node
	for _, n := range same {
		if mobilerec.Identity(n) == e.identity {
			exact = append(exact, n)
		}
	}
	if len(exact) == 1 {
		return exact[0], true
	}
	if len(exact) > 1 || e.windowKind != mobile.WindowKeyboard {
		// Rows of a list look alike and move when it scrolls, so a guess could tap the wrong row.
		return nil, false
	}
	// A key does not scroll: when the keyboard changed its layout a little, the key that is nearest to
	// where it was is the same key.
	return nearest(same, e.bounds)
}

// nearest picks the node whose box overlaps the old one most, when it covers at least half of the old box
// and the next one covers less than half as much.
func nearest(nodes []*mobile.Node, old mobile.Rect) (*mobile.Node, bool) {
	area := old.Width() * old.Height()
	if area <= 0 {
		return nil, false
	}
	var best *mobile.Node
	bestOverlap, second := 0, 0
	for _, n := range nodes {
		o := overlap(n.Bounds, old)
		switch {
		case o > bestOverlap:
			best, bestOverlap, second = n, o, bestOverlap
		case o > second:
			second = o
		}
	}
	if best == nil || bestOverlap*2 < area || second*2 >= bestOverlap {
		return nil, false
	}
	return best, true
}

func overlap(a, b mobile.Rect) int {
	w := min(a.Right, b.Right) - max(a.Left, b.Left)
	h := min(a.Bottom, b.Bottom) - max(a.Top, b.Top)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

// Target is the place that a ref or a pair of coordinates means.
type Target struct {
	Point  mobile.Point
	Bounds mobile.Rect
	Ref    string
	// Refound is true when the screen had changed and the ref was found again.
	Refound bool
}

// resolveRef finds the place of a ref on the screen as it is now. With the same digest as the listing it
// is the center of the box that the listing gave. Otherwise the element is found again, and when it
// cannot be, the error says the ref is stale and shows the screen as it is now, whose refs are then valid.
func (m *Manager) resolveRef(ctx context.Context, dev Device, ref string) (Target, error) {
	n, err := ParseRef(ref)
	if err != nil {
		return Target{}, err
	}
	e, ok := m.refs.Lookup(n)
	if !ok {
		if m.refs.Len() == 0 {
			return Target{}, errors.New("there are no refs yet: call mobile_list_elements_on_screen first")
		}
		return Target{}, fmt.Errorf("ref e%d does not exist: the last listing has e1 to e%d", n, m.refs.Len())
	}
	tree, err := dev.Source(ctx)
	if err != nil {
		return Target{}, err
	}
	name := fmt.Sprintf("e%d", n)
	if tree.Digest == e.digest {
		return Target{Point: e.bounds.Center(), Bounds: e.bounds, Ref: name}, nil
	}
	hints := m.hints(ctx, dev, tree)
	if e.pseudo {
		// The keyboard that draws its own keys has no node: its window is what is found again.
		for _, w := range mobile.Windows(tree, hints) {
			if w.Kind == mobile.WindowKeyboard && w.Package == e.windowPackage && !w.Bounds.Empty() {
				return Target{Point: w.Bounds.Center(), Bounds: w.Bounds, Ref: name, Refound: true}, nil
			}
		}
		l := m.listFrom(ctx, dev, tree)
		return Target{}, fmt.Errorf("ref %s is stale: the keyboard is gone. Call mobile_list_elements_on_screen again. The screen now:\n%s", name, l.text(m))
	}
	node, found := Refind(tree, hints, e)
	if !found {
		l := m.listFrom(ctx, dev, tree)
		return Target{}, fmt.Errorf("ref %s is stale: the screen changed. Call mobile_list_elements_on_screen again. The screen now:\n%s", name, l.text(m))
	}
	return Target{Point: node.Bounds.Center(), Bounds: node.Bounds, Ref: name, Refound: true}, nil
}

// pointOf is the place of a tap: a ref, or x and y. A ref wins when both are given.
func (m *Manager) pointOf(ctx context.Context, dev Device, ref string, x, y *float64) (Target, error) {
	if strings.TrimSpace(ref) != "" {
		t, err := m.resolveRef(ctx, dev, ref)
		if err != nil {
			return Target{}, err
		}
		return t, nil
	}
	if x == nil || y == nil {
		return Target{}, errors.New("give x and y, or a ref from mobile_list_elements_on_screen")
	}
	p, err := coordinate(*x, *y)
	if err != nil {
		return Target{}, err
	}
	return Target{Point: p}, nil
}

// coordinate rounds a pair of numbers to a point and refuses what is not a place on a screen.
func coordinate(x, y float64) (mobile.Point, error) {
	const limit = 100000
	if x != x || y != y || x < 0 || y < 0 || x > limit || y > limit {
		return mobile.Point{}, fmt.Errorf("(%v, %v) is not a point on the screen: x and y are pixels from the top left, 0 or more", x, y)
	}
	return mobile.Point{X: int(x + 0.5), Y: int(y + 0.5)}, nil
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
