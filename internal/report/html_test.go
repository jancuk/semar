package report_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/report"
)

type fakeFiles struct {
	actions map[string]domain.Action
	files   map[string][]byte
}

func (f fakeFiles) Action(rel string) (domain.Action, error) {
	a, ok := f.actions[rel]
	if !ok {
		return domain.Action{}, fmt.Errorf("no action %s", rel)
	}
	return a, nil
}

func (f fakeFiles) File(rel string, limit int64) ([]byte, error) {
	b, ok := f.files[rel]
	if !ok {
		return nil, fmt.Errorf("no file %s", rel)
	}
	return b, nil
}

// jpeg is the start of a JPEG file, which is all that the sniffing of the report reads.
var jpeg = []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0}

func htmlData() report.Data {
	hostile := `"><img src=x onerror=alert(1)>`
	run := domain.Run{ID: "r-1", Environment: "local", StartedAt: time.Unix(1700000000, 0), EndedAt: time.Unix(1700000060, 0), Results: []domain.ScenarioResult{
		{Scenario: "PAY-1-01", Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{}},
		{Scenario: "PAY-1-02", Outcome: domain.OutcomeFailed, Reasons: []domain.Reason{{Rule: domain.OutcomeFailed, Text: "step pay: expected status 200, observed status 503", Step: "pay"}},
			Checks:  []domain.CheckResult{{Step: "pay", Index: 0, Source: "ticket", State: domain.CheckFalse, Expected: "status equals 200", Observed: "503"}},
			Actions: []domain.ActionID{}},
		{Scenario: "PAY-1-03", Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{},
			Attempts: []domain.Attempt{{Outcome: domain.OutcomeFailed}, {Outcome: domain.OutcomePassed}}, AttemptsAllowed: 3, Flaky: true},
		{Scenario: "WEB-1-01", Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{}},
	}}
	scenarios := map[domain.ScenarioID]domain.Scenario{
		"PAY-1-01": {ID: "PAY-1-01", Title: "Pay an order", Ticket: "PROJ-12", Steps: []domain.Step{{ID: "pay", Action: "Pay the order"}}},
		"PAY-1-02": {ID: "PAY-1-02", Title: hostile, Ticket: "PROJ-12", Steps: []domain.Step{{ID: "pay", Action: "Pay the order"}}},
		"PAY-1-03": {ID: "PAY-1-03", Title: "Retry a payment", Ticket: "PROJ-13"},
		"WEB-1-01": {ID: "WEB-1-01", Title: "Open the board", Ticket: "PROJ-14", Steps: []domain.Step{{ID: "open", Action: "Open the board"}, {ID: "read", Action: "Read the board"}}},
	}
	acts := []domain.Activity{
		{Seq: 1, Surface: domain.SurfaceAPI, Use: domain.UseCase{Scenario: "PAY-1-02", Step: "pay"}, Evidence: "exchanges/PAY-1-02/0001-pay.json"},
		{Seq: 2, Surface: domain.SurfaceWeb, Use: domain.UseCase{Scenario: "WEB-1-01", Step: "open"}, Target: "open /b", Result: "done", Screenshot: "screenshots/WEB-1-01/0002-open.jpg"},
		{Seq: 3, Surface: domain.SurfaceWeb, Use: domain.UseCase{Scenario: "WEB-1-01", Step: "read"}, Target: "wait Board", Result: "done", Screenshot: "screenshots/WEB-1-01/0003-read.jpg"},
	}
	files := fakeFiles{
		actions: map[string]domain.Action{"exchanges/PAY-1-02/0001-pay.json": {
			Request:  &domain.HTTPRequest{Method: "POST", URL: "https://shop.example/pay", Body: domain.Body(`{"note":"</pre><script>alert(1)</script>"}`)},
			Response: &domain.HTTPResponse{Status: 503, Headers: map[string][]string{"Content-Type": {"text/html"}}, Body: domain.Body(`<script>alert("x")</script><p>down</p>`)},
		}},
		files: map[string][]byte{
			"screenshots/WEB-1-01/0002-open.jpg": jpeg,
			"screenshots/WEB-1-01/0003-read.jpg": jpeg,
		},
	}
	return report.Data{Run: run, Scenarios: scenarios, Activity: acts, Files: files, RunDir: "/runs/r-1"}
}

