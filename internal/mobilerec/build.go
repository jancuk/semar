package mobilerec

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/script"
)

// AgentDescription is the description of a flow that an agent recorded over MCP, unless the flow has one.
const AgentDescription = "Recorded by an agent over MCP"

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
	Added []screens.Addition
	// ScenarioPath is where the feature file is written, relative to the repository, when the recording
	// reopened a scenario: it is then written where it was. It is empty for a new scenario.
	ScenarioPath string
	Actions      int
	Checks       int
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
	added    map[string]int
	adds     []screens.Addition
}

// commonKey is the key of the additions to the common section.
const commonKey = "\x00common"

// taken is every name that a new element must not take with another locator: the elements that the file
// has and the ones that this build adds. A screen sees its own elements and the common ones. A common
// element is seen by every screen, so it must clear the names of all of them.
func (n *namedScreens) taken(screen string, common bool) map[string]domain.Locator {
	t := map[string]domain.Locator{}
	if common {
		for _, els := range n.existing.Screens {
			maps.Copy(t, els)
		}
	}
	maps.Copy(t, n.existing.Common)
	if !common {
		maps.Copy(t, n.existing.Screens[screen])
	}
	for _, a := range n.adds {
		if common || a.Common || a.Screen == screen {
			for _, e := range a.Elements {
				t[e.Name] = e.Locator
			}
		}
	}
	return t
}

// name gives the name of the element that the locator finds: the one that the file has already, or a
// new one, which is added. An element that is common is looked for in the common section, and an element
// of a screen that equals a common one takes its name.
func (n *namedScreens) name(screen, hint string, loc domain.Locator, common bool) string {
	name := record.NewNamer(n.taken(screen, common)).Name(hint, loc)
	if common {
		if have, ok := n.existing.Common[name]; ok && have == loc {
			return name
		}
		return n.add(commonKey, "", true, name, loc)
	}
	if have, ok := n.existing.Locator(screen, name); ok && have == loc {
		return name
	}
	return n.add(screen, screen, false, name, loc)
}

func (n *namedScreens) add(key, screen string, common bool, name string, loc domain.Locator) string {
	i, ok := n.added[key]
	if !ok {
		i = len(n.adds)
		n.added[key] = i
		n.adds = append(n.adds, screens.Addition{Screen: screen, Common: common})
	}
	for _, e := range n.adds[i].Elements {
		if e.Name == name {
			return name
		}
	}
	n.adds[i].Elements = append(n.adds[i].Elements, screens.Element{Name: name, Locator: loc})
	return name
}

