package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

// The second criterion is for a person on a screen: the API cannot show a banner.
const numberedTicket = `# SHOP-482 Refund above the total
## Acceptance criteria
1. A refund above the rest of the total is refused with 409.
2. The app shows a red banner for the refusal.
`

func (p *planRepo) commitNumberedTicket(t *testing.T) {
	t.Helper()
	p.git.Commit("SHOP-482 write the ticket", map[string]*string{"docs/tickets/SHOP-482.md": gittest.S(numberedTicket)})
}

// numberedDraft is the draft of the ticket test with the manual check on line 4, and without it
// when withManual is false.
func numberedDraft(withManual bool) string {
	d := strings.Replace(ticketDraftText, "SHOP-482.md:5 the app", "SHOP-482.md:4 the app", 1)
	if withManual {
		return d
	}
	i := strings.Index(d, `,
  "manualChecks"`)
	j := strings.LastIndex(d, "}")
	return d[:i] + "\n" + d[j:]
}

func TestPlanSendsAnUnaccountedUICriterionBackAndWritesItAsAManualCheck(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitNumberedTicket(t)
	p.configure(t, numberedDraft(false), numberedDraft(true))
	p.withTickets(t)
	made := p.recordJobs()
	p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--pipeline", "single")

	if len(*made) != 1 || len((*made)[0].Jobs()) != 2 {
		t.Fatalf("want one planner that ran twice, got %d", len(*made))
	}
	second := (*made)[0].Jobs()[1].Prompt
	contains(t, "the second prompt", second, "docs/tickets/SHOP-482.md:4 criterion 2 is not accounted for: add a scenario with source ticket, a manual check, or a notDrafted entry that cites it")
	if strings.Contains(second, "md:3 criterion 1 is not") {
		t.Error("the prompt names the criterion that the draft cites")
	}

	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "plan.md", string(md),
		"Acceptance criteria:\n\n| Line | No. | Criterion | Cited by |\n",
		"| 3 | 1 | A refund above the rest of the total is refused with 409. | SHOP-482-01 |\n",
		"| 4 | 2 | The app shows a red banner for the refusal. | manual check: The refusal shows a red banner |\n",
		"Attempts: 2 of 3")
	ob, err := os.ReadFile(p.path(".semar", "obligations", "SHOP-482.plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the obligations", string(ob), "id: SHOP-482/manual-1", "A red banner shows.")
	if strings.Contains(string(ob), "not accounted for") {
		t.Error("an accounted criterion is a gap")
	}
}

func TestPlanWritesAGapObligationForACriterionThatStaysUnaccounted(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitNumberedTicket(t)
	p.configure(t, numberedDraft(false), numberedDraft(false), numberedDraft(false))
	p.withTickets(t)
	p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--pipeline", "single")

	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "plan.md", string(md), "| 4 | 2 | The app shows a red banner for the refusal. | not accounted for |\n", "Attempts: 3 of 3")
	ob, err := os.ReadFile(p.path(".semar", "obligations", "SHOP-482.plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the obligations", string(ob), "id: SHOP-482/criterion-1", "source: criterion", `cite: "docs/tickets/SHOP-482.md:4"`, "gap: criterion not accounted for by the planner")
	led := p.semar(t, t.Context(), "status")
	contains(t, "the ledger", led.stdout, "SHOP-482/criterion-1")
}
