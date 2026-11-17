package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/secret"
)

const (
	defaultBudget        = 200
	defaultActionTimeout = 30 * time.Second
	defaultSettle        = 8 * time.Second
	waitLimit            = 10 * time.Second

	refusedNoUseCase = "a write must belong to a scenario step, an exploration, or a probe"
	refusedBudget    = "action budget reached"
)

// Files stores a file below the run directory.
type Files interface {
	WriteFile(rel string, data []byte) error
}

type Options struct {
	Policy   *effects.Policy
	Recorder *recorder.Recorder
	Files    Files
	Services map[string]string // service name to base URL
	Secrets  secret.Resolver
	// Accounts is the web sign-in of each account that has one, by the name of the account. An open
	// of a step of such an account signs in when the page shows a sign-in form.
	Accounts map[string]config.WebSignIn
	// Home is the Semar home, which holds the profiles of the browser. Environment names the
	// environment that the profiles belong to.
	Home        string
	Environment string
	Chrome      string // the program to start; "" finds one
	Headed      bool   // show the window
	Run         domain.RunID
	Budget      int // default 200 actions per engine
	// ActionTimeout bounds one action, and Settle bounds the wait until a page looks still. The
	// defaults are 30 and 8 seconds.
	ActionTimeout time.Duration
	Settle        time.Duration
	Clock         func() time.Time
	TraceID       func() string // 32 lower-case hex characters
	SpanID        func() string // 16 lower-case hex characters
	Logger        *slog.Logger
}

// Watched is an element that a check or a capture of the step names. Presence is true when only its
// existence or its number is asked: such an element that is absent is no reason for a repair.
type Watched struct {
	Element  string
	Presence bool
}

type Call struct {
	Use    domain.UseCase
	By     domain.Actor
	Action domain.WebAction
	// Locators holds the locator of every element that the action and the watched elements name, and
	// of every element that their within chains name, with the variables filled in.
	Locators    map[string]domain.Locator
	Watch       []Watched
	Captures    []domain.Capture
	AuthProfile string // the account of the browser profile; "" is default
	Why         string
}

type Result struct {
	Action   domain.Action     // redacted, as stored
	Captured map[string]string // raw values, for later steps only. Never store them
	Missing  []string          // capture names that gave no value
}

