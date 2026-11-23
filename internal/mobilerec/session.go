package mobilerec

import (
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
	// ConfirmClear says that the person agreed to reset: clear on a physical device.
	ConfirmClear bool
	// Settle bounds the wait for a still screen. The default is 3 seconds.
	Settle time.Duration
	Clock  func() time.Time
	Sleep  func(ctx context.Context, d time.Duration) error
	Now    func() time.Time
}

// Session records one flow or one scenario. It is safe for use by one goroutine at a time: every call holds
// the lock of the session, so that a studio that serves two requests at once still gives the device one
// action at a time.
type Session struct {
	o  Options
	ov *Overlay

	mu      sync.Mutex
	h       Handle
	driver  mobile.Driver
	device  mobile.About
	open    bool
	started bool

	seq    uint64
	frame  *Frame
	tree   *mobile.Tree
	nodes  []*mobile.Node
	screen string // the name that the person gave the screen of the frame, or ""

	events    []*event
	wantClear bool
	last      *Saved
	notice    string
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
		if o.Use != "" {
			return nil, errors.New("a flow does not use another flow")
		}
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
	return &Session{o: o, ov: ov}, nil
}

// Overlay gives the layer of secrets and pins of the session, for a verify that must see the same.
func (s *Session) Overlay() *Overlay { return s.ov }

// Kind and ID say what the session records.
func (s *Session) Kind() Kind { return s.o.Kind }
func (s *Session) ID() string { return s.o.ID }

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
	s.h, s.driver, s.device, s.open = h, d, d.About(), true
	if s.device.Device != "" {
		s.ov.Pin(SerialEnv, s.device.Device)
	}
	h.Engine.NewScenario()
	return nil
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
	if !s.open {
		return nil
	}
	s.open = false
	s.driver = nil
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
	return s.launchLocked(ctx, false)
}

// Close gives the device away. The overlay is kept, because a verify may follow: Wipe drops it.
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked(ctx)
}

// Wipe drops the secrets and the pins of the session.
func (s *Session) Wipe() { s.ov.Clear() }

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

func (s *Session) settle(ctx context.Context) (*mobile.Tree, error) {
	return mobile.Settle(ctx, s.driver, s.o.Settle, s.o.Clock, s.o.Sleep)
}

func (s *Session) refreshLocked(ctx context.Context) (*Frame, error) {
	t, err := s.settle(ctx)
	if err != nil {
		return nil, s.wrap(fmt.Errorf("the screen could not be read: %w", err))
	}
	return s.setFrame(t), nil
}

// SetScreen names the screen that the frame shows. The name holds until an action leaves the screen.
func (s *Session) SetScreen(name string) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkName(name); err != nil {
		return nil, err
	}
	s.screen = name
	if s.tree == nil {
		return nil, errors.New("there is no screen yet")
	}
	return s.setFrame(s.tree), nil
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

