package mobile_test

import (
	"context"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

func hidden(n *mobile.Node) *mobile.Node { n.Hidden = true; return n }

// payBtn is a button with the id pay. A locator by id gives hidden nodes too, so the replay's rule of one
// visible match is what tells them apart.
func payBtn(y int) *mobile.Node {
	b := mobiletest.Button("Pay", 0, y)
	b.ID = "pay"
	return b
}

// Resolve is the rule of a replay: the element is found when exactly one visible node matches.
func TestResolveFindsExactlyOneVisibleMatch(t *testing.T) {
	locs := map[string]domain.Locator{"pay": {ID: "pay"}}
	tests := []struct {
		name         string
		kids         []*mobile.Node
		found        bool
		count        int
		visible, has bool
	}{
		{"one visible", []*mobile.Node{payBtn(0)}, true, 1, true, true},
		{"a hidden one beside it", []*mobile.Node{payBtn(0), hidden(payBtn(200))}, true, 2, true, true},
		{"two visible", []*mobile.Node{payBtn(0), payBtn(200)}, false, 2, true, false},
		{"only a hidden one", []*mobile.Node{hidden(payBtn(0))}, false, 1, false, false},
		{"none", []*mobile.Node{mobiletest.Button("Cancel", 0, 0)}, false, 0, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, node, err := mobile.Resolve(mobiletest.Screen(tt.kids...), locs, "pay")
			if err != nil {
				t.Fatal(err)
			}
			if st.Found != tt.found || st.Count != tt.count || st.Visible != tt.visible || (node != nil) != tt.has {
				t.Errorf("state = %+v, node = %v", st, node)
			}
			if tt.found && node.Text != "Pay" {
				t.Errorf("node = %+v", node)
			}
		})
	}
}

func TestResolveFollowsWithinAndRefusesAnUnknownName(t *testing.T) {
	list := mobiletest.Node("android.widget.FrameLayout", "", 0, 0, mobiletest.Button("Pay", 0, 0))
	list.ID = "list"
	other := mobiletest.Button("Pay", 0, 500)
	tree := mobiletest.Screen(list, other)
	locs := map[string]domain.Locator{"list": {ID: "list"}, "pay": {Text: "Pay", Within: "list"}}
	st, node, err := mobile.Resolve(tree, locs, "pay")
	if err != nil || !st.Found || node == nil || node.Bounds.Top != 0 {
		t.Errorf("within: state = %+v, node = %v, err = %v", st, node, err)
	}
	if _, _, err := mobile.Resolve(tree, locs, "nope"); err == nil {
		t.Error("an element with no locator was resolved")
	}
}

func TestSettleReadsUntilTwoReadsAreAlike(t *testing.T) {
	a, b := mobiletest.Screen(mobiletest.Button("A", 0, 0)), mobiletest.Screen(mobiletest.Button("B", 0, 0))
	d := mobiletest.NewFakeDriver()
	d.Sources = []*mobile.Tree{a, b, b}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var slept []time.Duration
	sleep := func(_ context.Context, dur time.Duration) error {
		now = now.Add(dur)
		slept = append(slept, dur)
		return nil
	}
	got, err := mobile.Settle(context.Background(), d, 3*time.Second, func() time.Time { return now }, sleep)
	if err != nil || got != b {
		t.Fatalf("got = %v, err = %v", got, err)
	}
	if d.SourceCalls() != 3 || len(slept) != 2 || slept[0] != 150*time.Millisecond || slept[1] != 300*time.Millisecond {
		t.Errorf("reads = %d, sleeps = %v: the delay starts at 150 ms and doubles", d.SourceCalls(), slept)
	}
}

func TestSettleGivesTheLastReadAtTheLimit(t *testing.T) {
	d := mobiletest.NewFakeDriver()
	var trees []*mobile.Tree
	for i := 0; i < 40; i++ {
		trees = append(trees, mobiletest.Screen(mobiletest.Button(string(rune('a'+i%26))+string(rune('a'+i/26)), 0, 0)))
	}
	d.Sources = trees
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sleep := func(_ context.Context, dur time.Duration) error { now = now.Add(dur); return nil }
	got, err := mobile.Settle(context.Background(), d, time.Second, func() time.Time { return now }, sleep)
	if err != nil || got == nil || !now.Equal(time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)) {
		t.Errorf("got = %v, err = %v, now = %v: a screen that never settles is read at the cap", got, err, now)
	}
}
