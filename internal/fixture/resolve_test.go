package fixture

import (
	"context"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/verdict"
)

func TestFindReusesRecord(t *testing.T) {
	r := newRig(t, rigOptions{})
	id := r.seedPaidOrder("c-1")
	before := r.sent("POST", "")

	res := r.resolve(request(orderGiven))
	if len(res.Blocked) > 0 {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	if got := res.Vars["order.id"]; got != id {
		t.Errorf("order.id = %q, want %q", got, id)
	}
	if n := r.sent("POST", "") - before; n != 0 {
		t.Errorf("%d writes were sent for a record that find reused", n)
	}
	records := r.resolver().Release(t.Context(), res)
	f := example(t)
	want := []domain.FixtureRecord{{
		As: "order", Fixture: "order.paid", Version: "1.0.0", Hash: domain.FixtureHash(f), Via: "find",
		Outputs: map[string]string{"id": id}, Cleanup: "not_needed", Actions: []domain.ActionID{"a-0001"},
	}}
	if diff := diff(want, records); diff != "" {
		t.Errorf("records (-want +got):\n%s", diff)
	}
	if r.sent("POST", "/v1/orders/"+id+"/cancel") != 0 || r.orderStatus(id) != sampleapp.StatusPaid {
		t.Error("cleanup touched a record that find reused")
	}
}

func TestCreateWhenFindEmpty(t *testing.T) {
	r := newRig(t, rigOptions{})
	res := r.resolve(request(orderGiven))
	if len(res.Blocked) > 0 {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	if got := res.Vars["order.id"]; got != "o-1" {
		t.Errorf("order.id = %q", got)
	}
	if r.orderStatus("o-1") != sampleapp.StatusPaid {
		t.Error("the order is not paid")
	}
	records := r.resolver().Release(t.Context(), res)
	if r.orderStatus("o-1") != sampleapp.StatusCancelled {
		t.Error("cleanup did not cancel the order")
	}

	actions := r.sink.all()
	want := []string{
		"setup order/find 1",
		"setup order/create 1",
		"setup order/create 2",
		"setup order/create 3 try 1",
		"setup order/create 3 try 2",
		"cleanup order/cleanup 1",
	}
	if diff := diff(want, phases(actions)); diff != "" {
		t.Errorf("actions (-want +got):\n%s", diff)
	}
	for _, a := range actions {
		if a.Use.Fixture == nil || a.Use.Step != "" || a.Use.Scenario != testOwner || a.Use.Fixture.ID != "order.paid" {
			t.Errorf("action %s has use %+v", a.ID, a.Use)
		}
	}
	if !strings.Contains(string(actions[1].Request.Body), `"ref":"semar-r-1"`) {
		t.Errorf("create body %s does not carry the run ID", actions[1].Request.Body)
	}
	rec := records[0]
	if rec.Via != "create" || rec.Cleanup != "done" || len(rec.Left) != 0 || len(rec.Actions) != 6 {
		t.Errorf("record = %+v", rec)
	}
	if diff := diff(map[string]string{"id": "o-1"}, rec.Outputs); diff != "" {
		t.Errorf("outputs (-want +got):\n%s", diff)
	}
}

func TestUntilPollsWithInjectedSleep(t *testing.T) {
	cases := []struct {
		name   string
		settle int
		every  time.Duration
		waits  []time.Duration
	}{
		{"settles on the second read", 2, time.Second, []time.Duration{time.Second}},
		{"each wait doubles", 3, time.Second, []time.Duration{time.Second, 2 * time.Second}},
		{"the first wait is 2s by default", 2, 0, []time.Duration{2 * time.Second}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := example(t)
			f.Create[2].Until.Every = c.every
			r := newRig(t, rigOptions{settle: c.settle, fixtures: []domain.Fixture{f}})
			res := r.resolve(request(orderGiven))
			if len(res.Blocked) > 0 {
				t.Fatalf("Blocked = %q", res.Blocked)
			}
			if diff := diff(c.waits, r.clock.Waits()); diff != "" {
				t.Errorf("waits (-want +got):\n%s", diff)
			}
			var attempts []int
			for _, a := range r.sink.all() {
				if a.Use.Fixture.Phase == domain.PhaseCreate && a.Use.Fixture.Index == 3 {
					attempts = append(attempts, a.Use.Attempt)
				}
			}
			want := make([]int, len(c.waits)+1)
			for i := range want {
				want[i] = i + 1
			}
			if diff := diff(want, attempts); diff != "" {
				t.Errorf("attempts (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUntilTimeout(t *testing.T) {
	r := newRig(t, rigOptions{defects: []sampleapp.Defect{sampleapp.PaymentNeverSettles}})
	res := r.resolve(request(orderGiven))
	wantBlocked(t, res, "fixture order.paid: create step 3 (GET /v1/orders/{{id}}) waited 30s and $.data.status was never paid (6 attempts)")
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second}
	if diff := diff(want, r.clock.Waits()); diff != "" {
		t.Errorf("waits (-want +got):\n%s", diff)
	}
	// The order was created, so it is cleaned up.
	records := r.resolver().Release(t.Context(), res)
	if records[0].Via != "none" || records[0].Cleanup != "done" {
		t.Errorf("record = %+v", records[0])
	}
	if r.orderStatus("o-1") != sampleapp.StatusCancelled {
		t.Error("the order that create made was not cancelled")
	}
}

func failsPayments(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "POST" && req.URL.Path == "/v1/payments" {
			http.Error(w, `{"error":{"code":"INTERNAL"}}`, http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func TestCreateFailsIsBlockedNeverFailed(t *testing.T) {
	r := newRig(t, rigOptions{wrapPayments: failsPayments})
	res := r.resolve(request(orderGiven))
	wantBlocked(t, res, "fixture order.paid: find gave nothing and create step 2 (POST /v1/payments) answered 500")

	sc := domain.Scenario{ID: testOwner, Steps: []domain.Step{{ID: "read-order", Action: "Read the order"}}}
	got := verdict.Compute(verdict.Input{Scenario: sc, Blocked: res.Blocked})
	if got.Outcome != domain.OutcomeBlocked {
		t.Errorf("outcome = %s, want blocked", got.Outcome)
	}
	// The order of step 1 exists, so cleanup runs with what create captured (C5).
	records := r.resolver().Release(t.Context(), res)
	if records[0].Cleanup != "done" || r.orderStatus("o-1") != sampleapp.StatusCancelled {
		t.Errorf("record = %+v, order %s", records[0], r.orderStatus("o-1"))
	}
}

// closesAfterCreate reads a create and closes the connection without an answer.
func closesAfterCreate(n *atomic.Int32) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == "POST" && req.URL.Path == "/v1/orders" {
				n.Add(1)
				_, _ = io.ReadAll(req.Body)
				conn, _, err := http.NewResponseController(w).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

func TestEffectUnknownNotResent(t *testing.T) {
	var creates atomic.Int32
	r := newRig(t, rigOptions{wrapOrders: closesAfterCreate(&creates)})
	res := r.resolve(request(orderGiven))
	wantBlocked(t, res, "fixture effect unknown: fixture order.paid create step 1 (POST /v1/orders) may have reached the service; nothing was sent again")
	if n := creates.Load(); n != 1 {
		t.Errorf("the create was received %d times, want once", n)
	}
	if r.pay.Requests("", "") != 0 {
		t.Error("a later create step was sent")
	}
	records := r.resolver().Release(t.Context(), res)
	want := []string{"order created by fixture order.paid (as order) may still exist: cleanup step 1 (POST /v1/orders/{{id}}/cancel) was not sent: it needs a value that create did not capture"}
	if diff := diff(want, records[0].Left); diff != "" {
		t.Errorf("left (-want +got):\n%s", diff)
	}
	if records[0].Cleanup != "failed" || creates.Load() != 1 {
		t.Errorf("record = %+v, creates %d", records[0], creates.Load())
	}
}

func TestReadOnlyEnvironment(t *testing.T) {
	r := newRig(t, rigOptions{effects: config.Effects{Mode: "read_only"}})
	res := r.resolve(request(orderGiven))
	wantBlocked(t, res, "this environment is read-only: fixture order.paid found no record and cannot create one")
	if n := r.sent("POST", ""); n != 0 {
		t.Errorf("%d writes were sent", n)
	}
	if n := r.sent("GET", "/v1/orders"); n != 1 {
		t.Errorf("find sent %d reads, want 1", n)
	}

	// A record that find gives is still used on a read-only environment.
	r = newRig(t, rigOptions{effects: config.Effects{Mode: "read_only"}})
	id := r.seedPaidOrder("c-1")
	if res := r.resolve(request(orderGiven)); len(res.Blocked) > 0 || res.Vars["order.id"] != id {
		t.Errorf("Blocked = %q, vars %v", res.Blocked, res.Vars)
	}
}

func TestDeniedStep(t *testing.T) {
	r := newRig(t, rigOptions{effects: config.Effects{Deny: []config.OpPattern{{Method: "POST", Path: "/v1/payments"}}}})
	res := r.resolve(request(orderGiven))
	wantBlocked(t, res, "fixture order.paid: create[1]: POST /v1/payments is on the deny list of uat")
	if n := r.sent("", ""); n != 0 {
		t.Errorf("%d requests were sent", n)
	}

	// A pattern that matches only the real value is refused by the engine and recorded.
	r = newRig(t, rigOptions{effects: config.Effects{Deny: []config.OpPattern{{Method: "GET", Path: "/v1/orders/o-1"}}}})
	res = r.resolve(request(orderGiven))
	if len(res.Blocked) != 1 || !strings.HasPrefix(res.Blocked[0], "fixture order.paid: find gave nothing and create step 3 (GET /v1/orders/{{id}}) was refused: denied") {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	if strings.Contains(res.Blocked[0], "o-1") {
		t.Errorf("the text holds a value: %s", res.Blocked[0])
	}
	actions := r.sink.all()
	if last := actions[len(actions)-1]; last.Refused == "" || last.Use.Fixture.Index != 3 {
		t.Errorf("the refused step was not recorded: %+v", last)
	}
	if n := r.app.Requests("GET", "/v1/orders/o-1"); n != 0 {
		t.Errorf("the denied read reached the service %d times", n)
	}

	// A cleanup step is a write under the same policy, and its refusal is recorded.
	r = newRig(t, rigOptions{effects: config.Effects{Deny: []config.OpPattern{{Method: "POST", Path: "/v1/orders/o-1/cancel"}}}})
	res = r.resolve(request(orderGiven))
	records := r.resolver().Release(t.Context(), res)
	if records[0].Cleanup != "failed" || len(records[0].Left) != 1 || !strings.Contains(records[0].Left[0], "cleanup step 1 (POST /v1/orders/{{id}}/cancel) was refused") {
		t.Errorf("record = %+v", records[0])
	}
	actions = r.sink.all()
	if last := actions[len(actions)-1]; last.Use.Pass != domain.PassCleanup || last.Refused == "" {
		t.Errorf("the refused cleanup was not recorded in the cleanup pass: %+v", last.Use)
	}
	if r.app.Requests("POST", "/v1/orders/o-1/cancel") != 0 {
		t.Error("the denied cleanup reached the service")
	}
}

func TestOnceStep(t *testing.T) {
	r := newRig(t, rigOptions{effects: config.Effects{Once: []config.OpPattern{{Method: "POST", Path: "/v1/payments"}}}})
	res := r.resolve(request(orderGiven))
	wantBlocked(t, res, "fixture order.paid: create[1]: POST /v1/payments matches effects.once, and a fixture runs on every run")
	if n := r.sent("", ""); n != 0 {
		t.Errorf("%d requests were sent", n)
	}

	// A pattern that matches only the real value: the resolver refuses it before the engine sees it.
	r = newRig(t, rigOptions{effects: config.Effects{Once: []config.OpPattern{{Method: "POST", Path: "/v1/orders/o-1/cancel"}}}})
	res = r.resolve(request(orderGiven))
	if len(res.Blocked) > 0 {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	records := r.resolver().Release(t.Context(), res)
	want := []string{"order created by fixture order.paid (as order) may still exist: cleanup step 1 (POST /v1/orders/{{id}}/cancel) matches effects.once, and a fixture runs on every run"}
	if diff := diff(want, records[0].Left); diff != "" {
		t.Errorf("left (-want +got):\n%s", diff)
	}
	if r.app.Requests("POST", "/v1/orders/o-1/cancel") != 0 {
		t.Error("the cleanup that matches once was sent")
	}
}

func TestFindStepThatWritesAtRunTime(t *testing.T) {
	f := example(t)
	f.Find[0].Request.Method = "POST"
	f.Find[0].Request.Path = "/v1/orders/{{customer}}/search"
	f.Find[0].Request.Query = nil
	r := newRig(t, rigOptions{
		fixtures: []domain.Fixture{f},
		effects:  config.Effects{Reads: []config.OpPattern{{Method: "POST", Path: "/v1/orders/x/search"}}},
	})
	res := r.resolve(request(orderGiven))
	wantBlocked(t, res, "fixture order.paid: find step 1 (POST /v1/orders/{{customer}}/search) writes under the effects policy of uat; a find step must read")
	if n := r.sent("", ""); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestMissingParameterAndData(t *testing.T) {
	noDefault := example(t)
	noDefault.Params = map[string]domain.Param{"customer": {}}
	cases := []struct {
		name  string
		f     domain.Fixture
		data  map[string]string
		given domain.Given
		want  string
	}{
		{"required parameter", noDefault, nil, orderGiven, "fixture order.paid: parameter customer has no value"},
		{"missing data", example(t), map[string]string{}, orderGiven, "fixture order.paid: data customers.default is not set for environment uat"},
		{"data in with", example(t), map[string]string{}, domain.Given{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "{{data.customers.vip}}"}}, "fixture order.paid: data customers.vip is not set for environment uat"},
		{"unknown with", example(t), nil, domain.Given{Fixture: "order.paid", As: "order", With: map[string]string{"shop": "s-1"}}, "fixture order.paid: with sets shop, which is not a parameter of the fixture"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, rigOptions{fixtures: []domain.Fixture{c.f}})
			if c.data != nil {
				r.deps.Data = c.data
			}
			wantBlocked(t, r.resolve(request(c.given)), c.want)
			if n := r.sent("", ""); n != 0 {
				t.Errorf("%d requests were sent", n)
			}
		})
	}

	// A with value may name data and the run ID.
	r := newRig(t, rigOptions{})
	r.deps.Data["customers.vip"] = "c-9"
	res := r.resolve(request(domain.Given{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "{{data.customers.vip}}"}}))
	if len(res.Blocked) > 0 {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	if q := r.sink.all()[0].Request.URL; !strings.Contains(q, "customer=c-9") {
		t.Errorf("find was sent to %s", q)
	}
}

func TestNotApproved(t *testing.T) {
	r := newRig(t, rigOptions{unapproved: true})
	wantBlocked(t, r.resolve(request(orderGiven)), "fixture order.paid is not approved: run semar review order.paid")

	// An approval of the fixture as it was before a change does not count.
	r = newRig(t, rigOptions{})
	changed := example(t)
	changed.Description = "A paid order of 2000"
	r.deps.Catalogue = Catalogue{Fixtures: []domain.Fixture{changed}}
	wantBlocked(t, r.resolve(request(orderGiven)), "fixture order.paid is not approved: run semar review order.paid")

	wantBlocked(t, r.resolve(request(domain.Given{Fixture: "order.refunded", As: "refund"})), "fixture order.refunded (as refund) is missing")
	if n := r.sent("", ""); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestUnhealthy(t *testing.T) {
	r := newRig(t, rigOptions{})
	f := example(t)
	at := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	rec := HealthRecord{Fixture: f.ID, Version: f.Version, Hash: domain.FixtureHash(f), Environment: testEnv, At: at,
		Find: "nothing", Create: "failed", Cleanup: "not_needed", Reason: "fixture order.paid: create step 3 (GET /v1/orders/{{id}}) waited 30s and $.data.status was never paid (6 attempts)"}
	if err := r.health.Put(rec); err != nil {
		t.Fatal(err)
	}
	wantBlocked(t, r.resolve(request(orderGiven)), "fixture unhealthy: order.paid failed its last check on uat at 2026-11-17T08:00:00Z: "+rec.Reason)
	if n := r.sent("", ""); n != 0 {
		t.Errorf("%d requests were sent", n)
	}

	// A failed check of an older version says nothing about this one.
	rec.Hash = "0000"
	if err := r.health.Put(rec); err != nil {
		t.Fatal(err)
	}
	if res := r.resolve(request(orderGiven)); len(res.Blocked) > 0 {
		t.Errorf("Blocked = %q", res.Blocked)
	}
}

func TestCleanupReverseOrder(t *testing.T) {
	r := newRig(t, rigOptions{})
	res := r.resolve(request(
		domain.Given{Fixture: "order.paid", As: "first"},
		domain.Given{Fixture: "order.paid", As: "second", With: map[string]string{"customer": "c-2"}},
	))
	if len(res.Blocked) > 0 {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	if res.Vars["first.id"] != "o-1" || res.Vars["second.id"] != "o-2" {
		t.Fatalf("vars = %v", res.Vars)
	}
	records := r.resolver().Release(t.Context(), res)
	var order []string
	for _, a := range r.sink.all() {
		if a.Use.Pass == domain.PassCleanup {
			order = append(order, a.Use.Fixture.As+" "+a.Request.URL[len(r.ordersURL):])
		}
	}
	want := []string{"second /v1/orders/o-2/cancel", "first /v1/orders/o-1/cancel"}
	if diff := diff(want, order); diff != "" {
		t.Errorf("cleanup order (-want +got):\n%s", diff)
	}
	if len(records) != 2 || records[0].As != "first" || records[1].As != "second" || records[0].Cleanup != "done" || records[1].Cleanup != "done" {
		t.Errorf("records = %+v", records)
	}
}

func TestStopsAtFirstFailedEntry(t *testing.T) {
	r := newRig(t, rigOptions{})
	res := r.resolve(request(
		domain.Given{Fixture: "order.paid", As: "first"},
		domain.Given{Fixture: "order.refunded", As: "second"},
		domain.Given{Fixture: "order.paid", As: "third", With: map[string]string{"customer": "c-3"}},
	))
	if diff := diff([]string{"fixture order.refunded (as second) is missing"}, res.Blocked); diff != "" {
		t.Errorf("blocked (-want +got):\n%s", diff)
	}
	if r.app.Requests("POST", "/v1/orders") != 1 {
		t.Error("an entry after the failed one was resolved")
	}
	records := r.resolver().Release(t.Context(), res)
	if len(records) != 2 || records[1].Via != "none" || r.orderStatus("o-1") != sampleapp.StatusCancelled {
		t.Errorf("records = %+v", records)
	}
}

func TestCleanupFailureListsLeft(t *testing.T) {
	r := newRig(t, rigOptions{defects: []sampleapp.Defect{sampleapp.CancelFails}})
	res := r.resolve(request(orderGiven))
	if len(res.Blocked) > 0 {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	resolver := r.resolver()
	records := resolver.Release(t.Context(), res)
	want := []string{"order created by fixture order.paid (as order) may still exist: cleanup step 1 (POST /v1/orders/{{id}}/cancel) answered 500"}
	if diff := diff(want, records[0].Left); diff != "" {
		t.Errorf("left (-want +got):\n%s", diff)
	}
	if records[0].Cleanup != "failed" {
		t.Errorf("cleanup = %s", records[0].Cleanup)
	}
	again := resolver.Release(t.Context(), res)
	if diff := diff(records, again); diff != "" {
		t.Errorf("a second Release changed the records:\n%s", diff)
	}
	if n := r.app.Requests("POST", "/v1/orders/o-1/cancel"); n != 1 {
		t.Errorf("the cleanup was sent %d times, want once", n)
	}
}

func TestCleanupNoneKeepsRecord(t *testing.T) {
	f := example(t)
	f.Cleanup = nil
	r := newRig(t, rigOptions{fixtures: []domain.Fixture{f}})
	records := r.resolver().Release(t.Context(), r.resolve(request(orderGiven)))
	want := []string{"order created by fixture order.paid (as order) is kept: the fixture declares no cleanup"}
	if diff := diff(want, records[0].Left); diff != "" || records[0].Cleanup != "none" {
		t.Errorf("cleanup %s, left (-want +got):\n%s", records[0].Cleanup, diff)
	}
}

func TestReleaseAfterCancel(t *testing.T) {
	r := newRig(t, rigOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	res, err := r.resolver().Resolve(ctx, request(orderGiven))
	if err != nil || len(res.Blocked) > 0 {
		t.Fatalf("Resolve: %v, %q", err, res.Blocked)
	}
	cancel()
	records := r.resolver().Release(ctx, res)
	if records[0].Cleanup != "done" || r.orderStatus("o-1") != sampleapp.StatusCancelled {
		t.Errorf("record = %+v, order %s", records[0], r.orderStatus("o-1"))
	}
}

func TestResolveCancelledKeepsWhatWasMade(t *testing.T) {
	r := newRig(t, rigOptions{settle: 5})
	ctx, cancel := context.WithCancel(t.Context())
	r.clock.onSleep = func(int) { cancel() }
	res, err := r.resolver().Resolve(ctx, request(orderGiven))
	if err == nil {
		t.Fatal("Resolve after a cancel gave no error")
	}
	records := r.resolver().Release(ctx, res)
	if len(records) != 1 || records[0].Cleanup != "done" || r.orderStatus("o-1") != sampleapp.StatusCancelled {
		t.Errorf("records = %+v", records)
	}
}

func TestReleaseNil(t *testing.T) {
	r := newRig(t, rigOptions{})
	if got := r.resolver().Release(t.Context(), nil); got != nil {
		t.Errorf("Release(nil) = %v", got)
	}
}

func TestPerServiceAuthProfile(t *testing.T) {
	r := newRig(t, rigOptions{})
	if res := r.resolve(request(orderGiven)); len(res.Blocked) > 0 {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	// The stored header is redacted, so the proof is in the answers: payments refuses the token
	// of the orders service, and each service saw one session value.
	seen := map[string][]string{}
	for _, a := range r.sink.all() {
		svc := "orders"
		if strings.HasPrefix(a.Request.URL, r.payURL) {
			svc = "payments"
		}
		if a.Response == nil || a.Response.Status > 299 {
			t.Errorf("%s %s answered %+v", a.Request.Method, a.Request.URL, a.Response)
		}
		h := a.Request.Headers["Authorization"]
		if len(h) != 1 {
			t.Fatalf("%s carries %v", a.Request.URL, h)
		}
		if !slices.Contains(seen[svc], h[0]) {
			seen[svc] = append(seen[svc], h[0])
		}
	}
	if len(seen["orders"]) != 1 || len(seen["payments"]) != 1 || seen["orders"][0] == seen["payments"][0] {
		t.Errorf("sessions by service: %v", seen)
	}

	// With no profile on the service and none on the scenario, the step has no session.
	r = newRig(t, rigOptions{})
	req := request(orderGiven)
	req.AuthProfile = ""
	res := r.resolve(req)
	if len(res.Blocked) != 1 || !strings.Contains(res.Blocked[0], "create step 1 (POST /v1/orders) answered 401") {
		t.Errorf("Blocked = %q", res.Blocked)
	}
	if h := r.sink.all()[0].Request.Headers["Authorization"]; h != nil {
		t.Errorf("a step without a profile carries %v", h)
	}
}

func TestCaptureValueInjection(t *testing.T) {
	f := example(t)
	f.Create = []domain.FixtureStep{f.Create[0], {Request: domain.APIRequest{Service: "orders", Method: "GET", Path: "/v1/orders/{{id}}"}}}
	if ps := Validate(f, nil); len(ps) > 0 {
		t.Fatalf("the test fixture is invalid: %v", ps)
	}
	hostile := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == "POST" && req.URL.Path == "/v1/orders" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"data":{"id":"../admin"}}`)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
	r := newRig(t, rigOptions{fixtures: []domain.Fixture{f}, wrapOrders: hostile})
	res := r.resolve(request(orderGiven))
	if len(res.Blocked) != 1 || !strings.HasPrefix(res.Blocked[0], "fixture order.paid: find gave nothing and create step 2 (GET /v1/orders/{{id}}) could not be built: ") {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	if strings.Contains(res.Blocked[0], "admin") {
		t.Errorf("the text holds the captured value: %s", res.Blocked[0])
	}
	records := r.resolver().Release(t.Context(), res)
	if r.app.Requests("", "/v1/admin") != 0 || r.app.Requests("", "/admin") != 0 || r.app.Requests("GET", "/v1/orders/") != 0 {
		t.Error("a request reached a path that the captured value named")
	}
	if records[0].Cleanup != "failed" || len(records[0].Left) != 1 || !strings.Contains(records[0].Left[0], "was not sent") {
		t.Errorf("record = %+v", records[0])
	}
}

func exclusive(g domain.Given) domain.Given {
	g.Lease = domain.LeaseExclusive
	return g
}

func TestHeldRecordGoesToCreate(t *testing.T) {
	r := newRig(t, rigOptions{})
	seeded := r.seedPaidOrder("c-1")
	release, ok, err := r.leases.TryLock(testEnv, "order.paid", seeded)
	if err != nil || !ok {
		t.Fatalf("TryLock = %v, %v", ok, err)
	}
	defer release()

	res := r.resolve(request(exclusive(orderGiven)))
	if len(res.Blocked) > 0 {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	made := res.Vars["order.id"]
	if made == seeded || made == "" {
		t.Fatalf("order.id = %q: the held record was reused", made)
	}
	if _, ok, _ := r.leases.TryLock(testEnv, "order.paid", made); ok {
		t.Error("the created record is not held during the scenario")
	}
	records := r.resolver().Release(t.Context(), res)
	if records[0].Via != "create" {
		t.Errorf("via = %s", records[0].Via)
	}
	if r2, ok, _ := r.leases.TryLock(testEnv, "order.paid", made); !ok {
		t.Error("Release kept the lease")
	} else {
		r2()
	}
	if r.orderStatus(seeded) != sampleapp.StatusPaid {
		t.Error("cleanup touched the held record")
	}
}

func TestFindTakesTheLease(t *testing.T) {
	r := newRig(t, rigOptions{})
	seeded := r.seedPaidOrder("c-1")
	res := r.resolve(request(exclusive(orderGiven)))
	if res.Vars["order.id"] != seeded {
		t.Fatalf("vars = %v, blocked %q", res.Vars, res.Blocked)
	}
	// A parallel scenario sees the record as held and makes its own.
	other := r.resolve(request(exclusive(orderGiven)))
	if got := other.Vars["order.id"]; got == seeded || got == "" {
		t.Errorf("the second scenario got %q", got)
	}
	r.resolver().Release(t.Context(), other)
	r.resolver().Release(t.Context(), res)

	// Without a lease store an exclusive entry is blocked before anything is sent.
	r = newRig(t, rigOptions{noLeases: true})
	wantBlocked(t, r.resolve(request(exclusive(orderGiven))), "fixture order.paid (as order) needs lease: exclusive, and this run has no lease directory")
	if n := r.sent("", ""); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

// findOnly is a fixture that can only reuse a record, such as a verified customer.
func findOnly(t *testing.T) domain.Fixture {
	f := example(t)
	f.ID = "customer.verified"
	f.Lease = domain.LeaseExclusive
	f.Create, f.Cleanup = nil, nil
	if ps := Validate(f, nil); len(ps) > 0 {
		t.Fatalf("the test fixture is invalid: %v", ps)
	}
	return f
}

func TestLeaseWaitEnds(t *testing.T) {
	given := domain.Given{Fixture: "customer.verified", As: "customer"}

	r := newRig(t, rigOptions{fixtures: []domain.Fixture{findOnly(t)}})
	seeded := r.seedPaidOrder("c-1")
	release, ok, err := r.leases.TryLock(testEnv, "customer.verified", seeded)
	if err != nil || !ok {
		t.Fatalf("TryLock = %v, %v", ok, err)
	}
	res := r.resolve(request(given))
	release()
	wantBlocked(t, res, "lease not available: fixture customer.verified is held by another run on this machine (waited 2m0s)")
	waits := r.clock.Waits()
	if len(waits) != 120 || slices.ContainsFunc(waits, func(d time.Duration) bool { return d != time.Second }) {
		t.Errorf("waited %d times: %v", len(waits), waits)
	}

	// The holder lets go during the wait.
	r = newRig(t, rigOptions{fixtures: []domain.Fixture{findOnly(t)}})
	seeded = r.seedPaidOrder("c-1")
	release, _, _ = r.leases.TryLock(testEnv, "customer.verified", seeded)
	r.clock.onSleep = func(n int) {
		if n == 3 {
			release()
		}
	}
	res = r.resolve(request(given))
	if len(res.Blocked) > 0 || res.Vars["customer.id"] != seeded || len(r.clock.Waits()) != 3 {
		t.Errorf("Blocked = %q, vars %v, waits %v", res.Blocked, res.Vars, r.clock.Waits())
	}
	r.resolver().Release(t.Context(), res)

	// Nothing to wait for: find gave nothing and there is no create.
	r = newRig(t, rigOptions{fixtures: []domain.Fixture{findOnly(t)}})
	wantBlocked(t, r.resolve(request(given)), "fixture customer.verified: find gave nothing and the fixture has no create steps")
}

func TestFindOnlyNeverCreates(t *testing.T) {
	r := newRig(t, rigOptions{})
	finds, err := r.resolver().FindOnly(t.Context(), testOwner, []domain.Given{orderGiven, {Fixture: "order.refunded", As: "refund"}}, "alice", domain.PassTrial)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.GivenFind{
		{As: "order", Fixture: "order.paid", Actions: []domain.ActionID{"a-0001"}},
		{As: "refund", Fixture: "order.refunded", Problem: "fixture order.refunded (as refund) is missing"},
	}
	if diff := diff(want, finds); diff != "" {
		t.Errorf("finds (-want +got):\n%s", diff)
	}
	if n := r.sent("POST", ""); n != 0 {
		t.Errorf("%d writes were sent", n)
	}
	if a := r.sink.all()[0]; a.Use.Pass != domain.PassTrial || a.Use.Fixture.Phase != domain.PhaseFind {
		t.Errorf("use = %+v", a.Use)
	}

	id := r.seedPaidOrder("c-1")
	finds, err = r.resolver().FindOnly(t.Context(), testOwner, []domain.Given{orderGiven}, "alice", domain.PassTrial)
	if err != nil {
		t.Fatal(err)
	}
	if !finds[0].Found || !maps.Equal(finds[0].Outputs, map[string]string{"id": id}) {
		t.Errorf("finds = %+v", finds)
	}
}

func TestOutputs(t *testing.T) {
	r := newRig(t, rigOptions{})
	if got, ok := r.resolver().Outputs("order.paid"); !ok || !slices.Equal(got, []string{"id"}) {
		t.Errorf("Outputs = %v, %v", got, ok)
	}
	if _, ok := r.resolver().Outputs("order.refunded"); ok {
		t.Error("a missing fixture has outputs")
	}
}
