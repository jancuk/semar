package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/sampleapp"
)

var checkoutLegs = []string{"checkout.place", "checkout.pay", "checkout.settled"}

// journeyRepo is a copy of testdata/e2e-journeys, served by orders and payments on 127.0.0.1. A wait
// takes no time, because the journey runner sleeps through the option of the test.
func journeyRepo(t *testing.T, app sampleapp.Options, pay sampleapp.PaymentOptions) *repo {
	t.Helper()
	r := newRepo(t, repoOptions{tree: "e2e-journeys", app: app, payments: &pay})
	r.opts.Sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return r
}

// squash puts every run of spaces on one line as a single space, so that a test does not depend on
// the width of a column.
func squash(s string) string { return regexp.MustCompile(` +`).ReplaceAllString(s, " ") }

func (r *repo) journeyFile(name string) string { return r.path(".semar", "journeys", name+".yaml") }

func (r *repo) readJourney(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(r.journeyFile(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *repo) editJourney(t *testing.T, name string, change func(string) string) {
	t.Helper()
	text := r.readJourney(t, name)
	changed := change(text)
	if changed == text {
		t.Fatalf("the change to journey %s did not apply", name)
	}
	r.writeRel(t, ".semar/journeys/"+name+".yaml", changed)
}

// requireConfirmation makes every expected result of the checkout journey one that a person must confirm.
func (r *repo) requireConfirmation(t *testing.T) {
	t.Helper()
	r.editJourney(t, "checkout", func(s string) string { return strings.ReplaceAll(s, "source: person", "source: diff") })
}

// standardJourney is a second journey of one leg, with the criticality standard.
const standardJourney = `apiVersion: semar/v1
kind: Journey
id: lookup
title: An order is placed
owner: team-commerce
criticality: standard
legs:
  - id: place
    surface: api
    service: orders
    steps:
      - id: create
        action: Create an order of 500
        hint: { method: POST, path: /v1/orders }
        request: { method: POST, path: /v1/orders, json: { amount: 500, ref: "semar-{{run.id}}" } }
        captures: [{ name: id, jsonpath: "$.data.id" }]
        expect: [{ check: { status: 201 }, source: person }]
    outputs: [id]
`

// storedRun keeps a run of journeys as run.json, as semar journey run leaves it.
func (r *repo) storedRun(t *testing.T, id string, builds []domain.BuildRef, results ...domain.JourneyResult) {
	t.Helper()
	store, err := evidence.Open(r.path(".semar"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.NewRun(domain.RunID(id))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	if err := run.Close(domain.Run{ID: domain.RunID(id), Environment: "local", Purpose: domain.PurposeJourney, StartedAt: at, EndedAt: at.Add(time.Minute),
		BuildsStart: builds, BuildsEnd: builds, Results: []domain.ScenarioResult{}, Journeys: results}); err != nil {
		t.Fatal(err)
	}
}

func manifestOf(builds ...domain.BuildRef) domain.Manifest {
	return domain.Manifest{Services: builds, Hash: domain.ManifestHash(builds)}
}

func (r *repo) commitOf(t *testing.T, base, tok string) string {
	t.Helper()
	commit, _ := r.call(t, "GET", base, "/version", tok, nil)["commit"].(string)
	return commit
}

func TestJourneyListShowsLastOutcome(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})

	before := r.must(t, cli.ExitOK, "journey", "list")
	contains(t, "the list", squash(before.stdout), "checkout critical team-commerce 3 legs no result yet")
	if n := r.reached(""); n != 0 {
		t.Errorf("a plain list sent %d requests", n)
	}

	r.must(t, cli.ExitOK, "journey", "run", "checkout")
	run := r.lastRun(t)
	if len(run.Journeys) != 1 {
		t.Fatalf("run.json has %d journeys", len(run.Journeys))
	}
	reached := r.reached("")
	res := r.must(t, cli.ExitOK, "journey", "list")
	contains(t, "the list", squash(res.stdout), "checkout critical team-commerce 3 legs passed in r-test-0001 on manifest "+string(run.Journeys[0].Manifest.Hash)[:12])
	if n := r.reached(""); n != reached {
		t.Errorf("a list without --env sent %d requests", n-reached)
	}
}

func TestJourneyListStaleWithEnv(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	orders, payments := r.commitOf(t, r.ordersURL, token), r.commitOf(t, r.paymentsURL, "tok-pay")
	stored := manifestOf(
		domain.BuildRef{Service: "orders", Commit: "0ld0ld0", ConfigHash: ""},
		domain.BuildRef{Service: "payments", Commit: payments, ConfigHash: ""})
	r.storedRun(t, "r-old-1", stored.Services, domain.JourneyResult{
		Journey: "checkout", Manifest: stored, ManifestEnd: &stored, Outcome: domain.JourneyPassed,
		Legs: []domain.LegResult{}, Invariants: []domain.InvariantResult{}, Reasons: []domain.Reason{},
	})

	plain := r.must(t, cli.ExitOK, "journey", "list")
	contains(t, "the list without --env", squash(plain.stdout), "passed in r-old-1")
	if strings.Contains(plain.stdout, "stale") {
		t.Errorf("a list without --env read no build, and still says stale:\n%s", plain.stdout)
	}

	res := r.must(t, cli.ExitOK, "journey", "list", "--env", "local")
	contains(t, "the list", squash(res.stdout), "checkout critical team-commerce 3 legs stale (was passed in r-old-1): orders 0ld0ld0 then "+orders)
	if strings.Contains(res.stdout, "payments "+payments+" then") {
		t.Errorf("the list names a service that did not change:\n%s", res.stdout)
	}
}

func TestJourneyListSaysWhenABuildCannotBeRead(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "/version\n          commit: $.commit\nauthProfiles", "/missing\n          commit: $.commit\nauthProfiles", 1)
	})
	pay := r.commitOf(t, r.paymentsURL, "tok-pay")
	orders := r.commitOf(t, r.ordersURL, token)
	m := manifestOf(domain.BuildRef{Service: "orders", Commit: orders}, domain.BuildRef{Service: "payments", Commit: pay})
	r.storedRun(t, "r-old-1", m.Services, domain.JourneyResult{Journey: "checkout", Manifest: m, ManifestEnd: &m, Outcome: domain.JourneyPassed,
		Legs: []domain.LegResult{}, Invariants: []domain.InvariantResult{}, Reasons: []domain.Reason{}})
	res := r.must(t, cli.ExitOK, "journey", "list", "--env", "local")
	contains(t, "the list", squash(res.stdout), "passed in r-old-1", "not checked: the build of payments could not be read")
	if strings.Contains(res.stdout, "stale") {
		t.Errorf("an unreadable build made the result stale:\n%s", res.stdout)
	}
}

