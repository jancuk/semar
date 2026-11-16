package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/sampleapp"
)

// today is the day of the clock that newRepo gives a test.
const today = "2026-11-14"

func inDays(n int) string {
	d, err := time.Parse("2006-01-02", today)
	if err != nil {
		panic(err)
	}
	return d.AddDate(0, 0, n).Format("2006-01-02")
}

// ledgerRepo is a repository that nothing serves, with the scenario SHOP-482-01.
func ledgerRepo(t *testing.T) *repo {
	t.Helper()
	return newRepo(t, repoOptions{offline: true})
}

func (r *repo) writeRel(t *testing.T, rel, text string) {
	t.Helper()
	p := r.path(filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// copyScenario adds a scenario that has the file of SHOP-482-01 under another ID.
func (r *repo) copyScenario(t *testing.T, id string) {
	t.Helper()
	data, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", scenarioID+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	r.writeRel(t, ".semar/scenarios/SHOP-482/"+id+".yaml", strings.Replace(string(data), "id: "+scenarioID, "id: "+id, 1))
}

func scenarioResult(id string, outcome domain.Outcome, reason string) domain.ScenarioResult {
	res := domain.ScenarioResult{Scenario: domain.ScenarioID(id), Outcome: outcome}
	if reason != "" {
		res.Reasons = []domain.Reason{{Rule: outcome, Text: reason}}
	}
	return res
}

// putRun stores a run as run.json, or without it when it is not complete.
func (r *repo) putRun(t *testing.T, id, purpose string, builds []domain.BuildRef, complete bool, results ...domain.ScenarioResult) {
	t.Helper()
	store, err := evidence.Open(r.path(".semar"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.NewRun(domain.RunID(id))
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		return
	}
	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	if err := run.Close(domain.Run{ID: domain.RunID(id), Environment: "local", Purpose: purpose, StartedAt: at, EndedAt: at.Add(time.Minute),
		BuildsStart: builds, BuildsEnd: builds, Results: results}); err != nil {
		t.Fatal(err)
	}
}

func obligations(ticket string, entries ...string) string {
	head := "apiVersion: semar/v1\nkind: Obligations\n"
	if ticket != "" {
		head += "ticket: " + ticket + "\n"
	}
	return head + "obligations:\n" + strings.Join(entries, "")
}

func entry(id, title string, more ...string) string {
	s := fmt.Sprintf("  - id: %s\n    title: %s\n    source: diff\n", id, title)
	for _, m := range more {
		s += "    " + m + "\n"
	}
	return s
}

const manualEntryFields = `manual:
      kind: judgement
      text: A person decides
      steps: ["Read the document.", "Read the code."]
      expected: They agree
      writtenBy: code`

func (r *repo) ledgerFile(t *testing.T, text string) {
	t.Helper()
	r.writeRel(t, ".semar/obligations/SHOP-482.plan.yaml", text)
}

func TestLedgerGroupsAndExitCodes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, r *repo)
		code  int
		line  string // the first line of stdout
	}{
		{"every use case verified or deferred is 0", func(t *testing.T, r *repo) {
			r.putRun(t, "r-old-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
			r.ledgerFile(t, obligations("SHOP-482",
				entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]"),
				entry("SHOP-482/b", "Left for later", `gap: "not drafted: needs a fixture"`)))
			r.must(t, cli.ExitOK, "ledger", "defer", "SHOP-482/b", "--reason", "waiting for a fixture", "--until", inDays(7))
		}, cli.ExitOK, "2 use cases: 1 verified, 1 deferred"},
		{"a failed scenario is 1", func(t *testing.T, r *repo) {
			r.putRun(t, "r-old-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomeFailed, "refund-too-much: expected 422, got 201"))
			r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
		}, cli.ExitFailed, "1 use case: 0 verified, 1 failed"},
		{"a failed manual result is 1", func(t *testing.T, r *repo) {
			r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "By hand", manualEntryFields)))
			r.must(t, cli.ExitOK, "ledger", "record", "SHOP-482/a", "failed", "--notes", "no")
		}, cli.ExitFailed, "1 use case: 0 verified, 1 needs a person. Checked by a person: 1 failed."},
		{"a blocked use case is 2", func(t *testing.T, r *repo) {
			r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Never run", "covers: ["+scenarioID+"]")))
		}, cli.ExitUndecided, "1 use case: 0 verified, 1 blocked"},
		{"a use case that needs a person is 2, even when the person passed it", func(t *testing.T, r *repo) {
			r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "By hand", manualEntryFields)))
			r.must(t, cli.ExitOK, "ledger", "record", "SHOP-482/a", "passed", "--notes", "ok")
		}, cli.ExitUndecided, "1 use case: 0 verified, 1 needs a person. Checked by a person: 1 passed."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ledgerRepo(t)
			c.setup(t, r)
			res := r.must(t, c.code, "ledger")
			if first, _, _ := strings.Cut(res.stdout, "\n"); first != c.line {
				t.Errorf("the first line is %q, want %q", first, c.line)
			}
		})
	}
}

