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
	"github.com/jancuk/semar/internal/scenario"
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
	data, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature"))
	if err != nil {
		t.Fatal(err)
	}
	r.writeRel(t, ".semar/scenarios/SHOP-482/"+id+".feature", strings.Replace(string(data), "@id:"+scenarioID, "@id:"+id, 1))
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

// unreadShip is the end of the last line when the builds were not read.
const unreadShip = "The builds were not read."

func TestStatusGroupsAndExitCodes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, r *repo)
		code  int
		want  []string // each is in the output with the runs of spaces squashed
	}{
		{"every use case verified or deferred is 0 only with the builds read", func(t *testing.T, r *repo) {
			r.putRun(t, "r-old-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
			r.ledgerFile(t, obligations("SHOP-482",
				entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]"),
				entry("SHOP-482/b", "Left for later", `gap: "not drafted: needs a fixture"`)))
			r.must(t, cli.ExitOK, "defer", "SHOP-482/b", "--reason", "waiting for a fixture", "--until", inDays(7))
		}, cli.ExitUndecided, []string{"2 use cases", "verified 1", "deferred 1\n SHOP-482/b Left for later", "Ship: no. " + unreadShip}},
		{"a failed scenario is 1", func(t *testing.T, r *repo) {
			r.putRun(t, "r-old-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomeFailed, "refund-too-much: expected 422, got 201"))
			r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
		}, cli.ExitFailed, []string{"1 use case", "failing 1\n SHOP-482/a Covered\n SHOP-482-01 failed in r-old-1: refund-too-much: expected 422, got 201", "Ship: no. 1 failing."}},
		{"a failed manual result is 1", func(t *testing.T, r *repo) {
			r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "By hand", manualEntryFields)))
			r.must(t, cli.ExitOK, "check", "SHOP-482/a", "fail", "--note", "no")
		}, cli.ExitFailed, []string{"failing 1\n SHOP-482/a By hand\n failed, checked by a person"}},
		{"a use case that never ran is blocked, and is 2", func(t *testing.T, r *repo) {
			r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Never run", "covers: ["+scenarioID+"]")))
		}, cli.ExitUndecided, []string{"blocked 1 → semar run\n SHOP-482/a Never run\n SHOP-482-01 has no result: run it", "Ship: no. 1 open."}},
		{"a use case for a person is 2 when the pass names no build", func(t *testing.T, r *repo) {
			r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "By hand", manualEntryFields)))
			r.must(t, cli.ExitOK, "check", "SHOP-482/a", "pass", "--note", "ok")
		}, cli.ExitUndecided, []string{"for a person 1 → semar check <id> pass\n SHOP-482/a By hand"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ledgerRepo(t)
			c.setup(t, r)
			res := r.must(t, c.code, "status")
			contains(t, "the status", squash(res.stdout), c.want...)
		})
	}
}

func TestStatusGroupsInOrderAndAddUp(t *testing.T) {
	r := ledgerRepo(t)
	for _, id := range []string{"SHOP-482-02", "SHOP-482-03", "SHOP-482-04"} {
		r.copyScenario(t, id)
	}
	review := scenarioResult("SHOP-482-03", domain.OutcomeInconclusive, "an expected result is not confirmed")
	r.putRun(t, "r-old-1", "", nil, true,
		scenarioResult(scenarioID, domain.OutcomePassed, ""),
		scenarioResult("SHOP-482-02", domain.OutcomeFailed, "refund-too-much: expected 422, got 201"),
		review)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/verified", "V", "covers: ["+scenarioID+"]"),
		entry("SHOP-482/deferred", "D", `gap: "not drafted"`),
		entry("SHOP-482/person", "P", manualEntryFields),
		entry("SHOP-482/review", "B", "covers: [SHOP-482-03]"),
		entry("SHOP-482/failed", "F", "covers: [SHOP-482-02]"),
		entry("SHOP-482/nothing", "N")))
	r.must(t, cli.ExitOK, "defer", "SHOP-482/deferred", "--reason", "later", "--until", inDays(3))

	res := r.must(t, cli.ExitFailed, "status")
	out := squash(res.stdout)
	last := -1
	for _, h := range []string{"verified 1", "failing 1", "needs review 1", "for a person 1", "no test 1", "deferred 1"} {
		i := strings.Index(out, h)
		if i < 0 || i < last {
			t.Fatalf("the group %q is missing or out of order:\n%s", h, res.stdout)
		}
		last = i
	}
	contains(t, "the status", out,
		"6 use cases",
		"no test 1 → semar plan\n SHOP-482/nothing N\n use case with no test and no reason",
		"SHOP-482-02 failed in r-old-1: refund-too-much: expected 422, got 201",
		"needs review 1 → semar review\n SHOP-482/review B",
		"for a person 1 → semar check <id> pass\n SHOP-482/person P",
		"deferred 1\n SHOP-482/deferred D",
		"Ship: no. 1 failing, 3 open.")
}

