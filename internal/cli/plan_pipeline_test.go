package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

const criterionTicket = `# SHOP-482 Refund above the total

## Acceptance criteria

1. A refund above the rest of the total is refused with 422 and the error code REFUND_EXCEEDS_TOTAL.
`

const criterionAPI = `openapi: 3.0.3
servers:
  - url: https://orders.example.com/v1
paths:
  /orders/{orderId}/refunds:
    parameters:
      - {name: orderId, in: path, required: true, schema: {type: string}}
    post:
      requestBody:
        required: true
        content: {application/json: {schema: {type: object, required: [amount], properties: {amount: {type: integer}}}}}
      responses:
        "201": {description: refunded}
        "422":
          description: refused
          content: {application/json: {schema: {type: object, properties: {error: {type: object, properties: {code: {type: string}}}}}}}
`

const criterionAnswerJSON = `{"answer":{"kind":"scenario","title":"A refund above the total is refused","intent":"Before: it was accepted. Now: 422.",
 "given":[],
 "steps":[
  {"id":"create","setup":true,"as":"default","action":"Refund","operation":"post_orders_orderId_refunds","path":[{"name":"orderId","value":"missing-{{run.id}}"}],"query":[],"body":[{"field":"amount","value":5}],"captures":[],"expect":[]},
  {"id":"refund-too-much","setup":false,"as":"default","action":"Refund more than the total","operation":"post_orders_orderId_refunds",
   "path":[{"name":"orderId","value":"missing-{{run.id}}"}],"query":[],"body":[{"field":"amount","value":150000}],"captures":[],
   "expect":[{"subject":"status","name":"","operator":"equals","value":422},{"subject":"jsonpath","name":"$.error.code","operator":"equals","value":"REFUND_EXCEEDS_TOTAL"}]}]}}`

func (p *planRepo) commitCriterionTicket(t *testing.T) {
	t.Helper()
	p.git.Commit("SHOP-482 write the ticket and the API", map[string]*string{
		"docs/tickets/SHOP-482.md": gittest.S(criterionTicket),
		"docs/openapi.yaml":        gittest.S(criterionAPI),
	})
}

// withCriterionKnowledge lists the tickets folder and the API document in the configuration.
func (p *planRepo) withCriterionKnowledge(t *testing.T) {
	t.Helper()
	p.rewriteConfig(t, func(s string) string {
		return s + "knowledge:\n  - {id: tickets, kind: tickets, path: docs/tickets}\n  - {id: api, kind: openapi, path: docs/openapi.yaml, service: orders}\n"
	})
}

func TestPlanCriteriaPipelineEndToEnd(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitCriterionTicket(t)
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	made := p.recordJobs()
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--pipeline", "criteria", "--ladder", "planner:small", "--no-trial")

	contains(t, "stdout", res.stdout, "SHOP-482 (behaviour): 1 scenario\n", "model use: 1 job (1 small), 0 corrections,")
	contains(t, "stderr", res.stderr, "plans each acceptance criterion of release on planner (fake) small; the models get no tools")

	if len(*made) != 1 || len((*made)[0].Jobs()) != 1 {
		t.Fatalf("want one provider that ran once, got %d", len(*made))
	}
	job := (*made)[0].Jobs()[0]
	if !job.NoTools || job.MaxTurns != 2 || job.Model != "small" {
		t.Errorf("job = %+v", job)
	}
	if filepath.Base(job.Workdir) != "empty" {
		t.Errorf("the job runs in %s, and not in a directory of its own that holds nothing", job.Workdir)
	}
	contains(t, "the prompt", job.Prompt, "docs/tickets/SHOP-482.md:5", "REFUND_EXCEEDS_TOTAL", "post_orders_orderId_refunds", "`POST /v1/orders/{orderId}/refunds`", "- Path parameters: `orderId` (string).")
	contains(t, "the schema", string(job.Schema), `"post_orders_orderId_refunds"`)

	feature, err := os.ReadFile(p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-01.feature"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the scenario file", string(feature), "the status is 422", `$.error.code equals "REFUND_EXCEEDS_TOTAL"`, "# ticket docs/tickets/SHOP-482.md:5 A refund above the rest of the total is refused with 422")
	dir := p.path(".semar", "plans", p.head(t)[:12])
	answers, err := os.ReadFile(dir + "/answers.json")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "answers.json", string(answers), `"cite": "docs/tickets/SHOP-482.md:5"`, `"hash":`, `"model": "small"`)
	md, err := os.ReadFile(dir + "/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "plan.md", string(md), "- Pipeline: criteria.", "| docs/tickets/SHOP-482.md:5 | scenario | small | 0 |", "Prompt version: criterion 4, fixture 3",
		"| 5 | 1 | A refund above the rest of the total is refused with 422 and the error code REFUND_EXCEEDS_TOTAL. | SHOP-482-01 |",
		"Tip: write each criterion as Given, When, and Then lines")
}

func TestPlanPipelineFlagsAreChecked(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a ladder for the single pipeline": {[]string{"--pipeline", "single", "--ladder", "planner:small"}, "--ladder needs --pipeline criteria"},
		"an unknown pipeline":              {[]string{"--pipeline", "many"}, `--pipeline "many" must be single or criteria`},
		"a ladder with a provider":         {[]string{"--pipeline", "criteria", "--ladder", "planner:small", "--provider", "planner"}, "do not pass --provider or --model"},
		"a ladder with a model":            {[]string{"--pipeline", "criteria", "--ladder", "planner:small", "--model", "x"}, "do not pass --provider or --model"},
		"setup for criteria":               {[]string{"--pipeline", "criteria", "--with-setup"}, "the jobs of the criteria pipeline have no tools"},
		"a step with no model":             {[]string{"--pipeline", "criteria", "--ladder", "planner"}, "write <provider id>:<model>"},
		"an empty step":                    {[]string{"--pipeline", "criteria", "--ladder", "planner:small,"}, "an empty step"},
		"an unknown provider":              {[]string{"--pipeline", "criteria", "--ladder", "nobody:small"}, `unknown provider "nobody"`},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.commitCriterionTicket(t)
			p.configure(t, criterionAnswerJSON)
			p.withCriterionKnowledge(t)
			args := append([]string{"plan", "--diff", "main..release", "--no-trial"}, tc.args...)
			res := p.semar(t, t.Context(), args...)
			if res.code == cli.ExitOK || !strings.Contains(res.stderr, tc.want) {
				t.Errorf("exit %d, want a refusal with %q\nstderr:\n%s", res.code, tc.want, res.stderr)
			}
			if _, err := os.Stat(p.path(".semar", "scenarios")); err == nil {
				t.Error("a refused plan wrote scenarios")
			}
		})
	}
}
