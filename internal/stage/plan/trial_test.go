package plan

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/openapi"
)

func doc(ops ...string) openapi.Document {
	d := openapi.Document{Version: "3.0.3"}
	for _, o := range ops {
		method, path, _ := strings.Cut(o, " ")
		d.Operations = append(d.Operations, openapi.Operation{Method: method, Path: path})
	}
	return d
}

func statusExpect(code int) []domain.Expectation {
	return []domain.Expectation{{Check: domain.Check{Status: &code}}}
}

// trialScenarios is one scenario with a setup write, a read, and a read that expects a 404.
func trialScenarios() []domain.Scenario {
	hint := func(m, p string) *domain.RequestHint { return &domain.RequestHint{Method: m, Path: p} }
	return []domain.Scenario{
		{ID: "SHOP-1-01", Title: "first", Ticket: "SHOP-1", Steps: []domain.Step{
			{ID: "make", Setup: true, Hint: hint("POST", "/v1/orders")},
			{ID: "read", Hint: hint("GET", "/v1/orders/{id}"), Expect: statusExpect(200)},
			{ID: "missing", Hint: hint("GET", "/v1/orders/{id}/gone"), Expect: statusExpect(404)},
		}},
		{ID: "SHOP-1-02", Title: "second", Ticket: "SHOP-1", Steps: []domain.Step{
			{ID: "list", Hint: hint("GET", "/v1/orders"), Expect: statusExpect(200)},
		}},
	}
}

func sent(sc domain.ScenarioID, step domain.StepID, method, route string, status int, missing ...string) TrialStep {
	return TrialStep{Scenario: sc, Step: step, Result: TrialSent, Method: method, Route: route, Status: status, Missing: missing}
}

func TestTrialProblemsTable(t *testing.T) {
	base := []openapi.Document{doc("GET /v1/orders/{orderId}")}
	tests := []struct {
		name   string
		step   TrialStep
		base   []openapi.Document
		head   []openapi.Document
		back   []Problem
		second int
	}{
		{"405", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 405), base, nil,
			[]Problem{{"scenarios[0].steps[1]", "trial: the environment answered 405 for GET /v1/orders/{id}: the method is likely wrong for this route"}}, 0},
		{"404 on a route of the base document", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 404), base, nil,
			[]Problem{{"scenarios[0].steps[1]", "trial: the environment answered 404 for GET /v1/orders/{id}, a route that the API document had before the change: an ID in the path is likely wrong"}}, 0},
		{"404 on a route that no document has", sent("SHOP-1-01", "read", "GET", "/v1/order/{id}", 404), base, nil,
			[]Problem{{"scenarios[0].steps[1]", "trial: the environment answered 404 for GET /v1/order/{id}, and no API document has this route: the route is likely wrong"}}, 0},
		{"404 on a route that only the head document has", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 404), nil, base, nil, 1},
		{"404 with no API document", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 404), nil, nil, nil, 1},
		{"400", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 400), nil, nil,
			[]Problem{{"scenarios[0].steps[1]", "trial: the environment answered 400 for GET /v1/orders/{id}: a query parameter or a header is likely wrong"}}, 0},
		{"422", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 422), nil, nil,
			[]Problem{{"scenarios[0].steps[1]", "trial: the environment answered 422 for GET /v1/orders/{id}: a query parameter or a header is likely wrong"}}, 0},
		{"401", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 401), nil, nil,
			[]Problem{{"scenarios[0].steps[1]", "trial: the environment answered 401 for GET /v1/orders/{id} as auth profile support-agent: the route may need another account, or a header is wrong"}}, 0},
		{"403", sent("SHOP-1-02", "list", "GET", "/v1/orders", 403), nil, nil,
			[]Problem{{"scenarios[1].steps[0]", "trial: the environment answered 403 for GET /v1/orders as auth profile support-agent: the route may need another account, or a header is wrong"}}, 0},
		{"a capture that found nothing", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 200, "order_id"), nil, nil,
			[]Problem{{"scenarios[0].steps[1]", "trial: capture order_id found no value in the response of GET /v1/orders/{id}: the JSONPath is likely wrong, or the environment holds no such record and the scenario needs a fixture"}}, 0},
		{"a capture that found nothing after a 3xx", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 302, "order_id"), nil, nil,
			[]Problem{{"scenarios[0].steps[1]", "trial: capture order_id found no value in the response of GET /v1/orders/{id}: the JSONPath is likely wrong, or the environment holds no such record and the scenario needs a fixture"}}, 0},
		{"a capture of a 4xx answer is the status problem only", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 400, "order_id"), nil, nil,
			[]Problem{{"scenarios[0].steps[1]", "trial: the environment answered 400 for GET /v1/orders/{id}: a query parameter or a header is likely wrong"}}, 0},
		{"500", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 500), nil, nil, nil, 1},
		{"503", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 503, "order_id"), nil, nil, nil, 1},
		{"200 with every capture found", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 200), nil, nil, nil, 0},
		{"409 is not a trial problem", sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 409), nil, nil, nil, 0},
		{"no response", TrialStep{Scenario: "SHOP-1-01", Step: "read", Result: TrialSent, Method: "GET", Route: "/v1/orders/{id}", Err: "boom"}, nil, nil, nil, 0},
		{"not tried", TrialStep{Scenario: "SHOP-1-01", Step: "make", Result: TrialNotTried, Why: "writes", Method: "POST", Route: "/v1/orders"}, nil, nil, nil, 0},
		{"a given entry", TrialStep{Scenario: "SHOP-1-01", Given: "order", Result: TrialSent, Found: false}, nil, nil, nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			back, second := trialProblems(Trial{Steps: []TrialStep{tc.step}}, trialScenarios(), tc.base, tc.head, "support-agent")
			if fmt.Sprint(back) != fmt.Sprint(tc.back) {
				t.Errorf("back = %v, want %v", back, tc.back)
			}
			if len(second) != tc.second {
				t.Errorf("second look = %v, want %d", second, tc.second)
			}
		})
	}
}