func TestLedgerGroupsInOrder(t *testing.T) {
	r := ledgerRepo(t)
	for _, id := range []string{"SHOP-482-02", "SHOP-482-03", "SHOP-482-04"} {
		r.copyScenario(t, id)
	}
	r.putRun(t, "r-old-1", "", nil, true,
		scenarioResult(scenarioID, domain.OutcomePassed, ""),
		scenarioResult("SHOP-482-02", domain.OutcomeFailed, "refund-too-much: expected 422, got 201"),
		scenarioResult("SHOP-482-03", domain.OutcomeInconclusive, "an expected result is not confirmed"))
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/verified", "V", "covers: ["+scenarioID+"]"),
		entry("SHOP-482/deferred", "D", `gap: "not drafted"`),
		entry("SHOP-482/person", "P", manualEntryFields),
		entry("SHOP-482/blocked", "B", "covers: [SHOP-482-03]"),
		entry("SHOP-482/failed", "F", "covers: [SHOP-482-02]"),
		entry("SHOP-482/nothing", "N")))
	r.must(t, cli.ExitOK, "ledger", "defer", "SHOP-482/deferred", "--reason", "later", "--until", inDays(3))

	res := r.must(t, cli.ExitFailed, "ledger")
	last := -1
	for _, h := range []string{"No test and no reason (1)", "Failed (1)", "Blocked (1)", "Needs a person (1)", "Deferred (1)", "Verified (1)"} {
		i := strings.Index(res.stdout, h)
		if i < 0 || i < last {
			t.Fatalf("the group %q is missing or out of order:\n%s", h, res.stdout)
		}
		last = i
	}
	contains(t, "the table", res.stdout,
		"6 use cases: 1 verified, 1 failed, 2 blocked, 1 needs a person, 1 deferred",
		"SHOP-482/nothing", "use case with no test and no reason",
		"SHOP-482-02 failed in r-old-1: refund-too-much: expected 422, got 201",
		"SHOP-482-03 is inconclusive in r-old-1: an expected result is not confirmed",
		"A person decides",
		"deferred by tester until "+inDays(3)+": later")
}

func TestLedgerWithoutEnvSaysSo(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Gap", `gap: "not drafted"`)))
	const note = "semar: the builds that the environment runs now were not read; pass --env to count only results on the current builds\n"
	res := r.must(t, cli.ExitUndecided, "ledger")
	if res.stderr != note {
		t.Errorf("stderr = %q, want %q", res.stderr, note)
	}
	if strings.Contains(res.stdout, "builds") {
		t.Errorf("the note is on standard output:\n%s", res.stdout)
	}
}

