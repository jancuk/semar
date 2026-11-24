package domain

import (
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
	// WebSelect chooses the option of a list whose label is the Value.
	WebSelect  WebOp = "select"
	WebHover   WebOp = "hover"
	WebCheck   WebOp = "check"
	WebUncheck WebOp = "uncheck"
	// WebUpload puts the file at the Value, a path below .semar/files/ of the repository, in a file field.
	WebUpload WebOp = "upload"
)

var webOps = []WebOp{WebOpen, WebClick, WebFill, WebPress, WebDrag, WebWait, WebSelect, WebHover, WebCheck, WebUncheck, WebUpload}

// UploadDir is where the file of an upload is, below the repository.
const UploadDir = ".semar/files"

// UploadPathProblem says in plain words why a path is not one below UploadDir, or "". It looks at the
// text only: the engine also checks the file and the links on the disk.
func UploadPathProblem(p string) string {
	switch {
	case p == "":
		return "an upload needs the path of a file below " + UploadDir + "/"
	case strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~"):
		return "the path of an upload is relative to " + UploadDir + "/ and has no absolute part"
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "the path of an upload must stay below " + UploadDir + "/"
		}
	}
	return ""
}

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
	case WebClick, WebFill, WebPress, WebDrag, WebSelect, WebCheck, WebUncheck, WebUpload:
		return true
	}
	return false
}

