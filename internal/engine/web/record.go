package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/secret"
)

// recorderJS is added to every page of the window before the page's own scripts run. It sends what a
// person does through the binding, and it never reads a password.
//
//go:embed recorder.js
var recorderJS string

const (
	bindingName  = "__semarEvent"
	requestedFor = 10 * time.Second
	// graceAfterAction is how long Settle waits before it asks whether the page is still.
	graceAfterAction = 300 * time.Millisecond
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
	// SignIn makes Open sign in to the first page, and start to record after that: what the window
	// does to sign in is no part of the recording. Nil starts to record at once.
	SignIn *RecorderSignIn
	// Policy keeps the browser inside the hosts and the methods of an environment, as a step of a
	// replay is kept: a request that the policy does not admit is failed before it is sent. Nil
	// leaves the window free, as it is for a person.
	Policy *effects.Policy
}

// RecorderSignIn is the account that the window signs in with. Settle and ActionTimeout bound the wait
// for a page to look still and the whole sign-in, as they do for the engine.
type RecorderSignIn struct {
	Account       string
	Who           config.WebSignIn
	Secrets       secret.Resolver
	Settle        time.Duration
	ActionTimeout time.Duration
}

// Recorder is a Chrome window on Semar's own profile that reports what a person does in it. The
// command that opens a window and a test that sends input to a hidden one use this same type: only
// the one who sends the input differs.
type Recorder struct {
	o     RecorderOptions
	tab   context.Context
	stop  context.CancelFunc
	alloc context.CancelFunc

	guard *guard // nil with no Policy

	mu        sync.Mutex
	events    []record.Event
	requested time.Time // when a page last asked for a navigation: a click or a form did
	closeOnce sync.Once
	gone      bool // Wait has returned: the window is closed, or the browser is gone
	signedIn  bool
	armed     bool // the recorder is in every page of the window
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
	if r.o.Policy != nil {
		if err := r.guardTab(); err != nil {
			return err
		}
	}
	if _, err := chromedp.Call(r.tab, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: watchJS}); err != nil {
		return err
	}
	if r.o.SignIn == nil {
		return r.arm()
	}
	return nil
}

// guardTab judges every request that the page makes, as the engine of a replay does.
func (r *Recorder) guardTab() error {
	r.guard = newGuard(r.o.Policy, func() string { return randomHex(8) })
	if err := chromedp.Listen(r.tab, r.guard.onEvent); err != nil {
		return err
	}
	go r.guard.serve(r.tab)
	if _, err := chromedp.Call(r.tab, network.Enable, network.EnableParams{}); err != nil {
		return err
	}
	if err := freshFiles(r.tab); err != nil {
		return err
	}
	_, err := chromedp.Call(r.tab, fetch.Enable, fetch.EnableParams{Patterns: []*fetch.RequestPattern{{URLPattern: "*"}}})
	return err
}

// arm puts the recorder in every page of the window, and in the page that is open now.
func (r *Recorder) arm() error {
	if r.armed {
		return nil
	}
	r.armed = true
	if _, err := chromedp.Call(r.tab, runtime.AddBinding, runtime.AddBindingParams{Name: bindingName}); err != nil {
		return err
	}
	if _, err := chromedp.Call(r.tab, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: recorderJS}); err != nil {
		return err
	}
	_, err := chromedp.Call(r.tab, runtime.Evaluate, runtime.EvaluateParams{Expression: recorderJS})
	return err
}

// Context is the tab of the window. A test sends its input here.
func (r *Recorder) Context() context.Context { return r.tab }

// Open goes to an address as a person does who types it in the address bar: the move is a step.
// With SignIn, Open then signs in to the page when it shows a sign-in form, and the recording starts
// at that address, signed in.
func (r *Recorder) Open(rawURL string) error {
	if err := r.navigate(r.tab, rawURL); err != nil {
		return err
	}
	if r.o.SignIn == nil {
		return nil
	}
	return r.signInAndArm(rawURL)
}

func (r *Recorder) navigate(ctx context.Context, rawURL string) error {
	_, err := chromedp.Call(ctx, page.Navigate, page.NavigateParams{URL: rawURL, TransitionType: page.TransitionTypeTyped})
	if err != nil {
		return fmt.Errorf("the page could not be opened: %w", err)
	}
	return nil
}

func (r *Recorder) signInAndArm(rawURL string) error {
	o := r.o.SignIn
	opened, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("the page address does not parse: %w", err)
	}
	limit := o.ActionTimeout
	if limit <= 0 {
		limit = defaultActionTimeout
	}
	settle := o.Settle
	if settle <= 0 {
		settle = defaultSettle
	}
	ctx, cancel := context.WithTimeout(r.tab, limit)
	defer cancel()
	g := signer{tab: recorderTab{r, settle}, secrets: o.Secrets, limit: limit / 3}
	signed, err := g.afterOpen(ctx, o.Account, o.Who, opened)
	if err != nil {
		return err
	}
	if err := r.arm(); err != nil {
		return err
	}
	r.mu.Lock()
	r.signedIn = signed
	r.events = []record.Event{{Kind: record.KindOpen, Page: record.Page{URL: rawURL}}}
	r.mu.Unlock()
	return nil
}

