package replay_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/replay"
	"github.com/jancuk/semar/internal/screens"
)

const boardScreens = `apiVersion: semar/v1
kind: Screens
app: kanban
screens:
  Board:
    card: { role: heading, name: "{{leg.title}}" }
    done: { testId: done }
`

// fakeWeb is a browser engine that answers from a table, so that the runner is tested without Chrome.
type fakeWeb struct {
	mu    sync.Mutex
	calls []web.Call
	reply func(c web.Call, n int) (web.Result, error)
}

func (f *fakeWeb) NewScenario() {}

func (f *fakeWeb) Do(_ context.Context, c web.Call) (web.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	n := len(f.calls)
	f.mu.Unlock()
	if f.reply != nil {
		return f.reply(c, n)
	}
	a := domain.Action{
		ID: domain.ActionID(fmt.Sprintf("a-%04d", n)), Surface: domain.SurfaceWeb, Op: string(c.Action.Do), Use: c.Use, By: c.By,
		Web: &domain.WebRecord{Op: string(c.Action.Do), Elements: map[string]domain.ElementState{}},
	}
	for _, w := range c.Watch {
		a.Web.Elements[w.Element] = domain.ElementState{Found: true, Count: 1, Visible: true, Text: "Buy milk"}
	}
	return web.Result{Action: a, Captured: map[string]string{"shown": "Buy milk"}}, nil
}

type fakeScreens struct {
	text    string
	missing bool
	asked   []string
}

func (f *fakeScreens) Load(app string) (domain.Screens, error) {
	f.asked = append(f.asked, app)
	if f.missing {
		return domain.Screens{}, fmt.Errorf("%s: %w", screens.Path(app), screens.ErrNotFound)
	}
	return screens.Parse([]byte(f.text), screens.Path(app), app)
}

func webScenario() domain.Scenario {
	return domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "board-web", Title: "Board", Ticket: "T-1", Intent: "The board shows the card",
		Surface: domain.SurfaceWeb, AuthProfile: "alice",
		Steps: []domain.Step{
			{ID: "open", Action: "Open the board"},
			{ID: "read", Action: "Read the card", Expect: []domain.Expectation{
				{Check: domain.Check{Element: "card", Equals: "{{leg.title}}"}, Source: domain.SourcePerson},
			}},
		},
	}
}

func webScript(sc domain.Scenario) domain.Script {
	return domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc),
		Steps: []domain.ScriptStep{
			{Step: "open", Web: &domain.WebAction{Service: "orders", App: "kanban", Do: domain.WebOpen, Path: "/boards/{{leg.board}}"}},
			{Step: "read", Web: &domain.WebAction{Service: "orders", App: "kanban", Do: domain.WebWait, Screen: "Board", Element: "card"},
				Captures: []domain.Capture{{Name: "shown", Element: "card"}}},
		},
	}
}

// runWeb runs the web scenario through the runner with the fake engine. Its variables come from a
// preset, as the outputs of an earlier leg do in a journey.
func (r *rig) runWeb(t *testing.T, sc domain.Scenario, s domain.Script, fw *fakeWeb, fs *fakeScreens) domain.ScenarioResult {
	t.Helper()
	r.deps.Web = fw
	r.deps.Screens = fs
	got := r.runOne(t, replay.Single{Scenario: sc, Script: s, Vars: map[string]string{"leg.board": "b-1", "leg.title": "Buy milk"}})
	return compute(sc, got, r.builds(t))
}