func TestStatusWithoutABuildSaysSo(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Gap", `gap: "not drafted"`)))
	res := r.must(t, cli.ExitUndecided, "status")
	contains(t, "stderr", res.stderr, "the build of orders could not be read", "the results are not matched to the current build")
	if strings.Contains(res.stdout, "could not be read") {
		t.Errorf("the note is on standard output:\n%s", res.stdout)
	}
	contains(t, "the first line", strings.SplitN(res.stdout, "\n", 2)[0], "Not in a git repository", "local")
}

// Without --env, a pass on any build counts, so nothing is confirmed current: the exit code is 2 at
// best, and the first line says so. A failure still makes it 1.
func TestStatusWithTheBuildsNotReadIsNeverGreen(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
	r.putRun(t, "r-old-1", "", []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab"}}, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	res := r.must(t, cli.ExitUndecided, "status")
	contains(t, "the status", squash(res.stdout), "1 use case", "verified 1", "Ship: no. "+unreadShip)
	r.putRun(t, "r-z-new", "", nil, true, scenarioResult(scenarioID, domain.OutcomeFailed, "boom"))
	r.must(t, cli.ExitFailed, "status")
}

func TestStatusEnvMarksStale(t *testing.T) {
	r := newRepo(t, repoOptions{app: sampleapp.Options{Commit: "c0ffee1"}})
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
	r.putRun(t, "r-old-1", "", []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab"}}, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))

	// The only environment is read with no argument, and an old build is stale.
	res := r.must(t, cli.ExitUndecided, "status")
	contains(t, "the status", squash(res.stdout), "local · orders at c0ffee1",
		"stale 1 → semar run\n SHOP-482/a Covered\n SHOP-482-01 ran on orders 9f3c2ab, and the environment runs c0ffee1: rerun on the current build")
	if strings.Contains(res.stderr, "not read") {
		t.Errorf("the note about unread builds is printed although the environment was read:\n%s", res.stderr)
	}
	// Named as an argument, or with --env, it gives the same.
	r.must(t, cli.ExitUndecided, "status", "local")
	r.must(t, cli.ExitUndecided, "status", "--env", "local")
	res = r.must(t, cli.ExitError, "status", "local", "--env", "local")
	contains(t, "stderr", res.stderr, "name the environment once")

	// A run on the build that the environment runs now counts again.
	r.putRun(t, "r-z-new", "", []domain.BuildRef{{Service: "orders", Commit: "c0ffee1"}}, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	res = r.must(t, cli.ExitOK, "status")
	contains(t, "the status", squash(res.stdout), "1 use case", "verified 1", "Ship: yes.")
}

func TestStatusNamedEnvThatCannotBeReadIsAnError(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
	res := r.must(t, cli.ExitError, "status", "local")
	contains(t, "stderr", res.stderr, "the build of orders could not be read", "cannot be matched to the current build")
	if res.stdout != "" {
		t.Errorf("a status was printed without the builds:\n%s", res.stdout)
	}
}

func TestStatusBuildNeedsAnEnvironment(t *testing.T) {
	r := ledgerRepo(t)
	res := r.must(t, cli.ExitError, "status", "--build", "abcdef1")
	contains(t, "stderr", res.stderr, "--build needs an environment")
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
			res := r.must(t, cli.ExitError, append([]string{"defer"}, c.args...)...)
			contains(t, "stderr", res.stderr, c.want)
			if exists(t, r.path(".semar", "store", "obligations")) {
				t.Error("a refused deferral left a record")
			}
		})
	}

	// 180 days is the longest.
	res := r.must(t, cli.ExitOK, "defer", "SHOP-482/a", "--reason", "waiting", "--until", inDays(180))
	contains(t, "stdout", res.stdout, "deferred SHOP-482/a until "+inDays(180)+" as tester", "local, unverified identity")
}

