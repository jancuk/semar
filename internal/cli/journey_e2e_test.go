package cli_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/sampleapp"
)

// paidJourney is a journey of two legs: an order is placed on orders and paid on payments, and
// both name the same amount.
const paidJourney = `apiVersion: semar/v1
kind: Journey
id: paid
title: An order is placed and paid
owner: team-commerce
criticality: standard
legs:
  - id: place
    surface: api
    service: orders
    steps:
      - id: create
        action: Create an order of 1000
        hint: { method: POST, path: /v1/orders }
        request: { method: POST, path: /v1/orders, json: { amount: 1000 } }
        captures: [{ name: id, jsonpath: "$.data.id" }, { name: amount, jsonpath: "$.data.amount" }]
        expect: [{ check: { status: 201 }, source: person }]
    outputs: [id, amount]
  - id: pay
    surface: api
    service: payments
    needs: [place]
    steps:
      - id: pay
        action: Pay the order
        hint: { method: POST, path: /v1/payments }
        request: { method: POST, path: /v1/payments, json: { orderId: "{{place.id}}" } }
        captures: [{ name: amount, jsonpath: "$.data.amount" }]
        expect: [{ check: { status: 202 }, source: person }]
    outputs: [amount]
invariants:
  - equal: ["{{place.amount}}", "{{pay.amount}}"]
`

func legOutcomes(j domain.JourneyResult) []string {
	out := make([]string, len(j.Legs))
	for i, l := range j.Legs {
		out[i] = string(l.Leg) + " " + string(l.Outcome)
	}
	return out
}

func singleJourney(t *testing.T, r *repo) domain.JourneyResult {
	t.Helper()
	run := r.lastRun(t)
	if len(run.Journeys) != 1 {
		t.Fatalf("run.json has %d journeys, want 1", len(run.Journeys))
	}
	return run.Journeys[0]
}

func TestJourneyEndToEndPassed(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.writeRel(t, ".semar/journeys/paid.yaml", paidJourney)

	res := r.must(t, cli.ExitOK, "journey", "run", "checkout", "--env", "local")
	contains(t, "the summary", squash(res.stdout), "checkout passed", "place passed", "pay passed", "settled passed")
	contains(t, "stderr", res.stderr, "semar: running against local (test, test data)")

	run := r.lastRun(t)
	if run.Purpose != domain.PurposeJourney || run.Environment != "local" || run.EndedAt.IsZero() {
		t.Errorf("run.json: purpose %q, environment %q, ended %v", run.Purpose, run.Environment, run.EndedAt)
	}
	var legs []string
	for _, rr := range run.Results {
		legs = append(legs, string(rr.Scenario)+" "+string(rr.Outcome))
	}
	if got, want := strings.Join(legs, ", "), "checkout.place passed, checkout.pay passed, checkout.settled passed"; got != want {
		t.Errorf("leg results = %s, want %s", got, want)
	}
	j := singleJourney(t, r)
	if j.Journey != "checkout" || j.Outcome != domain.JourneyPassed || j.FirstFailing != "" || len(j.Reasons) != 0 {
		t.Errorf("journey result = %+v", j)
	}
	var services []string
	for _, b := range j.Manifest.Services {
		services = append(services, b.Service+" "+b.Commit)
	}
	if got := strings.Join(services, ", "); got != "orders 9f3c2ab, payments 9f3c2ab" {
		t.Errorf("manifest = %s", got)
	}
	if j.Manifest.Hash == "" || j.Manifest.Hash != domain.ManifestHash(j.Manifest.Services) || j.ManifestEnd == nil || j.ManifestEnd.Hash != j.Manifest.Hash {
		t.Errorf("manifest hash %q, end %+v", j.Manifest.Hash, j.ManifestEnd)
	}
	if len(j.Invariants) != 1 || j.Invariants[0].State != domain.CheckTrue || strings.Join(j.Invariants[0].Values, ",") != "1000,1000,1000" {
		t.Errorf("invariants = %+v", j.Invariants)
	}

	md := r.lastReport(t)
	contains(t, "report.md", md, "## Journeys", "### checkout An order is placed, paid, and shown as paid", "Outcome: **passed**",
		"- Manifest: orders 9f3c2ab config", "- Leg settled: passed", "- Invariant 1: true: 1000, 1000, 1000")
	if strings.Index(md, "## Journeys") > strings.Index(md, "- Environment:") {
		t.Errorf("the journeys do not come first:\n%s", md)
	}

	// Exactly one order was made, paid, and read until it was paid.
	if n := r.app.Requests("POST", "/v1/orders"); n != 1 {
		t.Errorf("%d orders were created", n)
	}
	if n := r.pay.Requests("POST", "/v1/payments"); n != 1 {
		t.Errorf("%d payments were made", n)
	}
	writes := r.must(t, cli.ExitOK, "show", "--activity", "--writes")
	contains(t, "the writes", writes.stdout, "POST /v1/orders", "POST /v1/payments")

	// The two-leg journey passes too, and the list shows both.
	r.must(t, cli.ExitOK, "journey", "run", "paid")
	if j := singleJourney(t, r); j.Journey != "paid" || j.Outcome != domain.JourneyPassed || strings.Join(legOutcomes(j), ", ") != "place passed, pay passed" {
		t.Errorf("paid = %+v", j)
	}
	list := r.must(t, cli.ExitOK, "journey", "list", "--env", "local")
	contains(t, "the list", squash(list.stdout), "checkout critical team-commerce 3 legs passed in r-test-0001", "paid standard team-commerce 2 legs passed in r-test-0002")

	r.assertNoSecret(t, []string{token, "tok-pay"})
}

