package mobilerec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/script"
)

// Draft is what a recording makes, before it is written: a flow, or a scenario with its script, and the
// elements that the screens file lacks.
type Draft struct {
	Kind     Kind
	ID       string
	App      string
	Ticket   string
	Flow     domain.Flow
	Scenario domain.Scenario
	Script   domain.Script
	// Added are the elements that the screens file does not have yet, by screen, in the order that they
	// were used. An element that the file has already, with the same locator, is reused and not added.
	Added   []screens.Addition
	Actions int
	Checks  int
	// Uses are the flows that the scenario runs.
	Uses []string
	// Notes say, in plain words, what the recording changed or left out.
	Notes []string
}

// Build turns the steps into a draft. It writes nothing. It reads the screens file as it is now, so
// that an element that the file has is reused, and one that clashes gets a new name.
func (s *Session) Build() (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildLocked()
}

type namedScreens struct {
	existing domain.Screens
	namers   map[string]*record.Namer
	added    map[string]int
	adds     []screens.Addition
}

func (n *namedScreens) name(screen, hint string, loc domain.Locator) string {
	nm, ok := n.namers[screen]
	if !ok {
		nm = record.NewNamer(n.existing.Screens[screen])
		n.namers[screen] = nm
	}
	name := nm.Name(hint, loc)
	if have, ok := n.existing.Locator(screen, name); ok && have == loc {
		return name
	}
	i, ok := n.added[screen]
	if !ok {
		i = len(n.adds)
		n.added[screen] = i
		n.adds = append(n.adds, screens.Addition{Screen: screen})
	}
	for _, e := range n.adds[i].Elements {
		if e.Name == name {
			return name
		}
	}
	n.adds[i].Elements = append(n.adds[i].Elements, screens.Element{Name: name, Locator: loc})
	return name
}

