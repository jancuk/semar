package mobiletest

import (
	"context"
	"sync"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
)

// Screen size of the trees that Screen builds.
const (
	ScreenW = 1080
	ScreenH = 2400
)

// Node builds one node: a box of 100 by 100 pixels at (x, y) with the given text and class.
func Node(class, text string, x, y int, kids ...*mobile.Node) *mobile.Node {
	return &mobile.Node{Class: class, Text: text, Enabled: true, Bounds: mobile.Rect{Left: x, Top: y, Right: x + 100, Bottom: y + 100}, Children: kids,
		Attrs: map[string]string{"text": text, "class": class}}
}

// Screen builds a finished tree with the given nodes inside a full-screen root.
func Screen(kids ...*mobile.Node) *mobile.Tree {
	root := &mobile.Node{Class: "hierarchy", Enabled: true, Bounds: mobile.Rect{Right: ScreenW, Bottom: ScreenH}, Children: kids}
	t := &mobile.Tree{Root: root, Width: ScreenW, Height: ScreenH}
	t.Finish()
	return t
}

// Button builds a button with the given text.
func Button(text string, x, y int) *mobile.Node {
	b := Node("android.widget.Button", text, x, y)
	b.Role = "button"
	return b
}

// Input builds an empty text field with an id and a hint.
func Input(id, hint string, x, y int) *mobile.Node {
	b := Node("android.widget.EditText", "", x, y)
	b.Role, b.ID, b.Hint = "textbox", id, hint
	b.Attrs["resource-id"] = id
	return b
}

// FakeDriver is a device that shows the screens that a test gives it. Set the exported fields before
// the driver is used. A test changes Screen between calls, or sets OnTouch and OnType to change it
// after an action. Read what the driver did with the methods, which hold the lock.
type FakeDriver struct {
	mu sync.Mutex

	// Screen is what Source returns.
	Screen *mobile.Tree
	// OnTouch runs after every touch, OnType after every typed text, OnKey after every key, and OnLaunch
	// after every launch or reset. They run with no lock held, and may change Screen through Show.
	OnTouch  func(mobile.Gesture)
	OnType   func(string)
	OnKey    func(mobile.Key)
	OnLaunch func()
	// OnReset runs after a reset that names a mode, in place of OnLaunch.
	OnReset func(mode string)
	// Sources, when set, is a script of what Source returns, one per call. The last one repeats.
	Sources []*mobile.Tree
	// Clip is what Clipboard reads.
	Clip string
	Info mobile.About

	ResetErr, LaunchErr, OpenErr, SourceErr, TouchErr, TypeErr, KeyErr error
	Shot                                                               []byte
	ShotErr                                                            error

	log         []string
	touches     []mobile.Gesture
	typed       []string
	keys        []mobile.Key
	links       []string
	sourceCalls int
	resetModes  []string
}

// NewFakeDriver gives a driver that shows an empty screen of the app com.example.shop.
func NewFakeDriver() *FakeDriver {
	return &FakeDriver{Screen: Screen(), Info: mobile.About{Device: "emulator-5554", Package: "com.example.shop"}}
}

// Show changes the screen that Source returns.
func (f *FakeDriver) Show(t *mobile.Tree) {
	f.mu.Lock()
	f.Screen = t
	f.mu.Unlock()
}

// Calls is the log of the actions, in order: reset, launch, open, touch, type, key.
func (f *FakeDriver) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

// Touches are the gestures that the driver got.
func (f *FakeDriver) Touches() []mobile.Gesture {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mobile.Gesture(nil), f.touches...)
}

// Typed are the texts that the driver got.
func (f *FakeDriver) Typed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.typed...)
}

// Keys are the keys that the driver got.
func (f *FakeDriver) Keys() []mobile.Key {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mobile.Key(nil), f.keys...)
}

// Links are the links that the driver opened.
func (f *FakeDriver) Links() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.links...)
}

// SourceCalls is how many times the tree was read.
func (f *FakeDriver) SourceCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sourceCalls
}

