package mobilerec_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mobilerec"
)

// listCard is a card of a list: a view with a label and a Delete button.
func listCard(label string, y int, id string) (card, del *mobile.Node) {
	l := textIn(label, 60, y+10)
	del = button("Delete", 60, y+100)
	card = node("android.view.ViewGroup", 50, y, 900, 250)
	if id != "" {
		card.ID, card.Attrs["resource-id"] = id, id
	}
	card.Children = []*mobile.Node{l, del}
	return card, del
}

func cardsScreen(labels []string, cardID string) (*mobile.Tree, []*mobile.Node) {
	var cards, dels []*mobile.Node
	for i, lab := range labels {
		c, d := listCard(lab, 300+i*300, cardID)
		cards, dels = append(cards, c), append(dels, d)
	}
	return screen(cards...), dels
}

func hasLocator(cs []mobilerec.Candidate, want domain.Locator) *mobilerec.Candidate {
	for i := range cs {
		if cs[i].Locator == want {
			return &cs[i]
		}
	}
	return nil
}

func TestRepeatedDeleteButtonsGetALocatorScopedByTheTextOfTheirCard(t *testing.T) {
	tree, dels := cardsScreen([]string{"Milk", "Eggs", "Bread"}, "")
	for i, want := range []string{"Milk", "Eggs", "Bread"} {
		got := mobilerec.Locate(tree, dels[i], mobilerec.ForTap, nil)
		c := hasLocator(got, domain.Locator{Desc: "Delete", Within: "container"})
		if c == nil {
			t.Fatalf("%s: no scoped locator in %+v", want, locators(got))
		}
		if c.Container == nil || c.Container.Locator != (domain.Locator{Class: "android.view.ViewGroup", HasText: want}) || c.Stability != mobilerec.Weak {
			t.Errorf("%s: container = %+v, stability %s", want, c.Container, c.Stability)
		}
		if c.Explicit {
			t.Error("a scoped locator is not explicit")
		}
	}
}

func TestARepeatedListIDGetsAContainerScopedByText(t *testing.T) {
	tree, dels := cardsScreen([]string{"Milk", "Eggs"}, "com.shop:id/row")
	for _, d := range dels {
		d.ID, d.Attrs["resource-id"] = "com.shop:id/delete", "com.shop:id/delete"
	}
	tree.Finish()
	got := mobilerec.Locate(tree, dels[1], mobilerec.ForTap, nil)
	if len(got) == 0 {
		t.Fatal("no locator")
	}
	// The id of the row is shared, so the container is found by text. The inner locator is the shared id.
	c := hasLocator(got, domain.Locator{ID: "delete", Within: "container"})
	if c == nil || c.Container == nil || c.Container.Locator != (domain.Locator{Class: "android.view.ViewGroup", HasText: "Eggs"}) {
		t.Fatalf("candidates = %+v", locators(got))
	}
	for _, g := range got {
		if g.Container != nil && g.Container.Locator.ID != "" {
			t.Errorf("a shared id was used for a container: %+v", g.Container)
		}
	}
}

func TestTheIndexIsExplicitAndOnlyTheLastResort(t *testing.T) {
	// Three Delete buttons in a row of the screen, with nothing to tell them apart.
	b := []*mobile.Node{button("Delete", 100, 300), button("Delete", 100, 600), button("Delete", 100, 900)}
	tree := screen(b...)
	usable, ambiguous := mobilerec.LocateAll(tree, b[1], mobilerec.ForTap, nil)
	if len(usable) != 1 {
		t.Fatalf("usable = %+v", usable)
	}
	c := usable[0]
	if !c.Explicit || c.Stability != mobilerec.Weak || c.Locator.Index != 2 || c.Matches != 3 || c.Note != "by position 2 of 3" || c.Tier != mobilerec.TierIndex {
		t.Errorf("index candidate = %+v", c)
	}
	if len(ambiguous) == 0 {
		t.Error("nothing is ambiguous")
	}
	if got := mobilerec.Locate(tree, b[1], mobilerec.ForTap, nil); len(got) != 0 {
		t.Errorf("Locate offered %+v: the index is never chosen for the person", locators(got))
	}

	// With a locator that is unique, no position is offered.
	b[1].ID, b[1].Attrs["resource-id"] = "com.shop:id/second", "com.shop:id/second"
	usable, _ = mobilerec.LocateAll(tree, b[1], mobilerec.ForTap, nil)
	for _, c := range usable {
		if c.Explicit {
			t.Errorf("a position was offered beside a unique locator: %+v", c)
		}
	}
	if len(usable) == 0 || usable[0].Locator.ID != "second" {
		t.Errorf("usable = %+v", locators(usable))
	}
}

func TestALabelThatChangesBetweenRunsIsNeverUsedForHasText(t *testing.T) {
	tree, dels := cardsScreen([]string{"Order 123456", "Total 12.50", "Due 12:30"}, "")
	usable, _ := mobilerec.LocateAll(tree, dels[0], mobilerec.ForTap, nil)
	for _, c := range usable {
		if c.Container != nil && c.Container.Locator.HasText != "" {
			t.Errorf("a changing label was used: %+v", c.Container)
		}
	}
	// The explicit position is what is left.
	if len(usable) == 0 || !usable[len(usable)-1].Explicit {
		t.Errorf("usable = %+v", usable)
	}
}

func TestALongLabelOrASecretIsNotUsedForHasText(t *testing.T) {
	long := strings.Repeat("a", 61)
	tree, dels := cardsScreen([]string{long, "hunter2"}, "")
	ov := mobilerec.NewOverlay(nil)
	ov.SetSecret("PW", "hunter2")
	usable, _ := mobilerec.LocateAll(tree, dels[1], mobilerec.ForTap, ov)
	for _, c := range usable {
		if c.Container != nil && c.Container.Locator.HasText != "" {
			t.Errorf("a label that may not be saved was used: %+v", c.Container)
		}
	}
}

func TestLocateAllReportsTheAmbiguousLocatorsWithTheirCounts(t *testing.T) {
	tree, dels := cardsScreen([]string{"Milk", "Eggs", "Bread"}, "")
	usable, ambiguous := mobilerec.LocateAll(tree, dels[0], mobilerec.ForTap, nil)
	if len(usable) == 0 {
		t.Fatal("no usable locator")
	}
	c := hasLocator(ambiguous, domain.Locator{Desc: "Delete"})
	if c == nil || c.Matches != 3 {
		t.Fatalf("ambiguous = %+v", ambiguous)
	}
	if hasLocator(ambiguous, domain.Locator{Role: "button", Name: "Delete"}) == nil {
		t.Errorf("the role locator is not reported: %+v", locators(ambiguous))
	}
	// A unique locator is not ambiguous.
	for _, a := range ambiguous {
		if a.Matches < 2 {
			t.Errorf("ambiguous with %d matches: %+v", a.Matches, a)
		}
	}
	// Locate gives what usable gives, without a position.
	if got := mobilerec.Locate(tree, dels[0], mobilerec.ForTap, nil); len(got) != len(usable) {
		t.Errorf("Locate = %d, usable = %d", len(got), len(usable))
	}
}

// BenchmarkLocateAllOnALargeTree measures the locators of one button among about 1000 nodes that repeat.
func BenchmarkLocateAllOnALargeTree(b *testing.B) {
	var labels []string
	for i := 0; i < 160; i++ {
		labels = append(labels, "Item "+string(rune('A'+i%26))+strings.Repeat("x", i/26))
	}
	tree, dels := cardsScreen(labels, "")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mobilerec.LocateAll(tree, dels[80], mobilerec.ForTap, nil)
	}
}
