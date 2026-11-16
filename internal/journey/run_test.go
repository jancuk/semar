package journey

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/check"
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
	"github.com/jancuk/semar/internal/verdict"
)

const testRun = "r-1"

var testKey = []byte("0123456789abcdef0123456789abcdef")

// fakeClock moves only when the runner sleeps, so a wait of 30 seconds takes no time.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	return nil
}

// memorySink keeps every recorded action in memory as well as on disk.
type memorySink struct {
	recorder.Sink
	mu      sync.Mutex
	actions []domain.Action
}

func (s *memorySink) WriteAction(a domain.Action) (string, error) {
	s.mu.Lock()
	s.actions = append(s.actions, a)
	s.mu.Unlock()
	return s.Sink.WriteAction(a)
}

func (s *memorySink) of(scenario domain.ScenarioID) []domain.Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Action
	for _, a := range s.actions {
		if a.Use.Scenario == scenario {
			out = append(out, a)
		}
	}
	return out
}

type rigOptions struct {
	orders, payments []sampleapp.Defect
	paymentsBuild    string // default: the payments service's /version
	wrapOrders       func(http.Handler) http.Handler
	wrapPayments     func(http.Handler) http.Handler
	redact           config.Redact
	fixtures         Fixtures
	catalogue        Catalogue
	prober           func(build.Prober) build.Prober
	services         map[string]string
	accounts         []string // the auth profiles of the configuration; default: the two services' and bob
}

type rig struct {
	app      *sampleapp.App
	payments *sampleapp.Payments
	sink     *memorySink
	clock    *fakeClock
	runner   *Runner

	mu       sync.Mutex
	profiles []string // the auth profile of each request, in order
}

