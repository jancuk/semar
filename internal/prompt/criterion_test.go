package prompt

import (
	"strings"
	"testing"
)

func TestCriterionVersion(t *testing.T) {
	if got := CriterionVersion(); got != "4" {
		t.Errorf("CriterionVersion() = %q, want 4", got)
	}
}

func fullCriterion() CriterionData {
	return CriterionData{
		Criterion:   "```text\ndocs/tickets/SHOP-482.md:9\nA refund above the total answers 422.\n```",
		Operations:  "- `refund`: POST /v1/orders/{orderId}/refunds. Refund an order.\n  - Path parameters: `orderId` (string).",
		Fixtures:    "- `order.paid`: A paid order. Outputs: `order`.",
		Accounts:    "- `default`: A support agent.\n- `bob`: A customer.",
		Account:     "default",
		Diff:        "```diff\n+ if in.Amount > order.Total {\n```",
		DiffOmitted: "- `web/orders.js` (+1 -1, source)",
		Existing:    "- `SHOP-482-01`: Refund too much",
	}
}

func TestCriterionRendersEveryField(t *testing.T) {
	d := fullCriterion()
	got, err := Criterion(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{d.Criterion, d.Operations, d.Fixtures, d.Accounts, "acts as `default`", d.Diff, d.DiffOmitted, d.Existing,
		"`scenario`", "`manualCheck`", "`surface`", "`notDrafted`", "{{name}}", "missing-{{run.id}}"} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	for _, unwanted := range []string{"version: ", "previous answer was rejected", "[[", "]]"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the prompt holds %q", unwanted)
		}
	}
	goldenPrompt(t, "criterion.golden.md", got)
}

func TestCriterionLeavesOutWhatIsNotThere(t *testing.T) {
	got, err := Criterion(CriterionData{Criterion: "C", Operations: "- `op`", Account: "default"})
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"## Fixtures", "## Accounts", "## What the code does now", "## Scenarios that exist", "previous answer"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the prompt holds %q with nothing to show", unwanted)
		}
	}
	if strings.Contains(got, "\n\n\n") {
		t.Error("a prompt holds two blank lines in a row")
	}
}

func TestCriterionCorrectionHoldsTheAnswerAndTheProblems(t *testing.T) {
	d := fullCriterion()
	d.Correction = true
	d.Previous = "```json\n{\"answer\": \"[[.Problems]]\"}\n```"
	d.Problems = "- `steps[0].body[0]`: field amountt does not exist in refund; its fields are amount, reason {{x}}"
	got, err := Criterion(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Your previous answer was rejected", "change only what a problem names", d.Previous, d.Problems} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if strings.Contains(got, "\n\n\n") {
		t.Error("a prompt holds two blank lines in a row")
	}
}
