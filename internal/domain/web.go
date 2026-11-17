package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// WebOp is what a web action does.
type WebOp string

const (
	WebOpen  WebOp = "open"
	WebClick WebOp = "click"
	WebFill  WebOp = "fill"
	WebPress WebOp = "press"
	WebDrag  WebOp = "drag"
	WebWait  WebOp = "wait"
)

var webOps = []WebOp{WebOpen, WebClick, WebFill, WebPress, WebDrag, WebWait}

// WebAction is the one browser action of a step. Path and Value may hold {{variables}}. Service and
// App are set from the leg of a journey, or written in the script of a web scenario.
type WebAction struct {
	Service string `yaml:"service,omitempty"`
	App     string `yaml:"app,omitempty"`
	Do      WebOp  `yaml:"do"`
	Path    string `yaml:"path,omitempty"`
	Screen  string `yaml:"screen,omitempty"`
	Element string `yaml:"element,omitempty"`
	// To is the element that a drag ends on. It is on the same screen as Element.
	To    string `yaml:"to,omitempty"`
	Value string `yaml:"value,omitempty"`
	// Secret is a secret reference such as env:SHOP_PASSWORD. Only fill takes one.
	Secret string `yaml:"secret,omitempty"`
}

// Writes reports whether the operation can change what the service holds. A click may submit a form.
func (o WebOp) Writes() bool {
	switch o {
	case WebClick, WebFill, WebPress, WebDrag:
		return true
	}
	return false
}

