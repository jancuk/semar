package journeymcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/screens"
)

// errEmptyLeg is what a builder says of a leg in which nothing was done.
var errEmptyLeg = errors.New("the leg has no step")

// MaxLegSteps is the most steps that a leg may hold, as the journey rules say.
const MaxLegSteps = 20

// buildLeg turns what was recorded into a leg. It changes nothing but the variables of the API tools,
// and only when it succeeds.
func (m *Manager) buildLeg(ctx context.Context, d *draft, l *openLeg) (*closedLeg, error) {
	switch l.surface {
	case domain.SurfaceAPI:
		return m.buildAPI(d, l, true)
	case domain.SurfaceWeb:
		return m.buildWeb(ctx, d, l)
	case domain.SurfaceAndroid:
		return m.buildAndroid(ctx, d, l)
	}
	return nil, fmt.Errorf("surface %q is not known", l.surface)
}

func baseLeg(l *openLeg) domain.Leg {
	return domain.Leg{
		ID: l.id, Surface: l.surface, Service: l.service, App: l.app, As: l.as,
		Needs: slices.Clone(l.needs), Wait: l.wait,
	}
}

var stepSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slug is a step ID of the words: lower case, hyphens, at most 40 characters.
func slug(s string) string {
	s = strings.Trim(stepSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "step"
	}
	return s
}

func uniqueID(seen map[string]int, base string) string {
	seen[base]++
	if n := seen[base]; n > 1 {
		return fmt.Sprintf("%s-%d", base, n)
	}
	return base
}

// ---- api ----