func TestWebScenarioRunsThroughTheRunner(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := webScenario()
	sc.Steps[0].Expect = nil
	s := webScript(sc)
	fw, fs := &fakeWeb{}, &fakeScreens{text: boardScreens}
	res := r.runWeb(t, sc, s, fw, fs)
	if res.Outcome != domain.OutcomePassed {
		t.Fatalf("outcome = %s: %+v", res.Outcome, res.Reasons)
	}
	if len(fw.calls) != 2 {
		t.Fatalf("the engine got %d calls, want 2", len(fw.calls))
	}
	open, read := fw.calls[0], fw.calls[1]
	if open.Action.Path != "/boards/b-1" || open.AuthProfile != "alice" || open.Use.Step != "open" || open.By != domain.ActorReplay {
		t.Errorf("open = %+v", open)
	}
	if read.Locators["card"].Name != "Buy milk" {
		t.Errorf("the locator was not rendered: %+v", read.Locators["card"])
	}
	if len(read.Watch) != 1 || read.Watch[0].Element != "card" || read.Watch[0].Presence || len(read.Watch[0].Attrs) != 0 {
		t.Errorf("watch = %+v", read.Watch)
	}
	if read.Why != "Read the card" {
		t.Errorf("why = %q", read.Why)
	}
}

func TestWebStepsUseTheScreensFileAsItIs(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := webScenario()
	sc.Steps[0].Expect = nil
	s := webScript(sc)
	fw, fs := &fakeWeb{}, &fakeScreens{text: boardScreens}
	res := r.runWeb(t, sc, s, fw, fs)
	if res.Outcome != domain.OutcomePassed {
		t.Fatalf("outcome = %s: %+v", res.Outcome, res.Reasons)
	}
	if len(fw.calls) != 2 {
		t.Errorf("the browser got %d calls, want 2", len(fw.calls))
	}
}

func TestWebStepsNeedTheirScreensFile(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := webScenario(), webScript(webScenario())
	res := r.runWeb(t, sc, s, &fakeWeb{}, &fakeScreens{missing: true})
	if res.Outcome != domain.OutcomeBlocked || !hasReason(res, "the screens of kanban were not found: .semar/screens/kanban.yaml") {
		t.Errorf("outcome = %s, reasons = %+v", res.Outcome, res.Reasons)
	}
}

func TestWebStepNamesAnElementThatTheFileLacks(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := webScenario(), webScript(webScenario())
	s.Steps[1].Web.Element = "gone"
	s.Steps[1].Captures = nil
	sc.Steps[1].Expect[0].Check.Element = "gone"
	s.ExpectHash = domain.ExpectHash(sc)
	fw := &fakeWeb{}
	res := r.runWeb(t, sc, s, fw, &fakeScreens{text: boardScreens})
	if res.Outcome != domain.OutcomeBlocked {
		t.Fatalf("outcome = %s: %+v", res.Outcome, res.Reasons)
	}
	if !hasReason(res, "element gone is not on screen Board of kanban; its elements are card, done") {
		t.Errorf("reasons = %+v", res.Reasons)
	}
	if len(fw.calls) != 0 {
		t.Error("a step that does not fit its screens was sent")
	}
}

func TestWebScenarioWithNoBrowserIsBlocked(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := webScenario(), webScript(webScenario())
	rep := r.runOne(t, replay.Single{Scenario: sc, Script: s, Vars: map[string]string{"leg.board": "b", "leg.title": "t"}})
	if res := compute(sc, rep, r.builds(t)); res.Outcome != domain.OutcomeBlocked || !hasReason(res, "this run has no browser") {
		t.Errorf("outcome = %s, reasons = %+v", res.Outcome, res.Reasons)
	}
}

