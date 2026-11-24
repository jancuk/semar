package journeymcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/journey"
	"github.com/jancuk/semar/internal/journeymcp"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/record"
)

// ---- fakes ----

type fakeAPI struct {
	mu    sync.Mutex
	steps []journeymcp.APIStep
	vars  map[string]string
	// before is what each step changed, for DropLast.
	sets []map[string]string
}

func newFakeAPI() *fakeAPI { return &fakeAPI{vars: map[string]string{}} }

func (f *fakeAPI) Seq() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.steps) }
func (f *fakeAPI) Since(i int) []journeymcp.APIStep {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.steps) {
		return nil
	}
	return slices.Clone(f.steps[i:])
}
func (f *fakeAPI) DropLast() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.steps) == 0 {
		return false
	}
	f.steps = f.steps[:len(f.steps)-1]
	return true
}
func (f *fakeAPI) Vars() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.vars)
}
func (f *fakeAPI) SetVar(k, v string) { f.mu.Lock(); defer f.mu.Unlock(); f.vars[k] = v }

// send logs a request as the api_request tool would.
func (f *fakeAPI) send(s journeymcp.APIStep, captured map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, s)
	maps.Copy(f.vars, captured)
}

type fakeWeb struct {
	mu      sync.Mutex
	open    bool
	url     string
	service string
	def     string
	events  []record.Event
	vars    map[string]string
}

func (f *fakeWeb) Mark(context.Context) (int, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.open {
		return 0, "", journeymcp.ErrNoWindow
	}
	return len(f.events), f.url, nil
}
func (f *fakeWeb) EventsFrom(i int) []record.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.events) {
		return nil
	}
	return slices.Clone(f.events[i:])
}
func (f *fakeWeb) Vars() map[string]string { return maps.Clone(f.vars) }
func (f *fakeWeb) Service() string         { return f.service }
func (f *fakeWeb) DefaultService() string  { return f.def }
func (f *fakeWeb) BaseURL(string) string   { return "http://shop.test" }
func (f *fakeWeb) add(ev ...record.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.open = true
	f.events = append(f.events, ev...)
}

type fakeMobile struct {
	mu        sync.Mutex
	started   []string
	steps     int
	flow      domain.Flow
	checks    []journeymcp.LegCheck
	foreign   string
	fgErr     error
	discarded int
	undone    int
	screens   int
	screenErr error
}

const legScreenText = "screen 1080x2400, 2 elements, called Welcome\nwindow app com.example.shop\ne1 button \"Sign in\""

// LegScreen lists a fixed screen and counts the calls.
func (f *fakeMobile) LegScreen(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.screens++
	if f.screenErr != nil {
		return "", f.screenErr
	}
	return legScreenText, nil
}

func (f *fakeMobile) StartLegFlow(_ context.Context, id, service, ticket string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, id+"@"+service+"#"+ticket)
	return nil
}
func (f *fakeMobile) EndLegFlow(context.Context) (journeymcp.LegFlow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl := f.flow
	fl.ID = strings.SplitN(f.started[len(f.started)-1], "@", 2)[0]
	return journeymcp.LegFlow{Flow: fl, App: "shop", Steps: f.steps, Captures: []string{"code"}, Checks: f.checks}, nil
}
func (f *fakeMobile) DiscardLegFlow(context.Context) error { f.discarded++; return nil }
func (f *fakeMobile) UndoLegStep(context.Context) error    { f.undone++; f.steps--; return nil }
func (f *fakeMobile) LegFlowSteps() int                    { return f.steps }
func (f *fakeMobile) ForegroundService(context.Context) (string, error) {
	return f.foreign, f.fgErr
}

type fakeBackend struct {
	mu       sync.Mutex
	existing map[domain.JourneyID]bool
	problems []journey.Problem
	outcome  journeymcp.RunOutcome
	runErr   error
	wrote    []domain.Journey
	pending  []journeymcp.Pending
	undone   int
	runs     []domain.JourneyID
	// buildProblem is what BuildProblem answers for every service.
	buildProblem string
}

func (b *fakeBackend) BuildProblem(context.Context, string) string { return b.buildProblem }

func (b *fakeBackend) Validate(j domain.Journey, p journeymcp.Pending) []journey.Problem {
	if b.problems != nil {
		return b.problems
	}
	// A flow that the draft writes is looked up in the pending files, and any other flow is one that is saved.
	flows := func(id string) (domain.Flow, error) {
		for _, f := range p.Flows {
			if f.ID == id {
				return f, nil
			}
		}
		return domain.Flow{APIVersion: "semar/v1", Kind: "Flow", ID: id, Surface: domain.SurfaceAndroid, Service: "shop", App: "shop",
			Params: []domain.FlowParam{{Name: "user"}},
			Steps:  []domain.ScriptStep{{Step: "go", Mobile: &domain.MobileAction{Do: domain.MobileLaunch}}}}, nil
	}
	return journey.ValidateWithFlows(j, nil, nil, flows)
}
func (b *fakeBackend) Write(j domain.Journey, p journeymcp.Pending, _ bool) (*journeymcp.Saved, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.wrote = append(b.wrote, j)
	b.pending = append(b.pending, p)
	return &journeymcp.Saved{Files: []string{journey.Path(j.ID)}, Undo: func() error { b.undone++; return nil }}, nil
}
func (b *fakeBackend) Run(_ context.Context, id domain.JourneyID) (journeymcp.RunOutcome, error) {
	b.runs = append(b.runs, id)
	return b.outcome, b.runErr
}
func (b *fakeBackend) Exists(id domain.JourneyID) bool { return b.existing[id] }

// rig is a Manager with its fakes, and the tools by name.
type rig struct {
	m     *journeymcp.Manager
	api   *fakeAPI
	web   *fakeWeb
	mob   *fakeMobile
	be    *fakeBackend
	tools map[string]mcp.Tool
	mw    mcp.Middleware
}

