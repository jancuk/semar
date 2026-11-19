package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

const cancelledTicket = `# SHOP-482 Refund of a cancelled order

## Acceptance criteria

1. A refund of an order that was cancelled is refused with 422.
`

const cancelledAPI = `openapi: 3.0.3
servers:
  - url: https://orders.example.com/v1
paths:
  /orders:
    post:
      operationId: createOrder
      requestBody:
        required: true
        content: {application/json: {schema: {type: object, required: [amount], properties: {amount: {type: integer}}}}}
      responses:
        "201": {description: created, content: {application/json: {schema: {type: object, properties: {data: {type: object, properties: {id: {type: string}}}}}}}}
  /orders/{orderId}/cancel:
    parameters:
      - {name: orderId, in: path, required: true, schema: {type: string}}
    post:
      operationId: cancelOrder
      responses:
        "200": {description: cancelled}
  /orders/{orderId}/refunds:
    parameters:
      - {name: orderId, in: path, required: true, schema: {type: string}}
    post:
      requestBody:
        required: true
        content: {application/json: {schema: {type: object, required: [amount], properties: {amount: {type: integer}}}}}
      responses:
        "201": {description: refunded}
        "422": {description: refused}
`

const needsCancelledAnswer = `{"answer":{"kind":"needsFixture","what":"Refund an order","fixtureId":"order.open","fixtureState":"An open order of the default account","fixtureOutputs":["id"]}}`

const cancelledFixtureAnswer = `{"fixture":{
 "create":[
  {"operation":"createOrder","path":[],"query":[],"body":[{"field":"amount","value":1000}],"captures":[{"name":"id","jsonpath":"$.data.id"}]}],
 "outputs":["id"],
 "cleanup":[{"operation":"cancelOrder","path":[{"name":"orderId","value":"{{id}}"}],"query":[],"body":[],"captures":[]}]}}`

const refundCancelledAnswer = `{"answer":{"kind":"scenario","title":"A refund of a cancelled order is refused","intent":"Before: it was accepted. Now: 422.",
 "given":[{"fixture":"order.open","as":"order","with":[]}],
 "steps":[{"id":"refund","setup":false,"as":"default","action":"Refund the cancelled order","operation":"post_orders_orderId_refunds",
  "path":[{"name":"orderId","value":"{{order.id}}"}],"query":[],"body":[{"field":"amount","value":5}],"captures":[],
  "expect":[{"subject":"status","name":"","operator":"equals","value":422}]}]}}`

func (p *planRepo) cancelledRepo(t *testing.T, answers ...string) *[]string {
	t.Helper()
	p.git.Commit("SHOP-482 write the ticket and the API", map[string]*string{
		"docs/tickets/SHOP-482.md": gittest.S(cancelledTicket),
		"docs/openapi.yaml":        gittest.S(cancelledAPI),
	})
	p.configure(t, answers...)
	p.withCriterionKnowledge(t)
	return nil
}

func TestPlanDraftsTheFixtureACriterionNeedsRunsItOnceAndPlansTheCriterionAgain(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.cancelledRepo(t, needsCancelledAnswer, cancelledFixtureAnswer, refundCancelledAnswer)
	made := p.recordJobs()
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")

	contains(t, "stdout", res.stdout,
		"SHOP-482 (behaviour): 1 scenario\n",
		"fixture drafted: order.open. Ran once: ok.\n",
		"next:\n  semar run   run the tests against the service\n",
	)
	if got := len((*made)[0].Jobs()); got != 3 {
		t.Errorf("the plan ran %d jobs, want 3: the criterion, the fixture, and the criterion again", got)
	}
	if n := p.app.Requests("POST", "/v1/orders"); n != 2 {
		t.Errorf("the environment got %d requests to /v1/orders, want the create and the cancel of one run of the fixture", n)
	}

	data, err := os.ReadFile(p.path(".semar", "fixtures", "order.open.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	fx, err := fixture.Parse(data, "order.open.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if fx.Owner != "semar" || fx.ID != "order.open" || len(fx.Create) != 1 || len(fx.Cleanup) != 1 || fx.Create[0].Request.Path != "/v1/orders" || fx.Cleanup[0].Request.Path != "/v1/orders/{{id}}/cancel" {
		t.Errorf("fixture = %+v", fx)
	}
	if info, err := os.Stat(p.path(".semar", "fixtures", "order.open.yaml")); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("fixture mode %v, %v; want 0644", info.Mode().Perm(), err)
	}

	feature, err := os.ReadFile(p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-01.feature"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the scenario", string(feature), "order.open")

	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "plan.md", string(md), "## Fixtures drafted\n",
		"- order.open: An open order of the default account. 2 requests, cleans up. Ran once: ok.\n",
		"  Used by SHOP-482-01, SHOP-482-E01, SHOP-482-E02.\n",
		"1 fixture drafted")

	list := p.must(t, cli.ExitOK, "fixture", "list")
	contains(t, "fixture list", list.stdout, "order.open", "used by SHOP-482-01")
}

