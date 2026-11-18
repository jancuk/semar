package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/stage/plan"
)

const againLinesTicket = "## Acceptance criteria\n\n1. One is refused.\n2. Two is accepted.\n3. Three is shown.\n4. Four is added.\n"

func againFor(t *testing.T, ticketText string, earlier ...plan.Earlier) *plan.Again {
	t.Helper()
	a := plan.Classify([]knowledge.Ticket{{Key: "SHOP-7", Path: "docs/tickets/SHOP-7.md", Text: ticketText}}, map[string][]plan.Earlier{"SHOP-7": earlier})
	return &a
}

func TestTheEndOfASecondPlanCountsCriteriaAndNamesTheOldTests(t *testing.T) {
	was := plan.Criteria(knowledge.Ticket{Key: "SHOP-7", Path: "docs/tickets/SHOP-7.md", Text: againLinesTicket})
	edited := strings.Replace(againLinesTicket, "Two is accepted", "Two is accepted for a minor", 1)
	a := againFor(t, edited,
		plan.Earlier{Number: 1, Key: was[0].Key, Tests: []domain.ScenarioID{"SHOP-7-01"}},
		plan.Earlier{Number: 2, Key: was[1].Key, Tests: []domain.ScenarioID{"SHOP-7-04"}},
		plan.Earlier{Number: 3, Key: was[2].Key, Manual: true},
		plan.Earlier{Number: 9, Key: strings.Repeat("9", 64), Tests: []domain.ScenarioID{"SHOP-7-09"}},
	)
	got := againLines(a)
	want := []string{
		"criteria: 4, 2 kept, 2 planned (1 changed), 1 removed",
		`SHOP-7-04 was written for an earlier text of criterion 2. Read both tests. To remove the old one: semar rm SHOP-7-04 --reason "criterion 2 changed"`,
		`SHOP-7-09 was written for a criterion that SHOP-7 no longer has (it was criterion 9). To remove it: semar rm SHOP-7-09 --reason "criterion 9 was removed"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("lines\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAFirstPlanHasNoLinesAboutEarlierPlans(t *testing.T) {
	if got := againLines(againFor(t, againLinesTicket)); len(got) != 0 {
		t.Errorf("lines = %q", got)
	}
	if got := againLines(nil); len(got) != 0 {
		t.Errorf("lines = %q", got)
	}
}
