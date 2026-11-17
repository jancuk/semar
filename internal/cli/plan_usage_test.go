package cli_test

import (
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/userconfig"
)

// A plan ends with one line of what it used, summed over its attempts.
func TestPlanPrintsWhatTheModelUsed(t *testing.T) {
	used := provider.Result{Turns: 7, InputTokens: 640_000, CacheReadTokens: 590_000, OutputTokens: 18_000, CostUSD: 1.1}

	t.Run("a plan that is written", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		used := used
		used.Output = []byte(goodDraft)
		s := &spy{res: used}
		p.opts.Providers = s.registry
		res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial")
		contains(t, "stdout", res.stdout, "\nmodel use: 1 attempt, 7 turns, 640k input (590k cached), 18k output, $1.10\n")
		if len(s.jobs) != 1 {
			t.Fatalf("%d jobs", len(s.jobs))
		}
	})

	t.Run("a plan that every attempt rejected", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		used := used
		used.Output = []byte(`{"summary": 7}`)
		s := &spy{res: used}
		p.opts.Providers = s.registry
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--no-trial")
		contains(t, "stderr", res.stderr, "semar: model use: 3 attempts, 21 turns, 1.9M input (1.8M cached), 54k output, $3.30\n")
	})

	t.Run("a plan with the setup loaded", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		used := used
		used.Output = []byte(goodDraft)
		s := &spy{res: used}
		p.opts.Providers = s.registry
		p.yourFile(t, userconfig.File{Version: 1, Default: "claude", Providers: []userconfig.Provider{{ID: "claude", Kind: "claude"}}})
		res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial", "--with-setup")
		contains(t, "stdout", res.stdout, "model use: 1 attempt,", "your MCP servers and skills were loaded, and they add tokens to every turn")
	})
}
