package mobilemcp_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mobilemcp"
)

func f64(v float64) *float64 { return &v }

// P is a point.
func P(x, y int) mobile.Point { return mobile.Point{X: x, Y: y} }

func TestTapsAndHolds(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_click_on_screen_at_coordinates", `{"x":100,"y":200}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 100, Y: 200}) || g.To != g.From || g.Taps != 0 || g.Hold != 0 {
		t.Errorf("click = %+v", g)
	}
	f.ok("mobile_double_tap_on_screen", `{"x":100,"y":200}`)
	if g := lastTouch(t, f); g.Taps != 2 || g.From != g.To {
		t.Errorf("double tap = %+v", g)
	}
	f.ok("mobile_long_press_on_screen_at_coordinates", `{"x":10,"y":20}`)
	if g := lastTouch(t, f); g.Hold != 500*time.Millisecond || g.From != g.To {
		t.Errorf("long press = %+v", g)
	}
	f.ok("mobile_long_press_on_screen_at_coordinates", `{"x":10,"y":20,"duration":1500}`)
	if g := lastTouch(t, f); g.Hold != 1500*time.Millisecond {
		t.Errorf("long press = %+v", g)
	}
	f.fails("mobile_long_press_on_screen_at_coordinates", `{"x":10,"y":20,"duration":10001}`, "duration")
	f.fails("mobile_long_press_on_screen_at_coordinates", `{"x":10,"y":20,"duration":-5}`, "duration")

	f.ok("mobile_list_elements_on_screen", "")
	f.ok("mobile_double_tap_on_screen", `{"ref":"e3"}`)
	if g := lastTouch(t, f); g.Taps != 2 || g.From != (mobile.Point{X: 540, Y: 1620}) {
		t.Errorf("double tap on a ref = %+v", g)
	}
	f.ok("mobile_long_press_on_screen_at_coordinates", `{"ref":"@e3","duration":900}`)
	if g := lastTouch(t, f); g.Hold != 900*time.Millisecond || g.From != (mobile.Point{X: 540, Y: 1620}) {
		t.Errorf("long press on a ref = %+v", g)
	}
}

func TestDrag(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_drag_on_screen", `{"x1":10,"y1":20,"x2":300,"y2":400}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 10, Y: 20}) || g.To != (mobile.Point{X: 300, Y: 400}) || g.Hold != 0 || g.Move == 0 {
		t.Errorf("drag = %+v", g)
	}
	f.ok("mobile_drag_on_screen", `{"x1":10,"y1":20,"x2":300,"y2":400,"hold":800}`)
	if g := lastTouch(t, f); g.Hold != 800*time.Millisecond {
		t.Errorf("drag with hold = %+v", g)
	}
	f.ok("mobile_list_elements_on_screen", "")
	f.ok("mobile_drag_on_screen", `{"fromRef":"e3","toRef":"e2"}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 540, Y: 1620}) || g.To != (mobile.Point{X: 540, Y: 460}) {
		t.Errorf("drag by refs = %+v", g)
	}
	f.ok("mobile_drag_on_screen", `{"fromRef":"e3","x2":5,"y2":6}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 540, Y: 1620}) || g.To != (mobile.Point{X: 5, Y: 6}) {
		t.Errorf("a ref and a point = %+v", g)
	}
	f.fails("mobile_drag_on_screen", `{"x1":10,"y1":20}`, "end of the drag")
	f.fails("mobile_drag_on_screen", `{"x2":10,"y2":20}`, "start of the drag")
	f.fails("mobile_drag_on_screen", `{"x1":1,"y1":2,"x2":3,"y2":4,"hold":10001}`, "hold")
	f.fails("mobile_drag_on_screen", `{"fromRef":"e99","toRef":"e2"}`, "does not exist")
}

