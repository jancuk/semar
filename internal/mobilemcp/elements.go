package mobilemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mobilerec"
)

// listing is one reading of the screen as the model sees it.
type listing struct {
	tree  *mobile.Tree
	els   []mobilerec.Element
	nodes []*mobile.Node
	// notes are lines that the text listing ends with: what the model should know about the screen as a
	// whole, such as a list that goes on past the edge.
	notes []string
}

// keyboardClass is the class of the one pseudo-element that stands for a keyboard that draws its keys
// itself and gives the accessibility service no node for them.
const keyboardClass = "semar.keyboard"

// hints names the keyboard when the screen has more than one window, so that its keys are told from the
// app. Failing to read the keyboard only means that the windows are guessed.
func (m *Manager) hints(ctx context.Context, dev Device, t *mobile.Tree) mobile.WindowHints {
	if t == nil || t.Root == nil || len(t.Root.Children) < 2 {
		return mobile.WindowHints{}
	}
	pkg, _, err := dev.InputMethod(ctx)
	if err != nil {
		return mobile.WindowHints{}
	}
	return mobile.WindowHints{IME: pkg}
}

// listFrom lists the elements of a tree and makes them the refs e1..eN.
func (m *Manager) listFrom(ctx context.Context, dev Device, t *mobile.Tree) *listing {
	hints := m.hints(ctx, dev, t)
	els, nodes := mobilerec.ListElements(t, hints)
	ws := mobile.Windows(t, hints)
	entries := make([]refEntry, len(nodes))
	for i, n := range nodes {
		e := refEntry{digest: t.Digest, identity: mobilerec.Identity(n), loose: loose(n), bounds: n.Bounds}
		if w := mobile.WindowOf(n, ws); w != nil {
			e.windowPackage, e.windowKind = w.Package, w.Kind
		}
		entries[i] = e
	}
	// A keyboard window with no key in the listing is drawn on a canvas. It is one element, so that the
	// model sees that a keyboard is there, and taps it by coordinates.
	for _, w := range ws {
		if w.Kind != mobile.WindowKeyboard || w.Bounds.Empty() || keyboardListed(els, w) {
			continue
		}
		els = append(els, mobilerec.Element{
			Index: len(els), Class: keyboardClass, Role: "keyboard", Package: w.Package, Window: string(w.Kind),
			Bounds: w.Bounds, Enabled: true,
		})
		nodes = append(nodes, nil)
		entries = append(entries, refEntry{
			digest: t.Digest, identity: keyboardClass + "\x00" + w.Package, loose: keyboardClass + "\x00" + w.Package,
			bounds: w.Bounds, windowPackage: w.Package, windowKind: w.Kind, pseudo: true,
		})
	}
	m.refs.Reset(t.Digest, entries)
	return &listing{tree: t, els: els, nodes: nodes, notes: m.listNotes(t, els)}
}

func keyboardListed(els []mobilerec.Element, w mobile.Window) bool {
	for _, e := range els {
		if e.Window == string(mobile.WindowKeyboard) && e.Package == w.Package {
			return true
		}
	}
	return false
}

// listNotes gives what is worth saying about a screen as a whole. A Flutter app has no resource-id on its
// elements unless it sets Semantics(identifier:), so a locator rests on the label, which a new wording
// changes. A scrollable node that touches the edge of the screen, or whose items are cut by its own box,
// holds more than the listing shows, because lazy lists keep only the items on screen.
func (m *Manager) listNotes(t *mobile.Tree, els []mobilerec.Element) []string {
	var notes []string
	if isFlutter(t) {
		for _, e := range els {
			if e.Window == string(mobile.WindowApp) && e.Class != keyboardClass && e.ID == "" {
				notes = append(notes, "this is a Flutter app and its elements have no resource-id: a locator rests on the label (content-desc), which a new wording breaks. Ask for Semantics(identifier: ...) on the elements that a test needs")
				break
			}
		}
	}
	if scrollableCut(t) {
		notes = append(notes, "list is scrollable: swipe to see more")
	}
	return notes
}