// elementName is the name of the element of a step in the screens file. A step that was loaded from files
// and not changed keeps the name that its file has. Any other goes through the namer, with its container.
func (n *namedScreens) elementName(ev *event) string {
	if ev.name != "" {
		return ev.name
	}
	loc := ev.locator
	if ev.container != nil {
		cn := ev.containerName
		if cn == "" {
			cn = n.name(ev.screen, ev.container.Hint, ev.container.Locator, ev.shared)
		}
		loc.Within = cn
	}
	return n.name(ev.screen, ev.hint, loc, ev.shared)
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
	ns := &namedScreens{existing: existing, added: map[string]int{}}
	d := Draft{Kind: s.o.Kind, ID: s.o.ID, App: s.o.App, Ticket: s.o.Ticket}
	if s.dirty {
		d.Notes = append(d.Notes, "a step that the device had done was deleted, so the device was not where the steps say: replay the recording before you trust it")
	}

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

	// One pass in the order of the recording: a step is a step, and a check or a capture goes on the step
	// before it when that step is on the same screen, or on a wait step of its own, which runs at the point
	// where it was recorded.
	flowKind := s.o.Kind == KindFlow
	var steps []domain.Step
	var script_ []domain.ScriptStep
	var stepEvents []*event
	setup := true
	checks := 0
	for _, ev := range s.events {
		if !ev.isRead() {
			st := domain.ScriptStep{Step: domain.StepID(ev.id)}
			if ev.use != "" {
				st.Use, st.With = ev.use, ev.with
				st.Captures = slices.Clone(ev.captures)
				st.Expect = slices.Clone(ev.flowExpect)
				d.Uses = appendOnce(d.Uses, ev.use)
			} else {
				m := domain.MobileAction{Do: ev.do, Value: ev.value, Secret: ev.secret, Link: ev.link, Screen: ev.screen, Within: ev.within}
				if ev.do == domain.MobileLaunch {
					m.Reset = ev.reset
					if reset != "" && ev == first {
						m.Reset = reset
					}
					m.Screen = ""
				}
				if ev.element() {
					m.Element = ns.elementName(ev)
				}
				if ev.to != nil {
					m.To = ns.elementName(ev.to)
				}
				if ev.do == domain.MobileSwipe && ev.element() || ev.do == domain.MobileWait && ev.element() {
					m.Screen = ev.screen
				}
				if ev.do == domain.MobileSwipe && !ev.element() || ev.do == domain.MobilePress || ev.do == domain.MobileWait && !ev.element() {
					m.Screen = ""
				}
				if !flowKind {
					m.Service, m.App = s.o.Service, s.o.App
				} else {
					m.Service, m.App = ev.service, ev.app
				}
				st.Mobile = &m
				st.Captures = slices.Clone(ev.captures)
				st.Expect = slices.Clone(ev.flowExpect)
				d.Actions++
			}
			if !ev.setup {
				setup = false
			}
			ds := domain.Step{ID: domain.StepID(ev.id), Setup: setup && ev.setup, Action: ev.text}
			if o := ev.step; o != nil {
				ds.As, ds.Hint = o.As, o.Hint
			}
			steps = append(steps, ds)
			script_ = append(script_, st)
			stepEvents = append(stepEvents, ev)
			continue
		}
		name := ns.elementName(ev)
		idx := len(steps) - 1
		// A check that was loaded stays on the step that it was on, when that step is still the one before it.
		owned := ev.after != nil && idx >= 0 && stepEvents[idx] == ev.after
		host := func() bool {
			if idx < 0 || script_[idx].Mobile == nil || script_[idx].Mobile.Screen != ev.screen || !flowKind && steps[idx].Setup {
				return false
			}
			// A check that expects a value is no check on the step that captures it: the step reads the value
			// after the check is evaluated.
			if ev.assertion != nil {
				for _, m := range refPattern.FindAllStringSubmatch(ev.assertion.value, -1) {
					if slices.ContainsFunc(script_[idx].Captures, func(c domain.Capture) bool { return c.Name == m[1] }) {
						return false
					}
				}
			}
			// A capture reads the screen of its step, and a launch, a key, and a link have none.
			if ev.capture != nil {
				switch script_[idx].Mobile.Do {
				case domain.MobileLaunch, domain.MobilePress, domain.MobileOpenLink:
					return false
				}
			}
			return true
		}
		if !owned && !host() {
			id := uniqueStepID(steps, "wait-"+name)
			m := domain.MobileAction{Do: domain.MobileWait, Screen: ev.screen, Element: name}
			if !flowKind {
				m.Service, m.App = s.o.Service, s.o.App
			}
			steps = append(steps, domain.Step{ID: domain.StepID(id), Action: "Wait for " + ev.hint})
			script_ = append(script_, domain.ScriptStep{Step: domain.StepID(id), Mobile: &m})
			stepEvents = append(stepEvents, nil)
			setup = false
			idx = len(steps) - 1
		}
		if c := ev.capture; c != nil {
			script_[idx].Captures = append(script_[idx].Captures, domain.Capture{Name: c.name, Element: name, Pattern: c.pattern})
			continue
		}
		var ex domain.Expectation
		if ev.assertion.expect != nil {
			ex = *ev.assertion.expect
			ex.Check.Element = name
		} else {
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
			ex = domain.Expectation{Check: ck, Source: domain.SourcePerson}
			if s.o.Author != nil {
				ex.Source, ex.By = domain.SourceBaseline, "model"
			}
		}
		if flowKind {
			// The checks of a flow are on its steps, and a person wrote them, unless an agent recorded them.
			if s.o.Author == nil {
				ex.Source = domain.SourcePerson
			}
			script_[idx].Expect = append(script_[idx].Expect, ex)
		} else {
			steps[idx].Expect = append(steps[idx].Expect, ex)
		}
		checks++
	}

	if flowKind {
		f := domain.Flow{
			APIVersion: "semar/v1", Kind: "Flow", ID: s.o.ID, Surface: domain.SurfaceAndroid,
			Description: s.o.Title, Service: s.o.Service, App: s.o.App,
		}
		if r := s.o.Resume; r != nil && r.Flow.ID != "" {
			f = r.Flow
			f.ID, f.Steps = s.o.ID, nil
			if s.o.Title != "" {
				f.Description = s.o.Title
			}
		}
		if s.o.Author != nil && f.Description == "" {
			f.Description = AgentDescription
		}
		f.Params = s.flowParams()
		f.Steps = script_
		if ps := flow.ValidateWith(f, f.ID, func(id string) (domain.Flow, error) {
			e, err := flow.Load(s.o.Repo, id)
			return e.Flow, err
		}); len(ps) > 0 {
			return Draft{}, fmt.Errorf("the recording makes a flow that is not valid: %s", ps[0])
		}
		d.Flow, d.Added, d.Checks = f, ns.adds, checks
		return d, s.checkScreens(&d, ns, nil)
	}
	if len(s.newParams) > 0 {
		return Draft{}, errors.New("a scenario has no params: a step types {{" + s.newParams[0].Name + "}}, which only a flow can have: record a flow, or delete the step")
	}

	if checks == 0 {
		return Draft{}, ErrNoCheck
	}
	title := s.o.Title
	sc := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: domain.ScenarioID(s.o.ID), Ticket: s.o.Ticket,
		Surface: domain.SurfaceAndroid,
	}
	authored := domain.ProviderRef{Instance: "person", Harness: "record"}
	if s.o.Author != nil {
		authored = *s.o.Author
	}
	if r := s.o.Resume; r != nil && r.Scenario.ID != "" {
		// A reopened scenario keeps what the recording does not touch: its intent, its profile, its fixtures,
		// its suites, its title.
		sc = r.Scenario
		sc.ID, sc.Steps = domain.ScenarioID(s.o.ID), nil
		if title == "" {
			title = sc.Title
		}
		if sc.ID == r.Scenario.ID {
			// The same scenario keeps its ticket, which the path of its file holds: a default that a surface
			// worked out from the ID must not move it.
			d.ScenarioPath = r.Path
		} else if s.o.Ticket != "" {
			sc.Ticket = s.o.Ticket
		}
		if reflect.DeepEqual(script_, r.Script.Steps) && s.o.Author == nil {
			authored = r.Script.AuthoredBy
		}
	}
	if title == "" {
		title = "Recorded " + s.o.ID
	}
	sc.Title, sc.Steps = title, steps
	d.Scenario = sc
	d.Script = domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc), ShapeHash: domain.ShapeHash(sc),
		AuthoredBy: authored, Steps: script_,
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
		if ev.capture != nil {
			texts = append(texts, ev.capture.pattern, ev.capture.name)
		}
		if ev.container != nil {
			texts = append(texts, ev.container.Hint, ev.container.Locator.ID, ev.container.Locator.Desc)
		}
		if to := ev.to; to != nil {
			texts = append(texts, to.hint, to.locator.ID, to.locator.Desc, to.locator.Name, to.locator.Text, to.locator.XPath)
			if to.container != nil {
				texts = append(texts, to.container.Hint, to.container.Locator.ID, to.container.Locator.Desc)
			}
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
			uses = append(uses, screens.MobileUse(where, *st.Mobile, captureElements(st.Captures)))
		}
	}
	if s.o.Kind == KindFlow {
		for _, st := range flow.Steps(d.Flow) {
			add("step "+string(st.Step), st)
			for _, ex := range st.Expect {
				if st.Mobile != nil {
					uses = append(uses, screens.Use{Where: "step " + string(st.Step), Surface: domain.SurfaceAndroid, Screen: st.Mobile.Screen, Elements: []string{ex.Check.Element}})
				}
			}
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
	if len(existing.Common) > 0 {
		out.Common = maps.Clone(existing.Common)
	}
	for name, els := range existing.Screens {
		out.Screens[name] = map[string]domain.Locator{}
		for k, v := range els {
			out.Screens[name][k] = v
		}
	}
	for _, a := range adds {
		if a.Common {
			if out.Common == nil {
				out.Common = map[string]domain.Locator{}
			}
			for _, e := range a.Elements {
				out.Common[e.Name] = e.Locator
			}
			continue
		}
		if out.Screens[a.Screen] == nil {
			out.Screens[a.Screen] = map[string]domain.Locator{}
		}
		for _, e := range a.Elements {
			out.Screens[a.Screen][e.Name] = e.Locator
		}
	}
	return out
}
