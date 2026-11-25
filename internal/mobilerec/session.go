package mobilerec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/screens"
)

const defaultSettle = 3 * time.Second

// Engine is what the session acts through: the engine of a replay, which holds the policy, the budget,
// the secrets, and the redaction. mobile.Engine is one.
type Engine interface {
	Do(ctx context.Context, c mobile.Call) (mobile.Result, error)
	NewScenario()
}

// Handle is one open device session. Close ends it and releases the device, so that a replay may take it.
type Handle struct {
	Engine   Engine
	Platform mobile.Platform
	Close    func(ctx context.Context) error
}

// PlatformFactory opens a device session. lookup reads the environment with the secrets and the serial of
// the recording on top: the engine, its secret resolver, and its redactor must read through it. A session
// asks for one more handle each time that it must give the device away, because a closed platform cannot
// be used again.
type PlatformFactory func(ctx context.Context, lookup LookupEnv) (Handle, error)

// Options say what a session records and where it writes.
type Options struct {
	Repo    string
	Kind    Kind
	ID      string // the flow or the scenario
	Ticket  string // of a scenario
	Title   string
	Service string
	App     string // the screens file; the default is the service
	Open    PlatformFactory
	// Lookup is the environment under the overlay. The default reads nothing.
	Lookup LookupEnv
	// Overlay is the layer of secrets and pins. The default is a new one over Lookup.
	Overlay *Overlay
	// Reset is the reset of the first launch: restart, clear, none, or "" for the one of the service.
	Reset string
	// Use is a flow that a scenario starts with. It runs live, and it is recorded as a use step.
	Use     string
	UseWith map[string]string
	// Resume reopens a saved flow or scenario: its steps are loaded, and Start replays them on the device up
	// to ResumeAt. Recording goes on from there.
	Resume *Loaded
	// ResumeAt is the number of steps to replay. 0 or less means all of them.
	ResumeAt int
	// ConfirmClear says that the person agreed to reset: clear on a physical device.
	ConfirmClear bool
	// Author is set when an agent records over MCP, and not a person: the script is authored by it, every
	// recorded check is a baseline written by a model, and a flow says in its description who recorded it.
	// Nil for the terminal and the studio, which change nothing.
	Author *domain.ProviderRef
	// Settle bounds the wait for a still screen. The default is 3 seconds.
	Settle time.Duration
	// SettlePoll is a fixed delay between the reads of a settle. Zero is the growing delay of a replay.
	SettlePoll time.Duration
	Clock      func() time.Time
	Sleep      func(ctx context.Context, d time.Duration) error
	Now        func() time.Time
}

// Session records one flow or one scenario. It is safe for use by one goroutine at a time: every call holds
// the lock of the session, so that a studio that serves two requests at once still gives the device one
// action at a time.
type Session struct {
	o  Options
	ov *Overlay

	mu      sync.Mutex
	h       Handle
	device  mobile.About
	open    bool
	started bool

	seq      uint64
	frame    *Frame
	tree     *mobile.Tree
	nodes    []*mobile.Node
	lastShot []byte // shrunk JPEG from the last engine action; nil when secret or refreshed directly
	screen   string // the name that the person gave the screen of the frame, or ""

	events    []*event
	wantClear bool
	last      *Saved
	notice    string

	// at is the cursor: the number of steps that the device has been taken through. A new step goes at the
	// cursor, which is the end of the recording unless a goto or a delete moved it.
	at int
	// dirty says that a step before the cursor was deleted, so the device shows what that step did.
	dirty bool
	// aliases map the name that a screen gets from its heading to the name that the person chose.
	aliases map[string]string
	// known is the screens file as it was last read, for the guess of a screen name.
	known     *domain.Screens
	guessTree string
	guessName string
	// params and vars are the values of the {{names}} that the steps use: the params of a reopened flow, and
	// what the steps captured.
	params map[string]string
	vars   map[string]string
	// newParams are the params that MakeParam made: the flow gets them when it is saved.
	newParams []domain.FlowParam

	// gap says that a person did something on the device by hand that no step records (see Unrecorded),
	// and gapAt is the number of steps there were when it happened. unrec and ranUnrec are set while an
	// unrecorded action runs: the first keeps its step off the timeline, the second says that the device
	// was touched even if the call failed afterwards.
	gap, unrec, ranUnrec bool
	gapAt                int
	// ime is the package of the keyboard, when the caller knows it: it tells the keyboard window from an
	// overlay in the elements of a frame.
	ime string
}

// New makes a session. It opens nothing: Start does.
func New(o Options) (*Session, error) {
	if o.Open == nil {
		return nil, errors.New("a recording session needs a way to open the device")
	}
	if o.Kind != KindFlow && o.Kind != KindScenario {
		return nil, fmt.Errorf("a session records a flow or a scenario, not %q", o.Kind)
	}
	if o.Kind == KindFlow {
		if err := flow.CheckID(o.ID); err != nil {
			return nil, err
		}
	}
	if o.Resume != nil && o.Use != "" {
		return nil, errors.New("a reopened recording has its own steps: leave out the flow to start with")
	}
	if o.Service == "" {
		return nil, errors.New("a recording session needs the service of the app")
	}
	if o.App == "" {
		o.App = o.Service
	}
	if o.Settle <= 0 {
		o.Settle = defaultSettle
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	ov := o.Overlay
	if ov == nil {
		ov = NewOverlay(o.Lookup)
	}
	s := &Session{o: o, ov: ov, aliases: map[string]string{}, params: map[string]string{}, vars: map[string]string{}}
	if o.Resume != nil {
		if err := s.load(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Overlay gives the layer of secrets and pins of the session, for a verify that must see the same.
func (s *Session) Overlay() *Overlay { return s.ov }

// Kind and ID say what the session records.
func (s *Session) Kind() Kind { return s.o.Kind }
func (s *Session) ID() string { return s.o.ID }

// Ticket and Title are what the recording is saved with now, after the defaults of the command.
func (s *Session) Ticket() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.o.Ticket
}

func (s *Session) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.o.Title
}

// Start opens the device, resets the app, and shows the first frame. A recording always starts with a
// reset, so a replay starts where the recording did. A scenario with a flow to use runs the flow.
func (s *Session) Start(ctx context.Context) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil, errors.New("the session has started already")
	}
	if err := s.openLocked(ctx); err != nil {
		return nil, err
	}
	if s.o.Resume != nil {
		return s.resumeLocked(ctx)
	}
	if s.device.Device != "" && !strings.HasPrefix(s.device.Device, "emulator-") && s.o.Reset == "clear" && !s.o.ConfirmClear {
		_ = s.closeLocked(ctx)
		return nil, ErrNeedsConfirm
	}
	s.started = true
	return s.beginLocked(ctx)
}

