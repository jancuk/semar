package mobile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/secret"
	"github.com/jancuk/semar/internal/uirepair"
)

const (
	defaultBudget        = 200
	defaultActionTimeout = 30 * time.Second
	defaultSettle        = 3 * time.Second
	defaultCheckTimeout  = 5 * time.Second
	waitLimit            = 10 * time.Second
	checkPoll            = 250 * time.Millisecond
	focusDelay           = 100 * time.Millisecond
	// maxSwipes is how many swipes a scroll makes before it gives up.
	maxSwipes     = 15
	maxLink       = 2048
	longPress     = 700 * time.Millisecond
	swipeDuration = 300 * time.Millisecond

	refusedNoUseCase = "a write must belong to a scenario step, an exploration, or a probe"
	refusedBudget    = "action budget reached"
)

// Files stores a file below the run directory.
type Files interface {
	WriteFile(rel string, data []byte) error
}

// Service is what the engine needs to know of the app of a service.
type Service struct {
	// Schemes are the custom schemes that an open may use, besides http and https.
	Schemes []string
}

type Options struct {
	Policy   *effects.Policy
	Recorder *recorder.Recorder
	Files    Files
	Secrets  secret.Resolver
	Platform Platform
	// Services are the services that have an app, by name.
	Services map[string]Service
	// Kind is the kind of the environment: config.KindTest or config.KindProduction.
	Kind string
	Run  domain.RunID
	// Budget is how many actions the engine takes. The default is 200.
	Budget int
	// ActionTimeout bounds one action, and Settle bounds the wait until the screen is still. The
	// defaults are 30 and 3 seconds.
	ActionTimeout time.Duration
	Settle        time.Duration
	// CheckTimeout is how long the checks of a step wait for their condition after the screen settles.
	CheckTimeout time.Duration
	Clock        func() time.Time
	// Sleep waits for d, or for the context. A test passes one that moves its clock.
	Sleep   func(ctx context.Context, d time.Duration) error
	TraceID func() string // 32 lower-case hex characters
	Logger  *slog.Logger
}

type Call struct {
	Use    domain.UseCase
	By     domain.Actor
	Action domain.MobileAction
	// Locators holds the locator of every element that the action and the watched elements name, and
	// of every element that their within chains name, with the variables filled in.
	Locators map[string]domain.Locator
	Watch    []domain.Watched
	// Until holds the checks of the step on an element, with the variables filled in. The engine reads
	// the screen again until they hold or CheckTimeout passes, and records the last reading. It decides
	// no outcome: the verdict evaluates the checks on the stored action.
	Until    []domain.Check
	Captures []domain.Capture
	Why      string
}

type Result struct {
	Action   domain.Action     // redacted, as stored
	Captured map[string]string // raw values, for later steps only. Never store them
	Missing  []string          // capture names that gave no value
}

type Engine struct {
	o Options

	mu    sync.Mutex
	sent  int
	shots int
	// reset holds the services whose app was reset in the scenario that is running.
	reset map[string]bool
	// device serializes the actions: one device does one thing at a time.
	device sync.Mutex
}

