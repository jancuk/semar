package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

const edgeTokenBob = "SEMAR_TOKEN_BOB"

const edgeAPI = `openapi: 3.0.3
paths:
  /v1/orders:
    get:
      parameters:
        - {name: status, in: query, schema: {type: string, enum: [created, paid]}}
        - {name: limit, in: query, schema: {type: integer, minimum: 1, maximum: 50}}
      responses:
        "200": {description: ok}
        "422": {description: bad query}
    post:
      requestBody:
        content:
          application/json:
            schema: {type: object, required: [amount], properties: {amount: {type: integer}}}
      responses:
        "201": {description: created}
        "400": {description: refused}
  /v1/orders/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        "200": {description: ok}
        "401": {description: no session}
        "404": {description: none}
  /v1/orders/{id}/refunds:
    post:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [amount]
              properties:
                amount: {type: integer, minimum: 1, maximum: 1000000}
                reason: {type: string, enum: [damaged, late]}
      responses:
        "201": {description: refunded}
        "400": {description: refused}
        "401": {description: no session}
        "404": {description: none}
`

// edgeDraft has a refund that a trial never sends, a read that needs the order that a write gives, and a
// list that a trial sends. Each is the source of edge cases.
const edgeDraft = `{
  "summary": "Refunds, reads and lists of orders.",
  "tickets": [
    {"key": "SHOP-482", "kind": "behaviour", "behaviour": "a refund above the rest of the total gets 422", "risk": "high", "reason": "money"},
    {"key": "SHOP-483", "kind": "behaviour", "behaviour": "an order is read by its ID and listed", "risk": "low", "reason": "a read"}
  ],
  "scenarios": [
    {
      "ticket": "SHOP-482", "title": "Refund above the order total is rejected", "intent": "Now it is rejected with 422.",
      "steps": [
        {"id": "create-order", "setup": true, "action": "Create a paid order for 100000", "service": "orders",
         "method": "POST", "hint": "/v1/orders", "path": "/v1/orders", "json": {"amount": 100000},
         "captures": [{"name": "order_id", "jsonpath": "$.data.id"}], "expect": []},
        {"id": "refund-too-much", "action": "Request a refund of 150000", "service": "orders",
         "method": "POST", "hint": "/v1/orders/{id}/refunds", "path": "/v1/orders/{{order_id}}/refunds",
         "json": {"amount": 150000, "reason": "damaged"},
         "expect": [{"check": {"status": 422}, "why": "internal/refund/handler.go:9 returns 422 above the rest"}]}
      ]
    },
    {
      "ticket": "SHOP-483", "title": "An order is read by its ID", "intent": "The owner reads the order.",
      "steps": [
        {"id": "create-order", "setup": true, "action": "Create an order", "method": "POST", "hint": "/v1/orders",
         "path": "/v1/orders", "json": {"amount": 100000}, "captures": [{"name": "order_id", "jsonpath": "$.data.id"}], "expect": []},
        {"id": "read-order", "action": "Read the order", "method": "GET", "hint": "/v1/orders/{id}", "path": "/v1/orders/{{order_id}}",
         "expect": [{"check": {"status": 200}, "why": "internal/orders/read.go:5 returns 200"}]}
      ]
    },
    {
      "ticket": "SHOP-483", "title": "Orders are listed by status", "intent": "The owner lists the created orders.",
      "steps": [
        {"id": "list-orders", "action": "List the created orders", "method": "GET", "hint": "/v1/orders", "path": "/v1/orders",
         "query": {"status": "created"},
         "expect": [{"check": {"status": 200}, "why": "internal/orders/read.go:5 returns 200"}]}
      ]
    }
  ],
  "notDrafted": [], "uncovered": []
}`

// edgeRepo is a plan repository with an API document, a second account, and the edge draft.
func edgeRepo(t *testing.T, extra string) *planRepo {
	t.Helper()
	p := newPlanRepo(t, sampleapp.Options{})
	p.git.Commit("SHOP-482 document the API", map[string]*string{"docs/openapi.yaml": gittest.S(edgeAPI)})
	p.configure(t, edgeDraft)
	t.Setenv(edgeTokenBob, "tok-bob")
	p.rewriteConfig(t, func(s string) string {
		s = strings.Replace(s, "providers:\n", "  bob:\n    session: {header: Authorization, value: \"Bearer {{secret.TOKEN}}\"}\n    secrets: {TOKEN: \"env:"+edgeTokenBob+"\"}\nproviders:\n", 1)
		return s + "knowledge:\n  - id: api\n    kind: openapi\n    path: docs/openapi.yaml\n    service: orders\n" + extra
	})
	return p
}

