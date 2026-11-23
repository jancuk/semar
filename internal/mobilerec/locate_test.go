package mobilerec_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mobilerec"
)

func clickable(n *mobile.Node) *mobile.Node { n.Attrs["clickable"] = "true"; return n }

func row(kids ...*mobile.Node) *mobile.Node {
	r := node("android.view.ViewGroup", 50, 600, 900, 200)
	clickable(r)
	r.Children = kids
	return r
}

func textIn(text string, x, y int) *mobile.Node {
	l := node("android.widget.TextView", x, y, 400, 80)
	l.Text = text
	l.Attrs["text"] = text
	return l
}

func locators(cs []mobilerec.Candidate) []domain.Locator {
	var out []domain.Locator
	for _, c := range cs {
		out = append(out, c.Locator)
	}
	return out
}

func TestHitTestFindsTheSmallestVisibleNode(t *testing.T) {
	leaf := textIn("Pay now", 100, 650)
	tree := screen(row(leaf))
	if got := mobilerec.HitTest(tree, 200, 680); got != leaf {
		t.Errorf("hit = %+v, want the leaf", got)
	}
	if got := mobilerec.HitTest(tree, 900, 780); got == nil || got.Class != "android.view.ViewGroup" {
		t.Errorf("hit outside the leaf = %+v, want the row", got)
	}
	if mobilerec.HitTest(tree, 1, 2399) != nil && mobilerec.HitTest(tree, 1, 2399).Class != "hierarchy" {
		t.Error("a point outside every node found a node")
	}
}

// A Compose or React Native row has its text in a leaf and its click on the parent. A tap targets the
// parent, and a locator that finds the leaf is accepted because the leaf lies inside the row with no
// click of its own. A check targets the leaf itself.
func TestTapClimbsToTheClickableAncestorAndAssertTargetsTheLeaf(t *testing.T) {
	leaf := textIn("Pay now", 100, 650)
	parent := row(leaf)
	tree := screen(parent)

	if got := mobilerec.ClimbClickable(leaf); got != parent {
		t.Fatalf("climb = %+v", got)
	}
	taps := mobilerec.Locate(tree, parent, mobilerec.ForTap, nil)
	if len(taps) == 0 || taps[0].Locator != (domain.Locator{Text: "Pay now"}) {
		t.Errorf("tap locators = %+v, want the text of the leaf inside the row", locators(taps))
	}
	checks := mobilerec.Locate(tree, leaf, mobilerec.ForAssert, nil)
	if len(checks) == 0 || checks[0].Locator != (domain.Locator{Text: "Pay now"}) {
		t.Errorf("assert locators = %+v", locators(checks))
	}
	// The row itself has no locator for a check of the row: only the leaf says its text.
	if got := mobilerec.Locate(tree, parent, mobilerec.ForAssert, nil); len(got) > 0 && got[0].Locator.Text != "Pay now" {
		t.Errorf("row locators = %+v", locators(got))
	}
}

func TestANestedClickableIsNotAcceptedForATapOnTheParent(t *testing.T) {
	inner := clickable(textIn("More", 100, 650))
	parent := row(inner)
	tree := screen(parent)
	for _, c := range mobilerec.Locate(tree, parent, mobilerec.ForTap, nil) {
		if c.Locator.Text == "More" {
			t.Errorf("a locator of the nested clickable was accepted for the parent: %+v", c)
		}
	}
}

