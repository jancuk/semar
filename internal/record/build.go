package record

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/screens"
)

// ErrNoCheck is the answer to a recording that holds no check: a test that expects nothing proves
// nothing, so nothing is written.
var ErrNoCheck = errors.New("a test needs at least one check: hold Option and click what must be true while the window is open")

// ErrSecretName is the answer to a recording of a password that has no environment variable to read
// it from at replay.
var ErrSecretName = errors.New("a password was typed, so the test needs the name of the environment variable that holds it: use --secret-env <NAME>")

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CheckSecretEnv refuses a name that is not an environment variable.
func CheckSecretEnv(name string) error {
	if !envName.MatchString(name) {
		return fmt.Errorf("%q is not the name of an environment variable", name)
	}
	return nil
}

// Options say what the files of the recording are called and where the recording was made.
type Options struct {
	ID          domain.ScenarioID
	Ticket      string
	App         string // the screens file, .semar/screens/<App>.yaml
	Service     string
	BaseURL     string // of the service: an address that a person typed is written as a path below it
	AuthProfile string // of the scenario; "" is no session of an API
	Given       []domain.Given
	Values      Values // what the fixtures gave: they are written as variables
	SecretEnv   string // the environment variable that holds the password that was typed
	Date        time.Time
	Existing    domain.Screens // the screens file as it is now; the zero value when there is none
	// Claims, when there are any, are what a flow meant each of its checks to prove, one for each check in
	// the order of the events. A check of a recording with claims is a ticket expectation with the cite
	// of its claim, written by a model, and it expects the claim and not what the page showed.
	Claims []Claim
}

// Claim is what a check is meant to prove: the text that the element must show, which comes from the
// criterion, and the cite of the criterion.
type Claim struct{ Expect, Cite string }

// FailNow is a check whose claim differs from what the page showed when the flow read it: the test
// that the recording makes fails on this build. Element is the name that the test gives the element.
type FailNow struct{ Element, Expect, Shown, Cite string }

// NamedLocator is an element of a screen.
type NamedLocator = screens.Element

// ScreenAdd is what the recording adds to one screen: the elements that the file does not have yet,
// in the order that they were used.
type ScreenAdd = screens.Addition

// Draft is the three files of a recording, before they are written.
type Draft struct {
	Scenario domain.Scenario
	Script   domain.Script
	Added    []ScreenAdd
	Actions  int // what the person did: opens, clicks, texts, keys, and drags
	Checks   int
	// FailsNow are the checks of a recording with claims that the page did not meet when it was read.
	FailsNow []FailNow
	// Notes name what the recording left out, in plain words.
	Notes []string
}

type item struct {
	ev     Event
	path   string // of an open
	screen string
}

// Build turns what a person did into a scenario, a script, and the elements that they need. It
// writes nothing.
func Build(events []Event, o Options) (Draft, error) {
	if o.SecretEnv != "" {
		if err := CheckSecretEnv(o.SecretEnv); err != nil {
			return Draft{}, err
		}
	}
	b := &builder{o: o, namers: map[string]*Namer{}, added: map[string]int{}, ids: map[string]int{}}
	items := b.items(events)
	checks := 0
	secret := false
	for _, it := range items {
		switch {
		case it.ev.Kind == KindCheck:
			checks++
		case it.ev.Kind == KindFill && it.ev.Secret:
			secret = true
		}
	}
	if checks == 0 {
		return Draft{}, ErrNoCheck
	}
	if len(o.Claims) > 0 && len(o.Claims) != checks {
		return Draft{}, fmt.Errorf("the flow made %s and gave %d claims: each check needs its claim", plural(checks, "check"), len(o.Claims))
	}
	if secret && o.SecretEnv == "" {
		return Draft{}, ErrSecretName
	}
	b.walk(items)
	b.draft.Checks = checks
	b.draft.Scenario = domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: o.ID, Title: b.title(items), Ticket: o.Ticket,
		Surface: domain.SurfaceWeb, AuthProfile: o.AuthProfile, Given: o.Given, Steps: b.steps,
	}
	b.draft.Script = domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: o.ID, ExpectHash: domain.ExpectHash(b.draft.Scenario),
		AuthoredBy: domain.ProviderRef{Instance: "person", Harness: "record"}, Steps: b.script,
	}
	return b.draft, nil
}

