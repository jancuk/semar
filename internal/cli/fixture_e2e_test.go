package cli_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/sampleapp"
)

func givenLine(h domain.Hash, how string) string {
	return "Given order: order.paid 1.0.0 (hash " + string(h)[:hashWidth] + "), " + how
}

func TestFixtureRunEndToEnd(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})

	// A fixture that nobody approved gives no state, and nothing reaches the services.
	blocked := r.must(t, cli.ExitUndecided, "run", "--scenario", fixtureScenario)
	contains(t, "the summary", blocked.stdout, fixtureScenario, "blocked", "fixture order.paid is not approved: run semar review order.paid")
	if n := r.reached("/v1"); n != 0 {
		t.Errorf("%d requests reached the services before the fixture was approved", n)
	}

	confirmed := r.must(t, cli.ExitOK, "confirm", "--fixture", "order.paid", "--yes")
	contains(t, "the approval", confirmed.stdout, "approved fixture order.paid 1.0.0", "local, unverified identity")
	hash, err := cli.FixtureHashOf(r.dir, "order.paid")
	if err != nil {
		t.Fatal(err)
	}

	res := r.must(t, cli.ExitOK, "run", "--scenario", fixtureScenario)
	contains(t, "the summary", res.stdout, fixtureScenario, "passed")

	if got := r.orderStatus(t, "o-1"); got != sampleapp.StatusCancelled {
		t.Errorf("order o-1 is %s, want cancelled by the cleanup", got)
	}
	// The prefix counts the create and the cancel of the one order.
	if n := r.app.Requests("POST", "/v1/orders"); n != 2 {
		t.Errorf("%d writes reached /v1/orders, want a create and a cancel", n)
	}
	if n := r.app.Requests("POST", "/v1/orders/o-1/cancel"); n != 1 {
		t.Errorf("the cleanup sent %d cancels, want 1", n)
	}

	run := r.lastRun(t)
	if len(run.Results) != 1 || run.Results[0].Outcome != domain.OutcomePassed || len(run.Results[0].Fixtures) != 1 {
		t.Fatalf("run.json results = %+v", run.Results)
	}
	rec := run.Results[0].Fixtures[0]
	if rec.As != "order" || rec.Fixture != "order.paid" || rec.Version != "1.0.0" || rec.Hash != hash ||
		rec.Via != "create" || rec.Cleanup != "done" || len(rec.Left) != 0 || rec.Outputs["id"] != "o-1" {
		t.Errorf("fixture record = %+v", rec)
	}
	// find, create 1 and 2, the two reads of create 3, and the cleanup.
	if len(rec.Actions) != 6 {
		t.Errorf("fixture record names %d actions, want 6: %v", len(rec.Actions), rec.Actions)
	}
	for _, id := range rec.Actions {
		if slicesContains(run.Results[0].Actions, id) {
			t.Errorf("action %s of the fixture is also an action of the scenario", id)
		}
	}

	md := r.lastReport(t)
	contains(t, "report.md", md, givenLine(hash, "made by create; cleanup done"))
	if strings.Contains(md, "Still exists") {
		t.Errorf("a clean run lists what still exists:\n%s", md)
	}

	acts := r.must(t, cli.ExitOK, "activity", "latest")
	contains(t, "the activity", acts.stdout,
		"fixture order.paid (as order): find step 1",
		"fixture order.paid (as order): create step 1",
		"fixture order.paid (as order): create step 2",
		"fixture order.paid (as order): create step 3",
		"fixture order.paid (as order): cleanup step 1",
		"POST /v1/orders/o-1/cancel",
	)
	if n := strings.Count(acts.stdout, "create step 3"); n != 2 {
		t.Errorf("the wait for the payment made %d reads, want 2:\n%s", n, acts.stdout)
	}
	cleanup := r.must(t, cli.ExitOK, "activity", "latest", "--pass", "cleanup")
	contains(t, "the cleanup pass", cleanup.stdout, "POST /v1/orders/o-1/cancel")

	r.assertNoSecret(t, []string{token, "tok-pay"})
}

func TestFixtureRunReusesARecordThatFindGives(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	seeded := r.seedPaidOrder(t)
	hash := r.approveFixture(t)
	orders := r.app.Requests("POST", "/v1/orders")

	res := r.must(t, cli.ExitOK, "run", "--scenario", fixtureScenario)
	contains(t, "the summary", res.stdout, "passed")

	if n := r.app.Requests("POST", "/v1/orders") - orders; n != 0 {
		t.Errorf("%d orders were created although find gave one", n)
	}
	if n := r.app.Requests("POST", "/v1/orders/"+seeded+"/cancel"); n != 0 {
		t.Errorf("the cleanup sent %d cancels for a record it did not make", n)
	}
	if got := r.orderStatus(t, seeded); got != sampleapp.StatusPaid {
		t.Errorf("the reused order is %s, want paid", got)
	}

	rec := r.lastRun(t).Results[0].Fixtures[0]
	if rec.Via != "find" || rec.Cleanup != "not_needed" || rec.Outputs["id"] != seeded || len(rec.Left) != 0 || len(rec.Actions) != 1 {
		t.Errorf("fixture record = %+v", rec)
	}
	md := r.lastReport(t)
	contains(t, "report.md", md, givenLine(hash, "found by find; cleanup not needed"))
	acts := r.must(t, cli.ExitOK, "activity", "latest", "--pass", "cleanup")
	if strings.Contains(acts.stdout, "cancel") {
		t.Errorf("a cleanup action ran for a record that find gave:\n%s", acts.stdout)
	}
}