func TestLedgerEnvMarksStale(t *testing.T) {
	r := newRepo(t, repoOptions{app: sampleapp.Options{Commit: "c0ffee1"}})
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
	r.putRun(t, "r-old-1", "", []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab"}}, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))

	// With no environment, the old build counts, and the use case is verified.
	r.must(t, cli.ExitOK, "ledger")

	res := r.must(t, cli.ExitUndecided, "ledger", "--env", "local")
	contains(t, "the table", res.stdout,
		"1 use case: 0 verified, 1 blocked",
		"SHOP-482-01 ran on orders 9f3c2ab, and the environment runs c0ffee1: rerun on the current build")
	if strings.Contains(res.stderr, "were not read") {
		t.Errorf("the note about unread builds is printed although --env was given:\n%s", res.stderr)
	}

	// A run on the build that the environment runs now counts again.
	r.putRun(t, "r-z-new", "", []domain.BuildRef{{Service: "orders", Commit: "c0ffee1"}}, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	res = r.must(t, cli.ExitOK, "ledger", "--env", "local")
	contains(t, "the table", res.stdout, "1 use case: 1 verified")
}

func TestLedgerEnvThatCannotBeReadIsAnError(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
	res := r.must(t, cli.ExitError, "ledger", "--env", "local")
	contains(t, "stderr", res.stderr, "the build of orders could not be read", "cannot be matched to the current build")
	if res.stdout != "" {
		t.Errorf("a ledger was printed without the builds:\n%s", res.stdout)
	}
}

func TestLedgerBuildNeedsEnv(t *testing.T) {
	r := ledgerRepo(t)
	res := r.must(t, cli.ExitError, "ledger", "--build", "abcdef1")
	contains(t, "stderr", res.stderr, "--build needs --env")
}

func TestDeferValidation(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Gap", `gap: "not drafted"`)))
	long := strings.Repeat("r", 301)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"a past date", []string{"SHOP-482/a", "--reason", "r", "--until", inDays(-1)}, "must be after today"},
		{"today", []string{"SHOP-482/a", "--reason", "r", "--until", today}, "must be after today"},
		{"181 days", []string{"SHOP-482/a", "--reason", "r", "--until", inDays(181)}, "at most 180 days"},
		{"not a date", []string{"SHOP-482/a", "--reason", "r", "--until", "next week"}, "YYYY-MM-DD"},
		{"no expiry", []string{"SHOP-482/a", "--reason", "r"}, "--until"},
		{"an empty reason", []string{"SHOP-482/a", "--reason", " ", "--until", inDays(5)}, "reason is required"},
		{"no reason", []string{"SHOP-482/a", "--until", inDays(5)}, "--reason"},
		{"a long reason", []string{"SHOP-482/a", "--reason", long, "--until", inDays(5)}, "longer than 300"},
		{"a reason of two lines", []string{"SHOP-482/a", "--reason", "a\nb", "--until", inDays(5)}, "one line"},
		{"an unknown ID", []string{"SHOP-482/zzz", "--reason", "r", "--until", inDays(5)}, "SHOP-482/zzz was not found"},
		{"an ID that is not safe", []string{"../x", "--reason", "r", "--until", inDays(5)}, "was not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := r.must(t, cli.ExitError, append([]string{"ledger", "defer"}, c.args...)...)
			contains(t, "stderr", res.stderr, c.want)
			if exists(t, r.path(".semar", "store", "obligations")) {
				t.Error("a refused deferral left a record")
			}
		})
	}

	// 180 days is the longest.
	res := r.must(t, cli.ExitOK, "ledger", "defer", "SHOP-482/a", "--reason", "waiting", "--until", inDays(180))
	contains(t, "stdout", res.stdout, "deferred SHOP-482/a until "+inDays(180)+" as tester", "local, unverified identity")
}

func TestDeferNeedsAName(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Gap", `gap: "not drafted"`)))
	r.opts.User = func() (string, error) { return "  ", nil }
	res := r.must(t, cli.ExitError, "ledger", "defer", "SHOP-482/a", "--reason", "r", "--until", inDays(5))
	contains(t, "stderr", res.stderr, "no name")
	r.opts.User = func() (string, error) { return "", fmt.Errorf("no user") }
	r.must(t, cli.ExitError, "ledger", "defer", "SHOP-482/a", "--reason", "r", "--until", inDays(5))
	if exists(t, r.path(".semar", "store", "obligations")) {
		t.Error("a deferral without a name left a record")
	}
}