func defects(ds []sampleapp.Defect) map[sampleapp.Defect]bool {
	m := map[sampleapp.Defect]bool{}
	for _, d := range ds {
		m[d] = true
	}
	return m
}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	r := &rig{clock: &fakeClock{now: time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)}}
	r.app = sampleapp.New(sampleapp.Options{Defects: defects(o.orders)})
	r.payments = sampleapp.NewPayments(r.app, sampleapp.PaymentOptions{Defects: defects(o.payments)})
	var oh, ph http.Handler = r.app, r.payments
	if o.wrapOrders != nil {
		oh = o.wrapOrders(oh)
	}
	if o.wrapPayments != nil {
		ph = o.wrapPayments(ph)
	}
	osrv, psrv := httptest.NewServer(oh), httptest.NewServer(ph)
	t.Cleanup(osrv.Close)
	t.Cleanup(psrv.Close)
	ou, _ := url.Parse(osrv.URL)
	pu, _ := url.Parse(psrv.URL)

	pol, err := effects.New(config.Environment{
		Hosts:   []string{ou.Host, pu.Host},
		Methods: []string{"GET", "HEAD", "POST"},
		Effects: config.Effects{Mode: "test_data"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{"orders-tester": "tok-alice", "payments-tester": "tok-pay", "bob": "tok-pay"}
	red, err := redact.New(o.redact, func() []string { return []string{"tok-alice", "tok-pay"} }, testKey)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := evidence.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.NewRun(testRun)
	if err != nil {
		t.Fatal(err)
	}
	r.sink = &memorySink{Sink: run}
	classify := func(method string, u *url.URL) effects.Class {
		return pol.Classify(effects.Request{Method: method, URL: u})
	}
	eng := api.New(api.Options{
		Policy:   pol,
		Recorder: recorder.New(r.sink, red, classify, testRun),
		Services: map[string]string{"orders": osrv.URL, "payments": psrv.URL},
		Session: func(_ context.Context, profile string) (string, string, error) {
			r.mu.Lock()
			r.profiles = append(r.profiles, profile)
			r.mu.Unlock()
			return "Authorization", "Bearer " + tokens[profile], nil
		},
		Run:     testRun,
		Version: "test",
	})
	paymentsBuild := o.paymentsBuild
	if paymentsBuild == "" {
		paymentsBuild = psrv.URL + "/version"
	}
	var prober build.Prober = build.NewHTTP([]config.Service{
		{Name: "orders", Build: &config.BuildProbe{Probe: "http", URL: osrv.URL + "/version", Commit: "$.commit"}},
		{Name: "payments", Build: &config.BuildProbe{Probe: "http", URL: paymentsBuild, Commit: "$.commit"}},
	}, nil)
	if o.prober != nil {
		prober = o.prober(prober)
	}
	services := o.services
	if services == nil {
		services = map[string]string{"orders": "orders-tester", "payments": "payments-tester"}
	}
	accounts := o.accounts
	if accounts == nil {
		accounts = []string{"bob", "orders-tester", "payments-tester"}
	}
	r.runner = New(Deps{
		Replay: replay.New(replay.Deps{
			Engine: eng, Builds: prober, Confirm: confirm.NewLocal(root),
			Trust: []domain.Source{domain.SourceDiff, domain.SourceBaseline}, Run: testRun, Accounts: accounts,
		}),
		Accounts:  accounts,
		Fixtures:  o.fixtures,
		Catalogue: o.catalogue,
		Builds:    prober,
		Services:  services,
		Clock:     r.clock.Now,
		Sleep:     r.clock.Sleep,
	})
	return r
}

func (r *rig) run(t *testing.T, j domain.Journey) (domain.JourneyResult, []domain.ScenarioResult) {
	t.Helper()
	jr, results, err := r.runner.Run(t.Context(), j)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return jr, results
}

func parsed(t *testing.T, text string) domain.Journey {
	t.Helper()
	j, err := Parse([]byte(text), exampleName)
	if err != nil {
		t.Fatal(err)
	}
	if ps := Validate(j, orderPaid); len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	return j
}

func outcomes(legs []domain.LegResult) []string {
	out := make([]string, len(legs))
	for i, l := range legs {
		out[i] = string(l.Leg) + " " + string(l.Outcome)
	}
	return out
}

func hasReason(rs []domain.Reason, text string) bool {
	return slices.ContainsFunc(rs, func(r domain.Reason) bool { return r.Text == text })
}

func TestPassed(t *testing.T) {
	r := newRig(t, rigOptions{})
	jr, results := r.run(t, example(t))

	if jr.Outcome != domain.JourneyPassed || jr.FirstFailing != "" || len(jr.Reasons) != 0 {
		t.Fatalf("journey %s, first failing %q, reasons %+v; legs %+v", jr.Outcome, jr.FirstFailing, jr.Reasons, jr.Legs)
	}
	if got, want := outcomes(jr.Legs), []string{"place passed", "pay passed", "settled passed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("legs = %v, want %v", got, want)
	}
	var ids []domain.ScenarioID
	for _, res := range results {
		ids = append(ids, res.Scenario)
	}
	if want := []domain.ScenarioID{"checkout.place", "checkout.pay", "checkout.settled"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("results = %v", ids)
	}
	want := []domain.InvariantResult{{Index: 0, State: domain.CheckTrue, Values: []string{"1000", "1000", "1000"}}}
	if !reflect.DeepEqual(jr.Invariants, want) {
		t.Errorf("invariants = %+v", jr.Invariants)
	}
	if n := len(jr.Manifest.Services); n != 2 || jr.Manifest.Hash != domain.ManifestHash(jr.Manifest.Services) {
		t.Errorf("manifest = %+v", jr.Manifest)
	}
	if jr.ManifestEnd == nil || jr.ManifestEnd.Hash != jr.Manifest.Hash {
		t.Errorf("manifest at the end = %+v", jr.ManifestEnd)
	}
	// The payment settles on the second read, so the wait slept once, for its first interval.
	if !reflect.DeepEqual(r.clock.slept, []time.Duration{time.Second}) {
		t.Errorf("slept %v", r.clock.slept)
	}
	reads := r.sink.of("checkout.settled")
	if len(reads) != 2 || reads[0].Use.Attempt != 0 || reads[1].Use.Attempt != 2 {
		t.Fatalf("settled actions = %+v", reads)
	}
	if got := results[2].Actions; !reflect.DeepEqual(got, []domain.ActionID{reads[0].ID, reads[1].ID}) {
		t.Errorf("settled result actions = %v", got)
	}
	if !strings.HasSuffix(reads[1].Request.URL, "/v1/orders/o-1") {
		t.Errorf("settled read %s", reads[1].Request.URL)
	}
}

func TestFailedFirstLegStopsLater(t *testing.T) {
	r := newRig(t, rigOptions{})
	j := parsed(t, edited(t, "expect: [{ check: { status: 202 }, source: person }]", "expect: [{ check: { status: 201 }, source: person }]"))
	jr, results := r.run(t, j)

	if jr.Outcome != domain.JourneyFailed || jr.FirstFailing != "pay" {
		t.Fatalf("journey %s, first failing %q, reasons %+v", jr.Outcome, jr.FirstFailing, jr.Reasons)
	}
	if got, want := outcomes(jr.Legs), []string{"place passed", "pay failed", "settled not_run"}; !reflect.DeepEqual(got, want) {
		t.Errorf("legs = %v, want %v", got, want)
	}
	if !hasReason(jr.Legs[2].Reasons, "stopped after leg pay") {
		t.Errorf("settled reasons = %+v", jr.Legs[2].Reasons)
	}
	if !strings.HasPrefix(jr.Reasons[0].Text, "leg pay failed: step pay: expected status 201") {
		t.Errorf("reasons = %+v", jr.Reasons)
	}
	if len(jr.Invariants) != 0 {
		t.Errorf("invariants were evaluated after a failed leg: %+v", jr.Invariants)
	}
	if n := r.app.Requests("GET", "/v1/orders/"); n != 0 {
		t.Errorf("settled sent %d reads", n)
	}
	if results[2].Outcome != domain.OutcomeNotRun || len(results[2].Actions) != 0 {
		t.Errorf("settled result = %+v", results[2])
	}
}

func TestWaitTimeoutFails(t *testing.T) {
	r := newRig(t, rigOptions{payments: []sampleapp.Defect{sampleapp.PaymentNeverSettles}})
	jr, results := r.run(t, example(t))

	if jr.Outcome != domain.JourneyFailed || jr.FirstFailing != "settled" {
		t.Fatalf("journey %s, first failing %q, reasons %+v", jr.Outcome, jr.FirstFailing, jr.Reasons)
	}
	const waited = "waited 30s and $.data.status was never paid (6 attempts)"
	if jr.Reasons[0].Text != "leg settled failed: "+waited {
		t.Errorf("reasons = %+v", jr.Reasons)
	}
	if got := jr.Legs[2].Reasons[0]; got.Text != waited || got.Rule != domain.OutcomeFailed {
		t.Errorf("settled reasons = %+v", jr.Legs[2].Reasons)
	}
	// The intervals of a fixture's until: 1s, doubled, at most 15s, and never past the timeout.
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second}
	if !reflect.DeepEqual(r.clock.slept, want) {
		t.Errorf("slept %v, want %v", r.clock.slept, want)
	}
	reads := r.sink.of("checkout.settled")
	if len(reads) != 6 {
		t.Fatalf("%d attempts were recorded, want 6", len(reads))
	}
	var ids []domain.ActionID
	for i, a := range reads {
		if want := map[bool]int{true: 0, false: i + 1}[i == 0]; a.Use.Attempt != want {
			t.Errorf("attempt %d has Use.Attempt %d, want %d", i+1, a.Use.Attempt, want)
		}
		ids = append(ids, a.ID)
	}
	if !reflect.DeepEqual(results[2].Actions, ids) {
		t.Errorf("settled actions = %v, want every attempt %v", results[2].Actions, ids)
	}
}

func TestInvariantFalse(t *testing.T) {
	r := newRig(t, rigOptions{payments: []sampleapp.Defect{sampleapp.PaymentAmountWrong}})
	jr, _ := r.run(t, example(t))

	if jr.Outcome != domain.JourneyFailed || jr.FirstFailing != "" {
		t.Fatalf("journey %s, first failing %q, reasons %+v, legs %v", jr.Outcome, jr.FirstFailing, jr.Reasons, outcomes(jr.Legs))
	}
	want := []domain.InvariantResult{{Index: 0, State: domain.CheckFalse, Values: []string{"1000", "1001", "1000"}}}
	if !reflect.DeepEqual(jr.Invariants, want) {
		t.Errorf("invariants = %+v", jr.Invariants)
	}
	if !hasReason(jr.Reasons, "invariant 1 is false: 1000, 1001, 1000") {
		t.Errorf("reasons = %+v", jr.Reasons)
	}
}

func TestInvariantMarkerBlocks(t *testing.T) {
	r := newRig(t, rigOptions{redact: config.Redact{Patterns: []string{`\b1000\b`}}})
	jr, _ := r.run(t, example(t))

	if jr.Outcome != domain.JourneyBlocked {
		t.Fatalf("journey %s, reasons %+v, legs %+v", jr.Outcome, jr.Reasons, jr.Legs)
	}
	if !hasReason(jr.Reasons, "invariant 1 could not be evaluated: a value holds a redaction marker") {
		t.Errorf("reasons = %+v", jr.Reasons)
	}
	for _, v := range jr.Invariants[0].Values {
		if !redact.HasMarker(v) || strings.Contains(v, "1000") {
			t.Errorf("a shown value is %q, want a redaction marker", v)
		}
	}
}

func TestManifestProbeFailsBlocks(t *testing.T) {
	r := newRig(t, rigOptions{paymentsBuild: "http://127.0.0.1:1/version"})
	jr, results := r.run(t, example(t))

	if jr.Outcome != domain.JourneyBlocked || len(jr.Reasons) != 1 || jr.Reasons[0].Text != "the build of payments could not be read" {
		t.Fatalf("journey %s, reasons %+v", jr.Outcome, jr.Reasons)
	}
	if got, want := outcomes(jr.Legs), []string{"place not_run", "pay not_run", "settled not_run"}; !reflect.DeepEqual(got, want) {
		t.Errorf("legs = %v", got)
	}
	if n := r.app.Requests("", "/v1/") + r.payments.Requests("", "/v1/"); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
	if len(r.sink.actions) != 0 || len(results) != 3 {
		t.Errorf("%d actions recorded, %d results", len(r.sink.actions), len(results))
	}
}

func TestBuildChangesStale(t *testing.T) {
	r := newRig(t, rigOptions{orders: []sampleapp.Defect{sampleapp.BuildChanges}})
	jr, _ := r.run(t, example(t))

	if jr.Outcome != domain.JourneyStale {
		t.Fatalf("journey %s, reasons %+v", jr.Outcome, jr.Reasons)
	}
	if jr.Reasons[0].Text != "a build changed during the journey: orders 9f3c2ab then c0ffee1" {
		t.Errorf("reasons = %+v", jr.Reasons)
	}
	if jr.FirstFailing != "place" || jr.ManifestEnd == nil || jr.ManifestEnd.Hash == jr.Manifest.Hash {
		t.Errorf("first failing %q, end %+v", jr.FirstFailing, jr.ManifestEnd)
	}
	// Every leg ran: the change was found only at the end.
	for _, l := range jr.Legs {
		if l.Outcome != domain.OutcomeStale {
			t.Errorf("leg %s is %s, want stale", l.Leg, l.Outcome)
		}
	}
}

// fakeFixtures resolves to fixed values and counts the cleanups.
type fakeFixtures struct {
	mu        sync.Mutex
	res       *fixture.Resolution
	err       error
	requests  []fixture.Request
	released  int
	cancelled error // the error of the context of Release, when it was called
}

func (f *fakeFixtures) Resolve(_ context.Context, req fixture.Request) (*fixture.Resolution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	return f.res, f.err
}

func (f *fakeFixtures) Release(ctx context.Context, _ *fixture.Resolution) []domain.FixtureRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released++
	f.cancelled = ctx.Err()
	return []domain.FixtureRecord{{As: "paid", Fixture: "order.paid", Via: fixture.ViaCreate, Cleanup: fixture.CleanupDone}}
}

