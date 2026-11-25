package mobilemcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mobilemcp"
)

var loginBox = mobile.Rect{Left: 0, Top: 1560, Right: 1080, Bottom: 1680}

func lastTouch(t *testing.T, f *fixture) mobile.Gesture {
	t.Helper()
	f.dev.mu.Lock()
	defer f.dev.mu.Unlock()
	if len(f.dev.touches) == 0 {
		t.Fatal("no touch reached the device")
	}
	return f.dev.touches[len(f.dev.touches)-1]
}

func TestParseRef(t *testing.T) {
	for in, want := range map[string]int{"e1": 1, "@e12": 12, "E3": 3, " e7 ": 7, "@E40": 40} {
		if got, err := mobilemcp.ParseRef(in); err != nil || got != want {
			t.Errorf("%q = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "e", "e0", "e-1", "e01", "x3", "3", "@@e3", "e3x", "button3", "e 3"} {
		if got, err := mobilemcp.ParseRef(in); err == nil {
			t.Errorf("%q was accepted as %d", in, got)
		}
	}
}

func TestListingGroupsByWindow(t *testing.T) {
	f := newFixture(t)
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	lines := strings.Split(text, "\n")
	if lines[0] != "screen 1080x2400, 4 elements" {
		t.Errorf("header = %q", lines[0])
	}
	idx := func(prefix string) int {
		for i, l := range lines {
			if strings.HasPrefix(l, prefix) {
				return i
			}
		}
		t.Fatalf("no line starts with %q in\n%s", prefix, text)
		return -1
	}
	if !(idx("window app com.shop") < idx("e1 ") && idx("e3 ") < idx("window keyboard com.kb") && idx("window keyboard com.kb") < idx("e4 ")) {
		t.Errorf("the windows are not grouped:\n%s", text)
	}
	want := `e3  button  "Sign in"  id=login_button  center=(540,1620)  bounds=[0,1560][1080,1680]  [clickable,enabled]`
	if !strings.Contains(text, want) {
		t.Errorf("no line %q in\n%s", want, text)
	}
	if !strings.Contains(text, `e2  textbox  ""  id=email  hint="Email"  center=(540,460)`) || !strings.Contains(text, "[editable,enabled]") {
		t.Errorf("the field line is wrong:\n%s", text)
	}
	if !strings.Contains(text, `e4  button  "q"  id=key_q`) {
		t.Errorf("the key line is wrong:\n%s", text)
	}
}

func TestListingWhenTheKeyboardIsUnknownGuessesOverlay(t *testing.T) {
	f := newFixture(t)
	f.dev.imeErr = errBoom
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(text, "window overlay com.kb") {
		t.Errorf("listing:\n%s", text)
	}
	if strings.Contains(text, "window keyboard") {
		t.Errorf("a keyboard was named with no IME:\n%s", text)
	}
}

func TestListingJSONAndRaw(t *testing.T) {
	f := newFixture(t)
	var l struct {
		Width, Height int
		Elements      []struct {
			Ref      string
			Center   struct{ X, Y int }
			Window   string
			Package  string
			Text     string
			Editable bool
			Focused  bool
			Enabled  bool
		}
	}
	decode(t, f.ok("mobile_list_elements_on_screen", `{"format":"json"}`), &l)
	if l.Width != 1080 || len(l.Elements) != 4 {
		t.Fatalf("listing = %+v", l)
	}
	if e := l.Elements[2]; e.Ref != "e3" || e.Center.X != 540 || e.Center.Y != 1620 || e.Window != "app" || e.Package != "com.shop" || e.Text != "Sign in" || !e.Enabled {
		t.Errorf("e3 = %+v", e)
	}
	if e := l.Elements[3]; e.Window != "keyboard" || e.Package != "com.kb" {
		t.Errorf("e4 = %+v", e)
	}
	if !l.Elements[1].Editable {
		t.Errorf("the field is not editable: %+v", l.Elements[1])
	}

	var raw struct {
		Width     int
		Truncated bool
		Tree      struct {
			Class    string
			Children []json.RawMessage
		}
	}
	decode(t, f.ok("mobile_list_elements_on_screen", `{"raw":true}`), &raw)
	if raw.Width != 1080 || raw.Tree.Class != "hierarchy" || len(raw.Tree.Children) != 2 || raw.Truncated {
		t.Errorf("raw = %+v", raw)
	}
	f.fails("mobile_list_elements_on_screen", `{"format":"xml"}`, "text or json")
}

func TestRefWithTheSameScreenUsesTheListedCenter(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_list_elements_on_screen", "")
	for _, ref := range []string{"e3", "@e3", "E3"} {
		res := f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+ref+`"}`)
		g := lastTouch(t, f)
		if g.From != (mobile.Point{X: 540, Y: 1620}) || g.To != g.From || g.Taps != 0 {
			t.Errorf("%s: gesture = %+v", ref, g)
		}
		if !strings.Contains(textOf(res), "e3 at (540,1620)") {
			t.Errorf("%s: result = %s", ref, textOf(res))
		}
	}
}

func TestRefIsFoundAgainWhenTheScreenChanged(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_list_elements_on_screen", "")

	// Another digest, the same layout.
	f.dev.setTree(screenTree("d2", loginBox, true))
	res := f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"e3"}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 540, Y: 1620}) {
		t.Errorf("same layout: gesture = %+v", g)
	}
	if !strings.Contains(textOf(res), "found again") {
		t.Errorf("result = %s", textOf(res))
	}

	// The button moved: the tap goes to its new center.
	moved := mobile.Rect{Left: 0, Top: 1400, Right: 1080, Bottom: 1520}
	f.dev.setTree(screenTree("d3", moved, true))
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"e3"}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 540, Y: 1460}) {
		t.Errorf("moved: gesture = %+v", g)
	}
}

