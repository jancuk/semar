package verdict_test

import (
	"testing"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/verdict"
)

func mobileAction() domain.Action {
	return domain.Action{Surface: domain.SurfaceAndroid, Op: "tap", Mobile: &domain.MobileRecord{Op: "tap"}}
}

func TestMobileStepPasses(t *testing.T) {
	res := verdict.Compute(webInput(mobileAction(), check.Result{State: domain.CheckTrue}))
	if res.Outcome != domain.OutcomePassed {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

func TestMobileStepFailsOnAFalseCheck(t *testing.T) {
	res := verdict.Compute(webInput(mobileAction(), check.Result{State: domain.CheckFalse, Expected: "element card equals Buy milk", Observed: "Buy eggs"}))
	if res.Outcome != domain.OutcomeFailed {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

// A locator that finds nothing is never a failure (TRD rule 5), on a device as in a browser.
func TestMobileStepWithAnElementNotFoundIsInconclusive(t *testing.T) {
	a := mobileAction()
	a.Err = check.NotFound + " (card matched 0)"
	res := verdict.Compute(webInput(a, check.Result{State: domain.CheckNotEvaluated, Detail: check.NotFound}))
	if res.Outcome != domain.OutcomeInconclusive {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

func TestRefusedMobileWriteIsBlocked(t *testing.T) {
	a := mobileAction()
	a.Refused = "read_only: tap writes, and the environment is read only"
	res := verdict.Compute(webInput(a, check.Result{State: domain.CheckNotEvaluated}))
	if res.Outcome != domain.OutcomeBlocked {
		t.Fatalf("outcome = %s: %s", res.Outcome, reasons(res))
	}
}

func TestMobileStepNeedsNoResponseNorInvariant(t *testing.T) {
	res := verdict.Compute(webInput(mobileAction(), check.Result{State: domain.CheckTrue}))
	for _, r := range res.Reasons {
		t.Errorf("a passed mobile step has the reason: %s", r.Text)
	}
}