func TestJourneyListShowsEveryInvalidFileWithItsLine(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.writeRel(t, ".semar/journeys/broken.yaml", strings.Replace(r.readJourney(t, "checkout"), "id: checkout", "id: broken\nschedule: nightly", 1))
	r.writeRel(t, ".semar/journeys/web.yaml", strings.Replace(strings.Replace(r.readJourney(t, "checkout"), "id: checkout", "id: web", 1), "surface: api", "surface: android", 1))
	r.writeRel(t, ".semar/journeys/evil.yaml", "apiVersion: semar/v1\nkind: Journey\nid: evil\n\"x\\e[31mred\\u202eblue\": 1\n")

	res := r.must(t, cli.ExitUndecided, "journey", "list")
	contains(t, "the list", res.stdout, "checkout")
	lines := map[string]string{}
	for _, l := range strings.Split(res.stdout, "\n") {
		if strings.HasPrefix(l, "invalid: ") {
			lines[strings.SplitN(strings.TrimPrefix(l, "invalid: "), ":", 2)[0]] = l
		}
	}
	line := regexp.MustCompile(`^invalid: \.semar/journeys/%s\.yaml:[0-9]+: `)
	for name, msg := range map[string]string{
		"broken": "unknown key schedule",
		"web":    "android legs are not supported yet: this version runs api legs and web legs; android legs come in M3",
	} {
		l, ok := lines[".semar/journeys/"+name+".yaml"]
		if !ok {
			t.Errorf("no invalid line for %s:\n%s", name, res.stdout)
			continue
		}
		if !regexp.MustCompile(strings.Replace(line.String(), "%s", name, 1)).MatchString(l) || !strings.Contains(l, msg) {
			t.Errorf("the line for %s is %q, want a file and a line, then %q", name, l, msg)
		}
	}
	if strings.ContainsAny(res.stdout, "\x1b\u202e") {
		t.Errorf("the list holds a control character:\n%q", res.stdout)
	}
	contains(t, "the list", res.stdout, `x\x1b[31mred`)
}

