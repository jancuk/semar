package cli_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

const reviewOffer = "1 test needs your review. Review it now? [Y/n]"

// unreviewedRun is a branch with one scenario whose expected results nobody confirmed, and a terminal
// that answers from stdin.
func unreviewedRun(t *testing.T, stdin string) *repo {
	t.Helper()
	r := dailyRepo(t)
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())
	r.opts.Terminal = func() bool { return true }
	r.opts.Stdin = strings.NewReader(stdin)
	return r
}

func TestRunOffersTheReviewRunsTheApprovedAgainAndEndsWithTheStatus(t *testing.T) {
	r := unreviewedRun(t, "y\ny\n")
	res := r.must(t, cli.ExitOK, "run")
	out := res.stdout

	offer, approved, status := strings.Index(out, reviewOffer), strings.Index(out, "approved "+scenarioID), strings.Index(out, "Ship:")
	passed := strings.LastIndex(squash(out), scenarioID+" passed")
	if offer < 0 || approved < offer || status < approved || passed < 0 {
		t.Fatalf("the offer, the approval, the second run, and the status are not in that order:\n%s", out)
	}
	if strings.Index(squash(out), scenarioID+" needs review") > offer {
		t.Errorf("the first results do not come before the offer:\n%s", out)
	}
	contains(t, "the status", squash(out[status:]), "Ship: yes.")
	if strings.Count(out, "report: ") != 2 {
		t.Errorf("want one report line for each of the two runs:\n%s", out)
	}
	if n := len(r.runDirs(t)); n != 2 {
		t.Errorf("want two runs in the store, got %d", n)
	}
}

func TestRunTakesEnterAsYesToTheReviewOffer(t *testing.T) {
	r := unreviewedRun(t, "\ny\n")
	res := r.must(t, cli.ExitOK, "run")
	contains(t, "the run", squash(res.stdout), reviewOffer, "approved "+scenarioID, scenarioID+" passed", "Ship: yes.")
}

func TestRunAfterNoToTheReviewOfferKeepsTheNextStep(t *testing.T) {
	for _, stdin := range []string{"n\n", ""} {
		r := unreviewedRun(t, stdin)
		res := r.must(t, cli.ExitUndecided, "run")
		contains(t, "the run", squash(res.stdout), reviewOffer,
			scenarioID+" needs review", "not confirmed → semar review "+scenarioID,
			"needs review 1 → semar review", "Ship: no. 1 open.")
		if strings.Contains(res.stdout, "approved ") {
			t.Errorf("a declined review approved something:\n%s", res.stdout)
		}
		if n := len(r.runDirs(t)); n != 1 {
			t.Errorf("stdin %q: want one run, got %d", stdin, n)
		}
	}
}

func TestRunWithNoReviewNeverOffersIt(t *testing.T) {
	r := unreviewedRun(t, "y\ny\n")
	res := r.must(t, cli.ExitUndecided, "run", "--no-review")
	if strings.Contains(res.stdout, "Review it now") {
		t.Errorf("--no-review still offered the review:\n%s", res.stdout)
	}
	contains(t, "the run", squash(res.stdout), "not confirmed → semar review "+scenarioID)
}

func TestRunWithNoTerminalNeverOffersTheReview(t *testing.T) {
	r := unreviewedRun(t, "y\ny\n")
	r.opts.Terminal = func() bool { return false }
	res := r.must(t, cli.ExitUndecided, "run")
	if strings.Contains(res.stdout, "Review it now") || strings.Contains(res.stdout, "approved ") {
		t.Errorf("a run with no terminal reviewed:\n%s", res.stdout)
	}
	contains(t, "the run", squash(res.stdout), "not confirmed → semar review "+scenarioID)
}

func TestRunWithNothingToReviewDoesNotOffer(t *testing.T) {
	r := unreviewedRun(t, "y\n")
	r.must(t, cli.ExitOK, "review", "--yes")
	r.opts.Stdin = strings.NewReader("y\n")
	res := r.must(t, cli.ExitOK, "run")
	if strings.Contains(res.stdout, "Review it now") {
		t.Errorf("the run offered a review with nothing to review:\n%s", res.stdout)
	}
	if n := len(r.runDirs(t)); n != 1 {
		t.Errorf("want one run, got %d", n)
	}
}

func TestRunRunsNothingAgainWhenThePersonApprovesNothing(t *testing.T) {
	r := unreviewedRun(t, "y\nn\n")
	res := r.must(t, cli.ExitUndecided, "run")
	contains(t, "the run", squash(res.stdout), reviewOffer, "approved 0 of 1 item", "Ship: no. 1 open.")
	if n := len(r.runDirs(t)); n != 1 {
		t.Errorf("a review that approved nothing ran again: %d runs", n)
	}
}

func TestRunOffersTheReviewForAScenarioNamedWithScenario(t *testing.T) {
	r := unreviewedRun(t, "y\ny\n")
	res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	contains(t, "the run", squash(res.stdout), reviewOffer, "approved "+scenarioID, scenarioID+" passed")
	if n := len(r.runDirs(t)); n != 2 {
		t.Errorf("want two runs, got %d", n)
	}
}

func TestRunReviewsTheScreensFileOfAWebScenario(t *testing.T) {
	r, _ := recordRepo(t)
	ctx := t.Context()
	r.record(t, ctx, cli.ExitOK, refundFlow(t), "SHOP-482-07", "--open", "/refund", "--service", "orders")

	r.opts.Terminal = func() bool { return true }
	r.opts.Stdin = strings.NewReader("y\ny\ny\n")
	res := r.run(t, ctx, cli.ExitOK, "--scenario", "SHOP-482-07")
	contains(t, "the run", squash(res.stdout), "1 test needs your review. Review it now? [Y/n]",
		"the screens of orders", "approved the screens of orders", "SHOP-482-07 passed")
}

func TestRunOffersOneReviewForSeveralTestsAndTheExitCodeIsThatOfTheSecondRun(t *testing.T) {
	r := unreviewedRun(t, "\ny\ny\n")
	// SHOP-480-01 has no script, so it is blocked again after its review, and the exit code says so.
	res := r.must(t, cli.ExitUndecided, "run", "--all", "--no-journeys")
	contains(t, "the run", squash(res.stdout), "2 tests need your review. Review them now? [Y/n]", "approved 2 of 2 items",
		scenarioID+" passed", "SHOP-480-01 blocked no script was accepted")
}
