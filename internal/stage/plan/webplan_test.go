package plan

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/fake"
)

const (
	webTurn1 = `{"turn":{"kind":"act","actions":[{"kind":"fill","ref":"e1","text":"Board {{run.id}}"},{"kind":"click","ref":"e2"}]}}`
	webTurn2 = `{"turn":{"kind":"act","actions":[{"kind":"check","ref":"e3","expect":"Saved"}]}}`
	webTurn3 = `{"turn":{"kind":"done","title":"A board is saved","intent":"Before: nothing. Now: saved.","unproved":""}}`
	partTurn = `{"turn":{"kind":"done","title":"A board is saved","intent":"Before: nothing. Now: saved.","unproved":"A refused save shows the reason."}}`
	manualT  = `{"turn":{"kind":"manual","reason":"the page has no such board"}}`
)

func isWebPrompt(p string) bool {
	return strings.Contains(p, "You check one acceptance criterion of a web application")
}

// webPlanner answers the criteria of the shop ticket as shopAnswers does, and each web flow turn from
// its own list of outputs: one list for each model that it is asked to be.
type webPlanner struct {
	mu    sync.Mutex
	jobs  []provider.Job
	turns map[string][]string // by model: the outputs of its web turns, in order
	used  map[string]int
	crit  *answersByCriterion
	cost  float64
}

func (w *webPlanner) fake(id string) *fake.Fake {
	return fake.NewFunc(id, func(j provider.Job) (provider.Result, error) {
		w.mu.Lock()
		w.jobs = append(w.jobs, j)
		if isWebPrompt(j.Prompt) {
			if w.used == nil {
				w.used = map[string]int{}
			}
			outs := w.turns[j.Model]
			out := outs[w.used[j.Model]%len(outs)]
			w.used[j.Model]++
			w.mu.Unlock()
			return provider.Result{Output: json.RawMessage(out), Model: j.Model, Turns: 1, InputTokens: 50, OutputTokens: 5, CostUSD: w.cost}, nil
		}
		w.mu.Unlock()
		return w.crit.fake(id).Run(context.Background(), j)
	})
}

func (w *webPlanner) webJobs() []provider.Job {
	var out []provider.Job
	for _, j := range w.jobs {
		if isWebPrompt(j.Prompt) {
			out = append(out, j)
		}
	}
	return out
}

// fakeExplorer plays the flow on a scripted browser and reports the test that a real one would write.
type fakeExplorer struct {
	mu         sync.Mutex
	requests   []WebRequest
	running    int
	maxRunning int
	// shown is what the check reads.
	shown string
	// failFirst makes the first flow end with a browser failure.
	failFirst string
}

func (f *fakeExplorer) Explore(ctx context.Context, req WebRequest, drive FlowDriver) (WebOutcome, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.running++
	f.maxRunning = max(f.maxRunning, f.running)
	n := len(f.requests)
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.running--; f.mu.Unlock() }()
	if n == 1 && f.failFirst != "" {
		return WebOutcome{Manual: f.failFirst}, nil
	}
	end, err := drive(ctx, &scriptedBrowser{shown: map[string]string{"e3": f.shown}})
	if err != nil {
		return WebOutcome{}, err
	}
	if end.Manual != "" {
		return WebOutcome{Manual: end.Manual, End: end}, nil
	}
	sc := domain.Scenario{ID: req.ID, Ticket: req.Ticket, Title: end.Done.Title, Surface: domain.SurfaceWeb}
	return WebOutcome{End: end, Test: &WebTest{Scenario: sc, Replay: "passed", Cite: req.Cite, ScenarioPath: ".semar/scenarios/" + req.Ticket + "/" + string(req.ID) + ".feature"}}, nil
}

func webRun(t *testing.T, w *webPlanner, x WebExplorer, models ...string) (Output, error) {
	t.Helper()
	in := shopInput(t)
	for _, m := range models {
		in.Ladder = append(in.Ladder, Rung{Planner: w.fake("p"), Model: m})
	}
	in.JobDir = t.TempDir()
	in.Web = x
	return RunCriteria(context.Background(), in)
}