func New(o Options) *Engine {
	if o.Budget <= 0 {
		o.Budget = defaultBudget
	}
	if o.ActionTimeout <= 0 {
		o.ActionTimeout = defaultActionTimeout
	}
	if o.Settle <= 0 {
		o.Settle = defaultSettle
	}
	if o.CheckTimeout <= 0 {
		o.CheckTimeout = defaultCheckTimeout
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleep
	}
	if o.TraceID == nil {
		o.TraceID = func() string { return randomHex(16) }
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Engine{o: o, reset: map[string]bool{}}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Close ends what the platform started. It is safe to call twice.
func (e *Engine) Close(ctx context.Context) error {
	if e.o.Platform == nil {
		return nil
	}
	return e.o.Platform.Close(ctx)
}

// NewScenario marks every app for a reset, so that the scenario that comes next starts with its app
// stopped, and with no data when the service says clear. It starts nothing: the reset comes with the
// first step of the scenario that uses the app.
func (e *Engine) NewScenario() {
	e.mu.Lock()
	e.reset = map[string]bool{}
	e.mu.Unlock()
}

func (e *Engine) take() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sent >= e.o.Budget {
		return false
	}
	e.sent++
	return true
}

func (e *Engine) needsReset(service string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.reset[service]
}

func (e *Engine) markReset(service string) {
	e.mu.Lock()
	e.reset[service] = true
	e.mu.Unlock()
}

// Do never returns an error for a refusal or for an element that is not there: those are in
// Result.Action. It returns an error for an unknown service, an action that is not well formed, a
// recorder failure, a context that was cancelled, and a platform that cannot run here, which wraps
// *Unavailable.
func (e *Engine) Do(ctx context.Context, c Call) (Result, error) {
	m := c.Action
	label := describe(m)
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("mobile %s: %w", label, err)
	}
	svc, known := e.o.Services[m.Service]
	if !known {
		return Result{}, fmt.Errorf("mobile: unknown service %q", m.Service)
	}
	if ps := m.Problems(); len(ps) > 0 {
		return Result{}, fmt.Errorf("mobile %s: %s", label, ps[0])
	}
	if e.o.Platform == nil {
		return Result{}, &Unavailable{Reason: "the run has no device"}
	}
	a := domain.Action{
		Surface: e.o.Platform.Surface(),
		Op:      string(m.Do),
		Use:     c.Use,
		By:      c.By,
		TraceID: e.o.TraceID(),
		At:      e.o.Clock(),
		Why:     c.Why,
		Mobile:  &domain.MobileRecord{Op: string(m.Do), App: m.App, Screen: m.Screen},
	}
	if loc, ok := c.Locators[m.Element]; ok && m.Element != "" {
		a.Mobile.Target = &loc
	}
	missing := captureNames(c.Captures)

	switch {
	case e.o.Policy != nil && e.o.Policy.ReadOnly():
		a.Refused = fmt.Sprintf("%s: %s is refused: the app on a device makes requests that Semar cannot see, so it cannot keep the promise of a read only environment", effects.ReadOnly, m.Do)
	case e.o.Kind == config.KindProduction:
		a.Refused = fmt.Sprintf("%s is refused: the environment is production, and the app on a device makes requests that Semar cannot see", m.Do)
	case m.Do.Writes() && c.Use.Pass == "":
		a.Refused = refusedNoUseCase
	case m.Do == domain.MobileOpenLink:
		a.Refused = e.linkRefusal(m.Link, svc, m.Service)
		a.Mobile.Link = m.Link
	}
	if a.Refused != "" {
		return e.record(ctx, label, a, nil, missing)
	}
	if !e.take() {
		a.Refused = refusedBudget
		return e.record(ctx, label, a, nil, missing)
	}

	value := m.Value
	if m.Secret != "" {
		if e.o.Secrets == nil {
			a.Err = "the secret could not be read: the engine has no secret source"
			return e.record(ctx, label, a, nil, missing)
		}
		v, err := e.o.Secrets.Resolve(ctx, m.Secret)
		if err != nil {
			a.Err = "the secret could not be read: " + err.Error()
			return e.record(ctx, label, a, nil, missing)
		}
		value = v
	}

	e.device.Lock()
	defer e.device.Unlock()
	d, err := e.o.Platform.Driver(ctx, m.Service)
	if err != nil {
		var un *Unavailable
		if errors.As(err, &un) || ctx.Err() != nil {
			return Result{}, fmt.Errorf("mobile %s: %w", label, err)
		}
		a.Err = errorText(err)
		return e.record(ctx, label, a, nil, missing)
	}
	about := d.About()
	a.Mobile.Package, a.Mobile.Device = about.Package, about.Device
	return e.run(ctx, d, c, value, label, a, missing)
}

