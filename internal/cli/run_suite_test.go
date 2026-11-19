package cli_test

import (
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
)

const featureRel = ".semar/scenarios/SHOP-482/SHOP-482-01.feature"

func (r *repo) tagSuite(t *testing.T, tag string) {
	t.Helper()
	data, err := os.ReadFile(r.path(featureRel))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), " @intent-newline", " "+tag+" @intent-newline", 1)
	if tag == "" {
		text = string(data)
	}
	r.writeRel(t, featureRel, text)
}

func TestASuiteTagAsksForNoNewReviewOfAnApprovedScenario(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	contains(t, "the run before the tag", squash(r.must(t, cli.ExitOK, "run", "--scenario", scenarioID).stdout), scenarioID+" passed")

	r.tagSuite(t, "@suite:smoke")
	res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	contains(t, "the run after the tag", squash(res.stdout), scenarioID+" passed")
	if strings.Contains(res.stdout, "needs review") || strings.Contains(res.stderr, "needs review") {
		t.Errorf("adding @suite:smoke asked for a new review:\n%s\n%s", res.stdout, res.stderr)
	}

	r.writeRel(t, featureRel, strings.Replace(readText(t, r.path(featureRel)), "@suite:smoke", "@suite:regression,smoke", 1))
	res = r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	if strings.Contains(res.stdout, "needs review") {
		t.Errorf("changing the suites asked for a new review:\n%s", res.stdout)
	}
	if out := r.must(t, cli.ExitOK, "review").stdout; !strings.Contains(out, "nothing needs review") {
		t.Errorf("semar review after the tag changed: %s", out)
	}
}

func readText(t *testing.T, p string) string { t.Helper(); return string(mustRead(t, p)) }

func TestRunSuiteRunsThatSuiteWhateverTheChangeIsAndNoJourney(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	r.copyScenario(t, "SHOP-480-01")
	r.tagSuite(t, "@suite:smoke")

	res := r.must(t, cli.ExitOK, "run", "--suite", "smoke")
	out := squash(res.stdout)
	contains(t, "--suite smoke", out, "Change the smoke suite", "Selected 1 scenario\n", scenarioID+" passed")
	if strings.Contains(res.stdout, "SHOP-480-01") || strings.Contains(out, "journey") {
		t.Errorf("--suite ran more than its suite:\n%s", res.stdout)
	}

	// A directory that is not a git repository has no change, and a suite does not need one.
	if strings.Contains(res.stderr, "not in a git repository") {
		t.Errorf("--suite asked for a change:\n%s", res.stderr)
	}
}

func TestRunSuiteIsAnErrorForASuiteThatNoScenarioIsIn(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)

	res := r.semar(t, t.Context(), "run", "--suite", "smoke")
	if res.code == cli.ExitOK || !strings.Contains(res.stderr, `no scenario is in the suite "smoke": no scenario has a @suite: tag`) {
		t.Errorf("a repository with no suite: exit %d\n%s", res.code, res.stderr)
	}
	r.tagSuite(t, "@suite:checkout,regression")
	res = r.semar(t, t.Context(), "run", "--suite", "smoke")
	if res.code == cli.ExitOK || !strings.Contains(res.stderr, `the suites that exist are checkout, regression`) {
		t.Errorf("a suite that does not exist: exit %d\n%s", res.code, res.stderr)
	}
	if n := r.app.Requests("", "/"); n != 0 {
		t.Errorf("%d requests were sent for a suite that does not exist", n)
	}
}

func TestRunSuiteRefusesWhatItCannotBeCombinedWith(t *testing.T) {
	r := newRepo(t, repoOptions{})
	for _, args := range [][]string{
		{"run", "--suite", "smoke", "--all"},
		{"run", "--suite", "smoke", "--scenario", scenarioID},
	} {
		res := r.semar(t, t.Context(), args...)
		if res.code == cli.ExitOK || !strings.Contains(res.stderr, "--suite cannot be combined with --all or --scenario") {
			t.Errorf("%v: exit %d\n%s", args, res.code, res.stderr)
		}
	}
	res := r.semar(t, t.Context(), "run", "--suite", "Smoke Test")
	if res.code == cli.ExitOK || !strings.Contains(res.stderr, "lower-case letters, digits, and - only") {
		t.Errorf("a bad name: exit %d\n%s", res.code, res.stderr)
	}
}

