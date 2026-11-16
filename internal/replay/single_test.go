package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/replay"
	"github.com/jancuk/semar/internal/verdict"
)

// leg builds the scenario and the script of a one-step API leg, as a journey leg is.
func leg(id domain.ScenarioID, step domain.Step, req domain.APIRequest, captures ...domain.Capture) (domain.Scenario, domain.Script) {
	sc := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: id, Title: "leg " + string(id),
		Ticket: "checkout", Intent: "Leg of journey checkout", Surface: domain.SurfaceAPI,
		AuthProfile: "support-agent", Steps: []domain.Step{step},
	}
	return sc, domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: id, ExpectHash: domain.ExpectHash(sc),
		AuthoredBy: domain.ProviderRef{Instance: "journey", Harness: "file"},
		Steps:      []domain.ScriptStep{{Step: step.ID, Request: &req, Captures: captures}},
	}
}

func placeLeg() (domain.Scenario, domain.Script) {
	return leg("checkout.place",
		domain.Step{
			ID: "create", Action: "Create an order of 1000",
			Hint:   &domain.RequestHint{Method: "POST", Path: "/v1/orders"},
			Expect: []domain.Expectation{{Check: domain.Check{Status: new(201)}, Source: domain.SourcePerson}},
		},
		domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders", JSON: map[string]any{"amount": 1000, "ref": "semar-{{run.id}}"}},
		domain.Capture{Name: "id", JSONPath: "$.data.id"}, domain.Capture{Name: "amount", JSONPath: "$.data.amount"},
	)
}

func readLeg(path string) (domain.Scenario, domain.Script) {
	return leg("checkout.settled",
		domain.Step{
			ID: "read", Action: "Read the order",
			Hint:   &domain.RequestHint{Method: "GET", Path: "/v1/orders/{id}"},
			Expect: []domain.Expectation{{Check: domain.Check{Status: new(200)}, Source: domain.SourcePerson}},
		},
		domain.APIRequest{Service: "orders", Method: "GET", Path: path},
	)
}

func (r *rig) runOne(t *testing.T, in replay.Single) replay.Replayed {
	t.Helper()
	got, err := replay.New(r.deps).RunOne(t.Context(), in)
	if err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	return got
}

// compute decides a leg as the journey runner will: the builds at the end are the builds at the start.
func compute(sc domain.Scenario, got replay.Replayed, builds []domain.BuildRef) domain.ScenarioResult {
	res := verdict.Compute(verdict.Input{
		Scenario: sc, Steps: got.Steps, Confirmed: got.Confirmed,
		BuildsStart: builds, BuildsEnd: builds, Blocked: got.Blocked,
	})
	res.Reasons = append(res.Reasons, got.Notes...)
	return res
}