// screenName is the name of the screen of a tree: the one that the person gave, else the heading of the
// screen, else Screen1.
func (s *Session) screenName(t *mobile.Tree) string {
	if s.screen != "" {
		return s.screen
	}
	if h := record.Slug(ScreenHeading(t)); h != "" {
		return titleName(h)
	}
	return "Screen1"
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

// setFrame turns a tree into the next frame. The tree is kept as it is, with its text, for the matching
// of the next action. The frame holds no secret value: the text of a node that shows one is redacted.
func (s *Session) setFrame(t *mobile.Tree) *Frame {
	s.seq++
	f := &Frame{ID: s.seq, Width: t.Width, Height: t.Height, Screen: s.screenName(t), Notice: s.notice}
	s.notice = ""
	s.tree = t
	s.nodes = nil
	t.Walk(func(n *mobile.Node) bool {
		if n.Package == "com.android.systemui" && s.device.Package != "com.android.systemui" {
			return true
		}
		if !interesting(n) {
			return true
		}
		e := Element{
			Index: len(s.nodes), Class: n.Class, Role: n.Role, Bounds: n.Bounds,
			Clickable: Clickable(n), Editable: Editable(n), Password: n.Password, Enabled: n.Enabled, Checked: n.Checked,
		}
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
		} else if cands := Locate(t, targetFor(n, ForTap), ForTap, s.ov); len(cands) > 0 {
			loc := cands[0].Locator
			e.Locator, e.Stability = &loc, cands[0].Stability
		}
		f.Elements = append(f.Elements, e)
		s.nodes = append(s.nodes, n)
		return true
	})
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

// identity is what tells a node from the others when the screen is read again: its class, its ids, and
// its place. The text is no part of it, because a field changes its text when it is typed in.
func identity(n *mobile.Node) string {
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
	fresh, err := s.driver.Source(ctx)
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

// run sends one action through the engine, and gives back the error text of an action that did not go
// through. The text has no secret in it.
func (s *Session) run(ctx context.Context, step string, m domain.MobileAction, locs map[string]domain.Locator) error {
	m.Service, m.App = s.o.Service, s.o.App
	res, err := s.h.Engine.Do(ctx, mobile.Call{
		Use: s.use(step), By: domain.ActorPilot, Action: m, Locators: locs,
		Why: "recording " + s.recordID(),
	})
	if err != nil {
		return s.wrap(err)
	}
	switch a := res.Action; {
	case a.Refused != "":
		return fail("the action was refused: %s", s.ov.Scrub(a.Refused))
	case a.Err != "":
		return fail("the action failed: %s", s.ov.Scrub(a.Err))
	}
	return nil
}

// launchLocked resets the app and starts it. The reset of the launch is the one that the session was given.
func (s *Session) launchLocked(ctx context.Context, record bool) (*Frame, error) {
	m := domain.MobileAction{Do: domain.MobileLaunch, Reset: s.o.Reset}
	step := s.stepID("start")
	if err := s.run(ctx, step, m, nil); err != nil {
		return nil, err
	}
	if record {
		s.events = append(s.events, &event{id: step, text: "Start the app", setup: true, do: domain.MobileLaunch, reset: s.o.Reset})
	}
	return s.refreshLocked(ctx)
}

// Act does one thing on the device, as a person does, and records it when it went through. It acts on the
// element that the person saw in the frame, found again on the screen as it is now.
func (s *Session) Act(ctx context.Context, a Act) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, err
	}
	switch domain.MobileOp(a.Do) {
	case domain.MobileTap, domain.MobileLongTap:
		return s.onElement(ctx, a, ForTap)
	case domain.MobileType:
		return s.onElement(ctx, a, ForType)
	case domain.MobileScroll:
		return s.onElement(ctx, a, ForAssert)
	case domain.MobileSwipe:
		if a.Target != nil {
			return s.onElement(ctx, a, ForAssert)
		}
		return s.plain(ctx, a, "swipe-"+a.Value, "Swipe "+a.Value)
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
	return nil, fail("the operation %q is not one that a recording does: use tap, longPress, type, swipe, scroll, press, open, or wait", a.Do)
}

// plain acts with no element: a swipe of the screen, a key, a link, a wait.
func (s *Session) plain(ctx context.Context, a Act, id, text string) (*Frame, error) {
	if a.Frame != 0 && (s.frame == nil || a.Frame != s.frame.ID) {
		return nil, s.stale(ctx)
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
	if err := s.run(ctx, sid, m, nil); err != nil {
		return nil, err
	}
	ev := &event{id: sid, text: text, do: m.Do, value: m.Value, screen: m.Screen, link: m.Link}
	if m.Do == domain.MobileOpenLink {
		ev.text = "Open the link " + quote(a.Value)
	}
	s.events = append(s.events, ev)
	if ev.do != domain.MobileWait && ev.do != domain.MobileSwipe && !(ev.do == domain.MobilePress && (a.Value == string(mobile.KeyHideKeyboard) || a.Value == string(mobile.KeyDelete) || a.Value == string(mobile.KeyTab))) {
		s.screen = ""
	}
	return s.refreshLocked(ctx)
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
	cands := Locate(tree, target, p, s.ov)
	if len(cands) == 0 {
		return nil, ErrNoLocator
	}
	best := cands[0]
	screen := a.Screen
	if screen == "" {
		screen = s.screenName(tree)
	}
	ev := &event{
		screen: screen, hint: HintOf(target), do: domain.MobileOp(a.Do),
		locator: best.Locator, container: best.Container,
	}
	m := domain.MobileAction{Do: ev.do, Screen: screen, Element: probeName}
	locs := map[string]domain.Locator{probeName: best.Locator}
	if best.Container != nil {
		locs[probeInner] = best.Container.Locator
	}
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
			m.Value, ev.value = a.Value, a.Value
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
	default:
		ev.text = "Tap " + ev.hint
	}
	if ps := m.Problems(); len(ps) > 0 {
		return nil, fail("%s", ps[0])
	}
	ev.id = s.stepID(string(ev.do) + "-" + HintOf(target))
	if err := s.run(ctx, ev.id, m, locs); err != nil {
		return nil, err
	}
	s.events = append(s.events, ev)
	if ev.do != domain.MobileWait && ev.do != domain.MobileScroll && ev.do != domain.MobileType && ev.do != domain.MobileSwipe {
		s.screen = ""
	}
	return s.refreshLocked(ctx)
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
	if s.o.Kind != KindScenario {
		return nil, fail("a flow has no checks: record a scenario to assert")
	}
	if a.Screen != "" {
		if err := checkName(a.Screen); err != nil {
			return nil, err
		}
	}
	tree, picked, err := s.resolve(ctx, a.Frame, a.Target)
	if err != nil {
		return nil, err
	}
	if s.leaks(picked) {
		return nil, ErrSecretOnScreen
	}
	cands := Locate(tree, picked, ForAssert, s.ov)
	if len(cands) == 0 {
		return nil, ErrNoLocator
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
	screen := a.Screen
	if screen == "" {
		screen = s.screenName(tree)
	}
	ev := &event{
		id: s.stepID("check-" + HintOf(picked)), screen: screen, hint: HintOf(picked),
		locator: cands[0].Locator, container: cands[0].Container, assertion: &assertion{kind: kind, value: want, attr: attr},
	}
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
	if kind.NeedsText() && IsDynamic(want) {
		s.notice = "the text " + quote(want) + " looks like it changes from one run to the next: check that it exists, or that it contains a fixed part"
	}
	s.events = append(s.events, ev)
	s.seq++
	f := *s.frame
	f.ID, f.Notice = s.seq, s.notice
	s.notice = ""
	s.frame = &f
	return s.frame, nil
}

// Capture takes a screenshot of the screen. It refuses a screen that shows a secret: the pixels of a
// picture cannot be scrubbed.
func (s *Session) Capture(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, err
	}
	t, err := s.driver.Source(ctx)
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
	png, err := s.driver.Screenshot(ctx)
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
	if s.o.Kind == KindFlow {
		return nil, flow.RunResult{}, fail("a flow does not use another flow")
	}
	entry, err := flow.Load(s.o.Repo, id)
	if err != nil {
		return nil, flow.RunResult{}, s.wrap(err)
	}
	vars := map[string]string{"run.id": "recording"}
	step := s.stepID("use-" + id)
	res, err := flow.RunWith(ctx, s.h.Engine, screenSource{s.o.Repo}, entry.Flow, with, vars, flow.Options{
		By:        domain.ActorPilot,
		KeepState: true,
		Use:       func(name string) domain.UseCase { return s.use(step + "." + name) },
	})
	if err != nil {
		return nil, res, s.wrap(err)
	}
	if res.Status != flow.StatusCompleted {
		return nil, res, fail("the flow %s did not complete: %s", id, s.ov.Scrub(res.Error))
	}
	setup := true
	for _, e := range s.events {
		if !e.setup {
			setup = false
		}
	}
	s.events = append(s.events, &event{id: step, text: "Run the flow " + id, setup: setup, use: id, with: maps.Clone(with)})
	s.screen = ""
	f, err := s.refreshLocked(ctx)
	return f, res, err
}

// screenSource gives the screens files of the repository to a flow.
type screenSource struct{ repo string }

func (s screenSource) Load(app string) (domain.Screens, error) { return screens.Load(s.repo, app) }

// Steps lists the recorded steps, for the timeline. The text has no secret in it.
func (s *Session) Steps() []Step {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Step, len(s.events))
	for i, e := range s.events {
		out[i] = Step{N: i + 1, ID: e.id, Text: s.ov.Scrub(e.text), Screen: e.screen, Kind: e.kind()}
	}
	return out
}

