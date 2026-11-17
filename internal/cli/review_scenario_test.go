package cli

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/obligation"
)

func refundScenario(cite1, cite2 string) domain.Scenario {
	status, zero := 422, 0
	return domain.Scenario{
		ID: "SHOP-482-01", Title: "Refund above the paid amount is refused", Ticket: "SHOP-482", Surface: domain.SurfaceAPI, AuthProfile: "default",
		Given: []domain.Given{{Fixture: "order.paid", As: "o"}},
		Steps: []domain.Step{
			{ID: "pay", Setup: true, Action: "Pay the order in full."},
			{ID: "refund", Action: "Refund more than was paid.", Expect: []domain.Expectation{
				{Check: domain.Check{Status: &status}, Source: domain.SourceTicket, Cite: cite1, By: "model"},
				{Check: domain.Check{JSONPath: "$.data.position", Equals: zero}, Source: domain.SourceTicket, Cite: cite2},
				{Check: domain.Check{JSONPath: "$.error.code", Equals: "REFUND_TOO_LARGE"}, Source: domain.SourceDiff},
			}},
		},
	}
}

func showItem(t *testing.T, dir string, use *obligation.Located, sc domain.Scenario) string {
	t.Helper()
	var b strings.Builder
	showScenarioItem(&b, dir, 2, 12, use, []unconfirmed{needsConfirmation(dir, sc, []domain.Source{domain.SourceTicket, domain.SourceDiff})}, func(domain.FixtureID) string { return "approved" })
	return b.String()
}

const ticketLines = "# SHOP-482 Limit a refund\n\n## Acceptance criteria\n\n1. A refund larger than the paid amount is refused.\n2. A refund keeps the card at the top.\n3. Third.\n"

func TestAScenarioItemOpensWithTheRequirementAndThenTheTest(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, "docs/tickets/SHOP-482.md", ticketLines)
	use := &obligation.Located{Obligation: obligation.Obligation{ID: "SHOP-482/scenario-SHOP-482-01", Title: "Refund above the paid amount is refused"}}
	got := showItem(t, dir, use, refundScenario("docs/tickets/SHOP-482.md:5 refund is refused", "docs/tickets/SHOP-482.md:5"))
	want := `
2 of 12 · SHOP-482 · docs/tickets/SHOP-482.md:5
  "A refund larger than the paid amount is refused."

  Test SHOP-482-01 · API · as default
    Refund above the paid amount is refused
    Given the fixture order.paid as o
    And   Pay the order in full.
    When  Refund more than was paid.
    Then  the status is 422 (by model)
    And   $.data.position equals 0
    And   $.error.code equals "REFUND_TOO_LARGE" (source diff)
    Uses the fixture order.paid (approved)
`
	if got != want {
		t.Errorf("item:\n%s\nwant:\n%s", got, want)
	}
}

func TestAScenarioItemListsEveryDistinctCite(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, "docs/tickets/SHOP-482.md", ticketLines)
	use := &obligation.Located{Obligation: obligation.Obligation{ID: "SHOP-482/scenario-SHOP-482-01", Title: "Refund"}}
	got := showItem(t, dir, use, refundScenario("docs/tickets/SHOP-482.md:5", "docs/tickets/SHOP-482.md:6 the order"))
	contains := []string{
		"2 of 12 · SHOP-482 · docs/tickets/SHOP-482.md:5, docs/tickets/SHOP-482.md:6\n",
		"  \"A refund larger than the paid amount is refused.\"\n  \"A refund keeps the card at the top.\"\n",
	}
	for _, want := range contains {
		if !strings.Contains(got, want) {
			t.Errorf("item lacks %q:\n%s", want, got)
		}
	}
}

func TestAScenarioItemKeepsTheTitleOfTheUseCaseWhenNothingIsCited(t *testing.T) {
	dir := t.TempDir()
	use := &obligation.Located{Obligation: obligation.Obligation{ID: "SHOP-482/scenario-SHOP-482-01", Title: "Refund above the paid amount is refused"}}
	got := showItem(t, dir, use, refundScenario("SHOP-482: a refund above the paid amount must be refused", ""))
	for _, want := range []string{
		"\n2 of 12 · SHOP-482/scenario-SHOP-482-01\n  \"Refund above the paid amount is refused\"\n\n  Test SHOP-482-01 · API · as default\n    Given the fixture",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("item lacks %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "Refund above the paid amount is refused"); n != 1 {
		t.Errorf("the title is printed %d times, want once:\n%s", n, got)
	}
}

func TestAScenarioItemSaysWhenACitedLineIsNotThere(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, "docs/tickets/SHOP-482.md", "one\n")
	got := showItem(t, dir, nil, refundScenario("docs/tickets/SHOP-482.md:9", "docs/tickets/SHOP-482.md:9"))
	if !strings.Contains(got, "2 of 12 · SHOP-482 · docs/tickets/SHOP-482.md:9\n  line not found\n") {
		t.Errorf("item:\n%s", got)
	}
}
