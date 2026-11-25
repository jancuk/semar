package mobilerec

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/screens"
)

// Loaded is a saved flow or scenario that a recording reopens: the files, as they were read. Scenario and
// Script are set for a scenario, and Flow for a flow. Screens is the screens file of the app, and Path is
// the file of the flow or the feature of the scenario, relative to the repository, with slashes.
type Loaded struct {
	Scenario domain.Scenario
	Script   domain.Script
	Flow     domain.Flow
	Screens  domain.Screens
	Path     string
}

// load turns the files of Options.Resume into the steps of the session, and the defaults of the params.
// A scenario with an API step or a web step is refused: reopening is for steps on a device.
func (s *Session) load() error {
	r := s.o.Resume
	var events []*event
	var err error
	switch s.o.Kind {
	case KindFlow:
		if r.Flow.ID != s.o.ID {
			return fmt.Errorf("the files hold the flow %q, and the recording is of %q", r.Flow.ID, s.o.ID)
		}
		events, err = loadFlow(r)
		for _, p := range r.Flow.Params {
			if p.Default != nil {
				s.params[p.Name] = *p.Default
			}
		}
	default:
		if string(r.Scenario.ID) != s.o.ID {
			return fmt.Errorf("the files hold the scenario %q, and the recording is of %q", r.Scenario.ID, s.o.ID)
		}
		events, err = loadScenario(r)
	}
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return errors.New("the saved recording has no step")
	}
	if at := s.o.ResumeAt; at > len(events) {
		return fmt.Errorf("the recording has %d steps, and there is no step %d to go to", len(events), at)
	}
	s.events = events
	return nil
}

func loadFlow(r *Loaded) ([]*event, error) {
	var events []*event
	for _, st := range r.Flow.Steps {
		ev, err := loadStep(r.Screens, st, r.Flow.Service, r.Flow.App)
		if err != nil {
			return nil, err
		}
		ev.text = stepText(ev)
		ev.flowExpect = slices.Clone(st.Expect)
		events = append(events, ev)
	}
	return events, nil
}

func loadScenario(r *Loaded) ([]*event, error) {
	sc, sc2 := r.Scenario, r.Script
	if sc.Surface != domain.SurfaceAndroid {
		return nil, fmt.Errorf("the scenario %s is for %q: only an Android scenario is reopened", sc.ID, sc.Surface)
	}
	if len(sc.Steps) != len(sc2.Steps) {
		return nil, fmt.Errorf("the stored script has %d steps and the scenario %d: record the scenario again", len(sc2.Steps), len(sc.Steps))
	}
	var events []*event
	for i, st := range sc.Steps {
		ss := sc2.Steps[i]
		if ss.Step != st.ID {
			return nil, fmt.Errorf("the stored script has the step %s where the scenario has %s: record the scenario again", ss.Step, st.ID)
		}
		if len(ss.Expect) > 0 {
			return nil, fmt.Errorf("step %s: the script of a scenario has no checks of its own", st.ID)
		}
		ev, err := loadStep(r.Screens, ss, "", "")
		if err != nil {
			return nil, err
		}
		own := st
		own.Expect = nil
		ev.id, ev.text, ev.setup, ev.step = string(st.ID), st.Action, st.Setup, &own
		events = append(events, ev)
		for _, ex := range st.Expect {
			ck, err := loadCheck(r.Screens, ev, st, ex, events)
			if err != nil {
				return nil, err
			}
			events = append(events, ck)
		}
	}
	return events, nil
}