func TestRecordNeedsAName(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "By hand", manualEntryFields)))
	r.opts.User = func() (string, error) { return "", nil }
	res := r.must(t, cli.ExitError, "ledger", "record", "SHOP-482/a", "passed", "--notes", "x")
	contains(t, "stderr", res.stderr, "no name")
	if exists(t, r.path(".semar", "store", "obligations")) {
		t.Error("a result without a name left a record")
	}
}

func TestRecordOnlyForNeedsAPerson(t *testing.T) {
	r := ledgerRepo(t)
	r.putRun(t, "r-old-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/verified", "V", "covers: ["+scenarioID+"]"),
		entry("SHOP-482/blocked", "B", `gap: "not drafted"`),
		entry("SHOP-482/person", "P", manualEntryFields)))

	for _, id := range []string{"SHOP-482/verified", "SHOP-482/blocked"} {
		res := r.must(t, cli.ExitError, "ledger", "record", id, "passed", "--notes", "x")
		contains(t, "stderr", res.stderr, id, "needs_a_person")
	}
	res := r.must(t, cli.ExitError, "ledger", "record", "SHOP-482/none", "passed", "--notes", "x")
	contains(t, "stderr", res.stderr, "SHOP-482/none was not found")
	res = r.must(t, cli.ExitError, "ledger", "record", "SHOP-482/person", "maybe", "--notes", "x")
	contains(t, "stderr", res.stderr, "passed or failed")
	if exists(t, r.path(".semar", "store", "obligations")) {
		t.Error("a refused result left a record")
	}

	res = r.must(t, cli.ExitOK, "ledger", "record", "SHOP-482/person", "passed", "--notes", "the document is right")
	contains(t, "stdout", res.stdout, "recorded passed for SHOP-482/person as tester", "checked by a person")
}

func TestRecordIsANewRecordAndKeepsTheState(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/person", "P", manualEntryFields)))

	r.must(t, cli.ExitOK, "ledger", "record", "SHOP-482/person", "failed", "--notes", "no")
	res := r.must(t, cli.ExitFailed, "ledger")
	contains(t, "the table", res.stdout, "Needs a person (1)", "A person decides", "failed, checked by a person", "Checked by a person: 1 failed.")

	r.opts.Clock = func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }
	r.must(t, cli.ExitOK, "ledger", "record", "SHOP-482/person", "passed", "--notes", "yes, after a fix")
	res = r.must(t, cli.ExitUndecided, "ledger")
	contains(t, "the table", res.stdout, "passed, checked by a person", "Checked by a person: 1 passed.")
	if strings.Contains(res.stdout, "1 verified") && !strings.Contains(res.stdout, "0 verified") {
		t.Errorf("a manual result counted as a pass:\n%s", res.stdout)
	}
	files, err := filepath.Glob(r.path(".semar", "store", "obligations", "SHOP-482", "person", "manual", "*.json"))
	if err != nil || len(files) != 2 {
		t.Errorf("%d manual records, want both: %v %v", len(files), files, err)
	}
}

