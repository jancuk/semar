package mobilemcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/mobilemcp"
)

// fakeDev is a Device with no adb. It records every call, and each answer can be set.
type fakeDev struct {
	mu    sync.Mutex
	calls []string

	serial   string
	emulator bool
	remote   bool

	tree     *mobile.Tree
	treeErr  error
	shot     []byte
	touches  []mobile.Gesture
	typed    []string
	keys     []int
	screen   android.ScreenInfo
	orient   string
	apps     []string
	fgPkg    string
	fgAct    string
	clip     string
	logs     []android.LogEntry
	logQuery android.LogQuery
	crashes  []android.Crash
	crashErr error
	recPath  string
	recOpts  android.RecOptions
	imePkg   string
	imeErr   error
	err      map[string]error // by method name: the error that the method gives

	// inflight and peak measure how many calls run at once.
	inflight, peak atomic.Int32
	delay          time.Duration
}

func newFake() *fakeDev {
	return &fakeDev{
		serial: "emulator-5554", emulator: true,
		tree:   screenTree("d1", mobile.Rect{Left: 0, Top: 1560, Right: 1080, Bottom: 1680}, true),
		shot:   pngBytes(1080, 2400),
		screen: android.ScreenInfo{Width: 1080, Height: 2400, Density: 420},
		orient: "portrait",
		imePkg: "com.kb",
		apps:   []string{"com.example.shop"},
		err:    map[string]error{},
	}
}

func (d *fakeDev) enter(name string, args ...any) error {
	n := d.inflight.Add(1)
	for {
		p := d.peak.Load()
		if n <= p || d.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if d.delay > 0 {
		time.Sleep(d.delay)
	}
	d.inflight.Add(-1)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, strings.TrimSpace(name+" "+fmt.Sprintln(args...)))
	return d.err[name]
}

