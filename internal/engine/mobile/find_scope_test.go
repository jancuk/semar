package mobile_test

import (
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
)

// card is a card of a list: a view with a title and a Delete button. The cards are 100 pixels apart.
func card(title string, y int) (c, del *mobile.Node) {
	t := n("android.widget.TextView", title, 0, y)
	del = button("Delete", 100, y)
	c = n("android.view.View", "", 0, y, t, del)
	c.Bounds = mobile.Rect{Left: 0, Top: y, Right: 300, Bottom: y + 100}
	return c, del
}

func threeCards() (*mobile.Tree, []*mobile.Node) {
	c1, d1 := card("Milk", 100)
	c2, d2 := card("Eggs", 300)
	c3, d3 := card("Bread", 500)
	return screen(c1, c2, c3), []*mobile.Node{d1, d2, d3}
}

func TestHasTextScopesAContainerAndTheElementInsideIt(t *testing.T) {
	tree, dels := threeCards()
	locs := map[string]domain.Locator{
		"milk-card": {Class: "android.view.View", HasText: "Milk"},
		"delete":    {Role: "button", Name: "Delete", Within: "milk-card"},
	}
	st, node, err := mobile.Resolve(tree, locs, "delete")
	if err != nil || !st.Found || node != dels[0] {
		t.Fatalf("state = %+v, node = %v, err = %v", st, node, err)
	}
	if _, node, _ := mobile.Resolve(tree, locs, "milk-card"); node == nil || node.Parent != tree.Root {
		t.Errorf("the container is not the card: %v", node)
	}
	// Without the scope the three buttons are ambiguous.
	st, _, _ = mobile.Resolve(tree, map[string]domain.Locator{"d": {Role: "button", Name: "Delete"}}, "d")
	if st.Found || st.Count != 3 {
		t.Errorf("unscoped state = %+v", st)
	}
}

func TestHasTextKeepsOnlyTheInnermostMatch(t *testing.T) {
	title := n("android.widget.TextView", "Milk", 0, 100)
	inner := n("android.view.View", "", 0, 100, title)
	outer := n("android.view.View", "", 0, 100, inner)
	tree := screen(outer)
	nodes, err := mobile.Find(tree, domain.Locator{Class: "android.view.View", HasText: "Milk"}, nil)
	if err != nil || len(nodes) != 1 || nodes[0] != inner {
		t.Errorf("nodes = %v, err = %v", nodes, err)
	}
}

func TestHasTextMatchesADescriptionAndCollapsesSpaces(t *testing.T) {
	label := n("android.view.View", "", 0, 100)
	label.Desc = "Milk   2 l"
	c := n("android.view.View", "", 0, 100, label)
	tree := screen(c, n("android.view.View", "", 0, 300))
	nodes, err := mobile.Find(tree, domain.Locator{Class: "android.view.View", HasText: "Milk 2 l"}, nil)
	if err != nil || len(nodes) != 1 || nodes[0] != label {
		t.Errorf("nodes = %v, err = %v", nodes, err)
	}
	// A text that nothing shows leaves nothing, and a locator with no other field finds nothing.
	if nodes, _ := mobile.Find(tree, domain.Locator{Class: "android.view.View", HasText: "Eggs"}, nil); len(nodes) != 0 {
		t.Errorf("nodes = %v", nodes)
	}
	if nodes, _ := mobile.Find(tree, domain.Locator{HasText: "Milk 2 l"}, nil); len(nodes) != 0 {
		t.Errorf("hasText alone found %v", nodes)
	}
}

func TestAFailedHasTextTriesTheNextStrategy(t *testing.T) {
	c, _ := card("Milk", 100)
	tree := screen(c)
	// The id matches nothing with the text, so the class is tried and does.
	nodes, err := mobile.Find(tree, domain.Locator{ID: "nope", Class: "android.view.View", HasText: "Milk"}, nil)
	if err != nil || len(nodes) != 1 || nodes[0] != c {
		t.Errorf("nodes = %v, err = %v", nodes, err)
	}
}

func TestIndexPicksTheNthVisibleMatchInTreeOrder(t *testing.T) {
	tree, dels := threeCards()
	for i, want := range dels {
		loc := map[string]domain.Locator{"d": {Role: "button", Name: "Delete", Index: i + 1}}
		st, node, err := mobile.Resolve(tree, loc, "d")
		if err != nil || !st.Found || node != want || st.Count != 1 {
			t.Errorf("index %d: state = %+v, node = %v, err = %v", i+1, st, node, err)
		}
	}
	// A hidden match is not counted.
	c1, d1 := card("Milk", 100)
	d1.Hidden = true
	c2, d2 := card("Eggs", 300)
	tree = screen(c1, c2)
	st, node, _ := mobile.Resolve(tree, map[string]domain.Locator{"d": {Role: "button", Name: "Delete", Index: 1}}, "d")
	if !st.Found || node != d2 {
		t.Errorf("state = %+v, node = %v", st, node)
	}
}

func TestIndexWithinAScope(t *testing.T) {
	d1 := button("Delete", 0, 100)
	d2 := button("Delete", 0, 300)
	list := n("android.widget.FrameLayout", "", 0, 100, d1, d2)
	list.ID = "list"
	other := button("Delete", 0, 600)
	tree := screen(other, list)
	locs := map[string]domain.Locator{"list": {ID: "list"}, "d": {Role: "button", Name: "Delete", Within: "list", Index: 2}}
	st, node, err := mobile.Resolve(tree, locs, "d")
	if err != nil || !st.Found || node != d2 {
		t.Errorf("state = %+v, node = %v, err = %v", st, node, err)
	}
}

func TestAnIndexPastTheLastMatchIsNotFound(t *testing.T) {
	tree, _ := threeCards()
	st, node, err := mobile.Resolve(tree, map[string]domain.Locator{"d": {Role: "button", Name: "Delete", Index: 4}}, "d")
	if err != nil || st.Found || st.Count != 0 || st.Visible || node != nil {
		t.Errorf("state = %+v, node = %v, err = %v", st, node, err)
	}
}

func TestWithoutAnIndexThreeMatchesAreStillRefused(t *testing.T) {
	tree, _ := threeCards()
	st, node, _ := mobile.Resolve(tree, map[string]domain.Locator{"d": {Role: "button", Name: "Delete"}}, "d")
	if st.Found || st.Count != 3 || node != nil {
		t.Errorf("state = %+v, node = %v", st, node)
	}
}

func TestTheChosenNodeGivesItsFlags(t *testing.T) {
	tree, dels := threeCards()
	dels[1].Enabled, dels[1].Checked = false, true
	st, _, _ := mobile.Resolve(tree, map[string]domain.Locator{"d": {Role: "button", Name: "Delete", Index: 2}}, "d")
	if st.Enabled || !st.Checked {
		t.Errorf("state = %+v", st)
	}
}

func TestATapOnAnIndexedElementTouchesItsCenter(t *testing.T) {
	r := newRig(t, rigOptions{})
	tree, _ := threeCards()
	r.d.screen = tree
	locs := map[string]domain.Locator{"del": {Role: "button", Name: "Delete", Index: 3}}
	res := r.do(call("go", act(domain.MobileTap, "Cart", "del"), locs))
	if res.Action.Err != "" {
		t.Fatalf("err = %q", res.Action.Err)
	}
	if len(r.d.touches) != 1 || r.d.touches[0].From != (mobile.Point{X: 150, Y: 550}) {
		t.Errorf("touches = %+v", r.d.touches)
	}
	if got := res.Action.Mobile.Target; got == nil || got.Index != 3 {
		t.Errorf("target = %+v", got)
	}
}
