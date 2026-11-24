package mobilerec

import (
	"errors"
	"fmt"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
)

// Kind is what a session records.
type Kind string

const (
	// KindFlow records a reusable flow, .semar/flows/<id>.yaml.
	KindFlow Kind = "flow"
	// KindScenario records a scenario: its feature file, its script, and its screens.
	KindScenario Kind = "scenario"
)

// The operations of Act, besides the ones of domain.MobileOp. Assert has a method of its own.
const (
	DoTap       = string(domain.MobileTap)
	DoLongPress = string(domain.MobileLongTap)
	DoDoubleTap = string(domain.MobileDoubleTap)
	DoDrag      = string(domain.MobileDrag)
	DoType      = string(domain.MobileType)
	DoSwipe     = string(domain.MobileSwipe)
	DoScroll    = string(domain.MobileScroll)
	DoPress     = string(domain.MobilePress)
	DoOpen      = string(domain.MobileOpenLink)
	DoWait      = string(domain.MobileWait)
)

var (
	// ErrStaleFrame is the answer to an action on a frame that the screen has left. The error carries the
	// frame that the screen shows now.
	ErrStaleFrame = errors.New("the screen changed: act on the new frame")
	// ErrNoLocator is the answer to an element that no locator finds alone.
	ErrNoLocator = errors.New("no locator finds that element alone, so a replay could not find it: use `loc N` to see the locators, and pick one, e.g. `tap N.K`")
	// ErrNeedsSecret is the answer to text typed into a password field: it must be a secret.
	ErrNeedsSecret = errors.New("a password field takes a secret: type it with a secret name, so that the text is never saved")
	// ErrSecretOnScreen is the answer to a request that would save or show a screen that holds a secret.
	ErrSecretOnScreen = errors.New("the screen shows a secret value: hide it first, so that it is not saved")
	// ErrNoCheck is the answer to a scenario that checks nothing: a test that expects nothing proves nothing.
	ErrNoCheck = errors.New("a scenario needs at least one check: assert what must be true")
	// ErrNothingToSave is the answer to a recording with no step.
	ErrNothingToSave = errors.New("nothing was recorded")
	// ErrNeedsConfirm is the answer to a save that needs reset: clear on a physical device, which deletes
	// the data of the app, and that the person did not confirm.
	ErrNeedsConfirm = errors.New("replaying a sign-in needs reset: clear, which deletes the data of the app on this device: confirm it to save")
	// ErrNothingToUndo is the answer to an undo when no save can be undone.
	ErrNothingToUndo = errors.New("there is no save to undo")
	// ErrNotSynced is the answer to an action before the first step of a reopened recording: the device is
	// not at any step yet.
	ErrNotSynced = errors.New("the device is not at any step of the recording yet: use goto N first")
	// ErrNotAtStep is the answer to a repick when the device is not at the step before the one to change.
	ErrNotAtStep = errors.New("the device is not at the step before it")
)

// StaleError is ErrStaleFrame with the frame that the screen shows now.
type StaleError struct{ Frame *Frame }

func (e *StaleError) Error() string   { return ErrStaleFrame.Error() }
func (e *StaleError) Is(t error) bool { return t == ErrStaleFrame }

// Target says which element an action is on: an index of the elements of the frame, or a point of it.
type Target struct {
	Index   int
	X, Y    int
	ByPoint bool
}

// Act is one thing that a person does on the device.
type Act struct {
	// Do is tap, longPress, doubleTap, drag, type, swipe, scroll, press, open, or wait.
	Do string
	// Frame is the frame that the person saw. An action on another frame is stale.
	Frame uint64
	// Target is the element, for the operations that act on one. A swipe, a wait, and a scroll may have none
	// or one: a scroll needs one.
	Target *Target
	// To is the element that a drag drops on. A drag needs one, and no other operation takes one.
	To *Target
	// Value is the text of a type, the direction of a swipe or a scroll, the key of a press, the link of
	// an open, or the hold of a drag, such as 800ms.
	Value string
	// Secret names the environment variable that holds the text of a type. Value is then the text itself,
	// which the session keeps in memory for this session only and never writes.
	Secret string
	// Screen names the screen of the element. The default is the name of the screen that the frame shows.
	Screen string
	// Candidate chooses the locator of the element: 0 is the best one, and K is the Kth of Candidates. A
	// position (an index) is used only when it is chosen here.
	Candidate int
}

// AssertKind is how an assertion checks its element.
type AssertKind string

const (
	AssertEquals   AssertKind = "equals"
	AssertContains AssertKind = "contains"
	AssertExists   AssertKind = "exists"
	// AssertNotExists checks that nothing on the screen matches the element.
	AssertNotExists AssertKind = "not_exists"
	// AssertVisible, AssertHidden, AssertEnabled, AssertDisabled, AssertChecked, and AssertUnchecked check
	// the state of the element.
	AssertVisible   AssertKind = "visible"
	AssertHidden    AssertKind = "hidden"
	AssertEnabled   AssertKind = "enabled"
	AssertDisabled  AssertKind = "disabled"
	AssertChecked   AssertKind = "checked"
	AssertUnchecked AssertKind = "unchecked"
	// AssertCount checks how many elements match: the value is the number.
	AssertCount AssertKind = "count"
	// AssertAttribute checks an attribute of the element: Attribute names it, and the value is what it equals.
	AssertAttribute AssertKind = "attribute"
)

