package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// maxSummaryRows is how many scenarios the summary lists before the rest is a count, so that the
// summary stays near 30 lines.
const maxSummaryRows = 15

// PipelineSummary writes summary.md: one line of counts, the builds, a table of every scenario that did
// not pass or is flaky with its first reason, and the path of the HTML report. It is short enough for a
// merge request.
func PipelineSummary(w io.Writer, d Data, htmlPath string) error {
	v := markdownViewer.buildView(d)
	var b strings.Builder
	fmt.Fprintf(&b, "# Semar report %s\n\n", v.RunID)
	if v.Interrupted {
		b.WriteString("This run did not finish: no end time was recorded.\n\n")
	}
	env := v.Environment
	if env != "" && v.EnvironmentKind != "" {
		env += " (" + v.EnvironmentKind + ")"
	}
	if env != "" {
		fmt.Fprintf(&b, "Environment: %s\n\n", env)
	}
	fmt.Fprintf(&b, "Outcomes: %s\n\n", v.Counts)
	head, more := cut(v.BuildsEnd, 400)
	if more > 0 {
		head += cutMark(more)
	}
	fmt.Fprintf(&b, "Builds: %s\n\n", head)

	var rows []scenarioView
	for _, s := range append(append([]scenarioView{}, v.Attention...), v.Passed...) {
		if s.Class != domain.OutcomePassed || s.Flaky {
			rows = append(rows, s)
		}
	}
	if len(rows) == 0 {
		b.WriteString("Every scenario passed.\n\n")
	} else {
		b.WriteString("| ID | Outcome | First reason |\n| --- | --- | --- |\n")
		for i, s := range rows {
			if i == maxSummaryRows {
				fmt.Fprintf(&b, "| and %d more | | in the HTML report |\n", len(rows)-i)
				break
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", s.ID, summaryOutcome(s), summaryReason(s))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "HTML report: `%s`\n", plain(htmlPath))
	_, err := io.WriteString(w, b.String())
	return err
}

func summaryOutcome(s scenarioView) string {
	if s.Flaky {
		return "flaky"
	}
	return s.Outcome
}

func summaryReason(s scenarioView) string {
	switch {
	case s.Flaky:
		return s.Attempts
	case s.Lead != "":
		return s.Lead
	}
	return "no reason recorded"
}
