package plan

import (
	"strings"
	"testing"
)

func TestPlanShowsWhatEachStepSends(t *testing.T) {
	in := goldenInput(t)
	got := render(t, in, goldenOutput(t, in, draft(t)))
	for _, want := range []string{
		"1. POST /v1/orders (setup): Create a paid order for 100000\n   - Sends json {\"amount\":100000}\n",
		"2. POST /v1/orders/{id}/refunds: Request a refund of 150000\n   - Sends POST /v1/orders/{{order_id}}/refunds; json {\"amount\":150000}\n   - Expects status 422.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan.md lacks %q:\n%s", want, got)
		}
	}
}

func TestPlanCutsAndEscapesWhatAScriptSends(t *testing.T) {
	in := goldenInput(t)
	out := goldenOutput(t, in, draft(t))
	r := out.Scripts[0].Steps[0].Request
	r.JSON = map[string]any{"note": strings.Repeat("x", 700), "html": "<b>[a](http://evil.test)</b>\n@ada"}
	got := render(t, in, out)
	if strings.Contains(got, "<b>") || strings.Contains(got, "](http://evil.test)") || strings.Contains(got, "\n@ada") {
		t.Errorf("a script put markup into plan.md:\n%s", got)
	}
	if !strings.Contains(got, "… [cut: ") {
		t.Errorf("a 700 character body is not cut:\n%s", got)
	}
}

func TestPlanHasNoSendsLineForAStepWithoutAScript(t *testing.T) {
	in := goldenInput(t)
	out := goldenOutput(t, in, draft(t))
	out.Scripts = nil
	if got := render(t, in, out); strings.Contains(got, "Sends") {
		t.Errorf("plan.md has a Sends line with no script:\n%s", got)
	}
}
