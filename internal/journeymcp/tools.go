package journeymcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/journey"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
)

func textJSON(v any) (mcp.Result, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.Result{}, fmt.Errorf("encode the result: %w", err)
	}
	return mcp.TextResult(string(b)), nil
}

func args(call mcp.Call, dst any) error { return mcp.DecodeArgs(call.Args, dst) }

func (m *Manager) defs() []*toolDef {
	return []*toolDef{
		m.startDef(), m.addLegDef(), m.statusDef(), m.undoDef(), m.saveDef(), m.runDef(), m.discardDef(),
	}
}

const legHelp = "Work on a leg with the api_*, web_*, and mobile_* tools as you do for any task: while a draft is open, the first action on a surface or a service opens a leg, and a change of surface or service closes it and opens the next, which needs the one before. "

// ---- start ----

func (m *Manager) startDef() *toolDef {
	return &toolDef{
		name: "semar_journey_start", title: "Start a journey", write: true,
		desc: "Start a draft journey: a test that spans services and surfaces, such as an order that is placed by the API, paid on the web, and shown on a device. One draft at a time. " + legHelp +
			"Nothing is written until semar_journey_save, which also replays the journey once with no model and keeps it only if that replay passes.",
		schema: mcp.Object(map[string]mcp.Schema{
			"id":          mcp.String("The ID of the journey: letters, digits, underscore, or hyphen, no dot. It is the file name .semar/journeys/<id>.yaml."),
			"title":       mcp.String("One line that says what the journey proves."),
			"owner":       mcp.String("The product or QA owner, such as team-commerce."),
			"criticality": mcp.Enum("critical or standard.", "standard", "critical").With("default", "standard"),
			"authProfile": mcp.String("The auth profile that the journey acts as, from api_list_services. Optional."),
			"replace":     mcp.Bool("Allow the save to write over a journey of the same ID."),
		}).Required("id", "title", "owner"),
		run: m.start,
	}
}

