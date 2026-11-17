package cli_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/claude"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

// costing adds a cost to every job of a provider, which the fake provider never reports.
type costing struct {
	provider.Instance
	cost float64
}

func (c costing) Run(ctx context.Context, job provider.Job) (provider.Result, error) {
	r, err := c.Instance.Run(ctx, job)
	r.CostUSD += c.cost
	return r, err
}

func (p *planRepo) costing(cost float64) {
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		var n atomic.Int64
		factory := func(cfg config.Provider) (provider.Instance, error) {
			f := fake.NewFunc(cfg.ID, func(provider.Job) (provider.Result, error) {
				out := strings.Replace(criterionAnswerJSON, "is refused", fmt.Sprintf("is refused, case %d", n.Add(1)), 1)
				return provider.Result{Output: []byte(out), Model: "fake", Turns: 1, InputTokens: 10}, nil
			})
			return costing{f, cost}, nil
		}
		r.Register(fake.Kind, factory)
		r.Register(claude.Kind, factory)
		return r
	}
}

func (p *planRepo) commitManyCriteria(t *testing.T, n int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("# SHOP-482 Refund above the total\n\n## Acceptance criteria\n\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%d. A refund of %d above the rest of the total is refused with 422.\n", i, i)
	}
	p.git.Commit("SHOP-482 write the ticket and the API", map[string]*string{
		"docs/tickets/SHOP-482.md": gittest.S(b.String()),
		"docs/openapi.yaml":        gittest.S(criterionAPI),
	})
	p.configure(t, goodDraft)
	p.withCriterionKnowledge(t)
}

func TestPlanStopsStartingJobsAtTheCostLimitAndExitsWith2(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitManyCriteria(t, 12)
	p.costing(0.01)
	res := p.must(t, cli.ExitUndecided, "plan", "--diff", "main..release", "--no-trial", "--max-cost", "0.02")
	contains(t, "stderr", res.stderr, "semar: the cost limit of $0.02 was reached", "were not planned")
	contains(t, "stdout", res.stdout, "criteria not planned: the cost limit of $0.02 was reached:")
	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "plan.md", string(md), "not planned: the cost limit of \\$0.02 was reached", "| not planned |")
}

func TestPlanLimitOfYourFileBindsAndTheFlagReplacesIt(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitManyCriteria(t, 12)
	p.costing(0.01)
	if err := os.WriteFile(p.opts.Home+"/providers.yaml", []byte("version: 1\ndefault: planner\nplan:\n  maxCost: 0.02\nproviders:\n  - id: planner\n    kind: claude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// planner is declared in semar.yaml as the fake; the file's default names it too.
	res := p.must(t, cli.ExitUndecided, "plan", "--diff", "main..release", "--no-trial")
	contains(t, "stderr", res.stderr, "the cost limit of $0.02 was reached")
	p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial", "--max-cost", "5", "--pipeline", "criteria")
}

func TestPlanRefusesABadMaxCost(t *testing.T) {
	for _, v := range []string{"0", "-1", "abc"} {
		p := newPlanRepo(t, sampleapp.Options{})
		p.commitManyCriteria(t, 2)
		p.costing(0.01)
		res := p.semar(t, t.Context(), "plan", "--diff", "main..release", "--max-cost", v)
		if res.code != cli.ExitError || !strings.Contains(res.stderr, "max-cost") {
			t.Errorf("--max-cost %s: exit %d\n%s", v, res.code, res.stderr)
		}
	}
}

func TestARepositoryCannotSetTheCostLimit(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitManyCriteria(t, 2)
	p.costing(0.01)
	p.rewriteConfig(t, func(s string) string { return s + "plan:\n  maxCost: 100\n" })
	res := p.semar(t, t.Context(), "plan", "--diff", "main..release", "--no-trial")
	if res.code == cli.ExitOK || !strings.Contains(res.stderr, "maxCost") {
		t.Errorf("exit %d, want a refusal that names maxCost\n%s", res.code, res.stderr)
	}
}

func TestPlanSingleStopsBetweenAttemptsAtTheCostLimit(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, badDraft, badDraft, badDraft)
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(fake.Kind, func(cfg config.Provider) (provider.Instance, error) {
			f, err := fake.New(cfg, root)
			return costing{f, 1}, err
		})
		return r
	}
	res := p.must(t, cli.ExitUndecided, "plan", "--diff", "main..release", "--pipeline", "single", "--max-cost", "1.5")
	contains(t, "stderr", res.stderr, "the cost limit of $1.50 was reached after 2 attempts, which spent $2.00, and no draft passed; nothing was written")
	p.assertNothingPlanned(t)
}