func newRig(t *testing.T, g mcpkit.Guard) *rig {
	t.Helper()
	r := &rig{api: newFakeAPI(), web: &fakeWeb{vars: map[string]string{}}, mob: &fakeMobile{foreign: "shop"}, be: &fakeBackend{existing: map[domain.JourneyID]bool{}, outcome: journeymcp.RunOutcome{Outcome: "passed"}}}
	r.mob.flow = domain.Flow{APIVersion: "semar/v1", Kind: "Flow", Surface: domain.SurfaceAndroid, Service: "shop", App: "shop",
		Steps: []domain.ScriptStep{
			{Step: "start", Mobile: &domain.MobileAction{Do: domain.MobileLaunch}},
			{Step: "read", Mobile: &domain.MobileAction{Do: domain.MobileTap, Screen: "Home", Element: "ok"},
				Captures: []domain.Capture{{Name: "code", Element: "code"}}}}}
	r.m = journeymcp.New(journeymcp.Options{Guard: g, API: r.api, Web: r.web, Mobile: r.mob, Backend: r.be})
	r.tools = map[string]mcp.Tool{}
	for _, tl := range r.m.Tools() {
		r.tools[tl.Name] = tl
	}
	r.mw = r.m.AutoLegMiddleware()
	return r
}

// call runs a journey tool.
func (r *rig) call(t *testing.T, name string, args any) mcp.Result {
	t.Helper()
	tool, ok := r.tools[name]
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	raw, _ := json.Marshal(args)
	res, err := tool.Handler(context.Background(), mcp.Call{Name: name, Args: raw})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func text(res mcp.Result) string {
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func (r *rig) ok(t *testing.T, name string, args any) string {
	t.Helper()
	res := r.call(t, name, args)
	if res.IsError {
		t.Fatalf("%s failed: %s", name, text(res))
	}
	return text(res)
}

// act runs the middleware for a surface tool, and then do, which stands for the tool itself.
func (r *rig) act(t *testing.T, tool mcp.Tool, args any, do func()) mcp.Result {
	t.Helper()
	raw, _ := json.Marshal(args)
	h := r.mw(tool, func(context.Context, mcp.Call) (mcp.Result, error) {
		if do != nil {
			do()
		}
		return mcp.TextResult("done"), nil
	})
	res, err := h(context.Background(), mcp.Call{Name: tool.Name, Args: raw})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

var (
	apiTool    = mcp.Tool{Name: "api_request", Surface: "api"}
	webOpen    = mcp.Tool{Name: "web_open", Surface: "web"}
	webClick   = mcp.Tool{Name: "web_click", Surface: "web", Write: true}
	webList    = mcp.Tool{Name: "web_list_elements", Surface: "web"}
	mobTap     = mcp.Tool{Name: "mobile_click_on_screen_at_coordinates", Surface: "mobile", Write: true}
	mobList    = mcp.Tool{Name: "mobile_list_elements_on_screen", Surface: "mobile"}
	runFlowToo = mcp.Tool{Name: "semar_run_flow", Surface: "mobile", Write: true}
)

func status(code int) *int { return &code }

func create(path string) journeymcp.APIStep {
	return journeymcp.APIStep{
		Service: "orders", Action: "Create an order",
		Request:  domain.APIRequest{Service: "orders", Method: "POST", Path: path, JSON: map[string]any{"amount": float64(1000)}},
		Captures: []domain.Capture{{Name: "id", JSONPath: "$.data.id"}},
		Status:   201,
	}
}

func pay() journeymcp.APIStep {
	return journeymcp.APIStep{
		Service: "payments", Action: "Pay the order", Status: 202,
		// The model sent the literal id, and the API tool lifted it to {{id}}: the bare name of the capture.
		Request: domain.APIRequest{Service: "payments", Method: "POST", Path: "/v1/payments", JSON: map[string]any{"orderId": "{{id}}"}},
		Expect:  []domain.Expectation{{Check: domain.Check{Status: status(202)}, Source: domain.SourceBaseline, By: "model"}},
	}
}

func startJourney(t *testing.T, r *rig) {
	t.Helper()
	r.ok(t, "semar_journey_start", map[string]any{"id": "checkout", "title": "An order is placed and paid", "owner": "team-commerce"})
}

// ---- tests ----

func TestToolsAreJourneySurfaceAndPortable(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	want := []string{"semar_journey_start", "semar_journey_add_leg", "semar_journey_status", "semar_journey_undo_step", "semar_journey_save", "semar_journey_run", "semar_journey_discard"}
	var got []string
	for _, tl := range r.m.Tools() {
		got = append(got, tl.Name)
		if tl.Surface != "journey" {
			t.Errorf("%s: surface %q", tl.Name, tl.Surface)
		}
		if err := mcp.ValidatePortableSchema(tl.Input); err != nil {
			t.Errorf("%s: %v", tl.Name, err)
		}
		wantWrite := tl.Name != "semar_journey_status"
		if tl.Write != wantWrite || tl.Annotations.ReadOnly == nil || *tl.Annotations.ReadOnly == wantWrite {
			t.Errorf("%s: write %v, annotations %+v", tl.Name, tl.Write, tl.Annotations)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
	props := r.tools["semar_journey_start"].Input["properties"].(map[string]mcp.Schema)
	if props["criticality"]["default"] != "standard" {
		t.Errorf("criticality default = %v", props["criticality"]["default"])
	}
	if r.tools["semar_journey_save"].Input["properties"].(map[string]mcp.Schema)["run"]["default"] != true {
		t.Error("run must default to true")
	}
}

func TestOneDraftAtATime(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	res := r.call(t, "semar_journey_start", map[string]any{"id": "other", "title": "x", "owner": "o"})
	if !res.IsError || !strings.Contains(text(res), "checkout") {
		t.Fatalf("a second start = %s", text(res))
	}
	r.ok(t, "semar_journey_discard", nil)
	r.ok(t, "semar_journey_start", map[string]any{"id": "other", "title": "x", "owner": "o"})
}

func TestStartRefusesAnExistingJourneyWithoutReplace(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.be.existing["checkout"] = true
	res := r.call(t, "semar_journey_start", map[string]any{"id": "checkout", "title": "t", "owner": "o"})
	if !res.IsError || !strings.Contains(text(res), "replace") {
		t.Fatalf("start = %s", text(res))
	}
	r.ok(t, "semar_journey_start", map[string]any{"id": "checkout", "title": "t", "owner": "o", "replace": true})
	if res := r.call(t, "semar_journey_start", map[string]any{"id": "../x", "title": "t", "owner": "o"}); !res.IsError {
		t.Error("an escaping id was accepted")
	}
}

func TestObserveModeRefusesEveryWriteTool(t *testing.T) {
	r := newRig(t, mcpkit.Guard{Observe: true, Reason: "the environment is production"})
	for name, tool := range r.tools {
		if !tool.Write {
			continue
		}
		res := r.call(t, name, map[string]any{"id": "x", "title": "t", "owner": "o", "surface": "api", "service": "s"})
		if !res.IsError || !strings.Contains(text(res), "only observes") {
			t.Errorf("%s was not refused: %s", name, text(res))
		}
	}
	if len(r.be.wrote) != 0 || len(r.be.runs) != 0 {
		t.Fatal("something was written or run")
	}
	// Reading stays possible.
	if res := r.call(t, "semar_journey_status", nil); !strings.Contains(text(res), "no draft") {
		t.Errorf("status = %s", text(res))
	}
}

func TestAutomaticLegsSwitchAndChainNeeds(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)

	res := r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), map[string]string{"id": "ord_12345"}) })
	if !strings.Contains(text(res), "opened the leg api_1") {
		t.Fatalf("no note for the first leg: %s", text(res))
	}
	// The same service goes on in the same leg.
	res = r.act(t, apiTool, map[string]any{"service": "orders"}, func() {})
	if strings.Contains(text(res), "opened") {
		t.Fatalf("a leg was opened for the same service: %s", text(res))
	}
	// Another service closes it and opens the next, which needs the first.
	res = r.act(t, apiTool, map[string]any{"service": "payments"}, func() { r.api.send(pay(), nil) })
	if !strings.Contains(text(res), "opened the leg api_2") || !strings.Contains(text(res), "needs api_1") {
		t.Fatalf("second leg: %s", text(res))
	}
	if v := r.api.Vars()["api_1.id"]; v != "ord_12345" {
		t.Errorf("the output of the closed leg is not a variable of the api tools: %v", r.api.Vars())
	}

	out := r.ok(t, "semar_journey_save", map[string]any{
		"invariants": [][]string{{"{{api_1.id}}", "{{api_1.id}}"}}, "run": false,
	})
	if !strings.Contains(out, `"saved": true`) {
		t.Fatalf("save = %s", out)
	}
	if len(r.be.wrote) != 1 {
		t.Fatal("the journey was not written")
	}
	j := r.be.wrote[0]
	if len(j.Legs) != 2 || j.Legs[0].ID != "api_1" || j.Legs[1].ID != "api_2" {
		t.Fatalf("legs = %+v", j.Legs)
	}
	if !slices.Equal(j.Legs[1].Needs, []domain.LegID{"api_1"}) || len(j.Legs[0].Needs) != 0 {
		t.Errorf("needs = %v / %v", j.Legs[0].Needs, j.Legs[1].Needs)
	}
	if !slices.Equal(j.Legs[0].Outputs, []string{"id"}) {
		t.Errorf("outputs = %v", j.Legs[0].Outputs)
	}
	// A bare {{id}} that belongs to an earlier leg is written as {{api_1.id}}.
	body := j.Legs[1].Steps[0].Request.JSON.(map[string]any)
	if body["orderId"] != "{{api_1.id}}" {
		t.Errorf("orderId = %v", body["orderId"])
	}
	if got := j.Legs[1].Steps[0].Hint; got.Method != "POST" || got.Path != "/v1/payments" {
		t.Errorf("hint = %+v", got)
	}
	first := j.Legs[0].Steps[0]
	if first.Request.Service != "" || first.Hint == nil {
		t.Errorf("first step = %+v", first)
	}
	// With no expect, the step holds what the system answered, by a model.
	if len(first.Expect) != 1 || first.Expect[0].Source != domain.SourceBaseline || first.Expect[0].By != "model" || *first.Expect[0].Check.Status != 201 {
		t.Errorf("expect = %+v", first.Expect)
	}
	if ps := journey.Validate(j, nil); len(ps) > 0 {
		t.Errorf("the journey is not valid: %v", ps)
	}
	data, err := journey.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"by: model", "source: baseline", "outputs:\n      - id", "needs:\n      - api_1", "{{api_1.id}}"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the YAML lacks %q:\n%s", want, data)
		}
	}
	if back, err := journey.Parse(data, "x"); err != nil || len(back.Legs) != 2 {
		t.Fatalf("round trip: %v", err)
	}
	if r.m.Active() {
		t.Error("the draft is still open after a save")
	}
}

