package replay_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/replay"
)

// pauses records the calls of Deps.Pause, and with the device the order of what happened.
type pauses struct {
	mu     sync.Mutex
	events []string
	got    []replay.Pause
}

func (p *pauses) add(e string) { p.mu.Lock(); p.events = append(p.events, e); p.mu.Unlock() }

func (p *pauses) pause(_ context.Context, ps replay.Pause) {
	p.mu.Lock()
	p.got = append(p.got, ps)
	p.events = append(p.events, "pause "+string(ps.Scenario)+" "+string(ps.Step))
	p.mu.Unlock()
}

// orderedMobile is a fakeMobile that logs each new scenario and each action in the same list as Pause.
type orderedMobile struct {
	*fakeMobile
	p *pauses
}

func (o orderedMobile) NewScenario() { o.p.add("new"); o.fakeMobile.NewScenario() }
func (o orderedMobile) Do(ctx context.Context, c mobile.Call) (mobile.Result, error) {
	o.p.add("do " + string(c.Use.Step))
	return o.fakeMobile.Do(ctx, c)
}

// tapFails makes the tap of every scenario fail with a miss and the closest elements.
func tapFails(c mobile.Call, n int) (mobile.Result, error) {
	a := domain.Action{ID: domain.ActionID(fmt.Sprintf("a-%d", n)), Surface: domain.SurfaceAndroid, Op: string(c.Action.Do), Use: c.Use,
		Mobile: &domain.MobileRecord{Op: string(c.Action.Do), App: "shop-app", Screen: c.Action.Screen, Screenshot: "screenshots/x.jpg"}}
	if c.Action.Do == domain.MobileTap {
		a.Err = "an element was not found: either the product changed or the script is out of date (element submit matched 0)"
		a.Mobile.Nearby = []domain.Candidate{{Element: "submit", Why: `button "Sign in"`}}
	}
	return mobile.Result{Action: a}, nil
}

func TestPauseIsCalledAtTheFailingStepBeforeTheNextScenarioStarts(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.deps.Workers = 1
	ps := &pauses{}
	r.deps.Pause = ps.pause
	r.deps.Mobile = orderedMobile{&fakeMobile{reply: tapFails}, ps}
	r.deps.Screens = &fakeScreens{text: signInScreens}
	sc := androidScenario()
	s := androidScript(sc)
	var scs []domain.Scenario
	for i := 1; i <= 2; i++ {
		c, ss := copyWeb(sc, s, domain.ScenarioID(fmt.Sprintf("AND-%d", i)))
		r.putScript(t, ss)
		scs = append(scs, c)
	}
	results, _, _ := r.run(t, scs...)
	want := "new,do start,do sign-in,pause AND-1 sign-in,new,do start,do sign-in,pause AND-2 sign-in"
	if got := strings.Join(ps.events, ","); got != want {
		t.Errorf("events:\n%s\nwant:\n%s", got, want)
	}
	p := ps.got[0]
	if p.Scenario != "AND-1" || p.Step != "sign-in" || p.Flow != "" || p.Screen != "SignIn" || p.Screenshot != "screenshots/x.jpg" ||
		!strings.Contains(p.Reason, "the step did not finish: an element was not found") || len(p.Nearby) != 1 || p.Nearby[0].Why != `button "Sign in"` {
		t.Errorf("pause = %+v", p)
	}
	for _, res := range results {
		if res.Outcome != domain.OutcomeInconclusive {
			t.Errorf("%s ended %s: a pause must not change the outcome", res.Scenario, res.Outcome)
		}
	}
}

