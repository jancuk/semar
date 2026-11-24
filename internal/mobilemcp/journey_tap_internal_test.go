package mobilemcp

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/mobilerec"
)

func mob(do domain.MobileOp, screen, element string) *domain.MobileAction {
	return &domain.MobileAction{Do: do, Service: "shop-app", App: "shop-app", Screen: screen, Element: element}
}

func modelCheck(element, equals string) domain.Expectation {
	return domain.Expectation{Check: domain.Check{Element: element, Equals: equals}, Source: domain.SourceBaseline, By: "model"}
}

// legDraft is what the session builds for a leg recorded as a scenario: a launch, a tap that hosts a check,
// and a wait that a check made.
func legDraft() mobilerec.Draft {
	launch := mob(domain.MobileLaunch, "", "")
	launch.Reset = "restart"
	return mobilerec.Draft{
		Kind: mobilerec.KindScenario, ID: "checkout-app", App: "shop-app",
		Scenario: domain.Scenario{Steps: []domain.Step{
			{ID: "launch", Action: "Start the app", Setup: true},
			{ID: "tap-submit", Action: "Tap submit", Expect: []domain.Expectation{modelCheck("greeting", "Hi Ana")}},
			{ID: "wait-total", Action: "Wait for total", Expect: []domain.Expectation{modelCheck("total", "12.00")}},
		}},
		Script: domain.Script{Steps: []domain.ScriptStep{
			{Step: "launch", Mobile: launch},
			{Step: "tap-submit", Mobile: mob(domain.MobileTap, "SignIn", "submit"), Captures: []domain.Capture{{Name: "code", Element: "code"}}},
			{Step: "wait-total", Mobile: mob(domain.MobileWait, "Home", "total")},
		}},
		Notes: []string{"a note"},
	}
}

func TestLegFlowOfSplitsTheFlowFromItsChecks(t *testing.T) {
	lf, err := legFlowOf(legDraft())
	if err != nil {
		t.Fatal(err)
	}
	if lf.Flow.ID != "checkout-app" || lf.Flow.Service != "shop-app" || lf.Flow.App != "shop-app" || len(lf.Flow.Steps) != 3 {
		t.Fatalf("flow = %+v", lf.Flow)
	}
	for _, st := range lf.Flow.Steps {
		if len(st.Expect) != 0 {
			t.Errorf("the flow holds a check on %s", st.Step)
		}
		if st.Mobile != nil && (st.Mobile.Service != "" || st.Mobile.App != "") {
			t.Errorf("step %s repeats the service of the flow: %+v", st.Step, st.Mobile)
		}
	}
	// A flow that holds no check of a model is a flow that the repository accepts.
	if _, err := flow.Marshal(lf.Flow); err != nil {
		t.Errorf("the flow is not valid: %v", err)
	}
	if len(lf.Checks) != 2 {
		t.Fatalf("checks = %+v", lf.Checks)
	}
	if c := lf.Checks[0]; c.ID != "check-greeting" || c.Screen != "SignIn" || c.Element != "greeting" || len(c.Expect) != 1 || c.Expect[0].By != "model" {
		t.Errorf("first check = %+v", c)
	}
	if c := lf.Checks[1]; c.Screen != "Home" || c.Element != "total" {
		t.Errorf("second check = %+v", c)
	}
	if !slices.Equal(lf.Captures, []string{"code"}) || lf.App != "shop-app" || len(lf.Notes) != 1 {
		t.Errorf("captures %v, app %q, notes %v", lf.Captures, lf.App, lf.Notes)
	}
}

func TestLegFlowOfRefusesAnActionAfterACheck(t *testing.T) {
	d := legDraft()
	d.Scenario.Steps = append(d.Scenario.Steps, domain.Step{ID: "tap-more", Action: "Tap more"})
	d.Script.Steps = append(d.Script.Steps, domain.ScriptStep{Step: "tap-more", Mobile: mob(domain.MobileTap, "Home", "more")})
	if _, err := legFlowOf(d); err == nil || !strings.Contains(err.Error(), "record the checks last") || !strings.Contains(err.Error(), "tap-more") {
		t.Fatalf("err = %v", err)
	}
	// A wait after a check is no action, and a flow that is run is one.
	d = legDraft()
	d.Scenario.Steps = append(d.Scenario.Steps, domain.Step{ID: "use-x"})
	d.Script.Steps = append(d.Script.Steps, domain.ScriptStep{Step: "use-x", Use: "x"})
	if _, err := legFlowOf(d); err == nil {
		t.Fatal("a use step after a check was accepted")
	}
}

func TestLegFlowOfNeedsACheck(t *testing.T) {
	d := legDraft()
	for i := range d.Scenario.Steps {
		d.Scenario.Steps[i].Expect = nil
	}
	if _, err := legFlowOf(d); !errors.Is(err, mobilerec.ErrNoCheck) {
		t.Fatalf("err = %v", err)
	}
}

func TestLegFlowOfNumbersTwoChecksOfTheSameElement(t *testing.T) {
	d := legDraft()
	d.Scenario.Steps[1].Expect = append(d.Scenario.Steps[1].Expect, modelCheck("greeting", "Hi Ana"))
	lf, err := legFlowOf(d)
	if err != nil {
		t.Fatal(err)
	}
	if lf.Checks[0].ID != "check-greeting" || lf.Checks[1].ID != "check-greeting-2" {
		t.Errorf("ids = %s, %s", lf.Checks[0].ID, lf.Checks[1].ID)
	}
}