func (d *fakeDev) called(prefix string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, c := range d.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (d *fakeDev) setTree(t *mobile.Tree) {
	d.mu.Lock()
	d.tree = t
	d.mu.Unlock()
}

func (d *fakeDev) Serial() string { return d.serial }
func (d *fakeDev) Emulator() bool { return d.emulator }
func (d *fakeDev) Remote() bool   { return d.remote }
func (d *fakeDev) Source(context.Context) (*mobile.Tree, error) {
	if err := d.enter("Source"); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tree, d.treeErr
}
func (d *fakeDev) Screenshot(context.Context) ([]byte, error) {
	if err := d.enter("Screenshot"); err != nil {
		return nil, err
	}
	return d.shot, nil
}
func (d *fakeDev) ScreenSize(context.Context) (android.ScreenInfo, error) {
	return d.screen, d.enter("ScreenSize")
}
func (d *fakeDev) Touch(_ context.Context, g mobile.Gesture) error {
	err := d.enter("Touch", g.From, g.To, g.Hold, g.Move, g.Taps)
	d.mu.Lock()
	d.touches = append(d.touches, g)
	d.mu.Unlock()
	return err
}
func (d *fakeDev) TypeFocused(_ context.Context, text string, replace bool) error {
	err := d.enter("TypeFocused", len(text), replace)
	d.mu.Lock()
	d.typed = append(d.typed, text)
	d.mu.Unlock()
	return err
}
func (d *fakeDev) Key(_ context.Context, code int) error {
	err := d.enter("Key", code)
	d.mu.Lock()
	d.keys = append(d.keys, code)
	d.mu.Unlock()
	return err
}
func (d *fakeDev) InputMethod(context.Context) (string, bool, error) {
	return d.imePkg, true, d.imeErr
}
func (d *fakeDev) Orientation(context.Context) (string, error) {
	return d.orient, d.enter("Orientation")
}
func (d *fakeDev) SetOrientation(_ context.Context, o string) error {
	d.orient = o
	return d.enter("SetOrientation", o)
}
func (d *fakeDev) Fold(_ context.Context, p string) error { return d.enter("Fold", p) }
func (d *fakeDev) SetLocation(_ context.Context, lat, lon float64) error {
	return d.enter("SetLocation", lat, lon)
}
func (d *fakeDev) ClearLocation(context.Context) error { return d.enter("ClearLocation") }
func (d *fakeDev) Clipboard(context.Context) (string, error) {
	return d.clip, d.enter("Clipboard")
}
func (d *fakeDev) SetClipboard(_ context.Context, t string) error {
	d.clip = t
	return d.enter("SetClipboard", len(t))
}
func (d *fakeDev) ListApps(_ context.Context, sys bool) ([]string, error) {
	return d.apps, d.enter("ListApps", sys)
}
func (d *fakeDev) Foreground(context.Context) (string, string, error) {
	return d.fgPkg, d.fgAct, d.enter("Foreground")
}
func (d *fakeDev) LaunchApp(_ context.Context, o android.LaunchOptions) error {
	return d.enter("LaunchApp", o.Package, o.Locales, o.Arguments)
}
func (d *fakeDev) TerminateApp(_ context.Context, p string) error { return d.enter("TerminateApp", p) }
func (d *fakeDev) InstallAPK(_ context.Context, p string) error {
	return d.enter("InstallAPK", filepath.Base(p))
}
func (d *fakeDev) UninstallApp(_ context.Context, p string) error { return d.enter("UninstallApp", p) }
func (d *fakeDev) ClearAppData(_ context.Context, p string) error { return d.enter("ClearAppData", p) }
func (d *fakeDev) OpenURL(_ context.Context, l string) error      { return d.enter("OpenURL", l) }
func (d *fakeDev) StartRecording(_ context.Context, o android.RecOptions) error {
	d.recOpts = o
	return d.enter("StartRecording")
}
func (d *fakeDev) StopRecording(context.Context) (string, error) {
	return d.recPath, d.enter("StopRecording")
}
func (d *fakeDev) Logs(_ context.Context, q android.LogQuery) ([]android.LogEntry, error) {
	d.logQuery = q
	return d.logs, d.enter("Logs")
}
func (d *fakeDev) ListCrashes(_ context.Context, pkg string, limit int) ([]android.Crash, error) {
	return d.crashes, d.enter("ListCrashes", pkg)
}
func (d *fakeDev) GetCrash(_ context.Context, id string) (android.Crash, error) {
	if err := d.enter("GetCrash", id); err != nil {
		return android.Crash{}, err
	}
	if d.crashErr != nil {
		return android.Crash{}, d.crashErr
	}
	for _, c := range d.crashes {
		if c.ID == id {
			return c, nil
		}
	}
	return android.Crash{}, android.ErrCrashNotFound
}

var _ mobilemcp.Device = (*fakeDev)(nil)

// screenTree is a screen of two windows: the app (a title, a login button, an email field) and the
// keyboard (one key). withLogin false leaves the login button out. Every call gives a new tree.
func screenTree(digest string, login mobile.Rect, withLogin bool) *mobile.Tree {
	node := func(pkg, class, role, id, text, desc, hint string, b mobile.Rect, attrs map[string]string) *mobile.Node {
		if attrs == nil {
			attrs = map[string]string{}
		}
		if text != "" {
			attrs["text"] = text
		}
		return &mobile.Node{Class: class, Role: role, Package: pkg, ID: id, Text: text, Desc: desc, Hint: hint, Enabled: true, Bounds: b, Attrs: attrs}
	}
	clickable := map[string]string{"clickable": "true"}
	appKids := []*mobile.Node{
		node("com.shop", "android.widget.TextView", "text", "com.shop:id/title", "Welcome back", "", "", mobile.Rect{Left: 100, Top: 200, Right: 980, Bottom: 300}, nil),
		node("com.shop", "android.widget.EditText", "textbox", "com.shop:id/email", "", "", "Email", mobile.Rect{Left: 100, Top: 400, Right: 980, Bottom: 520}, map[string]string{"editable": "true"}),
	}
	if withLogin {
		appKids = append(appKids, node("com.shop", "android.widget.Button", "button", "com.shop:id/login_button", "Sign in", "", "", login, clickable))
	}
	full := mobile.Rect{Right: 1080, Bottom: 2400}
	app := node("com.shop", "android.widget.FrameLayout", "", "", "", "", "", full, nil)
	app.Children = appKids
	kb := node("com.kb", "android.widget.FrameLayout", "", "", "", "", "", mobile.Rect{Top: 1800, Right: 1080, Bottom: 2400}, nil)
	kb.Children = []*mobile.Node{node("com.kb", "android.widget.Button", "button", "com.kb:id/key_q", "q", "q", "", mobile.Rect{Left: 0, Top: 1850, Right: 108, Bottom: 1950}, clickable)}
	root := &mobile.Node{Class: "hierarchy", Bounds: full, Enabled: true, Children: []*mobile.Node{app, kb}}
	t := &mobile.Tree{Root: root, Width: 1080, Height: 2400, Digest: digest}
	t.Finish()
	t.Digest = digest
	return t
}

var (
	pngOnce sync.Once
	pngData []byte
)

// pngBytes is a PNG of w by h, with a gradient so that a crop is not all one color.
func pngBytes(w, h int) []byte {
	if w == 1080 && h == 2400 {
		pngOnce.Do(func() { pngData = makePNG(w, h) })
		return pngData
	}
	return makePNG(w, h)
}

func makePNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// fixture is a Manager over a fake device.
type fixture struct {
	t       *testing.T
	m       *mobilemcp.Manager
	dev     *fakeDev
	tools   map[string]mcp.Tool
	out     *mcpkit.Outputs
	outDir  string
	realOut string
	opens   atomic.Int32
	closes  atomic.Int32
	scrub   *mcpkit.Scrubber
	now     time.Time
}

type fixOpt func(*mobilemcp.Options, *fixture)

func newFixture(t *testing.T, opts ...fixOpt) *fixture {
	t.Helper()
	f := &fixture{t: t, dev: newFake(), now: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)}
	f.outDir = filepath.Join(t.TempDir(), "mcp")
	out, err := mcpkit.NewOutputs(f.outDir)
	if err != nil {
		t.Fatal(err)
	}
	f.out = out
	f.realOut, _ = filepath.EvalSymlinks(f.outDir)
	f.scrub = mcpkit.NewScrubber(nil)
	o := mobilemcp.Options{
		Outputs: out, Scrub: f.scrub,
		Clock: func() time.Time { return f.now },
		Opener: func(_ context.Context, serial string) (mobilemcp.Device, func(context.Context) error, error) {
			f.opens.Add(1)
			if serial != "" {
				f.dev.serial = serial
			}
			return f.dev, func(context.Context) error { f.closes.Add(1); return nil }, nil
		},
		LookupEnv: func(name string) (string, bool) {
			if name == "SHOP_PASSWORD" {
				return "s3cret-Pa55word", true
			}
			return "", false
		},
	}
	for _, opt := range opts {
		opt(&o, f)
	}
	f.m = mobilemcp.New(o)
	f.tools = map[string]mcp.Tool{}
	for _, tool := range f.m.Tools() {
		f.tools[tool.Name] = tool
	}
	t.Cleanup(func() { _ = f.m.Close(context.Background()) })
	return f
}

