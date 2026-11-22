package mobile_test

import (
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
)

func TestFinishDerivesTextVisibilityAndLinks(t *testing.T) {
	label := n("android.widget.TextView", "  Hello \n  world ", 100, 100)
	inner1, inner2 := n("android.widget.TextView", "Pay", 100, 300), n("android.widget.TextView", "now", 200, 300)
	hiddenText := n("android.widget.TextView", "secret", 300, 300)
	hiddenText.Hidden = true
	box := n("android.widget.LinearLayout", "", 100, 300, inner1, inner2, hiddenText)
	icon := n("android.widget.ImageView", "", 100, 500)
	icon.Desc = "  Cart  icon "
	empty := n("android.widget.View", "", 100, 700)
	zero := n("android.widget.TextView", "Zero", 100, 900)
	zero.Bounds = mobile.Rect{Left: 100, Top: 900, Right: 100, Bottom: 1000}
	off := n("android.widget.TextView", "Off", 5000, 100)
	tree := screen(label, box, icon, empty, zero, off)

	find := func(class string, i int) *mobile.Node {
		var got *mobile.Node
		k := 0
		tree.Walk(func(x *mobile.Node) bool {
			if x.Class == class {
				if k == i {
					got = x
				}
				k++
			}
			return true
		})
		return got
	}
	if got := find("android.widget.TextView", 0).Text; got != "Hello world" {
		t.Errorf("own text = %q", got)
	}
	if box.Text != "Pay now" {
		t.Errorf("a container shows the visible text inside it: %q", box.Text)
	}
	if icon.Text != "Cart icon" {
		t.Errorf("an image with no text shows its description: %q", icon.Text)
	}
	if empty.Text != "" {
		t.Errorf("empty = %q", empty.Text)
	}
	for name, want := range map[string]struct {
		node *mobile.Node
		vis  bool
	}{"label": {label, true}, "box": {box, true}, "hidden": {hiddenText, false}, "zero area": {zero, false}, "off screen": {off, false}, "root": {tree.Root, true}} {
		if want.node.Visible != want.vis {
			t.Errorf("%s: visible = %v", name, want.node.Visible)
		}
	}
	if label.Parent != tree.Root || inner1.Parent != box || tree.Root.Parent != nil {
		t.Error("the nodes are not linked to their parents")
	}
	if tree.Count() != 10 {
		t.Errorf("count = %d", tree.Count())
	}
	if tree.Digest == "" {
		t.Error("no digest")
	}
	// Finish is safe to call again, and does not change the text.
	d := tree.Digest
	tree.Finish()
	if tree.Digest != d || label.Text != "Hello world" || box.Text != "Pay now" {
		t.Errorf("a second Finish changed the tree: %q %q", label.Text, box.Text)
	}
}

func TestAHiddenNodeHidesWhatIsInsideIt(t *testing.T) {
	kid := n("android.widget.TextView", "inside", 100, 100)
	parent := n("android.widget.FrameLayout", "", 100, 100, kid)
	parent.Hidden = true
	screen(parent)
	if kid.Visible || parent.Visible {
		t.Errorf("kid %v, parent %v", kid.Visible, parent.Visible)
	}
}

func TestAPasswordFieldHasNoText(t *testing.T) {
	pw := n("android.widget.EditText", "hunter2", 100, 100)
	pw.Password = true
	pw.Desc = "Password"
	box := n("android.widget.FrameLayout", "", 100, 100, pw)
	screen(box)
	if pw.Text != "" || pw.Attrs["text"] != "" || box.Text != "" {
		t.Errorf("text = %q, attr = %q, container = %q", pw.Text, pw.Attrs["text"], box.Text)
	}
	if _, has := pw.Attrs["text"]; has {
		t.Error("the text attribute of a password field is kept")
	}
}

func TestAShownHintIsNotText(t *testing.T) {
	f := n("android.widget.EditText", "Email address", 100, 100)
	f.Hint = "Email address"
	typed := n("android.widget.EditText", "ana@example.com", 100, 300)
	typed.Hint = "Email address"
	screen(f, typed)
	if f.Text != "" || typed.Text != "ana@example.com" {
		t.Errorf("hint field %q, typed field %q", f.Text, typed.Text)
	}
	if f.AccName() != "Email address" {
		t.Errorf("a field with a hint is named by it: %q", f.AccName())
	}
}

func TestAccNameOrder(t *testing.T) {
	x := &mobile.Node{Desc: "d", Text: "t", Hint: "h"}
	if x.AccName() != "d" {
		t.Errorf("name = %q", x.AccName())
	}
	x.Desc = ""
	if x.AccName() != "t" {
		t.Errorf("name = %q", x.AccName())
	}
	x.Text = ""
	if x.AccName() != "h" {
		t.Errorf("name = %q", x.AccName())
	}
}