func TestDeferNeedsAName(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Gap", `gap: "not drafted"`)))
	r.opts.User = func() (string, error) { return "  ", nil }
	res := r.must(t, cli.ExitError, "defer", "SHOP-482/a", "--reason", "r", "--until", inDays(5))
	contains(t, "stderr", res.stderr, "no name")
	r.opts.User = func() (string, error) { return "", fmt.Errorf("no user") }
	r.must(t, cli.ExitError, "defer", "SHOP-482/a", "--reason", "r", "--until", inDays(5))
	if exists(t, r.path(".semar", "store", "obligations")) {
		t.Error("a deferral without a name left a record")
	}
}

func TestRecordNeedsAName(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "By hand", manualEntryFields)))
	r.opts.User = func() (string, error) { return "", nil }
	res := r.must(t, cli.ExitError, "check", "SHOP-482/a", "pass", "--note", "x")
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
		res := r.must(t, cli.ExitError, "check", id, "pass", "--note", "x")
		contains(t, "stderr", res.stderr, id, "only a use case with a manual check")
	}
	res := r.must(t, cli.ExitError, "check", "SHOP-482/none", "pass", "--note", "x")
	contains(t, "stderr", res.stderr, "SHOP-482/none was not found")
	res = r.must(t, cli.ExitError, "check", "SHOP-482/person", "maybe", "--note", "x")
	contains(t, "stderr", res.stderr, "say pass or fail")
	if exists(t, r.path(".semar", "store", "obligations")) {
		t.Error("a refused result left a record")
	}

	res = r.must(t, cli.ExitOK, "check", "SHOP-482/person", "pass", "--note", "the document is right")
	contains(t, "stdout", res.stdout, "recorded passed for SHOP-482/person as tester", "checked by a person")
}

func TestRecordIsANewRecordAndKeepsTheState(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/person", "P", manualEntryFields)))

	r.must(t, cli.ExitOK, "check", "SHOP-482/person", "fail", "--note", "no")
	res := r.must(t, cli.ExitFailed, "status")
	contains(t, "the table", squash(res.stdout), "failing 1\n SHOP-482/person P\n failed, checked by a person")

	r.opts.Clock = func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }
	r.must(t, cli.ExitOK, "check", "SHOP-482/person", "pass", "--note", "yes, after a fix")
	res = r.must(t, cli.ExitUndecided, "status")
	contains(t, "the table", squash(res.stdout), "for a person 1 → semar check <id> pass\n SHOP-482/person P")
	if strings.Contains(res.stdout, "verified") {
		t.Errorf("a manual result with no build counted as a pass:\n%s", res.stdout)
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

	res := r.must(t, cli.ExitFailed, "status")
	contains(t, "the table", squash(res.stdout), "failing 1", "SHOP-482-01 failed in r-a-scenarios")
}

