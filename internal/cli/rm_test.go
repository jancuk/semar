package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
)

func rmRepo(t *testing.T) *repo {
	t.Helper()
	r := ledgerRepo(t)
	r.copyScenario(t, "SHOP-482-02")
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())
	r.putRun(t, "r-old-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/a", "Only the first test", "covers: ["+scenarioID+"]"),
		entry("SHOP-482/b", "Both tests", "covers: ["+scenarioID+", SHOP-482-02]")))
	return r
}

func TestRmRetiresAScenarioAndTheUseCaseSaysSo(t *testing.T) {
	r := rmRepo(t)
	res := r.must(t, cli.ExitOK, "rm", scenarioID, "--reason", "the refund rule moved to SHOP-490")
	contains(t, "stdout", res.stdout, "retired scenario "+scenarioID, "SHOP-482/a", "SHOP-482/b")

	if exists(t, r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature")) {
		t.Error("the scenario file is still there")
	}
	if exists(t, r.path(".semar", "store", "scripts", scenarioID)) {
		t.Error("the scripts of the scenario are still there")
	}
	if !exists(t, r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-02.feature")) {
		t.Error("another scenario went too")
	}
	if !exists(t, r.path(".semar", "runs", "r-old-1")) {
		t.Error("a stored run was removed")
	}

	st := r.must(t, cli.ExitUndecided, "status")
	out := squash(st.stdout)
	contains(t, "the status", out,
		"no test 1 → semar plan\n SHOP-482/a", scenarioID+" was retired by tester on 2026-11-14: the refund rule moved to SHOP-490",
		"blocked 1 → semar run\n SHOP-482/b", "SHOP-482-02 has no result: run it",
		"Ship: no.")
	if strings.Contains(out, "verified") {
		t.Errorf("a use case whose test was retired reads as done:\n%s", st.stdout)
	}
	data, err := os.ReadFile(r.path(".semar", "obligations", "SHOP-482.plan.yaml"))
	if err != nil || strings.Contains(string(data), "covers: ["+scenarioID) {
		t.Errorf("the scenario is still in covers: %v\n%s", err, data)
	}
	// The scenario is not there any more, so a second rm says so and changes nothing.
	again := r.must(t, cli.ExitError, "rm", scenarioID, "--reason", "again")
	contains(t, "stderr", again.stderr, scenarioID, "was not found")
}

func TestRmSaysWhatTheIDIsWhenItIsNotAScenario(t *testing.T) {
	r := rmRepo(t)
	r.writeFixture(t, "order.paid.yaml", orderPaidFixture)
	for id, want := range map[string]string{
		"SHOP-482/a": "is a use case, not a scenario",
		"SHOP-482":   "is a ticket, not a scenario",
		"order.paid": "is a fixture, not a scenario",
		"nowhere":    "was not found",
	} {
		args := []string{"rm", id, "--reason", "x"}
		res := r.must(t, cli.ExitError, args...)
		contains(t, "stderr for "+id, res.stderr, want)
	}
	if !exists(t, r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature")) {
		t.Error("a refused rm removed a scenario")
	}
}

func TestRmNeedsAReasonAndAName(t *testing.T) {
	r := rmRepo(t)
	res := r.must(t, cli.ExitError, "rm", scenarioID)
	contains(t, "stderr", res.stderr, "--reason is required")
	res = r.must(t, cli.ExitError, "rm", scenarioID, "--reason", "a\nb")
	contains(t, "stderr", res.stderr, "one line")
	r.opts.User = func() (string, error) { return " ", nil }
	res = r.must(t, cli.ExitError, "rm", scenarioID, "--reason", "x")
	contains(t, "stderr", res.stderr, "no name")
	if !exists(t, r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature")) {
		t.Error("a refused rm removed the scenario")
	}
}

func TestRmOfAScenarioNoUseCaseCoversStillRemovesIt(t *testing.T) {
	r := rmRepo(t)
	res := r.must(t, cli.ExitOK, "rm", "SHOP-482-02", "--reason", "a duplicate")
	contains(t, "stdout", res.stdout, "retired scenario SHOP-482-02", "SHOP-482/b")
	r.copyScenario(t, "SHOP-482-03")
	res = r.must(t, cli.ExitOK, "rm", "SHOP-482-03", "--reason", "never used")
	contains(t, "stdout", res.stdout, "no use case covered it")
}
