package cli_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
)

const (
	scenarioObligation = "SHOP-482/scenario-SHOP-482-01"
	gapObligation      = "SHOP-482/not-drafted-1"
	manualObligation   = "SHOP-482/difference-1"
	handObligation     = "refunds/refund-owner"
)

// ledgerE2ERepo is the sample application with the two obligations files of the plan: one entry
// that the scenario covers, one gap, one manual check, and a hand-written entry with no cover.
func ledgerE2ERepo(t *testing.T, app sampleapp.Options) *repo {
	t.Helper()
	r := newRepo(t, repoOptions{app: app})
	r.addScriptAndConfirm(t)
	r.ledgerFile(t, obligations("SHOP-482",
		entry(scenarioObligation, "Refund above the order total is rejected", "covers: ["+scenarioID+"]"),
		entry(gapObligation, "A refund of a paid order by another customer", `gap: "not drafted: needs fixture order.paid"`),
		entry(manualObligation, "The document and the code differ", manualEntryFields)))
	r.writeRel(t, ".semar/obligations/refunds.yaml", obligations("",
		entry(handObligation, "A customer cannot refund another customer's order", "ticket: SHOP-482")))
	return r
}

func TestLedgerEndToEnd(t *testing.T) {
	r := ledgerE2ERepo(t, sampleapp.Options{})

	res := r.must(t, cli.ExitOK, "run", "--all")
	const line = "4 use cases: 1 verified, 2 blocked, 1 needs a person"
	md := strings.Split(r.lastReport(t), "\n")
	if md[0] != "# Semar report r-test-0001" || md[1] != "" || md[2] != line || md[3] != "Details: `semar status --ticket SHOP-482`" {
		t.Errorf("the report does not open with the summary line:\n%s", strings.Join(md[:5], "\n"))
	}
	contains(t, "the run summary", res.stdout, line, scenarioID+"  passed")
	counts := strings.Index(res.stdout, "failed 0, blocked 0")
	if counts < 0 || strings.Index(res.stdout, line) < counts {
		t.Errorf("the line is not after the counts:\n%s", res.stdout)
	}

	// The hand-written entry has no cover and no reason, so it comes first among the use cases with no test.
	status := r.must(t, cli.ExitUndecided, "status")
	out := squash(status.stdout)
	contains(t, "the status", out, "4 use cases", "verified 1", "for a person 1 "+manualObligation, "no test 2 "+handObligation+" use case with no test and no reason",
		gapObligation+" not drafted: needs fixture order.paid")
	if first := strings.Index(out, handObligation); first > strings.Index(out, gapObligation) {
		t.Errorf("%s does not come first:\n%s", handObligation, status.stdout)
	}
	// The builds were read with no argument too, because there is one environment.
	r.must(t, cli.ExitUndecided, "status", "--env", "local")

	// A failed manual result puts the use case under failing and makes the exit 1.
	rec := r.must(t, cli.ExitOK, "check", manualObligation, "fail", "--note", "x")
	contains(t, "the record", rec.stdout, "checked by a person", "local, unverified identity")
	failed := r.must(t, cli.ExitFailed, "status")
	contains(t, "the status", squash(failed.stdout), "verified 1", "failing 1 "+manualObligation, "failed, checked by a person", "Ship: no. 1 failing, 2 open.")

	// A deferral turns the hand-written entry into deferred.
	r.must(t, cli.ExitOK, "defer", handObligation, "--reason", "r", "--until", inDays(7))
	deferred := r.must(t, cli.ExitFailed, "status")
	contains(t, "the status", squash(deferred.stdout), "no test 1 "+gapObligation, "deferred 1 "+handObligation+" deferred by tester until "+inDays(7)+": r")
	if strings.Contains(deferred.stdout, "use case with no test and no reason") {
		t.Errorf("a deferred use case is still listed with no reason:\n%s", deferred.stdout)
	}

	// The checklist names the manual entry and the command that records its result.
	list := r.must(t, cli.ExitFailed, "status", "--checklist")
	contains(t, "the checklist", list.stdout, "Manual checks for SHOP-482 (1)", "1. "+manualObligation+"  The document and the code differ",
		"Steps (written by code):", "Check:    semar check "+manualObligation+` pass|fail --note "<what you saw>"`)

	r.assertNoSecret(t, []string{token})
}

func TestLedgerEndToEndFailedScenario(t *testing.T) {
	r := ledgerE2ERepo(t, sampleapp.Options{Defects: defects(sampleapp.RefundOverTotal)})

	r.must(t, cli.ExitFailed, "run", "--all")
	md := strings.Split(r.lastReport(t), "\n")
	if md[2] != "4 use cases: 0 verified, 1 failed, 2 blocked, 1 needs a person" {
		t.Errorf("the report line is %q", md[2])
	}

	res := r.must(t, cli.ExitFailed, "status")
	contains(t, "the ledger", squash(res.stdout), "failing 1", scenarioObligation, "SHOP-482-01 failed in r-test-0001")

	// A deferral does not hide a failure.
	r.must(t, cli.ExitOK, "defer", scenarioObligation, "--reason", "known defect", "--until", inDays(7))
	res = r.must(t, cli.ExitFailed, "status")
	contains(t, "the ledger", squash(res.stdout), "failing 1", "SHOP-482-01 failed in r-test-0001", "deferred by tester until "+inDays(7)+": known defect")
	if strings.Contains(squash(res.stdout), " deferred 1 ") {
		t.Errorf("a failed use case is listed as deferred:\n%s", res.stdout)
	}
}

// A person's pass on the builds that run now lets a release gate go green, and a new build takes
// that back (Plan 21, D16).
func TestLedgerEndToEndManualResultOnTheBuild(t *testing.T) {
	r := ledgerE2ERepo(t, sampleapp.Options{})
	r.must(t, cli.ExitOK, "run", "--all")
	r.must(t, cli.ExitOK, "defer", gapObligation, "--reason", "r", "--until", inDays(7))
	r.must(t, cli.ExitOK, "defer", handObligation, "--reason", "r", "--until", inDays(7))
	r.must(t, cli.ExitUndecided, "status", "--env", "local")

	r.must(t, cli.ExitOK, "check", manualObligation, "pass", "--note", "looks right", "--env", "local")
	res := r.must(t, cli.ExitOK, "status", "--env", "local")
	contains(t, "the ledger", squash(res.stdout), "verified 2 "+manualObligation+" checked by tester", "deferred 2", "Ship: yes. 2 deferred.")
	r.must(t, cli.ExitOK, "status")
}
