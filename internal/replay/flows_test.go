package replay_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/replay"
)

type fakeFlows struct {
	flows   map[string]domain.Flow
	digests map[string]string
}

func (f fakeFlows) Flow(id string) (domain.Flow, string, error) {
	fl, ok := f.flows[id]
	if !ok {
		return domain.Flow{}, "", fmt.Errorf("%s: %w", flow.Path(id), flow.ErrNotFound)
	}
	return fl, f.digests[id], nil
}

func loginFlow() domain.Flow {
	return domain.Flow{
		APIVersion: "semar/v1", Kind: "Flow", ID: "login", Surface: domain.SurfaceAndroid, Service: "shop-app", App: "shop-app",
		Params: []domain.FlowParam{{Name: "email"}},
		Steps: []domain.ScriptStep{
			{Step: "start", Mobile: &domain.MobileAction{Do: domain.MobileLaunch}},
			{Step: "email", Mobile: &domain.MobileAction{Do: domain.MobileType, Screen: "SignIn", Element: "email", Value: "{{email}}"}},
			{Step: "submit", Mobile: &domain.MobileAction{Do: domain.MobileTap, Screen: "SignIn", Element: "submit"},
				Captures: []domain.Capture{{Name: "shown", Element: "welcome"}}},
		},
	}
}

func flowSource() fakeFlows {
	return fakeFlows{flows: map[string]domain.Flow{"login": loginFlow()}, digests: map[string]string{"login": "sha256:abc"}}
}

// useScenario signs in with the flow and then checks the welcome text, which is the first step that has
// an expected result.
func useScenario() (domain.Scenario, domain.Script) {
	sc := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "AND-2", Title: "Sign in with a flow", Ticket: "SHOP-1", Intent: "A customer signs in",
		Surface: domain.SurfaceAndroid, AuthProfile: "alice",
		Steps: []domain.Step{
			{ID: "login", Action: "Sign in"},
			{ID: "check", Action: "Read the welcome text", Expect: []domain.Expectation{
				{Check: domain.Check{Element: "welcome", Equals: "Hi Ana"}, Source: domain.SourcePerson},
			}},
		},
	}
	s := domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc),
		Steps: []domain.ScriptStep{
			{Step: "login", Use: "login", With: map[string]string{"email": "ana@example.com"}},
			{Step: "check", Mobile: &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileWait, Screen: "SignIn", Element: "welcome"}},
		},
	}
	return sc, s
}

func (r *rig) withFlows(fm *fakeMobile, fl replay.FlowSource) {
	r.deps.Mobile = fm
	r.deps.MobileServices = []string{"shop-app"}
	r.deps.Flows = fl
}

func TestAFlowExpandsIntoTheStepsOfTheScenario(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := useScenario()
	fm := &fakeMobile{}
	r.withFlows(fm, flowSource())
	r.deps.Screens = &fakeScreens{text: signInScreens}
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s})
	res := compute(sc, got, r.builds(t))
	if res.Outcome != domain.OutcomePassed {
		t.Fatalf("outcome = %s: %+v", res.Outcome, res.Reasons)
	}
	if len(fm.calls) != 4 || fm.scenarios != 1 {
		t.Fatalf("engine got %d calls and %d new scenarios, want 4 and 1", len(fm.calls), fm.scenarios)
	}
	for i, want := range []string{"start", "email", "submit"} {
		c := fm.calls[i]
		if c.Use.Step != "login" || c.Use.Pass != domain.PassScenario {
			t.Errorf("call %d use = %+v, want step login in the scenario pass", i, c.Use)
		}
		if !strings.Contains(c.Why, "flow login, step "+want) {
			t.Errorf("call %d why = %q, want the flow and step %s", i, c.Why, want)
		}
	}
	if typed := fm.calls[1].Action; typed.Value != "ana@example.com" || typed.Service != "shop-app" || typed.App != "shop-app" {
		t.Errorf("typed action = %+v: the with value and the flow's service and app must be filled in", typed)
	}
	if fm.calls[1].Locators["email"].ID != "email" {
		t.Errorf("locators = %+v", fm.calls[1].Locators)
	}
	if len(res.Actions) != 4 {
		t.Errorf("the result lists %d actions, want the 3 of the flow and 1 of the check", len(res.Actions))
	}
	if !slices.Equal(got.Flows, []domain.FlowRef{{ID: "login", Digest: "sha256:abc"}}) {
		t.Errorf("flows = %+v", got.Flows)
	}
	if got.Steps["login"].Action == nil || len(got.Steps["login"].Prior) != 2 {
		t.Errorf("evidence of login = %+v, want the last action and 2 prior", got.Steps["login"])
	}
	if got.Captured["shown"] != "Hi Ana" {
		t.Errorf("captured = %v: what the flow captures must reach the scenario", got.Captured)
	}
}