// call runs a tool with JSON arguments and gives the result.
func (f *fixture) call(name, argsJSON string) mcp.Result {
	f.t.Helper()
	tool, ok := f.tools[name]
	if !ok {
		f.t.Fatalf("no tool %s", name)
	}
	if argsJSON == "" {
		argsJSON = "{}"
	}
	res, err := tool.Handler(context.Background(), mcp.Call{Name: name, Args: json.RawMessage(argsJSON), Progress: func(float64, float64, string) {}})
	if err != nil {
		f.t.Fatalf("%s: handler error: %v", name, err)
	}
	return res
}

// ok runs a tool and fails the test when it gave an error.
func (f *fixture) ok(name, argsJSON string) mcp.Result {
	f.t.Helper()
	res := f.call(name, argsJSON)
	if res.IsError {
		f.t.Fatalf("%s %s: error: %s", name, argsJSON, textOf(res))
	}
	return res
}

// fails runs a tool and checks that it gave an error with the text in it.
func (f *fixture) fails(name, argsJSON, want string) string {
	f.t.Helper()
	res := f.call(name, argsJSON)
	got := textOf(res)
	if !res.IsError {
		f.t.Fatalf("%s %s: no error, got %s", name, argsJSON, got)
	}
	if want != "" && !strings.Contains(got, want) {
		f.t.Fatalf("%s %s: error %q lacks %q", name, argsJSON, got, want)
	}
	return got
}

func textOf(r mcp.Result) string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// decode reads the JSON text of a result into v.
func decode(t *testing.T, r mcp.Result, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(textOf(r)), v); err != nil {
		t.Fatalf("the result is not JSON: %v\n%s", err, textOf(r))
	}
}

var errBoom = errors.New("boom")

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
