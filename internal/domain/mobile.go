package domain

import (
	"fmt"
	"slices"
	"strings"
)

// MobileOp is what a mobile action does on a device.
type MobileOp string

const (
	// MobileLaunch starts the app of the service, after the reset of the scenario.
	MobileLaunch MobileOp = "launch"
	// MobileOpenLink opens a link with the app, as a deep link.
	MobileOpenLink MobileOp = "open"
	MobileTap      MobileOp = "tap"
	MobileLongTap  MobileOp = "longPress"
	// MobileType replaces the text of an input field with the Value, or with the Secret.
	MobileType MobileOp = "type"
	// MobileSwipe moves a finger in the direction that the Value names.
	MobileSwipe MobileOp = "swipe"
	// MobileScroll swipes until the element is on the screen.
	MobileScroll MobileOp = "scroll"
	// MobilePress sends one key.
	MobilePress MobileOp = "press"
	MobileWait  MobileOp = "wait"
)

var mobileOps = []MobileOp{MobileLaunch, MobileOpenLink, MobileTap, MobileLongTap, MobileType, MobileSwipe, MobileScroll, MobilePress, MobileWait}

// MobileDirections are the values of a swipe and of a scroll: where the finger goes.
var MobileDirections = []string{"up", "down", "left", "right"}

// MobileKeys are the values of a press.
var MobileKeys = []string{"back", "home", "enter", "search", "tab", "delete", "hide-keyboard"}

// MobileAction is the one device action of a step. Link and Value may hold {{variables}}. Service and
// App are set from the leg of a journey, or written in the script of a mobile scenario.
type MobileAction struct {
	Service string   `yaml:"service,omitempty"`
	App     string   `yaml:"app,omitempty"`
	Do      MobileOp `yaml:"do"`
	// Link is what open opens: an http or https address, or one of a scheme that the service lists.
	Link    string `yaml:"link,omitempty"`
	Screen  string `yaml:"screen,omitempty"`
	Element string `yaml:"element,omitempty"`
	// Within names the element of the same screen that a scroll moves.
	Within string `yaml:"within,omitempty"`
	Value  string `yaml:"value,omitempty"`
	// Secret is a secret reference such as env:SHOP_PASSWORD. Only type takes one.
	Secret string `yaml:"secret,omitempty"`
	// Reset is how a launch resets the app before it starts, in place of the reset of the service:
	// restart, clear, or none. Only a launch takes one. A scenario that must start from a clean install
	// and one that must start signed in can live in the same repository.
	Reset string `yaml:"reset,omitempty"`
}

// MobileResets are the values of the reset of a launch.
var MobileResets = []string{"restart", "clear", "none"}

// Writes reports whether the operation can change what the service holds. A tap may submit a form.
func (o MobileOp) Writes() bool {
	switch o {
	case MobileTap, MobileLongTap, MobileType, MobileSwipe, MobilePress:
		return true
	}
	return false
}