func TestLedgerSkipsTrialAndCheckRuns(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
	r.putRun(t, "r-a-scenarios", "", nil, true, scenarioResult(scenarioID, domain.OutcomeFailed, "refund-too-much: expected 422, got 201"))
	r.putRun(t, "r-b-trial", domain.PurposeTrial, nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	r.putRun(t, "r-c-check", domain.PurposeFixtureCheck, nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	r.putRun(t, "r-d-unfinished", "", nil, false)

	res := r.must(t, cli.ExitFailed, "ledger")
	contains(t, "the table", res.stdout, "Failed (1)", "SHOP-482-01 failed in r-a-scenarios")
}

func TestLedgerReadsTheNewestCompleteRun(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
	r.putRun(t, "r-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomeFailed, "old failure"))
	r.putRun(t, "r-2", "", nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	res := r.must(t, cli.ExitOK, "ledger")
	contains(t, "the table", res.stdout, "Verified (1)")

	// A run that did not touch the scenario leaves the result of the run before it.
	r.putRun(t, "r-3", "", nil, true)
	r.must(t, cli.ExitOK, "ledger")
}

func TestLedgerRunReadsOneRunAndPrintsTheChecklist(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]"),
		entry("SHOP-482/person", "P", manualEntryFields)))
	r.putRun(t, "r-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomeFailed, "old failure"))
	r.putRun(t, "r-2", "", nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))

	res := r.must(t, cli.ExitFailed, "ledger", "--run", "r-1")
	contains(t, "the table", res.stdout, "Failed (1)", "SHOP-482-01 failed in r-1: old failure", "Needs a person (1)",
		"Manual checks for SHOP-482 (1)", "1. SHOP-482/person  P", "semar ledger record SHOP-482/person passed|failed")
	if strings.Index(res.stdout, "Manual checks for") < strings.Index(res.stdout, "Needs a person (1)") {
		t.Errorf("the checklist comes before the table:\n%s", res.stdout)
	}

	// A run with nothing of the scenario gives "no result".
	r.putRun(t, "r-3", "", nil, true)
	res = r.must(t, cli.ExitUndecided, "ledger", "--run", "r-3")
	contains(t, "the table", res.stdout, "SHOP-482-01 has no result: run it")

	r.putRun(t, "r-4-trial", domain.PurposeTrial, nil, true)
	r.putRun(t, "r-5-open", "", nil, false)
	for _, id := range []string{"r-4-trial", "r-5-open", "r-none", "../x"} {
		res := r.must(t, cli.ExitError, "ledger", "--run", id)
		contains(t, "stderr", res.stderr, "run")
		if res.stdout != "" {
			t.Errorf("--run %s printed a table:\n%s", id, res.stdout)
		}
	}
}

func TestLedgerTicketFilter(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "A", `gap: "not drafted"`)))
	r.writeRel(t, ".semar/obligations/other.yaml", obligations("SHOP-480", entry("SHOP-480/b", "B", `gap: "not drafted"`)))
	r.writeRel(t, ".semar/obligations/refunds.yaml", obligations("", entry("refunds/c", "C", `gap: "not drafted"`)))

	res := r.must(t, cli.ExitUndecided, "ledger", "--ticket", "SHOP-482")
	contains(t, "the table", res.stdout, "1 use case:", "SHOP-482/a")
	if strings.Contains(res.stdout, "SHOP-480/b") || strings.Contains(res.stdout, "refunds/c") {
		t.Errorf("another ticket is listed:\n%s", res.stdout)
	}
	res = r.must(t, cli.ExitUndecided, "ledger", "--ticket", "SHOP-482", "--ticket", "refunds")
	contains(t, "the table", res.stdout, "2 use cases:", "SHOP-482/a", "refunds/c")
	res = r.must(t, cli.ExitError, "ledger", "--ticket", "a/b")
	contains(t, "stderr", res.stderr, "invalid ticket")
}

func TestLedgerWithoutObligationsSaysSo(t *testing.T) {
	r := ledgerRepo(t)
	res := r.must(t, cli.ExitUndecided, "ledger")
	contains(t, "stdout", res.stdout, "no use case: there is no obligation in .semar/obligations")
}

func TestLedgerNamesTheProblemsOfAFile(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, "apiVersion: semar/v1\nkind: Obligations\nobligations:\n  - id: SHOP-482/a\n    source: diff\n")
	res := r.must(t, cli.ExitError, "ledger")
	contains(t, "stderr", res.stderr, ".semar/obligations/SHOP-482.plan.yaml:", "title is required", "nothing was derived")
	if res.stdout != "" {
		t.Errorf("a table was printed from a file with a problem:\n%s", res.stdout)
	}
}

