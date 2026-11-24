// Package mobile is the engine that runs the steps of an app on a device. It knows no platform: a
// Platform starts what it needs and gives a Driver, which reads the native UI tree, sends a touch or a
// key, and takes a screenshot. Android is the platform of internal/engine/mobile/android, and the
// package imports neither adb nor net/http, so another platform can plug in without a change here.
package mobile

import (
	"context"
	"errors"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

// Platform starts a device session on first use and ends it when the run ends.
type Platform interface {
	Surface() domain.Surface
	// Driver gives the driver of the app of a service, and starts the platform on the first call. It
	// returns *Unavailable when the platform cannot run here and a person must fix something.
	Driver(ctx context.Context, service string) (Driver, error)
	// Close ends everything that Driver started. It is safe to call twice and after a failed start.
	Close(ctx context.Context) error
}

// Recoverer is a Platform whose device server can be started again, once a run found it dead or hung.
// The engine asks Healthy when an action failed, and calls Recover only when it says no. Recover drops
// the drivers it handed out, so the engine asks Driver again afterwards. A platform that cannot
// restart its server, such as one that uses a server of somebody else, returns an error from Recover.
type Recoverer interface {
	Healthy(ctx context.Context) bool
	Recover(ctx context.Context) error
}

// Crash is one crash or ANR (app not responding) that the device recorded for an app. Time is on the
// clock of the device, read as UTC.
type Crash struct {
	ID      string
	Process string
	// Summary is the line that says what happened, such as the exception.
	Summary string
	// Body is the whole report.
	Body string
	Time time.Time
}

// Diagnoser is a Platform that can say why an app stopped. The engine asks it after a step fails, and
// what it gives is evidence: it never changes a verdict. A platform that cannot, such as a device behind
// a server of somebody else, returns an error and the engine goes on without it.
type Diagnoser interface {
	// Crashes lists the crashes of the app since a time of the host, the oldest first, with their
	// reports.
	Crashes(ctx context.Context, pkg string, since time.Time) ([]Crash, error)
	// LogTail gives the last lines of the log of the app. The engine calls it only when the
	// configuration asks.
	LogTail(ctx context.Context, pkg string, lines int) (string, error)
}

// Foregrounder is a Platform that can say which app is in front.
type Foregrounder interface {
	// Foreground gives the package of the app in front, or "" when it is the launcher or the system.
	Foreground(ctx context.Context) (pkg string, err error)
}

// Driver is one app on one device.
type Driver interface {
	// Reset puts the app in the state that the scenario starts from: it is stopped, and with the
	// reset mode clear it has no data.
	Reset(ctx context.Context) error
	// Launch starts the app.
	Launch(ctx context.Context) error
	// Open opens a link with the app.
	Open(ctx context.Context, link string) error
	// Source reads the native UI tree. The tree is finished: see Tree.Finish.
	Source(ctx context.Context) (*Tree, error)
	Touch(ctx context.Context, g Gesture) error
	// Type replaces the text of the focused field.
	Type(ctx context.Context, text string) error
	Key(ctx context.Context, k Key) error
	// Screenshot gives a PNG. A screen that the app protects gives ErrSecure.
	Screenshot(ctx context.Context) ([]byte, error)
	About() About
}

// ResetWither is a Driver that can reset its app with a mode other than the one of its service. A launch
// that names a reset uses it: restart, clear, or none.
type ResetWither interface {
	ResetWith(ctx context.Context, mode string) error
}

// ClipboardReader is a Driver that can read the clipboard of the device. A recording holds the one session
// that the device allows, so it reads the clipboard through its driver and not through a second session.
type ClipboardReader interface {
	Clipboard(ctx context.Context) (string, error)
}

// ErrSecure is the error of a screenshot of a screen that the app does not allow to be captured.
var ErrSecure = errors.New("the screen is secure and cannot be captured")

// Point is a place on the screen, in pixels.
type Point struct{ X, Y int }

// Gesture is one touch. With From equal to To it is a tap, and with Hold it is a long press. With
// another To it is a swipe that takes Move. With another To and a Hold it is a drag: the finger stays
// down for Hold, so that the screen picks the element up, and then moves for Move.
//
// Taps is the number of taps of a tap: 0 or 1 is one tap, and 2 is a double tap. It counts only when
// From equals To and Hold is zero.
type Gesture struct {
	From, To Point
	Hold     time.Duration
	Move     time.Duration
	Taps     int
}

// Key is a key that a person can press.
type Key string

const (
	KeyBack         Key = "back"
	KeyHome         Key = "home"
	KeyEnter        Key = "enter"
	KeySearch       Key = "search"
	KeyTab          Key = "tab"
	KeyDelete       Key = "delete"
	KeyHideKeyboard Key = "hide-keyboard"
)

// About says which device and app a driver is for.
type About struct {
	Device  string
	Package string
	Version string
}

// Unavailable says that the platform cannot run here, for a reason that a person can fix: no adb, no
// device, a server that is missing. The scenario is blocked and not failed.
type Unavailable struct{ Reason string }

func (u *Unavailable) Error() string { return u.Reason }