func (m *Manager) start(_ context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Owner       string `json:"owner"`
		Criticality string `json:"criticality"`
		AuthProfile string `json:"authProfile"`
		Replace     bool   `json:"replace"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if m.d != nil {
		return mcp.Result{}, fmt.Errorf("the draft journey %s is open: save it with semar_journey_save or drop it with semar_journey_discard first", m.d.id)
	}
	if err := idcheck.Check("journey ID", a.ID); err != nil {
		return mcp.Result{}, err
	}
	if strings.Contains(a.ID, ".") {
		return mcp.Result{}, fmt.Errorf("id %s must not hold a dot: the scenario of a leg is named <journey>.<leg>", a.ID)
	}
	title := clean(a.Title)
	if title == "" {
		return mcp.Result{}, errors.New("title is needed: one line that says what the journey proves")
	}
	if err := idcheck.Check("owner", a.Owner); err != nil {
		return mcp.Result{}, err
	}
	crit := a.Criticality
	if crit == "" {
		crit = "standard"
	}
	if crit != "standard" && crit != "critical" {
		return mcp.Result{}, errors.New("criticality must be standard or critical")
	}
	if a.AuthProfile != "" {
		if err := idcheck.Check("auth profile name", a.AuthProfile); err != nil {
			return mcp.Result{}, err
		}
	}
	if m.o.Backend != nil && m.o.Backend.Exists(domain.JourneyID(a.ID)) && !a.Replace {
		return mcp.Result{}, fmt.Errorf("the journey %s exists already: start again with replace: true to write over it, or pick another id", a.ID)
	}
	m.d = &draft{
		id: domain.JourneyID(a.ID), title: title, owner: a.Owner, criticality: crit, authProfile: a.AuthProfile,
		replace: a.Replace, n: map[domain.Surface]int{}, author: mcpkit.AgentAuthor(call.Client.Name),
	}
	return textJSON(map[string]any{
		"journey": a.ID, "title": title, "criticality": crit,
		"next": "Do the work with the api_*, web_*, and mobile_* tools, or name a leg first with semar_journey_add_leg. Then save with semar_journey_save.",
	})
}

// ---- add_leg ----

func (m *Manager) addLegDef() *toolDef {
	return &toolDef{
		name: "semar_journey_add_leg", title: "Open a leg of the journey", write: true,
		desc: "Close the open leg and open a new one with a name, a surface, and a service. You need it to give a leg its ID, an auth profile (as), a wait, or more than the previous leg in needs, and for an android leg, which needs its service named. " +
			"A leg needs the previous leg unless needs says otherwise, and may use {{<leg>.<name>}} for the captures that a needed leg made. An android leg records its steps as a flow, from a fresh start of the app, when the first mobile_* action runs.",
		schema: mcp.Object(map[string]mcp.Schema{
			"id":      mcp.String("The ID of the leg: lower case letters, digits, and underscore, starting with a letter, such as place or pay_order."),
			"surface": mcp.Enum("The tools that the leg is made with.", "api", "web", "android"),
			"service": mcp.String("The service of the environment."),
			"app":     mcp.String("The screens file of a web or an android leg, .semar/screens/<app>.yaml. The service name by default."),
			"as":      mcp.String("The auth profile that the steps of the leg act as."),
			"needs":   mcp.Array("The legs that this leg reads the outputs of. The previous leg by default; an empty list means none.", mcp.String("A leg ID.")),
			"wait": mcp.Object(map[string]mcp.Schema{
				"jsonpath":   mcp.String("A JSONPath into the body of the last request of the leg."),
				"equals":     mcp.String("The value that ends the wait. A number, true, false, or null written as such is compared as that JSON value."),
				"timeoutSec": mcp.Int("How long to wait at most, in seconds.").With("minimum", 1).With("maximum", MaxWaitSec).With("default", DefaultWaitSec),
				"everySec":   mcp.Number("How often to repeat the last request, in seconds. 1 by default.").With("minimum", 0.1),
			}).Required("jsonpath", "equals"),
		}).Required("id", "surface", "service"),
		run: m.addLeg,
	}
}

func (m *Manager) addLeg(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		ID      string   `json:"id"`
		Surface string   `json:"surface"`
		Service string   `json:"service"`
		App     string   `json:"app"`
		As      string   `json:"as"`
		Needs   []string `json:"needs"`
		Wait    *struct {
			JSONPath   string  `json:"jsonpath"`
			Equals     string  `json:"equals"`
			TimeoutSec int     `json:"timeoutSec"`
			EverySec   float64 `json:"everySec"`
		} `json:"wait"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	d, err := m.needDraft()
	if err != nil {
		return mcp.Result{}, err
	}
	id := domain.LegID(a.ID)
	if !legIDPattern.MatchString(a.ID) {
		return mcp.Result{}, fmt.Errorf("the leg id %q must be lower case letters, digits, and underscore, starting with a letter", a.ID)
	}
	if d.taken(id) {
		return mcp.Result{}, fmt.Errorf("the leg %s exists already", a.ID)
	}
	surface := domain.Surface(a.Surface)
	if surface != domain.SurfaceAPI && surface != domain.SurfaceWeb && surface != domain.SurfaceAndroid {
		return mcp.Result{}, errors.New("surface must be api, web, or android")
	}
	if strings.TrimSpace(a.Service) == "" {
		return mcp.Result{}, errors.New("service is needed: the name of a service of the environment")
	}
	if err := idcheck.Check("service name", a.Service); err != nil {
		return mcp.Result{}, err
	}
	if a.App != "" {
		if surface == domain.SurfaceAPI {
			return mcp.Result{}, errors.New("app is for a web or an android leg: an api leg has no screens file")
		}
		if err := idcheck.Check("app name", a.App); err != nil {
			return mcp.Result{}, err
		}
	}
	if a.As != "" {
		if err := idcheck.Check("auth profile name", a.As); err != nil {
			return mcp.Result{}, err
		}
	}
	var wait *domain.LegWait
	if a.Wait != nil {
		if surface != domain.SurfaceAPI {
			return mcp.Result{}, errors.New("wait is for an api leg: it repeats the last request until the answer holds. A web or android leg waits with a check on an element")
		}
		w, err := legWait(a.Wait.JSONPath, a.Wait.Equals, a.Wait.TimeoutSec, a.Wait.EverySec)
		if err != nil {
			return mcp.Result{}, err
		}
		wait = w
	}
	known := map[domain.LegID]bool{}
	for _, l := range d.legs {
		known[l.leg.ID] = true
	}
	if d.cur != nil {
		known[d.cur.id] = true
	}
	var needs []domain.LegID
	for _, n := range a.Needs {
		if !known[domain.LegID(n)] {
			return mcp.Result{}, fmt.Errorf("needs names %q, which is not a leg of this draft", n)
		}
		if !slices.Contains(needs, domain.LegID(n)) {
			needs = append(needs, domain.LegID(n))
		}
	}
	dropped, err := m.closeCurLocked(ctx, d)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("the open leg could not be closed, so no leg was opened: %w", err)
	}
	if a.Needs == nil {
		needs = d.previous()
	}
	l := &openLeg{id: id, surface: surface, service: a.Service, app: a.App, as: a.As, needs: needs, wait: wait}
	if err := m.openLegLocked(ctx, d, l); err != nil {
		return mcp.Result{}, err
	}
	out := map[string]any{"leg": a.ID, "surface": a.Surface, "service": a.Service, "needs": needs}
	if dropped != "" {
		out["note"] = dropped
	}
	if l.buildNote != "" {
		out["warning"] = l.buildNote
	}
	return textJSON(out)
}

