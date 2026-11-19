package report_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/report"
)

var update = flag.Bool("update", false, "rewrite golden files")

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(want, got) {
		return
	}
	t.Errorf("%s differs from golden file (run with -update to accept):\n%s", name, lineDiff(string(want), string(got)))
}

func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < max(len(w), len(g)); i++ {
		var wl, gl string
		wok, gok := i < len(w), i < len(g)
		if wok {
			wl = w[i]
		}
		if gok {
			gl = g[i]
		}
		if wok && gok && wl == gl {
			continue
		}
		if wok {
			fmt.Fprintf(&b, "line %d  want: %q\n", i+1, wl)
		}
		if gok {
			fmt.Fprintf(&b, "line %d  got:  %q\n", i+1, gl)
		}
	}
	return b.String()
}

func intp(n int) *int { return &n }

func fixture() report.Data {
	t0 := time.Date(2026, 11, 14, 9, 30, 0, 0, time.FixedZone("WIB", 7*3600))
	builds := []domain.BuildRef{{Service: "orders", Commit: "a1b2c3d", ConfigHash: "sha256:11aa"}}
	buildsEnd := []domain.BuildRef{{Service: "orders", Commit: "a1b2c3d", ConfigHash: "sha256:11aa"}}
	run := domain.Run{
		Schema: 1, ID: "run-20261114-0001", Environment: "uat", EnvironmentKind: "test",
		StartedAt: t0, EndedAt: t0.Add(95 * time.Second),
		BuildsStart: builds, BuildsEnd: buildsEnd,
		Results: []domain.ScenarioResult{
			{Scenario: "SHOP-482-04", Outcome: domain.OutcomePassed},
			{Scenario: "SHOP-482-01", Outcome: domain.OutcomeFailed,
				Reasons: []domain.Reason{
					{Rule: domain.OutcomeFailed, Text: "refund-too-much: expected 422, got 201", Step: "refund"},
					{Rule: domain.OutcomeFailed, Text: "a check with a confirmed source failed", Step: "refund"},
				},
				Checks: []domain.CheckResult{
					{Step: "refund", Index: 0, Source: "ticket", State: domain.CheckFalse, Expected: "status 422", Observed: "status 201", Confirmed: true},
					{Step: "refund", Index: 1, Source: "testcase", State: domain.CheckTrue, Expected: "$.error equals over_limit", Observed: "over_limit"},
				}},
			{Scenario: "SHOP-482-02", Outcome: domain.OutcomeInconclusive,
				Reasons: []domain.Reason{{Rule: domain.OutcomeInconclusive, Text: "a check could not be evaluated", Step: "list"}},
				Checks: []domain.CheckResult{
					{Step: "list", Index: 0, Source: "baseline", State: domain.CheckNotEvaluated, Expected: "$.items count 3", Observed: "", Detail: "path $.items not found"},
				}},
			{Scenario: "SHOP-482-03", Outcome: domain.OutcomeBlocked,
				Reasons: []domain.Reason{{Rule: domain.OutcomeBlocked, Text: "setup step failed: order was not created", Step: "create-order"}}},
			{Scenario: "SHOP-482-05", Outcome: domain.OutcomePassed},
		},
	}
	scenarios := map[domain.ScenarioID]domain.Scenario{
		"SHOP-482-01": {ID: "SHOP-482-01", Title: "Refund above the paid amount is refused", Steps: []domain.Step{{
			ID: "refund", Expect: []domain.Expectation{
				{Check: domain.Check{Status: intp(422)}, Source: domain.SourceTicket, Cite: "SHOP-482 AC2"},
				{Check: domain.Check{JSONPath: "$.error", Equals: "over_limit"}, Source: domain.SourceTestcase},
			}}}},
		"SHOP-482-02": {ID: "SHOP-482-02", Title: "Order list"},
		"SHOP-482-03": {ID: "SHOP-482-03", Title: "Partial refund"},
		"SHOP-482-04": {ID: "SHOP-482-04", Title: "Full refund"},
		"SHOP-482-05": {ID: "SHOP-482-05", Title: "Refund of a cancelled order"},
	}
	acts := []domain.Activity{
		{Seq: 1, Verb: domain.VerbCreate, Result: "201", Object: &domain.ObjectRef{Type: "order", ID: "ord-1"}, Use: domain.UseCase{Scenario: "SHOP-482-01", Step: "create-order"}},
		{Seq: 2, Verb: domain.VerbCreate, Result: "201", Object: &domain.ObjectRef{Type: "refund", ID: "rf-9"}, Use: domain.UseCase{Scenario: "SHOP-482-01", Step: "refund"}},
		{Seq: 3, Verb: domain.VerbDelete, Result: "204", Object: &domain.ObjectRef{Type: "order", ID: "ord-1"}, Use: domain.UseCase{Scenario: "SHOP-482-01", Step: "cleanup"}},
	}
	return report.Data{Run: run, Scenarios: scenarios, Activity: acts}
}

