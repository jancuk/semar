package export

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
)

type ctrfReport struct {
	Results ctrfResults `json:"results"`
}

type ctrfResults struct {
	Tool        ctrfTool          `json:"tool"`
	Summary     ctrfSummary       `json:"summary"`
	Tests       []ctrfTest        `json:"tests"`
	Environment *ctrfEnvironment  `json:"environment,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

type ctrfTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ctrfSummary struct {
	Tests   int   `json:"tests"`
	Passed  int   `json:"passed"`
	Failed  int   `json:"failed"`
	Pending int   `json:"pending"`
	Skipped int   `json:"skipped"`
	Other   int   `json:"other"`
	Start   int64 `json:"start"`
	Stop    int64 `json:"stop"`
}

type ctrfTest struct {
	Name     string            `json:"name"`
	Status   string            `json:"status"`
	Duration int64             `json:"duration"`
	Message  string            `json:"message,omitempty"`
	Trace    string            `json:"trace,omitempty"`
	Suite    string            `json:"suite,omitempty"`
	Retries  int               `json:"retries,omitempty"`
	Flaky    bool              `json:"flaky,omitempty"`
	Extra    map[string]string `json:"extra"`
}

type ctrfEnvironment struct {
	TestEnvironment string `json:"testEnvironment"`
}

// ctrfStatus maps an outcome as D5 says. An outcome that is not one of the six is never a pass.
func ctrfStatus(o domain.Outcome) string {
	switch o {
	case domain.OutcomePassed:
		return "passed"
	case domain.OutcomeFailed:
		return "failed"
	case domain.OutcomeNotRun:
		return "skipped"
	default:
		return "other"
	}
}

// CTRF writes the run as a CTRF JSON report. The keys reportFormat and specVersion are not
// written, because the current schema's requirement for them is not verified.
func CTRF(w io.Writer, r Run) error {
	res := ctrfResults{
		Tool:  ctrfTool{Name: "semar", Version: r.Version},
		Tests: make([]ctrfTest, 0, len(r.Scenarios)),
		Summary: ctrfSummary{
			Start: epochMillis(r.Run.StartedAt.UnixMilli(), r.Run.StartedAt.IsZero()),
			Stop:  epochMillis(r.Run.EndedAt.UnixMilli(), r.Run.EndedAt.IsZero()),
		},
		Extra: map[string]string{"semarRun": string(r.Run.ID)},
	}
	if r.Run.Environment != "" {
		res.Environment = &ctrfEnvironment{TestEnvironment: cutMessage(r.Run.Environment)}
	}
	if b := buildsText(r.Run); b != "" {
		res.Extra["semarBuilds"] = b
	}
	for _, sc := range r.Scenarios {
		t := ctrfTest{
			Name:     testName(sc),
			Status:   ctrfStatus(sc.Result.Outcome),
			Duration: sc.DurationMS,
			Suite:    sc.Ticket,
			Extra: map[string]string{
				"semarOutcome":     string(sc.Result.Outcome),
				"semarRun":         string(r.Run.ID),
				"semarEnvironment": cutMessage(r.Run.Environment),
			},
		}
		if sc.Unknown {
			t.Extra["semarDurationUnknown"] = "true"
		}
		if n := len(sc.Result.Attempts); n > 1 {
			t.Retries, t.Flaky = n-1, sc.Result.Flaky
			t.Extra["semarAttempts"] = attemptOutcomes(sc.Result.Attempts)
		}
		if n, _ := sc.Result.ModelCount(); n > 0 {
			t.Extra["semarByModel"] = fmt.Sprint(n)
		}
		if sc.Result.Outcome != domain.OutcomePassed {
			t.Message, t.Trace = reasonTexts(sc.Result.Reasons)
		}
		switch t.Status {
		case "passed":
			res.Summary.Passed++
		case "failed":
			res.Summary.Failed++
		case "skipped":
			res.Summary.Skipped++
		default:
			res.Summary.Other++
		}
		res.Tests = append(res.Tests, t)
	}
	for _, jc := range journeyCases(r) {
		t := ctrfTest{
			Name:     jc.Name,
			Status:   ctrfJourneyStatus(jc.Result.Outcome),
			Duration: jc.DurationMS,
			Suite:    suiteForJourneys,
			Extra: map[string]string{
				"semarOutcome":     string(jc.Result.Outcome),
				"semarRun":         string(r.Run.ID),
				"semarEnvironment": cutMessage(r.Run.Environment),
				"semarJourney":     cutMessage(string(jc.Result.Journey)),
			},
		}
		if jc.Result.Outcome != domain.JourneyPassed {
			t.Message, t.Trace = jc.Message, jc.Detail
		}
		switch t.Status {
		case "passed":
			res.Summary.Passed++
		case "failed":
			res.Summary.Failed++
		case "skipped":
			res.Summary.Skipped++
		default:
			res.Summary.Other++
		}
		res.Tests = append(res.Tests, t)
	}
	res.Summary.Tests = len(res.Tests)
	data, err := atomicfile.EncodeJSON(ctrfReport{Results: res})
	if err != nil {
		return fmt.Errorf("encode ctrf report of run %s: %w", r.Run.ID, err)
	}
	_, err = w.Write(data)
	return err
}

// attemptOutcomes lists the outcome of each attempt in order, such as "failed, passed".
func attemptOutcomes(attempts []domain.Attempt) string {
	parts := make([]string, len(attempts))
	for i, a := range attempts {
		parts[i] = cutMessage(string(a.Outcome))
	}
	return strings.Join(parts, ", ")
}

func epochMillis(ms int64, zero bool) int64 {
	if zero {
		return 0
	}
	return ms
}

// testName is "<scenario id> <title>", or the ID alone when the title is not known (D8).
func testName(sc Scenario) string {
	name := cutMessage(string(sc.Result.Scenario))
	if sc.Title != "" {
		name += " " + sc.Title
	}
	return name
}

// reasonTexts gives the first reason as the message, and every reason, one per line, as the detail.
func reasonTexts(reasons []domain.Reason) (message, detail string) {
	if len(reasons) == 0 {
		return "", ""
	}
	lines := make([]string, len(reasons))
	for i, r := range reasons {
		lines[i] = r.Text
	}
	return cutMessage(reasons[0].Text), cutDetail(strings.Join(lines, "\n"))
}

// buildsText lists the builds at the end of the run, or at its start when the end has none.
func buildsText(run domain.Run) string {
	builds := run.BuildsEnd
	if len(builds) == 0 {
		builds = run.BuildsStart
	}
	sorted := append([]domain.BuildRef(nil), builds...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Service < sorted[j].Service })
	parts := make([]string, len(sorted))
	for i, b := range sorted {
		parts[i] = cutMessage(b.Service + " " + b.Commit)
	}
	return strings.Join(parts, ", ")
}
