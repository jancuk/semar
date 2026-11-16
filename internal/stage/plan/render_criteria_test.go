package plan

import (
	"strings"
	"testing"
)

func TestRenderGoldenWithTheAcceptanceCriteriaTable(t *testing.T) {
	in := goldenInput(t)
	ci := criteriaInput(t)
	in.Tickets, in.Accounts = ci.Tickets, ci.Accounts
	in.Tickets[0].Text = strings.Replace(in.Tickets[0].Text, "Criterion number 9.", "Pipes | <b>and</b> `ticks`\n   on a second line.", 1)
	d := citingSeven(t)
	out := goldenOutput(t, in, d)
	out.Unaccounted = Unaccounted(d, in.Tickets)
	got := render(t, in, out)
	for _, want := range []string{
		"Acceptance criteria:\n\n| Line | No. | Criterion | Cited by |\n",
		"| 6 | 1 | Criterion number 1. | SHOP-482-01 |\n",
		"| 8 | 3 | Criterion number 3. | manual check: The refused refund shows an error banner |\n",
		"| 11 | 6 | Criterion number 6. | not drafted: criterion 6 |\n",
		"| 13 | 8 | Criterion number 8. | not accounted for |\n",
		"| 14 | 9 | Pipes \\| &lt;b&gt;and&lt;/b&gt; \\`ticks\\` on a second line. | not accounted for |\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan.md lacks %q", want)
		}
	}
	if strings.Count(got, "Acceptance criteria:") != 1 {
		t.Error("only the ticket with a file has a table")
	}
	golden(t, "plan-criteria.golden.md", got)
}