func (s *Session) beginLocked(ctx context.Context) (*Frame, error) {
	if s.o.Use != "" {
		f, _, err := s.useFlowLocked(ctx, s.o.Use, s.o.UseWith)
		return f, err
	}
	return s.launchLocked(ctx, true)
}

// openLocked asks the factory for a device session, and pins the serial of the device that it got.
func (s *Session) openLocked(ctx context.Context) error {
	h, err := s.o.Open(ctx, s.ov.Lookup)
	if err != nil {
		return s.wrap(err)
	}
	d, err := h.Platform.Driver(ctx, s.o.Service)
	if err != nil {
		if h.Close != nil {
			_ = h.Close(context.WithoutCancel(ctx))
		}
		return s.wrap(err)
	}
	s.h, s.device, s.open = h, d.About(), true
	if s.device.Device != "" {
		s.ov.Pin(SerialEnv, s.device.Device)
	}
	h.Engine.NewScenario()
	return nil
}

// newReplayLocked makes the next action start with the app reset, and gives a replay of the steps the whole
// budget of the engine, when the engine has one to give.
func (s *Session) newReplayLocked() {
	// A replay starts from the reset, so what a person did by hand is gone from the device.
	s.gap = false
	s.h.Engine.NewScenario()
	if r, ok := s.h.Engine.(interface{ RefillBudget() }); ok {
		r.RefillBudget()
	}
}

// Emulator reports whether the device is an emulator, which a reset may clear without asking.
func (s *Session) Emulator() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.HasPrefix(s.device.Device, "emulator-")
}

// Device is the serial of the device of the recording.
func (s *Session) Device() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.device.Device
}

// Suspend gives the device away: it ends the device session, so that the lease is free for a verify. The
// steps and the secrets stay. Resume takes the device again.
func (s *Session) Suspend(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked(ctx)
}

func (s *Session) closeLocked(ctx context.Context) error {
	s.lastShot = nil
	if !s.open {
		return nil
	}
	s.open = false
	if s.h.Close == nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return s.wrap(s.h.Close(cctx))
}

// Resume opens a new device session on the same device, and resets the app. The steps stay: a recording
// that continues after a verify starts again from the reset.
func (s *Session) Resume(ctx context.Context) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open {
		return s.refreshLocked(ctx)
	}
	if err := s.openLocked(ctx); err != nil {
		return nil, err
	}
	s.gap = false // the app is reset again
	return s.launchLocked(ctx, false)
}

// Close gives the device away. The overlay is kept, because a verify may follow: Wipe drops it.
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked(ctx)
}

// Wipe drops the secrets and the pins of the session.
func (s *Session) Wipe() {
	s.mu.Lock()
	s.lastShot = nil
	s.mu.Unlock()
	s.ov.Clear()
}

// wrap scrubs an error, so that no message of the device or the engine carries a secret.
func (s *Session) wrap(err error) error {
	if err == nil {
		return nil
	}
	var un *mobile.Unavailable
	if errors.As(err, &un) {
		return &mobile.Unavailable{Reason: s.ov.Scrub(un.Reason)}
	}
	var se *StaleError
	if errors.As(err, &se) {
		return err
	}
	var ae *ActError
	if errors.As(err, &ae) {
		return &ActError{Msg: s.ov.Scrub(ae.Msg)}
	}
	if msg := err.Error(); s.ov.Leaks(msg) {
		return errors.New(s.ov.Scrub(msg))
	}
	return err
}

func (s *Session) liveCheck() error {
	if !s.started {
		return errors.New("the session has not started")
	}
	if !s.open {
		return errors.New("the device is not open: resume the session first")
	}
	return nil
}

// Frame gives the frame that the person sees now, without reading the device.
func (s *Session) Frame() *Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frame
}

// Refresh reads the device again, and gives a frame with a new number.
func (s *Session) Refresh(ctx context.Context) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, err
	}
	return s.refreshLocked(ctx)
}

// Peek reads the screen once and says whether it is still the one of the frame. A screen that changed on
// its own, as when a page loads after the action was done, gives its new frame; a screen that did not
// gives nil and false. A frame that is not the newest one gives nil and false too: its answer is on the way.
// The read is cut short after limit, because Peek holds the device and a person may be about to act: a
// screen that is slower to read than that gives slow, and nothing else.
func (s *Session) Peek(ctx context.Context, frame uint64, limit time.Duration) (f *Frame, changed, slow bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, false, false, err
	}
	if s.frame == nil || s.tree == nil || s.frame.ID != frame {
		return nil, false, false, nil
	}
	d, err := s.drv(ctx)
	if err != nil {
		return nil, false, false, err
	}
	rctx, cancel := context.WithTimeout(ctx, limit)
	t, err := d.Source(rctx)
	cancel()
	switch {
	case err != nil && rctx.Err() != nil && ctx.Err() == nil:
		return nil, false, true, nil
	case err != nil:
		return nil, false, false, s.wrap(fmt.Errorf("the screen could not be read: %w", err))
	case t.Digest == s.tree.Digest:
		return nil, false, false, nil
	}
	f, err = s.refreshLocked(ctx)
	return f, err == nil, false, err
}

// Clipboard reads the clipboard of the device through the driver of the session, which holds the device.
// It does not change what the recording knows: it is no step, and the frame stays.
func (s *Session) Clipboard(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return "", err
	}
	d, err := s.drv(ctx)
	if err != nil {
		return "", err
	}
	r, ok := d.(mobile.ClipboardReader)
	if !ok {
		return "", errors.New("this device cannot read the clipboard during a recording")
	}
	return r.Clipboard(ctx)
}