func TestRunRetriesOnlyFromZeroToFive(t *testing.T) {
	r := newRepo(t, repoOptions{})
	for _, n := range []string{"6", "-1"} {
		res := r.semar(t, t.Context(), "run", "--all", "--retries", n)
		if res.code == cli.ExitOK || !strings.Contains(res.stderr, "--retries takes a number from 0 to 5") {
			t.Errorf("--retries %s: exit %d\n%s", n, res.code, res.stderr)
		}
	}
}

// failsTheFirstCreate answers the first order that a run creates with 503.
func failsTheFirstCreate(next http.Handler) http.Handler {
	var n atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost && req.URL.Path == "/v1/orders" && n.Add(1) == 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func TestRunRetriesAFlakyScenarioAndNamesIt(t *testing.T) {
	r := newRepo(t, repoOptions{wrap: failsTheFirstCreate})
	r.addScriptAndConfirm(t)

	res := r.must(t, cli.ExitOK, "run", "--all", "--retries", "2")
	out := squash(res.stdout)
	contains(t, "the run", out, scenarioID+" passed flaky, passed on attempt 2 of 3")
	report := readText(t, r.latestRun(t)+"/report.md")
	contains(t, "report.md", report, "(flaky, passed on attempt 2 of 3)", "Attempt 1: blocked", "Attempt 2: passed", "flaky 1")
	contains(t, "the status", out, "flaky, passed on attempt 2 of 3")

	// The setting of the repository gives the same without the flag.
	r2 := newRepo(t, repoOptions{wrap: failsTheFirstCreate})
	r2.rewriteConfig(t, func(s string) string { return s + "replay:\n  retries: 2\n" })
	r2.addScriptAndConfirm(t)
	contains(t, "replay.retries", squash(r2.must(t, cli.ExitOK, "run", "--all").stdout), "flaky, passed on attempt 2 of 3")

	// With no retries the same service fails the run.
	r3 := newRepo(t, repoOptions{wrap: failsTheFirstCreate})
	r3.addScriptAndConfirm(t)
	r3.must(t, cli.ExitUndecided, "run", "--all")
}

func TestRunNeverSendsARequestOfTheOnceListAgainOnARetry(t *testing.T) {
	r := newRepo(t, repoOptions{app: sampleapp.Options{Defects: map[sampleapp.Defect]bool{sampleapp.RefundOverTotal: true}}})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "      mode: test_data\n", "      mode: test_data\n      once:\n        - {method: POST, path: \"/v1/orders/*/refunds\"}\n", 1)
	})
	r.addScriptAndConfirm(t)

	res := r.must(t, cli.ExitFailed, "run", "--all", "--retries", "3")
	contains(t, "the run", squash(res.stdout), scenarioID+" failed", "(the scenario sent a request that effects.once lists, so it was not run again)")
	if n := r.app.Requests("POST", "/v1/orders/"); n != 1 {
		t.Errorf("the refund of the once list was sent %d times, want 1", n)
	}
}

func TestRunCIAsksNoQuestionOnATerminal(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())
	r.opts.Terminal = func() bool { return true }

	r.opts.Stdin = strings.NewReader("n\n")
	res := r.must(t, cli.ExitUndecided, "run", "--all")
	contains(t, "the run with no --ci", res.stdout, "Review it now? [Y/n]")

	r.opts.Stdin = strings.NewReader("y\ny\ny\n")
	res = r.must(t, cli.ExitUndecided, "run", "--all", "--ci")
	if strings.Contains(res.stdout, "?") || strings.Contains(res.stdout, "[Y/n]") {
		t.Errorf("--ci asked a question:\n%s", res.stdout)
	}
	contains(t, "the run", squash(res.stdout), scenarioID+" needs review", "report: ")
	if got := r.must(t, cli.ExitUndecided, "run", "--all", "--ci").stdout; !strings.Contains(squash(got), scenarioID+" needs review") {
		t.Errorf("--ci approved something:\n%s", got)
	}
}