func (r *rig) builds(t *testing.T) []domain.BuildRef {
	t.Helper()
	b, err := r.deps.Builds.Probe(t.Context(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	return []domain.BuildRef{b}
}

func TestRunOnePresetVars(t *testing.T) {
	r := newRig(t, rigOptions{})
	place, placeScript := placeLeg()
	placed := r.runOne(t, replay.Single{Scenario: place, Script: placeScript})
	if got := compute(place, placed, r.builds(t)); got.Outcome != domain.OutcomePassed {
		t.Fatalf("place: %s %+v", got.Outcome, got.Reasons)
	}
	if placed.Captured["id"] != "o-1" || placed.Captured["amount"] != "1000" {
		t.Fatalf("captured = %v", placed.Captured)
	}

	read, readScript := readLeg("/v1/orders/{{place.id}}")
	got := r.runOne(t, replay.Single{
		Scenario: read, Script: readScript,
		Vars: map[string]string{"place.id": placed.Captured["id"], "place.amount": placed.Captured["amount"]},
	})
	if res := compute(read, got, r.builds(t)); res.Outcome != domain.OutcomePassed {
		t.Fatalf("settled: %s %+v", res.Outcome, res.Reasons)
	}
	a := r.storedAction(t, read.ID, "read")
	if !strings.HasSuffix(a.Request.URL, "/v1/orders/o-1") || a.Request.PathTemplate != "/v1/orders/{id}" {
		t.Errorf("the step was sent to %s with template %s", a.Request.URL, a.Request.PathTemplate)
	}
	if a.Use != (domain.UseCase{Pass: domain.PassScenario, Scenario: read.ID, Step: "read"}) || a.By != domain.ActorReplay {
		t.Errorf("use %+v by %s", a.Use, a.By)
	}
	if got.Last == nil || got.Last.Path != "/v1/orders/o-1" || got.Last.Use.Step != "read" {
		t.Errorf("last call = %+v", got.Last)
	}
}

func TestRunOneRunIDIsTheRunners(t *testing.T) {
	r := newRig(t, rigOptions{})
	place, s := placeLeg()
	r.runOne(t, replay.Single{Scenario: place, Script: s, Vars: map[string]string{"run.id": "other"}})
	if a := r.storedAction(t, place.ID, "create"); !strings.Contains(string(a.Request.Body), `"ref":"semar-r-1"`) {
		t.Errorf("body = %s", a.Request.Body)
	}
}

func TestRunOneRefusesUnknownVar(t *testing.T) {
	cases := []struct {
		name string
		path string
		vars map[string]string
		want string
	}{
		{"no value given", "/v1/orders/{{place.id}}", nil, "place.id is not a capture, run.id, or an output of a given entry"},
		{"another leg's output", "/v1/orders/{{pay.id}}", map[string]string{"place.id": "o-1"}, "pay.id is not a capture, run.id, or an output of a given entry"},
		{"a name of the leg that was not given", "/v1/orders/{{place.total}}", map[string]string{"place.id": "o-1"}, "place.total is not an output of fixture"},
		{"a secret", "/v1/orders/{{secret.token}}", map[string]string{"secret.token": "o-1"}, "a script cannot use a secret"},
		{"a data value", "/v1/orders/{{data.id}}", map[string]string{"data.id": "o-1"}, "data.id cannot be used in a script"},
		{"an undotted name", "/v1/orders/{{id}}", map[string]string{"id": "o-1"}, "id is used before any step captures it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, rigOptions{})
			sc, s := readLeg(tc.path)
			got := r.runOne(t, replay.Single{Scenario: sc, Script: s, Vars: tc.vars})
			if !slices.ContainsFunc(got.Blocked, func(b string) bool { return strings.Contains(b, tc.want) }) {
				t.Errorf("blocked = %q, want one that holds %q", got.Blocked, tc.want)
			}
			if n := r.app.Requests("", "/v1/orders"); n != 0 {
				t.Errorf("%d requests were sent", n)
			}
			if len(got.Steps) != 0 {
				t.Errorf("steps = %v", got.Steps)
			}
		})
	}
}

func TestRunOneBlocksGivenEntries(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := readLeg("/v1/orders/{{place.id}}")

	withGiven := sc
	withGiven.Given = []domain.Given{{Fixture: "order.new", As: "order"}}
	got := r.runOne(t, replay.Single{Scenario: withGiven, Script: s, Vars: map[string]string{"place.id": "o-1"}})
	if !slices.ContainsFunc(got.Blocked, func(b string) bool { return strings.Contains(b, "does not resolve given entries") }) {
		t.Errorf("blocked = %q", got.Blocked)
	}
	if n := r.app.Requests("", "/v1/orders"); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestRunOneWrongHashIsBlocked(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := readLeg("/v1/orders/{{place.id}}")
	s.ExpectHash = "0000"
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s, Vars: map[string]string{"place.id": "o-1"}})
	if !slices.ContainsFunc(got.Blocked, func(b string) bool { return strings.Contains(b, "the script was written for other expected results") }) {
		t.Errorf("blocked = %q", got.Blocked)
	}
	if n := r.app.Requests("", "/v1/orders"); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestRunOneDoesNotChangeItsInput(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := readLeg("/v1/orders/{{place.id}}")
	vars := map[string]string{"place.id": "o-1"}
	r.runOne(t, replay.Single{Scenario: sc, Script: s, Vars: vars})
	if !reflect.DeepEqual(vars, map[string]string{"place.id": "o-1"}) {
		t.Errorf("vars = %v", vars)
	}
}

// An injected value is stored like any capture: the recorder redacts it from the action.
func TestRunOneRedactsAnInjectedValue(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := leg("checkout.pay",
		domain.Step{
			ID: "create", Action: "Create an order with a reference",
			Hint:   &domain.RequestHint{Method: "POST", Path: "/v1/orders"},
			Expect: []domain.Expectation{{Check: domain.Check{Status: new(201)}, Source: domain.SourcePerson}},
		},
		domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders", JSON: map[string]any{"amount": 1000, "ref": "{{place.ref}}"}},
		domain.Capture{Name: "ref", JSONPath: "$.data.ref"},
	)
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s, Vars: map[string]string{"place.ref": token}})
	if got.Captured["ref"] != token {
		t.Fatalf("the raw capture = %q, want the injected value", got.Captured["ref"])
	}
	a := r.storedAction(t, sc.ID, "create")
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Errorf("the stored action holds the injected value: %s", raw)
	}
	if !strings.Contains(string(a.Request.Body), "[redacted:") {
		t.Errorf("request body = %s", a.Request.Body)
	}
}

