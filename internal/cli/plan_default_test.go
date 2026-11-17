package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/claude"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/sampleapp"
)

// claudeAsFake makes the built-in claude provider replay the drafts, and records the jobs it ran.
func (p *planRepo) claudeAsFake(t *testing.T, drafts ...string) *[]*fake.Fake {
	t.Helper()
	outputs := p.drafts(t, drafts...)
	var made []*fake.Fake
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(claude.Kind, func(cfg config.Provider) (provider.Instance, error) {
			cfg.Outputs = outputs
			f, err := fake.New(cfg, root)
			made = append(made, f)
			return f, err
		})
		return r
	}
	return &made
}

// withoutProviders drops the providers that configure wrote, so that the built-in claude plans.
func (p *planRepo) withoutProviders(t *testing.T) {
	t.Helper()
	p.rewriteConfig(t, func(s string) string {
		head, tail, _ := strings.Cut(s, "providers:")
		if i := strings.Index(tail, "knowledge:"); i >= 0 {
			return head + tail[i:]
		}
		return head
	})
}

func TestPlanPlansEachCriterionWhenATicketHasCriteria(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitCriterionTicket(t)
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	made := p.recordJobs()
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial")
	contains(t, "stderr", res.stderr, "plans each acceptance criterion", "1 ticket of this change has acceptance criteria")
	contains(t, "stdout", res.stdout, "model use: 1 job")
	if len(*made) != 1 || !(*made)[0].Jobs()[0].NoTools {
		t.Errorf("the plan did not run the criteria pipeline")
	}
}

func TestPlanPlansInOneJobAndSaysWhyWhenNoTicketHasCriteria(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial")
	contains(t, "stderr", res.stderr, "plans the change in one job", "no ticket of this change has acceptance criteria in a ticket file")
}

func TestPlanPipelineSingleStaysAvailable(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitNumberedTicket(t)
	p.configure(t, numberedDraft(true))
	p.withTickets(t)
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--pipeline", "single")
	if strings.Contains(res.stderr, "plans each acceptance criterion") || strings.Contains(res.stderr, "in one job: no ticket") {
		t.Errorf("--pipeline single was not taken as it is:\n%s", res.stderr)
	}
}

func TestPlanWithSetupKeepsTheSinglePipelineAndSaysWhy(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitNumberedTicket(t)
	p.configure(t, numberedDraft(true))
	p.withTickets(t)
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--with-setup")
	contains(t, "stderr", res.stderr, "--with-setup needs the single pipeline")
}

func TestPlanClaudePlansWithHaikuThenSonnetThenItsOwnModel(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitCriterionTicket(t)
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	p.withoutProviders(t)
	made := p.claudeAsFake(t, criterionAnswerJSON)
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial")
	contains(t, "stderr", res.stderr, "on claude (fake) haiku, and when its answer fails the checks twice, on claude (fake) sonnet, then claude (fake) its default model")
	contains(t, "stdout", res.stdout, "model use: 1 job (1 haiku)")
	if len(*made) != 1 || (*made)[0].Jobs()[0].Model != "haiku" {
		t.Errorf("the first job ran on %q, want haiku", (*made)[0].Jobs()[0].Model)
	}
}

func TestPlanClaudeWithAnExplicitModelPlansWithThatModelOnly(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitCriterionTicket(t)
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	p.withoutProviders(t)
	made := p.claudeAsFake(t, criterionAnswerJSON)
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial", "--model", "opus")
	if strings.Contains(res.stderr, "sonnet") {
		t.Errorf("a ladder was built although --model named the model:\n%s", res.stderr)
	}
	if (*made)[0].Jobs()[0].Model != "opus" {
		t.Errorf("the job ran on %q, want opus", (*made)[0].Jobs()[0].Model)
	}
}

func TestPlanLadderOfYourFileReplacesTheDefaultLadder(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitCriterionTicket(t)
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	p.withoutProviders(t)
	made := p.claudeAsFake(t, criterionAnswerJSON)
	home := p.opts.Home
	if err := os.WriteFile(home+"/providers.yaml", []byte("version: 1\ndefault: claude\nplan:\n  ladder: [claude:opus]\nproviders:\n  - id: claude\n    kind: claude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial")
	if strings.Contains(res.stderr, "haiku") || (*made)[0].Jobs()[0].Model != "opus" {
		t.Errorf("the ladder of the file was not used:\n%s", res.stderr)
	}
}