func TestRanking(t *testing.T) {
	t.Run("id short, then full when the short one is not alone", func(t *testing.T) {
		a := clickable(textIn("A", 100, 300))
		a.ID, a.Attrs["resource-id"] = "com.shop:id/save", "com.shop:id/save"
		b := clickable(textIn("B", 100, 500))
		b.ID, b.Attrs["resource-id"] = "com.lib:id/save", "com.lib:id/save"
		tree := screen(a, b)
		got := locators(mobilerec.Locate(tree, a, mobilerec.ForTap, nil))
		if len(got) == 0 || got[0] != (domain.Locator{ID: "com.shop:id/save"}) {
			t.Errorf("locators = %+v, want the full id first", got)
		}
		c := clickable(textIn("C", 100, 300))
		c.ID, c.Attrs["resource-id"] = "com.shop:id/unique", "com.shop:id/unique"
		got = locators(mobilerec.Locate(screen(c), c, mobilerec.ForTap, nil))
		if len(got) == 0 || got[0] != (domain.Locator{ID: "unique"}) {
			t.Errorf("locators = %+v, want the short id", got)
		}
	})

	t.Run("desc collapses the newlines of a flutter label", func(t *testing.T) {
		b := button("Add to\ncart\n  now", 100, 900)
		tree := screen(b)
		got := locators(mobilerec.Locate(tree, b, mobilerec.ForTap, nil))
		if len(got) == 0 || got[0] != (domain.Locator{Desc: "Add to cart now"}) {
			t.Errorf("locators = %+v", got)
		}
	})

	t.Run("role with name, then text", func(t *testing.T) {
		b := clickable(textIn("Continue", 100, 900))
		b.Role = "button"
		got := mobilerec.Locate(screen(b), b, mobilerec.ForTap, nil)
		if len(got) < 2 || got[0].Tier != mobilerec.TierRole || got[1].Tier != mobilerec.TierText {
			t.Errorf("tiers = %+v", got)
		}
	})

	t.Run("an edit text is found by id, desc, or hint and never by what it holds", func(t *testing.T) {
		f := node("android.widget.EditText", 100, 300, 800, 100)
		f.Text, f.Role, f.Hint = "ana@example.com", "textbox", "Email"
		f.Attrs["text"], f.Attrs["hint"] = "ana@example.com", "Email"
		got := mobilerec.Locate(screen(f), f, mobilerec.ForType, nil)
		if len(got) != 1 || got[0].Locator.XPath != "//android.widget.EditText[@hint='Email']" {
			t.Fatalf("locators = %+v, want one xpath on the hint", locators(got))
		}
		f.ID, f.Attrs["resource-id"] = "com.shop:id/email", "com.shop:id/email"
		got = mobilerec.Locate(screen(f), f, mobilerec.ForType, nil)
		if len(got) == 0 || got[0].Tier != mobilerec.TierID {
			t.Errorf("locators = %+v, want the id first", locators(got))
		}
		for _, c := range got {
			if c.Tier == mobilerec.TierText || c.Tier == mobilerec.TierRole {
				t.Errorf("an edit text got a %s locator: %+v", c.Tier, c)
			}
		}
	})

	t.Run("multiple unlabeled compose edit texts fall back to role and placeholder name", func(t *testing.T) {
		a := node("android.widget.EditText", 100, 300, 800, 100)
		a.Text, a.Role = "Shortcut", "textbox"
		b := node("android.widget.EditText", 100, 500, 800, 100)
		b.Text, b.Role = "Saved message", "textbox"
		got := mobilerec.Locate(screen(a, b), a, mobilerec.ForType, nil)
		if len(got) != 1 || got[0].Locator != (domain.Locator{Role: "textbox", Name: "Shortcut"}) || got[0].Stability != mobilerec.Weak {
			t.Fatalf("locators = %+v, want weak role=textbox name=Shortcut", got)
		}
	})

	t.Run("a text that repeats is found within a container that is alone", func(t *testing.T) {
		mk := func(id string, y int) (*mobile.Node, *mobile.Node) {
			edit := clickable(textIn("Edit", 100, y+50))
			c := node("android.view.ViewGroup", 50, y, 900, 200)
			c.ID, c.Attrs["resource-id"] = "com.shop:id/"+id, "com.shop:id/"+id
			c.Children = []*mobile.Node{edit}
			return c, edit
		}
		c1, edit1 := mk("row-1", 300)
		c2, _ := mk("row-2", 600)
		tree := screen(c1, c2)
		got := mobilerec.Locate(tree, edit1, mobilerec.ForTap, nil)
		if len(got) == 0 || got[0].Tier != mobilerec.TierWithin || got[0].Container == nil {
			t.Fatalf("locators = %+v, want a within locator", locators(got))
		}
		if got[0].Container.Locator != (domain.Locator{ID: "row-1"}) {
			t.Errorf("container = %+v", got[0].Container)
		}
	})

	t.Run("strings with variables, and both kinds of quote, are skipped", func(t *testing.T) {
		b := button("Hello {{name}}", 100, 900)
		for _, c := range mobilerec.Locate(screen(b), b, mobilerec.ForTap, nil) {
			if strings.Contains(c.Locator.Desc+c.Locator.Name+c.Locator.Text+c.Locator.XPath, "{{") {
				t.Errorf("a locator holds a variable: %+v", c)
			}
		}
		q := node("android.widget.Button", 100, 900, 300, 100)
		q.Text, q.Attrs["text"] = `It's "ok"`, `It's "ok"`
		for _, c := range mobilerec.Locate(screen(q), q, mobilerec.ForAssert, nil) {
			if c.Tier == mobilerec.TierXPath {
				t.Errorf("an xpath was made of a string with both quotes: %+v", c)
			}
		}
	})

	t.Run("dynamic text comes after text that holds still", func(t *testing.T) {
		b := node("android.widget.Button", 100, 900, 300, 100)
		b.Desc, b.Text, b.Role = "Pay", "Rp 150.000", "button"
		b.Attrs["content-desc"], b.Attrs["text"], b.Attrs["clickable"] = "Pay", "Rp 150.000", "true"
		got := mobilerec.Locate(screen(b), b, mobilerec.ForTap, nil)
		if len(got) == 0 || got[0].Locator.Desc != "Pay" {
			t.Fatalf("locators = %+v", locators(got))
		}
		last := got[len(got)-1]
		if last.Locator.Text != "Rp 150.000" || last.Stability != mobilerec.Weak {
			t.Errorf("last = %+v, want the amount, weak", last)
		}
	})

	t.Run("a clickable card with a ticking timestamp prefers its stable child label", func(t *testing.T) {
		title := textIn("semarclip", 100, 620)
		title.Role = "text"
		age := textIn("1s", 100, 700)
		age.Role = "text"
		pin := clickable(textIn("Pin", 800, 650))
		card := row(title, age, pin)
		card.Text = "semarclip 1s Pin"
		got := mobilerec.Locate(screen(card), card, mobilerec.ForTap, nil)
		if len(got) == 0 || got[0].Dynamic || (got[0].Locator.Name != "semarclip" && got[0].Locator.Text != "semarclip") {
			t.Fatalf("locators = %+v, want stable child label 'semarclip' first", got)
		}
	})
}

