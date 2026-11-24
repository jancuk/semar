package mobilemcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mobilemcp"
)

// seqDev gives the screens one after the other, and the last one for ever.
type seqDev struct {
	*fakeDev
	mu    sync.Mutex
	trees []*mobile.Tree
	reads int
}

func (d *seqDev) Source(ctx context.Context) (*mobile.Tree, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := min(d.reads, len(d.trees)-1)
	d.reads++
	return d.trees[i], nil
}

func (d *seqDev) count() int { d.mu.Lock(); defer d.mu.Unlock(); return d.reads }

// sleeps counts the waits of a Manager without waiting.
type sleeps struct {
	mu sync.Mutex
	ds []time.Duration
}

func (s *sleeps) opt(o *mobilemcp.Options, _ *fixture) {
	o.Sleep = func(ctx context.Context, d time.Duration) error {
		s.mu.Lock()
		s.ds = append(s.ds, d)
		s.mu.Unlock()
		return ctx.Err()
	}
}

func (s *sleeps) total() (n int, sum time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.ds {
		sum += d
	}
	return len(s.ds), sum
}

func seqFixture(t *testing.T, sl *sleeps, names ...string) (*fixture, *seqDev) {
	t.Helper()
	sd := &seqDev{}
	for i, n := range names {
		sd.trees = append(sd.trees, retree(t, n, "d"+string(rune('a'+i)), nil))
	}
	f := newFixture(t, sl.opt, func(o *mobilemcp.Options, fx *fixture) {
		sd.fakeDev = fx.dev
		o.Opener = func(context.Context, string) (mobilemcp.Device, func(context.Context) error, error) {
			return sd, func(context.Context) error { return nil }, nil
		}
	})
	return f, sd
}

func TestListingWaitsForTheFlutterSemanticsToBeBuilt(t *testing.T) {
	sl := &sleeps{}
	f, sd := seqFixture(t, sl, "flutter_semantics_empty.xml", "flutter_semantics_empty.xml", "flutter_sinau.xml")
	got := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(got, `button  "Masuk"`) {
		t.Fatalf("the listing has no button:\n%s", got)
	}
	if n, sum := sl.total(); n != 2 || sum != 800*time.Millisecond || sd.count() != 3 {
		t.Errorf("%d waits of %v in all, %d reads: want two waits of 400 ms and three reads", n, sum, sd.count())
	}
}

func TestListingGivesUpOnAFlutterViewThatStaysEmpty(t *testing.T) {
	sl := &sleeps{}
	f, sd := seqFixture(t, sl, "flutter_semantics_empty.xml")
	got := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.HasPrefix(got, "screen 1080x2400, 0 elements") {
		t.Errorf("listing = %q", got)
	}
	if n, _ := sl.total(); n != 3 || sd.count() != 4 {
		t.Errorf("%d waits, %d reads: want 3 waits and 4 reads", n, sd.count())
	}
}

func TestListingReadsAScreenThatIsNotFlutterOnce(t *testing.T) {
	sl := &sleeps{}
	f, sd := seqFixture(t, sl, "views.xml")
	f.ok("mobile_list_elements_on_screen", "")
	if n, _ := sl.total(); n != 0 || sd.count() != 1 {
		t.Errorf("%d waits, %d reads: a plain app is read once", n, sd.count())
	}
	// A Flutter view that has its elements is read once too.
	sl2 := &sleeps{}
	f2, sd2 := seqFixture(t, sl2, "flutter_sinau.xml")
	f2.ok("mobile_list_elements_on_screen", "")
	if n, _ := sl2.total(); n != 0 || sd2.count() != 1 {
		t.Errorf("%d waits, %d reads for a built tree", n, sd2.count())
	}
}

func TestLaunchWaitsForAFlutterAppAndStopsAtTenSeconds(t *testing.T) {
	sl := &sleeps{}
	f, sd := seqFixture(t, sl, "flutter_semantics_empty.xml", "flutter_semantics_empty.xml", "flutter_semantics_empty.xml", "flutter_sinau.xml")
	var out struct {
		Launched string `json:"launched"`
		WaitedMs int    `json:"waitedMs"`
	}
	decode(t, f.ok("mobile_launch_app", `{"packageName":"dev.sinau.sinau_mobile"}`), &out)
	if out.Launched != "dev.sinau.sinau_mobile" || out.WaitedMs != 1200 {
		t.Errorf("out = %+v: three empty reads, then the tree", out)
	}
	if n, sum := sl.total(); n != 3 || sum != 1200*time.Millisecond || sd.count() != 4 {
		t.Errorf("%d waits of %v, %d reads", n, sum, sd.count())
	}

	// A tree that is never built: the wait ends at 10 seconds, and the launch still worked.
	sl = &sleeps{}
	f, _ = seqFixture(t, sl, "flutter_semantics_empty.xml")
	decode(t, f.ok("mobile_launch_app", `{"packageName":"dev.sinau.sinau_mobile"}`), &out)
	if n, sum := sl.total(); out.WaitedMs != 10000 || sum != 10*time.Second || n != 25 {
		t.Errorf("waited %d ms in %d waits of %v in all", out.WaitedMs, n, sum)
	}
}