// withHash is a passed result that carries the hash of the expected results of the scenario file now.
func (r *repo) withHash(t *testing.T) domain.ScenarioResult {
	t.Helper()
	sc, err := scenario.Load(r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature"))
	if err != nil {
		t.Fatal(err)
	}
	res := scenarioResult(scenarioID, domain.OutcomePassed, "")
	res.ExpectHash = domain.ExpectHash(sc)
	return res
}

// ledgerTable is what semar status prints, whatever the exit code.
func (r *repo) ledgerTable(t *testing.T) string {
	t.Helper()
	return r.semar(t, t.Context(), "status").stdout
}

// editScenario changes the scenario file of SHOP-482-01, and fails when the change does not apply.
func (r *repo) editScenario(t *testing.T, from, to string) {
	t.Helper()
	rel := ".semar/scenarios/SHOP-482/" + scenarioID + ".feature"
	data, err := os.ReadFile(r.path(filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), from) {
		t.Fatalf("%s does not hold %q", rel, from)
	}
	r.writeRel(t, rel, strings.Replace(string(data), from, to, 1))
}

func TestLedgerDoesNotCountAPassOfAScenarioThatChanged(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
	r.putRun(t, "r-1", "", nil, true, r.withHash(t))
	contains(t, "the table", squash(r.ledgerTable(t)), "verified 1")

	// The expected result is edited after the pass.
	r.editScenario(t, "the status is 422", "the status is 400")
	contains(t, "the table", squash(r.ledgerTable(t)), "stale 1 → semar run\n SHOP-482/a Covered\n "+scenarioID+": rerun: the scenario changed since r-1")
}

func TestLedgerCountsAPassOfAnOldRunWithNoHash(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
	r.putRun(t, "r-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	r.editScenario(t, "the status is 422", "the status is 400")
	contains(t, "the table", squash(r.ledgerTable(t)), "verified 1")
}

func TestLedgerReadsTheNewestCompleteRun(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]")))
	r.putRun(t, "r-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomeFailed, "old failure"))
	r.putRun(t, "r-2", "", nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	res := r.must(t, cli.ExitUndecided, "status")
	contains(t, "the table", squash(res.stdout), "verified 1")

	// A run that did not touch the scenario leaves the result of the run before it.
	r.putRun(t, "r-3", "", nil, true)
	r.must(t, cli.ExitUndecided, "status")
}

func TestLedgerRunReadsOneRunAndPrintsTheChecklist(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]"),
		entry("SHOP-482/person", "P", manualEntryFields)))
	r.putRun(t, "r-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomeFailed, "old failure"))
	r.putRun(t, "r-2", "", nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))

	res := r.must(t, cli.ExitFailed, "status", "--run", "r-1")
	contains(t, "the table", squash(res.stdout), "failing 1", "SHOP-482-01 failed in r-1: old failure", "for a person 1")
	contains(t, "the checklist", res.stdout, "Manual checks for SHOP-482 (1)", "1. SHOP-482/person  P", "semar check SHOP-482/person pass|fail")
	if strings.Index(res.stdout, "Manual checks for") < strings.Index(squash(res.stdout), "for a person 1") {
		t.Errorf("the checklist comes before the table:\n%s", res.stdout)
	}

	// A run with nothing of the scenario gives "no result".
	r.putRun(t, "r-3", "", nil, true)
	res = r.must(t, cli.ExitUndecided, "status", "--run", "r-3")
	contains(t, "the table", res.stdout, "SHOP-482-01 has no result: run it")

	r.putRun(t, "r-4-trial", domain.PurposeTrial, nil, true)
	r.putRun(t, "r-5-open", "", nil, false)
	for _, id := range []string{"r-4-trial", "r-5-open", "r-none", "../x"} {
		res := r.must(t, cli.ExitError, "status", "--run", id)
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

	res := r.must(t, cli.ExitUndecided, "status", "--ticket", "SHOP-482")
	contains(t, "the table", squash(res.stdout), "1 use case of SHOP-482", "SHOP-482/a")
	if first := strings.SplitN(res.stdout, "\n", 2)[0]; !strings.HasPrefix(first, "The tickets named") {
		t.Errorf("the first line must say what was picked, not where the directory is: %q", first)
	}
	if strings.Contains(res.stdout, "SHOP-480/b") || strings.Contains(res.stdout, "refunds/c") {
		t.Errorf("another ticket is listed:\n%s", res.stdout)
	}
	res = r.must(t, cli.ExitUndecided, "status", "--ticket", "SHOP-482", "--ticket", "refunds")
	contains(t, "the table", squash(res.stdout), "2 use cases of SHOP-482, refunds", "SHOP-482/a", "refunds/c")
	res = r.must(t, cli.ExitError, "status", "--ticket", "a/b")
	contains(t, "stderr", res.stderr, "invalid ticket")
}

func TestLedgerWithoutObligationsSaysSo(t *testing.T) {
	r := ledgerRepo(t)
	res := r.must(t, cli.ExitUndecided, "status")
	contains(t, "stdout", res.stdout, "no use case: there is no obligation in .semar/obligations")
}

func TestLedgerNamesTheProblemsOfAFile(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, "apiVersion: semar/v1\nkind: Obligations\nobligations:\n  - id: SHOP-482/a\n    source: diff\n")
	res := r.must(t, cli.ExitError, "status")
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
	r.must(t, cli.ExitOK, "defer", "SHOP-482/a", "--reason", "reason \x1b[2J", "--until", inDays(2))
	for _, args := range [][]string{{"status"}, {"status", "--checklist"}} {
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
	res := r.must(t, cli.ExitUndecided, "status", "--checklist")
	want := `Manual checks for SHOP-482 (1)
1. SHOP-482/p  By hand
   Why a person: A person decides
   Steps (written by code):
     1. Read the document.
     2. Read the code.
   Expected: They agree
   Check:    semar check SHOP-482/p pass|fail --note "<what you saw>"
`
	if res.stdout != want {
		t.Errorf("checklist:\n%s\nwant:\n%s", res.stdout, want)
	}
	if strings.Contains(res.stdout, "use cases") {
		t.Errorf("the table is printed with --checklist:\n%s", res.stdout)
	}

	// With nothing for a person, it says so.
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Covered", `gap: "not drafted"`)))
	res = r.must(t, cli.ExitUndecided, "status", "--checklist")
	contains(t, "stdout", res.stdout, "no use case needs a person")
}

func TestLedgerExpiredDeferralIsIgnoredAndSaid(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Gap", `gap: "not drafted"`)))
	r.must(t, cli.ExitOK, "defer", "SHOP-482/a", "--reason", "later", "--until", inDays(2))
	r.opts.Clock = func() time.Time { return time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC) }
	res := r.must(t, cli.ExitUndecided, "status")
	contains(t, "the table", squash(res.stdout), "no test 1", "the deferral by tester expired on "+inDays(2), "not drafted")
}