// drv gives the driver of the app now. It is asked for at each use and never kept: the platform may
// start the server again in the middle of a recording, and a driver that was handed out before points at
// a session that no longer exists.
func (s *Session) drv(ctx context.Context) (mobile.Driver, error) {
	d, err := s.h.Platform.Driver(ctx, s.o.Service)
	if err != nil {
		return nil, s.wrap(err)
	}
	return d, nil
}

func (s *Session) settle(ctx context.Context) (*mobile.Tree, error) {
	d, err := s.drv(ctx)
	if err != nil {
		return nil, err
	}
	return mobile.SettleEvery(ctx, d, s.o.Settle, s.o.SettlePoll, s.o.Clock, s.o.Sleep)
}

func (s *Session) refreshLocked(ctx context.Context) (*Frame, error) {
	s.lastShot = nil
	t, err := s.settle(ctx)
	if err != nil {
		return nil, s.wrap(fmt.Errorf("the screen could not be read: %w", err))
	}
	return s.setFrame(t), nil
}

func checkName(name string) error {
	if name == "" {
		return errors.New("a screen needs a name")
	}
	for i, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || (i > 0 && (r == '-' || r == '_'))
		if !ok {
			return fmt.Errorf("the name %q must start with a letter or a digit and hold letters, digits, - and _", name)
		}
	}
	return nil
}

// titleName turns the slug of a heading into the name of a screen: Sign-in becomes SignIn.
func titleName(slug string) string {
	var b strings.Builder
	for _, part := range strings.Split(slug, "-") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	name := b.String()
	if name == "" || !(name[0] >= 'A' && name[0] <= 'Z' || name[0] >= 'a' && name[0] <= 'z') {
		return "Screen1"
	}
	return name
}

// interesting says whether a node is one that a person may want to act on or check: it takes a touch or
// text, or it shows text or a description of its own. A box that only holds other nodes is left out, though
// the text of its children is its text too.
func interesting(n *mobile.Node) bool {
	if !n.Visible || n.Bounds.Empty() {
		return false
	}
	return Clickable(n) || Editable(n) || n.Desc != "" || n.Attrs["text"] != ""
}

// ListElements lists the elements that a person may act on or check, with the node of each, in document
// order. The Index of an element is its place in the list. It is what a frame shows, before the text is
// scrubbed and the locators are found, and the tool server uses it too, so that both front ends filter
// alike. A node of the status bar or the navigation bar is left out unless the app in front is the system
// UI. The hints name the app and the keyboard, so that each element says which window holds it; with no
// hints the first window that is not the system is the app.
func ListElements(t *mobile.Tree, hints mobile.WindowHints) ([]Element, []*mobile.Node) {
	if t == nil {
		return nil, nil
	}
	ws := mobile.Windows(t, hints)
	var els []Element
	var nodes []*mobile.Node
	t.Walk(func(n *mobile.Node) bool {
		if n.Package == "com.android.systemui" && hints.App != "com.android.systemui" {
			return true
		}
		if !interesting(n) {
			return true
		}
		e := Element{
			Index: len(nodes), Class: n.Class, Role: n.Role, ID: n.ID, Desc: n.Desc, Text: n.Text, Hint: n.Hint,
			Bounds: n.Bounds, Clickable: Clickable(n), Editable: Editable(n), Password: n.Password,
			Enabled: n.Enabled, Checked: n.Checked, Focused: n.Focused, Package: n.Package,
		}
		if w := mobile.WindowOf(n, ws); w != nil {
			e.Window = string(w.Kind)
			if e.Package == "" {
				e.Package = w.Package
			}
		}
		els = append(els, e)
		nodes = append(nodes, n)
		return true
	})
	return els, nodes
}

// hints are what the session knows about the windows of a tree: the app, and the keyboard if it was told
// or can be told from the names of the windows.
func (s *Session) hints(t *mobile.Tree) mobile.WindowHints {
	h := mobile.WindowHints{App: s.device.Package, IME: s.ime}
	if h.IME == "" && t != nil && t.Root != nil && len(t.Root.Children) > 1 {
		h.IME = guessIME(t, h.App)
	}
	return h
}

// setFrame turns a tree into the next frame. The tree is kept as it is, with its text, for the matching
// of the next action. The frame holds no secret value: the text of a node that shows one is redacted.
func (s *Session) setFrame(t *mobile.Tree) *Frame {
	s.seq++
	s.lastShot = nil
	f := &Frame{ID: s.seq, Width: t.Width, Height: t.Height, Screen: s.screenName(t), Notice: s.notice}
	s.notice = ""
	s.tree = t
	s.nodes = nil
	els, nodes := ListElements(t, s.hints(t))
	for i, e := range els {
		n := nodes[i]
		leaked := false
		clean := func(v string) string {
			if s.ov.Leaks(v) {
				leaked = true
				return s.ov.Scrub(v)
			}
			return v
		}
		e.ID, e.Desc, e.Text, e.Hint = clean(n.ID), clean(n.Desc), clean(n.Text), clean(n.Hint)
		for _, v := range n.Attrs {
			clean(v)
		}
		if leaked {
			f.Secret = true
		} else {
			found, _ := LocateAll(t, targetFor(n, ForTap), ForTap, s.ov)
			switch {
			case len(found) > 0 && !found[0].Explicit:
				loc := found[0].Locator
				e.Locator, e.Stability = &loc, found[0].Stability
				if found[0].Tier == TierWithin {
					e.Warning = "found only inside another element: check that the choice is right, or see loc N"
				}
			case len(found) > 0:
				e.Warning = "no locator finds this alone: only a position does (" + found[0].Note + "), and it is used only when you pick it"
			default:
				e.Warning = "no locator finds this alone, so it cannot be recorded"
			}
		}
		f.Elements = append(f.Elements, e)
		s.nodes = append(s.nodes, n)
	}
	s.frame = f
	return f
}

