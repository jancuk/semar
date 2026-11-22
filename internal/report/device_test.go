package report_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/report"
)

func deviceActivity() []domain.Activity {
	return []domain.Activity{
		{Seq: 1, Surface: domain.SurfaceAndroid, Use: domain.UseCase{Scenario: "AND-1", Step: "start"}, Why: "Start the app", Target: "launch com.example.shop", Result: "done", Screenshot: "screenshots/AND-1/0001-start.jpg"},
		{Seq: 2, Surface: domain.SurfaceAndroid, Use: domain.UseCase{Scenario: "AND-1", Step: "sign-in"}, Why: "Tap <b>sign in</b>", Target: "tap SignIn", Result: "done", Screenshot: "../../etc/passwd"},
		{Seq: 3, Surface: domain.SurfaceWeb, Use: domain.UseCase{Scenario: "WEB-1", Step: "open"}, Why: "Open the board", Target: "open /b", Result: "done"},
		{Seq: 4, Surface: domain.SurfaceAPI, Use: domain.UseCase{Scenario: "API-1", Step: "x"}, Target: "GET /v1"},
	}
}

func TestMarkdownListsDeviceStepsApartFromBrowserSteps(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Markdown(&buf, report.Data{Run: domain.Run{ID: "r-1", Environment: "local"}, Activity: deviceActivity()}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	device, browser := strings.Index(out, "## Device steps"), strings.Index(out, "## Browser steps")
	if device < 0 || browser < 0 {
		t.Fatalf("the report lacks a section:\n%s", out)
	}
	for _, want := range []string{
		"- AND-1, step start: Start the app (launch com.example.shop): done. [Screenshot](screenshots/AND-1/0001-start.jpg)",
		"- AND-1, step sign-in: Tap &lt;b&gt;sign in&lt;/b&gt; (tap SignIn): done\n",
	} {
		if !strings.Contains(out[device:], want) {
			t.Errorf("the device steps lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "passwd") {
		t.Errorf("the report links a path that is not a screenshot of the run:\n%s", out)
	}
	if strings.Contains(out[browser:device], "AND-1") || strings.Contains(out[device:], "WEB-1") {
		t.Errorf("a step is listed in the wrong section:\n%s", out)
	}
}

func TestMarkdownOfABrowserOnlyRunHasNoDeviceSection(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Markdown(&buf, report.Data{Run: domain.Run{ID: "r-1", Environment: "local"}, Activity: deviceActivity()[2:]}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "Device steps") {
		t.Errorf("a run with no device step has a section for them:\n%s", buf.String())
	}
}

func TestMarkdownLinksTheScreenshotOfTheDeviceStepThatFailed(t *testing.T) {
	acts := deviceActivity()[:1]
	acts = append(acts, domain.Activity{Seq: 2, Surface: domain.SurfaceAndroid, Use: domain.UseCase{Scenario: "AND-1", Step: "read"}, Target: "wait SignIn", Result: "done", Screenshot: "screenshots/AND-1/0002-read.jpg"})
	run := domain.Run{ID: "r-1", Environment: "local", Results: []domain.ScenarioResult{{
		Scenario: "AND-1", Outcome: domain.OutcomeFailed,
		Checks: []domain.CheckResult{{Step: "read", Index: 0, State: domain.CheckFalse, Expected: "element welcome equals x", Observed: "y"}},
	}}}
	var buf bytes.Buffer
	if err := report.Markdown(&buf, report.Data{Run: run, Activity: acts}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "- Screenshot of step read: [Screenshot](screenshots/AND-1/0002-read.jpg)\n") {
		t.Errorf("the report lacks the screenshot of the failed step:\n%s", buf.String())
	}
}

func TestMarkdownNamesAnAPKBuild(t *testing.T) {
	run := domain.Run{ID: "r-1", Environment: "local", BuildsStart: []domain.BuildRef{{Service: "shop-app", Commit: "3edc4275636c107c4e59868bd388a2c72ed04eb2", Declared: true, APK: true}}}
	var buf bytes.Buffer
	if err := report.Markdown(&buf, report.Data{Run: run}); err != nil {
		t.Fatal(err)
	}
	want := "shop-app 3edc4275636c107c4e59868bd388a2c72ed04eb2 (the digest of the APK file of the service, not read from the service)"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("the report lacks %q:\n%s", want, buf.String())
	}
	buf.Reset()
	if err := report.DeclaredBuilds(&buf, run); err != nil || !strings.Contains(buf.String(), "build: "+want) {
		t.Errorf("DeclaredBuilds = %q, %v", buf.String(), err)
	}
}

func TestHTMLEmbedsTheScreenshotOfADeviceStep(t *testing.T) {
	d := htmlData()
	d.Run.Results = append(d.Run.Results, domain.ScenarioResult{Scenario: "AND-1", Outcome: domain.OutcomeFailed, Reasons: []domain.Reason{}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{}})
	d.Scenarios["AND-1"] = domain.Scenario{ID: "AND-1", Title: "Sign in", Ticket: "PROJ-15", Surface: domain.SurfaceAndroid, Steps: []domain.Step{{ID: "start", Action: "Start the app"}}}
	d.Activity = append(d.Activity, domain.Activity{Seq: 4, Surface: domain.SurfaceAndroid, Use: domain.UseCase{Scenario: "AND-1", Step: "start"}, Why: "Start the app", Target: "launch com.example.shop", Result: "done", Screenshot: "screenshots/AND-1/0004-start.jpg"})
	d.Files.(fakeFiles).files["screenshots/AND-1/0004-start.jpg"] = jpeg
	out := renderHTML(t, d)
	uri := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)
	// The failed device scenario embeds its screenshot, and the passed web scenario its last one.
	if n := strings.Count(out, uri); n != 2 {
		t.Errorf("the page embeds %d screenshots, want 2", n)
	}
	if !strings.Contains(out, "Start the app (launch com.example.shop): done") {
		t.Errorf("the page lacks the device step:\n%s", out)
	}
}