func TestStaleRefShowsTheNewScreenAndItsRefsWork(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_list_elements_on_screen", "")
	before := f.dev.called("Touch")
	f.dev.setTree(screenTree("d4", loginBox, false))
	msg := f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"e3"}`, "ref e3 is stale: the screen changed. Call mobile_list_elements_on_screen again")
	if !strings.Contains(msg, "window app com.shop") || strings.Contains(msg, "Sign in") {
		t.Errorf("the error does not show the new screen:\n%s", msg)
	}
	if f.dev.called("Touch") != before {
		t.Error("a stale ref reached the device")
	}
	// The refs of the new listing are good, with no new call to list.
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"e2"}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 540, Y: 460}) {
		t.Errorf("gesture = %+v", g)
	}
}

func TestRefErrors(t *testing.T) {
	f := newFixture(t)
	f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"e1"}`, "no refs yet")
	f.ok("mobile_list_elements_on_screen", "")
	f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"e99"}`, "does not exist")
	f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"button3"}`, "not a ref")
	f.fails("mobile_click_on_screen_at_coordinates", `{}`, "x and y")
	f.fails("mobile_click_on_screen_at_coordinates", `{"x":10}`, "x and y")
	f.fails("mobile_click_on_screen_at_coordinates", `{"x":-1,"y":5}`, "not a point")
	if f.dev.called("Touch") != 0 {
		t.Errorf("calls = %v", f.dev.calls)
	}
}

func TestRefWinsOverCoordinates(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_list_elements_on_screen", "")
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"e3","x":1,"y":2}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 540, Y: 1620}) {
		t.Errorf("gesture = %+v", g)
	}
	f.ok("mobile_click_on_screen_at_coordinates", `{"x":1.4,"y":2.6}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 1, Y: 3}) {
		t.Errorf("rounded gesture = %+v", g)
	}
	// A stale ref is an error even with coordinates: it is not silently replaced.
	f.dev.setTree(screenTree("d9", loginBox, false))
	f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"e3","x":1,"y":2}`, "stale")
}

// twoButtons is a screen whose app window and keyboard window each have a button of the same class,
// id, and description.
func twoButtons(digest string, app, kb bool, appBox, kbBox mobile.Rect) *mobile.Tree {
	btn := func(pkg string, b mobile.Rect) *mobile.Node {
		return &mobile.Node{Class: "android.widget.Button", Role: "button", Package: pkg, ID: "shared:id/ok", Desc: "OK", Enabled: true, Bounds: b,
			Attrs: map[string]string{"clickable": "true"}}
	}
	full := mobile.Rect{Right: 1080, Bottom: 2400}
	win := func(pkg string, kids ...*mobile.Node) *mobile.Node {
		return &mobile.Node{Class: "android.widget.FrameLayout", Package: pkg, Enabled: true, Bounds: full, Children: kids}
	}
	var wins []*mobile.Node
	appKids := []*mobile.Node{{Class: "android.widget.TextView", Package: "com.shop", Text: "Hi", Enabled: true, Bounds: mobile.Rect{Right: 100, Bottom: 50}, Attrs: map[string]string{"text": "Hi"}}}
	if app {
		appKids = append(appKids, btn("com.shop", appBox))
	}
	wins = append(wins, win("com.shop", appKids...))
	if kb {
		wins = append(wins, win("com.kb", btn("com.kb", kbBox)))
	}
	t := &mobile.Tree{Root: &mobile.Node{Class: "hierarchy", Bounds: full, Children: wins}, Width: 1080, Height: 2400}
	t.Finish()
	t.Digest = digest
	return t
}