// targetFor is the node that a purpose acts on for a node that the person picked.
func targetFor(n *mobile.Node, p Purpose) *mobile.Node {
	switch p {
	case ForTap:
		if c := ClimbClickable(n); c != nil {
			return c
		}
	case ForType:
		if Editable(n) {
			return n
		}
		for p := n.Parent; p != nil; p = p.Parent {
			if Editable(p) {
				return p
			}
		}
		var found *mobile.Node
		var walk func(*mobile.Node)
		walk = func(x *mobile.Node) {
			for _, k := range x.Children {
				if found == nil && k.Visible && Editable(k) {
					found = k
				}
				walk(k)
			}
		}
		walk(n)
		if found != nil {
			return found
		}
	}
	return n
}

// stale gives the error of an action on a frame that is not the current one, with the current frame.
func (s *Session) stale(ctx context.Context) error {
	f, err := s.refreshLocked(ctx)
	if err != nil {
		return err
	}
	return &StaleError{Frame: f}
}

// identity is Identity.
func identity(n *mobile.Node) string { return Identity(n) }

// Identity is what tells a node from the others when the screen is read again: its class, its ids, and
// its place. The text is no part of it, because a field changes its text when it is typed in.
func Identity(n *mobile.Node) string {
	return strings.Join([]string{n.Class, n.ID, collapseSpace(n.Desc), n.Hint, fmt.Sprint(n.Bounds)}, "\x00")
}

// resolve finds the node that a target of a frame means on the screen as it is now. A node that is not
// there, or not alone, makes the action stale: the person acted on a screen that the app changed.
func (s *Session) resolve(ctx context.Context, frame uint64, t Target) (*mobile.Tree, *mobile.Node, error) {
	if s.frame == nil || frame != s.frame.ID {
		return nil, nil, s.stale(ctx)
	}
	var picked *mobile.Node
	switch {
	case t.ByPoint:
		picked = HitTest(s.tree, t.X, t.Y)
	case t.Index >= 0 && t.Index < len(s.nodes):
		picked = s.nodes[t.Index]
	}
	if picked == nil {
		return nil, nil, fail("there is no element there")
	}
	d, err := s.drv(ctx)
	if err != nil {
		return nil, nil, err
	}
	fresh, err := d.Source(ctx)
	if err != nil {
		return nil, nil, s.wrap(fmt.Errorf("the screen could not be read: %w", err))
	}
	if fresh.Digest == s.tree.Digest {
		return s.tree, picked, nil
	}
	var same []*mobile.Node
	want := identity(picked)
	fresh.Walk(func(n *mobile.Node) bool {
		if n.Visible && identity(n) == want {
			same = append(same, n)
		}
		return true
	})
	if len(same) != 1 {
		return nil, nil, s.stale(ctx)
	}
	return fresh, same[0], nil
}

func (s *Session) use(step string) domain.UseCase {
	return domain.UseCase{Pass: domain.PassScenario, Scenario: domain.ScenarioID(s.recordID()), Step: domain.StepID(step)}
}

// recordID is the ID that the actions of the recording carry: the ID of the scenario, or of the flow.
func (s *Session) recordID() string {
	if s.o.ID == "" {
		return "recording"
	}
	return s.o.ID
}

// stepID gives an unused ID for a step.
func (s *Session) stepID(base string) string {
	base = record.Slug(base)
	if base == "" {
		base = "step"
	}
	taken := func(id string) bool {
		return slices.ContainsFunc(s.events, func(e *event) bool { return e.id == id })
	}
	id := base
	for i := 2; taken(id); i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return id
}

// run sends one action through the engine, and gives back the engine result or the error text of an
// action that did not go through. The text has no secret in it.
func (s *Session) run(ctx context.Context, step string, m domain.MobileAction, locs map[string]domain.Locator, initial *mobile.Tree) (mobile.Result, error) {
	return s.runCall(ctx, step, m, locs, nil, initial)
}

// runCall is run for an action that captures values: it gives back the engine result, including raw
// captured values for the variables of the session, the final settled tree, and the shrunk screenshot.
// A capture that found no value is a failure.
func (s *Session) runCall(ctx context.Context, step string, m domain.MobileAction, locs map[string]domain.Locator, caps []domain.Capture, initial *mobile.Tree) (mobile.Result, error) {
	m.Service, m.App = s.o.Service, s.o.App
	// The engine reads the elements that a call watches: a capture reads its element after the action.
	var watch []domain.Watched
	for _, c := range caps {
		if c.Element != "" && !slices.ContainsFunc(watch, func(w domain.Watched) bool { return w.Element == c.Element }) {
			watch = append(watch, domain.Watched{Element: c.Element})
		}
	}
	res, err := s.h.Engine.Do(ctx, mobile.Call{
		Use: s.use(step), By: domain.ActorPilot, Action: m, Locators: locs, Captures: caps, Watch: watch,
		Why: "recording " + s.recordID(), InitialTree: initial,
	})
	if err != nil {
		return mobile.Result{}, s.wrap(err)
	}
	switch a := res.Action; {
	case a.Refused != "":
		return mobile.Result{}, fail("the action was refused: %s", s.ov.Scrub(a.Refused))
	case a.Err != "":
		return mobile.Result{}, fail("the action failed: %s", s.ov.Scrub(a.Err))
	case len(res.Missing) > 0:
		return mobile.Result{}, fail("the capture %s found no value", strings.Join(res.Missing, ", "))
	}
	return res, nil
}

func (s *Session) treeHasSecret(t *mobile.Tree) bool {
	if t == nil {
		return false
	}
	secret := false
	t.Walk(func(n *mobile.Node) bool {
		if s.leaks(n) {
			secret = true
			return false
		}
		return true
	})
	return secret
}

// finishActionLocked builds the next frame from the settled tree returned by Engine.Do when available,
// avoiding a second Settle() loop, and caches its shrunk JPEG only if no secret is visible on screen.
func (s *Session) finishActionLocked(ctx context.Context, res mobile.Result) (*Frame, error) {
	if res.Tree != nil {
		f := s.setFrame(res.Tree)
		if !f.Secret && !s.treeHasSecret(res.Tree) && len(res.ShotJPEG) > 0 {
			s.lastShot = res.ShotJPEG
		} else {
			s.lastShot = nil
		}
		return f, nil
	}
	return s.refreshLocked(ctx)
}