func newWebPlanner(turns ...string) *webPlanner {
	return &webPlanner{crit: shopAnswers(), turns: map[string][]string{"haiku": turns, "sonnet": turns}}
}

func TestAWebCriterionIsExploredAndItsTestReplacesTheManualCheck(t *testing.T) {
	w := newWebPlanner(webTurn1, webTurn2, webTurn3)
	x := &fakeExplorer{shown: "Saved"}
	out, err := webRun(t, w, x, "haiku")
	if err != nil {
		t.Fatal(err)
	}
	if len(x.requests) != 1 || x.requests[0].ID != "SHOP-482-01" || x.requests[0].Ticket != "SHOP-482" || !strings.HasPrefix(x.requests[0].Cite, ticket482+":11 ") {
		t.Fatalf("requests = %+v", x.requests)
	}
	if len(out.WebTests) != 1 || out.WebTests[0].Scenario.ID != "SHOP-482-01" || len(out.Draft.ManualChecks) != 0 {
		t.Fatalf("web tests %+v, manual checks %+v", out.WebTests, out.Draft.ManualChecks)
	}
	if len(out.Scenarios) != 2 || out.Scenarios[0].ID != "SHOP-482-02" || out.Scenarios[1].ID != "SHOP-482-03" {
		t.Errorf("the API scenarios must number after the web test: %v", out.Scenarios)
	}
	if len(out.Unaccounted) != 0 || len(out.Draft.WebTests) != 1 || out.Draft.WebTests[0].ID != "SHOP-482-01" {
		t.Errorf("unaccounted %v, draft web tests %+v", out.Unaccounted, out.Draft.WebTests)
	}
	if got := out.Draft.Tickets[0]; got.Kind != "behaviour" {
		t.Errorf("ticket = %+v", got)
	}
	st := out.Pipeline.Web
	if st.Explored != 1 || st.Written != 1 || st.Manual != 0 || st.Turns != 3 || st.Calls != 3 || st.Escalated != 0 {
		t.Errorf("web stats = %+v", st)
	}
	if len(out.Pipeline.WebUses) != 1 || out.Pipeline.WebUses[0].Calls != 3 || out.Pipeline.WebUses[0].Model != "haiku" {
		t.Errorf("web uses = %+v", out.Pipeline.WebUses)
	}
	if out.Pipeline.Calls != 6 || out.Result.InputTokens != 3*100+3*50 {
		t.Errorf("a turn is a job of the plan: calls %d, input tokens %d", out.Pipeline.Calls, out.Result.InputTokens)
	}
	for _, j := range w.webJobs() {
		if !j.NoTools || j.MaxTurns != CriterionMaxTurns || !strings.Contains(j.Prompt, "e3 text") {
			t.Errorf("web job = %+v", j)
		}
	}
	if o := out.Outcomes[2]; o.Web == nil || o.Web.Test == nil || o.Result.Manual != nil {
		t.Errorf("outcome of the web criterion = %+v", o)
	}
}

func TestAManualCheckOnAMobileScreenIsNotExplored(t *testing.T) {
	w := newWebPlanner(webTurn1)
	w.crit.byLine[":11"] = strings.Replace(manualAnswer, `"surface":"web"`, `"surface":"mobile"`, 1)
	x := &fakeExplorer{}
	out, err := webRun(t, w, x, "haiku")
	if err != nil {
		t.Fatal(err)
	}
	if len(x.requests) != 0 || len(out.Draft.ManualChecks) != 1 || len(out.WebTests) != 0 || out.Pipeline.Web.Explored != 0 {
		t.Errorf("requests %v, manual checks %d, web tests %d", x.requests, len(out.Draft.ManualChecks), len(out.WebTests))
	}
}

func TestWebCriteriaStayManualWhenThePlanDoesNotExplore(t *testing.T) {
	w := newWebPlanner(webTurn1)
	out, err := webRun(t, w, nil, "haiku")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Draft.ManualChecks) != 1 || len(out.WebTests) != 0 || len(w.webJobs()) != 0 {
		t.Errorf("manual checks %d, web tests %d, web jobs %d", len(out.Draft.ManualChecks), len(out.WebTests), len(w.webJobs()))
	}
}