// loadStep makes the step event of a script step: its action, and for an element its locator from the
// screens file, with the names that the file gives, so that a save that changes nothing writes nothing.
func loadStep(scr domain.Screens, ss domain.ScriptStep, service, app string) (*event, error) {
	id := string(ss.Step)
	switch {
	case ss.Request != nil || ss.Web != nil:
		return nil, fmt.Errorf("step %s calls an API or a web page: only steps on a device can be reopened", id)
	case ss.Use != "":
		return &event{id: id, use: ss.Use, with: maps.Clone(ss.With), captures: slices.Clone(ss.Captures)}, nil
	case ss.Mobile == nil:
		return nil, fmt.Errorf("step %s has no action on a device", id)
	}
	m := *ss.Mobile
	if m.Service != "" && service != "" && m.Service != service {
		return nil, fmt.Errorf("step %s is for the service %s", id, m.Service)
	}
	ev := &event{
		id: id, do: m.Do, value: m.Value, secret: m.Secret, reset: m.Reset, link: m.Link, screen: m.Screen, within: m.Within, service: m.Service, app: m.App,
		captures: slices.Clone(ss.Captures), purpose: purposeOf(string(m.Do)),
	}
	if m.Element == "" {
		return ev, nil
	}
	names := slices.Concat(m.Elements(), captureElements(ss.Captures))
	if err := bindElement(scr, ev, m.Screen, m.Element, names); err != nil {
		return nil, fmt.Errorf("step %s: %w", id, err)
	}
	if m.To != "" {
		to := &event{screen: m.Screen, purpose: ForAssert}
		if err := bindElement(scr, to, m.Screen, m.To, []string{m.To}); err != nil {
			return nil, fmt.Errorf("step %s: %w", id, err)
		}
		ev.to = to
	}
	return ev, nil
}

func captureElements(caps []domain.Capture) []string {
	var out []string
	for _, c := range caps {
		if c.Element != "" {
			out = append(out, c.Element)
		}
	}
	return out
}

// bindElement gives the event the locator of the element, and the names of the file for it. The
// locators that the step sends are those of the element and the elements that it needs.
func bindElement(scr domain.Screens, ev *event, screen, element string, needed []string) error {
	loc, ok := scr.Locator(screen, element)
	if !ok {
		return fmt.Errorf("the screens file has no element %s on the screen %s", element, screen)
	}
	ev.locs = map[string]domain.Locator{}
	for _, n := range screens.Needed(scr, screen, needed) {
		ev.locs[n], _ = scr.Locator(screen, n)
	}
	ev.elem, ev.name, ev.hint = element, element, element
	_, own := scr.Screens[screen][element]
	ev.shared = !own
	if loc.Within != "" {
		if cl, ok := scr.Locator(screen, loc.Within); ok && cl.Within == "" {
			ev.container = &Container{Hint: loc.Within, Locator: cl}
			ev.containerName = loc.Within
			loc.Within = ""
		}
	}
	ev.locator = loc
	ev.cand = Candidate{Locator: loc, Container: ev.container}
	return nil
}

// loadCheck makes the check event of an expectation of a step. The expectation is kept as it is: its
// source, its by, and its cite stay, so that a check that a model wrote is not turned into one that a
// person wrote.
func loadCheck(scr domain.Screens, owner *event, st domain.Step, ex domain.Expectation, events []*event) (*event, error) {
	c := ex.Check
	switch {
	case owner.use != "":
		return nil, fmt.Errorf("step %s runs a flow and has a check: only a check on a step on the device can be reopened", st.ID)
	case c.Element == "" || c.Status != nil || c.JSONPath != "" || c.Header != "" || c.Page != "":
		return nil, fmt.Errorf("step %s: only a check of an element can be reopened", st.ID)
	case owner.screen == "":
		return nil, fmt.Errorf("step %s: a check needs the screen of its step", st.ID)
	}
	kept := ex
	ev := &event{
		id: uniqueEventID(events, "check-"+c.Element), screen: owner.screen, purpose: ForAssert, after: owner,
		assertion: &assertion{kind: kindOf(c), value: valueOf(c), attr: c.Attribute, expect: &kept},
	}
	if err := bindElement(scr, ev, owner.screen, c.Element, []string{c.Element}); err != nil {
		return nil, fmt.Errorf("step %s: %w", st.ID, err)
	}
	ev.text = checkText(c)
	return ev, nil
}

func uniqueEventID(events []*event, base string) string {
	id := base
	for i := 2; slices.ContainsFunc(events, func(e *event) bool { return e.id == id }); i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return id
}

func kindOf(c domain.Check) AssertKind {
	switch {
	case c.Exists != nil && *c.Exists:
		return AssertExists
	case c.Exists != nil:
		return AssertNotExists
	case c.Count != nil:
		return AssertCount
	case c.State != "":
		return AssertKind(c.State)
	case c.Attribute != "":
		return AssertAttribute
	case c.Contains != nil:
		return AssertContains
	case c.Equals != nil:
		return AssertEquals
	}
	return ""
}