// Elements lists the screen elements that the action acts on, in order.
func (m MobileAction) Elements() []string {
	var out []string
	for _, e := range []string{m.Element, m.Within} {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// Problems lists, in plain words, why the action is not well formed. It does not look at the screens
// file or at the schemes of the service: the loader and the engine that have them do.
func (m MobileAction) Problems() []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	if !slices.Contains(mobileOps, m.Do) {
		add("do must be one of %s", joinMobileOps())
		return out
	}
	forbid := func(field, value string) {
		if value != "" {
			add("%s takes no %s", m.Do, field)
		}
	}
	need := func(field, value string) {
		if value == "" {
			add("%s needs the %s", m.Do, field)
		}
	}
	if m.Reset != "" && m.Do != MobileLaunch {
		add("%s takes no reset: only launch does", m.Do)
	}
	switch m.Do {
	case MobileLaunch:
		if m.Reset != "" && !slices.Contains(MobileResets, m.Reset) {
			add("reset must be one of %s, got %q", strings.Join(MobileResets, ", "), m.Reset)
		}
		forbid("link", m.Link)
		forbid("element", m.Element)
		forbid("within", m.Within)
		forbid("value", m.Value)
		forbid("secret", m.Secret)
	case MobileOpenLink:
		need("link", m.Link)
		forbid("element", m.Element)
		forbid("within", m.Within)
		forbid("value", m.Value)
		forbid("secret", m.Secret)
	case MobileTap, MobileLongTap:
		need("screen", m.Screen)
		need("element", m.Element)
		forbid("link", m.Link)
		forbid("within", m.Within)
		forbid("value", m.Value)
		forbid("secret", m.Secret)
	case MobileType:
		need("screen", m.Screen)
		need("element", m.Element)
		forbid("link", m.Link)
		forbid("within", m.Within)
		switch {
		case m.Value == "" && m.Secret == "":
			add("type needs a value or a secret")
		case m.Value != "" && m.Secret != "":
			add("type takes a value or a secret, and not both")
		}
	case MobileSwipe:
		forbid("link", m.Link)
		forbid("within", m.Within)
		forbid("secret", m.Secret)
		directionProblem(m.Value, true, add)
		if m.Element != "" {
			need("screen", m.Screen)
		}
	case MobileScroll:
		need("screen", m.Screen)
		need("element", m.Element)
		forbid("link", m.Link)
		forbid("secret", m.Secret)
		directionProblem(m.Value, false, add)
	case MobilePress:
		forbid("link", m.Link)
		forbid("element", m.Element)
		forbid("within", m.Within)
		forbid("secret", m.Secret)
		switch {
		case m.Value == "":
			add("press needs the value: one of %s", strings.Join(MobileKeys, ", "))
		case !slices.Contains(MobileKeys, m.Value):
			add("press takes one of %s, got %q", strings.Join(MobileKeys, ", "), m.Value)
		}
	case MobileWait:
		forbid("link", m.Link)
		forbid("within", m.Within)
		forbid("value", m.Value)
		forbid("secret", m.Secret)
		if m.Element != "" {
			need("screen", m.Screen)
		}
	}
	if m.Secret != "" && !secretRefPattern.MatchString(m.Secret) {
		add("secret must be a secret reference such as env:SHOP_PASSWORD")
	}
	for field, text := range map[string]string{"link": m.Link, "value": m.Value} {
		if strings.Contains(webVariable.ReplaceAllString(text, ""), "{{") {
			add("%s holds a malformed variable", field)
		}
	}
	for field, name := range map[string]string{"element": m.Element, "within": m.Within, "screen": m.Screen} {
		if name != "" && !webNamePattern.MatchString(name) {
			add("%s %q must start with a letter or a digit and hold letters, digits, - and _", field, name)
		}
	}
	slices.Sort(out)
	return out
}

// directionProblem checks the value of a swipe, which must name a direction, and of a scroll, which
// may leave it out.
func directionProblem(v string, required bool, add func(string, ...any)) {
	switch {
	case v == "" && required:
		add("swipe needs the value: one of %s", strings.Join(MobileDirections, ", "))
	case v != "" && !slices.Contains(MobileDirections, v) && !strings.Contains(v, "{{"):
		add("the value must be one of %s, got %q", strings.Join(MobileDirections, ", "), v)
	}
}

func joinMobileOps() string {
	names := make([]string, len(mobileOps))
	for i, o := range mobileOps {
		names[i] = string(o)
	}
	return strings.Join(names, ", ")
}

// Variables lists the variable names in the link and the value, in order and without repeats.
func (m MobileAction) Variables() []string {
	var names []string
	for _, text := range []string{m.Link, m.Value} {
		for _, v := range webVariable.FindAllStringSubmatch(text, -1) {
			if !slices.Contains(names, v[1]) {
				names = append(names, v[1])
			}
		}
	}
	return names
}

// MobileRecord is what a mobile action leaves in the Action. It has no Request and no Response.
type MobileRecord struct {
	Op string `json:"op"`
	// App and Screen say which screens file and which screen the step used.
	App    string `json:"app,omitempty"`
	Screen string `json:"screen,omitempty"`
	// Package is the app that the service names, and Device is the serial of the device.
	Package string `json:"package,omitempty"`
	Device  string `json:"device,omitempty"`
	// Link is the link that an open opened.
	Link string `json:"link,omitempty"`
	// Target is the locator of the element that was acted on, with the variables filled in.
	Target   *Locator                `json:"target,omitempty"`
	Elements map[string]ElementState `json:"elements,omitempty"`
	// Screenshot is a path relative to the run directory.
	Screenshot string `json:"screenshot,omitempty"`
	// Repair holds up to three candidates for each element that was not found, and names for the
	// elements of a locator that matched several.
	Repair []Candidate `json:"repair,omitempty"`
}

// Watched is an element that a check or a capture of the step names. Presence is true when only its
// existence or its number is asked: such an element that is absent is no reason for a repair.
type Watched struct {
	Element  string
	Presence bool
	// Attrs are the attributes of the element that a check reads.
	Attrs []string
}

// UIRead is what an engine that shows a screen read after an action settled, in the one shape that
// the checks, the verdict, the report, and the repair of the screens file use for the browser and for
// a device alike.
type UIRead struct {
	Op       string
	App      string
	Screen   string
	URL      string
	Title    string
	Elements map[string]ElementState
	Repair   []Candidate
	// Screenshot is the path of the screenshot, relative to the run directory.
	Screenshot string
	// Device is true when the read is of a device and not of a page.
	Device bool
}

// UI is what the action read of a screen, or nil for an action that read none.
func (a Action) UI() *UIRead {
	switch {
	case a.Web != nil:
		w := a.Web
		return &UIRead{Op: w.Op, App: w.App, Screen: w.Screen, URL: w.URL, Title: w.Title, Elements: w.Elements, Repair: w.Repair, Screenshot: w.Screenshot}
	case a.Mobile != nil:
		m := a.Mobile
		return &UIRead{Op: m.Op, App: m.App, Screen: m.Screen, Elements: m.Elements, Repair: m.Repair, Screenshot: m.Screenshot, Device: true}
	}
	return nil
}

// OnScreen reports whether the surface shows a screen that a person can look at: a page or an app.
func (s Surface) OnScreen() bool { return s == SurfaceWeb || s == SurfaceAndroid }
