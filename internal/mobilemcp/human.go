package mobilemcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/mobilerec"
)

// This file is what the studio asks of the Manager when a person drives the device: read the screen,
// take a picture of it, and act on it by hand. The lock order is Baton.mu, then Manager.mu, then
// Session.mu. The baton is never held here: a caller (the studio host) asks the baton first, and the
// Still callback of a request asks it again once the Manager lock is held, so that a hand-back that
// came in between stops the action.

// Modes of the Manager as the studio reports them.
const (
	ModeLive   = "live"
	ModeRecord = "record"
)

// humanLockWait is how long a read of the studio waits for the lock of the Manager while the agent runs a
// call. Past it the studio gets the last view it had.
const humanLockWait = 400 * time.Millisecond

// screenShotSide is the longest side of the picture that the studio shows.
const screenShotSide = 960

// humanActLockWait is how long a person's action waits for the lock of the Manager. A call of the agent
// that holds it for longer, such as a read of the device log, is cancelled by the baton when the person
// takes control; this bound keeps the person from hanging behind one that does not stop.
const humanActLockWait = 2 * time.Second

// ErrDeviceBusy is the answer of a person's action that could not take the device in time.
var ErrDeviceBusy = errors.New("device is busy, try again")

// ErrBusy is the answer of a read that could not take the Manager because the agent is running a call.
var ErrBusy = errors.New("the agent is still running a call: try again in a moment")

// ErrSecretScreen is the answer of a picture of a screen that shows a secret value.
var ErrSecretScreen = errors.New("the screen shows a secret value, so its picture is hidden")

// ErrHumanRefused is the answer to an action by hand that the server does not allow, as in observe mode.
type ErrHumanRefused struct{ Reason string }

func (e *ErrHumanRefused) Error() string { return e.Reason }

// LiveElement is an element of the screen as the studio lists it. The texts are scrubbed.
type LiveElement struct {
	Index     int    `json:"index"`
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	Desc      string `json:"desc,omitempty"`
	ID        string `json:"id,omitempty"`
	Hint      string `json:"hint,omitempty"`
	Left      int    `json:"left"`
	Top       int    `json:"top"`
	Right     int    `json:"right"`
	Bottom    int    `json:"bottom"`
	Clickable bool   `json:"clickable,omitempty"`
	Editable  bool   `json:"editable,omitempty"`
	Password  bool   `json:"password,omitempty"`
	Enabled   bool   `json:"enabled"`
	Checked   bool   `json:"checked,omitempty"`
	Focused   bool   `json:"focused,omitempty"`
}

// LiveWindow groups the elements of one window: app, keyboard, system, or overlay.
type LiveWindow struct {
	Kind     string        `json:"kind"`
	Package  string        `json:"package,omitempty"`
	Elements []LiveElement `json:"elements"`
}

