package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/report"
)

func docActivity() []domain.Activity {
	use := func(step string) domain.UseCase {
		return domain.UseCase{Pass: domain.PassScenario, Scenario: "PROJ-12-S01", Step: domain.StepID(step)}
	}
	return []domain.Activity{
		{Seq: 1, Use: use("s1"), Target: "GET /orders/42", Doc: &domain.DocCheck{
			Operation: "GET /orders/{id}", Misfits: []domain.Misfit{{Path: "$.id", Want: "integer", Got: "string"}}, MoreMisfits: 1}},
		{Seq: 2, Use: use("s2"), Target: "GET /orders/43", Doc: &domain.DocCheck{Operation: "GET /orders/{id}", Undeclared: true, Status: 418}},
		{Seq: 3, Use: use("s3"), Target: "GET /slow", Doc: &domain.DocCheck{LimitMS: 2000, TookMS: 2300, SlowFail: true}},
		{Seq: 4, Use: use("s4"), Target: "GET /fine"},
	}
}

func TestDocFindingsAndWarnings(t *testing.T) {
	f := report.DocFindings(docActivity())
	if len(f) != 3 {
		t.Fatalf("findings = %+v", f)
	}
	if f[0].Failed || f[1].Failed || !f[2].Failed || f[2].Operation != "GET /slow" {
		t.Errorf("failed marks or operations: %+v", f)
	}
	var out bytes.Buffer
	report.WriteDocWarnings(&out, "shop", f)
	text := out.String()
	for _, want := range []string{
		"response checks: 2 warnings from environments.shop.checks, no outcome changed",
		"PROJ-12-S01 step s1: GET /orders/{id}: the response does not fit the API document: id is a string, and the document says integer (and 1 more)",
		"does not declare status 418",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "2300") {
		t.Errorf("a finding that failed its step was printed as a warning:\n%s", text)
	}
	out.Reset()
	report.WriteDocWarnings(&out, "shop", nil)
	if out.Len() != 0 {
		t.Errorf("printed for no finding: %q", out.String())
	}
}

func TestMarkdownListsEveryFinding(t *testing.T) {
	var md bytes.Buffer
	run := domain.Run{ID: "r-1", Environment: "shop"}
	if err := report.Markdown(&md, report.Data{Run: run, Activity: docActivity()}); err != nil {
		t.Fatal(err)
	}
	text := md.String()
	for _, want := range []string{
		"## Checks against the API document",
		"step s1, GET /orders/{id}",
		"(warning, no outcome changed)",
		"took 2300 ms, over the limit of 2000 ms (failed the step)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report misses %q:\n%s", want, text)
		}
	}
	md.Reset()
	if err := report.Markdown(&md, report.Data{Run: run}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md.String(), "Checks against the API document") {
		t.Error("the section is printed with no finding")
	}
}