func TestNotFoundRules(t *testing.T) {
	step := sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 404)
	cases := []struct {
		name       string
		base, head []openapi.Document
		wantBack   int
		wantSecond int
	}{
		{"the route is in the base document", []openapi.Document{doc("GET /v1/orders/{id}")}, nil, 1, 0},
		{"the route is in both documents", []openapi.Document{doc("GET /v1/orders/{id}")}, []openapi.Document{doc("GET /v1/orders/{id}")}, 1, 0},
		{"the route is in no document", []openapi.Document{doc("GET /v1/items")}, []openapi.Document{doc("GET /v1/items")}, 1, 0},
		{"the route is in the head document only", []openapi.Document{doc("GET /v1/items")}, []openapi.Document{doc("GET /v1/orders/{id}")}, 0, 1},
		{"the base document is missing and the head has the route", nil, []openapi.Document{doc("GET /v1/orders/{id}")}, 0, 1},
		{"the base document is missing and the head lacks the route", nil, []openapi.Document{doc("GET /v1/items")}, 1, 0},
		{"no document at all", nil, nil, 0, 1},
		{"a parameter name that differs still matches", []openapi.Document{doc("GET /v1/orders/{orderId}")}, nil, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			back, second := trialProblems(Trial{Steps: []TrialStep{step}}, trialScenarios(), tc.base, tc.head, "p")
			if len(back) != tc.wantBack || len(second) != tc.wantSecond {
				t.Errorf("back %v, second look %v; want %d and %d", back, second, tc.wantBack, tc.wantSecond)
			}
		})
	}
}

// D6: a scenario that expects a refusal sends no status problem and no capture problem back, and
// a 5xx is still a second look. The scenario is read, never the response.
func TestFourXXExpectedSendsNothing(t *testing.T) {
	scs := trialScenarios()
	for _, status := range []int{400, 401, 403, 404, 405, 422} {
		step := sent("SHOP-1-01", "missing", "GET", "/v1/orders/{id}/gone", status, "x")
		back, second := trialProblems(Trial{Steps: []TrialStep{step}}, scs, []openapi.Document{doc("GET /v1/orders/{id}/gone")}, nil, "p")
		if len(back) != 0 || len(second) != 0 {
			t.Errorf("status %d: back %v, second look %v; want nothing", status, back, second)
		}
	}
	back, second := trialProblems(Trial{Steps: []TrialStep{sent("SHOP-1-01", "missing", "GET", "/v1/orders/{id}/gone", 502)}}, scs, nil, nil, "p")
	if len(back) != 0 || len(second) != 1 {
		t.Errorf("a 502: back %v, second look %v; want one second look", back, second)
	}
	// A 4xx check anywhere among the expected results counts, and a body check beside it does not hide it.
	scs[0].Steps[2].Expect = append(scs[0].Steps[2].Expect, domain.Expectation{Check: domain.Check{JSONPath: "$.error"}})
	back, _ = trialProblems(Trial{Steps: []TrialStep{sent("SHOP-1-01", "missing", "GET", "/v1/orders/{id}/gone", 403)}}, scs, nil, nil, "p")
	if len(back) != 0 {
		t.Errorf("back = %v, want nothing", back)
	}
}

func TestProblemCap(t *testing.T) {
	var steps []TrialStep
	for range 27 {
		steps = append(steps, sent("SHOP-1-02", "list", "GET", "/v1/orders", 422))
	}
	back, _ := trialProblems(Trial{Steps: steps}, trialScenarios(), nil, nil, "p")
	if len(back) != 21 {
		t.Fatalf("%d problems, want 20 and one that counts the rest", len(back))
	}
	if last := back[20]; last != (Problem{Message: "trial: and 7 more problems of the same kinds"}) {
		t.Errorf("last = %+v", last)
	}
	for _, p := range back[:20] {
		if p.Path != "scenarios[1].steps[0]" {
			t.Errorf("path = %q", p.Path)
		}
	}
	back, _ = trialProblems(Trial{Steps: steps[:20]}, trialScenarios(), nil, nil, "p")
	if len(back) != 20 {
		t.Errorf("exactly 20 problems give %d", len(back))
	}
}

func TestTrialProblemsIgnoreASkippedTrialAndUnknownSteps(t *testing.T) {
	step := sent("SHOP-1-01", "read", "GET", "/v1/orders/{id}", 422)
	if back, second := trialProblems(Trial{Skipped: "the environment did not answer", Steps: []TrialStep{step}}, trialScenarios(), nil, nil, "p"); len(back)+len(second) != 0 {
		t.Errorf("a skipped trial gave %v and %v", back, second)
	}
	for _, s := range []TrialStep{sent("SHOP-9-01", "read", "GET", "/x", 422), sent("SHOP-1-01", "nope", "GET", "/x", 422)} {
		if back, _ := trialProblems(Trial{Steps: []TrialStep{s}}, trialScenarios(), nil, nil, "p"); len(back) != 0 {
			t.Errorf("a step that is not in the draft gave %v", back)
		}
	}
}