const withGiven = "criticality: critical\ngiven:\n  - fixture: order.paid\n    as: paid\n"

func TestFixtureBlocks(t *testing.T) {
	f := &fakeFixtures{res: &fixture.Resolution{Blocked: []string{"fixture order.paid is missing from .semar/fixtures"}}}
	r := newRig(t, rigOptions{fixtures: f})
	j := parsed(t, edited(t, "criticality: critical\n", withGiven))
	jr, _ := r.run(t, j)

	if jr.Outcome != domain.JourneyBlocked || jr.Reasons[0].Text != "fixture order.paid is missing from .semar/fixtures" {
		t.Fatalf("journey %s, reasons %+v", jr.Outcome, jr.Reasons)
	}
	if slices.ContainsFunc(jr.Reasons, func(x domain.Reason) bool { return x.Rule == domain.OutcomeFailed }) {
		t.Errorf("a fixture failure gave a failed reason: %+v", jr.Reasons)
	}
	if n := r.app.Requests("", "/v1/") + r.payments.Requests("", "/v1/"); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
	if f.released != 1 || len(jr.Fixtures) != 1 {
		t.Errorf("released %d times, fixtures %+v", f.released, jr.Fixtures)
	}
	if req := f.requests[0]; req.Owner != "checkout" || req.Pass != domain.PassSetup || len(req.Given) != 1 {
		t.Errorf("request = %+v", req)
	}

	t.Run("no fixture resolver", func(t *testing.T) {
		r := newRig(t, rigOptions{})
		jr, _ := r.run(t, j)
		if jr.Outcome != domain.JourneyBlocked || jr.Reasons[0].Text != reasonNoFixtures {
			t.Errorf("journey %s, reasons %+v", jr.Outcome, jr.Reasons)
		}
	})
}