// legWait makes the wait of a leg from the arguments of the tool.
func legWait(path, equals string, timeoutSec int, everySec float64) (*domain.LegWait, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("wait.jsonpath is needed")
	}
	if timeoutSec == 0 {
		timeoutSec = DefaultWaitSec
	}
	if timeoutSec < 1 || timeoutSec > MaxWaitSec {
		return nil, fmt.Errorf("wait.timeoutSec is 1 to %d seconds", MaxWaitSec)
	}
	if everySec == 0 {
		everySec = 1
	}
	if everySec < 0.1 || everySec > float64(timeoutSec) {
		return nil, errors.New("wait.everySec is from 0.1 up to the timeout")
	}
	return &domain.LegWait{
		Until:   domain.LegUntil{JSONPath: path, Equals: operand(equals)},
		Timeout: time.Duration(timeoutSec) * time.Second,
		Every:   time.Duration(everySec * float64(time.Second)),
	}, nil
}

// operand reads a number, true, false, or null written as such as that JSON value, and any other text as
// text.
func operand(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		switch x := v.(type) {
		case float64:
			if x == float64(int64(x)) {
				return int(x)
			}
			return x
		case bool, nil:
			return x
		}
	}
	return s
}

// ---- status ----

func (m *Manager) statusDef() *toolDef {
	return &toolDef{
		name: "semar_journey_status", title: "Show the draft journey",
		desc:   "Show the draft journey as it would be written: the YAML of the closed legs and of the open api or web leg, the problems that the journey rules find, and how many steps each leg has. It writes nothing.",
		schema: mcp.Object(nil),
		run:    m.status,
	}
}

func (m *Manager) status(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	if err := mcp.DecodeArgs(call.Args, &struct{}{}); err != nil {
		return mcp.Result{}, err
	}
	d, err := m.needDraft()
	if err != nil {
		return mcp.Result{}, err
	}
	legs := make([]map[string]any, 0, len(d.legs)+1)
	for _, l := range d.legs {
		legs = append(legs, legSummary(l.leg, "closed"))
	}
	view := d.legs
	out := map[string]any{"journey": d.id, "title": d.title}
	if c := d.cur; c != nil {
		entry := map[string]any{"id": c.id, "surface": c.surface, "service": c.service, "state": "open", "needs": c.needs}
		switch c.surface {
		case domain.SurfaceAPI:
			entry["steps"] = len(m.o.API.Since(c.apiMark))
		case domain.SurfaceWeb:
			entry["steps"] = len(m.o.Web.EventsFrom(c.webMark))
		case domain.SurfaceAndroid:
			if c.flowStarted {
				entry["steps"] = m.o.Mobile.LegFlowSteps()
				entry["recording"] = "the steps are recorded as the flow " + c.flowID
			} else {
				entry["steps"] = 0
			}
		}
		legs = append(legs, entry)
		// A peek at an api or a web leg shows what closing it would write. It changes nothing.
		switch c.surface {
		case domain.SurfaceAPI:
			if built, err := m.buildAPIPeek(d, c); err == nil {
				view = append(slices.Clone(view), built)
			}
		case domain.SurfaceWeb:
			if built, err := m.buildWeb(ctx, d, c); err == nil {
				view = append(slices.Clone(view), built)
			}
		}
	}
	out["legs"] = legs
	if len(view) > 0 {
		j := assemble(d, view, nil)
		if data, err := journey.Marshal(j); err == nil {
			out["yaml"] = string(data)
		} else {
			out["yamlError"] = err.Error()
		}
		if m.o.Backend != nil {
			ps := m.o.Backend.Validate(j, pendingOf(view))
			out["problems"] = problemLines(ps)
		}
	}
	return textJSON(out)
}