func TestRunOneKeepsTheCapturesThatWereFound(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := placeLeg()
	s.Steps[0].Captures = append(s.Steps[0].Captures, domain.Capture{Name: "gone", JSONPath: "$.data.nothing"})
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s})
	if _, ok := got.Captured["id"]; !ok {
		t.Errorf("captured = %v, want the values that were found", got.Captured)
	}
	if got.Last == nil {
		t.Error("no last call")
	}
}

func TestRunOneNeverProbesBuildsOrResolvesFixtures(t *testing.T) {
	r := newRig(t, rigOptions{buildURL: "http://127.0.0.1:1/version"})
	sc, s := placeLeg()
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s})
	if len(got.Blocked) != 0 || len(got.Steps) != 1 {
		t.Errorf("blocked %q steps %d", got.Blocked, len(got.Steps))
	}
}

func TestRunOneUsesThePassOfTheCaller(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := readLeg("/v1/orders/{{place.id}}")
	r.runOne(t, replay.Single{Scenario: sc, Script: s, Vars: map[string]string{"place.id": "o-1"}, Pass: domain.PassTrial})
	if a := r.storedAction(t, sc.ID, "read"); a.Use.Pass != domain.PassTrial {
		t.Errorf("pass = %s", a.Use.Pass)
	}
}

func TestRunOneConfirmation(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := leg("checkout.read",
		domain.Step{
			ID: "read", Action: "Read the order",
			Hint:   &domain.RequestHint{Method: "GET", Path: "/v1/orders/{id}"},
			Expect: []domain.Expectation{{Check: domain.Check{Status: new(200)}, Source: domain.SourceDiff}},
		},
		domain.APIRequest{Service: "orders", Method: "GET", Path: "/v1/orders/{{place.id}}"},
	)
	place, ps := placeLeg()
	r.runOne(t, replay.Single{Scenario: place, Script: ps})
	in := replay.Single{Scenario: sc, Script: s, Vars: map[string]string{"place.id": "o-1"}}
	if res := compute(sc, r.runOne(t, in), r.builds(t)); res.Outcome != domain.OutcomeInconclusive || !hasReason(res, "an expected result is not confirmed") {
		t.Errorf("unconfirmed: %s %+v", res.Outcome, res.Reasons)
	}
	r.confirmSources(t, sc, domain.SourceDiff)
	if res := compute(sc, r.runOne(t, in), r.builds(t)); res.Outcome != domain.OutcomePassed {
		t.Errorf("confirmed: %s %+v", res.Outcome, res.Reasons)
	}
}