// launchLocked resets the app and starts it. The reset of the launch is the one that the session was given.
func (s *Session) launchLocked(ctx context.Context, record bool) (*Frame, error) {
	m := domain.MobileAction{Do: domain.MobileLaunch, Reset: s.o.Reset}
	step := s.stepID("start")
	res, err := s.run(ctx, step, m, nil, nil)
	if err != nil {
		return nil, err
	}
	if record {
		s.insertLocked(&event{id: step, text: "Start the app", setup: true, do: domain.MobileLaunch, reset: s.o.Reset})
	}
	return s.finishActionLocked(ctx, res)
}

// Act does one thing on the device, as a person does, and records it when it went through. It acts on the
// element that the person saw in the frame, found again on the screen as it is now.
func (s *Session) Act(ctx context.Context, a Act) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, err
	}
	return s.actLocked(ctx, a)
}

// Unrecorded does one thing on the device exactly as Act does, and adds no step. It is for a person who
// takes over by hand and does not want the action recorded. The recording then has a gap: the device is
// not where the steps say, and Save refuses until the gap is closed by a Goto, which takes the device
// through the steps again, or by deleting the steps back past it.
func (s *Session) Unrecorded(ctx context.Context, a Act) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, err
	}
	s.unrec, s.ranUnrec = true, false
	defer func() { s.unrec = false }()
	f, err := s.actLocked(ctx, a)
	if s.ranUnrec {
		if !s.gap {
			s.gap, s.gapAt = true, len(s.events)
		}
		s.ranUnrec = false
	}
	return f, err
}

// Gap says whether the device did something by hand that no step records, and the number of steps there
// were when the first such action ran.
func (s *Session) Gap() (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gapLocked()
}

// gapLocked reports the gap. Deleting the steps back past the place where it happened closes it.
func (s *Session) gapLocked() (bool, int) {
	if s.gap && len(s.events) < s.gapAt {
		s.gap = false
	}
	return s.gap, s.gapAt
}

// ErrUnrecorded is the error of a save while steps that a person did by hand are not recorded.
var ErrUnrecorded = errors.New("steps done by hand were not recorded")

// GapError says where the unrecorded steps are. It matches ErrUnrecorded.
type GapError struct{ At int }

func (e *GapError) Error() string {
	return fmt.Sprintf("steps done by hand at step %d were not recorded: undo past them or record them", e.At+1)
}
func (e *GapError) Is(t error) bool { return t == ErrUnrecorded }

// SetIME tells the session which package is the keyboard, so that the keys of a keyboard are told from
// the app in the elements of a frame. "" leaves it to the session to guess from the names of the windows.
func (s *Session) SetIME(pkg string) {
	s.mu.Lock()
	s.ime = strings.TrimSpace(pkg)
	s.mu.Unlock()
}

// addStep puts the step of an action that ran on the timeline, unless the action is unrecorded.
func (s *Session) addStep(ev *event) {
	if s.unrec {
		s.ranUnrec = true
		return
	}
	s.insertLocked(ev)
}

// guessIME names the keyboard from the packages of the windows of a tree, for a caller that gave none: a
// package that is named like an input method. It never names the app or the system.
func guessIME(t *mobile.Tree, app string) string {
	if t == nil || t.Root == nil {
		return ""
	}
	for _, n := range t.Root.Children {
		pkg := n.Package
		if pkg == "" || pkg == app || pkg == "com.android.systemui" || pkg == "android" {
			continue
		}
		low := strings.ToLower(pkg)
		for _, w := range []string{"inputmethod", "keyboard", "honeyboard", "swiftkey", "gboard"} {
			if strings.Contains(low, w) {
				return pkg
			}
		}
	}
	return ""
}

func (s *Session) actLocked(ctx context.Context, a Act) (*Frame, error) {
	switch domain.MobileOp(a.Do) {
	case domain.MobileTap, domain.MobileLongTap, domain.MobileDoubleTap:
		return s.onElement(ctx, a, ForTap)
	case domain.MobileDrag:
		return s.onElement(ctx, a, ForAssert)
	case domain.MobileType:
		return s.onElement(ctx, a, ForType)
	case domain.MobileScroll:
		return s.onElement(ctx, a, ForAssert)
	case domain.MobileSwipe:
		if a.Target != nil {
			return s.onElement(ctx, a, ForAssert)
		}
		return s.plain(ctx, a, "swipe-"+a.Value, swipeText(a.Value))
	case domain.MobilePress:
		return s.plain(ctx, a, "press-"+a.Value, "Press "+a.Value)
	case domain.MobileOpenLink:
		return s.plain(ctx, a, "open", "Open the link "+a.Value)
	case domain.MobileWait:
		if a.Target != nil {
			return s.onElement(ctx, a, ForAssert)
		}
		return s.plain(ctx, a, "wait", "Wait for the screen")
	}
	return nil, fail("the operation %q is not one that a recording does: use tap, longPress, doubleTap, drag, type, swipe, scroll, press, open, or wait", a.Do)
}

// swipeText names a swipe of the whole screen by the finger and by what it shows: a finger that moves up
// shows what is below, which a person calls scrolling down.
func swipeText(direction string) string {
	shows := map[string]string{"up": "down", "down": "up", "left": "right", "right": "left"}[direction]
	if shows == "" {
		return "Swipe " + direction
	}
	return "Swipe " + direction + " (scroll " + shows + ")"
}

// plain acts with no element: a swipe of the screen, a key, a link, a wait.
func (s *Session) plain(ctx context.Context, a Act, id, text string) (*Frame, error) {
	if a.Frame != 0 && (s.frame == nil || a.Frame != s.frame.ID) {
		return nil, s.stale(ctx)
	}
	if err := s.syncedLocked(); err != nil {
		return nil, err
	}
	m := domain.MobileAction{Do: domain.MobileOp(a.Do), Value: a.Value}
	if m.Do == domain.MobileOpenLink {
		m.Value, m.Link = "", a.Value
		m.Screen = s.screenOf(a)
	}
	if ps := m.Problems(); len(ps) > 0 {
		return nil, fail("%s", ps[0])
	}
	sid := s.stepID(id)
	var initial *mobile.Tree
	if m.Do == domain.MobileSwipe && s.tree != nil && s.tree.Width > 0 && s.tree.Height > 0 {
		initial = s.tree
	}
	res, err := s.run(ctx, sid, m, nil, initial)
	if err != nil {
		return nil, err
	}
	ev := &event{id: sid, text: text, do: m.Do, value: m.Value, screen: m.Screen, link: m.Link}
	if m.Do == domain.MobileOpenLink {
		ev.text = "Open the link " + quote(a.Value)
	}
	s.addStep(ev)
	if ev.do != domain.MobileWait && ev.do != domain.MobileSwipe && !(ev.do == domain.MobilePress && (a.Value == string(mobile.KeyHideKeyboard) || a.Value == string(mobile.KeyDelete) || a.Value == string(mobile.KeyTab))) {
		s.screen = ""
	}
	return s.finishActionLocked(ctx, res)
}