// DeleteLastStep takes the last step off the recording. It is the only deletion: a step in the middle
// leaves a file that does not replay, because the device is where the later steps left it.
func (s *Session) DeleteLastStep() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return errors.New("there is no step to delete")
	}
	s.events = s.events[:len(s.events)-1]
	return nil
}

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
	}
	if e.Element != "" {
		if s.ov.Leaks(e.Element) {
			return ErrSecretOnScreen
		}
		ev.hint = oneLine(e.Element)
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
	for _, ev := range s.events {
		if ev.isAssert() && strings.Contains(ev.assertion.value, value) {
			continue
		}
		ev.text = strings.ReplaceAll(ev.text, value, Redacted)
		ev.hint = strings.ReplaceAll(ev.hint, value, Redacted)
		ev.value = strings.ReplaceAll(ev.value, value, Redacted)
		kept = append(kept, ev)
	}
	s.events = kept
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
		if o.Kind == KindFlow {
			if err := flow.CheckID(o.ID); err != nil {
				return nil, err
			}
			if o.Use != "" {
				return nil, errors.New("a flow does not use another flow")
			}
		}
		s.o = o
	}
	s.events, s.wantClear, s.screen, s.last = nil, false, "", nil
	if !s.open {
		if err := s.openLocked(ctx); err != nil {
			return nil, err
		}
	} else {
		s.h.Engine.NewScenario()
	}
	return s.beginLocked(ctx)
}

// Retarget changes what the recording is saved as, without touching the steps: a studio records first, and
// the person says at the end whether it is a flow or a scenario, and what it is called. A scenario starts
// with a flow only through Start or ResetSession, so a flow that holds a use step is refused when it is built.
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
		if slices.ContainsFunc(s.events, func(e *event) bool { return e.use != "" }) {
			return errors.New("a flow does not use another flow: record a scenario, or delete the step that runs a flow")
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