func TestAnElementThatIsNotFoundStopsTheScenarioInconclusive(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := webScenario()
	sc.Steps[0].Expect = nil
	sc.Steps[1].Expect[0].Check.Equals = "x"
	s := webScript(sc)
	s.ExpectHash = domain.ExpectHash(sc)
	s.Steps[0].Web.Path = "/"
	fw := &fakeWeb{reply: func(c web.Call, n int) (web.Result, error) {
		a := domain.Action{ID: domain.ActionID(fmt.Sprintf("a-%d", n)), Surface: domain.SurfaceWeb, Op: string(c.Action.Do), Use: c.Use,
			Web: &domain.WebRecord{Op: string(c.Action.Do), Elements: map[string]domain.ElementState{}}}
		if c.Action.Do == domain.WebWait {
			a.Err = "an element was not found: either the product changed or the script is out of date (element card matched 0)"
			a.Web.Elements["card"] = domain.ElementState{}
		}
		return web.Result{Action: a}, nil
	}}
	fs := &fakeScreens{text: boardScreens}
	res := r.runWeb(t, sc, s, fw, fs)
	if res.Outcome != domain.OutcomeInconclusive {
		t.Fatalf("outcome = %s: %+v", res.Outcome, res.Reasons)
	}
	if !hasReason(res, "an element was not found: either the product changed or the script is out of date") {
		t.Errorf("reasons = %+v", res.Reasons)
	}
}

func TestNoChromeBlocksTheScenario(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := webScenario()
	sc.Steps[1].Expect = nil
	sc.Steps[1].Expect = []domain.Expectation{{Check: domain.Check{Element: "card", Exists: ptrBool(true)}, Source: domain.SourcePerson}}
	s := webScript(sc)
	s.ExpectHash = domain.ExpectHash(sc)
	s.Steps[0].Web.Path = "/"
	s.Steps[1].Captures = nil
	fw := &fakeWeb{reply: func(c web.Call, n int) (web.Result, error) {
		return web.Result{}, fmt.Errorf("web open: %w", web.ErrChromeNotFound)
	}}
	res := r.runWeb(t, sc, s, fw, &fakeScreens{text: boardScreens})
	if res.Outcome != domain.OutcomeBlocked {
		t.Fatalf("outcome = %s: %+v", res.Outcome, res.Reasons)
	}
	if !hasReason(res, "Chrome was not found: install Chrome or set web.chrome in semar.yaml") {
		t.Errorf("reasons = %+v", res.Reasons)
	}
}

func TestALocatorWithAnUnknownVariableStopsTheStep(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := webScenario()
	sc.Steps[1].Expect[0].Check.Equals = "x"
	s := webScript(sc)
	s.Steps[0].Web.Path = "/"
	s.ExpectHash = domain.ExpectHash(sc)
	fw := &fakeWeb{}
	// The screens name {{leg.other}}, which no earlier step captured and no preset holds.
	res := r.runWeb(t, sc, s, fw, &fakeScreens{text: strings.Replace(boardScreens, "leg.title", "leg.other", 1)})
	if res.Outcome != domain.OutcomeInconclusive || !hasReason(res, "the locator of element card") {
		t.Errorf("outcome = %s: %+v", res.Outcome, res.Reasons)
	}
}

func TestOneWebScenarioAtATime(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := webScenario()
	sc.Steps[1].Expect = nil
	sc.Steps[1].Expect = []domain.Expectation{{Check: domain.Check{Element: "card", Exists: ptrBool(true)}, Source: domain.SourcePerson}}
	s := webScript(sc)
	s.ExpectHash = domain.ExpectHash(sc)
	s.Steps[0].Web.Path = "/"
	s.Steps[1].Captures = nil
	var mu sync.Mutex
	active, most := 0, 0
	fw := &fakeWeb{reply: func(c web.Call, n int) (web.Result, error) {
		mu.Lock()
		active++
		most = max(most, active)
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		return web.Result{Action: domain.Action{ID: domain.ActionID(fmt.Sprintf("a-%d", n)), Surface: domain.SurfaceWeb, Use: c.Use, Web: &domain.WebRecord{}}}, nil
	}}
	r.deps.Web, r.deps.Screens = fw, &fakeScreens{text: boardScreens}
	var scs []domain.Scenario
	for i := range 6 {
		c, ss := copyWeb(sc, s, domain.ScenarioID(fmt.Sprintf("board-%d", i)))
		r.putScript(t, ss)
		scs = append(scs, c)
	}
	r.run(t, scs...)
	if most > 1 {
		t.Errorf("%d web scenarios acted at once", most)
	}
}

