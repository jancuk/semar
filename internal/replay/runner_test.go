package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/replay"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/script"
)

const (
	runID = "r-1"
	token = "tok-alice"
)

var key = []byte("0123456789abcdef0123456789abcdef")

// rig is one replay against a fresh sample application.
type rig struct {
	app     *sampleapp.App
	url     string // of the sample application
	store   *evidence.Store
	confirm *confirm.Local
	policy  *effects.Policy
	deps    replay.Deps
}

type rigOptions struct {
	defects  []sampleapp.Defect
	mode     string
	timeout  time.Duration
	delay    time.Duration
	buildURL string                          // default: the sample application's /version
	wrap     func(http.Handler) http.Handler // wraps the sample application
	sink     func(recorder.Sink) recorder.Sink
	redact   config.Redact
	sessions map[string]string // profile to token; the default is the token of every profile
}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	defects := map[sampleapp.Defect]bool{}
	for _, d := range o.defects {
		defects[d] = true
	}
	app := sampleapp.New(sampleapp.Options{Defects: defects, Delay: o.delay})
	var h http.Handler = app
	if o.wrap != nil {
		h = o.wrap(app)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	mode := o.mode
	if mode == "" {
		mode = "test_data"
	}
	pol, err := effects.New(config.Environment{
		Hosts:   []string{u.Host},
		Methods: []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"},
		Effects: config.Effects{Mode: mode},
	})
	if err != nil {
		t.Fatal(err)
	}
	red, err := redact.New(o.redact, func() []string {
		out := []string{token}
		for _, v := range o.sessions {
			out = append(out, v)
		}
		return out
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := evidence.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.NewRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	classify := func(method string, u *url.URL) effects.Class {
		return pol.Classify(effects.Request{Method: method, URL: u})
	}
	var sink recorder.Sink = run
	if o.sink != nil {
		sink = o.sink(run)
	}
	eng := api.New(api.Options{
		Policy:   pol,
		Recorder: recorder.New(sink, red, classify, runID),
		Services: map[string]string{"orders": srv.URL},
		Session: func(_ context.Context, profile string) (string, string, error) {
			if v, ok := o.sessions[profile]; ok {
				return "Authorization", "Bearer " + v, nil
			}
			return "Authorization", "Bearer " + token, nil
		},
		Run:     runID,
		Version: "test",
		Timeout: o.timeout,
	})
	buildURL := o.buildURL
	if buildURL == "" {
		buildURL = srv.URL + "/version"
	}
	prober := build.NewHTTP([]config.Service{{
		Name:  "orders",
		Build: &config.BuildProbe{Probe: "http", URL: buildURL, Commit: "$.commit"},
	}}, nil)
	conf := confirm.NewLocal(root)
	return &rig{
		app:     app,
		url:     srv.URL,
		store:   store,
		confirm: conf,
		policy:  pol,
		deps: replay.Deps{
			Engine:   eng,
			Scripts:  store,
			Builds:   prober,
			Services: []string{"orders"},
			Confirm:  conf,
			Trust:    []domain.Source{domain.SourceDiff, domain.SourceBaseline},
			Run:      runID,
		},
	}
}

func loadScenario(t *testing.T) domain.Scenario {
	t.Helper()
	p := filepath.Join("testdata", "SHOP-482-01.scenario.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scenario.Parse(data, p)
	if err != nil {
		t.Fatal(err)
	}
	if ps := scenario.Validate(sc, scenario.FromFile); len(ps) > 0 {
		t.Fatalf("scenario problems: %v", ps)
	}
	return sc
}

func loadScript(t *testing.T) domain.Script {
	t.Helper()
	p := filepath.Join("testdata", "SHOP-482-01.script.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s, err := script.Parse(data, p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// copyScenario gives the scenario and its script another ID. The expectation hash does not cover the ID.
func copyScenario(sc domain.Scenario, s domain.Script, id domain.ScenarioID) (domain.Scenario, domain.Script) {
	sc.ID = id
	s.Scenario = id
	return sc, s
}

func (r *rig) putScript(t *testing.T, s domain.Script) {
	t.Helper()
	if err := r.store.PutScript(s); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) confirmAll(t *testing.T, scs ...domain.Scenario) {
	t.Helper()
	for _, sc := range scs {
		r.confirmSources(t, sc, domain.SourceDiff, domain.SourceBaseline)
	}
}

func (r *rig) confirmSources(t *testing.T, sc domain.Scenario, sources ...domain.Source) {
	t.Helper()
	err := r.confirm.Put(confirm.Record{
		Scenario:   sc.ID,
		ExpectHash: domain.ExpectHash(sc),
		User:       "tester",
		Identity:   "local, unverified identity",
		At:         time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		Sources:    sources,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (r *rig) run(t *testing.T, scs ...domain.Scenario) ([]domain.ScenarioResult, []domain.BuildRef, []domain.BuildRef) {
	t.Helper()
	results, start, end, err := replay.New(r.deps).Run(t.Context(), scs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != len(scs) {
		t.Fatalf("got %d results for %d scenarios", len(results), len(scs))
	}
	return results, start, end
}

// storedAction reads the one stored action of a step.
func (r *rig) storedAction(t *testing.T, id domain.ScenarioID, step domain.StepID) domain.Action {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(r.store.Root(), "runs", runID, "exchanges", string(id), "*-"+string(step)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("step %s of %s has %d stored actions, want 1", step, id, len(files))
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var a domain.Action
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func hasReason(res domain.ScenarioResult, text string) bool {
	return slices.ContainsFunc(res.Reasons, func(r domain.Reason) bool {
		return strings.Contains(r.Text, text)
	})
}

func expect(t *testing.T, res domain.ScenarioResult, outcome domain.Outcome, reason string) {
	t.Helper()
	if res.Outcome != outcome {
		t.Errorf("outcome = %s, want %s; reasons %+v", res.Outcome, outcome, res.Reasons)
	}
	if reason != "" && !hasReason(res, reason) {
		t.Errorf("no reason holds %q; reasons %+v", reason, res.Reasons)
	}
}

func TestReplay(t *testing.T) {
	cases := []struct {
		name    string
		defects []sampleapp.Defect
		mode    string
		timeout time.Duration
		delay   time.Duration
		confirm bool
		want    domain.Outcome
		reason  string
		check   func(t *testing.T, app *sampleapp.App, res domain.ScenarioResult)
	}{
		{name: "correct service", mode: "test_data", confirm: true, want: domain.OutcomePassed,
			check: func(t *testing.T, _ *sampleapp.App, res domain.ScenarioResult) {
				if len(res.Reasons) != 0 {
					t.Errorf("a passed result has reasons: %+v", res.Reasons)
				}
			}},
		{name: "refund above the total is accepted", defects: []sampleapp.Defect{sampleapp.RefundOverTotal}, mode: "test_data", confirm: true,
			want: domain.OutcomeFailed, reason: "step refund-too-much: expected status 422, observed status 201 (source ticket)"},
		{name: "refund answers 500", defects: []sampleapp.Defect{sampleapp.RefundServerError}, mode: "test_data", confirm: true,
			want: domain.OutcomeFailed, reason: "no server error"},
		{name: "nobody confirmed the diff result", mode: "test_data", confirm: false,
			want: domain.OutcomeInconclusive, reason: "an expected result is not confirmed"},
		{name: "build changed during the run", defects: []sampleapp.Defect{sampleapp.BuildChanges}, mode: "test_data", confirm: true,
			want: domain.OutcomeStale, reason: "the build changed during the run"},
		{name: "read-only environment", mode: "read_only", confirm: true,
			want: domain.OutcomeBlocked, reason: "the environment does not allow a step",
			check: func(t *testing.T, app *sampleapp.App, _ domain.ScenarioResult) {
				if n := app.Requests("POST", "/v1/"); n != 0 {
					t.Fatalf("%d writes reached the service", n)
				}
			}},
		// The sample application delays only a refund it accepts, so the refund above the total must be accepted to be slow.
		{name: "write times out", defects: []sampleapp.Defect{sampleapp.SlowRefund, sampleapp.RefundOverTotal}, mode: "test_data",
			timeout: 2 * time.Second, delay: time.Minute, confirm: true,
			want: domain.OutcomeInconclusive, reason: "a write timed out and its effect is unknown",
			check: func(t *testing.T, app *sampleapp.App, _ domain.ScenarioResult) { // Review Focus 4
				if n := app.Requests("POST", "/v1/orders/o-1/refunds"); n != 1 {
					t.Fatalf("the refund was sent %d times", n)
				}
				if n := app.Requests("GET", "/v1/orders/"); n != 0 {
					t.Fatalf("the readback was sent %d times after the write timed out", n)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, rigOptions{defects: tc.defects, mode: tc.mode, timeout: tc.timeout, delay: tc.delay})
			sc := loadScenario(t)
			r.putScript(t, loadScript(t))
			if tc.confirm {
				r.confirmAll(t, sc)
			}
			results, start, end := r.run(t, sc)
			expect(t, results[0], tc.want, tc.reason)
			if len(start) != 1 || len(end) != 1 {
				t.Errorf("builds start %v end %v, want one each", start, end)
			}
			if tc.check != nil {
				tc.check(t, r.app, results[0])
			}
		})
	}
}

func TestStoredActionsAreRedactedAndBelongToTheirStep(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	r.putScript(t, loadScript(t))
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomePassed, "")
	if len(results[0].Actions) != 3 {
		t.Fatalf("actions = %v, want 3", results[0].Actions)
	}
	for _, st := range sc.Steps {
		a := r.storedAction(t, sc.ID, st.ID)
		want := domain.UseCase{Pass: domain.PassScenario, Scenario: sc.ID, Step: st.ID}
		if st.Setup {
			want.Pass = domain.PassSetup
		}
		if a.Use != want || a.By != domain.ActorReplay {
			t.Errorf("step %s: use %+v by %q, want %+v by replay", st.ID, a.Use, a.By, want)
		}
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), token) {
			t.Errorf("step %s: the stored action holds the session token", st.ID)
		}
	}
	if got := r.storedAction(t, sc.ID, "refund-too-much").Request.PathTemplate; got != "/v1/orders/{id}/refunds" {
		t.Errorf("path template = %q, want the step's hint", got)
	}
}

func TestMissingScriptIsBlocked(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomeBlocked, "no script was accepted")
	if n := r.app.Requests("", "/v1/"); n != 0 {
		t.Fatalf("%d requests reached the service", n)
	}
}

func TestScriptThatDoesNotFitIsBlocked(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	s := loadScript(t)
	s.Steps[1].Request.Method = "PUT"
	r.putScript(t, s)
	r.confirmAll(t, sc)
	problems := script.Validate(sc, s)
	if len(problems) == 0 {
		t.Fatal("the changed script has no problem")
	}
	results, _, _ := r.run(t, sc)
	for _, p := range problems {
		expect(t, results[0], domain.OutcomeBlocked, p.Message)
	}
	if n := r.app.Requests("", "/v1/"); n != 0 {
		t.Fatalf("%d requests reached the service", n)
	}
}

func TestProbeFailureIsBlocked(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	r := newRig(t, rigOptions{buildURL: closed.URL + "/version"})
	sc := loadScenario(t)
	other, otherScript := copyScenario(sc, loadScript(t), "SHOP-482-02")
	r.putScript(t, loadScript(t))
	r.putScript(t, otherScript)
	r.confirmAll(t, sc, other)
	results, start, end := r.run(t, sc, other)
	for _, res := range results {
		expect(t, res, domain.OutcomeBlocked, "the build of orders could not be read")
	}
	if len(start) != 0 || len(end) != 0 {
		t.Errorf("builds start %v end %v, want none", start, end)
	}
	if n := r.app.Requests("", "/"); n != 0 {
		t.Fatalf("%d requests reached the service", n)
	}
}

// failsAfterFirst answers a build probe once and fails every later probe.
func failsAfterFirst(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) > 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"commit":"9f3c2ab"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEndProbeFailureIsNotAPass(t *testing.T) {
	cases := []struct {
		name      string
		services  int
		wantStart int
		wantEnd   int
	}{
		{"the only service", 1, 1, 0},
		// The end list then differs from the start list, and must not read as a changed build.
		{"one of two services", 2, 2, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			failing := failsAfterFirst(t, &calls)
			r := newRig(t, rigOptions{})
			services := []config.Service{{Name: "payments", Build: &config.BuildProbe{Probe: "http", URL: failing.URL, Commit: "$.commit"}}}
			r.deps.Services = []string{"payments"}
			if tc.services == 2 {
				services = append(services, config.Service{Name: "orders", Build: &config.BuildProbe{Probe: "http", URL: r.url + "/version", Commit: "$.commit"}})
				r.deps.Services = append(r.deps.Services, "orders")
			}
			r.deps.Builds = build.NewHTTP(services, nil)
			sc := loadScenario(t)
			r.putScript(t, loadScript(t))
			r.confirmAll(t, sc)
			results, start, end := r.run(t, sc)
			expect(t, results[0], domain.OutcomeInconclusive, "the build of payments could not be read after the run")
			if hasReason(results[0], "the build changed during the run") {
				t.Errorf("a build that could not be read is reported as changed: %+v", results[0].Reasons)
			}
			if len(start) != tc.wantStart || len(end) != tc.wantEnd {
				t.Errorf("builds start %v end %v, want %d and %d", start, end, tc.wantStart, tc.wantEnd)
			}
			if n := calls.Load(); n != 2 {
				t.Errorf("the build was probed %d times, want 2", n)
			}
		})
	}
}

func TestCancelledRun(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := newRig(t, rigOptions{
		defects: []sampleapp.Defect{sampleapp.SlowRefund, sampleapp.RefundOverTotal},
		delay:   time.Minute,
		wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if strings.HasSuffix(req.URL.Path, "/refunds") {
					cancel()
				}
				next.ServeHTTP(w, req)
			})
		},
	})
	sc := loadScenario(t)
	other, otherScript := copyScenario(sc, loadScript(t), "SHOP-482-02")
	r.putScript(t, loadScript(t))
	r.putScript(t, otherScript)
	r.confirmAll(t, sc, other)
	r.deps.Workers = 1

	results, start, end, err := replay.New(r.deps).Run(ctx, []domain.Scenario{sc, other})
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("err = %v, want the context's error", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Scenario != sc.ID || results[1].Scenario != other.ID {
		t.Fatalf("results are for %s and %s", results[0].Scenario, results[1].Scenario)
	}
	expect(t, results[0], domain.OutcomeInconclusive, "a write timed out and its effect is unknown")
	expect(t, results[1], domain.OutcomeNotRun, "the run was interrupted")
	if len(start) != 1 || len(end) != 0 {
		t.Errorf("builds start %v end %v, want one and none", start, end)
	}
	if n := r.app.Requests("GET", "/version"); n != 1 {
		t.Errorf("the build was probed %d times, want 1", n)
	}
	if n := r.app.Requests("POST", "/v1/orders/o-1/refunds"); n != 1 {
		t.Errorf("the refund was sent %d times", n)
	}
	if n := r.app.Requests("", "/v1/orders/o-2"); n != 0 {
		t.Errorf("the second scenario sent %d requests", n)
	}
}

func TestMissingCaptureStopsTheScenario(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	s := loadScript(t)
	s.Steps[0].Captures[0].JSONPath = "$.data.nope"
	r.putScript(t, s)
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomeInconclusive, "capture order_id found no value")
	if n := r.app.Requests("POST", "/v1/orders"); n != 1 {
		t.Errorf("the order was created %d times, want 1", n)
	}
	if n := r.app.Requests("", "/v1/orders/"); n != 0 {
		t.Errorf("%d later steps were sent", n)
	}
}

func TestEngineErrorStopsTheScenario(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	s := loadScript(t)
	s.Steps[1].Request.Service = "payments"
	r.putScript(t, s)
	r.confirmAll(t, sc)
	results, _, end, err := replay.New(r.deps).Run(t.Context(), []domain.Scenario{sc})
	if err == nil || !strings.Contains(err.Error(), "payments") || errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the engine's error", err)
	}
	if len(results) != 1 || len(end) != 1 {
		t.Fatalf("got %d results and end builds %v", len(results), end)
	}
	expect(t, results[0], domain.OutcomeInconclusive, "step refund-too-much could not be sent")
	if n := r.app.Requests("", "/v1/orders/"); n != 0 {
		t.Errorf("%d later steps were sent", n)
	}
}

func TestResultsKeepTheInputOrder(t *testing.T) {
	r := newRig(t, rigOptions{})
	base, baseScript := loadScenario(t), loadScript(t)
	var scs []domain.Scenario
	for i, id := range []domain.ScenarioID{"SHOP-482-05", "SHOP-482-01", "SHOP-482-04", "SHOP-482-02", "SHOP-482-03"} {
		sc, s := copyScenario(base, baseScript, id)
		if i%2 == 0 {
			r.putScript(t, s)
		}
		r.confirmAll(t, sc)
		scs = append(scs, sc)
	}
	r.deps.Workers = 3
	results, _, _ := r.run(t, scs...)
	for i, res := range results {
		if res.Scenario != scs[i].ID {
			t.Errorf("result %d is for %s, want %s", i, res.Scenario, scs[i].ID)
		}
		want := domain.OutcomeBlocked
		if i%2 == 0 {
			want = domain.OutcomePassed
		}
		expect(t, res, want, "")
	}
}

func TestScenariosDoNotShareData(t *testing.T) { // Q2
	r := newRig(t, rigOptions{})
	one, oneScript := copyScenario(loadScenario(t), loadScript(t), "SHOP-482-01")
	two, twoScript := copyScenario(loadScenario(t), loadScript(t), "SHOP-482-02")
	r.putScript(t, oneScript)
	r.putScript(t, twoScript)
	r.confirmAll(t, one, two)
	r.deps.Workers = 2
	results, _, _ := r.run(t, one, two)
	for _, res := range results {
		expect(t, res, domain.OutcomePassed, "")
	}
	first := r.storedAction(t, one.ID, "order-unchanged").Request.URL
	second := r.storedAction(t, two.ID, "order-unchanged").Request.URL
	if first == second {
		t.Fatalf("both readbacks read %s", first)
	}
}

func TestStepWithNoRequestStopsTheScenario(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *domain.Script)
	}{
		{"the script leaves the step out", func(s *domain.Script) { s.Steps = s.Steps[1:] }},
		{"the script binds the step with no request", func(s *domain.Script) {
			s.Steps[0].Request, s.Steps[0].Captures = nil, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, rigOptions{})
			sc := loadScenario(t)
			s := loadScript(t)
			tc.mutate(&s)
			s.Steps[len(s.Steps)-2].Request.Path = "/v1/orders/o-1/refunds"
			s.Steps[len(s.Steps)-1].Request.Path = "/v1/orders/o-1"
			if problems := script.Validate(sc, s); len(problems) > 0 {
				t.Fatalf("the script has problems: %v", problems)
			}
			r.putScript(t, s)
			r.confirmAll(t, sc)
			results, _, _ := r.run(t, sc)
			if results[0].Outcome == domain.OutcomePassed {
				t.Errorf("outcome = passed; reasons %+v", results[0].Reasons)
			}
			if !hasReason(results[0], "step create-order has no request in the script, so the later steps were not sent") {
				t.Errorf("no reason names the step; reasons %+v", results[0].Reasons)
			}
			if n := r.app.Requests("POST", ""); n != 0 {
				t.Errorf("%d POST requests reached the service, want 0", n)
			}
			if n := r.app.Requests("", "/v1/"); n != 0 {
				t.Errorf("%d later steps were sent", n)
			}
		})
	}
}