func (m *Manager) buildAPI(d *draft, l *openLeg, setVars bool) (*closedLeg, error) {
	steps := m.o.API.Since(l.apiMark)
	if len(steps) == 0 {
		return nil, errEmptyLeg
	}
	if len(steps) > MaxLegSteps {
		return nil, fmt.Errorf("leg %s has %d steps: at most %d. Split the work into more legs with semar_journey_add_leg", l.id, len(steps), MaxLegSteps)
	}
	leg := baseLeg(l)
	reach := d.reach(l.needs)
	own := map[string]bool{}
	ids := map[string]int{}
	var outputs []string
	for _, s := range steps {
		req := s.Request
		req.Service = ""
		q := qualifier{own: own, reach: reach}
		req = q.request(req)
		action := clean(s.Action)
		if action == "" {
			action = req.Method + " " + req.Path
		}
		stepID := uniqueID(ids, slug(firstNonEmpty(clean(s.Action), req.Method+" "+req.Path)))
		expect := slices.Clone(s.Expect)
		if len(expect) == 0 {
			status := s.Status
			expect = []domain.Expectation{{Check: domain.Check{Status: &status}, Source: domain.SourceBaseline, By: "model"}}
		}
		for i := range expect {
			expect[i].Check = q.check(expect[i].Check)
			if expect[i].By == "" {
				expect[i].By = "model"
			}
		}
		as := s.As
		if as == l.as {
			as = ""
		}
		leg.Steps = append(leg.Steps, domain.LegStep{
			ID: domain.StepID(stepID), Action: action,
			Hint:    &domain.RequestHint{Method: req.Method, Path: hintPath(req.Path)},
			Request: req, Expect: expect, Captures: slices.Clone(s.Captures), As: as,
		})
		for _, c := range s.Captures {
			own[c.Name] = true
			if !slices.Contains(outputs, c.Name) {
				outputs = append(outputs, c.Name)
			}
		}
	}
	leg.Outputs = outputs
	// The variables of the leg are known to the tools under <leg>.<name>, so a later request may use them.
	if setVars {
		vars := m.o.API.Vars()
		for _, name := range outputs {
			if v, ok := vars[name]; ok {
				m.o.API.SetVar(string(l.id)+"."+name, v)
			}
		}
	}
	return &closedLeg{leg: leg, from: l}, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

var (
	wholeVariable = regexp.MustCompile(`^\{\{[A-Za-z][A-Za-z0-9_]*(?:\.([A-Za-z0-9_]+))?\}\}$`)
	variableRef   = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]*)\}\}`)
)

// hintPath is the template of a request path: each segment that is one variable is a {param}.
func hintPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if m := wholeVariable.FindStringSubmatch(s); m != nil {
			name := m[1]
			if name == "" {
				name = strings.Trim(s, "{}")
			}
			segs[i] = "{" + name + "}"
		}
	}
	return strings.Join(segs, "/")
}

// qualifier turns a {{name}} that a leg did not capture itself into {{leg.name}} of the leg that it needs
// and that has the output. A leg reads the outputs of other legs only under their names.
type qualifier struct {
	own   map[string]bool
	reach []*closedLeg
}

func (q qualifier) text(s string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return variableRef.ReplaceAllStringFunc(s, func(ref string) string {
		name := strings.Trim(ref, "{}")
		if q.own[name] {
			return ref
		}
		// The nearest leg first.
		for i := len(q.reach) - 1; i >= 0; i-- {
			if slices.Contains(q.reach[i].leg.Outputs, name) {
				return "{{" + string(q.reach[i].leg.ID) + "." + name + "}}"
			}
		}
		return ref
	})
}

func (q qualifier) value(v any) any {
	switch x := v.(type) {
	case string:
		return q.text(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = q.value(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = q.value(e)
		}
		return out
	}
	return v
}

func (q qualifier) request(r domain.APIRequest) domain.APIRequest {
	r.Path = q.text(r.Path)
	if r.Query != nil {
		qq := make(map[string]string, len(r.Query))
		for k, v := range r.Query {
			qq[k] = q.text(v)
		}
		r.Query = qq
	}
	if r.Headers != nil {
		hh := make(map[string]string, len(r.Headers))
		for k, v := range r.Headers {
			hh[k] = q.text(v)
		}
		r.Headers = hh
	}
	if r.JSON != nil {
		r.JSON = q.value(r.JSON)
	}
	return r
}

func (q qualifier) check(c domain.Check) domain.Check {
	c.Equals, c.NotEquals, c.Contains = q.value(c.Equals), q.value(c.NotEquals), q.value(c.Contains)
	return c
}

// ---- web ----

func (m *Manager) buildWeb(ctx context.Context, d *draft, l *openLeg) (*closedLeg, error) {
	events := m.o.Web.EventsFrom(l.webMark)
	if len(events) == 0 {
		return nil, errEmptyLeg
	}
	// A leg that began while the window showed a page has no open of its own: the leg opens that page.
	if l.webHad && l.webURL != "" && events[0].Kind != record.KindOpen {
		events = append([]record.Event{{Kind: record.KindOpen, Page: record.Page{URL: l.webURL}}}, events...)
	}
	vars := m.legVars(d, l)
	var existing domain.Screens
	if m.o.Screens != nil {
		existing = m.o.Screens(l.app)
	}
	existing = withAdds(existing, l.app, d.pendingAdds(l.app))
	base := ""
	if m.o.Web != nil {
		base = m.o.Web.BaseURL(l.service)
	}
	dr, err := record.Build(events, record.Options{
		ID: domain.ScenarioID(string(d.id) + "." + string(l.id)), Ticket: string(d.id), App: l.app, Service: l.service,
		BaseURL: base, AuthProfile: l.as, Values: record.NewValues(vars), Existing: existing, ByModel: true, Date: m.clock(), Author: &d.author,
	})
	switch {
	case errors.Is(err, record.ErrNoCheck):
		return nil, fmt.Errorf("the web leg %s has no check, so it proves nothing: record one with web_assert on an element that shows the result, then close the leg again", l.id)
	case errors.Is(err, record.ErrSecretName):
		return nil, fmt.Errorf("a password was typed in the web leg %s: a journey signs in with an auth profile, so give the leg as: <profile> and do not type the password", l.id)
	case err != nil:
		return nil, fmt.Errorf("the web leg %s cannot be written: %w", l.id, err)
	}
	if len(dr.Script.Steps) != len(dr.Scenario.Steps) {
		return nil, fmt.Errorf("the web leg %s was built with %d steps and %d actions", l.id, len(dr.Scenario.Steps), len(dr.Script.Steps))
	}
	if len(dr.Script.Steps) > MaxLegSteps {
		return nil, fmt.Errorf("the web leg %s has %d steps: at most %d. Split the work into more legs with semar_journey_add_leg", l.id, len(dr.Script.Steps), MaxLegSteps)
	}
	leg := baseLeg(l)
	for i, st := range dr.Scenario.Steps {
		ss := dr.Script.Steps[i]
		if ss.Web == nil {
			return nil, fmt.Errorf("the web leg %s: step %s has no browser action", l.id, st.ID)
		}
		w := *ss.Web
		w.Service, w.App = "", ""
		leg.Steps = append(leg.Steps, domain.LegStep{ID: st.ID, Setup: st.Setup, Action: st.Action, Expect: st.Expect, Web: &w})
	}
	var files []ScreensFile
	if len(dr.Added) > 0 {
		files = []ScreensFile{{App: l.app, Adds: dr.Added}}
	}
	return &closedLeg{leg: leg, files: files, from: l, note: dr.Notes}, nil
}

// legVars are the values that a typed text is lifted back to: the outputs of the legs that this leg needs,
// as {{leg.name}}, and what the browser used.
func (m *Manager) legVars(d *draft, l *openLeg) map[string]string {
	vars := map[string]string{}
	if m.o.Web != nil {
		maps.Copy(vars, m.o.Web.Vars())
	}
	if m.o.API != nil {
		all := m.o.API.Vars()
		for _, c := range d.reach(l.needs) {
			for _, name := range c.leg.Outputs {
				key := string(c.leg.ID) + "." + name
				if v, ok := all[key]; ok {
					vars[key] = v
				}
			}
		}
	}
	return vars
}

// pendingAdds are what the closed legs add to a screens file, so that a later leg names elements the same way.
func (d *draft) pendingAdds(app string) []screens.Addition {
	var out []screens.Addition
	for _, l := range d.legs {
		for _, f := range l.files {
			if f.App == app {
				out = append(out, f.Adds...)
			}
		}
	}
	return out
}

// withAdds is the screens as they would be once the additions are written.
func withAdds(s domain.Screens, app string, adds []screens.Addition) domain.Screens {
	if len(adds) == 0 {
		return s
	}
	out := domain.Screens{APIVersion: "semar/v1", Kind: "Screens", App: app, Common: maps.Clone(s.Common), Screens: map[string]map[string]domain.Locator{}}
	for name, els := range s.Screens {
		out.Screens[name] = maps.Clone(els)
	}
	for _, a := range adds {
		for _, e := range a.Elements {
			if a.Common {
				if out.Common == nil {
					out.Common = map[string]domain.Locator{}
				}
				out.Common[e.Name] = e.Locator
				continue
			}
			if out.Screens[a.Screen] == nil {
				out.Screens[a.Screen] = map[string]domain.Locator{}
			}
			out.Screens[a.Screen][e.Name] = e.Locator
		}
	}
	return out
}

// ---- android ----

func (m *Manager) buildAndroid(ctx context.Context, d *draft, l *openLeg) (*closedLeg, error) {
	tap := m.o.Mobile
	leg := baseLeg(l)
	switch {
	case l.flowStarted:
		lf, err := tap.EndLegFlow(ctx)
		if err != nil {
			return nil, fmt.Errorf("the android leg %s could not be closed: %w", l.id, err)
		}
		if lf.Steps == 0 {
			return nil, errEmptyLeg
		}
		l.flowStarted = false
		leg.App = firstNonEmpty(l.app, lf.App, l.service)
		leg.Steps = []domain.LegStep{{ID: "run", Action: "Run the recorded steps of " + string(l.id), Use: lf.Flow.ID}}
		// The checks come after the flow, each on a step that waits for its element.
		for _, c := range lf.Checks {
			leg.Steps = append(leg.Steps, domain.LegStep{
				ID: domain.StepID(c.ID), Action: c.Action, Expect: slices.Clone(c.Expect),
				Mobile: &domain.MobileAction{Do: domain.MobileWait, Screen: c.Screen, Element: c.Element},
			})
		}
		leg.Outputs = slices.Clone(lf.Captures)
		out := &closedLeg{leg: leg, flows: []domain.Flow{lf.Flow}, note: lf.Notes}
		if len(lf.Added) > 0 {
			out.files = []ScreensFile{{App: leg.App, Adds: lf.Added}}
		}
		return out, nil
	}
	return nil, errEmptyLeg
}