func renderHTML(t *testing.T, d report.Data) string {
	t.Helper()
	var b bytes.Buffer
	if err := report.HTML(&b, d); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestHTMLShowsStoredTextAsTextAndNeverAsMarkup(t *testing.T) {
	out := renderHTML(t, htmlData())
	for _, bad := range []string{"<script>", `"><img`, "<p>down</p>", "</pre><script"} {
		if strings.Contains(out, bad) {
			t.Errorf("the page holds %q as markup", bad)
		}
	}
	for _, want := range []string{
		`&#34;&gt;&lt;img src=x onerror=alert(1)&gt;`,
		`&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;&lt;p&gt;down&lt;/p&gt;`,
		`&lt;/pre&gt;&lt;script&gt;alert(1)&lt;/script&gt;`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks the escaped text %q", want)
		}
	}
	if n := strings.Count(out, "<script"); n != 0 {
		t.Errorf("the page has %d script elements", n)
	}
}

func TestHTMLAsksForNothingFromTheNetwork(t *testing.T) {
	out := renderHTML(t, htmlData())
	if !strings.Contains(out, "default-src 'none'") {
		t.Error("the page does not forbid a request with a content security policy")
	}
	for _, m := range regexp.MustCompile(`(?i)(src|href)="([^"]*)"`).FindAllStringSubmatch(out, -1) {
		if !strings.HasPrefix(m[2], "data:image/") {
			t.Errorf("the page points at %s=%q", m[1], m[2][:min(len(m[2]), 40)])
		}
	}
	if strings.Contains(out, "@import") || strings.Contains(out, "<link") {
		t.Error("the page loads a style from outside")
	}
}

func TestHTMLListsTheScenariosThatNeedAttentionFirstAndOpen(t *testing.T) {
	out := renderHTML(t, htmlData())
	attention, passed := strings.Index(out, "Needs attention (2)"), strings.Index(out, "Passed (2)")
	if attention < 0 || passed < attention {
		t.Fatalf("the sections are not in order: attention at %d, passed at %d", attention, passed)
	}
	failed, flaky, ok := strings.Index(out, "PAY-1-02"), strings.Index(out, "PAY-1-03"), strings.Index(out, "PAY-1-01")
	if failed < attention || flaky < failed || flaky > passed || ok < passed {
		t.Errorf("the scenarios are not in order: failed %d, flaky %d, passed %d", failed, flaky, ok)
	}
	section := out[attention:passed]
	if strings.Count(section, "<details open>") < 2 {
		t.Errorf("a scenario that needs attention is closed:\n%s", section)
	}
	if strings.Contains(out[passed:], "<details open>") {
		t.Error("a passed scenario is open")
	}
	for _, want := range []string{"PROJ-12", "PROJ-13", "flaky, passed on attempt 2 of 3", "Exit code", "At least one scenario failed."} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
}

func TestHTMLShowsTheCheckTheExchangeAndTheCountsOfAFailedScenario(t *testing.T) {
	out := renderHTML(t, htmlData())
	for _, want := range []string{"Expected: <code>status equals 200</code>", "Received: <code>503</code>", "POST https://shop.example/pay", "HTTP 503", "Content-Type: text/html"} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
}

