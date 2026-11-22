package replay_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/replay"
)

const signInScreens = `apiVersion: semar/v1
kind: Screens
app: shop-app
screens:
  SignIn:
    email: { id: email }
    submit: { desc: Sign in }
    welcome: { id: welcome }
`

// fakeMobile is a device engine that answers from a table, so that the runner is tested with no device.
type fakeMobile struct {
	mu        sync.Mutex
	calls     []mobile.Call
	scenarios int
	reply     func(c mobile.Call, n int) (mobile.Result, error)
}

func (f *fakeMobile) NewScenario() { f.mu.Lock(); f.scenarios++; f.mu.Unlock() }

func (f *fakeMobile) Do(_ context.Context, c mobile.Call) (mobile.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	n := len(f.calls)
	f.mu.Unlock()
	if f.reply != nil {
		return f.reply(c, n)
	}
	a := domain.Action{
		ID: domain.ActionID(fmt.Sprintf("a-%04d", n)), Surface: domain.SurfaceAndroid, Op: string(c.Action.Do), Use: c.Use, By: c.By,
		Mobile: &domain.MobileRecord{Op: string(c.Action.Do), App: c.Action.App, Screen: c.Action.Screen, Elements: map[string]domain.ElementState{}},
	}
	for _, w := range c.Watch {
		a.Mobile.Elements[w.Element] = domain.ElementState{Found: true, Count: 1, Visible: true, Text: "Hi Ana"}
	}
	return mobile.Result{Action: a, Captured: map[string]string{"shown": "Hi Ana"}}, nil
}

func androidScenario() domain.Scenario {
	return domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "AND-1", Title: "Sign in", Ticket: "SHOP-1", Intent: "A customer signs in",
		Surface: domain.SurfaceAndroid, AuthProfile: "alice",
		Steps: []domain.Step{
			{ID: "start", Action: "Start the app"},
			{ID: "sign-in", Action: "Tap sign in", Expect: []domain.Expectation{
				{Check: domain.Check{Element: "welcome", Equals: "Hi Ana"}, Source: domain.SourcePerson},
			}},
		},
	}
}

func androidScript(sc domain.Scenario) domain.Script {
	return domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc),
		Steps: []domain.ScriptStep{
			{Step: "start", Mobile: &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileLaunch}},
			{Step: "sign-in", Mobile: &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileTap, Screen: "SignIn", Element: "submit"},
				Captures: []domain.Capture{{Name: "shown", Element: "welcome"}}},
		},
	}
}

func (r *rig) runAndroid(t *testing.T, sc domain.Scenario, s domain.Script, fm *fakeMobile, fs *fakeScreens) domain.ScenarioResult {
	t.Helper()
	if fm != nil {
		r.deps.Mobile = fm
	}
	r.deps.Screens = fs
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s})
	return compute(sc, got, r.builds(t))
}

func TestAndroidScenarioRunsThroughTheRunner(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := androidScenario()
	s := androidScript(sc)
	fm := &fakeMobile{}
	res := r.runAndroid(t, sc, s, fm, &fakeScreens{text: signInScreens})
	if res.Outcome != domain.OutcomePassed {
		t.Fatalf("outcome = %s: %+v", res.Outcome, res.Reasons)
	}
	if len(fm.calls) != 2 || fm.scenarios != 1 {
		t.Fatalf("the engine got %d calls and %d new scenarios, want 2 and 1", len(fm.calls), fm.scenarios)
	}
	tap := fm.calls[1]
	if tap.Locators["submit"].Desc != "Sign in" || tap.Use.Step != "sign-in" || tap.By != domain.ActorReplay || tap.Why != "Tap sign in" {
		t.Errorf("tap = %+v", tap)
	}
	if len(tap.Watch) != 1 || tap.Watch[0].Element != "welcome" || len(tap.Until) != 1 {
		t.Errorf("watch = %+v, until = %+v", tap.Watch, tap.Until)
	}
}

func TestAndroidStepsNeedTheirScreensFile(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := androidScenario()
	fm := &fakeMobile{}
	res := r.runAndroid(t, sc, androidScript(sc), fm, &fakeScreens{missing: true})
	if res.Outcome != domain.OutcomeBlocked || !hasReason(res, "the screens of shop-app were not found: .semar/screens/shop-app.yaml") {
		t.Errorf("outcome = %s, reasons = %+v", res.Outcome, res.Reasons)
	}
	if len(fm.calls) != 0 {
		t.Error("a step was sent although the screens file is missing")
	}
}

func TestAnAndroidElementThatTheFileLacksBlocksTheScenario(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := androidScenario(), androidScript(androidScenario())
	s.Steps[1].Mobile.Element = "gone"
	fm := &fakeMobile{}
	res := r.runAndroid(t, sc, s, fm, &fakeScreens{text: signInScreens})
	if res.Outcome != domain.OutcomeBlocked || !hasReason(res, "element gone is not on screen SignIn of shop-app") || len(fm.calls) != 0 {
		t.Errorf("outcome = %s, reasons = %+v, calls = %d", res.Outcome, res.Reasons, len(fm.calls))
	}
}