func describe(m domain.MobileAction) string {
	parts := []string{string(m.Do)}
	if m.Screen != "" {
		parts = append(parts, m.Screen)
	}
	if m.Element != "" {
		parts[len(parts)-1] += "." + m.Element
	}
	return strings.Join(parts, " ")
}

// deniedSchemes can never be opened, whatever the service lists.
var deniedSchemes = []string{"http", "https", "file", "content", "intent", "javascript", "data", "about", "vbscript"}

// linkRefusal says why a link may not be opened, or "". An http or https link goes through the effects
// policy as a GET, and any other scheme must be one that the service lists.
func (e *Engine) linkRefusal(link string, svc Service, name string) string {
	switch {
	case link == "":
		return "the link is empty"
	case len(link) > maxLink:
		return "the link is longer than 2048 bytes"
	case strings.ContainsFunc(link, func(r rune) bool { return r < 0x20 || r == 0x7f || r == ' ' }):
		return "the link holds a space or a control character"
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" {
		return "the link is not a URL with a scheme"
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
		if e.o.Policy == nil {
			return "the effects policy refused open: the engine has no policy"
		}
		if err := e.o.Policy.Admit(effects.Request{Method: "GET", URL: u}); err != nil {
			return refusalText(err, "open")
		}
		return ""
	}
	if slices.Contains(deniedSchemes, scheme) {
		return fmt.Sprintf("the scheme %s cannot be opened", scheme)
	}
	if !slices.Contains(svc.Schemes, scheme) {
		return fmt.Sprintf("the scheme %s is not in the schemes of service %s: list it under android.schemes to let a step open it", scheme, name)
	}
	return ""
}

// refusalText keeps the kind and the fixed reason of a policy refusal. The policy quotes the path of
// the request in some of its reasons, and the path can hold a value.
func refusalText(err error, label string) string {
	var r *effects.Refusal
	if !errors.As(err, &r) {
		return "the effects policy refused " + label
	}
	switch r.Kind {
	case effects.HostNotAllowed:
		return r.Error()
	case effects.ReadOnly:
		return string(r.Kind) + ": " + label + " writes, and the environment is read only"
	case effects.MethodNotAllowed:
		return string(r.Kind) + ": the method of " + label + " is not in the environment methods"
	default:
		return string(r.Kind) + ": " + label + " is not allowed: its path is not normal or it matches the deny list"
	}
}

// notFoundMiss is an element that the action looked for and did not find, with what the screen held then.
type notFoundMiss struct {
	element string
	state   domain.ElementState
	repair  []domain.Candidate
}

type notFoundError struct {
	element string
	count   int
}

func (n notFoundError) Error() string {
	return fmt.Sprintf("%s (element %s matched %d)", check.NotFound, n.element, n.count)
}

func isNotFound(err error) bool {
	var n notFoundError
	return errors.As(err, &n)
}

func isNotFoundText(s string) bool { return strings.HasPrefix(s, check.NotFound) }

// errorText is the first line of an error, which never holds what the device or the server sent back.
func errorText(err error) string {
	first, _, _ := strings.Cut(err.Error(), "\n")
	if errors.Is(err, context.DeadlineExceeded) {
		return "the action took too long"
	}
	return first
}

