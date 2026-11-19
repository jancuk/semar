package cli_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/usage"
)

// dailyRepo is the sample application on a branch that changes one ticket, with a scenario for that
// ticket and one for another ticket that the change does not touch.
func dailyRepo(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t, repoOptions{})
	r.opts.Email = func(string) string { return "ada@example.com" }
	r.copyScenario(t, "SHOP-480-01")
	rel := ".semar/scenarios/SHOP-482/SHOP-480-01.feature"
	data, err := os.ReadFile(r.path(rel))
	if err != nil {
		t.Fatal(err)
	}
	r.writeRel(t, rel, strings.Replace(string(data), "@ticket:SHOP-482", "@ticket:SHOP-480", 1))
	r.writeRel(t, "docs/tickets/SHOP-482.md", ticketFile)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/1", "A refund above the paid amount is refused", "cite: docs/tickets/SHOP-482.md:5", "covers: ["+scenarioID+"]")))
	r.git(t, "init", "--quiet", "--initial-branch=main")
	r.git(t, "add", "--all")
	r.git(t, "commit", "--quiet", "-m", "base")
	r.git(t, "checkout", "--quiet", "-b", "feature/refund-limit")
	r.git(t, "commit", "--quiet", "--allow-empty", "-m", "SHOP-482 limit a refund to the paid amount")
	return r
}

func TestRunRunsTheChangeAndEndsWithTheStatus(t *testing.T) {
	r := dailyRepo(t)
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())

	first := r.must(t, cli.ExitOK, "run")
	out := squash(first.stdout)
	contains(t, "the first run", out,
		"Change feature/refund-limit against main: SHOP-482",
		"Target local (test), orders at 9f3c2ab",
		"Selected 1 scenario",
		scenarioID+" passed",
		"1 use case of SHOP-482", "verified 1", "Ship: yes.")
	if strings.Contains(first.stdout, "SHOP-480-01") {
		t.Errorf("a scenario of another ticket ran:\n%s", first.stdout)
	}
	if strings.Index(first.stdout, "Change ") > strings.Index(first.stdout, scenarioID) {
		t.Errorf("the header does not come before the results:\n%s", first.stdout)
	}

	second := r.must(t, cli.ExitOK, "run")
	contains(t, "the second run", squash(second.stdout), scenarioID+" passed", "verified 1", "Ship: yes.")
	if n := r.app.Requests("", "/"); n == 0 {
		t.Error("the second run sent nothing")
	}
}

func TestRunNamesTheGapsBeforeItSendsAnything(t *testing.T) {
	r := dailyRepo(t)
	r.git(t, "commit", "--quiet", "--allow-empty", "-m", "SHOP-490 show the limit in the receipt")
	r.git(t, "commit", "--quiet", "--allow-empty", "-m", "tidy the logger")
	r.writeRel(t, ".semar/notes.md", "setup\n")
	r.git(t, "add", ".semar/notes.md")
	r.git(t, "commit", "--quiet", "-m", "Set up Semar")
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())
	r.must(t, cli.ExitOK, "review", "--yes")

	r.opts.Terminal = func() bool { return false }
	res := r.must(t, cli.ExitUndecided, "run")
	contains(t, "the header", squash(res.stdout),
		"Change feature/refund-limit against main: SHOP-482, SHOP-490",
		"Gaps SHOP-490 is not planned → semar plan",
		"1 commit has no ticket:", `"tidy the logger"`,
		scenarioID+" passed", "not planned 1 → semar plan\n SHOP-490", "Ship: no.")
	if strings.Contains(res.stdout, "Plan it now") || strings.Contains(res.stderr, "Plan it now") {
		t.Error("run offered to plan with no terminal")
	}
}