func (s *Session) screenOf(a Act) string {
	if a.Screen != "" {
		return a.Screen
	}
	if s.frame != nil {
		return s.frame.Screen
	}
	return "Screen1"
}

// onElement acts on the element of a target.
func (s *Session) onElement(ctx context.Context, a Act, p Purpose) (*Frame, error) {
	if a.Target == nil {
		return nil, fail("%s needs an element", a.Do)
	}
	if a.Screen != "" {
		if err := checkName(a.Screen); err != nil {
			return nil, err
		}
	}
	if err := s.syncedLocked(); err != nil {
		return nil, err
	}
	tree, picked, err := s.resolve(ctx, a.Frame, *a.Target)
	if err != nil {
		return nil, err
	}
	target := targetFor(picked, p)
	if domain.MobileOp(a.Do) == domain.MobileType {
		if !Editable(target) {
			return nil, fail("that element takes no text")
		}
		if a.Secret == "" && (target.Password || looksLikeCredential(target)) {
			if target.Password {
				return nil, ErrNeedsSecret
			}
			s.notice = "this field looks like it takes a password: type it as a secret, so that the text is never saved"
		}
	}
	if s.leaks(target) {
		return nil, ErrSecretOnScreen
	}
	usable, _ := LocateAll(tree, target, p, s.ov)
	best, alts, err := pickCandidate(usable, a.Candidate)
	if err != nil {
		return nil, err
	}
	screen := a.Screen
	if screen == "" {
		screen = s.screenName(tree)
	}
	ev := &event{
		screen: screen, hint: HintOf(target), do: domain.MobileOp(a.Do), purpose: p,
	}
	ev.choose(best, alts)
	initial := tree
	m := domain.MobileAction{Do: ev.do, Screen: screen, Element: probeName}
	if ev.do == domain.MobileDrag {
		if a.To == nil {
			return nil, fail("drag needs the element to drop on")
		}
		toTree, toPicked, err := s.resolve(ctx, a.Frame, *a.To)
		if err != nil {
			return nil, err
		}
		toTarget := targetFor(toPicked, ForAssert)
		if s.leaks(toTarget) {
			return nil, ErrSecretOnScreen
		}
		toUsable, _ := LocateAll(toTree, toTarget, ForAssert, s.ov)
		toBest, toAlts, err := pickCandidate(toUsable, 0)
		if err != nil {
			return nil, fail("the element to drop on: %v", err)
		}
		ev.chooseTo(HintOf(toTarget), toBest, toAlts)
		m.To = ev.to.elem
		initial = toTree
	} else if a.To != nil {
		return nil, fail("%s takes no element to drop on: only drag does", a.Do)
	}
	locs := ev.locs
	switch ev.do {
	case domain.MobileType:
		if a.Secret != "" {
			if err := CheckSecretEnv(a.Secret); err != nil {
				return nil, fail("%v", err)
			}
			if a.Value == "" {
				return nil, fail("a secret needs a value")
			}
			s.ov.SetSecret(a.Secret, a.Value)
			m.Secret = "env:" + a.Secret
			ev.secret = m.Secret
			ev.text = "Type the secret " + m.Secret + " in " + ev.hint
			s.wantClear = true
		} else {
			if a.Value == "" {
				return nil, fail("type needs a text")
			}
			// The device gets the text with its {{names}} filled in, and the file keeps the names.
			filled, err := s.fill(a.Value)
			if err != nil {
				return nil, err
			}
			m.Value, ev.value = filled, a.Value
			ev.text = "Type " + quote(a.Value) + " in " + ev.hint
		}
	case domain.MobileSwipe, domain.MobileScroll:
		m.Value, ev.value = a.Value, a.Value
		verb := "Swipe"
		if ev.do == domain.MobileScroll {
			verb = "Scroll to"
		}
		ev.text = verb + " " + ev.hint
		if a.Value != "" {
			ev.text += " " + a.Value
		}
	case domain.MobileWait:
		ev.text = "Wait for " + ev.hint
	case domain.MobileLongTap:
		ev.text = "Press and hold " + ev.hint
	case domain.MobileDoubleTap:
		ev.text = "Double-tap " + ev.hint
	case domain.MobileDrag:
		m.Value, ev.value = a.Value, a.Value
		ev.text = "Drag " + ev.hint + " to " + ev.to.hint
	default:
		ev.text = "Tap " + ev.hint
	}
	if ps := m.Problems(); len(ps) > 0 {
		return nil, fail("%s", ps[0])
	}
	ev.id = s.stepID(string(ev.do) + "-" + HintOf(target))
	res, err := s.run(ctx, ev.id, m, locs, initial)
	if err != nil {
		return nil, err
	}
	s.addStep(ev)
	if ev.do != domain.MobileWait && ev.do != domain.MobileScroll && ev.do != domain.MobileType && ev.do != domain.MobileSwipe {
		s.screen = ""
	}
	return s.finishActionLocked(ctx, res)
}