// run does the action, waits until the screen has settled, reads what the checks need, and records.
func (e *Engine) run(ctx context.Context, d Driver, c Call, value, label string, a domain.Action, missing []string) (Result, error) {
	actCtx, cancel := context.WithTimeout(ctx, e.o.ActionTimeout)
	defer cancel()

	m := c.Action
	began := e.o.Clock()
	var opErr error
	var miss *notFoundMiss
	dispatched := false

	if e.needsReset(m.Service) {
		if err := d.Reset(actCtx); err != nil {
			opErr = fmt.Errorf("the app could not be reset: %w", err)
		} else {
			e.markReset(m.Service)
		}
	}
	if opErr == nil {
		miss, dispatched, opErr = e.perform(actCtx, d, c, value)
	}
	// An element that the action could not find is read as the action saw it. After the action the
	// screen has changed: a button that was pressed may be gone, and that is no miss.
	var settled *Tree
	if actCtx.Err() == nil {
		var err error
		settled, err = e.settle(actCtx, d, e.o.Settle)
		if err != nil && opErr == nil && actCtx.Err() == nil {
			opErr = fmt.Errorf("the screen could not be read: %w", err)
		}
	}
	var states map[string]domain.ElementState
	var repair []domain.Candidate
	var readErr error
	if settled != nil && actCtx.Err() == nil {
		states, repair, readErr = e.readUntil(actCtx, d, settled, c)
	}
	if miss != nil {
		if states == nil {
			states = map[string]domain.ElementState{}
		}
		if _, read := states[miss.element]; !read {
			states[miss.element] = miss.state
		}
		if !slices.ContainsFunc(repair, func(x domain.Candidate) bool { return x.Element == miss.element }) {
			repair = append(repair, miss.repair...)
		}
	}
	a.Mobile.Elements = states
	a.Mobile.Repair = repair
	a.DurationMS = e.o.Clock().Sub(began).Milliseconds()

	switch {
	case ctx.Err() != nil:
		a.Err = "the run was interrupted"
	case opErr != nil:
		a.Err = errorText(opErr)
		a.EffectUnknown = m.Do.Writes() && dispatched && !isNotFound(opErr)
	case readErr != nil:
		a.Err = "the screen could not be read: " + errorText(readErr)
	}
	if shot, err := e.screenshot(ctx, d, a); err != nil {
		e.o.Logger.DebugContext(ctx, "no screenshot", "error", err)
	} else {
		a.Mobile.Screenshot = shot
	}

	var captured map[string]string
	if a.Err == "" || isNotFoundText(a.Err) {
		captured, missing = capture(states, c.Captures)
	}
	return e.record(ctx, label, a, captured, missing)
}

// perform acts on the screen. It gives the element that it did not find, whether an input was sent
// before the error (a write whose effect is not known), and the error.
func (e *Engine) perform(ctx context.Context, d Driver, c Call, value string) (*notFoundMiss, bool, error) {
	m := c.Action
	switch m.Do {
	case domain.MobileLaunch:
		return nil, true, d.Launch(ctx)
	case domain.MobileOpenLink:
		return nil, true, d.Open(ctx, m.Link)
	case domain.MobilePress:
		return nil, true, d.Key(ctx, Key(m.Value))
	case domain.MobileWait:
		return e.waitFor(ctx, d, c)
	case domain.MobileSwipe:
		return e.swipe(ctx, d, c)
	case domain.MobileScroll:
		return e.scroll(ctx, d, c)
	}
	tree, err := d.Source(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("the screen could not be read: %w", err)
	}
	rd := newReader(tree, c.Locators)
	target, miss, err := e.target(rd, m.Element)
	if err != nil {
		return miss, false, err
	}
	at := target.node.Bounds.Center()
	switch m.Do {
	case domain.MobileTap:
		return nil, true, d.Touch(ctx, Gesture{From: at, To: at})
	case domain.MobileLongTap:
		return nil, true, d.Touch(ctx, Gesture{From: at, To: at, Hold: longPress})
	case domain.MobileType:
		if err := d.Touch(ctx, Gesture{From: at, To: at}); err != nil {
			return nil, true, err
		}
		if err := e.o.Sleep(ctx, focusDelay); err != nil {
			return nil, true, err
		}
		return nil, true, d.Type(ctx, value)
	}
	return nil, false, fmt.Errorf("the action %s is not known", m.Do)
}

// target finds the one element that the action acts on. An element that is not there, or that matches
// several, is a miss with what the screen held and the repair candidates.
func (e *Engine) target(rd *reader, name string) (resolved, *notFoundMiss, error) {
	res, err := rd.resolve(name)
	if err != nil {
		return resolved{}, nil, err
	}
	if res.state.Found {
		return res, nil, nil
	}
	var miss *notFoundMiss
	if res.state.Count == 0 && res.state.Locator != nil {
		miss = &notFoundMiss{element: name, state: res.state, repair: uirepair.Candidates(name, *res.state.Locator, axNodes(rd.tree))}
	}
	return res, miss, notFoundError{name, res.state.Count}
}