func (p *planRepo) edgeFiles(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob(p.path(".semar", "scenarios", "*", "*-E*.feature"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(filepath.Base(f), ".feature")] = string(data)
	}
	return out
}

func edgeKinds(files map[string]string) []string {
	var kinds []string
	re := regexp.MustCompile(`@edge-kind:(\S+)`)
	for _, text := range files {
		if m := re.FindStringSubmatch(text); m != nil && !slices.Contains(kinds, m[1]) {
			kinds = append(kinds, m[1])
		}
	}
	slices.Sort(kinds)
	return kinds
}

func TestPlanDraftsEdgeCasesAndCasesAsAnotherAccountWithNoModel(t *testing.T) {
	p := edgeRepo(t, "")
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	contains(t, "stdout", res.stdout,
		"edge cases: 12 drafted, 5 left out (",
		"2 because it is over the limit of 6 for one operation",
		"1 because the API document declares no fitting status",
		"1 because its request could not be tried as another account",
		"1 because it writes, and a write is never tried as another account",
		"; each needs your review\n",
		"SHOP-482 (behaviour): 1 scenario", "SHOP-483 (behaviour): 2 scenarios")

	files := p.edgeFiles(t)
	if len(files) != 12 {
		t.Fatalf("%d edge case files, want 12: %v", len(files), files)
	}
	want := []string{"not-in-enum", "no-session", "other-account", "out-of-range", "required-field", "unknown-id", "wrong-type"}
	slices.Sort(want)
	if got := edgeKinds(files); !slices.Equal(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}

	required := files["SHOP-482-E01"]
	contains(t, "SHOP-482-E01", required,
		"@edge @edge-kind:required-field @edge-base:SHOP-482-01 @edge-on:amount",
		"Scenario: Edge case: POST /v1/orders/{id}/refunds without the required field amount\n",
		"A fixed rule drafted it from the API document, and no model took part.",
		"the status is 400\n", "# rule the API document declares 400 for POST /v1/orders/{id}/refunds",
		"Not tried in the plan: needs {{order_id}}")
	if strings.Contains(required, "by model") {
		t.Errorf("an edge case says that a model took part:\n%s", required)
	}

	var noSession, other, unknown string
	for id, text := range files {
		switch {
		case strings.Contains(text, "@edge-kind:no-session") && strings.Contains(text, "@id:SHOP-483-"):
			noSession = id
		case strings.Contains(text, "@edge-kind:other-account"):
			other = id
		case strings.Contains(text, "@edge-kind:unknown-id") && strings.Contains(text, "@id:SHOP-483-"):
			unknown = id
		}
	}
	contains(t, noSession, files[noSession], " @noauth", "the status is 401\n")
	contains(t, other, files[other], "@auth:bob", "the status is 200\n", "# rule observed in the plan trial as account bob, not required by the API document",
		"Tried once in the plan, as that account.")
	contains(t, unknown, files[unknown], "GET /v1/orders/{id} with an unknown id", "the status is 404\n", "Tried once in the plan: the environment answered 404.")

	// A trial sends reads only, and a request as another account is a read of the plan.
	if n := p.app.Requests("POST", ""); n != 0 {
		t.Errorf("the plan sent %d writes", n)
	}
	if n := p.app.Requests("GET", "/v1/orders/semar-no-such-id"); n != 1 {
		t.Errorf("the unknown-id case was sent %d times, want once", n)
	}

	// No case changes a use case before a person approves it.
	obligations, err := filepath.Glob(p.path(".semar", "obligations", "*.yaml"))
	if err != nil || len(obligations) == 0 {
		t.Fatalf("obligations %v, %v", obligations, err)
	}
	for _, f := range obligations {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "-E0") || strings.Contains(string(data), "-E1") {
			t.Errorf("%s holds an edge case before a person approved it:\n%s", f, data)
		}
	}
	md := p.planMD(t)
	contains(t, "plan.md", md, "SHOP-482-E01", "Edge case: GET /v1/orders/{id} with an unknown id")

	// After the approval, a case is one more test of the use case of its base.
	approved := p.must(t, cli.ExitOK, "review", "--yes")
	contains(t, "review", approved.stdout, "approved SHOP-482-E01")
	data, err := os.ReadFile(p.path(".semar", "obligations", "SHOP-482.plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the use case of SHOP-482-01", string(data), "covers: [SHOP-482-01, SHOP-482-E01")
	p.assertNoSecret(t, []string{token, "tok-bob"})
}