type builder struct {
	o      Options
	steps  []domain.Step
	script []domain.ScriptStep
	draft  Draft
	namers map[string]*Namer
	added  map[string]int // screen to its index in draft.Added
	ids    map[string]int
	// claimed is how many claims the checks have used.
	claimed int
}

// items applies the fixture values, leaves out what a file cannot say, and merges what repeats.
func (b *builder) items(events []Event) []item {
	var out []item
	for _, raw := range events {
		ev := b.o.Values.Apply(raw)
		it := item{ev: ev}
		if ev.Kind == KindOpen {
			path, ok := b.relPath(ev.Page.URL)
			if !ok {
				b.draft.Notes = append(b.draft.Notes, fmt.Sprintf("left out the move to %s: it is not an address of the service", host(ev.Page.URL)))
				continue
			}
			it.path = b.o.Values.Replace(path)
			if n := len(out); n > 0 && out[n-1].ev.Kind == KindOpen && out[n-1].path == it.path {
				continue
			}
		} else {
			it.screen = ScreenName(raw.Page.Title, pathOf(raw.Page.URL), b.o.Values.Raw())
		}
		// A fill replaces what the field holds, so the last of two in a row is the one that counts.
		if n := len(out); n > 0 && ev.Kind == KindFill && out[n-1].ev.Kind == KindFill && sameElement(out[n-1].ev.Target, ev.Target) {
			out[n-1] = it
			continue
		}
		out = append(out, it)
	}
	kept := out[:0]
	for _, it := range out {
		if it.ev.Kind == KindFill && !it.ev.Secret && it.ev.Value == "" {
			b.draft.Notes = append(b.draft.Notes, fmt.Sprintf("left out the clearing of %s: a step cannot type nothing", it.ev.Target.Hint))
			continue
		}
		kept = append(kept, it)
	}
	return kept
}

func sameElement(a, b *Element) bool { return a != nil && b != nil && a.Locator == b.Locator }

func (b *builder) walk(items []item) {
	leading := true
	for _, it := range items {
		ev := it.ev
		if ev.Kind != KindOpen {
			leading = false
		}
		switch ev.Kind {
		case KindOpen:
			b.action(it, leading, "open", "Open "+it.path, domain.WebAction{Do: domain.WebOpen, Path: it.path})
		case KindClick:
			name := b.element(it.screen, ev.Target)
			b.action(it, false, "click-"+name, "Click "+name, domain.WebAction{Do: domain.WebClick, Screen: it.screen, Element: name})
		case KindFill:
			name := b.element(it.screen, ev.Target)
			w := domain.WebAction{Do: domain.WebFill, Screen: it.screen, Element: name}
			text := "Type " + strconv.Quote(oneLine(ev.Value)) + " in " + name
			if ev.Secret {
				w.Secret = "env:" + b.o.SecretEnv
				text = "Type the secret " + w.Secret + " in " + name
			} else {
				w.Value = ev.Value
			}
			b.action(it, false, "fill-"+name, text, w)
		case KindPress:
			w := domain.WebAction{Do: domain.WebPress, Value: ev.Value}
			text := "Press " + ev.Value
			id := "press-" + Slug(ev.Value)
			if ev.Target != nil {
				name := b.element(it.screen, ev.Target)
				w.Screen, w.Element = it.screen, name
				text += " in " + name
				id += "-" + name
			}
			b.action(it, false, id, text, w)
		case KindDrag:
			from, to := b.element(it.screen, ev.Target), b.element(it.screen, ev.To)
			b.action(it, false, "drag-"+from+"-"+to, "Drag "+from+" to "+to, domain.WebAction{Do: domain.WebDrag, Screen: it.screen, Element: from, To: to})
		case KindCheck:
			b.check(it)
		}
	}
}

func (b *builder) action(it item, setup bool, id, text string, w domain.WebAction) {
	w.Service, w.App = b.o.Service, b.o.App
	sid := b.unique(id)
	b.steps = append(b.steps, domain.Step{ID: domain.StepID(sid), Setup: setup, Action: text})
	b.script = append(b.script, domain.ScriptStep{Step: domain.StepID(sid), Web: &w})
	b.draft.Actions++
}