// waitFor polls until the element of a wait is on the screen, for at most waitLimit. A wait with no
// element only lets the screen settle.
func (e *Engine) waitFor(ctx context.Context, d Driver, c Call) (*notFoundMiss, bool, error) {
	name := c.Action.Element
	if name == "" {
		return nil, false, nil
	}
	deadline := e.o.Clock().Add(waitLimit)
	for {
		tree, err := d.Source(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("the screen could not be read: %w", err)
		}
		rd := newReader(tree, c.Locators)
		res, miss, err := e.target(rd, name)
		if err == nil {
			return nil, false, nil
		}
		if !isNotFound(err) || res.state.Count > 1 || !e.o.Clock().Before(deadline) {
			return miss, false, err
		}
		if err := e.o.Sleep(ctx, checkPoll); err != nil {
			return miss, false, err
		}
	}
}

// swipe moves a finger over the element, or over the screen when the action names none.
func (e *Engine) swipe(ctx context.Context, d Driver, c Call) (*notFoundMiss, bool, error) {
	m := c.Action
	tree, err := d.Source(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("the screen could not be read: %w", err)
	}
	region := screenRect(tree)
	if m.Element != "" {
		target, mi, err := e.target(newReader(tree, c.Locators), m.Element)
		if err != nil {
			return mi, false, err
		}
		region = target.node.Bounds.Intersect(region)
	}
	if region.Empty() {
		return nil, false, errors.New("there is no screen to swipe on")
	}
	return nil, true, d.Touch(ctx, swipeGesture(region, m.Value))
}

// scroll swipes until the element is on the screen: at most maxSwipes times, and no more once a swipe
// leaves the screen as it was. value is where the person reads on: down shows what is below, which is
// a finger that moves up. A scroll that has the element on the screen already sends nothing.
func (e *Engine) scroll(ctx context.Context, d Driver, c Call) (*notFoundMiss, bool, error) {
	m := c.Action
	direction := m.Value
	if direction == "" {
		direction = "down"
	}
	tree, err := d.Source(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("the screen could not be read: %w", err)
	}
	var first *notFoundMiss
	dispatched := false
	for swipes := 0; ; swipes++ {
		rd := newReader(tree, c.Locators)
		res, miss, err := e.target(rd, m.Element)
		if err == nil {
			return nil, dispatched, nil
		}
		if first == nil {
			first = miss
		}
		if !isNotFound(err) || res.state.Count > 1 || swipes >= maxSwipes {
			return first, dispatched, err
		}
		region := screenRect(tree)
		if m.Within != "" {
			inner, err := rd.resolve(m.Within)
			switch {
			case err != nil:
				return first, dispatched, err
			case !inner.state.Found:
				return first, dispatched, notFoundError{m.Within, inner.state.Count}
			}
			region = inner.node.Bounds.Intersect(region)
		}
		if region.Empty() {
			return first, dispatched, errors.New("there is no screen to scroll")
		}
		if err := d.Touch(ctx, swipeGesture(region, oppositeOf(direction))); err != nil {
			return first, true, err
		}
		dispatched = true
		next, err := e.settle(ctx, d, e.o.Settle)
		if err != nil {
			return first, dispatched, fmt.Errorf("the screen could not be read: %w", err)
		}
		if next.Digest == tree.Digest {
			// The swipe changed nothing: the list has ended.
			_, _, err := e.target(newReader(next, c.Locators), m.Element)
			return first, dispatched, err
		}
		tree = next
	}
}

func screenRect(t *Tree) Rect {
	if t.Width > 0 && t.Height > 0 {
		return Rect{0, 0, t.Width, t.Height}
	}
	if t.Root != nil {
		return t.Root.Bounds
	}
	return Rect{}
}

func oppositeOf(direction string) string {
	switch direction {
	case "up":
		return "down"
	case "down":
		return "up"
	case "left":
		return "right"
	}
	return "left"
}

