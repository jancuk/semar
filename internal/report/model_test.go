package report_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/report"
)

// modelData is a run of one failed scenario whose first two expected results a model wrote.
func modelData(byModel bool) report.Data {
	t0 := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	bs := []domain.BuildRef{{Service: "orders", Commit: "a1b2c3d", ConfigHash: "sha256:11aa"}}
	checks := []domain.CheckResult{
		{Step: "refund", Index: 0, Source: "ticket", State: domain.CheckFalse, Expected: "status 422", Observed: "status 201", ByModel: byModel},
		{Step: "refund", Index: 1, Source: "ticket", State: domain.CheckFalse, Expected: "a", Observed: "b", ByModel: byModel},
		{Step: "refund", Index: 2, Source: "ticket", State: domain.CheckFalse, Expected: "c", Observed: "d"},
		{Step: "refund", Index: -1, Source: "invariant", State: domain.CheckTrue, Expected: "no server error", Observed: "status 201"},
	}
	exp := func() domain.Expectation {
		return domain.Expectation{Check: domain.Check{Status: intp(422)}, Source: domain.SourceTicket, Cite: "SHOP-482"}
	}
	return report.Data{
		Run: domain.Run{
			Schema: 1, ID: "run-20261114-0002", Environment: "uat", EnvironmentKind: "test",
			StartedAt: t0, EndedAt: t0.Add(time.Minute), BuildsStart: bs, BuildsEnd: bs,
			Results: []domain.ScenarioResult{{
				Scenario: "SHOP-482-01", Outcome: domain.OutcomeFailed,
				Reasons: []domain.Reason{{Rule: domain.OutcomeFailed, Text: "step refund: expected status 422, observed status 201", Step: "refund"}},
				Checks:  checks,
			}},
		},
		Scenarios: map[domain.ScenarioID]domain.Scenario{
			"SHOP-482-01": {ID: "SHOP-482-01", Title: "Refund", Ticket: "SHOP-482", Steps: []domain.Step{{ID: "refund", Expect: []domain.Expectation{exp(), exp(), exp()}}}},
		},
	}
}

func lines(t *testing.T, d report.Data) string {
	t.Helper()
	var b bytes.Buffer
	if err := report.Lines(&b, d.Run); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestTheRunLineCountsTheResultsAModelWrote(t *testing.T) {
	if got := lines(t, modelData(true)); !strings.Contains(got, "(2 of 3 results by model)") {
		t.Errorf("run line lacks the count:\n%s", got)
	}
	if got := lines(t, modelData(false)); strings.Contains(got, "by model") {
		t.Errorf("a run with no model result says by model:\n%s", got)
	}
}

func TestTheReportsShowByModelNextToEachSuchResult(t *testing.T) {
	d := modelData(true)
	md := render(t, d)
	if n := strings.Count(md, "- Source: ticket, cite: SHOP-482, by model\n"); n != 2 {
		t.Errorf("markdown marks %d results, want 2:\n%s", n, md)
	}
	if n := strings.Count(md, "- Source: ticket, cite: SHOP-482\n"); n != 1 {
		t.Errorf("markdown has %d unmarked results, want 1:\n%s", n, md)
	}
	if !strings.Contains(md, "2 of 3 results by model") {
		t.Errorf("markdown lacks the count:\n%s", md)
	}
	var h bytes.Buffer
	if err := report.HTML(&h, d); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(h.String(), ", by model)</span>"); n != 2 {
		t.Errorf("html marks %d results, want 2", n)
	}
	if !strings.Contains(h.String(), "2 of 3 results by model") {
		t.Error("html lacks the count")
	}
}

func TestTheSummaryCountsTheResultsAModelWrote(t *testing.T) {
	var b bytes.Buffer
	if err := report.PipelineSummary(&b, modelData(true), "report.html"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Results written by a model: 2 of 3") {
		t.Errorf("summary lacks the count:\n%s", b.String())
	}
}

func TestAnOldRunShowsNoModelCount(t *testing.T) {
	d := modelData(false)
	var h, s bytes.Buffer
	if err := report.HTML(&h, d); err != nil {
		t.Fatal(err)
	}
	if err := report.PipelineSummary(&s, d, "report.html"); err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"lines": lines(t, d), "markdown": render(t, d), "html": h.String(), "summary": s.String()} {
		if strings.Contains(out, "by model") || strings.Contains(out, "written by a model") {
			t.Errorf("%s mentions a model for a run stored before Plan 36", name)
		}
	}
}