// LiveStep is a recorded step, for the timeline.
type LiveStep struct {
	N       int    `json:"n"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Pending bool   `json:"pending,omitempty"`
}

// LiveView is the screen and the steps as the studio shows them.
type LiveView struct {
	// Mode is live or record.
	Mode          string       `json:"mode"`
	Width         int          `json:"width"`
	Height        int          `json:"height"`
	Secret        bool         `json:"secret,omitempty"`
	NoDevice      bool         `json:"noDevice,omitempty"`
	Busy          bool         `json:"busy,omitempty"`
	Observe       bool         `json:"observe,omitempty"`
	ObserveReason string       `json:"observeReason,omitempty"`
	Windows       []LiveWindow `json:"windows"`
	Steps         []LiveStep   `json:"steps"`
	// Gap and GapAt say that steps done by hand are not in the recording.
	Gap   bool `json:"gap,omitempty"`
	GapAt int  `json:"gapAt,omitempty"`
}

// lockBrief takes the lock of the Manager, or gives up after d when a call of the agent holds it.
func (m *Manager) lockBrief(ctx context.Context, d time.Duration) bool {
	if m.mu.TryLock() {
		return true
	}
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if m.mu.TryLock() {
				return true
			}
		case <-deadline.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
}

// statusBrief reads what the control tools report, waiting for the lock of the Manager for a moment only:
// a call of the agent that runs long must not hold the status of the baton. busy is true when it did.
func (m *Manager) statusBrief(ctx context.Context) (mode string, gap bool, at int, busy bool) {
	if !m.lockBrief(ctx, humanLockWait) {
		return "unknown", false, 0, true
	}
	mode, r := m.modeLocked(), m.rec
	m.mu.Unlock()
	if r != nil {
		gap, at = r.sess.Gap()
	}
	return mode, gap, at, false
}

// Mode says whether the tools act on the device at once (live) or through a recording (record).
func (m *Manager) Mode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.modeLocked()
}

func (m *Manager) modeLocked() string {
	if m.mode == modeRecord && m.rec != nil {
		return ModeRecord
	}
	return ModeLive
}

// ObserveReason is why a person's actions are refused, or "" when they are allowed.
func (m *Manager) ObserveReason() string {
	if err := m.guard.Allow(mcpkit.SurfaceMobile, true); err != nil {
		return m.scrub.Text(err.Error())
	}
	return ""
}

// Gap says whether steps that a person did by hand are missing from the recording, and where.
func (m *Manager) Gap() (bool, int) {
	m.mu.Lock()
	r := m.rec
	m.mu.Unlock()
	if r == nil {
		return false, 0
	}
	return r.sess.Gap()
}

// LiveSnapshot reads the screen and the steps for the studio. It makes no ref, so an agent that holds
// refs keeps them, and it never opens the device for the agent. refresh asks for a new reading of a
// recording (the person holds the control then, so a new frame number harms nobody). A call of the agent
// that holds the Manager makes it return busy: true with no screen, and the caller shows the last view.
func (m *Manager) LiveSnapshot(ctx context.Context, refresh bool) (LiveView, error) {
	v := LiveView{Windows: []LiveWindow{}, Steps: []LiveStep{}}
	v.ObserveReason = m.ObserveReason()
	v.Observe = v.ObserveReason != ""
	if !m.lockBrief(ctx, humanLockWait) {
		v.Busy = true
		v.Mode = ModeLive
		return v, nil
	}
	defer m.mu.Unlock()
	v.Mode = m.modeLocked()
	if v.Mode == ModeRecord {
		r := m.rec
		v.Steps = liveSteps(r.sess.Steps())
		v.Gap, v.GapAt = r.sess.Gap()
		if refresh {
			f, err := r.sess.Refresh(ctx)
			if err != nil {
				return v, err
			}
			r.frame = f
		}
		if f := r.frame; f != nil {
			v.Width, v.Height, v.Secret = f.Width, f.Height, f.Secret
			v.Windows = liveWindows(f.Elements, m.scrub.Text)
		}
		return v, nil
	}
	if m.dev == nil {
		if !refresh || m.open == nil {
			v.NoDevice = true
			return v, nil
		}
		if _, err := m.device(ctx); err != nil {
			return v, err
		}
	}
	t, err := m.dev.Source(ctx)
	if err != nil {
		return v, err
	}
	v.Width, v.Height = t.Width, t.Height
	v.Secret = m.treeLeaks(t)
	els, _ := mobilerec.ListElements(t, m.hints(ctx, m.dev, t))
	v.Windows = liveWindows(els, m.scrub.Text)
	return v, nil
}

func liveSteps(steps []mobilerec.Step) []LiveStep {
	out := make([]LiveStep, 0, len(steps))
	for _, s := range steps {
		out = append(out, LiveStep{N: s.N, ID: s.ID, Kind: s.Kind, Text: s.Text, Pending: s.Pending})
	}
	return out
}

// liveWindows groups elements by window, in the order that they come. clean scrubs a text.
func liveWindows(els []mobilerec.Element, clean func(string) string) []LiveWindow {
	out := []LiveWindow{}
	for _, e := range els {
		kind, pkg := nonEmpty(e.Window, "app"), e.Package
		if n := len(out); n == 0 || out[n-1].Kind != kind || out[n-1].Package != pkg {
			out = append(out, LiveWindow{Kind: kind, Package: pkg, Elements: []LiveElement{}})
		}
		role := e.Role
		if role == "" {
			role = e.Class
			if i := strings.LastIndex(role, "."); i >= 0 {
				role = role[i+1:]
			}
		}
		id := e.ID
		if i := strings.Index(id, ":id/"); i >= 0 {
			id = id[i+4:]
		}
		w := &out[len(out)-1]
		w.Elements = append(w.Elements, LiveElement{
			Index: e.Index, Kind: role, Text: truncateText(clean(e.Text), 160), Desc: truncateText(clean(collapse(e.Desc)), 160),
			ID: clean(id), Hint: truncateText(clean(e.Hint), 160),
			Left: e.Bounds.Left, Top: e.Bounds.Top, Right: e.Bounds.Right, Bottom: e.Bounds.Bottom,
			Clickable: e.Clickable, Editable: e.Editable, Password: e.Password, Enabled: e.Enabled, Checked: e.Checked, Focused: e.Focused,
		})
	}
	return out
}

// LiveScreen takes a picture of the screen as a JPEG. A screen that shows a secret is refused, because
// the pixels of a picture cannot be scrubbed.
func (m *Manager) LiveScreen(ctx context.Context) ([]byte, error) {
	if !m.lockBrief(ctx, 2*humanLockWait) {
		return nil, ErrBusy
	}
	defer m.mu.Unlock()
	if m.modeLocked() == ModeRecord {
		data, err := m.rec.sess.Capture(ctx)
		if err != nil {
			if errors.Is(err, mobilerec.ErrSecretOnScreen) {
				return nil, ErrSecretScreen
			}
			return nil, errors.New(m.scrub.Text(err.Error()))
		}
		return m.jpeg(data)
	}
	if m.dev == nil {
		return nil, errors.New("the device is not open yet")
	}
	t, err := m.dev.Source(ctx)
	if err != nil {
		return nil, errors.New(m.scrub.Text(err.Error()))
	}
	if m.treeLeaks(t) {
		return nil, ErrSecretScreen
	}
	data, err := m.dev.Screenshot(ctx)
	if err != nil {
		return nil, errors.New(m.scrub.Text(err.Error()))
	}
	return m.jpeg(data)
}

func (m *Manager) jpeg(png []byte) ([]byte, error) {
	shot, err := mobile.Render(png, mobile.ShotOptions{MaxSide: screenShotSide})
	if err != nil {
		return nil, err
	}
	return shot.Data, nil
}

// HumanActRequest is one thing that a person does by hand. Points are screen pixels.
type HumanActRequest struct {
	// Do is tap, doubleTap, longPress, swipe, type, or press.
	Do string
	// X and Y are the place of a tap, a long press, a double tap, and the start of a swipe. X2 and Y2 end a
	// swipe. Direction (up, down, left, right) is a swipe with no end.
	X, Y, X2, Y2 *float64
	Direction    string
	// Hold is the milliseconds of a long press. 0 is the default.
	Hold int
	// Text is typed in the field that has the focus. SecretEnv names a variable of the server whose value is
	// typed instead: it is never shown. Replace clears the field first, and Submit presses Enter after.
	Text      *string
	SecretEnv string
	Replace   bool
	Submit    bool
	// Key is the button of a press: BACK, HOME, ENTER, and so on.
	Key string
	// Still is asked with the Manager lock held, just before the device is touched. A non-nil error stops
	// the action. The studio host uses it to check that the person still holds the baton.
	Still func() error
}

// HumanResult says what a person's action did. Summary has no typed text in it: a text is "[typed N chars]".
type HumanResult struct {
	Summary  string
	Recorded bool
	Gap      bool
	GapAt    int
}

// HumanAct does what a person asks, on the device. In live mode it goes through the controller and is
// never recorded. In a recording with recordHuman it goes through the session, so it is a step. With
// recordHuman off it goes through Session.Unrecorded, which leaves a gap that a save refuses until it is
// closed. It is refused in observe mode.
func (m *Manager) HumanAct(ctx context.Context, req HumanActRequest, recordHuman bool) (HumanResult, error) {
	if err := m.guard.Allow(mcpkit.SurfaceMobile, true); err != nil {
		return HumanResult{}, &ErrHumanRefused{Reason: m.scrub.Text(err.Error())}
	}
	if req.Still != nil {
		if err := req.Still(); err != nil {
			return HumanResult{}, err
		}
	}
	if !m.lockBrief(ctx, humanActLockWait) {
		if err := ctx.Err(); err != nil {
			return HumanResult{}, err
		}
		return HumanResult{}, ErrDeviceBusy
	}
	defer m.mu.Unlock()
	if req.Still != nil {
		if err := req.Still(); err != nil {
			return HumanResult{}, err
		}
	}
	res, err := m.humanActLocked(ctx, req, recordHuman)
	if err != nil {
		return res, errors.New(m.scrub.Text(err.Error()))
	}
	return res, nil
}

func (m *Manager) humanActLocked(ctx context.Context, req HumanActRequest, recordHuman bool) (HumanResult, error) {
	text, secret, err := m.humanText(req)
	if err != nil {
		return HumanResult{}, err
	}
	if m.modeLocked() == ModeRecord {
		return m.humanRecordLocked(ctx, req, text, secret, recordHuman)
	}
	dev, err := m.device(ctx)
	if err != nil {
		return HumanResult{}, err
	}
	switch strings.ToLower(strings.TrimSpace(req.Do)) {
	case "tap", "doubletap", "longpress":
		p, err := humanPoint(req.X, req.Y)
		if err != nil {
			return HumanResult{}, err
		}
		g := mobile.Gesture{From: p, To: p}
		switch strings.ToLower(req.Do) {
		case "doubletap":
			g.Taps = 2
		case "longpress":
			hold := req.Hold
			if hold == 0 {
				hold = defaultLongPress
			}
			if hold < 1 || hold > maxHold {
				return HumanResult{}, fmt.Errorf("hold is 1 to %d milliseconds", maxHold)
			}
			g.Hold = time.Duration(hold) * time.Millisecond
		}
		if err := dev.Touch(ctx, g); err != nil {
			return HumanResult{}, err
		}
		return HumanResult{Summary: fmt.Sprintf("%s (%d,%d)", strings.ToLower(req.Do), p.X, p.Y)}, nil
	case "swipe":
		from, to, err := m.humanSwipe(ctx, dev, req)
		if err != nil {
			return HumanResult{}, err
		}
		if err := dev.Touch(ctx, mobile.Gesture{From: from, To: to, Move: swipeDuration}); err != nil {
			return HumanResult{}, err
		}
		return HumanResult{Summary: fmt.Sprintf("swipe (%d,%d) to (%d,%d)", from.X, from.Y, to.X, to.Y)}, nil
	case "type":
		if err := dev.TypeFocused(ctx, text, req.Replace); err != nil {
			return HumanResult{}, err
		}
		if req.Submit {
			code, _ := android.ButtonKeycode("ENTER")
			if err := dev.Key(ctx, code); err != nil {
				return HumanResult{}, fmt.Errorf("the text was typed, but Enter failed: %w", err)
			}
		}
		return HumanResult{Summary: typedSummary(text, secret)}, nil
	case "press":
		code, ok := android.ButtonKeycode(req.Key)
		if !ok {
			return HumanResult{}, fmt.Errorf("the button %q is not known", truncateText(req.Key, 30))
		}
		if err := dev.Key(ctx, code); err != nil {
			return HumanResult{}, err
		}
		return HumanResult{Summary: "press " + strings.ToUpper(strings.TrimSpace(req.Key))}, nil
	}
	return HumanResult{}, fmt.Errorf("%q is not something a person can do here: use tap, doubleTap, longPress, swipe, type, or press", truncateText(req.Do, 20))
}

// typedSummary is what the feed says about typed text: how many characters, and never the text.
func typedSummary(text, secret string) string {
	s := fmt.Sprintf("[typed %d chars]", len([]rune(text)))
	if secret != "" {
		s += " from " + secret
	}
	return s
}

// humanText reads the text of a type, and the secret that stands for it. A secret is looked up in the
// environment of the server, and taught to the scrubber so that it is in no result.
func (m *Manager) humanText(req HumanActRequest) (text, secret string, err error) {
	if strings.ToLower(strings.TrimSpace(req.Do)) != "type" {
		return "", "", nil
	}
	switch {
	case req.Text != nil && req.SecretEnv != "":
		return "", "", errors.New("give text or secretEnv, not both")
	case req.SecretEnv != "":
		if err := mobilerec.CheckSecretEnv(req.SecretEnv); err != nil {
			return "", "", err
		}
		v, ok := m.env(req.SecretEnv)
		if !ok || v == "" {
			return "", "", fmt.Errorf("the environment variable %s is not set", req.SecretEnv)
		}
		m.scrub.Add(v)
		return v, req.SecretEnv, nil
	case req.Text != nil:
		return *req.Text, "", nil
	}
	return "", "", errors.New("give the text to type, or secretEnv")
}

func humanPoint(x, y *float64) (mobile.Point, error) {
	if x == nil || y == nil {
		return mobile.Point{}, errors.New("give x and y")
	}
	return coordinate(*x, *y)
}

func (m *Manager) humanSwipe(ctx context.Context, dev Device, req HumanActRequest) (from, to mobile.Point, err error) {
	if req.X2 != nil || req.Y2 != nil {
		if from, err = humanPoint(req.X, req.Y); err != nil {
			return from, to, err
		}
		if to, err = humanPoint(req.X2, req.Y2); err != nil {
			return from, to, err
		}
		if from == to {
			return from, to, errors.New("the swipe starts and ends at the same place")
		}
		return from, to, nil
	}
	w, h, err := m.screenDims(ctx, dev)
	if err != nil {
		return from, to, err
	}
	return SwipePath(w, h, req.Direction, req.X, req.Y, 0)
}

// humanDirection is the direction of a swipe from its two ends: the longer axis wins.
func humanDirection(req HumanActRequest) (string, error) {
	if req.X2 == nil || req.Y2 == nil || req.X == nil || req.Y == nil {
		d := strings.ToLower(strings.TrimSpace(req.Direction))
		switch d {
		case "up", "down", "left", "right":
			return d, nil
		}
		return "", fmt.Errorf("the direction %q is not up, down, left, or right", truncateText(req.Direction, 20))
	}
	dx, dy := *req.X2-*req.X, *req.Y2-*req.Y
	if dx == 0 && dy == 0 {
		return "", errors.New("the swipe starts and ends at the same place")
	}
	if abs(dx) >= abs(dy) {
		if dx > 0 {
			return "right", nil
		}
		return "left", nil
	}
	if dy > 0 {
		return "down", nil
	}
	return "up", nil
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// humanRecordLocked acts through the recording session. With recordHuman the action is a step of the
// recording, and with it off the session does it and leaves a gap.
func (m *Manager) humanRecordLocked(ctx context.Context, req HumanActRequest, text, secret string, recordHuman bool) (HumanResult, error) {
	r := m.rec
	var act mobilerec.Act
	summary := ""
	switch strings.ToLower(strings.TrimSpace(req.Do)) {
	case "tap", "doubletap", "longpress":
		t, err := m.recTarget("", req.X, req.Y)
		if err != nil {
			return HumanResult{}, err
		}
		do := map[string]string{"tap": mobilerec.DoTap, "doubletap": mobilerec.DoDoubleTap, "longpress": mobilerec.DoLongPress}[strings.ToLower(req.Do)]
		act = mobilerec.Act{Do: do, Target: t}
		summary = fmt.Sprintf("%s (%d,%d)", strings.ToLower(req.Do), int(*req.X+0.5), int(*req.Y+0.5))
	case "swipe":
		dir, err := humanDirection(req)
		if err != nil {
			return HumanResult{}, err
		}
		act = mobilerec.Act{Do: mobilerec.DoSwipe, Value: dir}
		summary = "swipe " + dir
	case "type":
		t, err := m.focusedField()
		if err != nil {
			return HumanResult{}, err
		}
		act = mobilerec.Act{Do: mobilerec.DoType, Target: t, Value: text, Secret: secret}
		summary = typedSummary(text, secret)
	case "press":
		key, ok := recordKeys[strings.ToUpper(strings.TrimSpace(req.Key))]
		if !ok {
			return HumanResult{}, fmt.Errorf("the button %q cannot be recorded: a recording takes BACK, HOME, ENTER, SEARCH, TAB, and DEL. Turn off recording of your actions to press another", truncateText(req.Key, 30))
		}
		act = mobilerec.Act{Do: mobilerec.DoPress, Value: key}
		summary = "press " + strings.ToUpper(strings.TrimSpace(req.Key))
	default:
		return HumanResult{}, fmt.Errorf("%q is not something a person can do here: use tap, doubleTap, longPress, swipe, type, or press", truncateText(req.Do, 20))
	}
	if secret != "" {
		// The value comes from the environment of the server through the layer of the session, as for the agent.
		v, ok := r.sess.Overlay().Lookup(secret)
		if !ok || v == "" {
			return HumanResult{}, fmt.Errorf("the environment variable %s is not set", secret)
		}
		act.Value = v
	}
	act.Frame = r.frame.ID
	var (
		f   *mobilerec.Frame
		err error
	)
	if recordHuman {
		f, err = r.sess.Act(ctx, act)
	} else {
		f, err = r.sess.Unrecorded(ctx, act)
	}
	res := HumanResult{Summary: summary, Recorded: recordHuman && err == nil}
	res.Gap, res.GapAt = r.sess.Gap()
	if err != nil {
		var se *mobilerec.StaleError
		if errors.As(err, &se) && se.Frame != nil {
			r.frame = se.Frame
			return res, errors.New("the screen changed, so nothing was done: look at it again")
		}
		return res, m.hint(err)
	}
	if f != nil {
		r.frame = f
	}
	if recordHuman {
		r.unsaved = true
	}
	return res, nil
}

// OnHandBack is what runs when control returns to the agent: the refs of the last listing are dropped,
// because the screen may have changed under them, and a recording reads the screen again, so that its
// frame is the one that the agent sees next. It is safe to call with no recording and no device.
func (m *Manager) OnHandBack(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refs.Reset("", nil)
	if m.modeLocked() != ModeRecord {
		return nil
	}
	f, err := m.rec.sess.Refresh(ctx)
	if err != nil {
		return err
	}
	m.rec.frame = f
	return nil
}

// FreshListing reads the screen and writes it as mobile_list_elements_on_screen does, so that an agent that
// gets control back sees what a person left. The refs of the listing are valid. It is an error text, never
// a failure of the caller, when the device cannot be read.
func (m *Manager) FreshListing(ctx context.Context) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.modeLocked() == ModeRecord {
		text, err := m.recFrameText(ctx)
		if err != nil {
			return "the screen could not be listed: " + m.scrub.Text(err.Error())
		}
		return m.scrub.Text(text)
	}
	dev, err := m.device(ctx)
	if err != nil {
		return "the screen could not be listed: " + m.scrub.Text(err.Error())
	}
	l, err := m.list(ctx, dev)
	if err != nil {
		return "the screen could not be listed: " + m.scrub.Text(err.Error())
	}
	return l.text(m)
}