func TestTreeOfNothingIsSafe(t *testing.T) {
	var tree *mobile.Tree
	tree.Finish()
	tree.Walk(func(*mobile.Node) bool { t.Fatal("walked"); return true })
	if tree.Count() != 0 {
		t.Error("count")
	}
	(&mobile.Tree{}).Finish()
}

func TestRect(t *testing.T) {
	r := mobile.Rect{Left: 10, Top: 20, Right: 30, Bottom: 60}
	if r.Width() != 20 || r.Height() != 40 || r.Empty() || r.Center() != (mobile.Point{X: 20, Y: 40}) {
		t.Errorf("rect = %+v", r)
	}
	if !r.Intersect(mobile.Rect{Left: 40, Top: 0, Right: 50, Bottom: 100}).Empty() {
		t.Error("boxes that do not touch intersect")
	}
	if got := r.Intersect(mobile.Rect{Left: 0, Top: 0, Right: 25, Bottom: 30}); got != (mobile.Rect{Left: 10, Top: 20, Right: 25, Bottom: 30}) {
		t.Errorf("intersect = %+v", got)
	}
}

func find(t *testing.T, tree *mobile.Tree, loc domain.Locator, scope *mobile.Node) []*mobile.Node {
	t.Helper()
	got, err := mobile.Find(tree, loc, scope)
	if err != nil {
		t.Fatalf("Find(%v): %v", loc, err)
	}
	return got
}

func TestFindStrategies(t *testing.T) {
	email := n("android.widget.EditText", "", 100, 100)
	email.ID = "com.shop:id/email_field"
	email.Role = "textbox"
	email.Hint = "Email"
	submit := button("Sign in", 100, 300)
	submit.Desc = "Submit form"
	submit.ID = "com.shop:id/submit"
	title := n("android.widget.TextView", "Welcome", 100, 500)
	title.Role = "text"
	wrapper := n("android.widget.FrameLayout", "", 100, 500, title) // shows the same text as its child
	tree := screen(email, submit, wrapper)

	for name, c := range map[string]struct {
		loc  domain.Locator
		want []*mobile.Node
	}{
		"id, whole":             {domain.Locator{ID: "com.shop:id/email_field"}, []*mobile.Node{email}},
		"id, short":             {domain.Locator{ID: "email_field"}, []*mobile.Node{email}},
		"testId is an id":       {domain.Locator{TestID: "submit"}, []*mobile.Node{submit}},
		"id is not a suffix":    {domain.Locator{ID: "field"}, nil},
		"desc":                  {domain.Locator{Desc: "Submit form"}, []*mobile.Node{submit}},
		"desc is exact":         {domain.Locator{Desc: "Submit"}, nil},
		"role and name":         {domain.Locator{Role: "button", Name: "Submit form"}, []*mobile.Node{submit}},
		"role of any name":      {domain.Locator{Role: "textbox"}, []*mobile.Node{email}},
		"name by hint":          {domain.Locator{Name: "Email"}, []*mobile.Node{email}},
		"role is not case":      {domain.Locator{Role: "BUTTON"}, []*mobile.Node{submit}},
		"text, innermost":       {domain.Locator{Text: "Welcome"}, []*mobile.Node{title}},
		"text, collapsed":       {domain.Locator{Text: "  Sign   in "}, []*mobile.Node{submit}},
		"text of a hint":        {domain.Locator{Text: "Email"}, nil},
		"class":                 {domain.Locator{Class: "android.widget.EditText"}, []*mobile.Node{email}},
		"xpath":                 {domain.Locator{XPath: "//android.widget.Button[@text='Sign in']"}, []*mobile.Node{submit}},
		"xpath by resource id":  {domain.Locator{XPath: "//*[@resource-id='com.shop:id/submit']"}, nil},
		"xpath by class":        {domain.Locator{XPath: "//*[@class='android.widget.Button']"}, []*mobile.Node{submit}},
		"xpath position":        {domain.Locator{XPath: "/hierarchy/*[1]"}, []*mobile.Node{email}},
		"a locator for the web": {domain.Locator{CSS: "#x"}, nil},
		"an empty locator":      {domain.Locator{}, nil},
	} {
		got := find(t, tree, c.loc, nil)
		if len(got) != len(c.want) {
			t.Errorf("%s: found %d nodes, want %d", name, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: node %d is %s %q", name, i, got[i].Class, got[i].Text)
			}
		}
	}
}