func TestLedgerTextFromAFileIsData(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482",
		"  - id: SHOP-482/a\n    title: \"Title \\x1b[2J \\u202e reversed\"\n    source: diff\n    gap: \"gap \\x1b]0;owned\\x07 text\"\n",
		"  - id: SHOP-482/p\n    title: \"By hand \\x1b[31m\"\n    source: diff\n    cite: \"cite \\x1b[41m\"\n    manual:\n      kind: other\n      text: \"why \\x1b[1m\"\n      steps: [\"step \\x1b[2J\"]\n      expected: \"expected \\x9b31m\"\n      writtenBy: code\n"))
	r.must(t, cli.ExitOK, "ledger", "defer", "SHOP-482/a", "--reason", "reason \x1b[2J", "--until", inDays(2))
	for _, args := range [][]string{{"ledger"}, {"ledger", "--checklist"}} {
		res := r.must(t, cli.ExitUndecided, args...)
		for _, out := range []string{res.stdout, res.stderr} {
			if strings.ContainsAny(out, "\x1b\x07\u202e\u009b") {
				t.Errorf("semar %v printed a control character:\n%q", args, out)
			}
		}
		if !strings.Contains(res.stdout, `\x1b`) {
			t.Errorf("semar %v shows no escape:\n%s", args, res.stdout)
		}
	}
}

func TestLedgerChecklist(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/a", "Covered", `gap: "not drafted"`),
		entry("SHOP-482/p", "By hand", manualEntryFields)))
	res := r.must(t, cli.ExitUndecided, "ledger", "--checklist")
	want := `Manual checks for SHOP-482 (1)
1. SHOP-482/p  By hand
   Why a person: A person decides
   Steps (written by code):
     1. Read the document.
     2. Read the code.
   Expected: They agree
   Record:   semar ledger record SHOP-482/p passed|failed --notes "<what you saw>"
`
	if res.stdout != want {
		t.Errorf("checklist:\n%s\nwant:\n%s", res.stdout, want)
	}
	if strings.Contains(res.stdout, "use cases") {
		t.Errorf("the table is printed with --checklist:\n%s", res.stdout)
	}

	// With nothing for a person, it says so.
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", `gap: "not drafted"`)))
	res = r.must(t, cli.ExitUndecided, "ledger", "--checklist")
	contains(t, "stdout", res.stdout, "no use case needs a person")
}

func TestLedgerExpiredDeferralIsIgnoredAndSaid(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Gap", `gap: "not drafted"`)))
	r.must(t, cli.ExitOK, "ledger", "defer", "SHOP-482/a", "--reason", "later", "--until", inDays(2))
	r.opts.Clock = func() time.Time { return time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC) }
	res := r.must(t, cli.ExitUndecided, "ledger")
	contains(t, "the table", res.stdout, "Blocked (1)", "the deferral by tester expired on "+inDays(2), "not drafted")
}

func TestRunSummaryHasCoverageLine(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]"),
		entry("SHOP-482/b", "Gap", `gap: "not drafted"`)))
	r.writeRel(t, ".semar/obligations/other.yaml", obligations("SHOP-480", entry("SHOP-480/c", "Another ticket", `gap: "not drafted"`)))

	res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	lines := strings.Split(strings.TrimRight(res.stdout, "\n"), "\n")
	// The scenario line, the counts, the coverage line, and the path of the report.
	if len(lines) != 4 || lines[2] != "2 use cases: 1 verified, 1 blocked" || !strings.HasPrefix(lines[3], "report: ") {
		t.Errorf("summary:\n%s", res.stdout)
	}
	if md := r.lastReport(t); !strings.Contains(md, "\n2 use cases: 1 verified, 1 blocked\nDetails: `semar ledger --ticket SHOP-482`\n") {
		t.Errorf("report:\n%s", md)
	}
}

