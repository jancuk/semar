package verdict_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/verdict"
)

func webInput(action domain.Action, result check.Result) verdict.Input {
	sc := domain.Scenario{ID: "board", Steps: []domain.Step{{ID: "read", Expect: []domain.Expectation{
		{Check: domain.Check{Element: "card", Equals: "Buy milk"}, Source: domain.SourcePerson},
	}}}}
	action.ID = "a1"
	action.Use = domain.UseCase{Pass: domain.PassScenario, Scenario: "board", Step: "read"}
	return verdict.Input{
		Scenario:    sc,
		Steps:       map[domain.StepID]verdict.StepEvidence{"read": {Action: &action, Checks: []check.Result{result}}},
		BuildsStart: []domain.BuildRef{{Service: "board", Commit: "c1"}},
		BuildsEnd:   []domain.BuildRef{{Service: "board", Commit: "c1"}},
	}
}

func webAction() domain.Action {
	return domain.Action{Surface: domain.SurfaceWeb, Op: "wait", Web: &domain.WebRecord{Op: "wait"}}
}

func reasons(res domain.ScenarioResult) string {
	var texts []string
	for _, r := range res.Reasons {
		texts = append(texts, r.Text)
	}
	return strings.Join(texts, "; ")
}

func TestWebStepPasses(t *testing.T) {
	res := verdict.Compute(webInput(webAction(), check.Result{State: domain.CheckTrue}))
	if res.Outcome != domain.OutcomePassed {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

func TestWebStepFailsOnAFalseCheck(t *testing.T) {
	res := verdict.Compute(webInput(webAction(), check.Result{State: domain.CheckFalse, Expected: "element card equals Buy milk", Observed: "Buy eggs"}))
	if res.Outcome != domain.OutcomeFailed {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

// A locator that finds nothing is never a failure (TRD rule 5).
func TestWebStepWithAnElementNotFoundIsInconclusive(t *testing.T) {
	a := webAction()
	a.Err = check.NotFound + " (card matched 0)"
	res := verdict.Compute(webInput(a, check.Result{State: domain.CheckNotEvaluated, Detail: check.NotFound}))
	if res.Outcome != domain.OutcomeInconclusive {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
	if !strings.Contains(reasons(res), "an element was not found: either the product changed or the script is out of date") {
		t.Errorf("reasons = %s", reasons(res))
	}
}

func TestWebStepNotEvaluatedCheckIsInconclusive(t *testing.T) {
	res := verdict.Compute(webInput(webAction(), check.Result{State: domain.CheckNotEvaluated, Detail: check.NotFound}))
	if res.Outcome != domain.OutcomeInconclusive {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

func TestRefusedWebWriteIsBlocked(t *testing.T) {
	a := webAction()
	a.Refused = "read_only: click writes, and the environment is read only"
	res := verdict.Compute(webInput(a, check.Result{State: domain.CheckNotEvaluated}))
	if res.Outcome != domain.OutcomeBlocked {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

func TestWebStepNeedsNoInvariant(t *testing.T) {
	res := verdict.Compute(webInput(webAction(), check.Result{State: domain.CheckTrue}))
	for _, r := range res.Reasons {
		if strings.Contains(r.Text, "invariants") {
			t.Errorf("a web step was asked for an invariant: %s", r.Text)
		}
	}
}