func (s *Session) buildLocked() (Draft, error) {
	if len(s.events) == 0 {
		return Draft{}, ErrNothingToSave
	}
	if err := s.checkClean(); err != nil {
		return Draft{}, err
	}
	first := s.events[0]
	if first.do != domain.MobileLaunch && first.use == "" {
		return Draft{}, errors.New("a recording starts with the launch of the app, or with a flow")
	}
	existing, err := screens.Load(s.o.Repo, s.o.App)
	if err != nil && !errors.Is(err, screens.ErrNotFound) {
		return Draft{}, err
	}
	ns := &namedScreens{existing: existing, namers: map[string]*record.Namer{}, added: map[string]int{}}
	d := Draft{Kind: s.o.Kind, ID: s.o.ID, App: s.o.App, Ticket: s.o.Ticket}

	reset := ""
	if s.wantClear && first.do == domain.MobileLaunch && first.reset != "clear" {
		if !s.emulatorLocked() && !s.o.ConfirmClear {
			return Draft{}, ErrNeedsConfirm
		}
		reset = "clear"
		d.Notes = append(d.Notes, "the first launch resets with clear: a sign-in stays in the app until its data is cleared, so a replay would start signed in")
	} else if s.wantClear && first.use != "" {
		d.Notes = append(d.Notes, "this recording types a secret after the flow "+first.use+": the reset of the flow decides whether a replay starts signed out")
	}

	type built struct {
		ev   *event
		step domain.ScriptStep
	}
	var out []built
	checks := 0
	for _, ev := range s.events {
		if ev.isAssert() {
			continue
		}
		st := domain.ScriptStep{Step: domain.StepID(ev.id)}
		if ev.use != "" {
			st.Use, st.With = ev.use, ev.with
			d.Uses = appendOnce(d.Uses, ev.use)
			out = append(out, built{ev, st})
			continue
		}
		m := domain.MobileAction{Do: ev.do, Value: ev.value, Secret: ev.secret, Link: ev.link, Screen: ev.screen}
		if ev.do == domain.MobileLaunch {
			m.Reset = ev.reset
			if reset != "" && ev == first {
				m.Reset = reset
			}
			m.Screen = ""
		}
		if ev.element() {
			loc := ev.locator
			if ev.container != nil {
				loc.Within = ns.name(ev.screen, ev.container.Hint, ev.container.Locator)
			}
			m.Element = ns.name(ev.screen, ev.hint, loc)
		}
		if ev.do == domain.MobileSwipe && ev.element() || ev.do == domain.MobileWait && ev.element() {
			m.Screen = ev.screen
		}
		if ev.do == domain.MobileSwipe && !ev.element() || ev.do == domain.MobilePress || ev.do == domain.MobileWait && !ev.element() {
			m.Screen = ""
		}
		st.Mobile = &m
		out = append(out, built{ev, st})
		d.Actions++
	}

	if s.o.Kind == KindFlow {
		for _, ev := range s.events {
			if ev.isAssert() {
				return Draft{}, errors.New("a flow has no checks: delete the assertions, or record a scenario")
			}
		}
		f := domain.Flow{
			APIVersion: "semar/v1", Kind: "Flow", ID: s.o.ID, Surface: domain.SurfaceAndroid,
			Description: s.o.Title, Service: s.o.Service, App: s.o.App,
		}
		for _, b := range out {
			st := b.step
			if st.Mobile != nil {
				st.Mobile.Service, st.Mobile.App = "", ""
			}
			f.Steps = append(f.Steps, st)
		}
		if ps := flow.Validate(f, f.ID); len(ps) > 0 {
			return Draft{}, fmt.Errorf("the recording makes a flow that is not valid: %s", ps[0])
		}
		d.Flow, d.Added = f, ns.adds
		return d, s.checkScreens(&d, ns, nil)
	}

	// A scenario: build steps and checks in one chronological pass so any synthetic wait step needed
	// after a screen transition runs at the exact point it was recorded, never deferred to the end.
	var steps []domain.Step
	var script_ []domain.ScriptStep
	setup := true
	actionIdx := 0
	for _, ev := range s.events {
		if !ev.isAssert() {
			b := out[actionIdx]
			actionIdx++
			if !b.ev.setup {
				setup = false
			}
			st := b.step
			if st.Mobile != nil {
				st.Mobile.Service, st.Mobile.App = s.o.Service, s.o.App
			}
			steps = append(steps, domain.Step{ID: domain.StepID(b.ev.id), Setup: setup && b.ev.setup, Action: b.ev.text})
			script_ = append(script_, st)
			continue
		}
		name := ns.name(ev.screen, ev.hint, withContainer(ns, ev))
		ck := domain.Check{Element: name}
		switch ev.assertion.kind {
		case AssertExists:
			yes := true
			ck.Exists = &yes
		case AssertNotExists:
			no := false
			ck.Exists = &no
		case AssertCount:
			n, _ := strconv.Atoi(ev.assertion.value)
			ck.Count = &n
		case AssertAttribute:
			ck.Attribute, ck.Equals = ev.assertion.attr, ev.assertion.value
		case AssertVisible, AssertHidden, AssertEnabled, AssertDisabled, AssertChecked, AssertUnchecked:
			ck.State = ev.assertion.kind.State()
		case AssertContains:
			ck.Contains = ev.assertion.value
		default:
			ck.Equals = ev.assertion.value
		}
		idx := len(steps) - 1
		if idx < 0 || steps[idx].Setup || script_[idx].Mobile == nil || script_[idx].Mobile.Screen != ev.screen {
			id := uniqueStepID(steps, "wait-"+name)
			m := domain.MobileAction{Service: s.o.Service, App: s.o.App, Do: domain.MobileWait, Screen: ev.screen, Element: name}
			steps = append(steps, domain.Step{ID: domain.StepID(id), Action: "Wait for " + ev.hint})
			script_ = append(script_, domain.ScriptStep{Step: domain.StepID(id), Mobile: &m})
			setup = false
			idx = len(steps) - 1
		}
		steps[idx].Expect = append(steps[idx].Expect, domain.Expectation{Check: ck, Source: domain.SourcePerson})
		checks++
	}
	if checks == 0 {
		return Draft{}, ErrNoCheck
	}
	title := s.o.Title
	if title == "" {
		title = "Recorded " + s.o.ID
	}
	sc := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: domain.ScenarioID(s.o.ID), Title: title, Ticket: s.o.Ticket,
		Surface: domain.SurfaceAndroid, Steps: steps,
	}
	d.Scenario = sc
	d.Script = domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc), ShapeHash: domain.ShapeHash(sc),
		AuthoredBy: domain.ProviderRef{Instance: "person", Harness: "record"}, Steps: script_,
	}
	d.Added, d.Checks = ns.adds, checks
	if ps := scenario.Validate(sc, scenario.FromFile); len(ps) > 0 {
		return Draft{}, fmt.Errorf("the recording makes a scenario that is not valid: %s: %s", ps[0].Path, ps[0].Message)
	}
	if ps := script.ValidateWithFlows(sc, d.Script, nil, flow.Loader(s.o.Repo).Resolver()); len(ps) > 0 {
		return Draft{}, fmt.Errorf("the recording makes a script that does not fit its scenario: %s", ps[0].Message)
	}
	return d, s.checkScreens(&d, ns, script_)
}