// failingSink fails the nth action it is asked to write, after it has run before.
type failingSink struct {
	recorder.Sink
	n      int
	before func()
	calls  int
}

func (f *failingSink) WriteAction(a domain.Action) (string, error) {
	f.calls++
	if f.calls == f.n {
		if f.before != nil {
			f.before()
		}
		return "", errors.New("disk full")
	}
	return f.Sink.WriteAction(a)
}

const capturedID = "ID7x9Qk2"

// returnsCapturedID answers the order the setup step creates with a known id.
func returnsCapturedID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/orders" {
			_, _ = w.Write([]byte(`{"data":{"id":"` + capturedID + `"}}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TestRecorderFailureKeepsCapturedValuesOutOfReasonsAndErrors(t *testing.T) {
	r := newRig(t, rigOptions{
		wrap: returnsCapturedID,
		sink: func(s recorder.Sink) recorder.Sink { return &failingSink{Sink: s, n: 2} },
	})
	sc := loadScenario(t)
	s := loadScript(t)
	s.Steps[1].Request.Query = map[string]string{"order": "{{order_id}}"}
	r.putScript(t, s)
	r.confirmAll(t, sc)
	results, _, _, err := replay.New(r.deps).Run(t.Context(), []domain.Scenario{sc})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want the recorder's error", err)
	}
	if strings.Contains(err.Error(), capturedID) {
		t.Errorf("the returned error holds the captured value: %v", err)
	}
	if !hasReason(results[0], "step refund-too-much could not be sent or recorded") {
		t.Errorf("no reason says the step could not be sent or recorded: %+v", results[0].Reasons)
	}
	for _, reason := range results[0].Reasons {
		if strings.Contains(reason.Text, capturedID) {
			t.Errorf("a reason holds the captured value: %q", reason.Text)
		}
	}
	if n := r.app.Requests("GET", "/v1/orders/"); n != 0 {
		t.Errorf("the readback was sent %d times", n)
	}
}

func TestRecorderFailureDuringACancellationIsNotDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := newRig(t, rigOptions{
		sink: func(s recorder.Sink) recorder.Sink { return &failingSink{Sink: s, n: 2, before: cancel} },
	})
	sc := loadScenario(t)
	r.putScript(t, loadScript(t))
	r.confirmAll(t, sc)
	results, _, _, err := replay.New(r.deps).Run(ctx, []domain.Scenario{sc})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want the recorder's error as well as the cancellation", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the context's error", err)
	}
	if !hasReason(results[0], "step refund-too-much could not be sent or recorded") {
		t.Errorf("the recorder failure left no reason: %+v", results[0].Reasons)
	}
}

func TestScenarioCutOffBetweenStepsIsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := newRig(t, rigOptions{
		wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req)
				if req.Method == "POST" && req.URL.Path == "/v1/orders" {
					cancel()
				}
			})
		},
	})
	sc := loadScenario(t)
	r.putScript(t, loadScript(t))
	r.confirmAll(t, sc)
	results, _, _, err := replay.New(r.deps).Run(ctx, []domain.Scenario{sc})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context's error", err)
	}
	if results[0].Outcome == domain.OutcomePassed {
		t.Errorf("outcome = passed; reasons %+v", results[0].Reasons)
	}
	if !hasReason(results[0], "the run was interrupted") {
		t.Errorf("no reason says the run was interrupted: %+v", results[0].Reasons)
	}
	if n := r.app.Requests("POST", "/v1/orders/"); n != 0 {
		t.Errorf("the refund was sent %d times after the cancellation", n)
	}
}

func TestChecksReadTheRedactedAction(t *testing.T) {
	r := newRig(t, rigOptions{redact: config.Redact{JSONPaths: []string{"$.data.refunded_total"}}})
	sc := loadScenario(t)
	r.putScript(t, loadScript(t))
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	if results[0].Outcome == domain.OutcomePassed {
		t.Fatalf("outcome = passed; the check read the raw value: %+v", results[0].Reasons)
	}
	idx := slices.IndexFunc(results[0].Checks, func(c domain.CheckResult) bool { return c.Step == "order-unchanged" && c.Index == 0 })
	if idx < 0 {
		t.Fatalf("no result for the check on the masked value: %+v", results[0].Checks)
	}
	if got := results[0].Checks[idx].State; got != domain.CheckNotEvaluated {
		t.Errorf("state = %s, want %s", got, domain.CheckNotEvaluated)
	}
	if body := string(r.storedAction(t, sc.ID, "order-unchanged").Response.Body); strings.Contains(body, `"refunded_total":0`) {
		t.Errorf("the stored body holds the value: %s", body)
	}
}

// withBaseline adds an expectation of the baseline source, so that the hash changes too.
func withBaseline(sc domain.Scenario) domain.Scenario {
	steps := slices.Clone(sc.Steps)
	last := steps[len(steps)-1]
	last.Expect = append(slices.Clone(last.Expect), domain.Expectation{
		Check:  domain.Check{Status: new(200)},
		Source: domain.SourceBaseline,
	})
	steps[len(steps)-1] = last
	sc.Steps = steps
	return sc
}

func TestAConfirmationCoversOnlyTheSourcesItNames(t *testing.T) {
	sc := withBaseline(loadScenario(t))
	s := loadScript(t)
	s.ExpectHash = domain.ExpectHash(sc)

	t.Run("a source confirmed under a narrower trust list is not confirmed", func(t *testing.T) {
		r := newRig(t, rigOptions{})
		r.deps.Trust = []domain.Source{domain.SourceDiff}
		r.putScript(t, s)
		r.confirmSources(t, sc, domain.SourceDiff)
		if res, _, _ := r.run(t, sc); res[0].Outcome != domain.OutcomePassed {
			t.Fatalf("the narrow trust run = %s, reasons %+v", res[0].Outcome, res[0].Reasons)
		}

		r.deps.Trust = []domain.Source{domain.SourceDiff, domain.SourceBaseline}
		res, _, _ := r.run(t, sc)
		expect(t, res[0], domain.OutcomeInconclusive, "an expected result is not confirmed")
	})

	t.Run("a record without sources confirms nothing", func(t *testing.T) {
		r := newRig(t, rigOptions{})
		r.putScript(t, s)
		dir := filepath.Join(r.store.Root(), "store", "confirmations", string(sc.ID), string(domain.ExpectHash(sc)))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		old := `{"schema": 1, "scenario": "` + string(sc.ID) + `", "expectHash": "` + string(domain.ExpectHash(sc)) + `", "user": "old", "identity": "local, unverified identity", "at": "2026-11-14T10:00:00Z"}`
		if err := os.WriteFile(filepath.Join(dir, "old.json"), []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		res, _, _ := r.run(t, sc)
		expect(t, res[0], domain.OutcomeInconclusive, "an expected result is not confirmed")
	})
}

const givenID domain.ScenarioID = "SHOP-482-05"

func loadGivenScenario(t *testing.T) domain.Scenario {
	t.Helper()
	p := filepath.Join("testdata", "SHOP-482-05.scenario.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scenario.Parse(data, p)
	if err != nil {
		t.Fatal(err)
	}
	if ps := scenario.Validate(sc, scenario.FromFile); len(ps) > 0 {
		t.Fatalf("scenario problems: %v", ps)
	}
	return sc
}

// givenScript reads the order that the fixture made, at path.
func givenScript(sc domain.Scenario, path string) domain.Script {
	return domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc),
		AuthoredBy: domain.ProviderRef{Instance: "hand", Model: "none", Harness: "none"},
		Steps: []domain.ScriptStep{{
			Step:    "read-order",
			Request: &domain.APIRequest{Service: "orders", Method: "GET", Path: path},
		}},
	}
}

func loadFixture(t *testing.T) domain.Fixture {
	t.Helper()
	p := filepath.Join("testdata", "order.new.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	f, err := fixture.Parse(data, p)
	if err != nil {
		t.Fatal(err)
	}
	if ps := fixture.Validate(f, []string{"orders"}); len(ps) > 0 {
		t.Fatalf("fixture problems: %v", ps)
	}
	return f
}

// withFixtures gives the rig a real resolver over the one fixture of testdata, through the rig's engine.
func (r *rig) withFixtures(t *testing.T, approved bool) {
	t.Helper()
	f := loadFixture(t)
	if approved {
		err := r.confirm.PutFixture(confirm.FixtureApproval{
			Fixture: f.ID, Version: f.Version, Hash: domain.FixtureHash(f), User: "tester",
			Identity: "local, unverified identity", At: time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	r.deps.Fixtures = fixture.New(fixture.Deps{
		Engine:      r.deps.Engine,
		Policy:      r.policy,
		Catalogue:   fixture.Catalogue{Fixtures: []domain.Fixture{f}},
		Approvals:   r.confirm,
		Health:      fixture.NewHealthStore(r.store.Root()),
		Environment: "local",
		Services:    map[string]string{"orders": r.url},
		Run:         runID,
	})
}

func (r *rig) givenScenario(t *testing.T, path string) domain.Scenario {
	t.Helper()
	sc := loadGivenScenario(t)
	r.putScript(t, givenScript(sc, path))
	return sc
}

// fixtureActions counts the stored actions that a fixture sent, by phase.
func (r *rig) fixtureActions(t *testing.T) map[string]int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(r.store.Root(), "runs", runID, "exchanges", string(givenID), "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var a domain.Action
		if err := json.Unmarshal(data, &a); err != nil {
			t.Fatal(err)
		}
		switch {
		case a.Use.Fixture == nil:
			out["step"]++
		default:
			out[string(a.Use.Pass)+" "+a.Use.Fixture.Phase]++
			if a.Use.Step != "" {
				t.Errorf("a fixture action names step %s", a.Use.Step)
			}
		}
	}
	return out
}

func TestGivenBlocksWithoutCatalogue(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := r.givenScenario(t, "/v1/orders/{{order.id}}")
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomeBlocked, "the scenario names fixtures, and this run has no fixture catalogue")
	if n := r.app.Requests("", "/v1/orders"); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestBadScriptBlocksBeforeFixtures(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.withFixtures(t, true)
	sc := r.givenScenario(t, "/v1/orders/{{order.total}}")
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomeBlocked, "order.total is not an output of fixture order.new; its outputs are id")
	if n := r.app.Requests("", "/v1/orders"); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
	if len(results[0].Fixtures) != 0 {
		t.Errorf("fixtures = %+v", results[0].Fixtures)
	}
}

func TestFixtureBlockedSendsNoStep(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.withFixtures(t, false)
	sc := r.givenScenario(t, "/v1/orders/{{order.id}}")
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomeBlocked, "fixture order.new is not approved: run semar confirm --fixture order.new")
	if n := r.app.Requests("", "/v1/orders"); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
	if len(results[0].Actions) != 0 {
		t.Errorf("scenario actions = %v", results[0].Actions)
	}
	if got := results[0].Fixtures; len(got) != 1 || got[0].Via != fixture.ViaNone {
		t.Errorf("fixtures = %+v", got)
	}
}

func TestFixtureOutputsReachScript(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.withFixtures(t, true)
	sc := r.givenScenario(t, "/v1/orders/{{order.id}}")
	r.run(t, sc)
	a := r.storedAction(t, sc.ID, "read-order")
	if !strings.HasSuffix(a.Request.URL, "/v1/orders/o-1") {
		t.Errorf("the step was sent to %s", a.Request.URL)
	}
}

func TestFixtureRecordsInResult(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.withFixtures(t, true)
	sc := r.givenScenario(t, "/v1/orders/{{order.id}}")
	results, _, _ := r.run(t, sc)
	f := loadFixture(t)
	want := []domain.FixtureRecord{{
		As: "order", Fixture: "order.new", Version: "1.0.0", Hash: domain.FixtureHash(f), Via: fixture.ViaCreate,
		Outputs: map[string]string{"id": "o-1"}, Cleanup: fixture.CleanupDone, Actions: []domain.ActionID{"a-0001", "a-0003"},
	}}
	if got := results[0].Fixtures; !reflect.DeepEqual(want, got) {
		t.Errorf("fixtures = %+v\nwant       %+v", got, want)
	}
	if got := results[0].Actions; !slices.Equal(got, []domain.ActionID{"a-0002"}) {
		t.Errorf("scenario actions = %v, want only the step", got)
	}
	want2 := map[string]int{"setup create": 1, "step": 1, "cleanup cleanup": 1}
	if got := r.fixtureActions(t); !reflect.DeepEqual(want2, got) {
		t.Errorf("stored actions = %v, want %v", got, want2)
	}
}

func TestFixtureActionsAreNotStepEvidence(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.withFixtures(t, true)
	sc := r.givenScenario(t, "/v1/orders/{{order.id}}")
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomePassed, "")
	if hasReason(results[0], "does not match") {
		t.Errorf("a fixture action was taken for step evidence: %+v", results[0].Reasons)
	}
}

func TestCleanupAfterFailedStep(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.withFixtures(t, true)
	sc := loadGivenScenario(t)
	status := 404
	sc.Steps[0].Expect = sc.Steps[0].Expect[:1]
	sc.Steps[0].Expect[0].Check.Status = &status
	r.putScript(t, givenScript(sc, "/v1/orders/{{order.id}}"))
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomeFailed, "")
	if n := r.app.Requests("POST", "/v1/orders/o-1/cancel"); n != 1 {
		t.Errorf("cleanup was sent %d times, want once", n)
	}
	if got := results[0].Fixtures; len(got) != 1 || got[0].Cleanup != fixture.CleanupDone {
		t.Errorf("fixtures = %+v", got)
	}
}

func TestCleanupAfterInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	r := newRig(t, rigOptions{wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == "GET" && req.URL.Path == "/v1/orders/o-1" {
				cancel()
			}
			next.ServeHTTP(w, req)
		})
	}})
	r.withFixtures(t, true)
	sc := r.givenScenario(t, "/v1/orders/{{order.id}}")
	results, _, _, err := replay.New(r.deps).Run(ctx, []domain.Scenario{sc})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a cancelled run", err)
	}
	if n := r.app.Requests("POST", "/v1/orders/o-1/cancel"); n != 1 {
		t.Errorf("cleanup was sent %d times after the interrupt, want once", n)
	}
	if got := results[0].Fixtures; len(got) != 1 || got[0].Cleanup != fixture.CleanupDone {
		t.Errorf("fixtures = %+v", got)
	}
}

func TestRunIDVariable(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	s := loadScript(t)
	s.Steps[0].Request.JSON = map[string]any{"amount": 100000, "ref": "semar-{{run.id}}"}
	r.putScript(t, s)
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomePassed, "")
	if a := r.storedAction(t, sc.ID, "create-order"); !strings.Contains(string(a.Request.Body), `"ref":"semar-r-1"`) {
		t.Errorf("body = %s", a.Request.Body)
	}
}
