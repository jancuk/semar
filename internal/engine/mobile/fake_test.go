package mobile_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
)

const (
	screenW = 1080
	screenH = 2400
)

// n builds one node. The bounds are a box of 100 by 100 pixels at (x, y).
func n(class, text string, x, y int, kids ...*mobile.Node) *mobile.Node {
	return &mobile.Node{Class: class, Text: text, Enabled: true, Bounds: mobile.Rect{Left: x, Top: y, Right: x + 100, Bottom: y + 100}, Children: kids,
		Attrs: map[string]string{"text": text, "class": class}}
}

// screen builds a finished tree with the given nodes inside a full-screen root.
func screen(kids ...*mobile.Node) *mobile.Tree {
	root := &mobile.Node{Class: "hierarchy", Enabled: true, Bounds: mobile.Rect{Right: screenW, Bottom: screenH}, Children: kids}
	t := &mobile.Tree{Root: root, Width: screenW, Height: screenH}
	t.Finish()
	return t
}

func button(text string, x, y int) *mobile.Node {
	b := n("android.widget.Button", text, x, y)
	b.Role = "button"
	return b
}

// fakeDriver is a device that shows the screens that a test gives it.
type fakeDriver struct {
	mu sync.Mutex
	// screen is what Source returns. A test changes it between calls, or sets next to change it after
	// a touch.
	screen *mobile.Tree
	// onTouch runs after every touch, and may change the screen.
	onTouch func(mobile.Gesture)
	onType  func(string)
	// sources, when set, is a script of what Source returns, one per call. The last one repeats.
	sources []*mobile.Tree

	log     []string
	touches []mobile.Gesture
	typed   []string
	keys    []mobile.Key
	links   []string

	sourceCalls int
	about       mobile.About

	resetErr, launchErr, openErr, sourceErr, touchErr, typeErr, keyErr error
	shot                                                               []byte
	shotErr                                                            error
}

func (f *fakeDriver) rec(s string) { f.log = append(f.log, s) }

func (f *fakeDriver) Reset(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("reset")
	return f.resetErr
}
func (f *fakeDriver) Launch(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("launch")
	return f.launchErr
}
func (f *fakeDriver) Open(_ context.Context, link string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("open")
	f.links = append(f.links, link)
	return f.openErr
}

func (f *fakeDriver) Source(context.Context) (*mobile.Tree, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sourceCalls++
	if f.sourceErr != nil {
		return nil, f.sourceErr
	}
	if len(f.sources) > 0 {
		i := min(f.sourceCalls-1, len(f.sources)-1)
		return f.sources[i], nil
	}
	return f.screen, nil
}

func (f *fakeDriver) Touch(_ context.Context, g mobile.Gesture) error {
	f.mu.Lock()
	f.rec("touch")
	f.touches = append(f.touches, g)
	err := f.touchErr
	on := f.onTouch
	f.mu.Unlock()
	if on != nil && err == nil {
		on(g)
	}
	return err
}

func (f *fakeDriver) Type(_ context.Context, text string) error {
	f.mu.Lock()
	f.rec("type")
	f.typed = append(f.typed, text)
	err := f.typeErr
	on := f.onType
	f.mu.Unlock()
	if on != nil && err == nil {
		on(text)
	}
	return err
}

func (f *fakeDriver) Key(_ context.Context, k mobile.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("key")
	f.keys = append(f.keys, k)
	return f.keyErr
}

func (f *fakeDriver) Screenshot(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shot, f.shotErr
}

func (f *fakeDriver) About() mobile.About { return f.about }

type fakePlatform struct {
	d        *fakeDriver
	err      error
	drivers  int
	closed   int
	closeErr error
}

func (p *fakePlatform) Surface() domain.Surface { return domain.SurfaceAndroid }
func (p *fakePlatform) Driver(context.Context, string) (mobile.Driver, error) {
	p.drivers++
	if p.err != nil {
		return nil, p.err
	}
	return p.d, nil
}
func (p *fakePlatform) Close(context.Context) error { p.closed++; return p.closeErr }

type memFiles struct {
	mu    sync.Mutex
	files map[string][]byte
	err   error
}

func (m *memFiles) WriteFile(rel string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if m.files == nil {
		m.files = map[string][]byte{}
	}
	m.files[rel] = data
	return nil
}

type memSink struct {
	mu      sync.Mutex
	actions []domain.Action
}

