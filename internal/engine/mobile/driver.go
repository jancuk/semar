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

// ErrSecure is the error of a screenshot of a screen that the app does not allow to be captured.
var ErrSecure = errors.New("the screen is secure and cannot be captured")

// Point is a place on the screen, in pixels.
type Point struct{ X, Y int }

// Gesture is one touch. With From equal to To it is a tap, and with Hold it is a long press. With
// another To it is a swipe that takes Move.
type Gesture struct {
	From, To Point
	Hold     time.Duration
	Move     time.Duration
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