func copyWeb(sc domain.Scenario, s domain.Script, id domain.ScenarioID) (domain.Scenario, domain.Script) {
	sc.ID, s.Scenario = id, id
	return sc, s
}

func ptrBool(b bool) *bool { return &b }

// A click whose element was not found did nothing, so the other checks of the step read a page that
// the step did not change. They must not fail the step (TRD rule 5).
func TestAMissedTargetDecidesNoOtherCheck(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := webScenario()
	sc.Steps[0].Expect = nil
	sc.Steps[1].Expect = []domain.Expectation{{Check: domain.Check{Element: "done", Equals: "ordered"}, Source: domain.SourcePerson}}
	s := webScript(sc)
	s.ExpectHash = domain.ExpectHash(sc)
	s.Steps[0].Web.Path = "/"
	s.Steps[1].Web = &domain.WebAction{Service: "orders", App: "kanban", Do: domain.WebClick, Screen: "Board", Element: "card"}
	s.Steps[1].Captures = nil
	fw := &fakeWeb{reply: func(c web.Call, n int) (web.Result, error) {
		a := domain.Action{ID: domain.ActionID(fmt.Sprintf("a-%d", n)), Surface: domain.SurfaceWeb, Op: string(c.Action.Do), Use: c.Use,
			Web: &domain.WebRecord{Op: string(c.Action.Do), Elements: map[string]domain.ElementState{}}}
		if c.Action.Do == domain.WebClick {
			a.Err = "an element was not found: either the product changed or the script is out of date (element card matched 0)"
			a.Web.Elements["done"] = domain.ElementState{Found: true, Count: 1, Visible: true, Text: "waiting"}
		}
		return web.Result{Action: a}, nil
	}}
	res := r.runWeb(t, sc, s, fw, &fakeScreens{text: strings.Replace(boardScreens, "card:", "done:", 0)})
	if res.Outcome != domain.OutcomeInconclusive {
		t.Fatalf("outcome = %s: %+v", res.Outcome, res.Reasons)
	}
	for _, c := range res.Checks {
		if c.State == domain.CheckFalse {
			t.Errorf("a check was decided: %+v", c)
		}
	}
}

func TestTheChecksOfAWebStepGoToTheEngineToWaitFor(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := webScenario()
	sc.Steps[1].Expect = []domain.Expectation{
		{Check: domain.Check{Element: "card", Equals: "{{leg.title}}"}, Source: domain.SourcePerson},
		{Check: domain.Check{Element: "card", Attribute: "href", Contains: "/boards"}, Source: domain.SourcePerson},
		{Check: domain.Check{Element: "done", State: "hidden"}, Source: domain.SourcePerson},
		{Check: domain.Check{Page: "title", Contains: "Board"}, Source: domain.SourcePerson},
	}
	s := webScript(sc)
	s.Steps[1].Captures = nil
	fw, fs := &fakeWeb{}, &fakeScreens{text: boardScreens}
	r.runWeb(t, sc, s, fw, fs)
	read := fw.calls[1]
	if len(read.Until) != 4 {
		t.Fatalf("until = %+v", read.Until)
	}
	if read.Until[0].Equals != domain.CapturedText("Buy milk") && read.Until[0].Equals != "Buy milk" {
		t.Errorf("the variable of the first check was not filled in: %#v", read.Until[0].Equals)
	}
	got := map[string]web.Watched{}
	for _, w := range read.Watch {
		got[w.Element] = w
	}
	if w := got["card"]; w.Presence || len(w.Attrs) != 1 || w.Attrs[0] != "href" {
		t.Errorf("card = %+v: a text and an attribute are read", w)
	}
	if w := got["done"]; !w.Presence {
		t.Errorf("done = %+v: a hidden element that is absent is an answer, and no reason for a repair", w)
	}
}