func withContainer(ns *namedScreens, ev *event) domain.Locator {
	loc := ev.locator
	if ev.container != nil {
		loc.Within = ns.name(ev.screen, ev.container.Hint, ev.container.Locator)
	}
	return loc
}

func uniqueStepID(steps []domain.Step, base string) string {
	id := base
	for i := 2; ; i++ {
		clash := false
		for _, st := range steps {
			if string(st.ID) == id {
				clash = true
			}
		}
		if !clash {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

func appendOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// emulatorLocked is Emulator for a caller that holds the lock.
func (s *Session) emulatorLocked() bool { return strings.HasPrefix(s.device.Device, "emulator-") }

// checkClean refuses a recording that holds a secret value in any text that would be written.
func (s *Session) checkClean() error {
	for _, ev := range s.events {
		texts := []string{ev.id, ev.text, ev.screen, ev.hint, ev.value, ev.link, ev.locator.ID, ev.locator.Desc, ev.locator.Name,
			ev.locator.Text, ev.locator.XPath}
		if ev.assertion != nil {
			texts = append(texts, ev.assertion.value)
		}
		if ev.container != nil {
			texts = append(texts, ev.container.Hint, ev.container.Locator.ID, ev.container.Locator.Desc)
		}
		for _, t := range texts {
			if s.ov.Leaks(t) {
				return ErrSecretOnScreen
			}
		}
	}
	return nil
}

// checkScreens checks every step of the draft against the screens file that the write would make.
func (s *Session) checkScreens(d *Draft, ns *namedScreens, steps []domain.ScriptStep) error {
	merged := mergeScreens(ns.existing, s.o.App, d.Added)
	var uses []screens.Use
	add := func(where string, st domain.ScriptStep) {
		if st.Mobile != nil {
			uses = append(uses, screens.MobileUse(where, *st.Mobile, nil))
		}
	}
	if s.o.Kind == KindFlow {
		for _, st := range flow.Steps(d.Flow) {
			add("step "+string(st.Step), st)
		}
	} else {
		for _, st := range steps {
			add("step "+string(st.Step), st)
		}
		// A check reads an element of the screen of its step.
		for i, st := range d.Scenario.Steps {
			for _, ex := range st.Expect {
				if steps[i].Mobile != nil {
					uses = append(uses, screens.Use{Where: "step " + string(st.ID), Surface: domain.SurfaceAndroid, Screen: steps[i].Mobile.Screen, Elements: []string{ex.Check.Element}})
				}
			}
		}
	}
	if problems := screens.Check(merged, uses); len(problems) > 0 {
		return fmt.Errorf("the recording does not fit its screens file: %s", problems[0])
	}
	return nil
}

// mergeScreens is the screens file after the additions, in memory.
func mergeScreens(existing domain.Screens, app string, adds []screens.Addition) domain.Screens {
	out := domain.Screens{APIVersion: "semar/v1", Kind: "Screens", App: app, Screens: map[string]map[string]domain.Locator{}}
	for name, els := range existing.Screens {
		out.Screens[name] = map[string]domain.Locator{}
		for k, v := range els {
			out.Screens[name][k] = v
		}
	}
	for _, a := range adds {
		if out.Screens[a.Screen] == nil {
			out.Screens[a.Screen] = map[string]domain.Locator{}
		}
		for _, e := range a.Elements {
			out.Screens[a.Screen][e.Name] = e.Locator
		}
	}
	return out
}