func TestHTMLEmbedsTheScreenshotsOfAnyScenarioThatNeedsAttentionAndTheLastOfAPassedOne(t *testing.T) {
	d := htmlData()
	uri := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)
	out := renderHTML(t, d)
	if n := strings.Count(out, uri); n != 1 {
		t.Errorf("a passed web scenario embeds %d screenshots, want its last one only", n)
	}
	if !strings.Contains(out, "1 screenshot(s) are not in this file") || !strings.Contains(out, "/runs/r-1/screenshots") {
		t.Errorf("the page does not say that a screenshot was left out, or where it is")
	}
	if !strings.Contains(out, "screenshots/WEB-1-01/0002-open.jpg") {
		t.Error("the page does not name the screenshot that it left out")
	}

	d.Run.Results[3].Outcome = domain.OutcomeFailed
	out = renderHTML(t, d)
	if n := strings.Count(out, uri); n != 2 {
		t.Errorf("a failed web scenario embeds %d screenshots, want both", n)
	}
	if strings.Contains(out, "are not in this file") {
		t.Error("the page says a screenshot was left out when none was")
	}
}

func TestHTMLDoesNotEmbedAFileThatIsNotAnImage(t *testing.T) {
	d := htmlData()
	d.Files.(fakeFiles).files["screenshots/WEB-1-01/0003-read.jpg"] = []byte("<script>alert(1)</script>")
	out := renderHTML(t, d)
	if strings.Contains(out, "data:text") || strings.Contains(out, "PHNjcmlwdD4") {
		t.Error("the page embeds a file that is not an image")
	}
}

func TestHTMLReadsNoEvidenceWithoutFiles(t *testing.T) {
	d := htmlData()
	d.Files = nil
	out := renderHTML(t, d)
	if strings.Contains(out, "data:image") || strings.Contains(out, "POST https") {
		t.Error("the page holds evidence that it could not read")
	}
}

func TestHTMLOfARunThatDidNotFinish(t *testing.T) {
	d := htmlData()
	d.Run.EndedAt = time.Time{}
	out := renderHTML(t, d)
	if !strings.Contains(out, "This run did not finish") || !strings.Contains(out, "did not finish.") {
		t.Error("a run with no end time is not marked as unfinished, and its exit code is not 2")
	}
}

func TestTheSummaryIsShortAndListsWhatDidNotPass(t *testing.T) {
	d := htmlData()
	var b bytes.Buffer
	if err := report.PipelineSummary(&b, d, "/out/report.html"); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if n := strings.Count(out, "\n"); n > 30 {
		t.Errorf("the summary has %d lines", n)
	}
	for _, want := range []string{"| PAY-1-02 | failed |", "| PAY-1-03 | flaky | flaky, passed on attempt 2 of 3 |", "HTML report: `/out/report.html`", "Outcomes: failed 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "| PAY-1-01 |") {
		t.Errorf("the summary lists a scenario that passed:\n%s", out)
	}
	if strings.Contains(out, "<img") {
		t.Errorf("the summary holds markup:\n%s", out)
	}
}

func TestTheSummaryCutsALongTable(t *testing.T) {
	var results []domain.ScenarioResult
	for i := range 40 {
		results = append(results, domain.ScenarioResult{Scenario: domain.ScenarioID(fmt.Sprintf("PAY-1-%02d", i)), Outcome: domain.OutcomeFailed,
			Reasons: []domain.Reason{{Text: "step pay: expected status 200, observed status 503"}}})
	}
	var b bytes.Buffer
	if err := report.PipelineSummary(&b, report.Data{Run: domain.Run{ID: "r-1", Results: results}}, "/out/report.html"); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(b.String(), "\n"); n > 32 {
		t.Errorf("the summary of 40 failures has %d lines", n)
	}
	if !strings.Contains(b.String(), "and 25 more") {
		t.Errorf("the summary does not count the rows it left out:\n%s", b.String())
	}
}

func TestTheSummaryOfAGreenRunHasNoTable(t *testing.T) {
	run := domain.Run{ID: "r-1", Results: []domain.ScenarioResult{{Scenario: "PAY-1-01", Outcome: domain.OutcomePassed}}}
	var b bytes.Buffer
	if err := report.PipelineSummary(&b, report.Data{Run: run}, "/out/report.html"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Every scenario passed.") || strings.Contains(b.String(), "| ID |") {
		t.Errorf("the summary of a green run:\n%s", b.String())
	}
}
