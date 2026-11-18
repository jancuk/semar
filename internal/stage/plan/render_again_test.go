package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
)

func TestPlanMdListsWhatWasNotPlannedAgainAndWhy(t *testing.T) {
	in := shopInput(t)
	cs := Criteria(in.Tickets[0])
	in.Earlier = map[string][]Earlier{"SHOP-482": {
		{Number: 1, Key: cs[0].Key, Tests: []domain.ScenarioID{"SHOP-482-01"}},
		{Number: 2, Key: strings.Repeat("e", 64), Tests: []domain.ScenarioID{"SHOP-482-02", "SHOP-482-05"}},
		{Number: 3, Key: cs[2].Key, Manual: true},
		{Number: 7, Key: strings.Repeat("f", 64), Tests: []domain.ScenarioID{"SHOP-482-07"}},
	}}
	a := shopAnswers()
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	got := render(t, in, out)
	for _, want := range []string{
		"## Criteria that earlier plans covered\n",
		"- Kept, criterion 1 of SHOP-482. Tests: SHOP-482-01. No job ran for it.\n",
		"- Kept, criterion 3 of SHOP-482. A manual check keeps it. No job ran for it.\n",
		"- Changed, criterion 2 of SHOP-482. It was edited, so it was planned again. Tests of the earlier text: SHOP-482-02, SHOP-482-05. Read them against the new text, and remove one with `semar rm` if it is wrong now.\n",
		"- Removed, a criterion of SHOP-482 that was number 7. The ticket file no longer has it. Tests written for it: SHOP-482-07. Remove one with `semar rm` if no other criterion needs it.\n",
		"| 9 | 1 | ", "| SHOP-482-01 (kept) |\n",
		"| 11 | 3 | ", "| manual check (kept) |\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan.md lacks %q\n%s", want, got)
		}
	}
	if strings.Index(got, "## Criteria that earlier plans covered") > strings.Index(got, "## Not drafted") {
		t.Error("the section is after the Not drafted section")
	}
}

func TestPlanMdHasNoSectionForAFirstPlan(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := render(t, in, out); strings.Contains(got, "earlier plans covered") {
		t.Errorf("a first plan has a section for earlier plans:\n%s", got)
	}
}

func TestPlanMdSaysWhyATicketWhoseCriteriaAreAllKeptHasNoNewTest(t *testing.T) {
	in := shopInput(t)
	const other = "# SHOP-483: Reads\n\n## Acceptance criteria\n\n1. An order is read by its ID.\n"
	in.Change.Tickets = append(in.Change.Tickets, domain.TicketChange{Key: "SHOP-483"})
	in.Tickets = append(in.Tickets, knowledge.Ticket{Key: "SHOP-483", Path: "docs/tickets/SHOP-483.md", Text: other})
	in.Earlier = map[string][]Earlier{"SHOP-483": {{Number: 1, Key: Criteria(in.Tickets[1])[0].Key, Tests: []domain.ScenarioID{"SHOP-483-01"}}}}
	a := shopAnswers()
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	got := render(t, in, out)
	if strings.Contains(got, "--pipeline single") {
		t.Errorf("plan.md sends the ticket with a kept criterion to the single pipeline:\n%s", got)
	}
	if !strings.Contains(got, "### SHOP-483\n\nKind: none. Risk: standard, because its 1 acceptance criterion has a test or a manual check from an earlier plan.\n") {
		t.Errorf("plan.md does not say why SHOP-483 has no new test:\n%s", got)
	}
}