func TestFindTriesTheStrategiesInOrderAndTheFirstThatMatchesWins(t *testing.T) {
	a := n("android.widget.Button", "Pay", 100, 100)
	a.Desc = "other"
	b := n("android.widget.Button", "Pay", 100, 300)
	b.Desc = "pay-desc"
	tree := screen(a, b)
	// desc comes before text: the text would match both, and the desc matches one.
	got := find(t, tree, domain.Locator{Desc: "pay-desc", Text: "Pay"}, nil)
	if len(got) != 1 || got[0] != b {
		t.Errorf("found %d", len(got))
	}
	// A strategy that matches nothing falls through to the next.
	got = find(t, tree, domain.Locator{Desc: "nope", Text: "Pay"}, nil)
	if len(got) != 2 {
		t.Errorf("found %d, want the two that the text matches", len(got))
	}
}

func TestFindWithinAScope(t *testing.T) {
	in := n("android.widget.TextView", "Total", 100, 300)
	list := n("android.widget.LinearLayout", "", 100, 300, in)
	out := n("android.widget.TextView", "Total", 100, 900)
	tree := screen(list, out)
	if got := find(t, tree, domain.Locator{Text: "Total"}, nil); len(got) != 2 {
		t.Errorf("without a scope: %d", len(got))
	}
	if got := find(t, tree, domain.Locator{Text: "Total"}, list); len(got) != 1 || got[0] != in {
		t.Errorf("within the list: %d", len(got))
	}
	if got := find(t, tree, domain.Locator{XPath: "//android.widget.TextView"}, list); len(got) != 1 || got[0] != in {
		t.Errorf("xpath within the list: %d", len(got))
	}
	// The scope is not one of its own matches.
	if got := find(t, tree, domain.Locator{Class: "android.widget.LinearLayout"}, list); len(got) != 0 {
		t.Errorf("the scope matched itself: %d", len(got))
	}
}

func TestAnXPathThatIsNotSupportedIsAnErrorAndNamesWhy(t *testing.T) {
	tree := screen(button("A", 100, 100))
	_, err := mobile.Find(tree, domain.Locator{XPath: "//Button[not(@a)]"}, nil)
	if err == nil || !contains(err.Error(), "the locator could not be used") || !contains(err.Error(), "not supported") {
		t.Errorf("error = %v", err)
	}
	// In a run, the error is the error of the action and it names the element.
	r := newRig(t, rigOptions{})
	r.d.screen = tree
	c := call("go", act(domain.MobileTap, "S", "x"), map[string]domain.Locator{"x": {XPath: "//Button[not(@a)]"}})
	res := r.do(c)
	if !contains(res.Action.Err, "element x:") || len(r.d.touches) != 0 {
		t.Errorf("err = %q", res.Action.Err)
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestFindInAnEmptyTree(t *testing.T) {
	if got, err := mobile.Find(nil, domain.Locator{Text: "x"}, nil); err != nil || got != nil {
		t.Errorf("%v %v", got, err)
	}
	if got, err := mobile.Find(&mobile.Tree{}, domain.Locator{Text: "x"}, nil); err != nil || got != nil {
		t.Errorf("%v %v", got, err)
	}
}

func TestAttributeChecksReadTheNativeNames(t *testing.T) {
	r := newRig(t, rigOptions{})
	b := button("Pay", 100, 100)
	b.Attrs["content-desc"] = "Pay for the order"
	b.Attrs["resource-id"] = "com.shop:id/pay"
	b.ID = "com.shop:id/pay"
	pw := n("android.widget.EditText", "hunter2", 100, 300)
	pw.ID = "com.shop:id/pw"
	pw.Password = true
	pw.Attrs["value"] = "hunter2"
	r.d.screen = screen(b, pw)
	c := call("read", domain.MobileAction{Service: "shop-app", Do: domain.MobileWait}, map[string]domain.Locator{"pay": {ID: "pay"}, "pw": {ID: "pw"}})
	c.Watch = []domain.Watched{{Element: "pay", Attrs: []string{"content-desc", "missing"}}, {Element: "pw", Attrs: []string{"text", "value", "class"}}}
	res := r.do(c)
	if got := res.Action.Mobile.Elements["pay"].Attrs; got["content-desc"] != "Pay for the order" || len(got) != 1 {
		t.Errorf("pay attrs = %v", got)
	}
	if got := res.Action.Mobile.Elements["pw"].Attrs; got["text"] != "" || got["value"] != "" || got["class"] != "android.widget.EditText" {
		t.Errorf("a password field gave attrs %v", got)
	}
	if res.Action.Mobile.Elements["pw"].Text != "" {
		t.Error("a password field gave text")
	}
}