func TestPauseIsCalledForAFalseCheck(t *testing.T) {
	r := newRig(t, rigOptions{})
	ps := &pauses{}
	r.deps.Pause = ps.pause
	r.deps.Mobile = &fakeMobile{reply: func(c mobile.Call, n int) (mobile.Result, error) {
		a := domain.Action{ID: domain.ActionID(fmt.Sprintf("a-%d", n)), Surface: domain.SurfaceAndroid, Op: string(c.Action.Do), Use: c.Use,
			Mobile: &domain.MobileRecord{Op: string(c.Action.Do), Screen: c.Action.Screen, Elements: map[string]domain.ElementState{}}}
		for _, w := range c.Watch {
			a.Mobile.Elements[w.Element] = domain.ElementState{Found: true, Count: 1, Visible: true, Text: "Hi Bob"}
		}
		return mobile.Result{Action: a}, nil
	}}
	r.deps.Screens = &fakeScreens{text: signInScreens}
	sc := androidScenario()
	r.runOne(t, replay.Single{Scenario: sc, Script: androidScript(sc)})
	if len(ps.got) != 1 || ps.got[0].Step != "sign-in" || !strings.Contains(ps.got[0].Reason, "a check is false") {
		t.Fatalf("pauses = %+v", ps.got)
	}
}

func TestPauseIsCalledForAFalseFlowCheckAndNamesTheFlow(t *testing.T) {
	r := newRig(t, rigOptions{})
	ps := &pauses{}
	r.deps.Pause = ps.pause
	sc, s := useScenario()
	r.withFlows(&fakeMobile{}, sourceOf(withCheck(loginFlow(), "submit", "Hi Bob")))
	r.deps.Screens = &fakeScreens{text: signInScreens}
	r.runOne(t, replay.Single{Scenario: sc, Script: s})
	if len(ps.got) != 1 {
		t.Fatalf("pauses = %+v", ps.got)
	}
	p := ps.got[0]
	if p.Step != "login" || p.Flow != "login" || !strings.Contains(p.Reason, "a check of flow login step submit is false") {
		t.Errorf("pause = %+v", p)
	}
}

func TestPauseIsCalledForAFailingStepOfAFlow(t *testing.T) {
	r := newRig(t, rigOptions{})
	ps := &pauses{}
	r.deps.Pause = ps.pause
	sc, s := useScenario()
	r.withFlows(&fakeMobile{reply: func(c mobile.Call, n int) (mobile.Result, error) {
		a := domain.Action{ID: domain.ActionID(fmt.Sprintf("a-%d", n)), Surface: domain.SurfaceAndroid, Op: string(c.Action.Do), Use: c.Use,
			Mobile: &domain.MobileRecord{Op: string(c.Action.Do), Screen: c.Action.Screen}}
		if c.Action.Element == "email" {
			a.Err = "an element was not found"
		}
		return mobile.Result{Action: a}, nil
	}}, flowSource())
	r.deps.Screens = &fakeScreens{text: signInScreens}
	r.runOne(t, replay.Single{Scenario: sc, Script: s})
	if len(ps.got) != 1 || ps.got[0].Flow != "login" || ps.got[0].Step != "login" {
		t.Fatalf("pauses = %+v", ps.got)
	}
}

func TestNoPauseForAPassingStepARefusalOrAnEngineWithNoPause(t *testing.T) {
	r := newRig(t, rigOptions{})
	ps := &pauses{}
	r.deps.Pause = ps.pause
	r.deps.Mobile, r.deps.Screens = &fakeMobile{}, &fakeScreens{text: signInScreens}
	sc := androidScenario()
	r.runOne(t, replay.Single{Scenario: sc, Script: androidScript(sc)})
	if len(ps.got) != 0 {
		t.Errorf("a passing scenario paused: %+v", ps.got)
	}

	refused := &fakeMobile{reply: func(c mobile.Call, n int) (mobile.Result, error) {
		a := domain.Action{ID: domain.ActionID(fmt.Sprintf("r-%d", n)), Surface: domain.SurfaceAndroid, Use: c.Use, Refused: "the environment is production",
			Mobile: &domain.MobileRecord{Op: string(c.Action.Do)}}
		return mobile.Result{Action: a}, nil
	}}
	r.deps.Mobile = refused
	r.runOne(t, replay.Single{Scenario: sc, Script: androidScript(sc)})
	if len(ps.got) != 0 {
		t.Errorf("a refused action paused: %+v", ps.got)
	}

	r.deps.Pause = nil
	r.deps.Mobile = &fakeMobile{reply: tapFails}
	r.runOne(t, replay.Single{Scenario: sc, Script: androidScript(sc)})
}