func TestRunSummaryHasCoverageLine(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/a", "Covered", "covers: ["+scenarioID+"]"),
		entry("SHOP-482/b", "Gap", `gap: "not drafted"`)))
	r.writeRel(t, ".semar/obligations/other.yaml", obligations("SHOP-480", entry("SHOP-480/c", "Another ticket", `gap: "not drafted"`)))

	res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	// The scenario line, the path of the report, and the status of the use cases of the ticket.
	contains(t, "summary", squash(res.stdout), scenarioID+" passed", "report: ", "2 use cases of SHOP-482", "verified 1", "no test 1 → semar plan\n SHOP-482/b Gap\n not drafted", "Ship: no. 1 open.")
	if strings.Contains(res.stdout, "SHOP-480") {
		t.Errorf("a use case of another ticket is in the status:\n%s", res.stdout)
	}
	if md := r.lastReport(t); !strings.Contains(md, "\n2 use cases: 1 verified, 1 blocked\nDetails: `semar status --ticket SHOP-482`\n") {
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
	if md := r.lastReport(t); strings.Contains(md, "use case") || strings.Contains(md, "status") || !strings.HasPrefix(md, "# Semar report r-test-0001\n\n- Environment: local (test)\n") {
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
			res := r.must(t, cli.ExitError, "status")
			contains(t, "stderr", res.stderr, tt.kind+" record", tt.file, tt.want)
			if res.stdout != "" {
				t.Errorf("a table was printed from a record that cannot be trusted:\n%s", res.stdout)
			}
		})
	}
}

// A scenario file named like the scenario of a journey leg is refused, so that no result, label, or
// confirmation is shared with the leg.
func TestAScenarioIDOfTheFormOfALegIsRefused(t *testing.T) {
	r := ledgerRepo(t)
	r.copyScenario(t, "checkout.pay")
	for _, args := range [][]string{{"review", "checkout.pay", "--yes"}, {"run", "--scenario", "checkout.pay"}} {
		res := r.must(t, cli.ExitError, args...)
		contains(t, "stderr", res.stderr, "invalid scenario ID", "checkout.pay")
	}
}

// plantManual stores a manual result by hand, as an older or another build of Semar would have.
func (r *repo) plantManual(t *testing.T, result, builds string) {
	t.Helper()
	r.writeRel(t, ".semar/store/obligations/SHOP-482/a/manual/tester-1794560400.json",
		`{"schema": 1, "obligation": "SHOP-482/a", "result": "`+result+`", "user": "tester", "identity": "i", "at": "2026-11-13T09:00:00Z", "notes": "n"`+builds+`}`)
}