// swipeGesture is a swipe of a finger in the direction, over the middle three fifths of the region, so
// that it stays clear of the edges that the system uses for its own gestures.
func swipeGesture(r Rect, direction string) Gesture {
	cx, cy := (r.Left+r.Right)/2, (r.Top+r.Bottom)/2
	lo := func(a, b int) int { return a + (b-a)*1/5 }
	hi := func(a, b int) int { return a + (b-a)*4/5 }
	g := Gesture{Move: swipeDuration}
	switch direction {
	case "up":
		g.From, g.To = Point{cx, hi(r.Top, r.Bottom)}, Point{cx, lo(r.Top, r.Bottom)}
	case "down":
		g.From, g.To = Point{cx, lo(r.Top, r.Bottom)}, Point{cx, hi(r.Top, r.Bottom)}
	case "left":
		g.From, g.To = Point{hi(r.Left, r.Right), cy}, Point{lo(r.Left, r.Right), cy}
	default:
		g.From, g.To = Point{lo(r.Left, r.Right), cy}, Point{hi(r.Left, r.Right), cy}
	}
	return g
}

// read gives the state of every element that the step names, and the repair candidates for each
// element that is not there and that a check or a capture reads the text of.
func (e *Engine) read(t *Tree, c Call) (map[string]domain.ElementState, []domain.Candidate, error) {
	m := c.Action
	var names []string
	needsText := map[string]bool{}
	for _, name := range m.Elements() {
		names = append(names, name)
		needsText[name] = true
	}
	for _, wt := range c.Watch {
		if !slices.Contains(names, wt.Element) {
			names = append(names, wt.Element)
		}
		if !wt.Presence {
			needsText[wt.Element] = true
		}
	}
	rd := newReader(t, c.Locators)
	rd.attrs = map[string][]string{}
	for _, wt := range c.Watch {
		if len(wt.Attrs) > 0 {
			rd.attrs[wt.Element] = wt.Attrs
		}
	}
	states := make(map[string]domain.ElementState, len(names))
	var repair []domain.Candidate
	for _, name := range names {
		res, err := rd.resolve(name)
		if err != nil {
			return states, repair, err
		}
		states[name] = res.state
		if res.state.Count > 1 && needsText[name] {
			repair = append(repair, res.others...)
		}
		if wt := watchOf(c.Watch, name); wt != nil && wt.Presence && !needsText[name] {
			st := states[name]
			st.Presence = true
			states[name] = st
		}
		if res.state.Count == 0 && needsText[name] && res.state.Locator != nil {
			repair = append(repair, uirepair.Candidates(name, *res.state.Locator, axNodes(t))...)
		}
	}
	// An element that only the action named stays out of the record: the target is in it already, and
	// the screen has changed since the action.
	for _, name := range m.Elements() {
		if !slices.ContainsFunc(c.Watch, func(wt domain.Watched) bool { return wt.Element == name }) {
			delete(states, name)
			repair = slices.DeleteFunc(repair, func(cand domain.Candidate) bool { return cand.Element == name })
		}
	}
	if len(states) == 0 {
		states = nil
	}
	return states, repair, nil
}

func watchOf(watch []domain.Watched, name string) *domain.Watched {
	for i := range watch {
		if watch[i].Element == name {
			return &watch[i]
		}
	}
	return nil
}