// check puts the expectation under the step that came before it when that step acted on the same
// screen. Otherwise the page is not the one that the last step knows, and a wait for the first
// element of the check stands between them.
func (b *builder) check(it item) {
	name := b.element(it.screen, it.ev.Target)
	last := len(b.steps) - 1
	if last < 0 || b.steps[last].Setup || b.script[last].Web.Screen != it.screen {
		sid := b.unique("wait-" + name)
		w := domain.WebAction{Service: b.o.Service, App: b.o.App, Do: domain.WebWait, Screen: it.screen, Element: name}
		b.steps = append(b.steps, domain.Step{ID: domain.StepID(sid), Action: "Wait for " + name})
		b.script = append(b.script, domain.ScriptStep{Step: domain.StepID(sid), Web: &w})
		last++
	}
	ex := domain.Expectation{Check: domain.Check{Element: name, Equals: it.ev.Value}, Source: domain.SourcePerson}
	if len(b.o.Claims) > 0 {
		c := b.o.Claims[b.claimed]
		b.claimed++
		want := oneLine(b.o.Values.Replace(c.Expect))
		ex = domain.Expectation{Check: domain.Check{Element: name, Equals: want}, Source: domain.SourceTicket, Cite: c.Cite, By: "model"}
		if shown := oneLine(it.ev.Value); shown != want {
			b.draft.FailsNow = append(b.draft.FailsNow, FailNow{Element: name, Expect: want, Shown: shown, Cite: c.Cite})
		}
	}
	b.steps[last].Expect = append(b.steps[last].Expect, ex)
}

func (b *builder) unique(id string) string {
	b.ids[id]++
	if n := b.ids[id]; n > 1 {
		return id + "-" + strconv.Itoa(n)
	}
	return id
}

// element gives the name of an element on a screen, and notes it as added when the file lacks it.
func (b *builder) element(screen string, e *Element) string {
	n, ok := b.namers[screen]
	if !ok {
		n = NewNamer(b.o.Existing.Screens[screen])
		b.namers[screen] = n
	}
	name := n.Name(e.Hint, e.Locator)
	if _, have := b.o.Existing.Screens[screen][name]; have {
		return name
	}
	i, seen := b.added[screen]
	if !seen {
		i = len(b.draft.Added)
		b.added[screen] = i
		b.draft.Added = append(b.draft.Added, ScreenAdd{Screen: screen})
	}
	for _, have := range b.draft.Added[i].Elements {
		if have.Name == name {
			return name
		}
	}
	b.draft.Added[i].Elements = append(b.draft.Added[i].Elements, NamedLocator{Name: name, Locator: e.Locator})
	return name
}

func (b *builder) title(items []item) string {
	title := "Recorded on " + b.o.Date.Format("2006-01-02")
	for _, it := range items {
		if t := strings.Join(strings.Fields(dropValues(it.ev.Page.Title, b.o.Values)), " "); t != "" && it.ev.Kind != KindOpen {
			return title + ": " + t
		}
	}
	return title
}

func dropValues(s string, v Values) string {
	for _, raw := range v.Raw() {
		s = strings.ReplaceAll(s, raw, " ")
	}
	return s
}

// relPath is the path below the base URL of the service, with its query and its fragment. It is
// false for an address of another host or of another part of the host.
func (b *builder) relPath(raw string) (string, bool) {
	base, err := url.Parse(b.o.BaseURL)
	if err != nil {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != base.Scheme || u.Host != base.Host {
		return "", false
	}
	prefix := strings.TrimSuffix(base.EscapedPath(), "/")
	path := u.EscapedPath()
	if path != prefix && !strings.HasPrefix(path, prefix+"/") {
		return "", false
	}
	path = strings.TrimPrefix(path, prefix)
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		path += "#" + u.EscapedFragment()
	}
	return path, true
}

func host(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

func pathOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Path
	}
	return ""
}

// oneLine is a text as a sentence of the scenario shows it, on one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
