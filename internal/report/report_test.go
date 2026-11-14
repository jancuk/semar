package report_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
		Schema: 1, ID: "run-20261114-0001", Environment: "uat",
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
		"Data touched on UAT",
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

func TestMarkdownEmptyLedger(t *testing.T) {
	d := fixture()
	d.Activity = nil
	out := render(t, d)
	if !strings.Contains(out, "Data touched on UAT") || !strings.Contains(out, "Nothing was written") {
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

func TestMarkdownHostileValues(t *testing.T) {
	const hostile = "</details><script>x</script>\n# title | a `b` [l](http://evil)"
	d := fixture()
	r := &d.Run.Results[1]
	r.Checks[0].Observed = hostile
	r.Checks[0].Expected = hostile
	r.Checks[0].Detail = hostile
	r.Reasons[0].Text = hostile
	d.Run.Results[1].Scenario = "S\n## x"
	d.Scenarios["SHOP-482-02"] = domain.Scenario{ID: "SHOP-482-02", Title: hostile}
	d.Run.Environment = hostile
	out := render(t, d)
	for _, bad := range []string{"<script>", "</script>", "</details><", "\n# title", "\n## x", "[l]("} {
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

func TestMarkdownNoBodies(t *testing.T) {
	out := render(t, fixture())
	for _, bad := range []string{"requestBody", "responseBody", "Request body", "Response body"} {
		if strings.Contains(out, bad) {
			t.Errorf("report mentions %q", bad)
		}
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
	if err := report.Summary(&b, fixture().Run); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "summary.golden.txt", b.Bytes())
}

func TestSummaryShape(t *testing.T) {
	run := fixture().Run
	run.Results[1].Reasons[0].Text = "evil \x1b[31mred\x1b[0m\nline"
	var b bytes.Buffer
	if err := report.Summary(&b, run); err != nil {
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
	if err := report.Summary(&b, domain.Run{}); err != nil {
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

func TestCut(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short", "abc", 5, "abc"},
		{"exact", "abcde", 5, "abcde"},
		{"long", "abcdefgh", 5, "abcde… [cut: 3 more characters]"},
		{"runes", "ééééé", 2, "éé… [cut: 3 more characters]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := report.Cut(c.in, c.max); got != c.want {
				t.Errorf("Cut = %q, want %q", got, c.want)
			}
		})
	}
}