func TestIsDynamic(t *testing.T) {
	for _, s := range []string{"42", "Rp 150.000", "$12.50", "2026-11-22", "12:30", "Nov 22", "12345678", "order 98765", "3f2c9a10-aaaa-4bbb-8ccc-0123456789ab", "run-20261122"} {
		if !mobilerec.IsDynamic(s) {
			t.Errorf("%q should look dynamic", s)
		}
	}
	for _, s := range []string{"Sign in", "Add to cart", "Hi Ana", "Step 1 of 3", ""} {
		if mobilerec.IsDynamic(s) {
			t.Errorf("%q should not look dynamic", s)
		}
	}
}

func TestScreenNameFromHeadingOrTheTopBar(t *testing.T) {
	if got := mobilerec.ScreenHeading(signInScreen()); got != "Sign in" {
		t.Errorf("heading = %q", got)
	}
	bar := textIn("Orders", 100, 40)
	if got := mobilerec.ScreenHeading(screen(bar, button("Go", 100, 900))); got != "Orders" {
		t.Errorf("top bar = %q", got)
	}
	if got := mobilerec.ScreenHeading(screen(button("Go", 100, 900))); got != "" {
		t.Errorf("no heading = %q", got)
	}
	sys := textIn("Google Play services notification", 100, 20)
	sys.Package = "com.android.systemui"
	if got := mobilerec.ScreenHeading(screen(sys, bar)); got != "Orders" {
		t.Errorf("systemui bar was not skipped: %q", got)
	}
}

func TestAnUnnamedScreenIsScreen1(t *testing.T) {
	r := newRig(t)
	r.d.Show(screen(clickable(button("Go", 100, 900))))
	r.d.OnLaunch = func() {}
	r.d.OnReset = func(string) {}
	s := r.session(mobilerec.KindFlow, "plain")
	f, err := s.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if f.Screen != "Screen1" {
		t.Errorf("screen = %q", f.Screen)
	}
	f, err = s.SetScreen("Welcome")
	if err != nil || f.Screen != "Welcome" {
		t.Errorf("SetScreen = %+v %v", f, err)
	}
	if _, err := s.SetScreen("bad name"); err == nil {
		t.Error("a name with a space was taken")
	}
}