func (f *FakeDriver) Reset(context.Context) error {
	f.mu.Lock()
	f.log = append(f.log, "reset")
	err, on := f.ResetErr, f.OnLaunch
	f.mu.Unlock()
	if on != nil && err == nil {
		on()
	}
	return err
}

// ResetWith is a reset with a mode of the launch that asked for it. The log says which: "reset:clear".
// OnReset runs after it with the mode, and a test uses it to put the app back to a signed out screen
// for clear and to leave it alone for restart.
func (f *FakeDriver) ResetWith(_ context.Context, mode string) error {
	f.mu.Lock()
	f.log = append(f.log, "reset:"+mode)
	f.resetModes = append(f.resetModes, mode)
	err, on, onMode := f.ResetErr, f.OnLaunch, f.OnReset
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if onMode != nil {
		onMode(mode)
	} else if on != nil {
		on()
	}
	return nil
}

// ResetModes are the modes of the resets that a launch asked for, in order.
func (f *FakeDriver) ResetModes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.resetModes...)
}

func (f *FakeDriver) Launch(context.Context) error {
	f.mu.Lock()
	f.log = append(f.log, "launch")
	err, on := f.LaunchErr, f.OnLaunch
	f.mu.Unlock()
	if on != nil && err == nil {
		on()
	}
	return err
}

func (f *FakeDriver) Open(_ context.Context, link string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, "open")
	f.links = append(f.links, link)
	return f.OpenErr
}

func (f *FakeDriver) Source(context.Context) (*mobile.Tree, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sourceCalls++
	if f.SourceErr != nil {
		return nil, f.SourceErr
	}
	if len(f.Sources) > 0 {
		return f.Sources[min(f.sourceCalls-1, len(f.Sources)-1)], nil
	}
	return f.Screen, nil
}

func (f *FakeDriver) Touch(_ context.Context, g mobile.Gesture) error {
	f.mu.Lock()
	f.log = append(f.log, "touch")
	f.touches = append(f.touches, g)
	err, on := f.TouchErr, f.OnTouch
	f.mu.Unlock()
	if on != nil && err == nil {
		on(g)
	}
	return err
}

func (f *FakeDriver) Type(_ context.Context, text string) error {
	f.mu.Lock()
	f.log = append(f.log, "type")
	f.typed = append(f.typed, text)
	err, on := f.TypeErr, f.OnType
	f.mu.Unlock()
	if on != nil && err == nil {
		on(text)
	}
	return err
}

func (f *FakeDriver) Key(_ context.Context, k mobile.Key) error {
	f.mu.Lock()
	f.log = append(f.log, "key")
	f.keys = append(f.keys, k)
	err, on := f.KeyErr, f.OnKey
	f.mu.Unlock()
	if on != nil && err == nil {
		on(k)
	}
	return err
}

func (f *FakeDriver) Screenshot(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Shot, f.ShotErr
}

func (f *FakeDriver) About() mobile.About { return f.Info }

// FakePlatform gives one driver for every service. It counts how often it was asked and closed.
type FakePlatform struct {
	mu       sync.Mutex
	D        mobile.Driver
	Err      error
	CloseErr error
	drivers  int
	closed   int
}

func (p *FakePlatform) Surface() domain.Surface { return domain.SurfaceAndroid }

func (p *FakePlatform) Driver(context.Context, string) (mobile.Driver, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drivers++
	if p.Err != nil {
		return nil, p.Err
	}
	return p.D, nil
}

func (p *FakePlatform) Close(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed++
	return p.CloseErr
}

// Drivers is how many times a driver was asked for, and Closed how many times the platform was closed.
func (p *FakePlatform) Drivers() int { p.mu.Lock(); defer p.mu.Unlock(); return p.drivers }
func (p *FakePlatform) Closed() int  { p.mu.Lock(); defer p.mu.Unlock(); return p.closed }

// Clipboard reads Clip, so that the fake is a mobile.ClipboardReader.
func (f *FakeDriver) Clipboard(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Clip, nil
}
