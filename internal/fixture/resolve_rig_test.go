package fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/sampleapp"
)

const (
	testRun   = "r-1"
	testEnv   = "uat"
	testOwner = "SHOP-482-05"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

var tokens = map[string]string{"alice": "tok-alice", "payments-tester": "tok-pay"}

// fakeClock moves only when the resolver sleeps, so no test waits.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
	// onSleep runs after each wait, with the number of waits so far.
	onSleep func(n int)
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)}
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
	c.now = c.now.Add(d)
	c.waits = append(c.waits, d)
	n, hook := len(c.waits), c.onSleep
	c.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return nil
}

func (c *fakeClock) Waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.waits)
}

// collect keeps every stored action in memory, as the evidence run wrote it.
type collect struct {
	recorder.Sink
	mu      sync.Mutex
	actions []domain.Action
}

func (c *collect) WriteAction(a domain.Action) (string, error) {
	c.mu.Lock()
	c.actions = append(c.actions, a)
	c.mu.Unlock()
	return c.Sink.WriteAction(a)
}

func (c *collect) all() []domain.Action {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.actions)
}

type rigOptions struct {
	defects      []sampleapp.Defect
	settle       int
	effects      config.Effects // Mode test_data when empty
	wrapOrders   func(http.Handler) http.Handler
	wrapPayments func(http.Handler) http.Handler
	fixtures     []domain.Fixture // default: the example, approved
	unapproved   bool
	noLeases     bool
	timeout      time.Duration
}