func TestNoDraftMeansNoLegs(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	res := r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), nil) })
	if strings.Contains(text(res), "journey") {
		t.Fatalf("the middleware spoke without a draft: %s", text(res))
	}
	for _, tool := range []mcp.Tool{webList, mobList} {
		if res := r.act(t, tool, nil, nil); strings.Contains(text(res), "opened") {
			t.Errorf("%s opened a leg", tool.Name)
		}
	}
}

func TestExplicitLegWithWaitAndAs(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.ok(t, "semar_journey_add_leg", map[string]any{"id": "place", "surface": "api", "service": "orders", "as": "buyer"})
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), map[string]string{"id": "ord_12345"}) })
	r.ok(t, "semar_journey_add_leg", map[string]any{
		"id": "settled", "surface": "api", "service": "orders",
		"wait": map[string]any{"jsonpath": "$.data.status", "equals": "paid", "timeoutSec": 30, "everySec": 1},
	})
	read := journeymcp.APIStep{
		Service: "orders", Action: "Read the order", Status: 200,
		Request: domain.APIRequest{Method: "GET", Path: "/v1/orders/{{id}}"},
		Expect:  []domain.Expectation{{Check: domain.Check{JSONPath: "$.data.status", Equals: "paid"}, Source: domain.SourceBaseline, By: "model"}},
	}
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(read, nil) })
	r.ok(t, "semar_journey_save", map[string]any{"run": false})

	j := r.be.wrote[0]
	if j.Legs[0].ID != "place" || j.Legs[0].As != "buyer" {
		t.Errorf("leg 0 = %+v", j.Legs[0])
	}
	w := j.Legs[1].Wait
	if w == nil || w.Until.JSONPath != "$.data.status" || w.Until.Equals != "paid" || w.Timeout.Seconds() != 30 || w.Every.Seconds() != 1 {
		t.Errorf("wait = %+v", w)
	}
	if j.Legs[1].Steps[0].Request.Path != "/v1/orders/{{place.id}}" || j.Legs[1].Steps[0].Hint.Path != "/v1/orders/{id}" {
		t.Errorf("read step = %+v", j.Legs[1].Steps[0])
	}
	if ps := journey.Validate(j, nil); len(ps) > 0 {
		t.Errorf("not valid: %v", ps)
	}
}

