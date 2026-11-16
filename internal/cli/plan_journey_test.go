package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
)

// planJourneyRepo is a plan repository with the checkout journey and one journey file that is not valid.
func planJourneyRepo(t *testing.T) *planRepo {
	t.Helper()
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	text, err := os.ReadFile("../../testdata/e2e-journeys/.semar/journeys/checkout.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p.write(t, ".semar/journeys/checkout.yaml", string(text))
	p.write(t, ".semar/journeys/broken.yaml", strings.Replace(string(text), "id: checkout", "id: broken\nschedule: nightly", 1))
	return p
}

func (p *planRepo) planMarkdown(t *testing.T) string {
	t.Helper()
	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(md)
}

func TestPlanListsTheJourneysToRerunAndSendsNothingForThem(t *testing.T) {
	p := planJourneyRepo(t)
	p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial")

	md := p.planMarkdown(t)
	contains(t, "plan.md", md, "## Journeys to rerun\n\n- checkout (critical, owner team-commerce): leg place calls POST /v1/orders, which SHOP-482-01 and SHOP-483-01 test; "+
		"leg settled calls GET /v1/orders/{id}, which SHOP-483-01 tests. Run: semar journey run checkout --env uat\n")
	if strings.Contains(md, "broken") {
		t.Errorf("plan.md names a journey that is not valid:\n%s", md)
	}
	// The plan reads journey files and sends nothing: the trial is off, so no request left at all.
	if n := p.app.Requests("", ""); n != 0 {
		t.Errorf("%d requests reached the service while planning with --no-trial", n)
	}
}

func TestInvalidJourneyFileOneLine(t *testing.T) {
	p := planJourneyRepo(t)
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial")
	var lines []string
	for _, l := range strings.Split(res.stderr, "\n") {
		if strings.Contains(l, "journey file") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("%d lines about journey files, want one for the one problem:\n%s", len(lines), res.stderr)
	}
	contains(t, "the line", lines[0], "semar: journey file .semar/journeys/broken.yaml:", "unknown key schedule", "; plan.md does not use it")
}

func TestPlanWithNoJourneyFileHasNoSection(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--no-trial")
	if strings.Contains(p.planMarkdown(t), "Journeys to rerun") || strings.Contains(res.stderr, "journey") {
		t.Errorf("a repository without journeys got a section or a line:\n%s", res.stderr)
	}
}