const (
	buildsNow   = `, "builds": [{"service": "orders", "commit": "c0ffee1"}]`
	buildsOther = `, "builds": [{"service": "orders", "commit": "9f3c2ab"}]`
)

func TestStatusExitCodeWithManualResults(t *testing.T) {
	tests := []struct {
		name, result, builds string
		failedScenario       bool
		args                 []string
		want                 int
		lines                []string
	}{
		{"no manual result", "", "", false, []string{"--env", "local"}, cli.ExitUndecided, []string{"for a person 1 → semar check <id> pass\n SHOP-482/a By hand"}},
		{"passed on the builds that run now", "passed", buildsNow, false, []string{"--env", "local"}, cli.ExitOK,
			[]string{"verified 1\n SHOP-482/a By hand\n checked by tester", "Ship: yes."}},
		{"passed without builds", "passed", "", false, []string{"--env", "local"}, cli.ExitUndecided,
			[]string{"for a person 1 → semar check <id> pass\n SHOP-482/a By hand", "recorded without --env, so it is not tied to a build"}},
		{"passed on another commit", "passed", buildsOther, false, []string{"--env", "local"}, cli.ExitUndecided,
			[]string{"checked by a person on another build: rerun the check and record it with --env"}},
		{"failed on the builds that run now", "failed", buildsNow, false, []string{"--env", "local"}, cli.ExitFailed,
			[]string{"failing 1\n SHOP-482/a By hand\n failed, checked by a person"}},
		{"passed beside a failed scenario", "passed", buildsNow, true, []string{"--env", "local"}, cli.ExitFailed, []string{"failing 1\n SHOP-482/b Covered"}},
		{"passed, with the only environment read by default", "passed", buildsNow, false, nil, cli.ExitOK,
			[]string{"verified 1\n SHOP-482/a By hand\n checked by tester"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRepo(t, repoOptions{app: sampleapp.Options{Commit: "c0ffee1"}})
			entries := []string{entry("SHOP-482/a", "By hand", manualEntryFields)}
			if tt.failedScenario {
				entries = append(entries, entry("SHOP-482/b", "Covered", "covers: ["+scenarioID+"]"))
				r.putRun(t, "r-old-1", "", []domain.BuildRef{{Service: "orders", Commit: "c0ffee1"}}, true, scenarioResult(scenarioID, domain.OutcomeFailed, "boom"))
			}
			r.ledgerFile(t, obligations("SHOP-482", entries...))
			if tt.result != "" {
				r.plantManual(t, tt.result, tt.builds)
			}
			res := r.must(t, tt.want, append([]string{"status"}, tt.args...)...)
			contains(t, "the status", squash(res.stdout), tt.lines...)
		})
	}
}

func TestRecordBuildNeedsEnvAndEnvMustBeReadable(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "By hand", manualEntryFields)))
	res := r.must(t, cli.ExitError, "check", "SHOP-482/a", "pass", "--note", "x", "--build", "abcdef1")
	contains(t, "stderr", res.stderr, "--build needs an environment")
	res = r.must(t, cli.ExitError, "check", "SHOP-482/a", "pass", "--note", "x", "--env", "local")
	contains(t, "stderr", res.stderr, "the build of orders could not be read")
	if exists(t, r.path(".semar", "store", "obligations")) {
		t.Error("a refused record was stored")
	}
}

func TestLedgerRefusesHostileBuildsOnRead(t *testing.T) {
	many := strings.Repeat(`{"service": "orders", "commit": "c0ffee1"},`, 999) + `{"service": "orders", "commit": "c0ffee1"}`
	for name, builds := range map[string]string{
		"1000 builds":     `, "builds": [` + many + `]`,
		"an empty commit": `, "builds": [{"service": "orders", "commit": ""}]`,
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRepo(t)
			r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "By hand", manualEntryFields)))
			r.plantManual(t, "passed", builds)
			res := r.must(t, cli.ExitError, "status")
			contains(t, "stderr", res.stderr, "manual record", "is not counted")
			if res.stdout != "" {
				t.Errorf("a table was printed:\n%s", res.stdout)
			}
		})
	}
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "By hand", manualEntryFields)))
	r.plantManual(t, "passed", "")
	r.must(t, cli.ExitUndecided, "status")
}
