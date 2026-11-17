package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/fake"
)

// refundAnswer is a scenario for the refund operation of the shop.
func refundAnswer(title string, amount, status int, field string) string {
	return fmt.Sprintf(`{"answer":{"kind":"scenario","title":%q,"intent":"Before: x. Now: y.",
 "given":[{"fixture":"order.paid","as":"order","with":[{"name":"customer","value":"c-1"}]}],
 "steps":[{"id":"refund","setup":false,"as":"default","action":"Refund","operation":"post_v1_orders_orderId_refunds",
  "path":[{"name":"orderId","value":"{{order.order}}"}],"query":[],"body":[{"field":%q,"value":%d}],"captures":[],
  "expect":[{"subject":"status","name":"","operator":"equals","value":%d}]}]}}`, title, field, amount, status)
}

const manualAnswer = `{"answer":{"kind":"manualCheck","title":"The order page shows the refund","manualSteps":["Open the order"],"expected":"The refund is listed"}}`

// answersByCriterion answers each criterion of the shop ticket by its line, and the line 9 criterion
// wrongly while bad is set. It records every prompt that it was given.
type answersByCriterion struct {
	mu       sync.Mutex
	prompts  []string
	jobs     []provider.Job
	byLine   map[string]string
	bad      map[string]bool // lines that get a body field that does not exist
	always   map[string]bool // lines that get it again in a correction
	model    string
	failWith error
}

func (a *answersByCriterion) fake(id string) *fake.Fake {
	return fake.NewFunc(id, func(j provider.Job) (provider.Result, error) {
		a.mu.Lock()
		a.prompts = append(a.prompts, j.Prompt)
		a.jobs = append(a.jobs, j)
		a.mu.Unlock()
		if a.failWith != nil {
			return provider.Result{InputTokens: 7}, a.failWith
		}
		line := ""
		for _, l := range []string{":9", ":10", ":11"} {
			if strings.Contains(j.Prompt, ticket482+l+"\n") {
				line = l
			}
		}
		correction := strings.Contains(j.Prompt, "Your previous answer was rejected")
		out := a.byLine[line]
		if a.bad[line] && (!correction || a.always[line]) {
			out = refundAnswer("A bad one", 1, 422, "amountt")
		}
		return provider.Result{Output: json.RawMessage(out), Model: j.Model, Turns: 1, InputTokens: 100, OutputTokens: 10, CostUSD: 0.01}, nil
	})
}

func shopAnswers() *answersByCriterion {
	return &answersByCriterion{
		byLine: map[string]string{
			":9":  refundAnswer("A refund above the total is refused", 150000, 422, "amount"),
			":10": refundAnswer("A partial refund is accepted", 40000, 201, "amount"),
			":11": manualAnswer,
		},
		bad: map[string]bool{}, always: map[string]bool{},
	}
}

func ladder(rungs ...Rung) []Rung { return rungs }

func TestRunCriteriaAnswersEachCriterionWithNoTools(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	f := a.fake("p")
	in.Ladder = ladder(Rung{Planner: f, Model: "haiku", Reasoning: "low"})
	in.JobDir = t.TempDir()
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Scenarios) != 2 || len(out.Draft.ManualChecks) != 1 || len(out.Unaccounted) != 0 || len(out.Scripts) != 2 {
		t.Fatalf("scenarios %d, manual checks %d, unaccounted %v", len(out.Scenarios), len(out.Draft.ManualChecks), out.Unaccounted)
	}
	st := out.Pipeline
	if st.Criteria != 3 || st.Calls != 3 || st.Corrections != 0 || st.Escalated != 0 || st.Reused != 0 || st.Unanswered != 0 ||
		len(st.ByModel) != 1 || st.ByModel[0] != (ModelCount{"haiku", 3}) {
		t.Errorf("stats = %+v", st)
	}
	for _, j := range a.jobs {
		if !j.NoTools || j.MaxTurns != CriterionMaxTurns || j.Model != "haiku" || j.Reasoning != "low" || j.Workdir != in.JobDir || len(j.Schema) == 0 {
			t.Errorf("job = %+v", j)
		}
		if !strings.Contains(j.Prompt, "post_v1_orders_orderId_refunds") || !strings.Contains(j.Prompt, "order.paid") || !strings.Contains(j.Prompt, "handler.go") {
			t.Errorf("the prompt lacks the operations, the fixtures, or the diff:\n%s", j.Prompt)
		}
	}
	if out.Result.InputTokens != 300 || out.Result.OutputTokens != 30 || out.Attempts != 3 {
		t.Errorf("totals = %+v, attempts %d", out.Result, out.Attempts)
	}
	// The draft is the one the writer already knows: the ticket, the scenarios with their cites, the manual check.
	sc := out.Scenarios[0]
	if sc.ID != "SHOP-482-01" || sc.Steps[0].Expect[0].Source != domain.SourceTicket || !strings.HasPrefix(sc.Steps[0].Expect[0].Cite, ticket482+":9 ") {
		t.Errorf("scenario = %+v", sc)
	}
	if got := out.Draft.Tickets; len(got) != 1 || got[0].Kind != "behaviour" || got[0].Behaviour != "Refunds of an order" {
		t.Errorf("tickets = %+v", got)
	}
	if out.Scripts[0].AuthoredBy.Instance != "p" {
		t.Errorf("authored by = %+v", out.Scripts[0].AuthoredBy)
	}
	if len(out.Answers) != 3 || out.Answers[0].Hash == "" || out.Answers[0].Cite != ticket482+":9" {
		t.Errorf("answers = %+v", out.Answers)
	}
}