func TestPlanLimitsEdgeCasesByPriority(t *testing.T) {
	p := edgeRepo(t, "plan:\n  maxEdgeCases: 5\n")
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	contains(t, "stdout", res.stdout, "edge cases: 5 drafted, ", "because it is over the limit of 5 for one plan")
	files := p.edgeFiles(t)
	if len(files) != 5 {
		t.Fatalf("%d edge case files, want 5", len(files))
	}
	for _, kind := range edgeKinds(files) {
		if kind == "no-session" || kind == "other-account" || kind == "unknown-id" {
			t.Errorf("a case of kind %s was kept over a kind of a higher priority", kind)
		}
	}
}

func TestPlanDraftsNoEdgeCaseWhenTheyAreTurnedOff(t *testing.T) {
	for name, tc := range map[string]struct {
		extra string
		args  []string
	}{
		"the flag":    {"", []string{"--no-edge"}},
		"the setting": {"plan:\n  maxEdgeCases: 0\n", nil},
	} {
		t.Run(name, func(t *testing.T) {
			p := edgeRepo(t, tc.extra)
			res := p.must(t, cli.ExitOK, append([]string{"plan", "--diff", "main..release"}, tc.args...)...)
			if strings.Contains(res.stdout, "edge cases") {
				t.Errorf("the plan mentions edge cases:\n%s", res.stdout)
			}
			if files := p.edgeFiles(t); len(files) != 0 {
				t.Errorf("edge cases were written: %v", files)
			}
		})
	}
}

func TestPlanNeedsATrialToDraftEdgeCases(t *testing.T) {
	p := edgeRepo(t, "")
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial")
	if strings.Contains(res.stdout, "edge cases") || len(p.edgeFiles(t)) != 0 {
		t.Errorf("edge cases with no trial:\n%s", res.stdout)
	}
}

func TestADroppedEdgeCaseIsRemovedAndNotDraftedAgain(t *testing.T) {
	p := edgeRepo(t, "")
	p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	var id string
	for name, text := range p.edgeFiles(t) {
		if strings.Contains(text, "@edge-kind:unknown-id") && strings.Contains(text, "@id:SHOP-483-") {
			id = name
		}
	}
	if id == "" {
		t.Fatal("no unknown-id case of SHOP-483")
	}

	p.opts.Terminal = func() bool { return true }
	p.opts.Stdin = strings.NewReader("x\n")
	res := p.must(t, cli.ExitOK, "review", id)
	contains(t, "review", res.stdout, "[x] drop", "retired scenario "+id, "approved 0 of 1 item, dropped 1")
	if _, ok := p.edgeFiles(t)[id]; ok {
		t.Errorf("%s is still there", id)
	}
	dropped, err := os.ReadFile(p.path(".semar", "edge-dropped.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "edge-dropped.yaml", string(dropped), "SHOP-483|GET /v1/orders/{id}|unknown-id|id", "scenario: "+id, "reason: dropped in the review")

	// The same plan again has to start from new base tests, so the earlier ones are retired first. The edge
	// cases that were not dropped go too, by file, because semar rm would drop them as well.
	for _, base := range []string{"SHOP-482-01", "SHOP-483-01", "SHOP-483-02"} {
		p.must(t, cli.ExitOK, "rm", base, "--reason", "plan again")
	}
	files, err := filepath.Glob(p.path(".semar", "scenarios", "*", "*-E*.feature"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := filepath.Glob(p.path(".semar", "store", "scripts", "*-E*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range stored {
		if err := os.RemoveAll(d); err != nil {
			t.Fatal(err)
		}
	}
	again := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	contains(t, "stdout", again.stdout, "1 because a person dropped it in an earlier review")
	for _, text := range p.edgeFiles(t) {
		if strings.Contains(text, "@edge-kind:unknown-id") && strings.Contains(text, "@id:SHOP-483-") {
			t.Errorf("the dropped case was drafted again:\n%s", text)
		}
	}
}
