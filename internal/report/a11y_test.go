package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/report"
)

func a11yActivity() []domain.Activity {
	use := func(step string) domain.UseCase {
		return domain.UseCase{Pass: domain.PassScenario, Scenario: "AND-1", Step: domain.StepID(step)}
	}
	dup := domain.AccessibilityIssue{
		Screen: "Home", Kind: "missing_label", Class: "android.widget.ImageButton", Role: "button",
		ResourceID: "com.example.shop:id/fav", Bounds: "[10,20][110,120]",
	}
	small := domain.AccessibilityIssue{
		Screen: "Home", Kind: "small_target", Class: "android.widget.Button", Role: "button",
		Label: "Close", Bounds: "[900,10][964,74]", WidthDP: 32, HeightDP: 32,
	}
	return []domain.Activity{
		{Seq: 1, Surface: domain.SurfaceAndroid, Use: use("s1"), Accessibility: []domain.AccessibilityIssue{dup, small}},
		{Seq: 2, Surface: domain.SurfaceAndroid, Use: use("s2"), Accessibility: []domain.AccessibilityIssue{dup}},
		{Seq: 3, Surface: domain.SurfaceWeb, Use: use("s3"), Accessibility: []domain.AccessibilityIssue{dup}},
	}
}

func TestA11yFindingsDeduplicateAndWriteWarnings(t *testing.T) {
	findings := report.A11yFindings(a11yActivity())
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want 2 deduplicated", findings)
	}
	var out bytes.Buffer
	report.WriteA11yWarnings(&out, "uat", findings)
	text := out.String()
	for _, want := range []string{
		"accessibility checks: 2 warnings from environments.uat.checks, no outcome changed",
		"AND-1 step s1 (Home): missing label on button id=fav ([10,20][110,120])",
		`AND-1 step s1 (Home): small touch target (32x32dp, min 48dp) on button "Close" ([900,10][964,74])`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output misses %q:\n%s", want, text)
		}
	}
	out.Reset()
	report.WriteA11yWarnings(&out, "uat", nil)
	if out.Len() != 0 {
		t.Errorf("empty findings printed %q", out.String())
	}
}

func TestReportsRenderA11ySectionOnlyWhenPresent(t *testing.T) {
	run := domain.Run{ID: "r-a11y", Environment: "uat"}
	acts := a11yActivity()

	var md bytes.Buffer
	if err := report.Markdown(&md, report.Data{Run: run, Activity: acts}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Accessibility warnings", "missing label on button id=fav", "small touch target (32x32dp, min 48dp)"} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("report.md misses %q:\n%s", want, md.String())
		}
	}

	var h bytes.Buffer
	if err := report.HTML(&h, report.Data{Run: run, Activity: acts}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h2>Accessibility warnings</h2>", "missing label on button id=fav", "32x32dp"} {
		if !strings.Contains(h.String(), want) {
			t.Errorf("report.html misses %q", want)
		}
	}

	var sum bytes.Buffer
	if err := report.PipelineSummary(&sum, report.Data{Run: run, Activity: acts}, "report.html"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sum.String(), "Accessibility: 2 warnings") {
		t.Errorf("summary.md misses accessibility line:\n%s", sum.String())
	}

	// Empty findings must not add any accessibility section or line.
	md.Reset()
	h.Reset()
	sum.Reset()
	if err := report.Markdown(&md, report.Data{Run: run}); err != nil {
		t.Fatal(err)
	}
	if err := report.HTML(&h, report.Data{Run: run}); err != nil {
		t.Fatal(err)
	}
	if err := report.PipelineSummary(&sum, report.Data{Run: run}, "report.html"); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"md": md.String(), "html": h.String(), "summary": sum.String()} {
		if strings.Contains(body, "Accessibility") {
			t.Errorf("%s rendered accessibility with no findings:\n%s", name, body)
		}
	}
}