func TestRunCriteriaCorrectsAnAnswerWithThePlainProblems(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	a.bad[":9"] = true
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	st := out.Pipeline
	if st.Calls != 4 || st.Corrections != 1 || st.Escalated != 0 || len(out.Unaccounted) != 0 {
		t.Fatalf("stats = %+v", st)
	}
	var correction string
	for _, p := range a.prompts {
		if strings.Contains(p, "Your previous answer was rejected") {
			correction = p
		}
	}
	for _, want := range []string{"field amountt does not exist in post_v1_orders_orderId_refunds; its fields are amount, reason", "A bad one", ticket482 + ":9"} {
		if !strings.Contains(correction, want) {
			t.Errorf("the correction lacks %q:\n%s", want, correction)
		}
	}
	if out.Outcomes[0].Corrections != 1 {
		t.Errorf("outcome = %+v", out.Outcomes[0])
	}
}

func TestRunCriteriaEscalatesToTheNextModelAfterOneCorrection(t *testing.T) {
	in := shopInput(t)
	small, big := shopAnswers(), shopAnswers()
	small.bad[":9"], small.always[":9"] = true, true
	in.Ladder = ladder(Rung{Planner: small.fake("small"), Model: "haiku"}, Rung{Planner: big.fake("big"), Model: "opus"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	st := out.Pipeline
	// Criteria 2 and 3 take one call of the small model. Criterion 1 takes two calls of it and then one of the big one.
	if st.Calls != 5 || st.Corrections != 1 || st.Escalated != 1 || len(out.Unaccounted) != 0 ||
		len(st.ByModel) != 2 || st.ByModel[0] != (ModelCount{"haiku", 2}) || st.ByModel[1] != (ModelCount{"opus", 1}) {
		t.Fatalf("stats = %+v", st)
	}
	if len(big.jobs) != 1 || strings.Contains(big.jobs[0].Prompt, "previous answer was rejected") {
		t.Errorf("the higher model gets a fresh job: %d jobs", len(big.jobs))
	}
	if len(st.Uses) != 2 || st.Uses[0].Calls != 4 || st.Uses[1].Calls != 1 || st.Uses[1].PlannerID != "big" || st.Uses[0].Result.InputTokens != 400 {
		t.Errorf("uses = %+v", st.Uses)
	}
	if got := out.Scripts[0].AuthoredBy.Instance; got != "big" {
		t.Errorf("the scenario of criterion 1 was authored by %q", got)
	}
}

func TestRunCriteriaKeepsACriterionNoModelAnswered(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	a.bad[":9"], a.always[":9"] = true, true
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if st := out.Pipeline; st.Unanswered != 1 || st.Calls != 4 {
		t.Errorf("stats = %+v", st)
	}
	if len(out.Unaccounted) != 1 || out.Unaccounted[0].Number != 1 {
		t.Errorf("unaccounted = %+v: the criterion stays a gap", out.Unaccounted)
	}
	if o := out.Outcomes[0]; o.Answered || len(o.Problems) == 0 {
		t.Errorf("outcome = %+v", o)
	}
	if len(out.Scenarios) != 1 {
		t.Errorf("scenarios = %d", len(out.Scenarios))
	}
}

func TestRunCriteriaReusesAnAnswerOfTheSamePacket(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	first, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	// The plan wrote its scenarios, so the next plan sees them. Criterion 2 changes its text, so only it is asked again.
	b := shopAnswers()
	in2 := shopInput(t)
	in2.Tickets[0].Text = strings.Replace(shopTicket, "grows by the amount.", "grows by the amount, and the status is paid.", 1)
	in2.Ladder = ladder(Rung{Planner: b.fake("p"), Model: "haiku"})
	in2.Previous = first.Answers
	in2.Existing = map[string][]domain.Scenario{}
	out, err := RunCriteria(context.Background(), in2)
	if err != nil {
		t.Fatal(err)
	}
	st := out.Pipeline
	if st.Reused != 2 || st.Calls != 1 || len(b.jobs) != 1 || !strings.Contains(b.jobs[0].Prompt, "status is paid") {
		t.Fatalf("stats = %+v, jobs %d", st, len(b.jobs))
	}
	if !out.Outcomes[0].Reused || out.Outcomes[1].Reused || !out.Outcomes[2].Reused {
		t.Errorf("criteria 1 and 3 are unchanged and 2 is not: %v %v %v", out.Outcomes[0].Reused, out.Outcomes[1].Reused, out.Outcomes[2].Reused)
	}
	if len(out.Scenarios) != 2 || len(out.Draft.ManualChecks) != 1 {
		t.Errorf("the reused answer is in the draft: %d scenarios", len(out.Scenarios))
	}
}

func TestRunCriteriaCorrectsAStoredAnswerThatNoLongerPasses(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	first, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	// The scenario of criterion 1 exists now, so its stored answer repeats a title: it is corrected, not asked from nothing.
	b := shopAnswers()
	b.byLine[":9"] = refundAnswer("A refund above the total is refused, again", 150000, 422, "amount")
	in2 := shopInput(t)
	in2.Existing = map[string][]domain.Scenario{"SHOP-482": {{ID: "SHOP-482-01", Title: "A refund above the total is refused"}}}
	in2.Ladder = ladder(Rung{Planner: b.fake("p"), Model: "haiku"})
	in2.Previous = first.Answers
	out, err := RunCriteria(context.Background(), in2)
	if err != nil {
		t.Fatal(err)
	}
	if st := out.Pipeline; st.Calls != 1 || st.Corrections != 1 || st.Reused != 2 {
		t.Fatalf("stats = %+v", st)
	}
	if !strings.Contains(b.prompts[0], "repeats a scenario of ticket SHOP-482") || !strings.Contains(b.prompts[0], "A refund above the total is refused") {
		t.Errorf("the correction does not say what is wrong:\n%s", b.prompts[0])
	}
	if len(out.Scenarios) != 2 {
		t.Errorf("scenarios = %d", len(out.Scenarios))
	}
}

func TestRunCriteriaKeepsTitlesUniqueAndTheLimitOfScenarios(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	a.byLine[":10"] = refundAnswer("A refund above the total is refused", 40000, 201, "amount")
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Scenarios[1].Title; got != "A refund above the total is refused (criterion 2)" {
		t.Errorf("title = %q", got)
	}

	in = shopInput(t)
	in.Limits.MaxScenarios = 1
	in.Ladder = ladder(Rung{Planner: shopAnswers().fake("p"), Model: "haiku"})
	out, err = RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Scenarios) != 1 || len(out.Draft.NotDrafted) != 1 || !strings.Contains(out.Draft.NotDrafted[0].Needs, "higher limit") || len(out.Unaccounted) != 0 {
		t.Errorf("scenarios %d, notDrafted %+v, unaccounted %v: the one over the limit stays accounted for", len(out.Scenarios), out.Draft.NotDrafted, out.Unaccounted)
	}
}

func TestRunCriteriaStopsOnAProviderErrorWithTheTokensSpent(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	a.failWith = &provider.Error{Class: provider.UsageLimit, Detail: "the limit is reached"}
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.UsageLimit {
		t.Fatalf("err = %v", err)
	}
	if out.Result.InputTokens == 0 || out.Attempts == 0 || len(out.Scenarios) != 0 {
		t.Errorf("out = %+v", out)
	}
}

func TestRunCriteriaRunsAtMostMaxParallelJobsAtOnce(t *testing.T) {
	in := shopInput(t)
	var text strings.Builder
	text.WriteString("## Acceptance criteria\n\n")
	for i := range 12 {
		fmt.Fprintf(&text, "%d. Refund number %d is refused.\n", i+1, i)
	}
	in.Tickets[0].Text = text.String()
	var running, peak atomic.Int32
	f := fake.NewFunc("p", func(j provider.Job) (provider.Result, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return provider.Result{Output: json.RawMessage(`{"answer":{"kind":"notDrafted","what":"w","needs":"fixture x"}}`)}, nil
	})
	in.Ladder = ladder(Rung{Planner: f, Model: "haiku"})
	var last Progress
	var mu sync.Mutex
	in.Progress = func(p Progress) { mu.Lock(); last = p; mu.Unlock() }
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got < 2 || got > MaxParallelJobs {
		t.Errorf("peak = %d, want 2 to %d", got, MaxParallelJobs)
	}
	if len(out.Draft.NotDrafted) != 12 || len(out.Unaccounted) != 0 {
		t.Errorf("notDrafted %d, unaccounted %d", len(out.Draft.NotDrafted), len(out.Unaccounted))
	}
	if last.Total != 12 || last.Done != 12 {
		t.Errorf("progress = %+v", last)
	}
}

func TestRunCriteriaNeedsALadderAndACriterion(t *testing.T) {
	in := shopInput(t)
	if _, err := RunCriteria(context.Background(), in); err == nil || !strings.Contains(err.Error(), "at least one model") {
		t.Errorf("err = %v", err)
	}
	in.Ladder = ladder(Rung{Planner: shopAnswers().fake("p"), Model: "haiku"})
	in.Tickets = nil
	if _, err := RunCriteria(context.Background(), in); err == nil || !strings.Contains(err.Error(), "--pipeline single") {
		t.Errorf("err = %v", err)
	}
}

// trialOnce says 405 for the step of the second scenario, whose result is a success, once, and nothing after.
type trialOnce struct {
	calls int
	steps []TrialStep
}

func (tr *trialOnce) Try(_ context.Context, scenarios []domain.Scenario, scripts []domain.Script) (Trial, error) {
	tr.calls++
	if tr.calls > 1 || len(scenarios) < 2 {
		return Trial{Env: "local"}, nil
	}
	sc := scenarios[1]
	return Trial{Env: "local", Steps: []TrialStep{{
		Scenario: sc.ID, Step: sc.Steps[0].ID, Result: TrialSent, Method: "POST", Route: "/v1/orders/{orderId}/refunds", Status: 405,
	}}}, nil
}

func TestRunCriteriaSendsTrialProblemsBackToTheCriterionThatOwnsTheScenario(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	tr := &trialOnce{}
	in.Trial = tr
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if tr.calls != 2 || out.Trial == nil || out.TrialReturned != 1 || len(out.Refused) != 1 {
		t.Fatalf("trial calls %d, returned %d, refused %d", tr.calls, out.TrialReturned, len(out.Refused))
	}
	if st := out.Pipeline; st.Calls != 4 || st.Corrections != 1 {
		t.Errorf("stats = %+v", st)
	}
	var correction string
	for _, p := range a.prompts {
		if strings.Contains(p, "trial: the environment answered 405") {
			correction = p
		}
	}
	if !strings.Contains(correction, ticket482+":10\n") {
		t.Errorf("the trial problem went to the wrong criterion:\n%s", correction)
	}
	if len(out.Scenarios) != 2 {
		t.Errorf("scenarios = %d", len(out.Scenarios))
	}
}

func TestRunCriteriaTreatsAJobWithNoAnswerAsAProblemOfTheAnswer(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	good := a.fake("p")
	var first atomic.Bool
	f := fake.NewFunc("p", func(j provider.Job) (provider.Result, error) {
		if strings.Contains(j.Prompt, ticket482+":9\n") && first.CompareAndSwap(false, true) {
			return provider.Result{InputTokens: 50}, &provider.Error{Class: provider.ValidationError, Detail: "the harness reached the turn limit"}
		}
		return good.Run(context.Background(), j)
	})
	in.Ladder = ladder(Rung{Planner: f, Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if st := out.Pipeline; st.Corrections != 1 || st.Unanswered != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if got := messages(out.Outcomes[0].Returned); !strings.Contains(got, "the job returned no answer in the schema: the harness reached the turn limit") {
		t.Errorf("returned = %q", got)
	}

	// A refusal of the schema is the same for every job, so it ends the run.
	in2 := shopInput(t)
	refuse := fake.NewFunc("p", func(provider.Job) (provider.Result, error) {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: "the harness refused the output schema: too deep"}
	})
	in2.Ladder = ladder(Rung{Planner: refuse, Model: "haiku"})
	if _, err := RunCriteria(context.Background(), in2); err == nil {
		t.Error("a refused schema did not end the run")
	}
}