// Elements lists the screen elements that the action acts on, in order.
func (w WebAction) Elements() []string {
	var out []string
	for _, e := range []string{w.Element, w.To} {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

var secretRefPattern = regexp.MustCompile(`^[a-z][a-z0-9]*:[A-Za-z0-9_.-]+$`)

// webVariable is a well-formed {{name}} of a web action.
var webVariable = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*)\}\}`)

// Problems lists, in plain words, why the action is not well formed. It does not look at the screens
// file: whether a screen and an element exist is the job of the loader that has the file.
func (w WebAction) Problems() []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	if !slices.Contains(webOps, w.Do) {
		add("do must be one of %s", joinOps())
		return out
	}
	forbid := func(field, value string) {
		if value != "" {
			add("%s takes no %s", w.Do, field)
		}
	}
	need := func(field, value string) {
		if value == "" {
			add("%s needs the %s", w.Do, field)
		}
	}
	switch w.Do {
	case WebOpen:
		need("path", w.Path)
		if w.Path != "" && !strings.HasPrefix(w.Path, "/") {
			add("path %q must start with /: it is relative to the base URL of the service", w.Path)
		}
		forbid("element", w.Element)
		forbid("to", w.To)
		forbid("value", w.Value)
		forbid("secret", w.Secret)
	case WebClick:
		need("screen", w.Screen)
		need("element", w.Element)
		forbid("path", w.Path)
		forbid("to", w.To)
		forbid("value", w.Value)
		forbid("secret", w.Secret)
	case WebFill:
		need("screen", w.Screen)
		need("element", w.Element)
		forbid("path", w.Path)
		forbid("to", w.To)
		switch {
		case w.Value == "" && w.Secret == "":
			add("fill needs a value or a secret")
		case w.Value != "" && w.Secret != "":
			add("fill takes a value or a secret, and not both")
		}
	case WebPress:
		need("value", w.Value)
		forbid("path", w.Path)
		forbid("to", w.To)
		forbid("secret", w.Secret)
		if w.Element != "" {
			need("screen", w.Screen)
		}
	case WebDrag:
		need("screen", w.Screen)
		need("element", w.Element)
		need("to", w.To)
		forbid("path", w.Path)
		forbid("value", w.Value)
		forbid("secret", w.Secret)
	case WebWait:
		forbid("path", w.Path)
		forbid("to", w.To)
		forbid("value", w.Value)
		forbid("secret", w.Secret)
		if w.Element != "" {
			need("screen", w.Screen)
		}
	}
	if w.Secret != "" && !secretRefPattern.MatchString(w.Secret) {
		add("secret must be a secret reference such as env:SHOP_PASSWORD")
	}
	for field, text := range map[string]string{"path": w.Path, "value": w.Value} {
		if strings.Contains(webVariable.ReplaceAllString(text, ""), "{{") {
			add("%s holds a malformed variable", field)
		}
	}
	for field, name := range map[string]string{"element": w.Element, "to": w.To, "screen": w.Screen} {
		if name != "" && !webNamePattern.MatchString(name) {
			add("%s %q must start with a letter or a digit and hold letters, digits, - and _", field, name)
		}
	}
	slices.Sort(out)
	return out
}

func joinOps() string {
	names := make([]string, len(webOps))
	for i, o := range webOps {
		names[i] = string(o)
	}
	return strings.Join(names, ", ")
}

var webNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Variables lists the variable names in the path and the value, in order and without repeats.
func (w WebAction) Variables() []string {
	var names []string
	for _, text := range []string{w.Path, w.Value} {
		for _, m := range webVariable.FindAllStringSubmatch(text, -1) {
			if !slices.Contains(names, m[1]) {
				names = append(names, m[1])
			}
		}
	}
	return names
}

// Locator says how to find one element of a screen. Every field may hold {{variables}}. The engine
// tries TestID, then Role with Name, then Text, then CSS, and the first one that matches wins.
type Locator struct {
	TestID string `yaml:"testId,omitempty" json:"testId,omitempty"`
	Role   string `yaml:"role,omitempty" json:"role,omitempty"`
	Name   string `yaml:"name,omitempty" json:"name,omitempty"`
	Text   string `yaml:"text,omitempty" json:"text,omitempty"`
	CSS    string `yaml:"css,omitempty" json:"css,omitempty"`
	// Within names another element of the same screen. The search is limited to it.
	Within string `yaml:"within,omitempty" json:"within,omitempty"`
}

// Empty reports whether the locator has no way to find anything.
func (l Locator) Empty() bool {
	return l.TestID == "" && l.Role == "" && l.Name == "" && l.Text == "" && l.CSS == ""
}

// Strategies lists how the locator finds its element, in the order that the engine tries them.
func (l Locator) Strategies() []string {
	var out []string
	if l.TestID != "" {
		out = append(out, "testId")
	}
	if l.Role != "" || l.Name != "" {
		out = append(out, "role")
	}
	if l.Text != "" {
		out = append(out, "text")
	}
	if l.CSS != "" {
		out = append(out, "css")
	}
	return out
}

// String is the locator in one line, for a message. It holds the fields as written, so a locator
// with variables shows the variables and not their values.
func (l Locator) String() string {
	var parts []string
	add := func(k, v string) {
		if v == "" {
			return
		}
		if strings.IndexFunc(v, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
		}) >= 0 {
			v = strconv.Quote(v)
		}
		parts = append(parts, k+": "+v)
	}
	add("testId", l.TestID)
	add("role", l.Role)
	add("name", l.Name)
	add("text", l.Text)
	add("css", l.CSS)
	add("within", l.Within)
	return "{ " + strings.Join(parts, ", ") + " }"
}

// Fields lists the strings of the locator that may hold variables, by field name.
func (l Locator) Fields() map[string]string {
	return map[string]string{"testId": l.TestID, "role": l.Role, "name": l.Name, "text": l.Text, "css": l.CSS}
}

// Screens is the content of one .semar/screens/<app>.yaml: the elements of each screen of an app.
type Screens struct {
	APIVersion string                        `yaml:"apiVersion"`
	Kind       string                        `yaml:"kind"`
	App        string                        `yaml:"app"`
	Screens    map[string]map[string]Locator `yaml:"screens"`
}

// Locator gives the locator of an element of a screen.
func (s Screens) Locator(screen, element string) (Locator, bool) {
	l, ok := s.Screens[screen][element]
	return l, ok
}

// ScreenNames lists the screens in order.
func (s Screens) ScreenNames() []string { return slices.Sorted(maps.Keys(s.Screens)) }

// ElementNames lists the elements of a screen in order.
func (s Screens) ElementNames(screen string) []string {
	return slices.Sorted(maps.Keys(s.Screens[screen]))
}

// ScreensHash is the SHA-256 of what a person approved: the app and every locator. The layout of the
// file and its comments are not part of it. It is empty when the content cannot be encoded.
func ScreensHash(s Screens) Hash {
	type element struct {
		Name string  `json:"name"`
		Loc  Locator `json:"locator"`
	}
	type screen struct {
		Name     string    `json:"name"`
		Elements []element `json:"elements"`
	}
	hashed := struct {
		App     string   `json:"app"`
		Screens []screen `json:"screens"`
	}{App: s.App}
	for _, sn := range s.ScreenNames() {
		sc := screen{Name: sn}
		for _, en := range s.ElementNames(sn) {
			sc.Elements = append(sc.Elements, element{en, s.Screens[sn][en]})
		}
		hashed.Screens = append(hashed.Screens, sc)
	}
	b, err := json.Marshal(hashed)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return Hash(hex.EncodeToString(sum[:]))
}

// ElementState is what the engine read of one element after an action settled. Count is the number of
// matches, visible or not. Found is true when exactly one visible element matched, and Text is then
// its text. Visible is true when at least one match is visible.
type ElementState struct {
	Found   bool   `json:"found"`
	Count   int    `json:"count"`
	Text    string `json:"text,omitempty"`
	Visible bool   `json:"visible"`
	// Presence is true when a check asked only whether the element is there, or how many are. Its
	// absence is then an answer and no reason for a repair.
	Presence bool `json:"presence,omitempty"`
	// Locator is what was looked up, with the variables filled in.
	Locator *Locator `json:"locator,omitempty"`
}

// Candidate is a proposed locator for an element that was not found.
type Candidate struct {
	Element string  `json:"element"`
	Locator Locator `json:"locator"`
	Why     string  `json:"why"`
}

// WebRecord is what a web action leaves in the Action.
type WebRecord struct {
	URL string `json:"url,omitempty"`
	Op  string `json:"op"`
	// App and Screen say which screens file and which screen the step used.
	App    string `json:"app,omitempty"`
	Screen string `json:"screen,omitempty"`
	// Target is the locator of the element that was acted on, with the variables filled in.
	Target   *Locator                `json:"target,omitempty"`
	Elements map[string]ElementState `json:"elements,omitempty"`
	// Screenshot is a path relative to the run directory.
	Screenshot string `json:"screenshot,omitempty"`
	// Repair holds up to three candidates for each element that was not found.
	Repair []Candidate `json:"repair,omitempty"`
	// Blocked lists the hosts that the browser refused to reach during the action.
	Blocked []string `json:"blocked,omitempty"`
	// SignIn is set when the engine signed in to the page that an open reached.
	SignIn *WebSignIn `json:"signIn,omitempty"`
}

// WebSignIn says who the engine signed in as. It holds the user and never the password.
type WebSignIn struct {
	Account string `json:"account"`
	User    string `json:"user"`
}