func TestAFlowThatEndsAsManualIsTriedOnTheNextModelAndNowhereElse(t *testing.T) {
	w := newWebPlanner(webTurn1)
	w.turns["haiku"] = []string{manualT}
	w.turns["sonnet"] = []string{webTurn1, webTurn2, webTurn3}
	x := &fakeExplorer{shown: "Saved"}
	out, err := webRun(t, w, x, "haiku", "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(x.requests) != 2 || len(out.WebTests) != 1 {
		t.Fatalf("requests %d, web tests %d", len(x.requests), len(out.WebTests))
	}
	if x.requests[0].ID != x.requests[1].ID {
		t.Errorf("the second try must write the same test ID: %v", x.requests)
	}
	st := out.Pipeline.Web
	if st.Explored != 1 || st.Written != 1 || st.Escalated != 1 || st.Turns != 4 {
		t.Errorf("web stats = %+v", st)
	}
	if o := out.Outcomes[2]; o.Web.Rung != 1 {
		t.Errorf("the test came from rung %d, want 1", o.Web.Rung)
	}

	// A browser that could not be opened is not the fault of the model, so no other model is asked.
	w = newWebPlanner(webTurn1, webTurn2, webTurn3)
	x = &fakeExplorer{shown: "Saved", failFirst: "the browser could not be opened: no Chrome"}
	out, err = webRun(t, w, x, "haiku", "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(x.requests) != 1 || len(out.WebTests) != 0 || out.Pipeline.Web.Manual != 1 || len(out.Draft.ManualChecks) != 1 {
		t.Errorf("requests %d, web tests %d, stats %+v", len(x.requests), len(out.WebTests), out.Pipeline.Web)
	}
	if o := out.Outcomes[2]; o.Web == nil || !strings.Contains(o.Web.Manual, "no Chrome") {
		t.Errorf("the reason is lost: %+v", o.Web)
	}
}

func TestFlowsRunOneAtATime(t *testing.T) {
	w := newWebPlanner(webTurn1, webTurn2, webTurn3)
	w.crit.byLine[":10"] = manualAnswer
	x := &fakeExplorer{shown: "Saved"}
	out, err := webRun(t, w, x, "haiku")
	if err != nil {
		t.Fatal(err)
	}
	if len(x.requests) != 2 || x.maxRunning != 1 || x.requests[0].ID != "SHOP-482-01" || x.requests[1].ID != "SHOP-482-02" || len(out.WebTests) != 2 {
		t.Errorf("requests %+v, at most %d at once, web tests %d", x.requests, x.maxRunning, len(out.WebTests))
	}
}

func TestACheckThatFailsNowIsInTheDraftWithWhatThePageShows(t *testing.T) {
	// The model sends the check twice: the first time it learns what the page shows.
	w := newWebPlanner(webTurn1, webTurn2, webTurn2, webTurn3)
	x := &failingExplorer{fakeExplorer: &fakeExplorer{shown: "Nothing"}}
	out, err := webRun(t, w, x, "haiku")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.WebTests) != 1 || len(out.Draft.WebTests) != 1 || len(out.Draft.WebTests[0].FailsNow) != 1 {
		t.Fatalf("web tests %+v", out.Draft.WebTests)
	}
	if f := out.Draft.WebTests[0].FailsNow[0]; f.Expect != "Saved" || f.Shown != "Nothing" {
		t.Errorf("fails now = %+v", f)
	}
}

// failingExplorer reports the checks whose claim differs from what the page showed, as the real one does.
type failingExplorer struct{ *fakeExplorer }

func (f *failingExplorer) Explore(ctx context.Context, req WebRequest, drive FlowDriver) (WebOutcome, error) {
	out, err := f.fakeExplorer.Explore(ctx, req, drive)
	if out.Test != nil {
		for _, c := range out.End.Checks {
			if c.Expect != c.Shown {
				out.Test.FailsNow = append(out.Test.FailsNow, FailsNow{Element: c.Ref, Expect: c.Expect, Shown: c.Shown, Cite: req.Cite})
			}
		}
		out.Test.Replay = "failed"
	}
	return out, err
}