func TestRefIsFoundOnlyInItsOwnWindow(t *testing.T) {
	box := mobile.Rect{Left: 100, Top: 1000, Right: 300, Bottom: 1100}
	f := newFixture(t)
	f.dev.setTree(twoButtons("a1", true, true, box, mobile.Rect{Left: 500, Top: 1900, Right: 700, Bottom: 2000}))
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(text, "e2  button") || !strings.Contains(text, "e3  button") {
		t.Fatalf("listing:\n%s", text)
	}
	// The app button (e2) is gone and the keyboard still has a button just like it.
	f.dev.setTree(twoButtons("a2", false, true, box, mobile.Rect{Left: 500, Top: 1900, Right: 700, Bottom: 2000}))
	f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"e2"}`, "stale")
	if f.dev.called("Touch") != 0 {
		t.Error("the tap went to the keyboard button")
	}
	// The other way: the keyboard ref still finds the keyboard button.
	f.dev.setTree(twoButtons("a1", true, true, box, mobile.Rect{Left: 500, Top: 1900, Right: 700, Bottom: 2000}))
	f.ok("mobile_list_elements_on_screen", "")
	f.dev.setTree(twoButtons("a3", true, true, box, mobile.Rect{Left: 500, Top: 1800, Right: 700, Bottom: 1900}))
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"e3"}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 600, Y: 1850}) {
		t.Errorf("gesture = %+v", g)
	}
}

func TestSeveralMatchesPreferTheSameBoxElseStale(t *testing.T) {
	// Two app buttons that differ only by their place.
	two := func(digest string, a, b mobile.Rect) *mobile.Tree {
		btn := func(r mobile.Rect) *mobile.Node {
			return &mobile.Node{Class: "android.widget.Button", Role: "button", Package: "com.shop", ID: "x:id/row", Desc: "Row", Enabled: true, Bounds: r,
				Attrs: map[string]string{"clickable": "true"}}
		}
		full := mobile.Rect{Right: 1080, Bottom: 2400}
		win := &mobile.Node{Class: "android.widget.FrameLayout", Package: "com.shop", Enabled: true, Bounds: full, Children: []*mobile.Node{btn(a), btn(b)}}
		tr := &mobile.Tree{Root: &mobile.Node{Class: "hierarchy", Bounds: full, Children: []*mobile.Node{win}}, Width: 1080, Height: 2400}
		tr.Finish()
		tr.Digest = digest
		return tr
	}
	top := mobile.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 200}
	bottom := mobile.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 400}
	f := newFixture(t)
	f.dev.setTree(two("r1", top, bottom))
	f.ok("mobile_list_elements_on_screen", "")

	// Only the first one moved: the second keeps its box, so e2 is found and e1 is not.
	f.dev.setTree(two("r2", mobile.Rect{Left: 0, Top: 120, Right: 1080, Bottom: 220}, bottom))
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"e2"}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 540, Y: 350}) {
		t.Errorf("gesture = %+v", g)
	}
	f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"e1"}`, "stale")
}

func TestListingAccessibilityNotesCleanAndFailing(t *testing.T) {
	makeTree := func(kids ...*mobile.Node) *mobile.Tree {
		full := mobile.Rect{Right: 1080, Bottom: 2400}
		win := &mobile.Node{Class: "android.widget.FrameLayout", Package: "com.shop", Enabled: true, Bounds: full, Children: kids}
		tr := &mobile.Tree{Root: &mobile.Node{Class: "hierarchy", Bounds: full, Children: []*mobile.Node{win}}, Width: 1080, Height: 2400}
		tr.Finish()
		return tr
	}
	// Clean tree: 200x160 button at 420 dpi is ~76x61dp and labeled.
	f := newFixture(t)
	f.dev.setTree(makeTree(&mobile.Node{
		Class: "android.widget.Button", Role: "button", Package: "com.shop", ID: "com.shop:id/ok", Text: "Continue", Enabled: true,
		Bounds: mobile.Rect{Left: 100, Top: 400, Right: 300, Bottom: 560}, Attrs: map[string]string{"clickable": "true"},
	}))
	cleanText := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(cleanText, "note: accessibility:") {
		t.Errorf("clean screen had an accessibility note:\n%s", cleanText)
	}

	// Failing tree: unlabeled icon button + tiny labeled button + extra issues to test truncation & secret scrubbing.
	f.scrub.Add("secret-token-xyz")
	f.dev.setTree(makeTree(
		&mobile.Node{Class: "android.widget.ImageButton", Role: "button", Package: "com.shop", ID: "com.shop:id/icon_secret-token-xyz", Enabled: true,
			Bounds: mobile.Rect{Left: 100, Top: 200, Right: 300, Bottom: 400}, Attrs: map[string]string{"clickable": "true"}},
		&mobile.Node{Class: "android.widget.Button", Role: "button", Package: "com.shop", ID: "com.shop:id/close", Desc: "Close", Enabled: true,
			Bounds: mobile.Rect{Left: 400, Top: 200, Right: 480, Bottom: 280}, Attrs: map[string]string{"clickable": "true"}}, // 80px @ 420dpi = 30dp
		&mobile.Node{Class: "android.widget.ImageButton", Role: "button", Package: "com.shop", Enabled: true,
			Bounds: mobile.Rect{Left: 100, Top: 500, Right: 160, Bottom: 560}, Attrs: map[string]string{"clickable": "true"}}, // missing + small
	))
	got := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(got, "note: accessibility:") {
		t.Fatalf("expected accessibility note:\n%s", got)
	}
	if strings.Contains(got, "secret-token-xyz") {
		t.Errorf("secret leaked into accessibility note:\n%s", got)
	}
	if !strings.Contains(got, "2 missing labels") || !strings.Contains(got, "2 small touch targets (<48dp)") || !strings.Contains(got, "and 1 more") {
		t.Errorf("unexpected accessibility note:\n%s", got)
	}
	if !strings.Contains(got, "e1 ") || !strings.Contains(got, `e2 "Close" (30x30dp)`) {
		t.Errorf("expected eN refs in accessibility note:\n%s", got)
	}
}