func TestAddLegRefusals(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	if res := r.call(t, "semar_journey_add_leg", map[string]any{"id": "a", "surface": "api", "service": "s"}); !res.IsError || !strings.Contains(text(res), "semar_journey_start") {
		t.Errorf("without a draft: %s", text(res))
	}
	startJourney(t, r)
	for name, args := range map[string]map[string]any{
		"bad id":        {"id": "Bad-Id", "surface": "api", "service": "s"},
		"bad surface":   {"id": "a", "surface": "ios", "service": "s"},
		"no service":    {"id": "a", "surface": "api"},
		"web wait":      {"id": "a", "surface": "web", "service": "s", "wait": map[string]any{"jsonpath": "$.a", "equals": "b"}},
		"api app":       {"id": "a", "surface": "api", "service": "s", "app": "x"},
		"unknown needs": {"id": "a", "surface": "api", "service": "s", "needs": []string{"ghost"}},
	} {
		if res := r.call(t, "semar_journey_add_leg", args); !res.IsError {
			t.Errorf("%s was accepted", name)
		}
	}
	r.ok(t, "semar_journey_add_leg", map[string]any{"id": "a", "surface": "api", "service": "s"})
	if res := r.call(t, "semar_journey_add_leg", map[string]any{"id": "a", "surface": "api", "service": "s"}); !res.IsError {
		t.Error("a repeated leg id was accepted")
	}
}

func TestEmptyLegsAreDropped(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.ok(t, "semar_journey_add_leg", map[string]any{"id": "idle", "surface": "api", "service": "orders"})
	out := r.ok(t, "semar_journey_add_leg", map[string]any{"id": "real", "surface": "api", "service": "orders"})
	if !strings.Contains(out, "idle had no step") {
		t.Errorf("no note about the dropped leg: %s", out)
	}
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), map[string]string{"id": "ord_12345"}) })
	r.ok(t, "semar_journey_save", map[string]any{"run": false})
	j := r.be.wrote[0]
	if len(j.Legs) != 1 || j.Legs[0].ID != "real" || len(j.Legs[0].Needs) != 0 {
		t.Fatalf("legs = %+v", j.Legs)
	}
	// A draft with no steps at all cannot be saved.
	r.ok(t, "semar_journey_start", map[string]any{"id": "empty", "title": "t", "owner": "o"})
	if res := r.call(t, "semar_journey_save", nil); !res.IsError || !strings.Contains(text(res), "no leg") {
		t.Errorf("save of an empty draft = %s", text(res))
	}
}

func TestWebLegBuildsFromEventsWithALiftedValueAndASyntheticOpen(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() {
		r.api.send(create("/v1/orders"), map[string]string{"id": "ord_12345"})
	})

	// A window is open already, on a page, before the web leg starts.
	r.web.open, r.web.url, r.web.service = true, "http://shop.test/cart", "shop"
	page := record.Page{URL: "http://shop.test/cart", Title: "Cart"}
	field := &record.Element{Locator: domain.Locator{Role: "textbox", Name: "Order"}, Hint: "Order"}
	btn := &record.Element{Locator: domain.Locator{Role: "button", Name: "Find"}, Hint: "Find"}
	status := &record.Element{Locator: domain.Locator{CSS: "#status"}, Hint: "status"}
	res := r.act(t, webClick, map[string]any{"ref": "e1"}, func() {
		r.web.add(
			record.Event{Kind: record.KindFill, Page: page, Target: field, Value: "ord_12345"},
			record.Event{Kind: record.KindClick, Page: page, Target: btn},
			record.Event{Kind: record.KindCheck, Page: page, Target: status, Value: "paid"},
		)
	})
	if !strings.Contains(text(res), "opened the leg web_1 (web, service shop), needs api_1") {
		t.Fatalf("note = %s", text(res))
	}
	r.ok(t, "semar_journey_save", map[string]any{"run": false})
	j := r.be.wrote[0]
	leg := j.Legs[1]
	if leg.Surface != domain.SurfaceWeb || leg.App != "shop" || leg.Service != "shop" {
		t.Fatalf("leg = %+v", leg)
	}
	first := leg.Steps[0]
	if first.Web == nil || first.Web.Do != domain.WebOpen || first.Web.Path != "/cart" {
		t.Errorf("no synthetic open of the page at the mark: %+v", first.Web)
	}
	var typed *domain.WebAction
	for _, st := range leg.Steps {
		if st.Web != nil && st.Web.Do == domain.WebFill {
			typed = st.Web
		}
		if st.Web != nil && (st.Web.Service != "" || st.Web.App != "") {
			t.Errorf("the step repeats the service of the leg: %+v", st.Web)
		}
	}
	if typed == nil || typed.Value != "{{api_1.id}}" {
		t.Errorf("the typed literal is not lifted to the output of the api leg: %+v", typed)
	}
	checks := 0
	for _, st := range leg.Steps {
		for _, e := range st.Expect {
			checks++
			if e.By != "model" || e.Source != domain.SourceBaseline {
				t.Errorf("a web check is not marked as a model's: %+v", e)
			}
		}
	}
	if checks != 1 {
		t.Errorf("checks = %d", checks)
	}
	p := r.be.pending[0]
	if len(p.Screens) != 1 || p.Screens[0].App != "shop" || len(p.Screens[0].Adds) == 0 {
		t.Errorf("pending screens = %+v", p.Screens)
	}
}