func TestJourneyListWithoutAJourneyFile(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	if err := os.RemoveAll(r.path(".semar", "journeys")); err != nil {
		t.Fatal(err)
	}
	res := r.must(t, cli.ExitOK, "journey", "list")
	contains(t, "the list", res.stdout, "there is no journey file in "+filepath.Join(r.dir, ".semar", "journeys"))
}

func TestJourneyRunUnknownIDSendsNothing(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"unknown":       {[]string{"journey", "run", "checkout", "nope"}, "journey nope was not found"},
		"unsafe":        {[]string{"journey", "run", "../checkout"}, "invalid journey ID"},
		"invalid file":  {[]string{"journey", "run", "checkout", "broken"}, "journey broken is not valid, so it cannot be run"},
		"no selector":   {[]string{"journey", "run"}, "--critical"},
		"two selectors": {[]string{"journey", "run", "--critical", "checkout"}, "and only one of them"},
		"both flags":    {[]string{"journey", "run", "--critical", "--all"}, "and only one of them"},
	} {
		t.Run(name, func(t *testing.T) {
			r.writeRel(t, ".semar/journeys/broken.yaml", strings.Replace(r.readJourney(t, "checkout"), "id: checkout", "id: broken\nschedule: nightly", 1))
			res := r.must(t, cli.ExitError, tc.args...)
			contains(t, "stderr", res.stderr, tc.want)
			if n := r.reached(""); n != 0 {
				t.Errorf("%d requests reached the services", n)
			}
			if dirs := r.runDirs(t); len(dirs) != 0 {
				t.Errorf("a refused run created %v", dirs)
			}
		})
	}
}

func TestJourneyRunCritical(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.writeRel(t, ".semar/journeys/lookup.yaml", standardJourney)

	r.must(t, cli.ExitOK, "journey", "run", "--critical")
	run := r.lastRun(t)
	if len(run.Journeys) != 1 || run.Journeys[0].Journey != "checkout" {
		t.Errorf("--critical ran %+v", run.Journeys)
	}
	if n := r.app.Requests("POST", "/v1/orders"); n != 1 {
		t.Errorf("the critical run created %d orders, want 1", n)
	}

	r.must(t, cli.ExitOK, "journey", "run", "--all")
	run = r.lastRun(t)
	var ids []string
	for _, j := range run.Journeys {
		ids = append(ids, string(j.Journey))
	}
	if strings.Join(ids, ",") != "checkout,lookup" {
		t.Errorf("--all ran %v", ids)
	}
}

func TestJourneyRunSkipsAnInvalidFileAndIsNotAPass(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.writeRel(t, ".semar/journeys/broken.yaml", "apiVersion: semar/v1\nkind: Journey\nid: broken\nschedule: nightly\n")
	for _, sel := range []string{"--all", "--critical"} {
		res := r.must(t, cli.ExitUndecided, "journey", "run", sel)
		contains(t, "stderr", res.stderr, ".semar/journeys/broken.yaml:", "unknown key schedule", "this run skips it")
		contains(t, "stdout", res.stdout, "checkout")
		if got := r.lastRun(t); len(got.Journeys) != 1 || got.Journeys[0].Outcome != domain.JourneyPassed {
			t.Errorf("%s: the valid journey did not run: %+v", sel, got.Journeys)
		}
	}
}