func TestResendRecordsAttempt(t *testing.T) {
	r := newRig(t, rigOptions{})
	run := replay.New(r.deps)
	sc, s := readLeg("/v1/orders/{{place.id}}")
	place, ps := placeLeg()
	if _, err := run.RunOne(t.Context(), replay.Single{Scenario: place, Script: ps}); err != nil {
		t.Fatal(err)
	}
	got, err := run.RunOne(t.Context(), replay.Single{Scenario: sc, Script: s, Vars: map[string]string{"place.id": "o-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Last == nil {
		t.Fatal("no last call")
	}
	ev, captured, err := run.Resend(t.Context(), *got.Last, 2, sc.Steps[0])
	if err != nil {
		t.Fatalf("Resend: %v", err)
	}
	if ev.Action == nil || ev.Action.Use.Attempt != 2 || ev.Action.Use.Step != "read" || ev.Action.Use.Scenario != sc.ID {
		t.Fatalf("action = %+v", ev.Action)
	}
	if len(ev.Checks) != 1 || ev.Checks[0].State != domain.CheckTrue {
		t.Errorf("checks = %+v", ev.Checks)
	}
	if len(captured) != 0 {
		t.Errorf("captured = %v", captured)
	}
	if got.Last.Use.Attempt != 0 {
		t.Errorf("Resend changed the call it was given: %+v", got.Last.Use)
	}
	files, err := filepath.Glob(filepath.Join(r.store.Root(), "runs", runID, "exchanges", string(sc.ID), "*-read.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("stored actions: %v %v", files, err)
	}
	var attempts []int
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var a domain.Action
		if err := json.Unmarshal(data, &a); err != nil {
			t.Fatal(err)
		}
		attempts = append(attempts, a.Use.Attempt)
	}
	if !slices.Equal(attempts, []int{0, 2}) {
		t.Errorf("attempts = %v", attempts)
	}
}

func TestResendReturnsCapturesAndEngineErrors(t *testing.T) {
	r := newRig(t, rigOptions{})
	run := replay.New(r.deps)
	place, ps := placeLeg()
	got, err := run.RunOne(t.Context(), replay.Single{Scenario: place, Script: ps})
	if err != nil {
		t.Fatal(err)
	}
	ev, captured, err := run.Resend(t.Context(), *got.Last, 2, place.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	if ev.Action == nil || captured["id"] != "o-2" {
		t.Errorf("action %+v captured %v", ev.Action, captured)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ev, _, err = run.Resend(ctx, *got.Last, 3, place.Steps[0])
	if !errors.Is(err, context.Canceled) || ev.Action != nil {
		t.Errorf("a cancelled resend: action %+v err %v", ev.Action, err)
	}
}

// strip keeps what must be the same whichever entry point sent the step.
type stripped struct {
	Use      domain.UseCase
	By       domain.Actor
	Seq      int
	Method   string
	Template string
	Body     string
	Status   int
	Response string
	Captures map[string]string
	Refused  string
	Err      string
	Why      string
}

func (r *rig) storedActions(t *testing.T, id domain.ScenarioID) []stripped {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(r.store.Root(), "runs", runID, "exchanges", string(id), "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	var out []stripped
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var a domain.Action
		if err := json.Unmarshal(data, &a); err != nil {
			t.Fatal(err)
		}
		s := stripped{Use: a.Use, By: a.By, Seq: a.Seq, Captures: a.Captures, Refused: a.Refused, Err: a.Err, Why: a.Why}
		if a.Request != nil {
			s.Method, s.Template, s.Body = a.Request.Method, a.Request.PathTemplate, string(a.Request.Body)
		}
		if a.Response != nil {
			s.Status, s.Response = a.Response.Status, string(a.Response.Body)
		}
		out = append(out, s)
	}
	return out
}

// RunOne on a scenario gives what Run gives: the same outcome, the same reasons and checks, and the same stored actions.
func TestRunOneEqualsRun(t *testing.T) {
	cases := []struct {
		name    string
		confirm bool
		mutate  func(*domain.Scenario, *domain.Script)
	}{
		{name: "passes", confirm: true},
		{name: "not confirmed"},
		{name: "a step has no request", confirm: true, mutate: func(_ *domain.Scenario, s *domain.Script) { s.Steps[1].Request = nil }},
		{name: "a capture finds nothing", confirm: true, mutate: func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Captures = []domain.Capture{{Name: "order_id", JSONPath: "$.data.nothing"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, s := loadScenario(t), loadScript(t)
			if tc.mutate != nil {
				tc.mutate(&sc, &s)
			}
			viaRun := newRig(t, rigOptions{})
			viaRun.putScript(t, s)
			viaOne := newRig(t, rigOptions{})
			if tc.confirm {
				viaRun.confirmAll(t, sc)
				viaOne.confirmAll(t, sc)
			}

			results, start, end, err := replay.New(viaRun.deps).Run(t.Context(), []domain.Scenario{sc})
			if err != nil {
				t.Fatal(err)
			}
			got, err := replay.New(viaOne.deps).RunOne(t.Context(), replay.Single{Scenario: sc, Script: s})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(start, end) {
				t.Fatalf("builds start %v end %v", start, end)
			}
			res := compute(sc, got, start)
			if !reflect.DeepEqual(results[0], res) {
				t.Errorf("Run gave\n%+v\nRunOne gave\n%+v", results[0], res)
			}
			if a, b := viaRun.storedActions(t, sc.ID), viaOne.storedActions(t, sc.ID); !reflect.DeepEqual(a, b) {
				t.Errorf("stored actions differ\nRun    %+v\nRunOne %+v", a, b)
			}
		})
	}
}

func TestRunOneDoesNotShareState(t *testing.T) {
	r := newRig(t, rigOptions{})
	eng := replay.New(r.deps)
	vars := map[string]string{"place.id": "o-1"}
	place, ps := placeLeg()
	if _, err := eng.RunOne(t.Context(), replay.Single{Scenario: place, Script: ps}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Go(func() {
			sc, s := readLeg("/v1/orders/{{place.id}}")
			sc.ID = domain.ScenarioID("checkout.read" + string(rune('a'+i)))
			s.Scenario, s.ExpectHash = sc.ID, domain.ExpectHash(sc)
			got, err := eng.RunOne(t.Context(), replay.Single{Scenario: sc, Script: s, Vars: vars})
			if err != nil || len(got.Blocked) != 0 || got.Steps["read"].Action == nil {
				t.Errorf("leg %d: err %v blocked %q", i, err, got.Blocked)
			}
		})
	}
	wg.Wait()
}