func TestSwipePath(t *testing.T) {
	cases := []struct {
		name     string
		w, h     int
		dir      string
		x, y     *float64
		dist     float64
		from, to mobile.Point
		wantErr  string
	}{
		{name: "up from the center, 30% of the height", w: 1080, h: 2400, dir: "up", from: P(540, 1200), to: P(540, 480)},
		{name: "down", w: 1080, h: 2400, dir: "DOWN", from: P(540, 1200), to: P(540, 1920)},
		{name: "left, 30% of the width", w: 1080, h: 2400, dir: "left", from: P(540, 1200), to: P(216, 1200)},
		{name: "right", w: 1080, h: 2400, dir: "right", from: P(540, 1200), to: P(864, 1200)},
		{name: "from a point", w: 1080, h: 2400, dir: "up", x: f64(300), y: f64(1000), from: P(300, 1000), to: P(300, 280)},
		{name: "a distance", w: 1080, h: 2400, dir: "up", dist: 500, from: P(540, 1200), to: P(540, 700)},
		{name: "the end is kept inside the 5% margin", w: 1080, h: 2400, dir: "up", y: f64(200), dist: 900, from: P(540, 200), to: P(540, 120)},
		{name: "the start is kept inside the margin", w: 1080, h: 2400, dir: "down", x: f64(0), y: f64(2399), dist: 100, wantErr: "no room"},
		{name: "a start on the edge moves in", w: 1080, h: 2400, dir: "right", x: f64(0), y: f64(500), dist: 200, from: P(54, 500), to: P(254, 500)},
		{name: "a far distance stops at the margin", w: 1080, h: 2400, dir: "left", dist: 99999, from: P(540, 1200), to: P(54, 1200)},
		{name: "landscape", w: 2400, h: 1080, dir: "left", from: P(1200, 540), to: P(480, 540)},
		{name: "no room", w: 1080, h: 2400, dir: "right", x: f64(5000), y: f64(500), wantErr: "no room"},
		{name: "a bad direction", w: 1080, h: 2400, dir: "north", wantErr: "not up, down"},
		{name: "no screen", w: 0, h: 0, dir: "up", wantErr: "not known"},
	}
	for _, c := range cases {
		from, to, err := mobilemcp.SwipePath(c.w, c.h, c.dir, c.x, c.y, c.dist)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err = %v", c.name, err)
			}
			continue
		}
		if err != nil || from != c.from || to != c.to {
			t.Errorf("%s: %v -> %v, %v; want %v -> %v", c.name, from, to, err, c.from, c.to)
		}
	}
}

func TestSwipeTool(t *testing.T) {
	f := newFixture(t)
	res := f.ok("mobile_swipe_on_screen", `{"direction":"up"}`)
	g := lastTouch(t, f)
	if g.From != (mobile.Point{X: 540, Y: 1200}) || g.To != (mobile.Point{X: 540, Y: 480}) || g.Move != 300*time.Millisecond || g.Hold != 0 {
		t.Errorf("swipe = %+v", g)
	}
	if !strings.Contains(textOf(res), "swiped up from (540,1200) to (540,480)") {
		t.Errorf("result = %s", textOf(res))
	}
	f.ok("mobile_swipe_on_screen", `{"direction":"left","x":900,"y":300,"distance":400}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 900, Y: 300}) || g.To != (mobile.Point{X: 500, Y: 300}) {
		t.Errorf("swipe = %+v", g)
	}
	f.fails("mobile_swipe_on_screen", `{"direction":"sideways"}`, "direction")
	f.fails("mobile_swipe_on_screen", `{"direction":"up","distance":-3}`, "distance")
	f.fails("mobile_swipe_on_screen", `{"direction":"up","x":-3}`, "0 or more")
	// The size of the screen comes from the tree, or from the device when the tree gives none.
	f.dev.tree.Width, f.dev.tree.Height = 0, 0
	f.ok("mobile_swipe_on_screen", `{"direction":"down"}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 540, Y: 1200}) || g.To != (mobile.Point{X: 540, Y: 1920}) {
		t.Errorf("swipe with the device size = %+v", g)
	}
}