func TestJourneyRunExitCodes(t *testing.T) {
	cases := []struct {
		name    string
		app     sampleapp.Options
		pay     sampleapp.PaymentOptions
		prepare func(t *testing.T, r *repo)
		code    int
		want    string
	}{
		{name: "passed", code: cli.ExitOK, want: "checkout  passed"},
		{name: "an invariant is false", pay: sampleapp.PaymentOptions{Defects: defects(sampleapp.PaymentAmountWrong)}, code: cli.ExitFailed,
			want: "checkout failed invariant 1 is false: 1000, 1001, 1000"},
		{name: "a build changes", app: sampleapp.Options{Defects: defects(sampleapp.BuildChanges)}, code: cli.ExitUndecided, want: "checkout stale a build changed during the journey: orders 9f3c2ab then c0ffee1; first failing leg place"},
		{name: "the expected results are not confirmed", prepare: func(t *testing.T, r *repo) { r.requireConfirmation(t) }, code: cli.ExitUndecided,
			want: "checkout blocked first failing leg place: an expected result is not confirmed"},
		{name: "a build cannot be read", prepare: func(t *testing.T, r *repo) {
			r.rewriteConfig(t, func(s string) string {
				return strings.Replace(s, "/version\n          commit: $.commit\nauthProfiles", "/nothing\n          commit: $.commit\nauthProfiles", 1)
			})
		}, code: cli.ExitUndecided, want: "checkout blocked the build of payments could not be read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := journeyRepo(t, tc.app, tc.pay)
			if tc.prepare != nil {
				tc.prepare(t, r)
			}
			res := r.must(t, tc.code, "journey", "run", "checkout")
			contains(t, "stdout", squash(res.stdout), squash(tc.want))
		})
	}
}

func TestJourneyRunNeedsAnExpectedResultConfirmedBeforeItSendsAnything(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.editJourney(t, "checkout", func(s string) string { return strings.Replace(s, "source: person", "source: diff", 1) })
	res := r.must(t, cli.ExitUndecided, "journey", "run", "checkout")
	contains(t, "stdout", squash(res.stdout), "checkout blocked first failing leg place: an expected result is not confirmed: run semar confirm --journey checkout",
		"place inconclusive", "pay not_run", "settled not_run")
	if n := r.reached(""); n != 0 {
		t.Errorf("%d requests were sent for a journey whose expected results nobody confirmed", n)
	}
	run := r.lastRun(t)
	if len(run.Journeys) != 1 || run.Journeys[0].Outcome != domain.JourneyBlocked || run.Journeys[0].FirstFailing != "place" || len(run.Results) != 3 {
		t.Fatalf("run.json: %+v", run.Journeys)
	}
	if got := run.Journeys[0].Reasons[0].Text; got != "leg place ended inconclusive: an expected result is not confirmed: run semar confirm --journey checkout" {
		t.Errorf("the journey reason is %q", got)
	}
	for _, rr := range run.Results {
		if len(rr.Actions) != 0 {
			t.Errorf("leg %s names actions although nothing was sent", rr.Scenario)
		}
	}
}

func TestJourneyRunOfAProductionEnvironmentNeedsItsName(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.rewriteConfig(t, func(s string) string { return strings.Replace(s, "kind: test", "kind: production", 1) })
	res := r.must(t, cli.ExitError, "journey", "run", "checkout")
	contains(t, "stderr", res.stderr, `the environment "local" is a production environment; name it with --env local to run against it`)
	if n := r.reached(""); n != 0 {
		t.Errorf("%d requests reached the services", n)
	}
	res = r.must(t, cli.ExitOK, "journey", "run", "checkout", "--env", "local")
	contains(t, "stderr", res.stderr, "semar: running against local (production, test data)")
}