func TestJourneyEndToEndFailedLegStopsTheLaterLegs(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	// The payment answers 202, and the file expects 200.
	r.editJourney(t, "checkout", func(s string) string { return strings.Replace(s, "status: 202", "status: 200", 1) })

	res := r.must(t, cli.ExitFailed, "journey", "run", "checkout")
	contains(t, "the summary", squash(res.stdout), "checkout failed first failing leg pay:", "place passed", "pay failed", "settled not_run stopped after leg pay")
	j := singleJourney(t, r)
	if j.Outcome != domain.JourneyFailed || j.FirstFailing != "pay" || strings.Join(legOutcomes(j), ", ") != "place passed, pay failed, settled not_run" {
		t.Errorf("journey = %+v", j)
	}
	if got := j.Legs[2].Reasons[0].Text; got != "stopped after leg pay" {
		t.Errorf("the last leg's reason is %q", got)
	}
	if len(j.Invariants) != 0 {
		t.Errorf("a failed leg still gave invariants: %+v", j.Invariants)
	}
	if n := r.app.Requests("GET", "/v1/orders/"); n != 0 {
		t.Errorf("the leg after the failure sent %d reads", n)
	}
	contains(t, "report.md", r.lastReport(t), "First failing leg: pay.", "Reproduce: semar journey run checkout --env local")
}

func TestJourneyEndToEndWaitTimeout(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{Defects: defects(sampleapp.PaymentNeverSettles)})

	res := r.must(t, cli.ExitFailed, "journey", "run", "checkout")
	j := singleJourney(t, r)
	if j.Outcome != domain.JourneyFailed || j.FirstFailing != "settled" || strings.Join(legOutcomes(j), ", ") != "place passed, pay passed, settled failed" {
		t.Fatalf("journey = %+v", j)
	}
	m := regexp.MustCompile(`^waited 30s and \$\.data\.status was never paid \(([0-9]+) attempts\)$`).FindStringSubmatch(j.Legs[2].Reasons[0].Text)
	if m == nil {
		t.Fatalf("the leg reason is %q", j.Legs[2].Reasons[0].Text)
	}
	attempts, _ := strconv.Atoi(m[1])
	if attempts < 2 {
		t.Errorf("%d attempts", attempts)
	}
	contains(t, "the summary", squash(res.stdout), "checkout failed first failing leg settled: waited 30s and $.data.status was never paid ("+m[1]+" attempts)")
	// Each attempt reached the service, and each one is a recorded action of the leg.
	if n := r.app.Requests("GET", "/v1/orders/o-1"); n != attempts {
		t.Errorf("%d reads reached the service, the leg says %d attempts", n, attempts)
	}
	if got := len(r.lastRun(t).Results[2].Actions); got != attempts {
		t.Errorf("the leg names %d actions, want one for each of the %d attempts", got, attempts)
	}
}