func TestPerServiceSession(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.run(t, example(t))
	want := []string{"orders-tester", "payments-tester", "orders-tester", "orders-tester"}
	if !reflect.DeepEqual(r.profiles, want) {
		t.Errorf("profiles = %v, want %v", r.profiles, want)
	}

	t.Run("the journey's profile for a service with none", func(t *testing.T) {
		r := newRig(t, rigOptions{services: map[string]string{"payments": "payments-tester"}})
		j := parsed(t, edited(t, "criticality: critical\n", "criticality: critical\nauthProfile: orders-tester\n"))
		jr, _ := r.run(t, j)
		if jr.Outcome != domain.JourneyPassed {
			t.Fatalf("journey %s, reasons %+v", jr.Outcome, jr.Reasons)
		}
		if !reflect.DeepEqual(r.profiles, want) {
			t.Errorf("profiles = %v, want %v", r.profiles, want)
		}
	})
}

func TestCleanupAfterFailedLeg(t *testing.T) {
	f := &fakeFixtures{res: &fixture.Resolution{Vars: map[string]string{"paid.id": "o-9"}}}
	r := newRig(t, rigOptions{fixtures: f})
	j := parsed(t, edited(t, "expect: [{ check: { status: 202 }, source: person }]", "expect: [{ check: { status: 201 }, source: person }]"))
	j.Given = []domain.Given{{Fixture: "order.paid", As: "paid"}}
	jr, _ := r.run(t, j)

	if jr.Outcome != domain.JourneyFailed {
		t.Fatalf("journey %s, reasons %+v", jr.Outcome, jr.Reasons)
	}
	if f.released != 1 || len(f.requests) != 1 {
		t.Fatalf("resolved %d times, released %d times", len(f.requests), f.released)
	}
	if !reflect.DeepEqual(jr.Fixtures, []domain.FixtureRecord{{As: "paid", Fixture: "order.paid", Via: fixture.ViaCreate, Cleanup: fixture.CleanupDone}}) {
		t.Errorf("fixtures = %+v", jr.Fixtures)
	}
}

func TestCleanupAfterInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := &fakeFixtures{res: &fixture.Resolution{Vars: map[string]string{}}}
	r := newRig(t, rigOptions{fixtures: f, wrapPayments: func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/v1/payments" {
				cancel()
			}
			h.ServeHTTP(w, req)
		})
	}})
	j := example(t)
	j.Given = []domain.Given{{Fixture: "order.paid", As: "paid"}}
	jr, results, err := r.runner.Run(ctx, j)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if f.released != 1 || f.cancelled != nil {
		t.Errorf("released %d times, on a context with error %v", f.released, f.cancelled)
	}
	if jr.Legs[0].Outcome != domain.OutcomePassed || len(results[0].Actions) != 1 {
		t.Errorf("the evidence of the finished leg is gone: %+v %+v", jr.Legs[0], results[0])
	}
	if jr.Legs[2].Outcome != domain.OutcomeNotRun || !hasReason(jr.Legs[2].Reasons, reasonInterrupted) {
		t.Errorf("settled = %+v", jr.Legs[2])
	}
	if jr.Outcome == domain.JourneyPassed || jr.ManifestEnd != nil {
		t.Errorf("journey %s, end %+v", jr.Outcome, jr.ManifestEnd)
	}
	if n := r.app.Requests("GET", "/v1/orders/"); n != 0 {
		t.Errorf("%d reads after the interrupt", n)
	}
}

func TestInterruptBeforeStartSendsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := newRig(t, rigOptions{})
	jr, results, err := r.runner.Run(ctx, example(t))
	if !errors.Is(err, context.Canceled) || len(results) != 3 || jr.Outcome == domain.JourneyPassed {
		t.Fatalf("err %v, %d results, journey %s", err, len(results), jr.Outcome)
	}
	if len(r.sink.actions) != 0 {
		t.Errorf("%d actions recorded", len(r.sink.actions))
	}
	if jr.Outcome != domain.JourneyNotRun || hasReason(jr.Reasons, reasonNoEnd) {
		t.Errorf("a journey that sent nothing is %s with reasons %+v, want not_run", jr.Outcome, jr.Reasons)
	}
}

// countingProber counts the probes of each service.
type countingProber struct {
	build.Prober
	mu    sync.Mutex
	calls map[string]int
}

func (c *countingProber) Probe(ctx context.Context, service string) (domain.BuildRef, error) {
	c.mu.Lock()
	c.calls[service]++
	c.mu.Unlock()
	if service == "inventory" {
		return domain.BuildRef{Service: "inventory", Commit: "1234abc"}, nil
	}
	return c.Prober.Probe(ctx, service)
}

type catalogue map[domain.FixtureID]domain.Fixture

func (c catalogue) Get(id domain.FixtureID) (domain.Fixture, bool) { f, ok := c[id]; return f, ok }

func TestManifestHasFixtureServices(t *testing.T) {
	var cp *countingProber
	f := &fakeFixtures{res: &fixture.Resolution{Vars: map[string]string{}}}
	r := newRig(t, rigOptions{
		fixtures: f,
		catalogue: catalogue{"order.paid": {ID: "order.paid", Create: []domain.FixtureStep{
			{Request: domain.APIRequest{Service: "inventory", Method: "POST", Path: "/v1/stock"}},
		}}},
		prober: func(p build.Prober) build.Prober {
			cp = &countingProber{Prober: p, calls: map[string]int{}}
			return cp
		},
	})
	j := example(t)
	j.Given = []domain.Given{{Fixture: "order.paid", As: "paid"}}
	jr, _ := r.run(t, j)

	var names []string
	for _, b := range jr.Manifest.Services {
		names = append(names, b.Service)
	}
	if !reflect.DeepEqual(names, []string{"inventory", "orders", "payments"}) {
		t.Errorf("manifest services = %v", names)
	}
	if want := map[string]int{"inventory": 2, "orders": 2, "payments": 2}; !reflect.DeepEqual(cp.calls, want) {
		t.Errorf("probes = %v, want each service at the start and at the end", cp.calls)
	}
	if jr.Outcome != domain.JourneyPassed {
		t.Errorf("journey %s, reasons %+v", jr.Outcome, jr.Reasons)
	}
}

// fakeSingle passes every step of every leg and captures what captures holds for its scenario.
type fakeSingle struct {
	mu       sync.Mutex
	captures map[domain.ScenarioID]map[string]string
	vars     map[domain.ScenarioID]map[string]string
	n        int
}

func (f *fakeSingle) RunOne(_ context.Context, in replay.Single) (replay.Replayed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.vars == nil {
		f.vars = map[domain.ScenarioID]map[string]string{}
	}
	f.vars[in.Scenario.ID] = in.Vars
	rep := replay.Replayed{Steps: map[domain.StepID]verdict.StepEvidence{}, Captured: map[string]string{}, Confirmed: func(domain.Expectation) bool { return true }}
	for _, st := range in.Scenario.Steps {
		f.n++
		a := domain.Action{
			ID:       domain.ActionID("a-" + strconv.Itoa(f.n)),
			Use:      domain.UseCase{Pass: domain.PassScenario, Scenario: in.Scenario.ID, Step: st.ID},
			Response: &domain.HTTPResponse{Status: 200},
			Captures: f.captures[in.Scenario.ID],
		}
		ev := verdict.StepEvidence{Action: &a, Invariants: []check.Result{{State: domain.CheckTrue}}}
		for range st.Expect {
			ev.Checks = append(ev.Checks, check.Result{State: domain.CheckTrue})
		}
		rep.Steps[st.ID] = ev
	}
	maps.Copy(rep.Captured, f.captures[in.Scenario.ID])
	return rep, nil
}

