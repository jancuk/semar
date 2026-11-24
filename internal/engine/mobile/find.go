package mobile

import (
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/uirepair"
	"github.com/jancuk/semar/internal/xpathlite"
)

const (
	maxText    = 500
	maxMatches = 200
	maxWithin  = 16
	// maxNamed is how many of the elements that one locator matched are named for a person.
	maxNamed = 3
)

// resolved is what one locator found on one tree.
type resolved struct {
	state domain.ElementState
	node  *Node
	// others names up to maxNamed of the elements that a locator matched when it matched several.
	others []domain.Candidate
}

// errLocator is an error of the locator, as opposed to an element that is not there.
type errLocator struct{ msg string }

func (e errLocator) Error() string { return e.msg }

// reader reads the elements of one tree for one action, finds each locator, and follows within. The
// elements of a step are read from one picture of the screen.
type reader struct {
	tree *Tree
	locs map[string]domain.Locator
	memo map[string]resolved
	// attrs holds the attributes that checks read, by the name of the element.
	attrs map[string][]string
}

func newReader(t *Tree, locs map[string]domain.Locator) *reader {
	return &reader{tree: t, locs: locs, memo: map[string]resolved{}}
}

// Resolve finds the element name on the tree the way a replay does: the locators are tried in the
// order of Find, within chains are followed, and the element is found only when exactly one visible
// node matches. The state says how many nodes matched and how many of them are visible. The node is
// nil when the element is not found. An error is a fault of the locator itself, not an absent element.
// The recorder uses it so that a locator it saves is one that a replay resolves.
func Resolve(t *Tree, locs map[string]domain.Locator, name string) (domain.ElementState, *Node, error) {
	res, err := newReader(t, locs).resolve(name)
	if err != nil {
		return domain.ElementState{}, nil, err
	}
	return res.state, res.node, nil
}

func (r *reader) resolve(name string) (resolved, error) { return r.resolveDepth(name, 0) }

func (r *reader) resolveDepth(name string, depth int) (resolved, error) {
	if res, ok := r.memo[name]; ok {
		return res, nil
	}
	loc, ok := r.locs[name]
	if !ok {
		return resolved{}, errLocator{fmt.Sprintf("the locator of element %s was not given to the engine", name)}
	}
	if depth > maxWithin {
		return resolved{}, errLocator{fmt.Sprintf("element %s is inside too many other elements", name)}
	}
	res := resolved{state: domain.ElementState{Locator: &loc}}
	var scope *Node
	if loc.Within != "" {
		outer, err := r.resolveDepth(loc.Within, depth+1)
		if err != nil {
			return resolved{}, err
		}
		if !outer.state.Found {
			// Nothing is inside an element that is not there.
			r.memo[name] = res
			return res, nil
		}
		scope = outer.node
	}
	nodes, err := Find(r.tree, loc, scope)
	if err != nil {
		return resolved{}, fmt.Errorf("element %s: %w", name, err)
	}
	if len(nodes) > maxMatches {
		nodes = nodes[:maxMatches]
	}
	if loc.Index > 0 {
		// The nth visible match, in the order of the tree, is the element. Past the last one, nothing is.
		var visible []*Node
		for _, n := range nodes {
			if n.Visible {
				visible = append(visible, n)
			}
		}
		nodes = nil
		if loc.Index <= len(visible) {
			nodes = visible[loc.Index-1 : loc.Index]
		}
	}
	var seen []*Node
	for _, n := range nodes {
		res.state.Count++
		if n.Visible {
			res.state.Visible = true
			seen = append(seen, n)
			res.state.Text = clip(n.Text)
		}
	}
	if len(seen) == 1 {
		res.state.Found, res.node = true, seen[0]
	} else {
		res.state.Text = ""
	}
	only := res.node
	if only == nil && len(nodes) == 1 {
		only = nodes[0]
	}
	if only != nil {
		res.state.Enabled, res.state.Checked = only.Enabled, only.Checked
		for _, k := range r.attrs[name] {
			if v, has := only.Attrs[k]; has && !(only.Password && (k == "text" || k == "value")) {
				if res.state.Attrs == nil {
					res.state.Attrs = map[string]string{}
				}
				res.state.Attrs[k] = clip(v)
			}
		}
	}
	if len(nodes) > 1 {
		res.others = nameMatches(name, nodes)
	}
	r.memo[name] = res
	return res, nil
}

func clip(s string) string {
	if r := []rune(s); len(r) > maxText {
		return string(r[:maxText])
	}
	return s
}

// Find gives the nodes that the first strategy of the locator that matches anything gives, and none
// when no strategy matches. A strategy that has no field in the locator is skipped. The order is: id
// (testId is the same), desc, role and name, text, xpath, class. Within a scope, only the nodes
// inside the scope are matched. The tree must be finished.
func Find(t *Tree, loc domain.Locator, scope *Node) ([]*Node, error) {
	if t == nil || t.Root == nil {
		return nil, nil
	}
	for _, s := range loc.MobileStrategies() {
		var nodes []*Node
		switch s {
		case "id":
			want := loc.ID
			if want == "" {
				want = loc.TestID
			}
			nodes = collect(t, scope, func(n *Node) bool { return idMatches(n.ID, want) })
		case "desc":
			want := collapse(loc.Desc)
			nodes = collect(t, scope, func(n *Node) bool { return n.Desc != "" && collapse(n.Desc) == want })
		case "role":
			name := collapse(loc.Name)
			nodes = collect(t, scope, func(n *Node) bool {
				if loc.Role != "" && !strings.EqualFold(n.Role, loc.Role) {
					return false
				}
				return name == "" || n.AccName() == name
			})
		case "text":
			want := collapse(loc.Text)
			nodes = collect(t, scope, func(n *Node) bool { return n.Visible && n.Text == want && !deeperText(n, want) })
		case "xpath":
			found, err := byXPath(t, loc.XPath, scope)
			if err != nil {
				return nil, err
			}
			nodes = found
		case "class":
			nodes = collect(t, scope, func(n *Node) bool { return n.Class == loc.Class })
		}
		if loc.HasText != "" {
			nodes = withText(nodes, collapse(loc.HasText))
		}
		if len(nodes) > 0 {
			return nodes, nil
		}
	}
	return nil, nil
}