// UploadProblem says why the path of an upload that a script holds is not one below UploadDir, or "". A
// path with a variable is checked by the engine, which refuses the step with the same words.
func (w WebAction) UploadProblem() string {
	if w.Do != WebUpload || strings.Contains(w.Value, "{{") {
		return ""
	}
	return UploadPathProblem(w.Value)
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
	case WebClick, WebHover, WebCheck, WebUncheck:
		need("screen", w.Screen)
		need("element", w.Element)
		forbid("path", w.Path)
		forbid("to", w.To)
		forbid("value", w.Value)
		forbid("secret", w.Secret)
	case WebSelect, WebUpload:
		need("screen", w.Screen)
		need("element", w.Element)
		need("value", w.Value)
		forbid("path", w.Path)
		forbid("to", w.To)
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
// tries TestID, then Role with Name, then Text, then CSS, and the first one that matches wins. On a
// device it tries ID or TestID, Desc, Role with Name, Text, XPath, and Class.
type Locator struct {
	TestID string `yaml:"testId,omitempty" json:"testId,omitempty"`
	Role   string `yaml:"role,omitempty" json:"role,omitempty"`
	Name   string `yaml:"name,omitempty" json:"name,omitempty"`
	Text   string `yaml:"text,omitempty" json:"text,omitempty"`
	CSS    string `yaml:"css,omitempty" json:"css,omitempty"`
	// ID, Desc, Class, and XPath find an element of an app on a device. ID is the resource id on
	// Android, and Desc is its content description. A browser ignores them, and a device ignores CSS.
	ID    string `yaml:"id,omitempty" json:"id,omitempty"`
	Desc  string `yaml:"desc,omitempty" json:"desc,omitempty"`
	Class string `yaml:"class,omitempty" json:"class,omitempty"`
	XPath string `yaml:"xpath,omitempty" json:"xpath,omitempty"`
	// Within names another element of the same screen. The search is limited to it.
	Within string `yaml:"within,omitempty" json:"within,omitempty"`
	// HasText keeps only the matches that show this text or description, or hold a visible element that
	// does, and of those the innermost. Index picks the nth visible match, counted from 1, in the order of
	// the screen. Both are for a device only, and HasText cannot be used alone.
	HasText string `yaml:"hasText,omitempty" json:"hasText,omitempty"`
	Index   int    `yaml:"index,omitempty" json:"index,omitempty"`
}

// Empty reports whether the locator has no way to find anything.
func (l Locator) Empty() bool {
	return l.TestID == "" && l.Role == "" && l.Name == "" && l.Text == "" && l.CSS == "" &&
		l.ID == "" && l.Desc == "" && l.Class == "" && l.XPath == ""
}

// ForWeb reports whether a browser can find the element with the locator.
func (l Locator) ForWeb() bool { return len(l.Strategies()) > 0 }

// ForMobile reports whether a device can find the element with the locator.
func (l Locator) ForMobile() bool { return len(l.MobileStrategies()) > 0 }

// MobileStrategies lists how the locator finds its element on a device, in the order that the engine
// tries them. TestID is the same as ID there.
func (l Locator) MobileStrategies() []string {
	var out []string
	if l.ID != "" || l.TestID != "" {
		out = append(out, "id")
	}
	if l.Desc != "" {
		out = append(out, "desc")
	}
	if l.Role != "" || l.Name != "" {
		out = append(out, "role")
	}
	if l.Text != "" {
		out = append(out, "text")
	}
	if l.XPath != "" {
		out = append(out, "xpath")
	}
	if l.Class != "" {
		out = append(out, "class")
	}
	return out
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
	add("id", l.ID)
	add("desc", l.Desc)
	add("class", l.Class)
	add("xpath", l.XPath)
	add("within", l.Within)
	add("hasText", l.HasText)
	if l.Index != 0 {
		parts = append(parts, "index: "+strconv.Itoa(l.Index))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// Fields lists the strings of the locator that may hold variables, by field name.
func (l Locator) Fields() map[string]string {
	return map[string]string{"testId": l.TestID, "role": l.Role, "name": l.Name, "text": l.Text, "css": l.CSS,
		"id": l.ID, "desc": l.Desc, "class": l.Class, "xpath": l.XPath, "hasText": l.HasText}
}

// Screens is the content of one .semar/screens/<app>.yaml: the elements of each screen of an app.
type Screens struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	App        string `yaml:"app"`
	// Common holds the elements that every screen can find. An element of a screen with the same name
	// wins over it.
	Common  map[string]Locator            `yaml:"common,omitempty"`
	Screens map[string]map[string]Locator `yaml:"screens"`
}

// Locator gives the locator of an element of a screen, or else of the common elements.
func (s Screens) Locator(screen, element string) (Locator, bool) {
	if l, ok := s.Screens[screen][element]; ok {
		return l, true
	}
	l, ok := s.Common[element]
	return l, ok
}

// ScreenNames lists the screens in order.
func (s Screens) ScreenNames() []string { return slices.Sorted(maps.Keys(s.Screens)) }

// ElementNames lists the elements of a screen in order.
func (s Screens) ElementNames(screen string) []string {
	return slices.Sorted(maps.Keys(s.Screens[screen]))
}

// ElementState is what the engine read of one element after an action settled. Count is the number of
// matches, visible or not. Found is true when exactly one visible element matched, and Text is then
// its text. Visible is true when at least one match is visible.
type ElementState struct {
	Found   bool   `json:"found"`
	Count   int    `json:"count"`
	Text    string `json:"text,omitempty"`
	Visible bool   `json:"visible"`
	// Enabled and Checked are read from the one element that matched. Attrs holds the attributes that
	// a check named, and only those the element has. A password field gives no value.
	Enabled bool              `json:"enabled,omitempty"`
	Checked bool              `json:"checked,omitempty"`
	Attrs   map[string]string `json:"attrs,omitempty"`
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
	// Title is the title of the page after the action settled.
	Title string `json:"title,omitempty"`
	Op    string `json:"op"`
	// App and Screen say which screens file and which screen the step used.
	App    string `json:"app,omitempty"`
	Screen string `json:"screen,omitempty"`
	// Target is the locator of the element that was acted on, with the variables filled in.
	Target   *Locator                `json:"target,omitempty"`
	Elements map[string]ElementState `json:"elements,omitempty"`
	// Screenshot is a path relative to the run directory.
	Screenshot string `json:"screenshot,omitempty"`
	// Repair holds up to three candidates for each element that was not found, and up to three names for
	// the elements of a locator that matched several.
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