func TestScreenDensityRetriesAfterTransientScreenSizeError(t *testing.T) {
	full := mobile.Rect{Right: 1080, Bottom: 2400}
	small := &mobile.Node{
		Class: "android.widget.Button", Role: "button", Package: "com.shop", ID: "com.shop:id/tiny", Desc: "Tiny", Enabled: true,
		Bounds: mobile.Rect{Left: 400, Top: 200, Right: 480, Bottom: 280}, Attrs: map[string]string{"clickable": "true"},
	}
	win := &mobile.Node{Class: "android.widget.FrameLayout", Package: "com.shop", Enabled: true, Bounds: full, Children: []*mobile.Node{small}}
	tr := &mobile.Tree{Root: &mobile.Node{Class: "hierarchy", Bounds: full, Children: []*mobile.Node{win}}, Width: 1080, Height: 2400}
	tr.Finish()

	f := newFixture(t)
	f.dev.setTree(tr)
	f.dev.err["ScreenSize"] = errBoom

	// First call: ScreenSize fails transiently -> small-target check skipped, NOT permanently cached as 0.
	first := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(first, "small touch target") {
		t.Errorf("unexpected small target note when ScreenSize failed:\n%s", first)
	}

	// Second call: ScreenSize succeeds -> small-target check runs and density is cached.
	delete(f.dev.err, "ScreenSize")
	second := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(second, "1 small touch target (<48dp)") {
		t.Errorf("small target note missing after ScreenSize recovered:\n%s", second)
	}
	// Third call uses cached density without calling ScreenSize again.
	before := f.dev.called("ScreenSize")
	f.ok("mobile_list_elements_on_screen", "")
	if after := f.dev.called("ScreenSize"); after != before {
		t.Errorf("ScreenSize called again after caching: %d -> %d", before, after)
	}
}

func TestA11yNoteDoesNotMisattributeRefOnSameBoundsWrapper(t *testing.T) {
	full := mobile.Rect{Right: 1080, Bottom: 2400}
	sameBounds := mobile.Rect{Left: 100, Top: 200, Right: 300, Bottom: 400}
	// A checkable node with no label shares identical bounds with a listed TextView sibling.
	// ListElements lists only the TextView (e1), NOT the unlabeled checkable node, while
	// CheckAccessibility flags the checkable node as missing_label. refForIssue must match by
	// exact *mobile.Node pointer so it omits e1 rather than pointing at the sibling TextView.
	listed := &mobile.Node{
		Class: "android.widget.TextView", Role: "text", Package: "com.shop", Text: "Label", Enabled: true,
		Bounds: sameBounds, Attrs: map[string]string{"text": "Label"},
	}
	unlisted := &mobile.Node{
		Class: "android.widget.CheckBox", Package: "com.shop", ID: "com.shop:id/toggle", Enabled: true,
		Bounds: sameBounds, Attrs: map[string]string{"checkable": "true"},
	}
	win := &mobile.Node{Class: "android.widget.FrameLayout", Package: "com.shop", Enabled: true, Bounds: full, Children: []*mobile.Node{listed, unlisted}}
	tr := &mobile.Tree{Root: &mobile.Node{Class: "hierarchy", Bounds: full, Children: []*mobile.Node{win}}, Width: 1080, Height: 2400}
	tr.Finish()

	f := newFixture(t)
	f.dev.setTree(tr)
	got := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(got, "note: accessibility: 1 missing label: id=toggle (missing label)") {
		t.Errorf("expected checkable issue without misattributed ref, got:\n%s", got)
	}
	if strings.Contains(got, "e1 id=toggle") {
		t.Errorf("unlisted node stole listed sibling's e1 ref:\n%s", got)
	}
}
