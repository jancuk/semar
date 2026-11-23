package verdict_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/verdict"
)

// A step that uses a flow ran several actions: the last is the evidence, the earlier ones are Prior, and
// the result lists them all, in the order they ran.
func TestPriorActionsAreListedBeforeTheLastOne(t *testing.T) {
	in := webInput(mobileAction(), check.Result{State: domain.CheckTrue})
	last := in.Steps["read"]
	prior := func(id string) domain.Action {
		return domain.Action{ID: domain.ActionID(id), Surface: domain.SurfaceAndroid, Mobile: &domain.MobileRecord{Op: "tap"},
			Use: domain.UseCase{Pass: domain.PassScenario, Scenario: "board", Step: "read"}}
	}
	last.Prior = []domain.Action{prior("p1"), prior("p2")}
	in.Steps["read"] = last
	res := verdict.Compute(in)
	if res.Outcome != domain.OutcomePassed {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
	if want := []domain.ActionID{"p1", "p2", "a1"}; !slices.Equal(res.Actions, want) {
		t.Errorf("actions = %v, want %v", res.Actions, want)
	}
}

func TestAPriorActionOfAnotherStepIsAMismatch(t *testing.T) {
	in := webInput(mobileAction(), check.Result{State: domain.CheckTrue})
	last := in.Steps["read"]
	last.Prior = []domain.Action{{ID: "p1", Use: domain.UseCase{Pass: domain.PassScenario, Scenario: "board", Step: "other"}}}
	in.Steps["read"] = last
	res := verdict.Compute(in)
	if res.Outcome != domain.OutcomeInconclusive || !strings.Contains(reasons(res), `was recorded for step "other"`) {
		t.Errorf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

func TestAPriorActionUsedTwiceOrWithNoIDIsAMismatch(t *testing.T) {
	in := webInput(mobileAction(), check.Result{State: domain.CheckTrue})
	last := in.Steps["read"]
	ok := domain.UseCase{Pass: domain.PassScenario, Scenario: "board", Step: "read"}
	last.Prior = []domain.Action{{ID: "a1", Use: ok}, {Use: ok}}
	in.Steps["read"] = last
	res := verdict.Compute(in)
	text := reasons(res)
	if res.Outcome != domain.OutcomeInconclusive || !strings.Contains(text, "was already used by an earlier step") || !strings.Contains(text, "an action without an ID") {
		t.Errorf("outcome = %s: %s", res.Outcome, text)
	}
}