func TestJourneyRunStopsWhenTheContextEnds(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res := r.semar(t, ctx, "journey", "run", "checkout")
	if res.code != cli.ExitUndecided && res.code != cli.ExitError {
		t.Errorf("exit %d for an interrupted run", res.code)
	}
	if exists(t, filepath.Join(r.latestRunOrEmpty(), "run.json")) {
		t.Error("an interrupted run kept a run.json")
	}
}

func (r *repo) latestRunOrEmpty() string {
	dirs, _ := os.ReadDir(r.path(".semar", "runs"))
	if len(dirs) == 0 {
		return r.path(".semar", "runs", "none")
	}
	return r.path(".semar", "runs", dirs[len(dirs)-1].Name())
}

func (r *repo) confirmationFiles(t *testing.T, scenario string) []string {
	t.Helper()
	files, err := filepath.Glob(r.path(".semar", "store", "confirmations", scenario, "*", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestConfirmJourneyStoresOnePerLeg(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.requireConfirmation(t)
	r.opts.Terminal = func() bool { return false }

	// Without --yes and without a terminal, nothing is stored.
	refused := r.must(t, cli.ExitUndecided, "confirm", "--journey", "checkout")
	contains(t, "stdout", refused.stdout, "Leg place of journey checkout (scenario checkout.place) has 1 expected results that a person must confirm:",
		"Leg pay of journey checkout (scenario checkout.pay)", "Leg settled of journey checkout (scenario checkout.settled)", "(source diff)")
	contains(t, "stderr", refused.stderr, "nothing was confirmed")
	for _, id := range checkoutLegs {
		if n := len(r.confirmationFiles(t, id)); n != 0 {
			t.Errorf("%s has %d confirmations after a refusal", id, n)
		}
	}

	res := r.must(t, cli.ExitOK, "confirm", "--journey", "checkout", "--yes")
	contains(t, "stdout", res.stdout, "local, unverified identity")
	for _, id := range checkoutLegs {
		if n := len(r.confirmationFiles(t, id)); n != 1 {
			t.Errorf("%s has %d confirmation files, want 1", id, n)
		}
		contains(t, "stdout", res.stdout, "confirmed scenario "+id+" for expectation hash ")
	}

	run := r.must(t, cli.ExitOK, "journey", "run", "checkout")
	contains(t, "stdout", squash(run.stdout), "checkout passed")

	// A rewritten expected result lapses the confirmation of its leg only.
	r.editJourney(t, "checkout", func(s string) string { return strings.Replace(s, "status: 202", "status: 200", 1) })
	again := r.must(t, cli.ExitUndecided, "journey", "run", "checkout")
	contains(t, "stdout", squash(again.stdout), "first failing leg pay")
}

func TestConfirmJourneyUsage(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"unknown":         {[]string{"confirm", "--journey", "nope", "--yes"}, "journey nope was not found"},
		"with --all":      {[]string{"confirm", "--journey", "checkout", "--all"}, "and not several of them"},
		"with a fixture":  {[]string{"confirm", "--journey", "checkout", "--fixture", "x"}, "and not both"},
		"with a scenario": {[]string{"confirm", "--journey", "checkout", "SHOP-482-01"}, "and not several of them"},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.must(t, cli.ExitError, tc.args...)
			contains(t, "stderr", res.stderr, tc.want)
		})
	}
	// The example has no expected result that needs a person.
	res := r.must(t, cli.ExitOK, "confirm", "--journey", "checkout", "--yes")
	contains(t, "stdout", res.stdout, "no expected result of journey checkout needs a confirmation")
}

