package plan

import (
	"strings"
	"testing"
)

// A renamed scenario is matched by the method and route of its first step, so a rename cannot
// carry a changed expected result past D8.
func TestExpectationFrozenCatchesARename(t *testing.T) {
	renamed := withProblem(t, func(d *Draft) {
		d.Scenarios[0].Title = "A refund that is too large is refused"
		d.Scenarios[0].Steps[1].Expect[0].Check.Status = intp(404)
	})
	tr := &fakeTrialer{trials: []Trial{{Steps: []TrialStep{readStep(405)}}, {}}}
	p := scriptedDrafts([]byte(goodDraft), renamed, []byte(goodDraft))
	in := input(t)
	in.Trial = tr
	if _, err := Run(t.Context(), p, in); err != nil {
		t.Fatal(err)
	}
	want := "- `scenarios[0].steps[1].expect`: the expected results of \"A refund that is too large is refused\", which has the first step of \"Refund above the order total is rejected\", changed after the trial run"
	if len(p.jobs) < 3 {
		t.Fatalf("%d prompts, want 3: the changed draft was accepted", len(p.jobs))
	}
	if !strings.Contains(p.jobs[2].Prompt, want) {
		t.Errorf("the third prompt does not refuse the renamed scenario:\n%s", p.jobs[2].Prompt)
	}
	if len(tr.calls) != 2 {
		t.Errorf("%d trials, want 2: the renamed draft is not tried", len(tr.calls))
	}
}

// A rename that keeps every expected result is a repair.
func TestRenameWithTheSameExpectationsPasses(t *testing.T) {
	renamed := withProblem(t, func(d *Draft) { d.Scenarios[0].Title = "A refund that is too large is refused" })
	tr := &fakeTrialer{trials: []Trial{{Steps: []TrialStep{readStep(405)}}, {Run: "r-2"}}}
	p := scriptedDrafts([]byte(goodDraft), renamed)
	in := input(t)
	in.Trial = tr
	out, err := Run(t.Context(), p, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.UsedAttempt != 2 || len(tr.calls) != 2 {
		t.Fatalf("used attempt %d, %d trials; want the renamed draft tried and accepted", out.UsedAttempt, len(tr.calls))
	}
}
