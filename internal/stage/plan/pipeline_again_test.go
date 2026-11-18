package plan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
)

// keptShop is what a first plan of the shop ticket left behind: a test for criterion 1 and a manual
// check for criterion 3.
func keptShop(in Input) map[string][]Earlier {
	cs := Criteria(in.Tickets[0])
	return map[string][]Earlier{"SHOP-482": {
		{Number: 1, Key: cs[0].Key, Tests: []domain.ScenarioID{"SHOP-482-01"}},
		{Number: 3, Key: cs[2].Key, Manual: true},
	}}
}

func TestRunCriteriaAsksNoModelAboutAKeptCriterion(t *testing.T) {
	first := shopInput(t)
	a := shopAnswers()
	first.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	done, err := RunCriteria(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}

	// The scenario of criterion 1 exists now, and so do the stored answers of all three criteria. Only
	// criterion 2 is asked: a stored answer of a kept criterion is not even read, so it cannot be refused
	// for a title that the scenario has.
	in := shopInput(t)
	in.Earlier = keptShop(in)
	in.Existing = map[string][]domain.Scenario{"SHOP-482": {{ID: "SHOP-482-01", Title: "A refund above the total is refused"}}}
	b := shopAnswers()
	in.Ladder = ladder(Rung{Planner: b.fake("p"), Model: "haiku"})
	in.Previous = done.Answers
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.jobs) != 0 {
		t.Errorf("a stored answer of criterion 2 was not reused, so %d jobs ran", len(b.jobs))
	}
	st := out.Pipeline
	if st.Criteria != 1 || st.Calls != 0 || st.Corrections != 0 || st.Reused != 1 {
		t.Errorf("stats = %+v: one criterion is planned, from its stored answer", st)
	}
	if len(out.Scenarios) != 1 || out.Scenarios[0].Title != "A partial refund is accepted" || len(out.Draft.ManualChecks) != 0 {
		t.Errorf("scenarios %+v, manual checks %+v", out.Scenarios, out.Draft.ManualChecks)
	}
	if len(out.Unaccounted) != 0 {
		t.Errorf("a kept criterion counts as accounted for: %+v", out.Unaccounted)
	}
	if out.Again == nil || out.Again.Count(StateKept) != 2 || out.Again.Count(StateNew) != 1 {
		t.Errorf("again = %+v", out.Again)
	}
}

func TestRunCriteriaSendsOnlyTheNewCriterionToTheModel(t *testing.T) {
	in := shopInput(t)
	in.Earlier = keptShop(in)
	a := shopAnswers()
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.prompts) != 1 || !strings.Contains(a.prompts[0], ticket482+":10\n") {
		t.Fatalf("%d jobs ran, want one for criterion 2", len(a.prompts))
	}
	if out.Pipeline.Criteria != 1 || len(out.Outcomes) != 1 || len(out.Answers) != 1 {
		t.Errorf("stats %+v, %d outcomes, %d answers", out.Pipeline, len(out.Outcomes), len(out.Answers))
	}
}

func TestRunCriteriaDoesNotExploreAKeptWebCriterion(t *testing.T) {
	in := shopInput(t)
	in.Earlier = keptShop(in)
	w := newWebPlanner(webTurn1, webTurn2, webTurn3)
	x := &fakeExplorer{shown: "Saved"}
	in.Ladder = ladder(Rung{Planner: w.fake("p"), Model: "haiku"})
	in.Web = x
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.requests) != 0 || len(w.webJobs()) != 0 || len(out.WebTests) != 0 {
		t.Errorf("the kept web criterion was explored: %d requests, %d web jobs", len(x.requests), len(w.webJobs()))
	}
}

func TestRunCriteriaPlansAChangedCriterionAndKeepsTheRest(t *testing.T) {
	first := shopInput(t)
	in := shopInput(t)
	in.Earlier = keptShop(first)
	in.Tickets[0].Text = strings.Replace(shopTicket, "A refund above the order total", "A refund above the open order total", 1)
	a := shopAnswers()
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.prompts) != 2 {
		t.Fatalf("%d jobs ran, want criteria 1 and 2", len(a.prompts))
	}
	if got := out.Again.Count(StateChanged); got != 1 || out.Again.Criteria[0].State != StateChanged || len(out.Again.Criteria[0].Tests) != 1 {
		t.Errorf("again = %+v", out.Again.Criteria)
	}
}

func TestRunCriteriaRefusesToPlanWhenEveryCriterionIsKept(t *testing.T) {
	in := shopInput(t)
	cs := Criteria(in.Tickets[0])
	in.Earlier = map[string][]Earlier{"SHOP-482": {
		{Number: 1, Key: cs[0].Key, Tests: []domain.ScenarioID{"SHOP-482-01"}},
		{Number: 2, Key: cs[1].Key, Tests: []domain.ScenarioID{"SHOP-482-02"}},
		{Number: 3, Key: cs[2].Key, Manual: true},
	}}
	a := shopAnswers()
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	_, err := RunCriteria(context.Background(), in)
	if !errors.Is(err, ErrAllKept) || len(a.jobs) != 0 {
		t.Errorf("err = %v, %d jobs", err, len(a.jobs))
	}
}

func TestAccountingCountsAKeptCriterionAsAccountedFor(t *testing.T) {
	in := shopInput(t)
	cs := Criteria(in.Tickets[0])
	again := Classify(in.Tickets, map[string][]Earlier{"SHOP-482": {{Number: 1, Key: cs[0].Key, Tests: []domain.ScenarioID{"SHOP-482-01"}}, {Number: 3, Key: cs[2].Key, Manual: true}}})
	got := Accounting(Draft{}, []knowledge.Ticket{in.Tickets[0]}, nil, &again)
	if len(got) != 3 || strings.Join(got[0].CitedBy, ",") != "SHOP-482-01 (kept)" || strings.Join(got[2].CitedBy, ",") != "manual check (kept)" || len(got[1].CitedBy) != 0 {
		t.Errorf("accounting = %+v", got)
	}
}