func TestJourneyEndToEndInvariantFalse(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{Defects: defects(sampleapp.PaymentAmountWrong)})

	res := r.must(t, cli.ExitFailed, "journey", "run", "checkout")
	contains(t, "the summary", squash(res.stdout), "checkout failed invariant 1 is false: 1000, 1001, 1000")
	j := singleJourney(t, r)
	if j.Outcome != domain.JourneyFailed || j.FirstFailing != "" || strings.Join(legOutcomes(j), ", ") != "place passed, pay passed, settled passed" {
		t.Errorf("journey = %+v", j)
	}
	if len(j.Invariants) != 1 || j.Invariants[0].State != domain.CheckFalse || strings.Join(j.Invariants[0].Values, ",") != "1000,1001,1000" {
		t.Errorf("invariants = %+v", j.Invariants)
	}
	contains(t, "report.md", r.lastReport(t), "- Invariant 1: false: 1000, 1001, 1000")
}

func TestJourneyEndToEndBuildChangeIsStale(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{Defects: defects(sampleapp.BuildChanges)}, sampleapp.PaymentOptions{})

	res := r.must(t, cli.ExitUndecided, "journey", "run", "checkout")
	contains(t, "the summary", res.stdout, "a build changed during the journey: orders 9f3c2ab then c0ffee1")
	j := singleJourney(t, r)
	if j.Outcome != domain.JourneyStale || j.ManifestEnd == nil || j.ManifestEnd.Hash == j.Manifest.Hash {
		t.Fatalf("journey = %+v", j)
	}
	// A later read of the builds shows the stored result as stale, with the part that changed.
	list := r.must(t, cli.ExitOK, "journey", "list", "--env", "local")
	contains(t, "the list", squash(list.stdout), "checkout critical team-commerce 3 legs stale (was stale in r-test-0001): orders 9f3c2ab then c0ffee1")
}

func TestJourneyEndToEndConfirmationOfTheExpectedResults(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.editJourney(t, "checkout", func(s string) string { return strings.Replace(s, "source: person", "source: diff", 1) })

	// No one confirmed the expected result of leg place, so the journey is blocked and sends nothing.
	res := r.must(t, cli.ExitUndecided, "journey", "run", "checkout")
	contains(t, "the summary", squash(res.stdout), "checkout needs review first failing leg place:")
	j := singleJourney(t, r)
	if j.Outcome != domain.JourneyBlocked || j.FirstFailing != "place" || !strings.HasPrefix(j.Reasons[0].Text, "leg place ended inconclusive") {
		t.Errorf("journey = %+v", j)
	}
	if n := r.reached(""); n != 0 {
		t.Errorf("%d requests were sent", n)
	}

	confirmed := r.must(t, cli.ExitOK, "review", "checkout", "--yes")
	contains(t, "the confirmation", confirmed.stdout, "Leg place of journey checkout (scenario checkout.place)", "approved journey checkout", "local, unverified identity")
	if strings.Contains(confirmed.stdout, "checkout.pay") {
		t.Errorf("a leg whose expected result is the person's own was listed:\n%s", confirmed.stdout)
	}

	r.must(t, cli.ExitOK, "journey", "run", "checkout")
	if j := singleJourney(t, r); j.Outcome != domain.JourneyPassed {
		t.Errorf("after the confirmation the journey is %s: %+v", j.Outcome, j.Reasons)
	}
}

func TestJourneyEndToEndRefusesAnAndroidLeg(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	text := strings.Replace(strings.Replace(r.readJourney(t, "checkout"), "id: checkout", "id: web-flow", 1), "surface: api", "surface: android", 1)
	r.writeRel(t, ".semar/journeys/web-flow.yaml", text)
	const j7 = "legs[0].surface: android legs are not supported yet: this version runs api legs and web legs; android legs come in M3"

	list := r.must(t, cli.ExitUndecided, "journey", "list")
	contains(t, "the list", list.stdout, "invalid: .semar/journeys/web-flow.yaml:", j7)
	contains(t, "the list", squash(list.stdout), "checkout critical team-commerce 3 legs no result yet")

	// --all skips the file with the same message, runs the rest, and is not a pass.
	all := r.must(t, cli.ExitUndecided, "journey", "run", "--all")
	contains(t, "stderr", all.stderr, "semar: journey file .semar/journeys/web-flow.yaml:", j7, "this run skips it", "1 journey file was skipped")
	contains(t, "the summary", squash(all.stdout), "checkout passed")

	// A run that names the file is refused before anything is sent.
	before := r.reached("")
	named := r.must(t, cli.ExitError, "journey", "run", "web-flow")
	contains(t, "stderr", named.stderr, "journey web-flow is not valid, so it cannot be run", j7)
	if n := r.reached(""); n != before {
		t.Errorf("a refused run sent %d requests", n-before)
	}
}