func render(t *testing.T, d report.Data) string {
	t.Helper()
	var b bytes.Buffer
	if err := report.Markdown(&b, d); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestMarkdownGolden(t *testing.T) {
	compareGolden(t, "report.golden.md", []byte(render(t, fixture())))
}

func declaredFixture() report.Data {
	d := fixture()
	declared := []domain.BuildRef{{Service: "orders", Commit: "0123456789ab", Declared: true}}
	d.Run.BuildsStart, d.Run.BuildsEnd = declared, slices.Clone(declared)
	d.Run.Environment, d.Run.EnvironmentKind = "prod", "production"
	return d
}

func TestMarkdownWithADeclaredBuildGolden(t *testing.T) {
	compareGolden(t, "report.declared.golden.md", []byte(render(t, declaredFixture())))
}

func TestMarkdownSaysWhatADeclaredBuildCannotShow(t *testing.T) {
	const sentence = "A declared build cannot show that the service was redeployed during the run."
	d := declaredFixture()
	if got := render(t, d); !strings.Contains(got, sentence) {
		t.Errorf("a run with only declared builds lacks the sentence:\n%s", got)
	}
	d.Run.BuildsEnd = append(d.Run.BuildsEnd, domain.BuildRef{Service: "billing", Commit: "c0ffee1"})
	if got := render(t, d); strings.Contains(got, sentence) || !strings.Contains(got, "billing c0ffee1 config") {
		t.Errorf("a run with a probed build:\n%s", got)
	}
	if got := render(t, fixture()); strings.Contains(got, sentence) || strings.Contains(got, "declared") {
		t.Errorf("a run with no declared build:\n%s", got)
	}
}

func TestSummaryShowsADeclaredBuild(t *testing.T) {
	var b bytes.Buffer
	if err := report.Summary(&b, declaredFixture().Run, ""); err != nil {
		t.Fatal(err)
	}
	want := "build: orders 0123456789ab (declared on the command line, not read from the service)\n"
	if !strings.HasSuffix(b.String(), want) {
		t.Errorf("summary ends with %q, want %q", b.String(), want)
	}
}

func TestMarkdownDeterministic(t *testing.T) {
	d := fixture()
	first := render(t, d)
	for i := 0; i < 5; i++ {
		if got := render(t, d); got != first {
			t.Fatalf("render %d differs from the first", i)
		}
	}
}

func TestMarkdownOrder(t *testing.T) {
	out := render(t, fixture())
	order := []string{"SHOP-482-01", "SHOP-482-03", "SHOP-482-02", "<details>", "SHOP-482-04", "SHOP-482-05"}
	last := -1
	for _, s := range order {
		i := strings.Index(out, s)
		if i < 0 {
			t.Fatalf("%q missing from report", s)
		}
		if i <= last {
			t.Errorf("%q is out of order", s)
		}
		last = i
	}
	if n := strings.Count(out, "<details>"); n != 1 {
		t.Errorf("want one details block, got %d", n)
	}
	for _, want := range []string{
		"Reproduce: semar run --scenario SHOP-482-01",
		"Reproduce: semar run --scenario SHOP-482-02",
		"Reproduce: semar run --scenario SHOP-482-03",
		"a check with a confirmed source failed",
		"refund-too-much: expected 422, got 201",
		"status 422", "status 201", "ticket", "SHOP-482 AC2",
		"Data touched",
		"2026-11-14T02:30:00Z", "2026-11-14T02:31:35Z",
		"run-20261114-0001",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.Contains(out, "Reproduce: semar run --scenario SHOP-482-04") {
		t.Error("a passed scenario must not carry a reproduce line")
	}
}

func TestMarkdownStaleAndNotRunOrder(t *testing.T) {
	d := report.Data{Run: domain.Run{ID: "r", Results: []domain.ScenarioResult{
		{Scenario: "S-5", Outcome: domain.OutcomeNotRun},
		{Scenario: "S-4", Outcome: domain.OutcomeStale},
		{Scenario: "S-3", Outcome: domain.OutcomeInconclusive},
		{Scenario: "S-2", Outcome: domain.OutcomeBlocked},
		{Scenario: "S-1", Outcome: domain.OutcomeFailed},
		{Scenario: "S-6", Outcome: domain.OutcomeFailed},
	}}}
	out := render(t, d)
	last := -1
	for _, id := range []string{"S-1", "S-6", "S-2", "S-3", "S-4", "S-5"} {
		i := strings.Index(out, "### "+id)
		if i < 0 || i <= last {
			t.Fatalf("%s missing or out of order in:\n%s", id, out)
		}
		last = i
	}
}

func TestMarkdownEnvironmentOfARecordWithoutAKind(t *testing.T) {
	d := fixture()
	d.Run.EnvironmentKind = ""
	if out := render(t, d); !strings.Contains(out, "- Environment: uat\n") {
		t.Errorf("a record with no kind must show the name alone:\n%s", out)
	}
}

func TestMarkdownEmptyLedger(t *testing.T) {
	d := fixture()
	d.Activity = nil
	out := render(t, d)
	if !strings.Contains(out, "Data touched") || !strings.Contains(out, "Nothing was written") {
		t.Errorf("empty ledger must say nothing was written:\n%s", out)
	}
}

func TestMarkdownLedgerRows(t *testing.T) {
	out := render(t, fixture())
	for _, want := range []string{"order ord-1", "cleaned up", "refund rf-9", "left in place"} {
		if !strings.Contains(out, want) {
			t.Errorf("ledger lacks %q:\n%s", want, out)
		}
	}
}

func TestMarkdownEmptyRun(t *testing.T) {
	out := render(t, report.Data{Run: domain.Run{ID: "r-empty"}})
	if !strings.Contains(out, "r-empty") || !strings.Contains(out, "No scenario") {
		t.Errorf("empty run report:\n%s", out)
	}
	if strings.Contains(out, "<details>") {
		t.Error("no details block without passed scenarios")
	}
}

func TestMarkdownInterrupted(t *testing.T) {
	d := fixture()
	d.Run.EndedAt = time.Time{}
	out := render(t, d)
	if !strings.Contains(out, "did not finish") {
		t.Errorf("a run without an end time must say it did not finish:\n%s", out)
	}
	if strings.Contains(render(t, fixture()), "did not finish") {
		t.Error("a finished run must not say it did not finish")
	}
}

// hostile is text that would break out of a line of Markdown, open a link, start math, or reorder the text.
// The name stays readable in the output, so a test can tell that the value was shown.
func hostile(name string) string {
	return "</details><script>" + name + "</script>\n# " + name + " | a `b` [l](http://evil.example/" + name + ") " +
		"https://evil.example www.evil.example $x$ \u202e\u200b"
}

func TestMarkdownHostileValues(t *testing.T) {
	d := fixture()
	var names []string
	h := func(name string) string {
		names = append(names, name)
		return hostile(name)
	}
	failed := &d.Run.Results[1]
	step := h("step")
	failed.Checks[0].Step = domain.StepID(step)
	failed.Reasons[0].Step, failed.Reasons[1].Step = domain.StepID(step), domain.StepID(step)
	failed.Checks[0].Observed = h("observed")
	failed.Checks[0].Expected = h("expected")
	failed.Checks[0].Detail = h("detail")
	failed.Checks[0].Source = h("source")
	failed.Reasons[0].Text = h("reason")
	sc := d.Scenarios["SHOP-482-01"]
	sc.Title = h("title")
	sc.Steps[0].ID = domain.StepID(step)
	sc.Steps[0].Expect[0].Cite = h("cite")
	d.Scenarios["SHOP-482-01"] = sc
	for _, i := range []int{0, 4} {
		passed := d.Scenarios[d.Run.Results[i].Scenario]
		passed.Title = h("passedtitle")
		d.Scenarios[d.Run.Results[i].Scenario] = passed
	}
	d.Run.Results[2].Scenario = domain.ScenarioID(h("scenarioid"))
	d.Run.Environment = h("environment")
	d.Run.ID = domain.RunID(h("runid"))
	d.Run.BuildsStart[0].Service = h("buildservice")
	d.Run.BuildsStart[0].Commit = h("buildcommit")
	d.Run.BuildsEnd[0].ConfigHash = h("buildconfig")
	for i := range d.Activity {
		d.Activity[i].Object.Type = h("objecttype")
		d.Activity[i].Object.ID = h("objectid")
		d.Activity[i].Use.Scenario = domain.ScenarioID(h("ledgerscenario"))
		d.Activity[i].Use.Step = domain.StepID(h("ledgerstep"))
	}
	out := render(t, d)

	for _, bad := range []string{
		"<script>", "</script>", "</details><", "[l](", "http://", "https://", "www.", "$x$", "\u202e", "\u200b",
	} {
		if strings.Contains(out, bad) {
			t.Errorf("report contains %q:\n%s", bad, out)
		}
	}
	if n := strings.Count(out, "<details>"); n != 1 {
		t.Errorf("details opened %d times", n)
	}
	if n := strings.Count(out, "</details>"); n != 1 {
		t.Errorf("details closed %d times", n)
	}
	for _, name := range names {
		if !strings.Contains(out, name) {
			t.Errorf("the report lost the value %q, which the test needs to see neutralised", name)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "# Semar report") && !strings.HasPrefix(line, "### ") && !strings.HasPrefix(line, "## Data touched") {
			t.Errorf("a value started a heading: %q", line)
		}
	}
}

func TestMarkdownCutsEveryLongValue(t *testing.T) {
	long := strings.Repeat("y", 5000)
	d := fixture()
	d.Run.ID = domain.RunID(long)
	d.Run.Environment = long
	d.Run.BuildsStart[0].Service = long
	d.Run.Results[1].Scenario = domain.ScenarioID(long)
	d.Run.Results[1].Reasons[0].Text = long
	d.Run.Results[1].Reasons[0].Step = domain.StepID(long)
	d.Activity[0].Object.ID = long
	sc := d.Scenarios["SHOP-482-01"]
	sc.Title = long
	d.Scenarios[domain.ScenarioID(long)] = sc
	d.Scenarios["SHOP-482-02"] = domain.Scenario{ID: "SHOP-482-02", Title: long}
	out := render(t, d)
	if strings.Contains(out, strings.Repeat("y", 300)) {
		t.Error("a long value was not cut")
	}
	if n := strings.Count(out, "[cut: "); n < 7 {
		t.Errorf("%d cut marks, want one for each of the 7 long values:\n%s", n, out)
	}
}

func TestSummaryCutsAndCleansTheReason(t *testing.T) {
	run := fixture().Run
	run.Results[1].Reasons[0].Text = "a\u202eb" + strings.Repeat("z", 5000)
	var b bytes.Buffer
	if err := report.Summary(&b, run, ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "\u202e") {
		t.Error("the summary holds a bidi control")
	}
	if strings.Contains(out, strings.Repeat("z", 300)) || !strings.Contains(out, "[cut: ") {
		t.Errorf("the reason was not cut:\n%s", out)
	}
}

func TestMarkdownCutsLongValues(t *testing.T) {
	d := fixture()
	d.Run.Results[1].Checks[0].Observed = strings.Repeat("x", 5000)
	out := render(t, d)
	if strings.Contains(out, strings.Repeat("x", 300)) {
		t.Error("observed value was not cut")
	}
	if !strings.Contains(out, strings.Repeat("x", 200)) {
		t.Error("the first 200 characters must stay")
	}
	if !strings.Contains(out, "[cut: 4800 more characters]") {
		t.Errorf("cut mark missing:\n%s", out)
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		name     string
		outcomes []domain.Outcome
		want     int
	}{
		{"all passed", []domain.Outcome{"passed", "passed"}, 0},
		{"failed wins", []domain.Outcome{"passed", "failed", "inconclusive"}, 1},
		{"inconclusive", []domain.Outcome{"passed", "inconclusive"}, 2},
		{"blocked", []domain.Outcome{"blocked"}, 2},
		{"stale", []domain.Outcome{"stale"}, 2},
		{"not run", []domain.Outcome{"not_run"}, 2},
		{"no results", nil, 2},
		{"unknown is not passed", []domain.Outcome{"passed", "flaky"}, 2},
		{"empty outcome is not passed", []domain.Outcome{""}, 2},
		{"failed beats unknown", []domain.Outcome{"flaky", "failed"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var rs []domain.ScenarioResult
			for _, o := range c.outcomes {
				rs = append(rs, domain.ScenarioResult{Outcome: o})
			}
			if got := report.ExitCode(rs); got != c.want {
				t.Errorf("ExitCode = %d, want %d", got, c.want)
			}
		})
	}
}

func TestSummaryGolden(t *testing.T) {
	var b bytes.Buffer
	if err := report.Summary(&b, fixture().Run, ""); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "summary.golden.txt", b.Bytes())
}

func TestSummaryShape(t *testing.T) {
	run := fixture().Run
	run.Results[1].Reasons[0].Text = "evil \x1b[31mred\x1b[0m\nline"
	var b bytes.Buffer
	if err := report.Summary(&b, run, ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "\x1b") {
		t.Error("summary must carry no escape sequences")
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(run.Results)+1 {
		t.Fatalf("want %d lines, got %d:\n%s", len(run.Results)+1, len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "SHOP-482-01") {
		t.Errorf("first line must be the failed scenario: %q", lines[0])
	}
}

func TestSummaryEmptyRun(t *testing.T) {
	var b bytes.Buffer
	if err := report.Summary(&b, domain.Run{}, ""); err != nil {
		t.Fatal(err)
	}
	want := "failed 0, blocked 0, inconclusive 0, stale 0, not_run 0, passed 0\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestEscape(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "refund-too-much: 422", "refund-too-much: 422"},
		{"html", "<b>&</b>", "&lt;b&gt;&amp;&lt;/b&gt;"},
		{"backtick", "a`b", "a\\`b"},
		{"pipe", "a|b", "a\\|b"},
		{"newline", "a\nb\r\nc", "a↵b↵c"},
		{"heading after newline", "x\n# t", "x↵\\# t"},
		{"backslash", `a\b`, `a\\b`},
		{"link", "[x](u)", `\[x\](u)`},
		{"emphasis", "*a* _b_", `\*a\* \_b\_`},
		{"control", "a\x1b[0mb\x00", "a \\[0mb "},
		{"intraword underscore", "not_run _x_", `not_run \_x\_`},
		{"math", "costs $5 and $x$", `costs \$5 and \$x\$`},
		{"http link", "see http://evil.example/x", `see http\://evil.example/x`},
		{"https link", "HTTPS://evil.example", `HTTPS\://evil.example`},
		{"www link", "www.evil.example and WWW.evil.example", `www\.evil.example and WWW\.evil.example`},
		{"bidi control", "a\u202eb\u200bc\ufeffd", "abcd"},
		{"bidi control inside a scheme", "http:\u200b//evil.example www\u202e.evil.example", `http\://evil.example www\.evil.example`},
		{"mention and address", "@octocat a@b.example", `\@octocat a\@b.example`},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := report.Escape(c.in); got != c.want {
				t.Errorf("Escape(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestShown(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"short", "abc", "abc"},
		{"exact", strings.Repeat("a", 200), strings.Repeat("a", 200)},
		{"long", strings.Repeat("a", 205), strings.Repeat("a", 200) + "… [cut: 5 more characters]"},
		{"runes", strings.Repeat("é", 203), strings.Repeat("é", 200) + "… [cut: 3 more characters]"},
		{"escapes after the cut", strings.Repeat("a", 199) + "|b", strings.Repeat("a", 199) + `\|… [cut: 1 more characters]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := report.Shown(c.in); got != c.want {
				t.Errorf("Shown = %q, want %q", got, c.want)
			}
		})
	}
}

func givenReport() report.Data {
	t0 := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	hash := func(c string) domain.Hash { return domain.Hash(strings.Repeat(c, 64)) }
	run := domain.Run{
		Schema: 1, ID: "r-given", Environment: "local", EnvironmentKind: "test",
		StartedAt: t0, EndedAt: t0.Add(time.Minute),
		Results: []domain.ScenarioResult{
			{Scenario: "SHOP-482-05", Outcome: domain.OutcomePassed, Fixtures: []domain.FixtureRecord{{
				As: "order", Fixture: "order.paid", Version: "1.0.0", Hash: hash("a"), Via: "create", Cleanup: "failed",
				Left: []string{"order created by fixture order.paid (as order) may still exist: cleanup step 1 (POST /v1/orders/{{id}}/cancel) answered 500"},
			}}},
			{Scenario: "SHOP-482-06", Outcome: domain.OutcomePassed, Fixtures: []domain.FixtureRecord{{
				As: "order", Fixture: "order.paid", Version: "1.0.0", Hash: hash("b"), Via: "find", Cleanup: "not_needed",
			}}},
			{Scenario: "SHOP-482-07", Outcome: domain.OutcomeBlocked,
				Reasons: []domain.Reason{{Rule: domain.OutcomeBlocked, Text: "fixture order.paid: create[0]: POST /v1/orders is on the deny list of uat"}},
				Fixtures: []domain.FixtureRecord{{
					As: "order", Fixture: "order.paid", Version: "1.0.0", Hash: hash("c"), Via: "none", Cleanup: "not_needed",
				}}},
		},
	}
	scenarios := map[domain.ScenarioID]domain.Scenario{
		"SHOP-482-05": {ID: "SHOP-482-05", Title: "Refund of a paid order"},
		"SHOP-482-06": {ID: "SHOP-482-06", Title: "Refund of a found order"},
		"SHOP-482-07": {ID: "SHOP-482-07", Title: "Refund with a denied fixture"},
	}
	return report.Data{Run: run, Scenarios: scenarios}
}

func TestReportStillExists(t *testing.T) {
	out := render(t, givenReport())
	compareGolden(t, "report.given.golden.md", []byte(out))
	for _, want := range []string{
		"Given order: order.paid 1.0.0 (hash aaaaaaaaaaaa), made by create; cleanup failed",
		"Given order: order.paid 1.0.0 (hash bbbbbbbbbbbb), found by find; cleanup not needed",
		"Given order: order.paid 1.0.0 (hash cccccccccccc), no state was given",
		"## Still exists",
		"SHOP-482-05: order created by fixture order.paid",
		"answered 500",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestReportWithoutLeftRecordsHasNoStillExists(t *testing.T) {
	d := givenReport()
	d.Run.Results[0].Fixtures[0].Left = nil
	d.Run.Results[0].Fixtures[0].Cleanup = "done"
	out := render(t, d)
	if strings.Contains(out, "Still exists") {
		t.Errorf("a run with nothing left shows the section:\n%s", out)
	}
	if !strings.Contains(out, "made by create; cleanup done") {
		t.Errorf("the cleanup word is missing:\n%s", out)
	}
	if got := render(t, fixture()); strings.Contains(got, "Still exists") || strings.Contains(got, "Given ") {
		t.Errorf("a run without given entries shows fixture lines:\n%s", got)
	}
}

func TestReportFixtureValuesAreEscaped(t *testing.T) {
	d := givenReport()
	rec := &d.Run.Results[0].Fixtures[0]
	rec.As = "a\n# head"
	rec.Left = []string{"<script>x</script>\n## Injected [l](http://evil.example/)"}
	out := render(t, d)
	for _, bad := range []string{"<script>", "[l](", "http://"} {
		if strings.Contains(out, bad) {
			t.Errorf("report contains %q:\n%s", bad, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "# Semar report") && !strings.HasPrefix(line, "### ") &&
			!strings.HasPrefix(line, "## Data touched") && !strings.HasPrefix(line, "## Still exists") {
			t.Errorf("a value started a heading: %q", line)
		}
	}
}

const coverageLine = "42 use cases: 35 verified, 2 failed, 3 blocked, 2 need a person"

func coverageReport() report.Data {
	d := fixture()
	d.Coverage = coverageLine
	d.CoverageTickets = []string{"SHOP-482", "refunds"}
	return d
}

func TestReportOpensWithCoverageLine(t *testing.T) {
	out := render(t, coverageReport())
	compareGolden(t, "report.coverage.golden.md", []byte(out))
	lines := strings.Split(out, "\n")
	if lines[0] != "# Semar report run-20261114-0001" || lines[1] != "" || lines[2] != coverageLine {
		t.Errorf("the report does not open with the title and the summary line:\n%s", strings.Join(lines[:4], "\n"))
	}
	if lines[3] != "Details: `semar status --ticket SHOP-482 --ticket refunds`" {
		t.Errorf("the line after the summary is %q", lines[3])
	}
	if n := strings.Count(out, "use cases"); n != 1 {
		t.Errorf("the report holds the summary line %d times", n)
	}
}

func TestReportWithoutCoverageIsTheOldReport(t *testing.T) {
	d := fixture()
	d.CoverageTickets = []string{"SHOP-482"}
	if got := render(t, d); got != render(t, fixture()) {
		t.Errorf("tickets alone changed the report:\n%s", lineDiff(render(t, fixture()), got))
	}
	for _, name := range []string{"report.golden.md", "report.declared.golden.md", "report.given.golden.md"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "use case") || strings.Contains(string(data), "semar status") {
			t.Errorf("%s mentions the coverage", name)
		}
	}
}

func TestReportCoverageStaysOneLine(t *testing.T) {
	d := coverageReport()
	d.Coverage = "1 use case: 1 verified\n# Injected <b>x</b> [l](http://evil.example/)"
	d.CoverageTickets = []string{"SHOP-482 `x`", "bad\n--ticket", "SHOP-480"}
	out := render(t, d)
	for _, bad := range []string{"<b>", "[l](", "http://", "\n# Injected", "`x`", "bad"} {
		if strings.Contains(out, bad) {
			t.Errorf("report contains %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "Details: `semar status --ticket SHOP-480`") {
		t.Errorf("a safe ticket was dropped:\n%s", out)
	}
}

func TestReportCoverageOnAnInterruptedRun(t *testing.T) {
	d := coverageReport()
	d.Run.EndedAt = time.Time{}
	lines := strings.Split(render(t, d), "\n")
	if lines[2] != coverageLine || !strings.HasPrefix(lines[5], "> This run did not finish") {
		t.Errorf("the summary line does not come before the note about an unfinished run:\n%s", strings.Join(lines[:7], "\n"))
	}
}

func TestSummaryHasCoverageLine(t *testing.T) {
	var b bytes.Buffer
	run := declaredFixture().Run
	if err := report.Summary(&b, run, coverageLine); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "summary.coverage.golden.txt", b.Bytes())
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	n := len(lines)
	if lines[n-2] != coverageLine || !strings.HasPrefix(lines[n-1], "build: ") || !strings.HasPrefix(lines[n-3], "failed ") {
		t.Errorf("the line is not between the counts and the builds:\n%s", b.String())
	}
	var without bytes.Buffer
	if err := report.Summary(&without, run, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without.String(), "use case") {
		t.Errorf("a run with no obligation prints a coverage line:\n%s", without.String())
	}
}

func TestSummaryCoverageLineIsPlain(t *testing.T) {
	var b bytes.Buffer
	if err := report.Summary(&b, fixture().Run, "1 use case\x1b[2J: 1 verified\nsecond \u202eline"); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(b.String(), "\x1b\u202e") || strings.Contains(b.String(), "\nsecond") {
		t.Errorf("the coverage line is not plain:\n%q", b.String())
	}
}