// NeedsText says whether the kind of an assertion checks a text, which defaults to what the element shows.
func (k AssertKind) NeedsText() bool { return k == AssertEquals || k == AssertContains }

// Known says whether k is a kind of assertion.
func (k AssertKind) Known() bool {
	switch k {
	case AssertEquals, AssertContains, AssertExists, AssertNotExists, AssertVisible, AssertHidden,
		AssertEnabled, AssertDisabled, AssertChecked, AssertUnchecked, AssertCount, AssertAttribute:
		return true
	}
	return false
}

// State is the element state that a kind checks, or "".
func (k AssertKind) State() string {
	switch k {
	case AssertVisible, AssertHidden, AssertEnabled, AssertDisabled, AssertChecked, AssertUnchecked:
		return string(k)
	}
	return ""
}

// Assert is one thing that must be true of the screen.
type Assert struct {
	Frame  uint64
	Target Target
	Kind   AssertKind
	// Value is what the text must be or hold. The default is the text that the element shows now.
	Value string
	// Attribute names the attribute that an attribute check reads.
	Attribute string
	Screen    string
	// Candidate chooses the locator of the element, as Act.Candidate does.
	Candidate int
}

// CaptureReq is a value to take from the text of an element, for the steps after it.
type CaptureReq struct {
	Frame  uint64
	Target Target
	// Candidate chooses the locator of the element, as Act.Candidate does.
	Candidate int
	// Name is the variable: later steps write {{Name}}.
	Name string
	// Pattern is an optional regular expression: the value is its first group, or the whole match.
	Pattern string
	Screen  string
}

// Edit changes what a step says about itself. It never changes a locator: an old step has no screen to
// check a new locator against.
type Edit struct {
	// ID renames the step.
	ID string
	// Text is the sentence of the scenario for the step.
	Text string
	// Screen renames the screen of the step.
	Screen string
	// Element renames what the element of the step is called by.
	Element string
	// Secret turns the literal text of a type into a secret that lives in the environment variable of that
	// name. The text is erased from the session, and Value is asked of nothing: it is the old text.
	Secret string
	// Common says that the element of the step, and its container, are shared: they go to the common
	// section of the screens file, which every screen can find. False takes the step back to its screen.
	Common *bool
}

// Element is one thing that the person can act on, as a frame shows it. Text and the attributes have no
// secret in them.
type Element struct {
	Index     int         `json:"index"`
	Class     string      `json:"class"`
	Role      string      `json:"role,omitempty"`
	ID        string      `json:"id,omitempty"`
	Desc      string      `json:"desc,omitempty"`
	Text      string      `json:"text,omitempty"`
	Hint      string      `json:"hint,omitempty"`
	Bounds    mobile.Rect `json:"bounds"`
	Clickable bool        `json:"clickable,omitempty"`
	Editable  bool        `json:"editable,omitempty"`
	Password  bool        `json:"password,omitempty"`
	Enabled   bool        `json:"enabled"`
	Checked   bool        `json:"checked,omitempty"`
	Focused   bool        `json:"focused,omitempty"`
	// Package is the app that draws the element, and Window is the kind of window that holds it: app,
	// keyboard, system, or overlay.
	Package   string          `json:"package,omitempty"`
	Window    string          `json:"window,omitempty"`
	Stability string          `json:"stability,omitempty"`
	Locator   *domain.Locator `json:"locator,omitempty"`
	// Warning says why the locator of the element needs a look: no locator finds it alone, or the best one
	// finds it inside another element.
	Warning string `json:"warning,omitempty"`
}

// Frame is the screen as the person sees it: one reading of the device, with a number that grows with
// every reading.
type Frame struct {
	ID       uint64    `json:"frameId"`
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	Screen   string    `json:"screen"`
	Elements []Element `json:"elements"`
	// Secret is true when the screen shows a value that the person typed as a secret, as when a password
	// is shown. The text of those elements is redacted, and the screenshot is not given.
	Secret bool `json:"secret,omitempty"`
	// Notice is a warning for the person about the last action.
	Notice string `json:"notice,omitempty"`
}

// Step is one recorded step, as the timeline shows it.
type Step struct {
	N      int    `json:"n"`
	ID     string `json:"id"`
	Text   string `json:"text"`
	Screen string `json:"screen,omitempty"`
	Kind   string `json:"kind"`
	// Pending is true for a step after the cursor: the device is not at it, and a new step goes before it.
	Pending bool `json:"pending,omitempty"`
	// Locator is how the element of the step is found, with no secret in it. Stability is its rating.
	Locator   string `json:"locator,omitempty"`
	Stability string `json:"stability,omitempty"`
	// Shared is true when the element goes to the common section.
	Shared bool `json:"shared,omitempty"`
}

