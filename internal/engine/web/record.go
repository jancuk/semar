package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/record"
)

// recorderJS is added to every page of the window before the page's own scripts run. It sends what a
// person does through the binding, and it never reads a password.
//
//go:embed recorder.js
var recorderJS string

const (
	bindingName  = "__semarEvent"
	requestedFor = 10 * time.Second
)

// RecorderOptions say which profile the window uses. Hidden runs Chrome with no window: it is for a
// test, which sends the input that a person would.
type RecorderOptions struct {
	Chrome      string // "" finds Chrome
	Home        string
	Environment string
	Account     string // "" is default
	Hidden      bool
	// Poll is how often Wait looks for the window. The default is half a second.
	Poll time.Duration
}

// Recorder is a Chrome window on Semar's own profile that reports what a person does in it. The
// command that opens a window and a test that sends input to a hidden one use this same type: only
// the one who sends the input differs.
type Recorder struct {
	o     RecorderOptions
	tab   context.Context
	stop  context.CancelFunc
	alloc context.CancelFunc

	mu        sync.Mutex
	events    []record.Event
	requested time.Time // when a page last asked for a navigation: a click or a form did
	closeOnce sync.Once
}

// StartRecorder starts Chrome on the profile of the account and puts the recorder in every page of
// it. It opens no address: Open does, as a person types one.
func StartRecorder(ctx context.Context, o RecorderOptions) (*Recorder, error) {
	chrome, err := FindChrome(o.Chrome)
	if err != nil {
		return nil, err
	}
	dir, err := ProfileDir(o.Home, o.Environment, o.Account)
	if err != nil {
		return nil, err
	}
	if err := ensureProfile(dir); err != nil {
		return nil, err
	}
	allocCtx, allocCancel := allocator(chrome, dir, !o.Hidden)
	tab, cancel := chromedp.NewContext(allocCtx)
	r := &Recorder{o: o, tab: tab, stop: cancel, alloc: allocCancel}
	if err := r.start(); err != nil {
		r.shutdown()
		return nil, fmt.Errorf("the browser could not be started on the profile %s: %w; if another window of this profile is open, close it", dir, err)
	}
	return r, nil
}

func (r *Recorder) start() error {
	if err := chromedp.Do(r.tab); err != nil {
		return err
	}
	if err := chromedp.Listen(r.tab, r.onEvent); err != nil {
		return err
	}
	if _, err := chromedp.Call(r.tab, page.Enable, page.EnableParams{}); err != nil {
		return err
	}
	if _, err := chromedp.Call(r.tab, runtime.AddBinding, runtime.AddBindingParams{Name: bindingName}); err != nil {
		return err
	}
	_, err := chromedp.Call(r.tab, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: recorderJS})
	return err
}

// Context is the tab of the window. A test sends its input here.
func (r *Recorder) Context() context.Context { return r.tab }

// Open goes to an address as a person does who types it in the address bar: the move is a step.
func (r *Recorder) Open(rawURL string) error {
	_, err := chromedp.Call(r.tab, page.Navigate, page.NavigateParams{URL: rawURL, TransitionType: page.TransitionTypeTyped})
	if err != nil {
		return fmt.Errorf("the page could not be opened: %w", err)
	}
	return nil
}

// Events gives what has been sent so far, in the order that it happened.
func (r *Recorder) Events() []record.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]record.Event(nil), r.events...)
}

// Wait returns when the person has closed the window.
func (r *Recorder) Wait(ctx context.Context) error {
	return waitWindowClosed(ctx, r.tab, r.o.Poll)
}

// Finish sends the text of a field that was typed and not yet sent, ends Chrome, and gives every
// event. It waits until Chrome has exited, so that the profile is on the disk.
func (r *Recorder) Finish() []record.Event {
	r.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(r.tab, 2*time.Second)
		_, _ = chromedp.Run(ctx, chromedp.Evaluate[any](`window.__semarFlush && window.__semarFlush()`))
		cancel()
		time.Sleep(50 * time.Millisecond)
		r.shutdown()
	})
	return r.Events()
}

func (r *Recorder) shutdown() {
	closeBrowser(r.tab)
	r.stop()
	r.alloc()
}

// onEvent runs in the loop of the tab, so it only files what it hears.
func (r *Recorder) onEvent(ev any) {
	switch e := ev.(type) {
	case *runtime.EventBindingCalled:
		if e.Name != bindingName {
			return
		}
		var got record.Event
		dec := json.NewDecoder(strings.NewReader(e.Payload))
		if err := dec.Decode(&got); err != nil {
			return
		}
		r.add(got)
	case *page.EventFrameRequestedNavigation:
		r.mu.Lock()
		r.requested = time.Now()
		r.mu.Unlock()
	case *page.EventFrameNavigated:
		if e.Frame == nil || e.Frame.ParentID != "" {
			return
		}
		r.mu.Lock()
		// A request that never finished, such as a download, must not hide the next address.
		byPage := time.Since(r.requested) < requestedFor
		r.requested = time.Time{}
		r.mu.Unlock()
		if byPage || !openable(e.Frame.URL) {
			return
		}
		r.add(record.Event{Kind: record.KindOpen, Page: record.Page{URL: e.Frame.URL}})
	}
}

func (r *Recorder) add(ev record.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

// openable is false for the pages that the browser shows when it has nothing to show.
func openable(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}