func TestWebLegWithoutACheckIsRefusedAndStaysOpen(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.web.service = "shop"
	page := record.Page{URL: "http://shop.test/cart", Title: "Cart"}
	r.act(t, webOpen, map[string]any{"service": "shop"}, func() {
		r.web.add(record.Event{Kind: record.KindOpen, Page: page})
	})
	res := r.call(t, "semar_journey_save", nil)
	if !res.IsError || !strings.Contains(text(res), "web_assert") {
		t.Fatalf("save = %s", text(res))
	}
	if len(r.be.wrote) != 0 {
		t.Fatal("something was written")
	}
	// The leg is still open, so the agent can add the check.
	r.act(t, webClick, map[string]any{}, func() {
		r.web.add(record.Event{Kind: record.KindCheck, Page: page, Target: &record.Element{Locator: domain.Locator{CSS: "#a"}, Hint: "a"}, Value: "x"})
	})
	r.ok(t, "semar_journey_save", map[string]any{"run": false})
	if len(r.be.wrote) != 1 {
		t.Fatal("the journey was not written after the fix")
	}
}

func TestAndroidLegRecordsAFlowOnTheFirstWriteAndUsesIt(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	// Listing does not start a recording and opens no leg.
	if res := r.act(t, mobList, nil, nil); strings.Contains(text(res), "opened") || len(r.mob.started) != 0 {
		t.Fatalf("a listing started something: %s", text(res))
	}
	r.mob.steps = 3
	r.mob.checks = []journeymcp.LegCheck{{
		ID: "check-code", Action: "Check code", Screen: "Home", Element: "code",
		Expect: []domain.Expectation{{Check: domain.Check{Element: "code", Equals: "A1B2"}, Source: domain.SourceBaseline, By: "model"}},
	}}
	sent := 0
	res := r.act(t, mobTap, map[string]any{"x": 1, "y": 2}, func() { sent++ })
	if !strings.Contains(text(res), "opened the leg android_1 (android, service shop)") {
		t.Fatalf("note = %s", text(res))
	}
	if !slices.Equal(r.mob.started, []string{"checkout-android_1@shop#checkout"}) {
		t.Fatalf("started = %v", r.mob.started)
	}
	// The tap that started the recording aimed at the screen before the restart, so it was not sent.
	if sent != 0 || !strings.Contains(text(res), "was not sent") {
		t.Fatalf("the tap was sent (%d) or not explained: %s", sent, text(res))
	}
	// More actions of the same leg start nothing again, and are sent.
	r.act(t, mobTap, nil, func() { sent++ })
	if len(r.mob.started) != 1 || sent != 1 {
		t.Fatalf("started again: %v, sent %d", r.mob.started, sent)
	}
	r.ok(t, "semar_journey_save", map[string]any{"run": false})
	j := r.be.wrote[0]
	leg := j.Legs[0]
	if leg.Surface != domain.SurfaceAndroid || leg.App != "shop" || len(leg.Steps) != 2 || leg.Steps[0].Use != "checkout-android_1" {
		t.Fatalf("leg = %+v", leg)
	}
	// The check comes after the flow, on a step that waits for its element, written by a model.
	chk := leg.Steps[1]
	if chk.ID != "check-code" || chk.Mobile == nil || chk.Mobile.Do != domain.MobileWait || chk.Mobile.Screen != "Home" || chk.Mobile.Element != "code" ||
		len(chk.Expect) != 1 || chk.Expect[0].By != "model" {
		t.Errorf("check step = %+v", chk)
	}
	if !slices.Equal(leg.Outputs, []string{"code"}) {
		t.Errorf("outputs = %v", leg.Outputs)
	}
	if p := r.be.pending[0]; len(p.Flows) != 1 || p.Flows[0].ID != "checkout-android_1" {
		t.Errorf("pending flows = %+v", p.Flows)
	}
}

func TestAScreenActionThatStartsTheRecordingIsNotSent(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	sent := 0
	res := r.act(t, mobTap, map[string]any{"x": 964, "y": 158}, func() { sent++ })
	got := text(res)
	if sent != 0 || res.IsError {
		t.Fatalf("the tap was sent %d times, or failed: %s", sent, got)
	}
	for _, want := range []string{"opened the leg android_1 (android, service shop)", "mobile_click_on_screen_at_coordinates was not sent", legScreenText} {
		if !strings.Contains(got, want) {
			t.Errorf("answer lacks %q:\n%s", want, got)
		}
	}
	if r.mob.screens != 1 {
		t.Errorf("LegScreen called %d times, want 1", r.mob.screens)
	}
	// The leg is open and recording: the next tap is sent.
	r.act(t, mobTap, map[string]any{"x": 1, "y": 2}, func() { sent++ })
	if sent != 1 || r.mob.screens != 1 || len(r.mob.started) != 1 {
		t.Errorf("sent %d, screens %d, started %v", sent, r.mob.screens, r.mob.started)
	}
}

func TestAScreenActionThatStartsTheRecordingSaysSoWhenTheScreenIsNotRead(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.mob.screenErr = errors.New("the device is busy")
	sent := 0
	got := text(r.act(t, mobTap, nil, func() { sent++ }))
	if sent != 0 || !strings.Contains(got, "was not sent") || !strings.Contains(got, "List the screen with mobile_list_elements_on_screen and act again.") {
		t.Errorf("sent %d, answer:\n%s", sent, got)
	}
}

func TestARunOfALinkOrAFlowStillRunsWhenItStartsTheRecording(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	open := mcp.Tool{Name: "mobile_open_url", Surface: "mobile", Write: true}
	sent := 0
	got := text(r.act(t, open, nil, func() { sent++ }))
	if sent != 1 || strings.Contains(got, "was not sent") {
		t.Errorf("sent %d, answer:\n%s", sent, got)
	}
}

func TestALegOfAServiceWithNoBuildCarriesAWarning(t *testing.T) {
	const problem = "the service shop has no build that the replay can read"
	r := newRig(t, mcpkit.Guard{})
	r.be.buildProblem = problem
	startJourney(t, r)
	// The leg that an action opens says it in its note.
	got := text(r.act(t, mobTap, nil, nil))
	if !strings.Contains(got, "journey: warning: "+problem) {
		t.Errorf("the auto-opened leg has no warning:\n%s", got)
	}
	// The leg that the agent opens says it in its answer.
	got = r.ok(t, "semar_journey_add_leg", map[string]any{"id": "pay", "surface": "api", "service": "orders"})
	if !strings.Contains(got, `"warning"`) || !strings.Contains(got, problem) {
		t.Errorf("add_leg has no warning:\n%s", got)
	}
	// A backend with nothing to say adds nothing.
	q := newRig(t, mcpkit.Guard{})
	startJourney(t, q)
	if got := text(q.act(t, mobTap, nil, nil)); strings.Contains(got, "warning") {
		t.Errorf("a warning without a problem:\n%s", got)
	}
	if got := q.ok(t, "semar_journey_add_leg", map[string]any{"id": "pay", "surface": "api", "service": "orders"}); strings.Contains(got, "warning") {
		t.Errorf("a warning without a problem:\n%s", got)
	}
}

