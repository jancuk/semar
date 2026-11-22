package mobile_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
)

// listDevice is a list whose rows are shown from a first row on. A swipe up moves the list by a page.
type listDevice struct {
	rows, page int
	first      int
}

func (l *listDevice) tree() *mobile.Tree {
	var kids []*mobile.Node
	for i := l.first; i < l.first+l.page && i < l.rows; i++ {
		row := n("android.widget.TextView", "Row "+itoa(i), 100, 300+(i-l.first)*200)
		kids = append(kids, row)
	}
	return screen(kids...)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func newList(t *testing.T, rows, page int) (*rig, *listDevice) {
	r := newRig(t, rigOptions{})
	l := &listDevice{rows: rows, page: page}
	r.d.screen = l.tree()
	r.d.onTouch = func(g mobile.Gesture) {
		// A finger that goes up moves the list down: the next rows come into view.
		if g.To.Y < g.From.Y && l.first+l.page < l.rows {
			l.first = min(l.first+l.page, l.rows-1)
		}
		r.d.screen = l.tree()
	}
	return r, l
}

func scrollTo(row string) mobile.Call {
	a := act(domain.MobileScroll, "List", "row")
	return call("s", a, map[string]domain.Locator{"row": {Text: row}})
}

func TestScrollReachesAnElementBelowTheScreen(t *testing.T) {
	r, l := newList(t, 40, 8)
	res := r.do(scrollTo("Row 20"))
	if res.Action.Err != "" {
		t.Fatalf("err = %q", res.Action.Err)
	}
	if l.first != 16 || len(r.d.touches) != 2 {
		t.Errorf("first row = %d after %d swipes", l.first, len(r.d.touches))
	}
	for _, g := range r.d.touches {
		if g.To.Y >= g.From.Y {
			t.Errorf("a scroll down is a finger that goes up: %+v", g)
		}
	}
	if res.Action.EffectUnknown {
		t.Error("a scroll is a read")
	}
}

func TestScrollSendsNothingWhenTheElementIsOnTheScreen(t *testing.T) {
	r, _ := newList(t, 40, 8)
	res := r.do(scrollTo("Row 3"))
	if res.Action.Err != "" || len(r.d.touches) != 0 {
		t.Errorf("err = %q, touches %d", res.Action.Err, len(r.d.touches))
	}
}

func TestScrollEndsWhenTheListEnds(t *testing.T) {
	r, l := newList(t, 12, 8)
	res := r.do(scrollTo("Row 99"))
	if !strings.HasPrefix(res.Action.Err, check.NotFound) {
		t.Fatalf("err = %q", res.Action.Err)
	}
	// One swipe moved the list, and the next one changed nothing.
	if len(r.d.touches) != 2 || l.first != 8 {
		t.Errorf("swipes = %d, first row %d", len(r.d.touches), l.first)
	}
	// The state is that of before the scroll, with the locator that was looked up.
	if st := res.Action.Mobile.Elements["row"]; st.Locator == nil || st.Locator.Text != "Row 99" || st.Count != 0 {
		t.Errorf("miss = %+v", st)
	}
	if res.Action.EffectUnknown {
		t.Error("a scroll that found nothing has no unknown effect")
	}
}

func TestScrollGivesUpAfterFifteenSwipes(t *testing.T) {
	r := newRig(t, rigOptions{})
	// Every swipe changes the screen, so the list never ends.
	i := 0
	r.d.screen = screen(n("android.widget.TextView", "Row 0", 100, 300))
	r.d.onTouch = func(mobile.Gesture) {
		i++
		r.d.screen = screen(n("android.widget.TextView", "Row "+itoa(i), 100, 300))
	}
	res := r.do(scrollTo("Row 99"))
	if !strings.HasPrefix(res.Action.Err, check.NotFound) || len(r.d.touches) != 15 {
		t.Errorf("err = %q, swipes = %d", res.Action.Err, len(r.d.touches))
	}
}

func TestScrollUpGoesTheOtherWay(t *testing.T) {
	r, l := newList(t, 40, 8)
	l.first = 24
	r.d.screen = l.tree()
	r.d.onTouch = func(g mobile.Gesture) {
		if g.To.Y > g.From.Y {
			l.first = max(l.first-l.page, 0)
		}
		r.d.screen = l.tree()
	}
	a := act(domain.MobileScroll, "List", "row")
	a.Value = "up"
	res := r.do(call("s", a, map[string]domain.Locator{"row": {Text: "Row 10"}}))
	if res.Action.Err != "" || len(r.d.touches) == 0 || r.d.touches[0].To.Y <= r.d.touches[0].From.Y {
		t.Errorf("err = %q, touches %+v", res.Action.Err, r.d.touches)
	}
}

func TestScrollWithinSwipesOnlyInsideTheContainer(t *testing.T) {
	r := newRig(t, rigOptions{})
	box := n("androidx.recyclerview.widget.RecyclerView", "", 0, 0)
	box.Desc = "inner"
	box.Bounds = mobile.Rect{Left: 200, Top: 1000, Right: 700, Bottom: 1500}
	r.d.screen = screen(box)
	r.d.onTouch = func(mobile.Gesture) {}
	a := act(domain.MobileScroll, "List", "row")
	a.Within = "inner"
	res := r.do(call("s", a, map[string]domain.Locator{"row": {Text: "x"}, "inner": {Desc: "inner"}}))
	if !strings.HasPrefix(res.Action.Err, check.NotFound) || len(r.d.touches) == 0 {
		t.Fatalf("err = %q, touches %d", res.Action.Err, len(r.d.touches))
	}
	for _, g := range r.d.touches {
		for _, p := range []mobile.Point{g.From, g.To} {
			if p.X < 200 || p.X > 700 || p.Y < 1000 || p.Y > 1500 {
				t.Errorf("the swipe left the container: %+v", g)
			}
		}
	}
	// A container that is not there is named.
	r2 := newRig(t, rigOptions{})
	res = r2.do(call("s", a, map[string]domain.Locator{"row": {Text: "x"}, "inner": {Desc: "inner"}}))
	if !strings.Contains(res.Action.Err, "element inner matched 0") || len(r2.d.touches) != 0 {
		t.Errorf("err = %q", res.Action.Err)
	}
}

func TestScrollStopsAtAnElementThatSeveralNodesMatch(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.screen = screen(n("android.widget.TextView", "Row", 100, 300), n("android.widget.TextView", "Row", 100, 500))
	res := r.do(scrollTo("Row"))
	if !strings.Contains(res.Action.Err, "element row matched 2") || len(r.d.touches) != 0 {
		t.Errorf("err = %q, touches %d", res.Action.Err, len(r.d.touches))
	}
}

func TestSettleReadsTwiceWhenTheScreenIsStill(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.screen = screen(button("A", 100, 100))
	r.do(call("s", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	// launch: one reset, then the settle after it reads twice, and the screenshot is separate.
	if r.d.sourceCalls != 2 {
		t.Errorf("source calls = %d, want 2", r.d.sourceCalls)
	}
	if len(r.clk.sleeps) != 1 || r.clk.sleeps[0] != 150*time.Millisecond {
		t.Errorf("sleeps = %v", r.clk.sleeps)
	}
}

func TestSettleBacksOffAndStopsAtTheLimit(t *testing.T) {
	r := newRig(t, rigOptions{})
	i := 0
	// A screen that never stops changing, as a spinner does.
	r.d.sources = nil
	r.d.screen = nil
	spin := func() *mobile.Tree { i++; return screen(n("android.widget.TextView", "t"+itoa(i), 100, 100)) }
	d := &changingDriver{fakeDriver: r.d, next: spin}
	r.p.d = r.d
	r.e = mobile.New(mobile.Options{Platform: &fixedPlatform{d: d}, Recorder: recorderOf(t, r), Files: r.files, Policy: policyOf(t),
		Services: map[string]mobile.Service{"shop-app": {}}, Clock: r.clk.Now, Sleep: r.clk.Sleep})
	start := r.clk.Now()
	res := r.do(call("s", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if res.Action.Err != "" {
		t.Fatalf("a screen that never settles is read as it is: %q", res.Action.Err)
	}
	took := r.clk.Now().Sub(start)
	if took < 3*time.Second || took > 3*time.Second+time.Millisecond {
		t.Errorf("settle took %s, want exactly the 3s cap", took)
	}
	want := []time.Duration{150 * time.Millisecond, 300 * time.Millisecond, 500 * time.Millisecond, 500 * time.Millisecond}
	for k, w := range want {
		if r.clk.sleeps[k] != w {
			t.Errorf("sleep %d = %s, want %s (all: %v)", k, r.clk.sleeps[k], w, r.clk.sleeps)
		}
	}
	for _, s := range r.clk.sleeps {
		if s > 500*time.Millisecond {
			t.Errorf("a sleep of %s is over the backoff cap", s)
		}
	}
}

func TestEqualDigestsReuseTheParsedTree(t *testing.T) {
	r := newRig(t, rigOptions{})
	first := screen(button("A", 100, 100))
	again := screen(button("A", 100, 100))
	if first.Digest != again.Digest {
		t.Fatal("two trees of the same content have different digests")
	}
	if screen(button("B", 100, 100)).Digest == first.Digest {
		t.Fatal("two trees of other content have the same digest")
	}
	r.d.sources = []*mobile.Tree{first, again}
	c := call("s", domain.MobileAction{Service: "shop-app", Do: domain.MobileWait}, map[string]domain.Locator{"a": {Role: "button", Name: "A"}})
	c.Watch = []domain.Watched{{Element: "a"}}
	res := r.do(c)
	if st := res.Action.Mobile.Elements["a"]; !st.Found {
		t.Errorf("state = %+v", st)
	}
}

type changingDriver struct {
	*fakeDriver
	next func() *mobile.Tree
}

func (c *changingDriver) Source(_ context.Context) (*mobile.Tree, error) { return c.next(), nil }

type fixedPlatform struct{ d mobile.Driver }

func (p *fixedPlatform) Surface() domain.Surface                               { return domain.SurfaceAndroid }
func (p *fixedPlatform) Driver(context.Context, string) (mobile.Driver, error) { return p.d, nil }
func (p *fixedPlatform) Close(context.Context) error                           { return nil }
