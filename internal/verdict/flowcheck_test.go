package verdict_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/verdict"
)

func flowInput(setup bool, expect []domain.Expectation, fcs ...verdict.FlowCheck) verdict.Input {
	sc := domain.Scenario{ID: "board", Steps: []domain.Step{{ID: "login", Setup: setup, Expect: expect}}}
	a := mobileAction()
	a.ID = "a1"
	pass := domain.PassScenario
	if setup {
		pass = domain.PassSetup
	}
	a.Use = domain.UseCase{Pass: pass, Scenario: "board", Step: "login"}
	return verdict.Input{
		Scenario:    sc,
		Steps:       map[domain.StepID]verdict.StepEvidence{"login": {Action: &a, FlowChecks: fcs}},
		BuildsStart: []domain.BuildRef{{Service: "board", Commit: "c1"}},
		BuildsEnd:   []domain.BuildRef{{Service: "board", Commit: "c1"}},
	}
}

var ownCheck = []domain.Expectation{{Check: domain.Check{Element: "card", Exists: ptrBool(true)}, Source: domain.SourcePerson}}

func ptrBool(b bool) *bool { return &b }

func fc(state domain.CheckState) verdict.FlowCheck {
	return verdict.FlowCheck{Flow: "login", Step: "ready", Result: check.Result{State: state, Expected: "element welcome equals Hi Ana", Observed: "Hi Bob"}}
}

func flowResults(res domain.ScenarioResult) []domain.CheckResult {
	var out []domain.CheckResult
	for _, c := range res.Checks {
		if c.Source == domain.SourceFlow {
			out = append(out, c)
		}
	}
	return out
}

func TestAFalseFlowCheckFails(t *testing.T) {
	in := flowInput(false, ownCheck, fc(domain.CheckFalse))
	in.Steps["login"] = withChecks(in.Steps["login"], check.Result{State: domain.CheckTrue})
	res := verdict.Compute(in)
	if res.Outcome != domain.OutcomeFailed || !strings.Contains(reasons(res), "step login: flow login, step ready: expected element welcome equals Hi Ana, observed Hi Bob") {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
	got := flowResults(res)
	if len(got) != 1 || got[0].Index != -1 || got[0].State != domain.CheckFalse || got[0].ByModel {
		t.Errorf("flow results = %+v", got)
	}
}

func withChecks(ev verdict.StepEvidence, rs ...check.Result) verdict.StepEvidence {
	ev.Checks = rs
	return ev
}

func TestAFalseFlowCheckOfASetupStepBlocks(t *testing.T) {
	res := verdict.Compute(flowInput(true, nil, fc(domain.CheckFalse)))
	if res.Outcome != domain.OutcomeBlocked || !strings.Contains(reasons(res), verdict.SetupFailedPrefix+"login flow login, step ready") {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

func TestAFlowCheckThatWasNotEvaluatedIsInconclusive(t *testing.T) {
	in := flowInput(false, ownCheck, verdict.FlowCheck{Flow: "login", Step: "ready", Result: check.Result{State: domain.CheckNotEvaluated, Detail: "no value for x"}})
	in.Steps["login"] = withChecks(in.Steps["login"], check.Result{State: domain.CheckTrue})
	res := verdict.Compute(in)
	if res.Outcome != domain.OutcomeInconclusive || !strings.Contains(reasons(res), "a check could not be evaluated: no value for x") {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

func TestATrueFlowCheckNeverSatisfiesTheNeedForACheck(t *testing.T) {
	res := verdict.Compute(flowInput(false, nil, fc(domain.CheckTrue)))
	if res.Outcome != domain.OutcomeInconclusive || !strings.Contains(reasons(res), "the scenario has no check besides the invariants") {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
	in := flowInput(false, ownCheck, fc(domain.CheckTrue))
	in.Steps["login"] = withChecks(in.Steps["login"], check.Result{State: domain.CheckTrue})
	res = verdict.Compute(in)
	if res.Outcome != domain.OutcomePassed {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
	if by, n := res.ModelCount(); n != 1 || by != 0 {
		t.Errorf("model count = %d of %d, want 0 of 1: a flow check is not an expected result", by, n)
	}
}