func TestTypeKeys(t *testing.T) {
	f := newFixture(t)
	d := f.dev
	f.ok("mobile_type_keys", `{"text":"hello"}`)
	if len(d.typed) != 1 || d.typed[0] != "hello" || d.called("TypeFocused 5 false") != 1 || d.called("Touch") != 0 {
		t.Errorf("typed = %v, calls = %v", d.typed, d.calls)
	}
	f.ok("mobile_type_keys", `{"text":"x","replace":true,"submit":true}`)
	if d.called("TypeFocused 1 true") != 1 || len(d.keys) != 1 || d.keys[0] != 66 {
		t.Errorf("calls = %v keys = %v", d.calls, d.keys)
	}
	f.ok("mobile_list_elements_on_screen", "")
	res := f.ok("mobile_type_keys", `{"text":"a@b.c","ref":"e2"}`)
	if g := lastTouch(t, f); g.From != (mobile.Point{X: 540, Y: 460}) {
		t.Errorf("the field was not tapped first: %+v", g)
	}
	if !strings.Contains(textOf(res), "in e2") || strings.Contains(textOf(res), "a@b.c") {
		t.Errorf("result = %s", textOf(res))
	}
	f.fails("mobile_type_keys", `{}`, "text to type")
	f.fails("mobile_type_keys", `{"text":"a","secretEnv":"SHOP_PASSWORD"}`, "not both")
	f.fails("mobile_type_keys", `{"secretEnv":"NOT_SET_ANYWHERE"}`, "not set")
	f.fails("mobile_type_keys", `{"secretEnv":"PATH"}`, "reserved")
	f.fails("mobile_type_keys", `{"text":"a","ref":"e99"}`, "does not exist")
	d.err["TypeFocused"] = errBoom
	f.fails("mobile_type_keys", `{"text":"a"}`, "boom")
	d.err["TypeFocused"] = nil
	d.err["Key"] = errBoom
	f.fails("mobile_type_keys", `{"text":"a","submit":true}`, "Enter failed")
}

func TestTypeKeysWithASecretNeverShowsIt(t *testing.T) {
	f := newFixture(t)
	res := f.ok("mobile_type_keys", `{"secretEnv":"SHOP_PASSWORD","replace":true}`)
	if len(f.dev.typed) != 1 || f.dev.typed[0] != "s3cret-Pa55word" {
		t.Errorf("typed = %v", f.dev.typed)
	}
	if strings.Contains(textOf(res), "s3cret-Pa55word") || !strings.Contains(textOf(res), "SHOP_PASSWORD") {
		t.Errorf("result = %s", textOf(res))
	}
	// The value is known from now on: a screen that shows it is scrubbed and is not captured.
	f.dev.setTree(textTree("t1", "Your password is s3cret-Pa55word"))
	listing := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(listing, "s3cret-Pa55word") || !strings.Contains(listing, "[redacted:") {
		t.Errorf("listing = %s", listing)
	}
	f.fails("mobile_take_screenshot", "", "secret value")
}

// textTree is a screen of one text.
func textTree(digest, text string) *mobile.Tree {
	full := mobile.Rect{Right: 1080, Bottom: 2400}
	n := &mobile.Node{Class: "android.widget.TextView", Role: "text", Package: "com.shop", Text: text, Enabled: true, Bounds: mobile.Rect{Left: 10, Top: 10, Right: 900, Bottom: 100}, Attrs: map[string]string{"text": text}}
	win := &mobile.Node{Class: "android.widget.FrameLayout", Package: "com.shop", Enabled: true, Bounds: full, Children: []*mobile.Node{n}}
	t := &mobile.Tree{Root: &mobile.Node{Class: "hierarchy", Bounds: full, Children: []*mobile.Node{win}}, Width: 1080, Height: 2400}
	t.Finish()
	t.Digest = digest
	return t
}