const givenJourney = `apiVersion: semar/v1
kind: Journey
id: seen-paid
title: A paid order is read back
owner: team-commerce
criticality: standard
given:
  - fixture: order.paid
    as: order
legs:
  - id: read
    surface: api
    service: orders
    steps:
      - id: read
        action: Read the paid order
        hint: { method: GET, path: "/v1/orders/{id}" }
        request: { method: GET, path: "/v1/orders/{{order.id}}" }
        captures: [{ name: amount, jsonpath: "$.data.amount" }]
        expect: [{ check: { jsonpath: "$.data.status", equals: paid }, source: person }]
    outputs: [amount]
`

func TestJourneyEndToEndGivenFixture(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "      mode: test_data\n", "      mode: test_data\n    data:\n      customers.default: c-1\n", 1)
	})
	fixtureText, err := os.ReadFile("../../testdata/e2e-fixtures/.semar/fixtures/order.paid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r.writeRel(t, ".semar/fixtures/order.paid.yaml", string(fixtureText))
	r.writeRel(t, ".semar/journeys/seen-paid.yaml", givenJourney)

	// A fixture that nobody approved blocks the journey, and nothing reaches /v1.
	blocked := r.must(t, cli.ExitUndecided, "journey", "run", "seen-paid")
	contains(t, "the summary", squash(blocked.stdout), "seen-paid needs review fixture order.paid is not approved: run semar review order.paid")
	if n := r.reached("/v1"); n != 0 {
		t.Errorf("%d requests reached the services before the fixture was approved", n)
	}
	if j := singleJourney(t, r); j.Outcome != domain.JourneyBlocked {
		t.Errorf("an unapproved fixture gave %s, and never failed a journey", j.Outcome)
	}

	r.approveFixture(t)
	r.must(t, cli.ExitOK, "journey", "run", "seen-paid")
	j := singleJourney(t, r)
	if j.Outcome != domain.JourneyPassed || len(j.Fixtures) != 1 || j.Fixtures[0].Cleanup != "done" || j.Fixtures[0].Outputs["id"] != "o-1" {
		t.Fatalf("journey = %+v, fixtures %+v", j, j.Fixtures)
	}
	var services []string
	for _, b := range j.Manifest.Services {
		services = append(services, b.Service)
	}
	if strings.Join(services, ",") != "orders,payments" {
		t.Errorf("the manifest holds %v, want the service that a fixture step calls too", services)
	}
	if got := r.orderStatus(t, "o-1"); got != sampleapp.StatusCancelled {
		t.Errorf("order o-1 is %s, want cancelled by the cleanup", got)
	}
	contains(t, "report.md", r.lastReport(t), "- Given order: order.paid 1.0.0 (hash ", "made by create; cleanup done")
	r.assertNoSecret(t, []string{token, "tok-pay"})
}

// A service that names no auth profile signs in with the one that fixture check uses, the profile
// named default, both for the journey's fixture and for its legs. Before, the fixture went out with
// no session and the journey ended blocked with a 401.
func TestJourneyEndToEndServiceWithoutProfileUsesDefault(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.rewriteConfig(t, func(s string) string {
		s = strings.Replace(s, "      mode: test_data\n", "      mode: test_data\n    data:\n      customers.default: c-1\n", 1)
		s = strings.Replace(s, "        authProfile: support-agent\n", "", 1)
		return strings.Replace(s, "  support-agent:\n", "  default:\n", 1)
	})
	fixtureText, err := os.ReadFile("../../testdata/e2e-fixtures/.semar/fixtures/order.paid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r.writeRel(t, ".semar/fixtures/order.paid.yaml", string(fixtureText))
	r.writeRel(t, ".semar/journeys/seen-paid.yaml", givenJourney)
	r.approveFixture(t)

	r.must(t, cli.ExitOK, "journey", "run", "seen-paid")
	if j := singleJourney(t, r); j.Outcome != domain.JourneyPassed || len(j.Fixtures) != 1 || j.Fixtures[0].Cleanup != "done" {
		t.Fatalf("journey = %+v, fixtures %+v", j, j.Fixtures)
	}
}