func legSummary(l domain.Leg, state string) map[string]any {
	return map[string]any{"id": l.ID, "surface": l.Surface, "service": l.Service, "state": state, "steps": len(l.Steps), "needs": l.Needs, "outputs": l.Outputs}
}

// buildAPIPeek makes the leg from the log without setting variables.
func (m *Manager) buildAPIPeek(d *draft, l *openLeg) (*closedLeg, error) {
	return m.buildAPI(d, l, false)
}

func problemLines(ps []journey.Problem) []string {
	out := make([]string, 0, min(len(ps), MaxShown+1))
	for i, p := range ps {
		if i == MaxShown {
			out = append(out, fmt.Sprintf("... and %d more", len(ps)-MaxShown))
			break
		}
		out = append(out, clean(p.String()))
	}
	return out
}

// ---- undo_step ----

func (m *Manager) undoDef() *toolDef {
	return &toolDef{
		name: "semar_journey_undo_step", title: "Undo the last step of the open leg", write: true,
		desc:   "Drop the last step of the open leg. An api step is removed from the log with the variables that it captured. An android step is removed from the recorded flow. A web step cannot be removed one by one. A closed leg cannot be changed.",
		schema: mcp.Object(nil),
		run:    m.undo,
	}
}

func (m *Manager) undo(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	if err := mcp.DecodeArgs(call.Args, &struct{}{}); err != nil {
		return mcp.Result{}, err
	}
	d, err := m.needDraft()
	if err != nil {
		return mcp.Result{}, err
	}
	c := d.cur
	if c == nil {
		return mcp.Result{}, errors.New("there is no open leg: a closed leg cannot be changed. Drop the whole draft with semar_journey_discard, or go on with a new leg")
	}
	switch c.surface {
	case domain.SurfaceAPI:
		if len(m.o.API.Since(c.apiMark)) == 0 || !m.o.API.DropLast() {
			return mcp.Result{}, fmt.Errorf("the leg %s has no step to undo", c.id)
		}
		return textJSON(map[string]any{"leg": c.id, "undone": "the last request", "steps": len(m.o.API.Since(c.apiMark))})
	case domain.SurfaceWeb:
		return mcp.Result{}, fmt.Errorf("a web step cannot be undone: the page has moved on. Drop the draft with semar_journey_discard and record the journey again, or go on and fix it with a later step. The leg %s is kept as it is", c.id)
	}
	if !c.flowStarted {
		return mcp.Result{}, fmt.Errorf("the leg %s has no step to undo", c.id)
	}
	if err := m.o.Mobile.UndoLegStep(ctx); err != nil {
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{"leg": c.id, "undone": "the last step", "steps": m.o.Mobile.LegFlowSteps()})
}

// ---- discard ----

func (m *Manager) discardDef() *toolDef {
	return &toolDef{
		name: "semar_journey_discard", title: "Drop the draft journey", write: true, destructive: true,
		desc:   "Drop the draft journey and the recording of an open android leg. Nothing was written, so no file changes. The variables that the api tools kept stay.",
		schema: mcp.Object(nil),
		run:    m.discard,
	}
}

func (m *Manager) discard(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	if err := mcp.DecodeArgs(call.Args, &struct{}{}); err != nil {
		return mcp.Result{}, err
	}
	d, err := m.needDraft()
	if err != nil {
		return mcp.Result{}, err
	}
	var derr error
	if d.cur != nil && d.cur.flowStarted {
		derr = m.o.Mobile.DiscardLegFlow(ctx)
	}
	m.d = nil
	if derr != nil {
		return mcp.Result{}, fmt.Errorf("the draft %s is dropped, but its android recording did not end cleanly: %w", d.id, derr)
	}
	return mcp.TextResult("the draft journey " + string(d.id) + " is dropped. Nothing was written."), nil
}

// Close drops the draft and any recording that it holds. The command calls it when the server ends.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	if m.d != nil && m.d.cur != nil && m.d.cur.flowStarted && m.o.Mobile != nil {
		err = m.o.Mobile.DiscardLegFlow(ctx)
	}
	m.d = nil
	return err
}