// scrollableCut reports whether a visible scrollable node reaches the edge of the screen, or has a child
// that its own box cuts.
func scrollableCut(t *mobile.Tree) bool {
	cut := false
	t.Walk(func(n *mobile.Node) bool {
		if !n.Scrollable || !n.Visible {
			return true
		}
		b := n.Bounds
		if b.Top <= 0 || b.Left <= 0 || (t.Height > 0 && b.Bottom >= t.Height) || (t.Width > 0 && b.Right >= t.Width) {
			// A box that fills the screen on both sides along its axis scrolls, and says so.
			cut = true
			return false
		}
		for _, k := range n.Children {
			if k.Visible && (k.Bounds.Top < b.Top || k.Bounds.Bottom > b.Bottom || k.Bounds.Left < b.Left || k.Bounds.Right > b.Right) {
				cut = true
				return false
			}
		}
		return true
	})
	return cut
}

// list reads the screen and makes the refs.
func (m *Manager) list(ctx context.Context, dev Device) (*listing, error) {
	t, err := m.sourceWarm(ctx, dev)
	if err != nil {
		return nil, err
	}
	return m.listFrom(ctx, dev, t), nil
}

// text writes the listing grouped by window, with the secrets scrubbed:
//
//	window app com.shop
//	e1  button  "Sign in"  id=login  center=(540,1620)  bounds=[0,1560][1080,1680]  [clickable,enabled]
func (l *listing) text(m *Manager) string {
	var b strings.Builder
	fmt.Fprintf(&b, "screen %dx%d, %d elements\n", l.tree.Width, l.tree.Height, len(l.els))
	lastWin := ""
	for _, e := range l.els {
		pkg := e.Package
		if w := e.Window + " " + pkg; w != lastWin {
			fmt.Fprintf(&b, "window %s %s\n", nonEmpty(e.Window, "app"), nonEmpty(pkg, "unknown"))
			lastWin = w
		}
		b.WriteString(m.elementLine(e))
		b.WriteByte('\n')
	}
	for _, n := range l.notes {
		b.WriteString("note: " + n + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Manager) elementLine(e mobilerec.Element) string {
	var b strings.Builder
	if e.Class == keyboardClass {
		return fmt.Sprintf("e%d  keyboard  %s  bounds=[%d,%d][%d,%d]  (no keys exposed: tap by coordinates)",
			e.Index+1, m.scrub.Text(e.Package), e.Bounds.Left, e.Bounds.Top, e.Bounds.Right, e.Bounds.Bottom)
	}
	kind := e.Role
	if kind == "" {
		kind = e.Class
		if i := strings.LastIndex(kind, "."); i >= 0 {
			kind = kind[i+1:]
		}
	}
	text := m.scrub.Text(e.Text)
	fmt.Fprintf(&b, "e%d  %s  %q", e.Index+1, kind, truncateText(text, 160))
	if e.ID != "" {
		id := e.ID
		if i := strings.Index(id, ":id/"); i >= 0 {
			id = id[i+4:]
		}
		fmt.Fprintf(&b, "  id=%s", m.scrub.Text(id))
	}
	// Merged semantics (Flutter) give a description of several lines. One line per element keeps the
	// listing readable, and the desc locator matches the same collapsed text.
	if desc := collapse(e.Desc); desc != "" && desc != collapse(e.Text) {
		fmt.Fprintf(&b, "  desc=%q", truncateText(m.scrub.Text(desc), 160))
	}
	if e.Hint != "" {
		fmt.Fprintf(&b, "  hint=%q", truncateText(m.scrub.Text(e.Hint), 160))
	}
	c := e.Bounds.Center()
	fmt.Fprintf(&b, "  center=(%d,%d)  bounds=[%d,%d][%d,%d]", c.X, c.Y, e.Bounds.Left, e.Bounds.Top, e.Bounds.Right, e.Bounds.Bottom)
	var flags []string
	for _, f := range []struct {
		on   bool
		name string
	}{{e.Clickable, "clickable"}, {e.Editable, "editable"}, {e.Focused, "focused"}, {e.Checked, "checked"}, {e.Password, "password"}} {
		if f.on {
			flags = append(flags, f.name)
		}
	}
	if e.Enabled {
		flags = append(flags, "enabled")
	} else {
		flags = append(flags, "disabled")
	}
	fmt.Fprintf(&b, "  [%s]", strings.Join(flags, ","))
	return b.String()
}

// jsonElement is an element of the JSON listing: the element, its ref, and its center.
type jsonElement struct {
	Ref    string `json:"ref"`
	Center struct {
		X int `json:"x"`
		Y int `json:"y"`
	} `json:"center"`
	mobilerec.Element
}

// json writes the listing as JSON, with the secrets scrubbed.
func (l *listing) json(m *Manager) ([]byte, error) {
	out := struct {
		Width    int           `json:"width"`
		Height   int           `json:"height"`
		Elements []jsonElement `json:"elements"`
	}{Width: l.tree.Width, Height: l.tree.Height, Elements: make([]jsonElement, 0, len(l.els))}
	for _, e := range l.els {
		e.ID, e.Desc, e.Text, e.Hint = m.scrub.Text(e.ID), m.scrub.Text(e.Desc), m.scrub.Text(e.Text), m.scrub.Text(e.Hint)
		je := jsonElement{Ref: fmt.Sprintf("e%d", e.Index+1), Element: e}
		c := e.Bounds.Center()
		je.Center.X, je.Center.Y = c.X, c.Y
		out.Elements = append(out.Elements, je)
	}
	return json.Marshal(out)
}

// rawNode is a node of the raw tree.
type rawNode struct {
	Class    string     `json:"class"`
	Package  string     `json:"package,omitempty"`
	ID       string     `json:"id,omitempty"`
	Desc     string     `json:"desc,omitempty"`
	Text     string     `json:"text,omitempty"`
	Hint     string     `json:"hint,omitempty"`
	Bounds   [4]int     `json:"bounds"`
	Flags    []string   `json:"flags,omitempty"`
	Children []*rawNode `json:"children,omitempty"`
}

// maxRawNodes bounds the raw tree that mobile_list_elements_on_screen gives with raw.
const maxRawNodes = 4000

// rawTree writes the whole tree as JSON, scrubbed, cut at maxRawNodes nodes.
func (m *Manager) rawTree(t *mobile.Tree) ([]byte, error) {
	count := 0
	truncated := false
	var conv func(n *mobile.Node) *rawNode
	conv = func(n *mobile.Node) *rawNode {
		if count >= maxRawNodes {
			truncated = true
			return nil
		}
		count++
		r := &rawNode{
			Class: n.Class, Package: n.Package, ID: m.scrub.Text(n.ID), Desc: m.scrub.Text(n.Desc), Text: m.scrub.Text(n.Text),
			Hint: m.scrub.Text(n.Hint), Bounds: [4]int{n.Bounds.Left, n.Bounds.Top, n.Bounds.Right, n.Bounds.Bottom},
		}
		for _, f := range []struct {
			on   bool
			name string
		}{{n.Visible, "visible"}, {n.Enabled, "enabled"}, {n.Focused, "focused"}, {n.Checked, "checked"}, {n.Password, "password"}, {n.Scrollable, "scrollable"}} {
			if f.on {
				r.Flags = append(r.Flags, f.name)
			}
		}
		for _, k := range n.Children {
			if c := conv(k); c != nil {
				r.Children = append(r.Children, c)
			}
		}
		return r
	}
	root := conv(t.Root)
	return json.Marshal(map[string]any{"width": t.Width, "height": t.Height, "truncated": truncated, "tree": root})
}

// collapse writes a text on one line: runs of white space, newlines included, become one space.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