// event is a step as the session keeps it, with what the builder needs to name its element.
type event struct {
	id     string
	text   string
	setup  bool
	screen string
	hint   string

	do        domain.MobileOp
	locator   domain.Locator
	container *Container
	value     string
	secret    string // an env: reference
	reset     string
	link      string
	use       string
	with      map[string]string
	within    string // the element that a scroll moves, of a loaded step
	// to is the element that a drag drops on: it has its own hint, locator, and names. Its locators are in
	// locs too, under the names that the step sends.
	to *event
	// service and app are the ones that a step of a loaded flow names for itself. A flow that was recorded
	// has none: it has them once, in its head.
	service, app string

	// purpose says what the locator is for: it decides which candidates a repick accepts.
	purpose Purpose
	// cand is the candidate that the step uses: its tier, its rating, and its note.
	cand Candidate
	// alts are the other candidates that were confirmed when the step was recorded or repicked.
	alts []Candidate
	// locs and elem are exactly what the call sent: the element, and the locators that it names.
	locs map[string]domain.Locator
	elem string
	// name and containerName are the names of the element and of its container in the screens file, kept
	// for a step that was loaded from files and not changed. A change clears them.
	name          string
	containerName string
	// shared sends the element and its container to the common section.
	shared bool

	// step carries what a loaded scenario step says besides its text, and captures what its script step
	// captures. flowExpect is what a loaded step of a flow checks. They are written back as they were.
	step       *domain.Step
	captures   []domain.Capture
	flowExpect []domain.Expectation
	// after is the step event that a loaded check belonged to.
	after *event

	// assertion is set for a check, which is no step on the device.
	assertion *assertion
	// capture is set for a capture of an element's text: it does nothing on the device. The build puts it on
	// the step before it, or on a wait step of its own.
	capture *captureSpec
}

type captureSpec struct {
	name    string
	pattern string
}

type assertion struct {
	kind  AssertKind
	value string
	attr  string
	// expect is the expectation of a loaded check, kept as it was: its source, its by, and its cite.
	expect *domain.Expectation
}

func (e *event) isAssert() bool { return e.assertion != nil }

// isRead is a check or a capture: it reads the screen and does nothing to the device.
func (e *event) isRead() bool { return e.assertion != nil || e.capture != nil }

func (e *event) kind() string {
	switch {
	case e.isAssert():
		return "assert"
	case e.capture != nil:
		return "capture"
	case e.use != "":
		return "use"
	}
	return string(e.do)
}

func (e *event) element() bool { return !e.locator.Empty() }

// ActError is an action that did not go through. The text has no secret in it.
type ActError struct{ Msg string }

func (e *ActError) Error() string { return e.Msg }

// ErrCaptureHoldsText says that every locator of an element holds the text that a capture reads, so a
// saved file would hold the value. It is an *ActError, and errors.Is finds it by the pointer.
var ErrCaptureHoldsText error = &ActError{Msg: "every locator of that element holds the text that the capture reads, so a file would hold the value: pick another element, or one of its locators with N.K"}

func fail(format string, args ...any) error { return &ActError{Msg: fmt.Sprintf(format, args...)} }

// words is how a state reads in a sentence: the element is checked, is not checked.
func (k AssertKind) words() string {
	if k == AssertUnchecked {
		return "not checked"
	}
	return string(k)
}

// choose makes c the locator of the step, with the other candidates that were confirmed. The step then
// sends the locator under the probe names, and the names of the screens file are made again at the build.
func (e *event) choose(c Candidate, alts []Candidate) {
	e.cand, e.alts = c, alts
	e.locator, e.container = c.Locator, c.Container
	e.locs = map[string]domain.Locator{probeName: c.Locator}
	if c.Container != nil {
		e.locs[probeInner] = c.Container.Locator
	}
	e.elem = probeName
	e.name, e.containerName = "", ""
	e.syncDest()
}

// chooseTo makes c the locator of the element that a drag drops on, and puts it in the locators that the
// step sends, next to the ones of the element that it drags.
func (e *event) chooseTo(hint string, c Candidate, alts []Candidate) {
	to := &event{screen: e.screen, hint: hint, purpose: ForAssert, shared: e.shared}
	to.cand, to.alts = c, alts
	to.locator, to.container = c.Locator, c.Container
	e.to = to
	e.syncDest()
}

// syncDest adds the locators of the drop element to locs. A drop element that was recorded is sent under
// the probe names, and one that was loaded keeps the names of its file.
func (e *event) syncDest() {
	to := e.to
	if to == nil || to.locator.Empty() {
		return
	}
	if e.locs == nil {
		e.locs = map[string]domain.Locator{}
	}
	if to.name == "" {
		to.elem = probeTo
	}
	loc := to.locator
	if to.container != nil {
		inner := to.containerName
		if inner == "" {
			inner = probeToInner
		}
		loc.Within = inner
		e.locs[inner] = to.container.Locator
	}
	e.locs[to.elem] = loc
}