func TestConfirmJourneyNeedsAnEnvironmentWhenThereAreSeveral(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.requireConfirmation(t)
	r.rewriteConfig(t, func(s string) string {
		const uat = "  uat:\n    kind: test\n    hosts: [\"127.0.0.1:9\"]\n    methods: [GET]\n    effects: {mode: read_only}\n    services:\n      - name: orders\n        baseUrl: http://127.0.0.1:9\n"
		return strings.Replace(s, "authProfiles:\n", uat+"authProfiles:\n", 1)
	})
	res := r.must(t, cli.ExitError, "confirm", "--journey", "checkout", "--yes")
	contains(t, "stderr", res.stderr, "choose an environment: local, uat")
	r.must(t, cli.ExitOK, "confirm", "--journey", "checkout", "--yes", "--env", "local")
}

func TestLedgerReadsJourneyOutcome(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/flow", "An order is placed, paid, and shown as paid", "journeys: [checkout]"),
		entry("SHOP-482/gone", "A journey that no file defines", "journeys: [vanished]")))

	before := r.must(t, cli.ExitUndecided, "ledger")
	contains(t, "the ledger", before.stdout, "journey checkout has no result: run it", "journey vanished was deleted: no scenario or journey covers this")

	run := r.must(t, cli.ExitOK, "journey", "run", "checkout")
	md := strings.Split(r.lastReport(t), "\n")
	if md[2] != "1 use case: 1 verified" {
		t.Errorf("the report line is %q\n%s", md[2], strings.Join(md[:4], "\n"))
	}
	contains(t, "the summary", run.stdout, "1 use case: 1 verified")

	res := r.must(t, cli.ExitUndecided, "ledger", "--env", "local")
	contains(t, "the ledger", res.stdout, "2 use cases: 1 verified, 1 blocked", "Verified (1)", "SHOP-482/flow")

	// A failed journey makes the use case failed, whatever the build.
	r2 := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{Defects: defects(sampleapp.PaymentAmountWrong)})
	r2.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/flow", "An order is placed", "journeys: [checkout]")))
	r2.must(t, cli.ExitFailed, "journey", "run", "checkout")
	failed := r2.must(t, cli.ExitFailed, "ledger")
	contains(t, "the ledger", failed.stdout, "Failed (1)", "journey checkout failed in r-test-0001: invariant 1 is false")
}

func TestLedgerCountsAStaleJourney(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/flow", "An order is placed", "journeys: [checkout]")))
	r.must(t, cli.ExitOK, "journey", "run", "checkout")
	r.must(t, cli.ExitOK, "ledger", "--env", "local")

	// A newer result on another commit: the environment has moved on since.
	pay := r.commitOf(t, r.paymentsURL, "tok-pay")
	old := manifestOf(domain.BuildRef{Service: "orders", Commit: "0ld0ld0"}, domain.BuildRef{Service: "payments", Commit: pay})
	r.storedRun(t, "r-test-9999", old.Services, domain.JourneyResult{Journey: "checkout", Manifest: old, ManifestEnd: &old, Outcome: domain.JourneyPassed,
		Legs: []domain.LegResult{}, Invariants: []domain.InvariantResult{}, Reasons: []domain.Reason{}})
	res := r.must(t, cli.ExitUndecided, "ledger", "--env", "local")
	contains(t, "the ledger", res.stdout, "journey checkout ran on orders 0ld0ld0, and the environment runs 9f3c2ab: rerun on the current build")
}

func TestJourneyRunOfAnInvalidFileShowsItSafely(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.writeRel(t, ".semar/journeys/evil.yaml", "apiVersion: semar/v1\nkind: Journey\nid: evil\n\"x\\e[31mred\\u202eblue\": 1\n")
	for _, args := range [][]string{{"journey", "run", "evil"}, {"journey", "run", "--all"}, {"confirm", "--journey", "evil", "--yes"}} {
		res := r.semar(t, t.Context(), args...)
		if strings.ContainsAny(res.stdout+res.stderr, "\x1b\u202e") {
			t.Errorf("semar %s printed a control character:\n%q", strings.Join(args, " "), res.stderr)
		}
		contains(t, "stderr", res.stderr, ".semar/journeys/evil.yaml:", `x\x1b[31mred`)
	}
}