func TestAnAndroidElementWithOnlyBrowserFieldsBlocksTheScenario(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := androidScenario(), androidScript(androidScenario())
	text := "apiVersion: semar/v1\nkind: Screens\napp: shop-app\nscreens:\n  SignIn:\n    submit: { css: \"#go\" }\n    welcome: { id: welcome }\n"
	res := r.runAndroid(t, sc, s, &fakeMobile{}, &fakeScreens{text: text})
	if res.Outcome != domain.OutcomeBlocked || !hasReason(res, "cannot be found on a device") {
		t.Errorf("outcome = %s, reasons = %+v", res.Outcome, res.Reasons)
	}
}

func TestAndroidScenarioWithNoDeviceIsBlocked(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := androidScenario()
	res := r.runAndroid(t, sc, androidScript(sc), nil, &fakeScreens{text: signInScreens})
	if res.Outcome != domain.OutcomeBlocked || !hasReason(res, "this run has no device") {
		t.Errorf("outcome = %s, reasons = %+v", res.Outcome, res.Reasons)
	}
}

func TestAServiceWithNoAndroidAppBlocksTheScenario(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.deps.MobileServices = []string{"other"}
	sc := androidScenario()
	fm := &fakeMobile{}
	res := r.runAndroid(t, sc, androidScript(sc), fm, &fakeScreens{text: signInScreens})
	if res.Outcome != domain.OutcomeBlocked || !hasReason(res, "service shop-app has no android app in this environment") || len(fm.calls) != 0 {
		t.Errorf("outcome = %s, reasons = %+v", res.Outcome, res.Reasons)
	}
}

func TestAnUnavailableDeviceBlocksTheScenarioWithItsReason(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := androidScenario()
	fm := &fakeMobile{reply: func(mobile.Call, int) (mobile.Result, error) {
		return mobile.Result{}, fmt.Errorf("mobile launch: %w", &mobile.Unavailable{Reason: "adb was not found: install the Android platform tools"})
	}}
	res := r.runAndroid(t, sc, androidScript(sc), fm, &fakeScreens{text: signInScreens})
	if res.Outcome != domain.OutcomeBlocked || !hasReason(res, "adb was not found: install the Android platform tools") {
		t.Fatalf("outcome = %s, reasons = %+v", res.Outcome, res.Reasons)
	}
	if len(fm.calls) != 1 {
		t.Errorf("the engine got %d calls, want 1: the scenario stops at the first", len(fm.calls))
	}
}

func TestAnAndroidElementThatIsNotFoundEndsInconclusive(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := androidScenario()
	fm := &fakeMobile{reply: func(c mobile.Call, n int) (mobile.Result, error) {
		a := domain.Action{ID: domain.ActionID(fmt.Sprintf("a-%d", n)), Surface: domain.SurfaceAndroid, Op: string(c.Action.Do), Use: c.Use,
			Mobile: &domain.MobileRecord{Op: string(c.Action.Do), Elements: map[string]domain.ElementState{}}}
		if c.Action.Do == domain.MobileTap {
			a.Err = "an element was not found: either the product changed or the script is out of date (element submit matched 0)"
			a.Mobile.Elements["welcome"] = domain.ElementState{Found: true, Count: 1, Visible: true, Text: "other"}
		}
		return mobile.Result{Action: a}, nil
	}}
	res := r.runAndroid(t, sc, androidScript(sc), fm, &fakeScreens{text: signInScreens})
	if res.Outcome != domain.OutcomeInconclusive {
		t.Fatalf("outcome = %s: %+v", res.Outcome, res.Reasons)
	}
	for _, c := range res.Checks {
		if c.State == domain.CheckFalse {
			t.Errorf("a check was decided after a missed target: %+v", c)
		}
	}
}

func TestOneAndroidScenarioAtATime(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := androidScenario()
	s := androidScript(sc)
	var mu sync.Mutex
	active, most := 0, 0
	fm := &fakeMobile{reply: func(c mobile.Call, n int) (mobile.Result, error) {
		mu.Lock()
		active++
		most = max(most, active)
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		return mobile.Result{Action: domain.Action{ID: domain.ActionID(fmt.Sprintf("a-%d", n)), Surface: domain.SurfaceAndroid, Use: c.Use, Mobile: &domain.MobileRecord{}}}, nil
	}}
	r.deps.Mobile, r.deps.Screens = fm, &fakeScreens{text: signInScreens}
	var scs []domain.Scenario
	for i := range 6 {
		c, ss := copyWeb(sc, s, domain.ScenarioID(fmt.Sprintf("AND-%d", i)))
		r.putScript(t, ss)
		scs = append(scs, c)
	}
	r.run(t, scs...)
	if most > 1 {
		t.Errorf("%d scenarios acted on the device at once", most)
	}
}