func TestRunOffersToPlanOnATerminalAndPlansOnlyOnY(t *testing.T) {
	r := dailyRepo(t)
	r.git(t, "commit", "--quiet", "--allow-empty", "-m", "SHOP-490 show the limit in the receipt")
	r.writeRel(t, "docs/tickets/SHOP-490.md", "# SHOP-490 Show the limit\n\n## Acceptance criteria\n\n1. The receipt shows the limit.\n")
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())
	r.must(t, cli.ExitOK, "review", "--yes")

	r.opts.Home = t.TempDir()
	r.opts.Terminal = func() bool { return true }
	r.opts.Stdin = strings.NewReader("n\n")
	res := r.must(t, cli.ExitUndecided, "run")
	contains(t, "the offer", res.stdout, "SHOP-490 has a ticket file and no use cases. Plan it now with claude with haiku, no estimate yet? [y/N]")

	// Earlier plans by criteria on the model that would plan first give the offer its amount.
	rec := usage.Record{At: time.Now(), Provider: "claude", Kind: "claude", Pipeline: usage.PipelineCriteria, Asked: "haiku", Outcome: "ok", Attempts: 4, CostUSD: 0.08}
	if err := usage.Append(r.opts.Home, rec); err != nil {
		t.Fatal(err)
	}
	r.opts.Stdin = strings.NewReader("n\n")
	res = r.must(t, cli.ExitUndecided, "run")
	contains(t, "the offer with an amount", res.stdout, "Plan it now with claude with haiku, about $0.02 for 1 criterion, from 4 earlier jobs of claude haiku? [y/N]")
	if exists(t, r.path(".semar", "plans")) {
		t.Error("run planned without a y")
	}

	r.opts.Stdin = strings.NewReader("")
	res = r.must(t, cli.ExitUndecided, "run", "--no-plan")
	if strings.Contains(res.stdout, "Plan it now") {
		t.Errorf("--no-plan still offered:\n%s", res.stdout)
	}
	contains(t, "the gap", squash(res.stdout), "SHOP-490 is not planned")
}

func TestRunOfAChangeWithNoTicketSaysSoAndNamesAll(t *testing.T) {
	r := dailyRepo(t)
	r.git(t, "checkout", "--quiet", "main")
	r.git(t, "checkout", "--quiet", "-b", "chore/logger")
	r.git(t, "commit", "--quiet", "--allow-empty", "-m", "tidy the logger")
	res := r.must(t, cli.ExitUndecided, "run")
	contains(t, "stdout", res.stdout, "Change    chore/logger against main: no ticket", "--all")
	if n := r.app.Requests("", "/"); n != 0 {
		t.Errorf("%d requests were sent for a change with no ticket", n)
	}
}

func TestRunWithScenariosOrAllIsNotTheChange(t *testing.T) {
	r := dailyRepo(t)
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())
	r.must(t, cli.ExitOK, "review", "--yes")
	res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	contains(t, "--scenario", res.stdout, "Change    the scenarios named", "Selected  1 scenario\n")
	res = r.semar(t, t.Context(), "run", "--all")
	contains(t, "--all", res.stdout, "Change    every scenario")
	res = r.must(t, cli.ExitError, "run", "--all", "--scenario", scenarioID)
	contains(t, "stderr", res.stderr, "and not both")
	res = r.must(t, cli.ExitError, "run", "local", "--env", "local")
	contains(t, "stderr", res.stderr, "name the environment once")
}

func TestRunOutsideAGitRepositoryNeedsAllOrScenario(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	res := r.must(t, cli.ExitError, "run")
	contains(t, "stderr", res.stderr, "not in a git repository", "--all", "--scenario")
}

func TestRunRunsTheCriticalJourneysAfterTheScenarios(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.requireConfirmation(t)
	r.must(t, cli.ExitOK, "review", "checkout", "--yes")
	res := r.must(t, cli.ExitOK, "run", "--all")
	contains(t, "the run", squash(res.stdout), "Selected 0 scenarios, 1 critical journey", "checkout passed")
	res = r.must(t, cli.ExitUndecided, "run", "--all", "--no-journeys")
	contains(t, "--no-journeys", res.stdout, "no scenario ran")
}

type failingReader struct{ t *testing.T }

func (f failingReader) Read([]byte) (int, error) {
	f.t.Error("run read a line from standard input")
	return 0, os.ErrClosed
}

func TestRunHasNoNoReviewFlag(t *testing.T) {
	r := dailyRepo(t)
	res := r.semar(t, t.Context(), "run", "--no-review")
	if res.code != cli.ExitError {
		t.Fatalf("exit %d, want %d", res.code, cli.ExitError)
	}
	contains(t, "stderr", res.stderr, "unknown flag: --no-review")
}

func TestRunOnATerminalAsksNothingAfterTheTests(t *testing.T) {
	r := dailyRepo(t)
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())
	r.opts.Terminal = func() bool { return true }
	r.opts.Stdin = failingReader{t}
	res := r.semar(t, t.Context(), "run", "--scenario", scenarioID)
	if n := strings.Count(res.stdout, "report:"); n != 1 {
		t.Errorf("%d report lines, want 1:\n%s", n, res.stdout)
	}
	if strings.Contains(res.stdout, "Review") || strings.Contains(res.stdout, "review them") {
		t.Errorf("run offered a review:\n%s", res.stdout)
	}
	runs, err := os.ReadDir(r.path(".semar", "runs"))
	if err != nil || len(runs) != 1 {
		t.Errorf("run directories: %v, %v", len(runs), err)
	}
}
