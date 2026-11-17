package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

const goodDraft = `{
  "summary": "Refunds above the order total are now rejected, and an order can be read.",
  "tickets": [
    {"key": "SHOP-482", "kind": "behaviour", "behaviour": "a refund above the total gets 422", "risk": "standard", "reason": "money moves"},
    {"key": "SHOP-483", "kind": "refactor", "behaviour": "none", "risk": "low", "reason": "a rename"}
  ],
  "scenarios": [
    {
      "ticket": "SHOP-482",
      "title": "Refund above the order total is rejected",
      "intent": "Before: accepted. Now: 422.",
      "steps": [
        {
          "id": "create-order", "setup": true, "action": "Create a paid order for 100000",
          "method": "POST", "hint": "/v1/orders", "path": "/v1/orders",
          "json": {"amount": 100000},
          "captures": [{"name": "order_id", "jsonpath": "$.data.id"}],
          "expect": []
        },
        {
          "id": "refund-too-much", "action": "Request a refund of 150000",
          "method": "POST", "hint": "/v1/orders/{id}/refunds", "path": "/v1/orders/{{order_id}}/refunds",
          "json": {"amount": 150000},
          "expect": [
            {"check": {"status": 422}, "why": "internal/refund/handler.go:41 returns 422 when the amount is above the rest"},
            {"check": {"jsonpath": "$.error.code", "equals": "REFUND_EXCEEDS_TOTAL"}, "why": "internal/refund/handler.go:42 names the code"}
          ]
        }
      ]
    },
    {
      "ticket": "SHOP-483",
      "title": "An order can be read back",
      "intent": "The read must stay the same.",
      "steps": [
        {
          "id": "create-order", "setup": true, "action": "Create an order",
          "method": "POST", "hint": "/v1/orders", "path": "/v1/orders",
          "json": {"amount": 5},
          "captures": [{"name": "order_id", "jsonpath": "$.data.id"}],
          "expect": []
        },
        {
          "id": "read-order", "action": "Read it",
          "method": "GET", "hint": "/v1/orders/{id}", "path": "/v1/orders/{{order_id}}",
          "expect": [{"check": {"jsonpath": "$.data.amount", "equals": 5}, "why": "internal/orders/read.go:3 returns the amount"}]
        }
      ]
    }
  ],
  "notDrafted": [{"ticket": "SHOP-482", "what": "a refund on another user's order", "needs": "a second test account"}],
  "uncovered": [{"file": "internal/refund/audit.go", "reason": "writes a row that no endpoint returns"}]
}`

func lines(n int) string { return strings.Repeat("// line\n", n) }

func sourceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"internal/refund/handler.go": lines(50),
		"internal/orders/read.go":    "package orders\n\nfunc read() {}", // three lines, no final newline
		PlanDir + "/changes.md":      lines(10),
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func input(t *testing.T) Input {
	t.Helper()
	return Input{
		Change: domain.ChangeSet{
			Base: "main", Head: "release", BaseCommit: strings.Repeat("a", 40), HeadCommit: strings.Repeat("b", 40),
			Tickets: []domain.TicketChange{{Key: "SHOP-482"}, {Key: "SHOP-483"}},
		},
		SourceDir:     sourceDir(t),
		Services:      []string{"orders"},
		Methods:       []string{"GET", "POST"},
		AuthProfile:   "default",
		SessionHeader: "X-Session",
		Limits:        config.Plan{MaxScenarios: 12},
	}
}

