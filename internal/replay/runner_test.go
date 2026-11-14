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
	deps    replay.Deps
}

type rigOptions struct {
	defects  []sampleapp.Defect
	mode     string
	timeout  time.Duration
	delay    time.Duration
	buildURL string                          // default: the sample application's /version
	wrap     func(http.Handler) http.Handler // wraps the sample application
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
	red, err := redact.New(config.Redact{}, func() []string { return []string{token} }, key)
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
	eng := api.New(api.Options{
		Policy:   pol,
		Recorder: recorder.New(run, red, classify, runID),
		Services: map[string]string{"orders": srv.URL},
		Session: func(context.Context, string) (string, string, error) {
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
		deps: replay.Deps{
			Engine:   eng,
			Scripts:  store,
			Builds:   prober,
			Services: []string{"orders"},
			Confirm:  conf,
			Trust:    []domain.Source{domain.SourceDiff, domain.SourceBaseline},
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
		err := r.confirm.Put(confirm.Record{
			Scenario:   sc.ID,
			ExpectHash: domain.ExpectHash(sc),
			User:       "tester",
			Identity:   "local, unverified identity",
			At:         time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
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
		return strings.Contains(r.Text, text) || strings.Contains(string(r.Step), text)
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
		check   func(t *testing.T, app *sampleapp.App)
	}{
		{name: "correct service", mode: "test_data", confirm: true, want: domain.OutcomePassed},
		{name: "refund above the total is accepted", defects: []sampleapp.Defect{sampleapp.RefundOverTotal}, mode: "test_data", confirm: true,
			want: domain.OutcomeFailed, reason: "refund-too-much"},
		{name: "refund answers 500", defects: []sampleapp.Defect{sampleapp.RefundServerError}, mode: "test_data", confirm: true,
			want: domain.OutcomeFailed, reason: "no server error"},
		{name: "nobody confirmed the diff result", mode: "test_data", confirm: false,
			want: domain.OutcomeInconclusive, reason: "an expected result is not confirmed"},
		{name: "build changed during the run", defects: []sampleapp.Defect{sampleapp.BuildChanges}, mode: "test_data", confirm: true,
			want: domain.OutcomeStale, reason: "the build changed during the run"},
		{name: "read-only environment", mode: "read_only", confirm: true,
			want: domain.OutcomeBlocked, reason: "the environment does not allow a step",
			check: func(t *testing.T, app *sampleapp.App) {
				if n := app.Requests("POST", "/v1/"); n != 0 {
					t.Fatalf("%d writes reached the service", n)
				}
			}},
		// The sample application delays only a refund it accepts, so the refund above the total must be accepted to be slow.
		{name: "write times out", defects: []sampleapp.Defect{sampleapp.SlowRefund, sampleapp.RefundOverTotal}, mode: "test_data",
			timeout: 300 * time.Millisecond, delay: time.Minute, confirm: true,
			want: domain.OutcomeInconclusive, reason: "a write timed out and its effect is unknown",
			check: func(t *testing.T, app *sampleapp.App) { // Review Focus 4
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
				tc.check(t, r.app)
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
