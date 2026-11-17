package cli_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
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
	for _, want := range []string{"## The change table", "## The difference", "This is the whole difference between the two versions, apart from", "```diff\ndiff --git a/"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, "changes.md") {
		t.Error("the prompt sends the planner to changes.md")
	}
}

// A committed .semar names hosts, and the copy of the source has none of it, so the prompt shows
// none of it either.
func TestPlanPromptLeavesOutTheFilesOfSemar(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.git.Commit("SHOP-482 keep the Semar configuration", map[string]*string{
		".semar/notes.yaml": gittest.S("baseUrl: http://internal-host.example.com\n"),
	})
	p.configure(t, goodDraft)
	made := p.recordJobs()
	p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	prompt := onlyPrompt(t, *made)
	if !strings.Contains(prompt, "## The difference") {
		t.Fatal("the prompt has no difference")
	}
	if strings.Contains(prompt, "internal-host.example.com") {
		t.Error("the prompt holds a host of a file of .semar")
	}
}
