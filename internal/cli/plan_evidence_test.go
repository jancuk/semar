package cli_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/sampleapp"
)

// The planner gets the difference in the prompt, taken from the same copy that
// .semar-plan/change.diff holds.
func TestPlanPromptHoldsTheDifference(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	s := &spy{err: &provider.Error{Class: provider.ProviderError, Detail: "stop"}}
	p.opts.Providers = s.registry
	p.must(t, cli.ExitError, "plan", "--diff", "main..release")
	if len(s.jobs) != 1 {
		t.Fatalf("%d jobs", len(s.jobs))
	}
	prompt := s.jobs[0].Prompt
	for _, want := range []string{"## The change table", "## The difference", "This is the whole difference between the two versions.", "```diff\ndiff --git a/"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, "changes.md") {
		t.Error("the prompt sends the planner to changes.md")
	}
}