func TestAFailingFlowStepStopsTheScenario(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := useScenario()
	fm := &fakeMobile{}
	fm.reply = func(c mobile.Call, n int) (mobile.Result, error) {
		a := domain.Action{
			ID: domain.ActionID(fmt.Sprintf("a-%04d", n)), Surface: domain.SurfaceAndroid, Op: string(c.Action.Do), Use: c.Use, By: c.By, Why: c.Why,
			Mobile: &domain.MobileRecord{Op: string(c.Action.Do), App: c.Action.App, Screen: c.Action.Screen},
		}
		if c.Action.Element == "email" {
			a.Err = "element email was not found"
		}
		return mobile.Result{Action: a}, nil
	}
	r.withFlows(fm, flowSource())
	r.deps.Screens = &fakeScreens{text: signInScreens}
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s})
	res := compute(sc, got, r.builds(t))
	if res.Outcome == domain.OutcomePassed {
		t.Fatalf("outcome = %s, want a scenario that stopped", res.Outcome)
	}
	if len(fm.calls) != 2 {
		t.Fatalf("the engine got %d calls, want 2: the submit step and the check must not be sent after the email step failed", len(fm.calls))
	}
	ev := got.Steps["login"]
	if ev.Action == nil || ev.Action.Err == "" || !strings.Contains(ev.Action.Why, "step email") || len(ev.Prior) != 1 {
		t.Errorf("evidence = %+v, want the failing email action with the start action before it", ev)
	}
	if _, sent := got.Steps["check"]; sent {
		t.Error("the check step has evidence although it was never sent")
	}
	if !hasReason(res, "element email was not found") {
		t.Errorf("reasons = %+v, want the failure named", res.Reasons)
	}
}

func TestAScriptThatOnlyUsesAFlowLocksAndResets(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "AND-3", Title: "Only the flow", Ticket: "SHOP-1", Intent: "x",
		Surface: domain.SurfaceAndroid, AuthProfile: "alice",
		Steps: []domain.Step{{ID: "login", Action: "Sign in"}},
	}
	s := domain.Script{APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc),
		Steps: []domain.ScriptStep{{Step: "login", Use: "login", With: map[string]string{"email": "a@b.c"}}}}
	fm := &fakeMobile{}
	r.withFlows(fm, flowSource())
	r.deps.Screens = &fakeScreens{text: signInScreens}
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s})
	if fm.scenarios != 1 || len(fm.calls) != 3 || len(got.Blocked) > 0 {
		t.Errorf("new scenarios = %d, calls = %d, blocked = %v: a script of one use step needs the reset and the device", fm.scenarios, len(fm.calls), got.Blocked)
	}
}

func TestAScreenThatAFlowNamesIsCheckedBeforeAnythingIsSent(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := useScenario()
	fl := loginFlow()
	fl.Steps[1].Mobile.Element = "gone"
	fm := &fakeMobile{}
	r.withFlows(fm, fakeFlows{flows: map[string]domain.Flow{"login": fl}, digests: map[string]string{"login": "sha256:abc"}})
	r.deps.Screens = &fakeScreens{text: signInScreens}
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s})
	res := compute(sc, got, r.builds(t))
	if res.Outcome != domain.OutcomeBlocked || !hasReason(res, "element gone is not on screen SignIn of shop-app") || !hasReason(res, "flow login, step email") || len(fm.calls) != 0 {
		t.Errorf("outcome = %s, reasons = %+v, calls = %d", res.Outcome, res.Reasons, len(fm.calls))
	}
}

func TestAFlowThatIsMissingBlocksTheScenario(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := useScenario()
	fm := &fakeMobile{}
	r.withFlows(fm, fakeFlows{})
	r.deps.Screens = &fakeScreens{text: signInScreens}
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s})
	res := compute(sc, got, r.builds(t))
	if res.Outcome != domain.OutcomeBlocked || !hasReason(res, "flow login") || len(fm.calls) != 0 {
		t.Errorf("outcome = %s, reasons = %+v, calls = %d", res.Outcome, res.Reasons, len(fm.calls))
	}
}

func TestARunWithNoFlowsBlocksAScriptThatUsesOne(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := useScenario()
	fm := &fakeMobile{}
	r.withFlows(fm, nil)
	r.deps.Screens = &fakeScreens{text: signInScreens}
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s})
	res := compute(sc, got, r.builds(t))
	if res.Outcome != domain.OutcomeBlocked || !hasReason(res, "this run has no flows") || len(fm.calls) != 0 {
		t.Errorf("outcome = %s, reasons = %+v, calls = %d", res.Outcome, res.Reasons, len(fm.calls))
	}
}

func TestTheRunRecordsTheFlowThatRan(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := useScenario()
	r.putScript(t, s)
	fm := &fakeMobile{}
	r.withFlows(fm, flowSource())
	r.deps.Screens = &fakeScreens{text: signInScreens}
	results, _, _ := r.run(t, sc)
	if got := results[0].Flows; !slices.Equal(got, []domain.FlowRef{{ID: "login", Digest: "sha256:abc"}}) {
		t.Errorf("flows = %+v, want the id and the digest of login", got)
	}
}