// withText keeps the nodes that show the text, or hold a visible node that shows it, as text or as a
// description. Of those, only the innermost are kept: a node with another kept node inside it is dropped.
func withText(nodes []*Node, want string) []*Node {
	var kept []*Node
	for _, n := range nodes {
		if showsText(n, want) {
			kept = append(kept, n)
		}
	}
	var out []*Node
	for _, n := range kept {
		innermost := true
		for _, o := range kept {
			if o != n && inside(o, n) {
				innermost = false
				break
			}
		}
		if innermost {
			out = append(out, n)
		}
	}
	return out
}

func showsText(n *Node, want string) bool {
	if n.Text == want || collapse(n.Desc) == want {
		return true
	}
	for _, k := range n.Children {
		if k.Visible && showsText(k, want) {
			return true
		}
	}
	return false
}

// idMatches is true for the whole id, or for the part after :id/, which is how a person names an
// Android resource.
func idMatches(have, want string) bool {
	if have == "" || want == "" {
		return false
	}
	if have == want {
		return true
	}
	_, short, ok := strings.Cut(have, ":id/")
	return ok && short == want
}

// deeperText reports whether a visible node inside n shows the same text, so that only the innermost
// node of that text is found.
func deeperText(n *Node, want string) bool {
	for _, k := range n.Children {
		if k.Visible && (k.Text == want || deeperText(k, want)) {
			return true
		}
	}
	return false
}

// collect gives the nodes below scope, or in the whole tree, that keep accepts, in document order. The
// scope itself is not one of them.
func collect(t *Tree, scope *Node, keep func(*Node) bool) []*Node {
	var out []*Node
	visit := func(n *Node) bool {
		if n != scope && keep(n) {
			out = append(out, n)
		}
		return true
	}
	if scope != nil {
		walkNode(scope, visit)
	} else {
		t.Walk(visit)
	}
	return out
}

func walkNode(n *Node, fn func(*Node) bool) {
	if !fn(n) {
		return
	}
	for _, k := range n.Children {
		walkNode(k, fn)
	}
}

// xnode lets the xpath of a locator read a Node, whose children are a field.
type xnode struct{ n *Node }

func (x xnode) Name() string { return x.n.Class }

func (x xnode) Attr(name string) (string, bool) {
	if name == "class" {
		return x.n.Class, x.n.Class != ""
	}
	v, ok := x.n.Attrs[name]
	return v, ok
}

func (x xnode) Children() []xnode {
	out := make([]xnode, len(x.n.Children))
	for i, k := range x.n.Children {
		out[i] = xnode{k}
	}
	return out
}

func byXPath(t *Tree, src string, scope *Node) ([]*Node, error) {
	e, err := xpathlite.Compile(src)
	if err != nil {
		return nil, errLocator{"the locator could not be used: " + err.Error()}
	}
	var out []*Node
	for _, x := range xpathlite.Select(e, xnode{t.Root}) {
		if x.n != scope && (scope == nil || inside(x.n, scope)) {
			out = append(out, x.n)
		}
	}
	return out, nil
}

// inside reports whether n is below scope.
func inside(n, scope *Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p == scope {
			return true
		}
	}
	return false
}

// nameMatches proposes a locator for each of the first nodes that a locator matched, as the repair
// names its candidates, so that a person can make the locator match one.
func nameMatches(element string, nodes []*Node) []domain.Candidate {
	why := fmt.Sprintf("one of the %d elements that the locator matches", len(nodes))
	var out []domain.Candidate
	for _, n := range nodes {
		if len(out) == maxNamed {
			break
		}
		var loc domain.Locator
		switch {
		case n.ID != "":
			loc = domain.Locator{ID: n.ID}
		case n.Desc != "" && len([]rune(n.Desc)) <= 60:
			loc = domain.Locator{Desc: collapse(n.Desc)}
		case uirepair.Meaningful(n.Role) && n.AccName() != "":
			loc = domain.Locator{Role: n.Role, Name: n.AccName()}
		case n.Text != "" && len([]rune(n.Text)) <= 60:
			loc = domain.Locator{Text: n.Text}
		default:
			loc = domain.Locator{Class: n.Class}
		}
		out = append(out, domain.Candidate{Element: element, Locator: loc, Why: why})
	}
	return out
}

// axNodes is the tree as the repair reads it: the role and the name of each node that has a name.
func axNodes(t *Tree) []uirepair.AXNode {
	var out []uirepair.AXNode
	t.Walk(func(n *Node) bool {
		if name := n.AccName(); name != "" || n.ID != "" {
			role := n.Role
			if role == "" {
				role = "generic"
			}
			out = append(out, uirepair.AXNode{Role: role, Name: name, ID: n.ID, Desc: collapse(n.Desc)})
		}
		return true
	})
	return out
}