func TestRunWithoutObligationsHasNoCoverageLine(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	if strings.Contains(res.stdout, "use case") || res.stderr != "semar: running against local (test, test data)\n" {
		t.Errorf("a repository with no obligation changed the summary:\n%s\n%s", res.stdout, res.stderr)
	}
	if md := r.lastReport(t); strings.Contains(md, "use case") || strings.Contains(md, "ledger") || !strings.HasPrefix(md, "# Semar report r-test-0001\n\n- Environment: local (test)\n") {
		t.Errorf("a repository with no obligation changed the report:\n%s", md)
	}
}

func TestRunWithObligationsOfOtherTicketsHasNoCoverageLine(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	r.writeRel(t, ".semar/obligations/other.yaml", obligations("SHOP-480", entry("SHOP-480/c", "Another ticket", `gap: "not drafted"`)))
	res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	if strings.Contains(res.stdout, "use case") || strings.Contains(r.lastReport(t), "use case") {
		t.Errorf("a use case that no scenario of the run touches was counted:\n%s", res.stdout)
	}
}

func TestRunCountsTheStoredResultOfAScenarioItDidNotRun(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	r.copyScenario(t, "SHOP-482-02")
	builds := []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab"}}
	r.putRun(t, "r-0-old", "", builds, true, scenarioResult("SHOP-482-02", domain.OutcomePassed, ""))
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Both", "covers: ["+scenarioID+", SHOP-482-02]")))
	r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	if md := r.lastReport(t); !strings.Contains(md, "\n1 use case: 1 verified\n") {
		t.Errorf("report:\n%s", md)
	}

	// The same result on another build is stale, and says what to do.
	r.putRun(t, "r-1-old", "", []domain.BuildRef{{Service: "orders", Commit: "1234567"}}, true, scenarioResult("SHOP-482-02", domain.OutcomePassed, ""))
	r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	if md := r.lastReport(t); !strings.Contains(md, "\n1 use case: 0 verified, 1 blocked\n") {
		t.Errorf("report:\n%s", md)
	}
}

func TestRunWithAnInvalidObligationsFileLeavesTheLineOut(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	r.ledgerFile(t, "apiVersion: semar/v1\nkind: Obligations\nobligations:\n  - id: SHOP-482/a\n    source: diff\n")
	res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	contains(t, "stderr", res.stderr, "semar: the use case line is left out of the report: .semar/obligations/SHOP-482.plan.yaml:", "title is required")
	if strings.Contains(res.stdout, "use case") || strings.Contains(r.lastReport(t), "use case") {
		t.Errorf("a line was written from a file with a problem")
	}
}

func TestLedgerRefusesStoredRecordsThatTheCommandsWouldRefuse(t *testing.T) {
	deferral := func(user, reason, until string) string {
		return `{"schema": 1, "obligation": "SHOP-482/a", "user": "` + user + `", "identity": "i", "at": "2026-11-13T09:00:00Z", "reason": "` + reason + `", "until": "` + until + `"}`
	}
	cases := []struct {
		name, kind, file, body, want string
	}{
		{"a deferral to year 9999 with no name and no reason", "deferral", "-1794560400.json", deferral("", "", "9999-01-01T23:59:59Z"), "is not counted"},
		{"a manual result from 2099", "manual", "x-4070908800.json",
			`{"schema": 1, "obligation": "SHOP-482/a", "result": "passed", "user": "x", "identity": "i", "at": "2099-01-01T00:00:00Z", "notes": "n"}`, "after now"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := ledgerRepo(t)
			r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Left for later", `gap: "not drafted"`)))
			r.writeRel(t, ".semar/store/obligations/SHOP-482/a/"+tt.kind+"/"+tt.file, tt.body)
			res := r.must(t, cli.ExitError, "ledger")
			contains(t, "stderr", res.stderr, tt.kind+" record", tt.file, tt.want)
			if res.stdout != "" {
				t.Errorf("a table was printed from a record that cannot be trusted:\n%s", res.stdout)
			}
		})
	}
}