func TestAndroidServiceUnknownAsksForAddLeg(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.mob.fgErr = errors.New("com.android.launcher is not an app of the environment")
	res := r.act(t, mobTap, nil, nil)
	if !res.IsError || !strings.Contains(text(res), "semar_journey_add_leg") {
		t.Fatalf("result = %s", text(res))
	}
	if len(r.mob.started) != 0 {
		t.Fatal("a recording started")
	}
	r.ok(t, "semar_journey_add_leg", map[string]any{"id": "app", "surface": "android", "service": "shop"})
	if res := r.act(t, mobTap, nil, nil); res.IsError {
		t.Fatalf("after add_leg: %s", text(res))
	}
	if !slices.Equal(r.mob.started, []string{"checkout-app@shop#checkout"}) {
		t.Errorf("started = %v", r.mob.started)
	}
}

func TestARunOfASavedFlowStartsTheRecordingOfTheLeg(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.ok(t, "semar_journey_add_leg", map[string]any{"id": "login", "surface": "android", "service": "shop"})
	called := false
	res := r.act(t, runFlowToo, map[string]any{"id": "login"}, func() { called = true })
	if res.IsError || !called {
		t.Fatalf("the flow was not run: %s", text(res))
	}
	if !slices.Equal(r.mob.started, []string{"checkout-login@shop#checkout"}) {
		t.Fatalf("started = %v", r.mob.started)
	}
	// The recording goes on with the next action, and starts nothing again.
	r.act(t, mobTap, nil, nil)
	if len(r.mob.started) != 1 {
		t.Fatalf("started again: %v", r.mob.started)
	}
}

func TestUndoStep(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	if res := r.call(t, "semar_journey_undo_step", nil); !res.IsError {
		t.Error("undo with no open leg")
	}
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), map[string]string{"id": "ord_12345"}) })
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), nil) })
	r.ok(t, "semar_journey_undo_step", nil)
	if r.api.Seq() != 1 {
		t.Fatalf("log = %d steps after undo", r.api.Seq())
	}
	r.ok(t, "semar_journey_undo_step", nil)
	if res := r.call(t, "semar_journey_undo_step", nil); !res.IsError {
		t.Error("undo with an empty leg")
	}
	// A step of an earlier leg is not undone through a later leg.
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), nil) })
	r.act(t, apiTool, map[string]any{"service": "payments"}, nil)
	if res := r.call(t, "semar_journey_undo_step", nil); !res.IsError {
		t.Error("undo reached into a closed leg")
	}

	r.ok(t, "semar_journey_discard", nil)
	startJourney(t, r)
	r.web.service = "shop"
	r.act(t, webOpen, map[string]any{"service": "shop"}, func() {
		r.web.add(record.Event{Kind: record.KindOpen, Page: record.Page{URL: "http://shop.test/a"}})
	})
	if res := r.call(t, "semar_journey_undo_step", nil); !res.IsError || !strings.Contains(text(res), "web step cannot be undone") {
		t.Errorf("web undo = %s", text(res))
	}

	r.ok(t, "semar_journey_discard", nil)
	startJourney(t, r)
	r.mob.steps = 2
	r.act(t, mobTap, nil, nil)
	r.ok(t, "semar_journey_undo_step", nil)
	if r.mob.undone != 1 {
		t.Errorf("android undo calls = %d", r.mob.undone)
	}
}

