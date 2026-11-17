package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/report"
)

func TestMarkdownListsBrowserStepsWithTheirScreenshots(t *testing.T) {
	acts := []domain.Activity{
		{Seq: 1, Surface: domain.SurfaceWeb, Use: domain.UseCase{Scenario: "board", Step: "drag"}, Why: "Drag card A onto Doing", Target: "drag Board", Result: "done", Screenshot: "screenshots/board/0001-drag.png"},
		{Seq: 2, Surface: domain.SurfaceWeb, Use: domain.UseCase{Scenario: "board", Step: "pay"}, Why: "Press <b>Pay</b>", Target: "click Cart", Result: "error: an element was not found", Screenshot: "../../etc/passwd"},
		{Seq: 3, Surface: domain.SurfaceAPI, Use: domain.UseCase{Scenario: "board", Step: "x"}, Target: "GET /v1"},
	}
	var buf bytes.Buffer
	if err := report.Markdown(&buf, report.Data{Run: domain.Run{ID: "r-1", Environment: "local"}, Activity: acts}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"## Browser steps",
		"- board, step drag: Drag card A onto Doing (drag Board): done. [Screenshot](screenshots/board/0001-drag.png)",
		"- board, step pay: Press &lt;b&gt;Pay&lt;/b&gt; (click Cart): error: an element was not found",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "passwd") {
		t.Errorf("the report links a path that is not a screenshot of the run:\n%s", out)
	}
	if strings.Contains(out, "GET /v1") {
		t.Errorf("an API action is listed as a browser step")
	}
}