func TestTheCostLimitStopsTheFlowsAndLeavesTheCheckManual(t *testing.T) {
	w := newWebPlanner(webTurn1, webTurn2, webTurn3)
	w.cost = 0.5
	in := shopInput(t)
	in.Ladder = ladder(Rung{Planner: w.fake("p"), Model: "haiku"})
	in.JobDir = t.TempDir()
	in.Web = &fakeExplorer{shown: "Saved"}
	in.MaxCost = 0.03 // the three first jobs cost 0.01 each, so no turn can start
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.webJobs()) != 0 || len(out.WebTests) != 0 || !out.CostLimit || len(out.Draft.ManualChecks) != 1 {
		t.Errorf("web jobs %d, web tests %d, cost limit %v", len(w.webJobs()), len(out.WebTests), out.CostLimit)
	}
	if o := out.Outcomes[2]; o.Web == nil || !strings.Contains(o.Web.Manual, "cost limit") {
		t.Errorf("web = %+v", o.Web)
	}
}

// removingExplorer reports a test with a Remove that counts, and fails the second flow.
type removingExplorer struct {
	*fakeExplorer
	removed []string
}

func (r *removingExplorer) Explore(ctx context.Context, req WebRequest, drive FlowDriver) (WebOutcome, error) {
	if len(r.requests) == 1 {
		r.requests = append(r.requests, req)
		return WebOutcome{}, context.Canceled
	}
	out, err := r.fakeExplorer.Explore(ctx, req, drive)
	if out.Test != nil {
		id := string(req.ID)
		out.Test.Remove = func() error { r.removed = append(r.removed, id); return nil }
	}
	return out, err
}

func TestAPlanThatFailsTakesTheTestsOfItsFlowsBackOut(t *testing.T) {
	w := newWebPlanner(webTurn1, webTurn2, webTurn3)
	w.crit.byLine[":10"] = manualAnswer
	x := &removingExplorer{fakeExplorer: &fakeExplorer{shown: "Saved"}}
	_, err := webRun(t, w, x, "haiku")
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err = %v", err)
	}
	if len(x.removed) != 1 || x.removed[0] != "SHOP-482-01" {
		t.Errorf("removed %v: the test that the first flow wrote must go", x.removed)
	}
}

func TestAFlowThatProvesPartOfACriterionKeepsAManualCheckForTheRest(t *testing.T) {
	w := newWebPlanner(webTurn1, webTurn2, partTurn)
	out, err := webRun(t, w, &fakeExplorer{shown: "Saved"}, "haiku")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.WebTests) != 1 || len(out.Draft.ManualChecks) != 1 {
		t.Fatalf("web tests %d, manual checks %d: the part that the test does not prove needs a person", len(out.WebTests), len(out.Draft.ManualChecks))
	}
	m := out.Draft.ManualChecks[0]
	if m.Surface != SurfaceWeb || m.Expected != "A refused save shows the reason." || !strings.HasPrefix(m.Cite, ticket482+":11 ") ||
		!strings.Contains(m.Title, "A board is saved") || len(m.Steps) != 1 || !strings.Contains(m.Steps[0], "A refused save shows the reason.") {
		t.Errorf("manual check = %+v", m)
	}
	if got := out.Draft.WebTests[0].Unproved; got != "A refused save shows the reason." {
		t.Errorf("the plan does not list what the test leaves out: %q", got)
	}
	if len(out.Unaccounted) != 0 {
		t.Errorf("unaccounted %v", out.Unaccounted)
	}
	if len(out.Scenarios) != 2 || out.Scenarios[0].ID != "SHOP-482-02" {
		t.Errorf("the API scenarios must number after the web test, as when it proves all: %v", out.Scenarios)
	}
	if out.Pipeline.Web.Written != 1 || out.Pipeline.Web.Manual != 0 {
		t.Errorf("web stats = %+v", out.Pipeline.Web)
	}
}