func TestPlanSendsAFixtureThatFailsItsRunBackToTheModel(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	bad := strings.Replace(cancelledFixtureAnswer, `"value":1000`, `"value":0`, 1)
	p.cancelledRepo(t, needsCancelledAnswer, bad, cancelledFixtureAnswer, refundCancelledAnswer)
	made := p.recordJobs()
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	contains(t, "stdout", res.stdout, "fixture drafted: order.open. Ran once: ok.")
	jobs := (*made)[0].Jobs()
	if len(jobs) != 4 {
		t.Fatalf("jobs = %d, want 4", len(jobs))
	}
	contains(t, "the correction", jobs[2].Prompt, "Your previous answer was rejected", "trial: ", "400")
}

func TestPlanWithNoTrialDraftsTheFixtureAndSendsNothingToTheEnvironment(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.cancelledRepo(t, needsCancelledAnswer, cancelledFixtureAnswer, refundCancelledAnswer)
	p.recordJobs()
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial")
	contains(t, "stdout", res.stdout, "fixture drafted: order.open. Not run: turned off with --no-trial.\n")
	if n := p.app.Requests("", ""); n != 0 {
		t.Errorf("the environment got %d requests", n)
	}
	if _, err := os.Stat(p.path(".semar", "fixtures", "order.open.yaml")); err != nil {
		t.Error(err)
	}
}

func TestPlanDoesNotRunAFixtureOnAnEnvironmentThatAllowsOnlyReads(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.cancelledRepo(t, needsCancelledAnswer, cancelledFixtureAnswer, refundCancelledAnswer)
	p.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "effects: {mode: test_data}", "effects: {mode: read_only}", 1)
	})
	p.recordJobs()
	res := p.semar(t, t.Context(), "plan", "--diff", "main..release")
	contains(t, "stdout", res.stdout, "fixture drafted: order.open. Not run:")
	if n := p.app.Requests("POST", ""); n != 0 {
		t.Errorf("a read-only environment got %d writes", n)
	}
}

func TestPlanDoesNotSendAFixtureCreateThatEffectsDeny(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.cancelledRepo(t, needsCancelledAnswer, cancelledFixtureAnswer, cancelledFixtureAnswer, cancelledFixtureAnswer, cancelledFixtureAnswer)
	p.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "effects: {mode: test_data}", "effects: {mode: test_data, deny: [{method: POST, path: /v1/orders}]}", 1)
	})
	p.recordJobs()
	res := p.semar(t, t.Context(), "plan", "--diff", "main..release")
	contains(t, "stdout", res.stdout, "fixture not drafted: order.open:", "deny list")
	if strings.Contains(res.stdout, "semar review") {
		t.Errorf("the plan points to semar review:\n%s", res.stdout)
	}
	if n := p.app.Requests("POST", "/v1/orders"); n != 0 {
		t.Errorf("the environment got %d creates although effects.deny lists them", n)
	}
	if _, err := os.Stat(p.path(".semar", "fixtures", "order.open.yaml")); err == nil {
		t.Error("a fixture whose create the policy denies was written")
	}
}

func TestPlanKeepsACriterionNotDraftedWhenItsFixtureCannotBeWritten(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	bad := strings.Replace(cancelledFixtureAnswer, `"createOrder"`, `"openAccount"`, 1)
	p.cancelledRepo(t, needsCancelledAnswer, bad, bad, bad, bad)
	p.recordJobs()
	res := p.semar(t, t.Context(), "plan", "--diff", "main..release")
	contains(t, "stdout", res.stdout, "fixture not drafted: order.open: create[0].operation: the operation \"openAccount\" is not one of the operations")
	if _, err := os.Stat(p.path(".semar", "fixtures", "order.open.yaml")); err == nil {
		t.Error("a fixture that failed was written")
	}
	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "plan.md", string(md), "- order.open: not drafted. create\\[0\\].operation:", "The fixture was not drafted")
}