func quote(v string) string { return fmt.Sprintf("%q", oneLine(v)) }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// looksLikeCredential says whether a field is named like one that takes a password or a code.
func looksLikeCredential(n *mobile.Node) bool {
	name := strings.ToLower(n.ID + " " + n.Hint + " " + n.Desc)
	for _, w := range []string{"password", "passcode", "passwd", "pin", "secret", "token", "otp", "cvv"} {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

// leaks reports whether a node shows a value that the person typed as a secret.
func (s *Session) leaks(n *mobile.Node) bool {
	for _, v := range []string{n.Text, n.Desc, n.Hint, n.ID} {
		if s.ov.Leaks(v) {
			return true
		}
	}
	return false
}

// Assert records a check of the screen: the text of an element, or that it is there. It does nothing on
// the device. A scenario is the only thing that has checks.
func (s *Session) Assert(ctx context.Context, a Assert) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, err
	}
	if a.Screen != "" {
		if err := checkName(a.Screen); err != nil {
			return nil, err
		}
	}
	if err := s.syncedLocked(); err != nil {
		return nil, err
	}
	tree, picked, err := s.resolve(ctx, a.Frame, a.Target)
	if err != nil {
		return nil, err
	}
	if s.leaks(picked) {
		return nil, ErrSecretOnScreen
	}
	usable, _ := LocateAll(tree, picked, ForAssert, s.ov)
	best, alts, err := pickCandidate(usable, a.Candidate)
	if err != nil {
		return nil, err
	}
	kind := a.Kind
	if kind == "" {
		kind = AssertEquals
	}
	if !kind.Known() {
		return nil, fail("the check %q is not one that a recording does", string(kind))
	}
	want := oneLine(a.Value)
	attr := strings.TrimSpace(a.Attribute)
	switch kind {
	case AssertEquals, AssertContains:
		if want == "" {
			want = picked.Text
		}
		if want == "" {
			return nil, fail("that element shows no text: assert that it exists")
		}
	case AssertCount:
		if n, err := strconv.Atoi(want); err != nil || n < 0 {
			return nil, fail("a count check needs a number of 0 or more")
		}
	case AssertAttribute:
		if attr == "" || strings.ContainsAny(attr, " \t\r\n") {
			return nil, fail("an attribute check needs the name of the attribute")
		}
		if want == "" {
			want = picked.Attrs[attr]
		}
		if want == "" {
			return nil, fail("the element has no value for the attribute %s: say what it must equal", attr)
		}
	default:
		want, attr = "", ""
	}
	if s.ov.Leaks(want) || s.ov.Leaks(attr) {
		return nil, ErrSecretOnScreen
	}
	// A check may expect a captured value: the file keeps {{name}}.
	if err := s.checkRefs(want); err != nil {
		return nil, err
	}
	screen := a.Screen
	if screen == "" {
		screen = s.screenName(tree)
	}
	ev := &event{
		id: s.stepID("check-" + HintOf(picked)), screen: screen, hint: HintOf(picked), purpose: ForAssert,
		assertion: &assertion{kind: kind, value: want, attr: attr},
	}
	ev.choose(best, alts)
	switch kind {
	case AssertExists:
		ev.text = ev.hint + " is on the screen"
	case AssertNotExists:
		ev.text = ev.hint + " is not on the screen"
	case AssertCount:
		ev.text = want + " of " + ev.hint + " are on the screen"
	case AssertAttribute:
		ev.text = ev.hint + " has " + attr + " " + quote(want)
	case AssertContains:
		ev.text = ev.hint + " contains " + quote(want)
	case AssertEquals:
		ev.text = ev.hint + " equals " + quote(want)
	default:
		ev.text = ev.hint + " is " + kind.words()
	}
	if kind.NeedsText() && !strings.Contains(want, "{{") && IsDynamic(want) {
		s.notice = "the text " + quote(want) + " looks like it changes from one run to the next: check that it exists, or that it contains a fixed part"
	}
	s.insertLocked(ev)
	s.seq++
	f := *s.frame
	f.ID, f.Notice = s.seq, s.notice
	s.notice = ""
	s.frame = &f
	return s.frame, nil
}

// LastShotJPEG returns the shrunk JPEG captured by the most recent engine action when it matches
// frameID and the current frame holds no secret. Returns nil when a fresh Capture is needed.
func (s *Session) LastShotJPEG(frameID uint64) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || !s.open || s.frame == nil || s.frame.ID != frameID || s.frame.Secret || len(s.lastShot) == 0 {
		return nil
	}
	return bytes.Clone(s.lastShot)
}

// Capture takes a screenshot of the screen. It refuses a screen that shows a secret: the pixels of a
// picture cannot be scrubbed.
func (s *Session) Capture(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, err
	}
	d, err := s.drv(ctx)
	if err != nil {
		return nil, err
	}
	t, err := d.Source(ctx)
	if err != nil {
		return nil, s.wrap(err)
	}
	secret := false
	t.Walk(func(n *mobile.Node) bool {
		if s.leaks(n) {
			secret = true
			return false
		}
		return true
	})
	if secret {
		return nil, ErrSecretOnScreen
	}
	png, err := d.Screenshot(ctx)
	if err != nil {
		return nil, s.wrap(err)
	}
	return png, nil
}

// UseFlow runs a flow on the device now, and records a use step when it completed. A flow that failed,
// that was refused, or that could not be run leaves the recording as it was.
func (s *Session) UseFlow(ctx context.Context, id string, with map[string]string) (*Frame, flow.RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, flow.RunResult{}, err
	}
	return s.useFlowLocked(ctx, id, with)
}

func (s *Session) useFlowLocked(ctx context.Context, id string, with map[string]string) (*Frame, flow.RunResult, error) {
	if err := s.syncedLocked(); err != nil {
		return nil, flow.RunResult{}, err
	}
	entry, err := flow.Load(s.o.Repo, id)
	if err != nil {
		return nil, flow.RunResult{}, s.wrap(err)
	}
	if err := s.checkUse(s.o.ID, s.o.Kind, id, with); err != nil {
		return nil, flow.RunResult{}, err
	}
	step := s.stepID("use-" + id)
	res, err := flow.RunWith(ctx, s.h.Engine, screenSource{s.o.Repo}, entry.Flow, with, s.scope(), flow.Options{
		By:        domain.ActorPilot,
		KeepState: true,
		Flows:     flow.Loader(s.o.Repo).Flow,
		Use:       func(name string) domain.UseCase { return s.use(step + "." + name) },
	})
	if err != nil {
		return nil, res, s.wrap(err)
	}
	if res.Status != flow.StatusCompleted {
		return nil, res, fail("the flow %s did not complete: %s", id, s.ov.Scrub(res.Error))
	}
	setup := true
	for _, e := range s.events[:s.at] {
		if !e.setup {
			setup = false
		}
	}
	s.insertLocked(&event{id: step, text: "Run the flow " + id, setup: setup, use: id, with: maps.Clone(with)})
	maps.Copy(s.vars, res.Captured)
	s.screen = ""
	f, err := s.refreshLocked(ctx)
	return f, res, err
}