func valueOf(c domain.Check) string {
	switch {
	case c.Count != nil:
		return fmt.Sprint(*c.Count)
	case c.Contains != nil:
		return fmt.Sprint(c.Contains)
	case c.Equals != nil:
		return fmt.Sprint(c.Equals)
	case c.NotEquals != nil:
		return fmt.Sprint(c.NotEquals)
	}
	return ""
}

// checkText is the sentence of a loaded check, for the timeline.
func checkText(c domain.Check) string {
	el := c.Element
	switch {
	case c.Exists != nil && *c.Exists:
		return el + " is on the screen"
	case c.Exists != nil:
		return el + " is not on the screen"
	case c.Count != nil:
		return fmt.Sprintf("%d of %s are on the screen", *c.Count, el)
	case c.State != "":
		if w, ok := domain.ElementStates[c.State]; ok {
			return el + " " + w
		}
	case c.Attribute != "":
		return el + " has " + c.Attribute + " " + quote(valueOf(c))
	case c.Contains != nil:
		return el + " contains " + quote(valueOf(c))
	case c.Equals != nil:
		return el + " equals " + quote(valueOf(c))
	case c.NotEquals != nil:
		return el + " does not equal " + quote(valueOf(c))
	}
	return el + " is checked"
}

// stepText is the sentence of a step of a loaded flow, which has none of its own.
func stepText(e *event) string {
	switch {
	case e.use != "":
		return "Run the flow " + e.use
	case e.do == domain.MobileLaunch:
		return "Start the app"
	case e.do == domain.MobileOpenLink:
		return "Open the link " + quote(e.link)
	case e.do == domain.MobilePress:
		return "Press " + e.value
	case e.do == domain.MobileType && e.secret != "":
		return "Type the secret " + e.secret + " in " + e.hint
	case e.do == domain.MobileType:
		return "Type " + quote(e.value) + " in " + e.hint
	}
	verb := map[domain.MobileOp]string{
		domain.MobileTap: "Tap", domain.MobileLongTap: "Press and hold", domain.MobileSwipe: "Swipe",
		domain.MobileDoubleTap: "Double-tap", domain.MobileDrag: "Drag",
		domain.MobileScroll: "Scroll to", domain.MobileWait: "Wait for",
	}[e.do]
	if verb == "" {
		verb = string(e.do)
	}
	if e.do == domain.MobileSwipe && e.hint == "" && e.value != "" {
		return swipeText(e.value)
	}
	text := verb
	if e.hint != "" {
		text += " " + e.hint
	}
	if (e.do == domain.MobileSwipe || e.do == domain.MobileScroll) && e.value != "" {
		text += " " + e.value
	}
	if e.do == domain.MobileDrag && e.to != nil {
		text += " to " + e.to.hint
	}
	return strings.TrimSpace(text)
}

// resumeLocked starts a reopened recording: it opens nothing new, it replays the steps up to ResumeAt, and
// it records no launch of its own. A secret or a param that the steps need and the session lacks is not
// fatal: the device is left as it is, the cursor is at 0, and a goto asks for the values.
func (s *Session) resumeLocked(ctx context.Context) (*Frame, error) {
	at := s.o.ResumeAt
	if at <= 0 || at > len(s.events) {
		at = len(s.events)
	}
	if s.device.Device != "" && !s.emulatorLocked() && !s.o.ConfirmClear && s.needsClearLocked(at) {
		_ = s.closeLocked(ctx)
		return nil, ErrNeedsConfirm
	}
	s.started = true
	s.at = 0
	s.known = nil
	secrets, params := s.missingSecretsLocked(at), s.missingParamsLocked(at)
	if len(secrets) > 0 || len(params) > 0 {
		f, err := s.refreshLocked(ctx)
		if err != nil {
			return nil, err
		}
		f.Notice = (&NeedsValues{Secrets: secrets, Params: params}).Error() + ": give them when you go to a step"
		return f, nil
	}
	f, err := s.gotoLocked(ctx, at)
	if err != nil {
		if f == nil {
			return nil, err
		}
		f.Notice = "the replay stopped: " + s.ov.Scrub(err.Error())
	}
	return f, nil
}