// readUntil reads the screen that settled. When the step has checks, it reads again until they all
// hold or CheckTimeout passes, and gives the last reading. A check that an element is not there, or
// hidden, holds on a screen that has not drawn the element yet, so after such a check holds the engine
// waits until the screen is still and reads once more.
func (e *Engine) readUntil(ctx context.Context, d Driver, first *Tree, c Call) (map[string]domain.ElementState, []domain.Candidate, error) {
	until := slices.DeleteFunc(slices.Clone(c.Until), func(k domain.Check) bool { return k.Element == "" })
	states, repair, err := e.read(first, c)
	if len(until) == 0 || err != nil {
		return states, repair, err
	}
	deadline := e.o.Clock().Add(e.o.CheckTimeout)
	held := e.hold(c, until, states, repair)
	for !held && e.o.Clock().Before(deadline) && ctx.Err() == nil {
		if err := e.o.Sleep(ctx, checkPoll); err != nil {
			break
		}
		t, err := d.Source(ctx)
		if err != nil {
			return states, repair, err
		}
		if states, repair, err = e.read(t, c); err != nil {
			return states, repair, err
		}
		held = e.hold(c, until, states, repair)
	}
	if held && slices.ContainsFunc(until, absence) {
		t, err := e.settle(ctx, d, e.o.Settle)
		if err != nil {
			return states, repair, err
		}
		return e.read(t, c)
	}
	return states, repair, nil
}

// absence is a check that holds when something is not shown.
func absence(k domain.Check) bool {
	return k.Element != "" && (k.Exists != nil && !*k.Exists || k.Count != nil && *k.Count == 0 || k.State == "hidden")
}

// hold says whether every check is true of what was read. The verdict decides the outcome later, from
// the stored action: this only tells the engine whether to read again.
func (e *Engine) hold(c Call, until []domain.Check, states map[string]domain.ElementState, repair []domain.Candidate) bool {
	surface := domain.SurfaceAndroid
	if e.o.Platform != nil {
		surface = e.o.Platform.Surface()
	}
	act := domain.Action{Surface: surface, Mobile: &domain.MobileRecord{Op: string(c.Action.Do), Elements: states, Repair: repair}}
	for _, k := range until {
		if check.Eval(k, act).State != domain.CheckTrue {
			return false
		}
	}
	return true
}

// screenshot stores one JPEG of the screen, at most 1280 pixels on its longest side, and gives its path
// below the run directory. It is evidence of the step, so a failure to take it never fails the step,
// and a secure screen gives none.
func (e *Engine) screenshot(ctx context.Context, d Driver, a domain.Action) (string, error) {
	if e.o.Files == nil {
		return "", errors.New("the engine has no place for files")
	}
	shotCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	png, err := d.Screenshot(shotCtx)
	if err != nil {
		return "", err
	}
	data, err := Shrink(png)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	e.shots++
	n := e.shots
	e.mu.Unlock()
	dir := string(a.Use.Scenario)
	if dir == "" {
		dir = "run"
	}
	name := fmt.Sprintf("%04d", n)
	if a.Use.Step != "" {
		name += "-" + string(a.Use.Step)
	}
	rel := "screenshots/" + dir + "/" + name + ".jpg"
	if err := e.o.Files.WriteFile(rel, data); err != nil {
		return "", err
	}
	return rel, nil
}

func captureNames(caps []domain.Capture) []string {
	names := make([]string, len(caps))
	for i, c := range caps {
		names[i] = c.Name
	}
	return names
}

// capture gives the text of each element capture whose element was found. Anything else is missing.
func capture(states map[string]domain.ElementState, caps []domain.Capture) (map[string]string, []string) {
	if len(caps) == 0 {
		return nil, nil
	}
	got := map[string]string{}
	var missing []string
	for _, c := range caps {
		st, ok := states[c.Element]
		if c.Element == "" || !ok || !st.Found {
			missing = append(missing, c.Name)
			continue
		}
		got[c.Name] = st.Text
	}
	return got, missing
}

// record ends every path through Do that has an action to store.
func (e *Engine) record(ctx context.Context, label string, a domain.Action, captured map[string]string, missing []string) (Result, error) {
	if captured == nil {
		captured = map[string]string{}
	}
	if len(captured) > 0 {
		a.Captures = maps.Clone(captured)
	}
	stored, err := e.o.Recorder.Record(a)
	if err != nil {
		return Result{}, fmt.Errorf("mobile %s: %w", label, err)
	}
	res := Result{Action: stored, Captured: captured, Missing: missing}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("mobile %s: %w", label, err)
	}
	return res, nil
}