// screenSource gives the screens files of the repository to a flow.
type screenSource struct{ repo string }

func (s screenSource) Load(app string) (domain.Screens, error) { return screens.Load(s.repo, app) }

// EditStep changes what the nth step says about itself: its ID, its sentence, its screen, what its element
// is called by, or a literal text that becomes a secret. It never changes a locator.
func (s *Session) EditStep(n int, e Edit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < 1 || n > len(s.events) {
		return fmt.Errorf("there is no step %d", n)
	}
	ev := s.events[n-1]
	if e.ID != "" {
		if err := checkName(e.ID); err != nil {
			return err
		}
		if slices.ContainsFunc(s.events, func(o *event) bool { return o != ev && o.id == e.ID }) {
			return fmt.Errorf("the step ID %s is used already", e.ID)
		}
		ev.id = e.ID
	}
	if e.Text != "" {
		if s.ov.Leaks(e.Text) {
			return ErrSecretOnScreen
		}
		ev.text = oneLine(e.Text)
	}
	if e.Screen != "" {
		if err := checkName(e.Screen); err != nil {
			return err
		}
		ev.screen = e.Screen
		ev.name, ev.containerName = "", ""
	}
	if e.Element != "" {
		if s.ov.Leaks(e.Element) {
			return ErrSecretOnScreen
		}
		ev.hint = oneLine(e.Element)
		ev.name = ""
	}
	if e.Common != nil {
		if !ev.element() {
			return errors.New("a step with no element has nothing to share")
		}
		if ev.shared != *e.Common {
			ev.shared = *e.Common
			ev.name, ev.containerName = "", ""
		}
	}
	if e.Secret != "" {
		if ev.do != domain.MobileType || ev.value == "" {
			return errors.New("only a step that types a text can turn it into a secret")
		}
		if err := CheckSecretEnv(e.Secret); err != nil {
			return err
		}
		old := ev.value
		s.ov.SetSecret(e.Secret, old)
		ev.value, ev.secret = "", "env:"+e.Secret
		ev.text = "Type the secret " + ev.secret + " in " + ev.hint
		s.wantClear = true
		s.eraseLocked(old)
	}
	return nil
}

// eraseLocked takes a value out of everything that the session holds as text. A check that expected it
// is dropped, because it would expect the redaction.
func (s *Session) eraseLocked(value string) {
	kept := s.events[:0]
	at := s.at
	for i, ev := range s.events {
		if ev.isAssert() && strings.Contains(ev.assertion.value, value) {
			if i < s.at {
				at--
			}
			continue
		}
		ev.text = strings.ReplaceAll(ev.text, value, Redacted)
		ev.hint = strings.ReplaceAll(ev.hint, value, Redacted)
		ev.value = strings.ReplaceAll(ev.value, value, Redacted)
		kept = append(kept, ev)
	}
	s.events, s.at = kept, at
	s.lastShot = nil
	if s.frame != nil && s.tree != nil {
		s.setFrame(s.tree)
	}
}

// Target changes what the session records next, after a save: a new flow or a new scenario. The steps are
// dropped, and the app is reset.
type Next struct {
	Kind   Kind
	ID     string
	Ticket string
	Title  string
	Use    string
	With   map[string]string
}

// ResetSession drops the steps, resets the app, and starts a new recording, which is the same as the one
// before unless next says other. The secrets stay: the next recording may need them to sign in again.
func (s *Session) ResetSession(ctx context.Context, next *Next) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return nil, errors.New("the session has not started")
	}
	if next != nil {
		o := s.o
		if next.Kind != "" {
			o.Kind = next.Kind
		}
		if next.ID != "" {
			o.ID = next.ID
		}
		o.Ticket, o.Title, o.Use, o.UseWith = next.Ticket, next.Title, next.Use, next.With
		o.Resume, o.ResumeAt = nil, 0
		if o.Kind == KindFlow {
			if err := flow.CheckID(o.ID); err != nil {
				return nil, err
			}
		}
		s.o = o
	}
	s.events, s.at, s.dirty, s.wantClear, s.screen, s.last, s.lastShot = nil, 0, false, false, "", nil, nil
	s.gap, s.gapAt = false, 0
	s.vars, s.newParams = map[string]string{}, nil
	if s.o.Resume == nil {
		s.params = map[string]string{}
	}
	if !s.open {
		if err := s.openLocked(ctx); err != nil {
			return nil, err
		}
	} else {
		s.newReplayLocked()
	}
	return s.beginLocked(ctx)
}

// Retarget changes what the recording is saved as, without touching the steps: a studio records first, and
// the person says at the end whether it is a flow or a scenario, and what it is called. A flow that uses a
// flow which uses it is refused.
func (s *Session) Retarget(next Next) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.o
	if next.Kind != "" {
		if next.Kind != KindFlow && next.Kind != KindScenario {
			return fmt.Errorf("a session records a flow or a scenario, not %q", next.Kind)
		}
		o.Kind = next.Kind
	}
	if next.ID != "" {
		o.ID = next.ID
	}
	o.Ticket, o.Title = next.Ticket, next.Title
	if o.Kind == KindFlow {
		if err := flow.CheckID(o.ID); err != nil {
			return err
		}
		for _, e := range s.events {
			if e.use != "" {
				if err := s.checkUse(o.ID, o.Kind, e.use, e.with); err != nil {
					return err
				}
			}
		}
	}
	s.o = o
	return nil
}

// ConfirmClear records that the person agreed to reset: clear on this device, which deletes the data of
// the app. A physical device needs it before a recording that signs in can be saved.
func (s *Session) ConfirmClear() {
	s.mu.Lock()
	s.o.ConfirmClear = true
	s.mu.Unlock()
}

// AppInspector reads the devices and the app in front, for a guided setup. engine/mobile/android has one.
type AppInspector = mobile.AppInspector