// SignedIn is the account that Open signed in as, and false when the page showed no sign-in form.
func (r *Recorder) SignedIn() (account string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.signedIn {
		return "", false
	}
	return r.o.SignIn.Account, true
}

// recorderTab is the tab of the window, for the sign-in. The window has no guard that counts requests,
// so a page looks still when it is complete and its tree has not changed for a short time.
type recorderTab struct {
	r     *Recorder
	limit time.Duration
}

func (t recorderTab) settle(ctx context.Context) { t.r.settle(ctx, t.limit) }

// settle waits until the page is complete, its tree has not changed for a short time, and, with a
// Policy, no request is open.
func (r *Recorder) settle(ctx context.Context, limit time.Duration) {
	idle := func(time.Time) (time.Duration, int) { return time.Hour, 0 }
	if r.guard != nil {
		idle = r.guard.idle
	}
	settleUntil(ctx, limit, idle)
}

// Settle waits for the page to look still, for at most a few seconds, and then returns whatever the
// page shows: a page that never settles is read as it is.
func (r *Recorder) Settle(ctx context.Context) {
	c, cancel := context.WithCancel(r.tab)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-c.Done():
		}
	}()
	// A page often starts its request, or draws its answer, a moment after an action, and a page that
	// looks still in that moment is not settled yet.
	select {
	case <-c.Done():
		return
	case <-time.After(graceAfterAction):
	}
	r.settle(c, defaultSettle)
}

// Reopen throws away what was recorded and goes to the address again, as the first move of a new
// recording. A flow does it after its fixtures are made, so that the page shows their data and the
// test starts at the first page.
func (r *Recorder) Reopen(rawURL string) error {
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
	return r.Open(rawURL)
}

func (t recorderTab) location(ctx context.Context) (string, error) {
	return chromedp.Run(ctx, chromedp.Location())
}

func (t recorderTab) navigate(ctx context.Context, rawURL string) error {
	return t.r.navigate(ctx, rawURL)
}

// CloseWindow closes every page of the window, as a person does with its close button. A test that
// stands where the person stands calls it when its input is done.
func (r *Recorder) CloseWindow() {
	res, err := chromedp.CallBrowser(r.tab, target.GetTargets, target.GetTargetsParams{})
	if err != nil {
		return
	}
	for _, info := range res.TargetInfos {
		if info.Type == "page" {
			_, _ = chromedp.CallBrowser(r.tab, target.CloseTarget, target.CloseTargetParams{TargetID: info.TargetID})
		}
	}
}

// BlockedHosts lists the hosts that the policy of the recorder refused to reach, sorted. A recorder with
// no policy blocks nothing.
func (r *Recorder) BlockedHosts() []string {
	if r.guard == nil {
		return nil
	}
	return r.guard.blockedHosts()
}

// Events gives what has been sent so far, in the order that it happened.
func (r *Recorder) Events() []record.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]record.Event(nil), r.events...)
}

// Screenshot takes a JPEG of what the tab shows, at most 1280 pixels wide.
func (r *Recorder) Screenshot(ctx context.Context) ([]byte, error) {
	c, cancel := context.WithCancel(r.tab)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-c.Done():
		}
	}()
	return captureJPEG(c)
}

// Mark is how many events have been heard so far. A caller that wants the events of one stretch keeps it
// and asks EventsFrom for what came after.
func (r *Recorder) Mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// EventsFrom gives a copy of the events from index i on. An index past the end gives none.
func (r *Recorder) EventsFrom(i int) []record.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 {
		i = 0
	}
	if i >= len(r.events) {
		return nil
	}
	return append([]record.Event(nil), r.events[i:]...)
}

// Location is the address of the page that the tab shows now.
func (r *Recorder) Location(ctx context.Context) (string, error) {
	c, cancel := context.WithCancel(r.tab)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-c.Done():
		}
	}()
	return chromedp.Run(c, chromedp.Location())
}

// Goto moves the tab to an address as a person does who types it: the move is a step. Unlike Open it
// does not sign in again and does not start the recording over.
func (r *Recorder) Goto(rawURL string) error { return r.navigate(r.tab, rawURL) }

// Wait returns when the person has closed the window.
func (r *Recorder) Wait(ctx context.Context) error {
	err := waitWindowClosed(ctx, r.tab, r.o.Poll, true, nil)
	r.mu.Lock()
	r.gone = true
	r.mu.Unlock()
	return err
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
	r.mu.Lock()
	gone := r.gone
	r.mu.Unlock()
	if !gone {
		closeBrowser(r.tab)
	}
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
		r.add(record.Event{Kind: record.KindOpen, Page: record.Page{URL: e.Frame.URL + e.Frame.URLFragment}})
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