func TestStatusShowsYAMLProblemsAndCounts(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	if res := r.call(t, "semar_journey_status", nil); !res.IsError {
		t.Error("status without a draft")
	}
	startJourney(t, r)
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), map[string]string{"id": "ord_12345"}) })
	out := r.ok(t, "semar_journey_status", nil)
	var v struct {
		Legs []struct {
			ID    string `json:"id"`
			State string `json:"state"`
			Steps int    `json:"steps"`
		} `json:"legs"`
		YAML     string   `json:"yaml"`
		Problems []string `json:"problems"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Legs) != 1 || v.Legs[0].State != "open" || v.Legs[0].Steps != 1 {
		t.Errorf("legs = %+v", v.Legs)
	}
	if !strings.Contains(v.YAML, "id: api_1") || !strings.Contains(v.YAML, "kind: Journey") {
		t.Errorf("yaml = %s", v.YAML)
	}
	// Looking at the draft sets no variable and closes nothing.
	if _, ok := r.api.Vars()["api_1.id"]; ok {
		t.Error("status set a variable of the api tools")
	}
	r.be.problems = []journey.Problem{{Path: "legs[0].steps[0]", Message: "something is wrong"}}
	out = r.ok(t, "semar_journey_status", nil)
	if !strings.Contains(out, "something is wrong") {
		t.Errorf("problems missing: %s", out)
	}
}

func TestSaveWithAReplayThatPasses(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), map[string]string{"id": "ord_12345"}) })
	out := r.ok(t, "semar_journey_save", nil)
	if !strings.Contains(out, `"outcome": "passed"`) || !strings.Contains(out, `"replayed": true`) {
		t.Fatalf("save = %s", out)
	}
	if len(r.be.runs) != 1 || r.be.runs[0] != "checkout" || r.be.undone != 0 {
		t.Errorf("runs %v undone %d", r.be.runs, r.be.undone)
	}
}

func TestSaveUndoesTheFilesWhenTheReplayDoesNotPass(t *testing.T) {
	for _, outcome := range []string{"failed", "blocked", "stale", "not_run"} {
		t.Run(outcome, func(t *testing.T) {
			r := newRig(t, mcpkit.Guard{})
			r.be.outcome = journeymcp.RunOutcome{Outcome: outcome, FirstFailing: "api_1", Reasons: []string{"status was 500, not 201"}}
			startJourney(t, r)
			r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), map[string]string{"id": "ord_12345"}) })
			res := r.call(t, "semar_journey_save", nil)
			if !res.IsError {
				t.Fatalf("a replay that is %s was kept: %s", outcome, text(res))
			}
			if r.be.undone != 1 {
				t.Fatalf("undo calls = %d", r.be.undone)
			}
			for _, want := range []string{outcome, "api_1", "status was 500", "removed"} {
				if !strings.Contains(text(res), want) {
					t.Errorf("result lacks %q: %s", want, text(res))
				}
			}
			// The last api leg is open again, so the agent can fix it.
			if !r.m.Active() {
				t.Fatal("the draft is gone")
			}
			r.ok(t, "semar_journey_undo_step", nil)
		})
	}
}

func TestSaveUndoesWhenTheReplayCannotRun(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.be.runErr = errors.New("the replay could not start")
	startJourney(t, r)
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), nil) })
	res := r.call(t, "semar_journey_save", nil)
	if !res.IsError || r.be.undone != 1 || !strings.Contains(text(res), "could not start") {
		t.Fatalf("result = %s, undone %d", text(res), r.be.undone)
	}
}

func TestSaveWritesNothingWhenTheJourneyIsNotValid(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.be.problems = []journey.Problem{{Path: "legs[0].needs[0]", Message: "ghost is not a leg of this journey"}}
	startJourney(t, r)
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), nil) })
	res := r.call(t, "semar_journey_save", nil)
	if !res.IsError || !strings.Contains(text(res), "ghost") || len(r.be.wrote) != 0 || len(r.be.runs) != 0 {
		t.Fatalf("result = %s, wrote %d", text(res), len(r.be.wrote))
	}
	r.be.problems = nil
	r.ok(t, "semar_journey_save", map[string]any{"run": false})
}

func TestSaveRefusesAnExistingJourneyWithoutReplace(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.be.existing["checkout"] = true
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), nil) })
	if res := r.call(t, "semar_journey_save", nil); !res.IsError || !strings.Contains(text(res), "replace") {
		t.Fatalf("save = %s", text(res))
	}
	r.ok(t, "semar_journey_save", map[string]any{"replace": true, "run": false})
}

func TestSaveChecksInvariants(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), nil) })
	if res := r.call(t, "semar_journey_save", map[string]any{"invariants": [][]string{{"only one"}}}); !res.IsError {
		t.Error("an invariant of one value was accepted")
	}
}

func TestRunTool(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	if res := r.call(t, "semar_journey_run", map[string]any{"id": "ghost"}); !res.IsError || !strings.Contains(text(res), "not saved") {
		t.Errorf("unknown journey = %s", text(res))
	}
	r.be.existing["checkout"] = true
	out := r.ok(t, "semar_journey_run", map[string]any{"id": "checkout"})
	if !strings.Contains(out, `"outcome": "passed"`) {
		t.Errorf("run = %s", out)
	}
	r.be.outcome = journeymcp.RunOutcome{Outcome: "failed", FirstFailing: "pay", Reasons: []string{"status was 500"}}
	res := r.call(t, "semar_journey_run", map[string]any{"id": "checkout"})
	if !res.IsError || !strings.Contains(text(res), "pay") {
		t.Errorf("a failed run = %s", text(res))
	}
}

func TestRunIsRefusedWhileAnAndroidLegRecords(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.act(t, mobTap, nil, nil)
	r.be.existing["checkout"] = true
	if res := r.call(t, "semar_journey_run", map[string]any{"id": "checkout"}); !res.IsError || !strings.Contains(text(res), "recording") {
		t.Fatalf("run = %s", text(res))
	}
}

func TestDiscardEndsTheRecordingOfAnOpenAndroidLeg(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.act(t, mobTap, nil, nil)
	r.ok(t, "semar_journey_discard", nil)
	if r.mob.discarded != 1 || r.m.Active() {
		t.Errorf("discarded %d, active %v", r.mob.discarded, r.m.Active())
	}
	if res := r.call(t, "semar_journey_discard", nil); !res.IsError {
		t.Error("a second discard")
	}
}

func TestCloseDropsARecording(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.act(t, mobTap, nil, nil)
	if err := r.m.Close(context.Background()); err != nil || r.mob.discarded != 1 {
		t.Fatalf("Close = %v, discarded %d", err, r.mob.discarded)
	}
}

func TestSecretsNeverReachAResult(t *testing.T) {
	scrub := mcpkit.NewScrubber(nil, "tok_SECRET_value_9f3")
	r := newRig(t, mcpkit.Guard{})
	r.m = journeymcp.New(journeymcp.Options{Scrub: scrub, API: r.api, Web: r.web, Mobile: r.mob, Backend: r.be})
	r.tools = map[string]mcp.Tool{}
	for _, tl := range r.m.Tools() {
		r.tools[tl.Name] = tl
	}
	r.mw = r.m.AutoLegMiddleware()
	r.ok(t, "semar_journey_start", map[string]any{"id": "checkout", "title": "title with tok_SECRET_value_9f3 inside", "owner": "o"})
	r.act(t, apiTool, map[string]any{"service": "orders"}, func() { r.api.send(create("/v1/orders"), nil) })
	for _, name := range []string{"semar_journey_status", "semar_journey_discard"} {
		if out := text(r.call(t, name, nil)); strings.Contains(out, "tok_SECRET_value_9f3") {
			t.Errorf("%s leaked the secret: %s", name, out)
		}
	}
}

func TestMiddlewareIgnoresOtherTools(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	for _, tl := range []mcp.Tool{
		{Name: "api_list_services", Surface: "api"}, {Name: "web_save", Surface: "web", Write: true},
		{Name: "web_close", Surface: "web"}, {Name: "semar_record_start", Surface: "mobile", Write: true},
		{Name: "semar_journey_start", Surface: "journey", Write: true},
	} {
		called := false
		h := r.mw(tl, func(context.Context, mcp.Call) (mcp.Result, error) { called = true; return mcp.TextResult("x"), nil })
		res, _ := h(context.Background(), mcp.Call{Name: tl.Name})
		if !called || strings.Contains(text(res), "opened") {
			t.Errorf("%s: called %v, result %s", tl.Name, called, text(res))
		}
	}
	if r.m.Active() == false {
		t.Fatal("the draft was lost")
	}
}

func TestRaceOfLegsAndStatus(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc := []string{"orders", "payments"}[i%2]
			r.act(t, apiTool, map[string]any{"service": svc}, func() { r.api.send(create("/v1/orders"), nil) })
			r.call(t, "semar_journey_status", nil)
		}()
	}
	wg.Wait()
}

func TestALaunchThatStartsTheRecordingIsNotSentAgain(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	r.ok(t, "semar_journey_add_leg", map[string]any{"id": "app", "surface": "android", "service": "shop"})
	launch := mcp.Tool{Name: "mobile_launch_app", Surface: "mobile", Write: true}
	called := 0
	res := r.act(t, launch, map[string]any{"packageName": "com.example.shop"}, func() { called++ })
	if called != 0 || res.IsError || !strings.Contains(text(res), "started the app from a fresh start") {
		t.Fatalf("the first launch: called %d, %s", called, text(res))
	}
	if !slices.Equal(r.mob.started, []string{"checkout-app@shop#checkout"}) {
		t.Fatalf("started = %v", r.mob.started)
	}
	// Once the recording goes on, a launch is the tool's own to answer.
	r.act(t, launch, map[string]any{"packageName": "com.example.shop"}, func() { called++ })
	if called != 1 {
		t.Errorf("the second launch was swallowed: called %d", called)
	}
}

func TestTheRecordingOfAnAndroidLegIsTheJourneysToClose(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	save := mcp.Tool{Name: "semar_record_save", Surface: "mobile", Write: true}
	// With no draft the tool is the recording's own.
	called := false
	if res := r.act(t, save, nil, func() { called = true }); res.IsError || !called {
		t.Fatalf("without a draft: %s", text(res))
	}
	startJourney(t, r)
	r.act(t, mobTap, nil, nil)
	for _, name := range []string{"semar_record_start", "semar_record_save", "semar_record_stop", "semar_record_undo"} {
		called = false
		res := r.act(t, mcp.Tool{Name: name, Surface: "mobile", Write: true}, nil, func() { called = true })
		if !res.IsError || called || !strings.Contains(text(res), "semar_journey_add_leg") {
			t.Errorf("%s in an android leg: called %v, %s", name, called, text(res))
		}
	}
	// The tools that shape the recording stay available.
	called = false
	r.act(t, mcp.Tool{Name: "semar_record_assert", Surface: "mobile", Write: true}, nil, func() { called = true })
	if !called {
		t.Error("semar_record_assert was refused")
	}
}

func TestWebOpenWithTheOnlyWebServiceOpensTheLegAtOnce(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	startJourney(t, r)
	// With several web services and no window, the leg waits for the action that names or shows one.
	res := r.act(t, webOpen, map[string]any{"path": "/x"}, func() {})
	if strings.Contains(text(res), "opened the leg") {
		t.Fatalf("a leg was opened without a service: %s", text(res))
	}
	r.web.def = "shop"
	res = r.act(t, webOpen, map[string]any{"path": "/x"}, func() {})
	if !strings.Contains(text(res), "opened the leg web_1 (web, service shop)") {
		t.Fatalf("note = %s", text(res))
	}
}

func TestAFailedReplayLeavesNoWebLegToOpenAgain(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.be.outcome = journeymcp.RunOutcome{Outcome: "failed", FirstFailing: "web_1", Reasons: []string{"the page did not show it"}}
	startJourney(t, r)
	r.web.service = "shop"
	page := record.Page{URL: "http://shop.test/cart", Title: "Cart"}
	r.act(t, webOpen, map[string]any{"service": "shop"}, func() {
		r.web.add(record.Event{Kind: record.KindOpen, Page: page},
			record.Event{Kind: record.KindCheck, Page: page, Target: &record.Element{Locator: domain.Locator{CSS: "#a"}, Hint: "a"}, Value: "x"})
	})
	res := r.call(t, "semar_journey_save", nil)
	if !res.IsError || r.be.undone != 1 || !strings.Contains(text(res), "the page did not show it") {
		t.Fatalf("save = %s", text(res))
	}
	// The replay closed the browser, so there is no leg to undo a step of.
	if res := r.call(t, "semar_journey_undo_step", nil); !res.IsError || !strings.Contains(text(res), "no open leg") {
		t.Errorf("undo = %s", text(res))
	}
}

func TestWebCloseAndWebSaveAreRefusedWhileTheDraftHoldsAnOpenWebLeg(t *testing.T) {
	webClose := mcp.Tool{Name: "web_close", Surface: "web"}
	webSave := mcp.Tool{Name: "web_save", Surface: "web", Write: true}
	r := newRig(t, mcpkit.Guard{})

	// With no draft the middleware stays out of the way.
	ran := 0
	for _, tl := range []mcp.Tool{webClose, webSave} {
		r.act(t, tl, map[string]any{}, func() { ran++ })
	}
	if ran != 2 {
		t.Fatalf("with no draft, %d of 2 calls ran", ran)
	}

	startJourney(t, r)
	r.web.open, r.web.url, r.web.service = true, "http://shop.test/cart", "shop"
	r.ok(t, "semar_journey_add_leg", map[string]any{"id": "shop", "surface": "web", "service": "shop"})
	for _, tl := range []mcp.Tool{webClose, webSave} {
		ran = 0
		res := r.act(t, tl, map[string]any{}, func() { ran++ })
		if !res.IsError || ran != 0 {
			t.Fatalf("%s with an open web leg: ran=%d %+v", tl.Name, ran, res)
		}
		for _, want := range []string{"cannot close/save the web browser", "semar_journey_save", "semar_journey_discard"} {
			if !strings.Contains(text(res), want) {
				t.Errorf("%s: %q is not in %q", tl.Name, want, text(res))
			}
		}
	}

	// An api leg does not hold the browser, so the web tools are the agent's again.
	r.ok(t, "semar_journey_add_leg", map[string]any{"id": "orders", "surface": "api", "service": "orders"})
	ran = 0
	r.act(t, webClose, map[string]any{}, func() { ran++ })
	if ran != 1 {
		t.Error("web_close was refused with no web leg open")
	}
}