func draft(t *testing.T) Draft {
	t.Helper()
	d, err := DecodeDraft([]byte(goodDraft))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestValidateBuildsScenariosAndScripts(t *testing.T) {
	in := input(t)
	in.Existing = map[string][]domain.Scenario{"SHOP-482": {{ID: "SHOP-482-03", Title: "Another"}, {ID: "SHOP-482-x", Title: "Odd"}}}
	scs, scripts, ps := Validate(draft(t), in)
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	if len(scs) != 2 || len(scripts) != 2 {
		t.Fatalf("%d scenarios and %d scripts, want 2 and 2", len(scs), len(scripts))
	}
	if scs[0].ID != "SHOP-482-04" || scs[1].ID != "SHOP-483-01" {
		t.Errorf("IDs %s and %s, want SHOP-482-04 and SHOP-483-01", scs[0].ID, scs[1].ID)
	}
	sc := scs[0]
	if sc.APIVersion != "semar/v1" || sc.Kind != "Scenario" || sc.Surface != domain.SurfaceAPI || sc.AuthProfile != "default" || sc.Ticket != "SHOP-482" {
		t.Errorf("scenario header = %+v", sc)
	}
	refund := sc.Steps[1]
	if refund.Hint == nil || *refund.Hint != (domain.RequestHint{Method: "POST", Path: "/v1/orders/{id}/refunds"}) {
		t.Errorf("hint = %+v", refund.Hint)
	}
	for _, e := range refund.Expect {
		if e.Source != domain.SourceDiff || !strings.HasPrefix(e.Cite, "internal/refund/handler.go:4") {
			t.Errorf("expectation = %+v, want the source diff and the why as its cite", e)
		}
	}
	scr := scripts[0]
	if scr.Scenario != sc.ID || scr.ExpectHash != domain.ExpectHash(sc) || scr.ExpectHash == "" {
		t.Errorf("script binds %s/%s, want %s/%s", scr.Scenario, scr.ExpectHash, sc.ID, domain.ExpectHash(sc))
	}
	req := scr.Steps[1].Request
	if req.Service != "orders" || req.Method != "POST" || req.Path != "/v1/orders/{{order_id}}/refunds" {
		t.Errorf("request = %+v", req)
	}
	if !reflect.DeepEqual(req.JSON, map[string]any{"amount": 150000}) {
		t.Errorf("json = %#v, want the amount as an integer", req.JSON)
	}
	if !reflect.DeepEqual(scr.Steps[0].Captures, []domain.Capture{{Name: "order_id", JSONPath: "$.data.id"}}) {
		t.Errorf("captures = %+v", scr.Steps[0].Captures)
	}
}

func TestValidateCanonicalisesAWholeNumber(t *testing.T) {
	d := draft(t)
	d.Scenarios[1].Steps[1].Expect[0].Check.Equals = json.Number("5.0")
	d.Scenarios[1].Steps[1].Expect = append(d.Scenarios[1].Steps[1].Expect, ExpectDraft{
		Check: CheckDraft{JSONPath: "$.data", Contains: map[string]any{"amount": json.Number("5e0"), "list": []any{json.Number("-0")}}},
		Why:   "internal/orders/read.go:3 returns the order",
	})
	scs, _, ps := Validate(d, input(t))
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	exp := scs[1].Steps[1].Expect
	if exp[0].Check.Equals != 5 {
		t.Errorf("equals = %#v, want the integer 5", exp[0].Check.Equals)
	}
	if want := map[string]any{"amount": 5, "list": []any{0}}; !reflect.DeepEqual(exp[1].Check.Contains, want) {
		t.Errorf("contains = %#v, want %#v", exp[1].Check.Contains, want)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	type mutate func(d *Draft, in *Input)
	refund := func(d *Draft) *StepDraft { return &d.Scenarios[0].Steps[1] }
	tests := []struct {
		name   string
		change mutate
		path   string
		says   string
	}{
		{"a scenario ticket not in the change set", func(d *Draft, _ *Input) { d.Scenarios[1].Ticket = "SHOP-999" },
			"scenarios[1].ticket", "SHOP-999"},
		{"a listed ticket not in the change set", func(d *Draft, _ *Input) {
			d.Tickets = append(d.Tickets, TicketDraft{Key: "OPS-1", Kind: "none", Risk: "low"})
		}, "tickets[2].key", "OPS-1"},
		{"a ticket of the change set with no entry", func(d *Draft, _ *Input) { d.Tickets = d.Tickets[:1] },
			"tickets", "SHOP-483"},
		{"a ticket listed twice", func(d *Draft, _ *Input) { d.Tickets = append(d.Tickets, d.Tickets[1]) },
			"tickets[2].key", "more than once"},
		{"an unknown kind", func(d *Draft, _ *Input) { d.Tickets[1].Kind = "cleanup" }, "tickets[1].kind", "behaviour, refactor"},
		{"an unknown risk", func(d *Draft, _ *Input) { d.Tickets[1].Risk = "medium" }, "tickets[1].risk", "low, standard, high"},
		{"an unknown service", func(d *Draft, _ *Input) { refund(d).Service = "payments" },
			"scenarios[0].steps[1].service", "payments"},
		{"no service when there are several", func(_ *Draft, in *Input) { in.Services = []string{"orders", "payments"} },
			"scenarios[0].steps[0].service", "orders, payments"},
		{"a method the environment does not allow", func(d *Draft, _ *Input) { refund(d).Method = "DELETE" },
			"scenarios[0].steps[1].method", "GET, POST"},
		{"a step ID that repeats", func(d *Draft, _ *Input) { refund(d).ID = "create-order" },
			"scenarios[0].steps[1].id", "duplicate step id"},
		{"a variable used before a step captures it", func(d *Draft, _ *Input) { d.Scenarios[0].Steps[0].Captures = nil },
			"scenarios[0].steps[1]", "order_id is used before any step captures it"},
		{"the session header", func(d *Draft, _ *Input) { refund(d).Headers = map[string]string{"x-session": "abc"} },
			`scenarios[0].steps[1].headers["x-session"]`, "session header"},
		{"an authorization header", func(d *Draft, _ *Input) { refund(d).Headers = map[string]string{"AUTHORIZATION": "Bearer x"} },
			`scenarios[0].steps[1].headers["AUTHORIZATION"]`, "cannot be drafted"},
		{"a cookie header", func(d *Draft, _ *Input) { refund(d).Headers = map[string]string{"Cookie": "a=b"} },
			`scenarios[0].steps[1].headers["Cookie"]`, "cannot be drafted"},
		{"a host header", func(d *Draft, _ *Input) { refund(d).Headers = map[string]string{"host": "evil.example"} },
			`scenarios[0].steps[1].headers["host"]`, "cannot be drafted"},
		{"a header that frames the message", func(d *Draft, _ *Input) { refund(d).Headers = map[string]string{"Transfer-Encoding": "chunked"} },
			"scenarios[0].steps[1]", "set by the HTTP client"},
		{"a header name that is not a token", func(d *Draft, _ *Input) { refund(d).Headers = map[string]string{"X Bad": "1"} },
			"scenarios[0].steps[1]", "not a valid HTTP header name"},
		{"a header value with a line break", func(d *Draft, _ *Input) { refund(d).Headers = map[string]string{"X-A": "1\r\nX-B: 2"} },
			"scenarios[0].steps[1]", "control character"},
		{"a why with no file and line", func(d *Draft, _ *Input) { refund(d).Expect[0].Why = "the handler returns 422" },
			"scenarios[0].steps[1].expect[0].why", "<file>:<line>"},
		{"a why that names no file of the source", func(d *Draft, _ *Input) { refund(d).Expect[0].Why = "internal/nope.go:1 x" },
			"scenarios[0].steps[1].expect[0].why", "internal/nope.go is not a file of the source"},
		{"a why past the end of the file", func(d *Draft, _ *Input) { refund(d).Expect[0].Why = "internal/orders/read.go:4 x" },
			"scenarios[0].steps[1].expect[0].why", "has 3 lines"},
		{"a why at line zero", func(d *Draft, _ *Input) { refund(d).Expect[0].Why = "internal/orders/read.go:0 x" },
			"scenarios[0].steps[1].expect[0].why", "lines start at 1"},
		{"a why outside the source", func(d *Draft, _ *Input) { refund(d).Expect[0].Why = "../outside.go:1 x" },
			"scenarios[0].steps[1].expect[0].why", "not a path inside the source"},
		{"a why that names a directory", func(d *Draft, _ *Input) { refund(d).Expect[0].Why = "internal/orders:1 x" },
			"scenarios[0].steps[1].expect[0].why", "not a regular file"},
		{"a why that names what Semar wrote", func(d *Draft, _ *Input) { refund(d).Expect[0].Why = PlanDir + "/changes.md:2 x" },
			"scenarios[0].steps[1].expect[0].why", "written for you"},
		{"a scenario with no expectation", func(d *Draft, _ *Input) { d.Scenarios[1].Steps[1].Expect = nil },
			"scenarios[1]", "at least one expected result"},
		{"a title that exists for the ticket", func(_ *Draft, in *Input) {
			in.Existing = map[string][]domain.Scenario{"SHOP-483": {{ID: "SHOP-483-01", Title: "an order can be  read back"}}}
		}, "scenarios[1].title", "repeats"},
		{"a title that repeats in the draft", func(d *Draft, _ *Input) {
			extra := d.Scenarios[0]
			d.Scenarios = append(d.Scenarios, extra)
		}, "scenarios[2].title", "repeats"},
		{"a behaviour ticket with neither a scenario nor a notDrafted entry", func(d *Draft, _ *Input) {
			d.Tickets[1].Kind = "behaviour"
			d.Scenarios = d.Scenarios[:1]
		}, "tickets[1]", "neither a scenario nor a notDrafted entry"},
		{"more scenarios than the limit", func(_ *Draft, in *Input) { in.Limits.MaxScenarios = 1 }, "scenarios", "limit is 1"},
		{"a notDrafted ticket not in the change set", func(d *Draft, _ *Input) { d.NotDrafted[0].Ticket = "X-1" },
			"notDrafted[0].ticket", "X-1"},
		{"a check with two kinds (scenario.Validate)", func(d *Draft, _ *Input) { refund(d).Expect[0].Check.JSONPath = "$.a" },
			"scenarios[0].steps[1].expect[0].check", "exactly one of status, jsonpath, header"},
		{"a check with no operator (scenario.Validate)", func(d *Draft, _ *Input) { refund(d).Expect[1].Check.Equals = nil },
			"scenarios[0].steps[1].expect[1].check", "exactly one operator"},
		{"a jsonpath that does not parse (scenario.Validate)", func(d *Draft, _ *Input) { refund(d).Expect[1].Check.JSONPath = "$[" },
			"scenarios[0].steps[1].expect[1].check.jsonpath", "does not parse"},
		{"a setup step with an expectation (scenario.Validate)", func(d *Draft, _ *Input) {
			d.Scenarios[0].Steps[0].Expect = []ExpectDraft{{Check: CheckDraft{Status: new(201)}, Why: "internal/refund/handler.go:1 x"}}
		}, "scenarios[0].steps[0].expect", "setup step"},
		{"a step ID that is not an ID (scenario.Validate)", func(d *Draft, _ *Input) { refund(d).ID = "refund too much" },
			"scenarios[0].steps[1].id", "must match"},
		{"a path that does not match the hint (script.Validate)", func(d *Draft, _ *Input) { refund(d).Path = "/v1/refunds" },
			"scenarios[0].steps[1]", "does not match"},
		{"a path with a host (script.Validate)", func(d *Draft, _ *Input) { refund(d).Path = "https://evil.example/x" },
			"scenarios[0].steps[1]", "must start with /"},
		{"a secret placeholder (script.Validate)", func(d *Draft, _ *Input) {
			refund(d).Headers = map[string]string{"X-Key": "{{secret.TOKEN}}"}
		}, "scenarios[0].steps[1]", "malformed variable"},
		{"a capture name that is not a name (script.Validate)", func(d *Draft, _ *Input) {
			d.Scenarios[0].Steps[0].Captures = append(d.Scenarios[0].Steps[0].Captures, CaptureDraft{Name: "1x", JSONPath: "$.a"})
		}, "scenarios[0].steps[0]", "not a valid variable name"},
		{"a number with no exact text (scenario.Marshal)", func(d *Draft, _ *Input) {
			refund(d).Expect[1].Check.Equals = json.Number("0.1000000000000000055511151231257827")
		}, "scenarios[0].steps[1].expect[1].check.equals", "cannot be held exactly"},
		{"a bound that a float rounds", func(d *Draft, _ *Input) {
			refund(d).Expect[1].Check = CheckDraft{JSONPath: "$.a", Greater: new(json.Number("9007199254740993"))}
		}, "scenarios[0].steps[1].expect[1].check.greater", "cannot be held exactly"},
		{"text that is not UTF-8 (script.Marshal)", func(d *Draft, _ *Input) { refund(d).JSON = map[string]any{"note": "\xff"} },
			"scenarios[0].steps[1].json.note", "UTF-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, in := draft(t), input(t)
			tt.change(&d, &in)
			scs, scripts, ps := Validate(d, in)
			if scs != nil || scripts != nil {
				t.Error("Validate returned values with problems")
			}
			for _, p := range ps {
				if p.Path == tt.path && strings.Contains(p.Message, tt.says) {
					return
				}
			}
			t.Errorf("no problem at %s that says %q; got:\n%s", tt.path, tt.says, problemList(ps))
		})
	}
}

func TestValidateReturnsEveryProblemInOrder(t *testing.T) {
	d, in := draft(t), input(t)
	d.Tickets[1].Risk = "medium"
	d.Scenarios[0].Steps[1].Method = "DELETE"
	d.Scenarios[1].Steps[1].Expect[0].Why = "nowhere"
	_, _, first := Validate(d, in)
	_, _, second := Validate(d, in)
	if len(first) < 3 {
		t.Fatalf("%d problems, want at least 3:\n%s", len(first), problemList(first))
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two runs gave different problems:\n%s\n---\n%s", problemList(first), problemList(second))
	}
	if first[0].Path != "tickets[1].risk" {
		t.Errorf("first problem at %s, want tickets[1].risk", first[0].Path)
	}
}

func TestDecodeDraftIsStrict(t *testing.T) {
	for name, data := range map[string]string{
		"an unknown field": `{"summary": "", "tickets": [], "scenarios": [], "notDrafted": [], "uncovered": [], "extra": 1}`,
		"a field of a manual check that does not exist": `{"manualChecks": [{"ticket": "A-1", "owner": "x"}]}`,
		"trailing data": `{"summary": ""} {}`,
		"not an object": `[1]`,
	} {
		if _, err := DecodeDraft([]byte(data)); err == nil {
			t.Errorf("%s: DecodeDraft accepted it", name)
		}
	}
	d, err := DecodeDraft([]byte(`{"scenarios": [{"steps": [{"json": {"a": 1.50}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Scenarios[0].Steps[0].JSON.(map[string]any)["a"]; got != json.Number("1.50") {
		t.Errorf("json.a = %#v, want the text 1.50", got)
	}
}

func TestSchema(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(Schema(), &root); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	walk(t, "", root)

	check := dig(t, root, "scenarios", "steps", "expect", "check")
	var got []string
	for k := range check["properties"].(map[string]any) {
		got = append(got, k)
	}
	var want []string
	ct := reflect.TypeFor[domain.Check]()
	for i := range ct.NumField() {
		name, _, _ := strings.Cut(ct.Field(i).Tag.Get("yaml"), ",")
		// The planner drafts steps for the API. An element check belongs to a web step, which a person writes.
		if name != "element" {
			want = append(want, name)
		}
	}
	if !sameSet(got, want) {
		t.Errorf("check properties %v, want the fields of domain.Check %v", got, want)
	}
}

// walk refuses a property named source, except the choice of an expectation between diff and ticket
// (R17: no source of a scenario or a host), and every keyword that a tool schema may not hold.
func walk(t *testing.T, path string, node map[string]any) {
	t.Helper()
	allowed := []string{"type", "properties", "required", "items", "enum", "additionalProperties"}
	for k, v := range node {
		if !slices.Contains(allowed, k) {
			t.Errorf("%s uses the keyword %s", path, k)
		}
		if k == "additionalProperties" && v != false {
			t.Errorf("%s: additionalProperties is %v, want false", path, v)
		}
	}
	if props, ok := node["properties"].(map[string]any); ok {
		if node["additionalProperties"] != false {
			t.Errorf("%s has fixed keys and allows others", path)
		}
		for name, sub := range props {
			if strings.EqualFold(name, "source") && path != ".scenarios[].steps[].expect[]" {
				t.Errorf("%s has a property named %s", path, name)
			}
			walk(t, path+"."+name, sub.(map[string]any))
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		walk(t, path+"[]", items)
	}
}

func dig(t *testing.T, node map[string]any, names ...string) map[string]any {
	t.Helper()
	for _, n := range names {
		node = node["properties"].(map[string]any)[n].(map[string]any)
		if items, ok := node["items"].(map[string]any); ok {
			node = items
		}
	}
	return node
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// A not-found test may use an ID that cannot exist, built from the id of the run (D10).
func TestMissingIDFormPassesValidation(t *testing.T) {
	d := draft(t)
	d.Scenarios[1].Steps[1].Path = "/v1/orders/missing-{{run.id}}"
	d.Scenarios[1].Steps[1].Expect[0] = ExpectDraft{Check: CheckDraft{Status: new(404)}, Why: "internal/orders/read.go:3 returns 404 for an unknown order"}
	if _, _, ps := Validate(d, input(t)); len(ps) > 0 {
		t.Fatalf("the missing-ID form is refused: %s", problemTexts(ps))
	}
}