func (f *fakeSingle) Resend(context.Context, api.Call, int, domain.Step) (verdict.StepEvidence, map[string]string, error) {
	return verdict.StepEvidence{}, nil, errors.New("no wait in this journey")
}

type staticProber struct{}

func (staticProber) Probe(_ context.Context, service string) (domain.BuildRef, error) {
	return domain.BuildRef{Service: service, Commit: "9f3c2ab"}, nil
}

// branching has a leg c that needs nothing, after a and b, where b needs a.
const branching = `apiVersion: semar/v1
kind: Journey
id: shop
title: Branches
owner: team-commerce
criticality: standard
legs:
  - id: a
    surface: api
    service: orders
    steps:
      - { id: s, action: Create, hint: { method: POST, path: /v1/orders }, request: { method: POST, path: /v1/orders }, captures: [{ name: id, jsonpath: "$.id" }], expect: [{ check: { status: 200 }, source: person }] }
    outputs: [id]
  - id: c
    surface: api
    service: orders
    steps:
      - { id: s, action: Read, hint: { method: GET, path: /v1/orders }, request: { method: GET, path: /v1/orders }, captures: [{ name: id, jsonpath: "$.id" }], expect: [{ check: { status: 200 }, source: person }] }
    outputs: [id]
  - id: b
    surface: api
    service: payments
    needs: [a]
    steps:
      - { id: s, action: Pay, hint: { method: GET, path: "/v1/orders/{id}" }, request: { method: GET, path: "/v1/orders/{{a.id}}" }, captures: [{ name: id, jsonpath: "$.id" }], expect: [{ check: { status: 200 }, source: person }] }
    outputs: [id]
  - id: d
    surface: api
    service: payments
    needs: [b]
    steps:
      - { id: s, action: Read, hint: { method: GET, path: "/v1/orders/{id}" }, request: { method: GET, path: "/v1/orders/{{a.id}}" }, expect: [{ check: { status: 200 }, source: person }] }
`

func fakeRunner(f *fakeSingle) *Runner {
	c := &fakeClock{}
	return New(Deps{Replay: f, Builds: staticProber{}, Clock: c.Now, Sleep: c.Sleep})
}

func TestLegReadsOnlyTheLegsItNeeds(t *testing.T) {
	j, err := Parse([]byte(branching), "shop.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ps := Validate(j, nil); len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	f := &fakeSingle{captures: map[domain.ScenarioID]map[string]string{
		"shop.a": {"id": "o-1"}, "shop.b": {"id": "p-1"}, "shop.c": {"id": "o-2"},
	}}
	jr, _, err := fakeRunner(f).Run(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	if jr.Outcome != domain.JourneyPassed {
		t.Fatalf("journey %s, reasons %+v", jr.Outcome, jr.Reasons)
	}
	if got := outcomes(jr.Legs); !reflect.DeepEqual(got, []string{"a passed", "c passed", "b passed", "d passed"}) {
		t.Errorf("run order = %v", got)
	}
	want := map[domain.ScenarioID]map[string]string{
		"shop.a": {},
		"shop.c": {},
		"shop.b": {"a.id": "o-1"},
		"shop.d": {"a.id": "o-1", "b.id": "p-1"}, // a through b; never c, which d does not need
	}
	if !reflect.DeepEqual(f.vars, want) {
		t.Errorf("vars = %v, want %v", f.vars, want)
	}
}

