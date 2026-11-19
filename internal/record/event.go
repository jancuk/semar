// Package record turns what a person did in a Chrome window into the files of a web test: the
// screens, the scenario, and the script. The window itself is in internal/engine/web. This package
// takes the events that the window sent and does no I/O but the writing of the three files.
package record

import "github.com/jancuk/semar/internal/domain"

// Kind is what a person did.
type Kind string

const (
	KindOpen  Kind = "open"
	KindClick Kind = "click"
	KindFill  Kind = "fill"
	KindPress Kind = "press"
	KindDrag  Kind = "drag"
	KindCheck Kind = "check"
	// KindSelect is the choice of an option in a list, and Value is its label. KindTick and KindUntick
	// are a checkbox that a person checked or cleared. KindHover is a pointer held over an element.
	KindSelect Kind = "select"
	KindTick   Kind = "tick"
	KindUntick Kind = "untick"
	KindHover  Kind = "hover"
)

// Page is the page that a person was on when the event happened.
type Page struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// Element is an element as the recorder chose to find it. Hint is what the element is called by:
// its test ID, its name, or its text.
type Element struct {
	Locator domain.Locator `json:"locator"`
	Hint    string         `json:"hint"`
}

// Event is one thing that a person did, in the order that they did it. Value is the text of a fill,
// the key of a press, and the text that a check expects. A fill of a password has Secret set and no
// Value: the recorder never reads a password.
type Event struct {
	Kind   Kind     `json:"kind"`
	Page   Page     `json:"page"`
	Target *Element `json:"target,omitempty"`
	To     *Element `json:"to,omitempty"`
	Value  string   `json:"value,omitempty"`
	Secret bool     `json:"secret,omitempty"`
	// Probe is set on a check that the explorer of a plan decided itself, on the page as it was then.
	Probe *Probe `json:"-"`
}

// Probe is what the explorer found when it decided a check of a form that reads more than the text of an
// element: whether the check held, and the words for what the page showed.
type Probe struct {
	Held  bool
	Shown string
}