func TestFixtureRunNotApprovedIsBlockedBeforeAnyRequest(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})

	res := r.must(t, cli.ExitUndecided, "run", "--scenario", fixtureScenario)
	contains(t, "the summary", res.stdout, "blocked")
	if strings.Contains(res.stdout, "failed 1") {
		t.Errorf("a fixture that is not approved must never give failed:\n%s", res.stdout)
	}
	if n := r.reached("/v1"); n != 0 {
		t.Errorf("%d requests reached the services", n)
	}
	run := r.lastRun(t)
	if len(run.Results) != 1 || run.Results[0].Outcome != domain.OutcomeBlocked || len(run.Results[0].Actions) != 0 {
		t.Fatalf("results = %+v", run.Results)
	}
	hash, err := cli.FixtureHashOf(r.dir, "order.paid")
	if err != nil {
		t.Fatal(err)
	}
	md := r.lastReport(t)
	contains(t, "report.md", md,
		"fixture order.paid is not approved: run semar review order.paid",
		givenLine(hash, "no state was given; cleanup not needed"))

	// An approval belongs to the fixture as it was: a change lapses it.
	r.approveFixture(t)
	file := r.path(".semar", "fixtures", "order.paid.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, bytes.Replace(data, []byte("of 1000"), []byte("of 1001"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	r.must(t, cli.ExitUndecided, "run", "--scenario", fixtureScenario)
	if n := r.reached("/v1"); n != 0 {
		t.Errorf("%d requests reached the services for a changed fixture", n)
	}
}

func TestFixtureRunCleanupFailureIsListed(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{Defects: defects(sampleapp.CancelFails)}, sampleapp.PaymentOptions{})
	hash := r.approveFixture(t)

	res := r.must(t, cli.ExitOK, "run", "--scenario", fixtureScenario)
	contains(t, "the summary", res.stdout, "passed")
	rec := r.lastRun(t).Results[0].Fixtures[0]
	if rec.Cleanup != "failed" || len(rec.Left) != 1 {
		t.Fatalf("fixture record = %+v", rec)
	}
	md := r.lastReport(t)
	contains(t, "report.md", md,
		givenLine(hash, "made by create; cleanup failed"),
		"## Still exists",
		"SHOP-482-05: order created by fixture order.paid (as order) may still exist: cleanup step 1 (POST /v1/orders/{{id}}/cancel) answered 500")
	if got := r.orderStatus(t, "o-1"); got != sampleapp.StatusPaid {
		t.Errorf("order o-1 is %s, want paid, because its cancel failed", got)
	}
}

func TestFixtureRunPaymentThatNeverSettlesIsBlocked(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{Defects: defects(sampleapp.PaymentNeverSettles)})
	r.approveFixture(t)

	res := r.must(t, cli.ExitUndecided, "run", "--scenario", fixtureScenario)
	contains(t, "the summary", res.stdout, "blocked", "create step 3 (GET /v1/orders/{{id}}) waited 1s and $.data.status was never paid")
	if strings.Contains(res.stdout, "failed 1") {
		t.Errorf("a fixture that cannot give its state must never give failed:\n%s", res.stdout)
	}
	run := r.lastRun(t)
	if got := run.Results[0]; got.Outcome != domain.OutcomeBlocked || len(got.Actions) != 0 {
		t.Errorf("result = %+v, want blocked with no scenario action", got)
	}
	// The order that create made is still cancelled.
	if got := r.orderStatus(t, "o-1"); got != sampleapp.StatusCancelled {
		t.Errorf("order o-1 is %s, want cancelled", got)
	}
	if n := r.app.Requests("GET", "/v1/orders/o-1"); n < 3 {
		t.Errorf("the wait read the order %d times", n)
	}
}

func TestFixtureCheckThenARunOfAnUnhealthyFixture(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{Defects: defects(sampleapp.PaymentNeverSettles)})
	r.approveFixture(t)

	res := r.must(t, cli.ExitFailed, "fixture", "check", "--all", "--env", "local", "--auth-profile", "support-agent")
	contains(t, "the check", res.stdout, "order.paid 1.0.0: unhealthy", "waited 1s")
	if got := r.orderStatus(t, "o-1"); got != sampleapp.StatusCancelled {
		t.Errorf("the check left order o-1 %s", got)
	}
	if !exists(t, r.path(".semar", "store", "fixture-health", "local", "order.paid.json")) {
		t.Fatal("the check wrote no health record")
	}

	ordersBefore := r.app.Requests("POST", "/v1/orders")
	blocked := r.must(t, cli.ExitUndecided, "run", "--scenario", fixtureScenario)
	contains(t, "the summary", blocked.stdout, "blocked", "fixture unhealthy: order.paid failed its last check on local at 2026-11-14T")
	if n := r.app.Requests("POST", "/v1/orders") - ordersBefore; n != 0 {
		t.Errorf("the run created %d orders for an unhealthy fixture", n)
	}
}