func TestMissingOutputBlocks(t *testing.T) {
	j, err := Parse([]byte(branching), "shop.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSingle{captures: map[domain.ScenarioID]map[string]string{}}
	jr, _, err := fakeRunner(f).Run(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	if jr.Outcome != domain.JourneyBlocked || jr.FirstFailing != "a" {
		t.Fatalf("journey %s, first failing %q", jr.Outcome, jr.FirstFailing)
	}
	if want := "leg a ended blocked: output id has no value"; jr.Reasons[0].Text != want {
		t.Errorf("reasons = %+v, want %q", jr.Reasons, want)
	}
	if got := outcomes(jr.Legs); !reflect.DeepEqual(got, []string{"a blocked", "c not_run", "b not_run", "d not_run"}) {
		t.Errorf("legs = %v", got)
	}
}

// A wait that ends on an error answer has not seen its condition hold. A leg whose expectations
// are met by that answer must not pass for it.
func TestWaitEndedByAnErrorAnswerFails(t *testing.T) {
	var reads atomic.Int32
	r := newRig(t, rigOptions{wrapOrders: func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/v1/orders/") && reads.Add(1) > 1 {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			h.ServeHTTP(w, req)
		})
	}})
	text := edited(t, "        captures: [{ name: amount, jsonpath: \"$.data.amount\" }]\n        expect: [{ check: { jsonpath: \"$.data.status\", equals: paid }, source: person }]",
		"        expect: [{ check: { status: 503 }, source: person }]")
	text = strings.Replace(text, "    outputs: [amount]\ninvariants:\n  - equal: [\"{{place.amount}}\", \"{{pay.amount}}\", \"{{settled.amount}}\"]", "invariants:\n  - equal: [\"{{place.amount}}\", \"{{pay.amount}}\"]", 1)
	jr, results := r.run(t, parsed(t, text))

	if jr.Outcome != domain.JourneyFailed || jr.FirstFailing != "settled" {
		t.Fatalf("journey %s, first failing %q, reasons %+v, legs %v", jr.Outcome, jr.FirstFailing, jr.Reasons, outcomes(jr.Legs))
	}
	if got := jr.Legs[2].Reasons[0]; got.Text != "wait ended: answered 503" || got.Rule != domain.OutcomeFailed {
		t.Errorf("settled reasons = %+v", jr.Legs[2].Reasons)
	}
	if results[2].Outcome != domain.OutcomeFailed {
		t.Errorf("settled result = %s", results[2].Outcome)
	}
}

func (r *rig) sentAs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.profiles)
}

func TestLegAsSendsThatSessionOnItsStepsOnly(t *testing.T) {
	r := newRig(t, rigOptions{})
	j := example(t)
	j.Legs[1].As = "bob"
	jr, _ := r.run(t, j)
	if jr.Outcome != domain.JourneyPassed {
		t.Fatalf("journey %s: %+v", jr.Outcome, jr.Reasons)
	}
	for _, l := range jr.Legs {
		var want []string
		if l.Leg == "pay" {
			want = []string{"bob"}
		}
		if !slices.Equal(l.As, want) {
			t.Errorf("leg %s acted as %v, want %v", l.Leg, l.As, want)
		}
	}
	// leg settled waits, so it sends more than one request, all as its own profile
	if got, want := r.sentAs(), []string{"orders-tester", "bob", "orders-tester", "orders-tester"}; !slices.Equal(got, want) {
		t.Errorf("sessions = %v, want %v", got, want)
	}
}

func TestStepAsBeatsTheLegAs(t *testing.T) {
	r := newRig(t, rigOptions{})
	j := example(t)
	j.Legs[1].As = "bob"
	j.Legs[1].Steps[0].As = "payments-tester"
	jr, _ := r.run(t, j)
	if jr.Outcome != domain.JourneyPassed {
		t.Fatalf("journey %s: %+v", jr.Outcome, jr.Reasons)
	}
	if got, want := r.sentAs(), []string{"orders-tester", "payments-tester", "orders-tester", "orders-tester"}; !slices.Equal(got, want) {
		t.Errorf("sessions = %v, want %v", got, want)
	}
}

func TestAnUnknownAccountBlocksTheJourneyBeforeAnyRequest(t *testing.T) {
	for name, edit := range map[string]func(j *domain.Journey){
		"on a leg":  func(j *domain.Journey) { j.Legs[2].As = "dave" },
		"on a step": func(j *domain.Journey) { j.Legs[2].Steps[0].As = "dave" },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, rigOptions{})
			j := example(t)
			edit(&j)
			jr, results := r.run(t, j)
			if jr.Outcome != domain.JourneyBlocked {
				t.Fatalf("journey %s: %+v", jr.Outcome, jr.Reasons)
			}
			if got := r.sentAs(); len(got) != 0 || r.app.Requests("", "/v1/") != 0 {
				t.Errorf("requests were sent: %v", got)
			}
			if len(jr.Reasons) == 0 || jr.Reasons[0].Text != "account dave is not in the configuration" {
				t.Errorf("reasons = %+v", jr.Reasons)
			}
			if got := outcomes(jr.Legs); !slices.Equal(got, []string{"place not_run", "pay not_run", "settled not_run"}) {
				t.Errorf("legs = %v", got)
			}
			if len(results) != 3 {
				t.Errorf("%d results", len(results))
			}
		})
	}
}