type Engine struct {
	o Options

	mu       sync.Mutex
	sessions map[string]*session
	sent     int
	shots    int
	closed   bool
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
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.TraceID == nil {
		o.TraceID = func() string { return randomHex(16) }
	}
	if o.SpanID == nil {
		o.SpanID = func() string { return randomHex(8) }
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Engine{o: o, sessions: map[string]*session{}}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Close ends every browser that the engine started, and waits until it has exited so that its
// profile is written to the disk.
func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	e.NewScenario()
}

// NewScenario ends every browser that the engine started, so that the scenario that comes next
// starts with no page, no state of a tab, and no session cookie of the one before. The profile on
// the disk stays, and with it what a person signed in to with semar login.
func (e *Engine) NewScenario() {
	e.mu.Lock()
	sessions := slices.Collect(maps.Values(e.sessions))
	e.sessions = map[string]*session{}
	e.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
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

// Do never returns an error for a refusal or for an element that is not there: those are in
// Result.Action. It returns an error for an unknown service, an action that is not well formed, a
// recorder failure, a context that was cancelled, and a Chrome that cannot be found, which wraps
// ErrChromeNotFound.
func (e *Engine) Do(ctx context.Context, c Call) (Result, error) {
	w := c.Action
	label := describe(w)
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("web %s: %w", label, err)
	}
	base, known := e.o.Services[w.Service]
	if !known {
		return Result{}, fmt.Errorf("web: unknown service %q", w.Service)
	}
	if ps := w.Problems(); len(ps) > 0 {
		return Result{}, fmt.Errorf("web %s: %s", label, ps[0])
	}
	a := domain.Action{
		Surface: domain.SurfaceWeb,
		Op:      string(w.Do),
		Use:     c.Use,
		By:      c.By,
		TraceID: e.o.TraceID(),
		At:      e.o.Clock(),
		Why:     c.Why,
		Web:     &domain.WebRecord{Op: string(w.Do), App: w.App, Screen: w.Screen},
	}
	if loc, ok := c.Locators[w.Element]; ok && w.Element != "" {
		a.Web.Target = &loc
	}
	missing := captureNames(c.Captures)

	var open *url.URL
	switch {
	case w.Do.Writes() && c.Use.Pass == "":
		a.Refused = refusedNoUseCase
	case w.Do.Writes() && e.o.Policy.ReadOnly():
		a.Refused = fmt.Sprintf("%s: %s writes, and the environment is read only", effects.ReadOnly, w.Do)
	case w.Do == domain.WebOpen:
		head, tail := script.SplitWebPath(w.Path)
		u, err := api.JoinURL(base, head, nil)
		if err == nil && tail != "" {
			u, err = url.Parse(u.String() + tail)
		}
		if err != nil {
			a.Err = "the page address does not parse"
			break
		}
		if err := e.o.Policy.Admit(effects.Request{Method: "GET", URL: u}); err != nil {
			a.Refused = refusalText(err, "open "+path.Clean("/"+strings.TrimPrefix(w.Path, "/")))
			break
		}
		open = u
		a.Web.URL = u.String()
	}
	if a.Refused != "" || a.Err != "" {
		return e.record(ctx, label, a, nil, missing)
	}
	if !e.take() {
		a.Refused = refusedBudget
		return e.record(ctx, label, a, nil, missing)
	}

	value := w.Value
	if w.Secret != "" {
		if e.o.Secrets == nil {
			a.Err = "the secret could not be read: the engine has no secret source"
			return e.record(ctx, label, a, nil, missing)
		}
		v, err := e.o.Secrets.Resolve(ctx, w.Secret)
		if err != nil {
			a.Err = "the secret could not be read: " + err.Error()
			return e.record(ctx, label, a, nil, missing)
		}
		value = v
	}

	s, err := e.session(c.AuthProfile)
	if err != nil {
		if errors.Is(err, ErrChromeNotFound) {
			return Result{}, fmt.Errorf("web %s: %w", label, err)
		}
		a.Err = err.Error()
		return e.record(ctx, label, a, nil, missing)
	}
	return e.run(ctx, s, c, open, value, label, a, missing)
}

func describe(w domain.WebAction) string {
	parts := []string{string(w.Do)}
	if w.Screen != "" {
		parts = append(parts, w.Screen)
	}
	if w.Element != "" {
		parts[len(parts)-1] += "." + w.Element
	}
	return strings.Join(parts, " ")
}

// run does the action on the page, waits until the page has settled, reads what the checks need, and records.
func (e *Engine) run(ctx context.Context, s *session, c Call, open *url.URL, value, label string, a domain.Action, missing []string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tabCtx, cancel := context.WithTimeout(s.ctx, e.o.ActionTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	s.guard.begin(a.TraceID)

	w := c.Action
	began := e.o.Clock()
	rd := newReader(tabCtx, c.Locators)
	var opErr error
	dispatched := false
	switch w.Do {
	case domain.WebOpen:
		opErr = s.navigate(tabCtx, open.String())
		dispatched = true
		if who, ok := e.o.Accounts[accountName(c.AuthProfile)]; ok && opErr == nil {
			opErr = e.signInAfterOpen(tabCtx, s, accountName(c.AuthProfile), who, open, &a)
		}
	case domain.WebWait:
		if w.Element != "" {
			res, wrd, err := waitFor(tabCtx, c.Locators, w.Element, waitLimit)
			rd.release()
			rd = wrd
			opErr = notFoundOr(err, w.Element, res)
		}
	default:
		opErr, dispatched = e.perform(rd, c, value)
	}
	// An element that the action could not find is read from the page as the action saw it. After the
	// action the page has changed: a button that was pressed may be gone, and that is no miss.
	var miss *notFoundMiss
	var nf notFoundError
	if errors.As(opErr, &nf) {
		if r, ok := rd.memo[nf.element]; ok && r.state.Count == 0 && r.state.Locator != nil {
			miss = &notFoundMiss{element: nf.element, state: r.state}
			if tree, err := rd.axNodes(); err == nil {
				miss.repair = Candidates(nf.element, *r.state.Locator, tree)
			}
		}
	}
	rd.release()
	s.settle(tabCtx, e.o.Settle)

	// The page is read again after it has settled, with a reader of its own: the one of the action
	// holds what the page looked like before.
	post := newReader(tabCtx, c.Locators)
	defer post.release()
	states, repair, readErr := e.read(post, c)
	if miss != nil {
		if states == nil {
			states = map[string]domain.ElementState{}
		}
		if _, read := states[miss.element]; !read {
			states[miss.element] = miss.state
		}
		if !slices.ContainsFunc(repair, func(c domain.Candidate) bool { return c.Element == miss.element }) {
			repair = append(repair, miss.repair...)
		}
	}
	a.Web.Elements = states
	a.Web.Repair = repair
	a.DurationMS = e.o.Clock().Sub(began).Milliseconds()
	a.Web.Blocked = s.guard.blockedHosts()
	if loc, err := s.location(tabCtx); err == nil {
		a.Web.URL = loc
	}

	switch {
	case ctx.Err() != nil:
		a.Err = "the run was interrupted"
	case opErr != nil:
		a.Err = errorText(opErr)
		a.EffectUnknown = w.Do.Writes() && dispatched && !isNotFound(opErr)
	case readErr != nil:
		a.Err = "the page could not be read: " + errorText(readErr)
	}
	if shot, err := e.screenshot(tabCtx, a); err != nil {
		e.o.Logger.DebugContext(ctx, "no screenshot", "error", err)
	} else {
		a.Web.Screenshot = shot
	}

	var captured map[string]string
	if a.Err == "" || isNotFoundText(a.Err) {
		captured, missing = capture(states, c.Captures)
	}
	return e.record(ctx, label, a, captured, missing)
}

// perform acts on the element that the step names. It gives the error, and whether an input event was
// sent before the error: a write whose effect is not known.
func (e *Engine) perform(rd *reader, c Call, value string) (err error, dispatched bool) {
	w := c.Action
	target, err := rd.resolve(w.Element)
	if err != nil && w.Element != "" {
		return err, false
	}
	if w.Element != "" && !target.state.Found {
		return notFound(w.Element, target), false
	}
	switch w.Do {
	case domain.WebClick:
		return rd.click(target.id), true
	case domain.WebFill:
		return rd.fill(target.id, value), true
	case domain.WebPress:
		if w.Element != "" {
			if err := rd.focus(target.id); err != nil {
				return err, false
			}
		}
		return pressKey(rd.ctx, value), true
	case domain.WebDrag:
		to, err := rd.resolve(w.To)
		if err != nil {
			return err, false
		}
		if !to.state.Found {
			return notFound(w.To, to), false
		}
		return rd.drag(target.id, to.id), true
	}
	return fmt.Errorf("the action %s is not known", w.Do), false
}

// notFoundMiss is an element that the action looked for and did not find, with what the page held then.
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

func notFound(name string, r resolved) error { return notFoundError{name, r.state.Count} }

func notFoundOr(err error, name string, r resolved) error {
	switch {
	case err != nil:
		return err
	case !r.state.Found:
		return notFound(name, r)
	}
	return nil
}

func isNotFound(err error) bool {
	var n notFoundError
	return errors.As(err, &n)
}

func isNotFoundText(s string) bool { return strings.HasPrefix(s, check.NotFound) }

// errorText is the first line of an error, which never holds the page's data.
func errorText(err error) string {
	first, _, _ := strings.Cut(err.Error(), "\n")
	return first
}

// read gives the state of every element that the step names, and the repair candidates for each
// element that is not there and that a check or a capture reads the text of.
func (e *Engine) read(rd *reader, c Call) (map[string]domain.ElementState, []domain.Candidate, error) {
	w := c.Action
	var names []string
	needsText := map[string]bool{}
	for _, name := range w.Elements() {
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
	states := make(map[string]domain.ElementState, len(names))
	var repair []domain.Candidate
	for _, name := range names {
		res, err := rd.resolve(name)
		if err != nil {
			return states, repair, err
		}
		states[name] = res.state
		if wt := watchOf(c.Watch, name); wt != nil && wt.Presence && !needsText[name] {
			st := states[name]
			st.Presence = true
			states[name] = st
		}
		if res.state.Count == 0 && needsText[name] && res.state.Locator != nil {
			tree, err := rd.axNodes()
			if err != nil {
				return states, repair, err
			}
			repair = append(repair, Candidates(name, *res.state.Locator, tree)...)
		}
	}
	// An element that only the action named stays out of the record: the target is in it already, and
	// the page has changed since the action.
	for _, name := range w.Elements() {
		if !slices.ContainsFunc(c.Watch, func(wt Watched) bool { return wt.Element == name }) {
			delete(states, name)
			// Its candidates are those of a miss that the action saw, and not of what the page shows now.
			repair = slices.DeleteFunc(repair, func(cand domain.Candidate) bool { return cand.Element == name })
		}
	}
	if len(states) == 0 {
		states = nil
	}
	return states, repair, nil
}

func watchOf(watch []Watched, name string) *Watched {
	for i := range watch {
		if watch[i].Element == name {
			return &watch[i]
		}
	}
	return nil
}

// screenshot stores one PNG of the page and gives its path below the run directory.
func (e *Engine) screenshot(ctx context.Context, a domain.Action) (string, error) {
	if e.o.Files == nil {
		return "", errors.New("the engine has no place for files")
	}
	data, err := capturePNG(ctx)
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
	rel := "screenshots/" + dir + "/" + name + ".png"
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
		return Result{}, fmt.Errorf("web %s: %w", label, err)
	}
	res := Result{Action: stored, Captured: captured, Missing: missing}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("web %s: %w", label, err)
	}
	return res, nil
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