func TestLaunchOfAPlainAppDoesNotWait(t *testing.T) {
	sl := &sleeps{}
	f, _ := seqFixture(t, sl, "views.xml")
	res := textOf(f.ok("mobile_launch_app", `{"packageName":"com.example.shop"}`))
	if strings.Contains(res, "waited") {
		t.Errorf("result = %q", res)
	}
	if n, _ := sl.total(); n != 0 {
		t.Errorf("%d waits", n)
	}
}

func TestLaunchWarmUpEndsWhenTheCallIsCancelled(t *testing.T) {
	sl := &sleeps{}
	f, _ := seqFixture(t, sl, "flutter_semantics_empty.xml")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := f.tools["mobile_launch_app"]
	if _, err := tool.Handler(ctx, callOf("mobile_launch_app", `{"packageName":"dev.sinau.sinau_mobile"}`)); err != nil {
		t.Fatal(err)
	}
	if n, _ := sl.total(); n > 1 {
		t.Errorf("%d waits after the cancel", n)
	}
}

func callOf(name, argsJSON string) mcp.Call {
	return mcp.Call{Name: name, Args: json.RawMessage(argsJSON), Progress: func(float64, float64, string) {}}
}

// slowDev is a device whose dump of the UI tree takes four seconds on the clock of the fixture.
type slowDev struct {
	*seqDev
	f    *fixture
	step time.Duration
}

func (d *slowDev) Source(ctx context.Context) (*mobile.Tree, error) {
	d.f.now = d.f.now.Add(d.step) // the handler runs in the goroutine of the test, so this is not shared
	return d.seqDev.Source(ctx)
}

func TestLaunchWarmUpIsBoundedByTheWallClockNotByTheCountOfPauses(t *testing.T) {
	sl := &sleeps{}
	sd := &seqDev{trees: []*mobile.Tree{retree(t, "flutter_semantics_empty.xml", "da", nil)}}
	var slow *slowDev
	f := newFixture(t, sl.opt, func(o *mobilemcp.Options, fx *fixture) {
		sd.fakeDev = fx.dev
		slow = &slowDev{seqDev: sd, f: fx, step: 4 * time.Second}
		o.Opener = func(context.Context, string) (mobilemcp.Device, func(context.Context) error, error) {
			return slow, func(context.Context) error { return nil }, nil
		}
	})
	var out struct {
		Launched string `json:"launched"`
		WaitedMs int    `json:"waitedMs"`
	}
	decode(t, f.ok("mobile_launch_app", `{"packageName":"dev.sinau.sinau_mobile"}`), &out)
	// Every read costs 4 s of wall clock. With pauses counted alone, the loop would run 25 times (10 s of
	// 400 ms pauses). On the clock it ends after three reads, at 12 s.
	if n, _ := sl.total(); n > 2 || sd.count() > 3 {
		t.Errorf("%d pauses and %d reads: the wall-clock budget was not kept", n, sd.count())
	}
	if out.Launched != "dev.sinau.sinau_mobile" || out.WaitedMs < 10000 || out.WaitedMs > 13000 {
		t.Errorf("out = %+v", out)
	}
}

func TestListingWaitsForAWebViewToBeFilled(t *testing.T) {
	sl := &sleeps{}
	f, sd := seqFixture(t, sl, "webview_empty.xml", "webview_loaded.xml")
	got := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(got, `button  "Continue"`) {
		t.Fatalf("the listing has no button of the page:\n%s", got)
	}
	if n, sum := sl.total(); n != 1 || sum != 400*time.Millisecond || sd.count() != 2 {
		t.Errorf("%d waits of %v in all, %d reads: want one wait of 400 ms and two reads", n, sum, sd.count())
	}
}

func TestListingGivesUpOnAWebViewThatStaysEmpty(t *testing.T) {
	sl := &sleeps{}
	f, sd := seqFixture(t, sl, "webview_empty.xml")
	f.ok("mobile_list_elements_on_screen", "")
	if n, _ := sl.total(); n != 3 || sd.count() != 4 {
		t.Errorf("%d waits, %d reads: want 3 waits and 4 reads", n, sd.count())
	}
}

func TestListingReadsASmallDocumentOnce(t *testing.T) {
	sl := &sleeps{}
	sd := &seqDev{trees: []*mobile.Tree{retree(t, "webview_empty.xml", "da", func(tr *mobile.Tree) {
		// A document of a twentieth of the screen is a banner, not a page that is still loading.
		doc := findNode(t, tr, func(n *mobile.Node) bool { return n.Role == "document" })
		doc.Bounds = mobile.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 400}
	})}}
	f := newFixture(t, sl.opt, func(o *mobilemcp.Options, fx *fixture) {
		sd.fakeDev = fx.dev
		o.Opener = func(context.Context, string) (mobilemcp.Device, func(context.Context) error, error) {
			return sd, func(context.Context) error { return nil }, nil
		}
	})
	f.ok("mobile_list_elements_on_screen", "")
	if n, _ := sl.total(); n != 0 || sd.count() != 1 {
		t.Errorf("%d waits, %d reads: a small document is read once", n, sd.count())
	}
}