type rig struct {
	t         *testing.T
	app       *sampleapp.App
	pay       *sampleapp.Payments
	ordersURL string
	payURL    string
	sink      *collect
	clock     *fakeClock
	approvals *confirm.Local
	health    *HealthStore
	leases    *Leases
	deps      Deps
}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	defects := map[sampleapp.Defect]bool{}
	for _, d := range o.defects {
		defects[d] = true
	}
	app := sampleapp.New(sampleapp.Options{Defects: defects})
	pay := sampleapp.NewPayments(app, sampleapp.PaymentOptions{Defects: defects, SettleAfterReads: o.settle})
	var oh, ph http.Handler = app, pay
	if o.wrapOrders != nil {
		oh = o.wrapOrders(app)
	}
	if o.wrapPayments != nil {
		ph = o.wrapPayments(pay)
	}
	osrv, psrv := httptest.NewServer(oh), httptest.NewServer(ph)
	t.Cleanup(osrv.Close)
	t.Cleanup(psrv.Close)

	eff := o.effects
	if eff.Mode == "" {
		eff.Mode = "test_data"
	}
	pol, err := effects.New(config.Environment{
		Hosts:   []string{host(t, osrv.URL), host(t, psrv.URL)},
		Methods: []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"},
		Effects: eff,
	})
	if err != nil {
		t.Fatal(err)
	}
	red, err := redact.New(config.Redact{}, func() []string { return []string{"tok-alice", "tok-pay"} }, testKey)
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
	sink := &collect{Sink: run}
	classify := func(method string, u *url.URL) effects.Class {
		return pol.Classify(effects.Request{Method: method, URL: u})
	}
	clock := newClock()
	eng := api.New(api.Options{
		Policy:   pol,
		Recorder: recorder.New(sink, red, classify, testRun),
		Services: map[string]string{"orders": osrv.URL, "payments": psrv.URL},
		Session: func(_ context.Context, profile string) (string, string, error) {
			tok, ok := tokens[profile]
			if !ok {
				return "", "", fmt.Errorf("no secret for auth profile %s", profile)
			}
			return "Authorization", "Bearer " + tok, nil
		},
		Run:     testRun,
		Version: "test",
		Clock:   clock.Now,
		Timeout: o.timeout,
	})

	fixtures := o.fixtures
	if fixtures == nil {
		fixtures = []domain.Fixture{example(t)}
	}
	approvals := confirm.NewLocal(root)
	if !o.unapproved {
		for _, f := range fixtures {
			approve(t, approvals, f)
		}
	}
	var leases *Leases
	if !o.noLeases {
		if leases, err = OpenLeases(t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	slices.SortFunc(fixtures, func(a, b domain.Fixture) int { return strings.Compare(string(a.ID), string(b.ID)) })
	health := NewHealthStore(root)
	return &rig{
		t: t, app: app, pay: pay, ordersURL: osrv.URL, payURL: psrv.URL, sink: sink, clock: clock,
		approvals: approvals, health: health, leases: leases,
		deps: Deps{
			Engine:      eng,
			Policy:      pol,
			Catalogue:   Catalogue{Fixtures: fixtures},
			Approvals:   approvals,
			Health:      health,
			Leases:      leases,
			Environment: testEnv,
			EnvServices: []string{"orders", "payments"},
			ServiceAuth: map[string]string{"payments": "payments-tester"},
			Data:        map[string]string{"customers.default": "c-1"},
			Run:         testRun,
			Clock:       clock.Now,
			Sleep:       clock.Sleep,
		},
	}
}

func host(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func approve(t *testing.T, l *confirm.Local, f domain.Fixture) {
	t.Helper()
	err := l.PutFixture(confirm.FixtureApproval{
		Fixture: f.ID, Version: f.Version, Hash: domain.FixtureHash(f), User: "tester",
		Identity: "local, unverified identity", At: time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (r *rig) resolver() *Resolver { return New(r.deps) }

func request(given ...domain.Given) Request {
	return Request{Owner: testOwner, Given: given, AuthProfile: "alice", Pass: domain.PassSetup}
}

var orderGiven = domain.Given{Fixture: "order.paid", As: "order"}

func (r *rig) resolve(req Request) *Resolution {
	r.t.Helper()
	res, err := r.resolver().Resolve(r.t.Context(), req)
	if err != nil {
		r.t.Fatalf("Resolve: %v", err)
	}
	if res == nil {
		r.t.Fatal("Resolve returned no resolution")
	}
	return res
}

// seedPaidOrder makes a paid order of alice for the customer, outside the resolver.
func (r *rig) seedPaidOrder(customer string) string {
	r.t.Helper()
	var created struct {
		Data struct{ ID string } `json:"data"`
	}
	r.call("POST", r.ordersURL+"/v1/orders", "tok-alice", map[string]any{"amount": 1000, "customer": customer}, &created)
	r.call("POST", r.payURL+"/v1/payments", "tok-pay", map[string]any{"orderId": created.Data.ID}, nil)
	for range 5 {
		var read struct {
			Data struct{ Status string } `json:"data"`
		}
		r.call("GET", r.ordersURL+"/v1/orders/"+created.Data.ID, "tok-alice", nil, &read)
		if read.Data.Status == "paid" {
			return created.Data.ID
		}
	}
	r.t.Fatalf("order %s never became paid", created.Data.ID)
	return ""
}

func (r *rig) orderStatus(id string) string {
	r.t.Helper()
	var read struct {
		Data struct{ Status string } `json:"data"`
	}
	r.call("GET", r.ordersURL+"/v1/orders/"+id, "tok-alice", nil, &read)
	return read.Data.Status
}

func (r *rig) call(method, u, token string, body any, out any) {
	r.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(r.t.Context(), method, u, rd)
	if err != nil {
		r.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode > 299 {
		r.t.Fatalf("%s %s answered %d", method, u, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			r.t.Fatal(err)
		}
	}
}

// sent is the number of requests that reached either service, the seeding ones included.
func (r *rig) sent(method, prefix string) int {
	return r.app.Requests(method, prefix) + r.pay.Requests(method, prefix)
}

func phases(actions []domain.Action) []string {
	out := make([]string, len(actions))
	for i, a := range actions {
		u := a.Use
		label := string(u.Pass) + " "
		if u.Fixture != nil {
			label += fmt.Sprintf("%s/%s %d", u.Fixture.As, u.Fixture.Phase, u.Fixture.Index)
		}
		if u.Attempt > 0 {
			label += fmt.Sprintf(" try %d", u.Attempt)
		}
		out[i] = label
	}
	return out
}

func wantBlocked(t *testing.T, res *Resolution, want string) {
	t.Helper()
	if len(res.Blocked) != 1 || res.Blocked[0] != want {
		t.Fatalf("Blocked = %q\nwant      [%q]", res.Blocked, want)
	}
	if len(res.Vars) != 0 {
		t.Errorf("a blocked resolution exposes %v", res.Vars)
	}
}

// diff is "" when the values are deeply equal, and both values otherwise.
func diff(want, got any) string {
	if reflect.DeepEqual(want, got) {
		return ""
	}
	return fmt.Sprintf("want %#v\ngot  %#v", want, got)
}