func (s *memSink) WriteAction(a domain.Action) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, a)
	return "actions/" + string(a.ID) + ".json", nil
}
func (s *memSink) AppendActivity(domain.Activity) error { return nil }

// clock is a clock that only a sleep moves.
type clock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.sleeps = append(c.sleeps, d)
	c.mu.Unlock()
	return nil
}

type rig struct {
	t     *testing.T
	e     *mobile.Engine
	d     *fakeDriver
	p     *fakePlatform
	sink  *memSink
	files *memFiles
	clk   *clock
}

var testKey = []byte("0123456789abcdef0123456789abcdef")

type rigOptions struct {
	readOnly bool
	kind     string
	secrets  []string
	schemes  []string
	hosts    []string
	budget   int
	resolver *fakeSecrets
}

type fakeSecrets struct {
	values map[string]string
}

func (f *fakeSecrets) Resolve(_ context.Context, ref string) (string, error) {
	v, ok := f.values[ref]
	if !ok {
		return "", errors.New("no such secret " + ref)
	}
	return v, nil
}

func (f *fakeSecrets) Loaded() []string { return nil }

func newRig(t *testing.T, o rigOptions, tune ...func(*mobile.Options)) *rig {
	t.Helper()
	mode := "test_data"
	if o.readOnly {
		mode = "read_only"
	}
	hosts := o.hosts
	if hosts == nil {
		hosts = []string{"shop.example.com"}
	}
	pol, err := effects.New(config.Environment{Hosts: hosts, Methods: []string{"GET"}, Effects: config.Effects{Mode: mode}})
	if err != nil {
		t.Fatal(err)
	}
	red, err := redact.New(config.Redact{}, func() []string { return o.secrets }, testKey)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, d: &fakeDriver{screen: screen(), about: mobile.About{Device: "emulator-5554", Package: "com.example.shop"}},
		sink: &memSink{}, files: &memFiles{}, clk: &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}
	r.p = &fakePlatform{d: r.d}
	rec := recorder.New(r.sink, red, func(string, *url.URL) effects.Class { return effects.Read }, "run-1")
	opts := mobile.Options{
		Policy: pol, Recorder: rec, Files: r.files, Platform: r.p, Kind: o.kind, Run: "run-1", Budget: o.budget,
		Services: map[string]mobile.Service{"shop-app": {Schemes: o.schemes}},
		Clock:    r.clk.Now, Sleep: r.clk.Sleep, TraceID: func() string { return "0123456789abcdef0123456789abcdef" },
	}
	if o.resolver != nil {
		opts.Secrets = o.resolver
	}
	for _, f := range tune {
		f(&opts)
	}
	r.e = mobile.New(opts)
	return r
}

func use(step string) domain.UseCase {
	return domain.UseCase{Pass: domain.PassScenario, Scenario: "AND-1", Step: domain.StepID(step)}
}

func act(do domain.MobileOp, screenName, element string) domain.MobileAction {
	return domain.MobileAction{Service: "shop-app", App: "shop-app", Do: do, Screen: screenName, Element: element}
}

// call is a call for an action and the locators of the elements it names.
func call(step string, a domain.MobileAction, locs map[string]domain.Locator) mobile.Call {
	return mobile.Call{Use: use(step), By: domain.ActorReplay, Action: a, Locators: locs}
}

func (r *rig) do(c mobile.Call) mobile.Result {
	r.t.Helper()
	res, err := r.e.Do(context.Background(), c)
	if err != nil {
		r.t.Fatalf("Do: %v", err)
	}
	return res
}

func pngOf(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

type slowDriver struct{ *fakeDriver }

func (s *slowDriver) Launch(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

type slowPlatform struct{ d *slowDriver }

func (p *slowPlatform) Surface() domain.Surface                               { return domain.SurfaceAndroid }
func (p *slowPlatform) Driver(context.Context, string) (mobile.Driver, error) { return p.d, nil }
func (p *slowPlatform) Close(context.Context) error                           { return nil }

func recorderOf(t *testing.T, r *rig) *recorder.Recorder {
	t.Helper()
	red, err := redact.New(config.Redact{}, func() []string { return nil }, testKey)
	if err != nil {
		t.Fatal(err)
	}
	return recorder.New(r.sink, red, func(string, *url.URL) effects.Class { return effects.Read }, "run-1")
}

func policyOf(t *testing.T) *effects.Policy {
	t.Helper()
	p, err := effects.New(config.Environment{Hosts: []string{"shop.example.com"}, Methods: []string{"GET"}, Effects: config.Effects{Mode: "test_data"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
