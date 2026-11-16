package report

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/jancuk/semar/internal/domain"
)

var knownOutcomes = []domain.Outcome{
	domain.OutcomeFailed,
	domain.OutcomeBlocked,
	domain.OutcomeInconclusive,
	domain.OutcomeStale,
	domain.OutcomeNotRun,
	domain.OutcomePassed,
}

// ExitCode is 0 when every scenario passed, 1 when one failed, and 2 otherwise.
func ExitCode(results []domain.ScenarioResult) int {
	if len(results) == 0 {
		return 2
	}
	code := 0
	for _, r := range results {
		switch r.Outcome {
		case domain.OutcomeFailed:
			return 1
		case domain.OutcomePassed:
		default:
			code = 2
		}
	}
	return code
}

// ordered returns the results sorted by what needs attention first, keeping the run's order within one outcome.
// An outcome that is not one of the six known ones comes after them, in the run's order.
func ordered(results []domain.ScenarioResult) []domain.ScenarioResult {
	out := make([]domain.ScenarioResult, 0, len(results))
	for _, o := range knownOutcomes {
		for _, r := range results {
			if r.Outcome == o {
				out = append(out, r)
			}
		}
	}
	for _, r := range results {
		if !isKnown(r.Outcome) {
			out = append(out, r)
		}
	}
	return out
}

func isKnown(o domain.Outcome) bool {
	for _, k := range knownOutcomes {
		if o == k {
			return true
		}
	}
	return false
}

func countLine(results []domain.ScenarioResult) string {
	counts := map[domain.Outcome]int{}
	other := 0
	for _, r := range results {
		if isKnown(r.Outcome) {
			counts[r.Outcome]++
		} else {
			other++
		}
	}
	parts := make([]string, 0, len(knownOutcomes)+1)
	for _, o := range knownOutcomes {
		parts = append(parts, fmt.Sprintf("%s %d", o, counts[o]))
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("unknown %d", other))
	}
	return strings.Join(parts, ", ")
}

// Summary writes one line per scenario, one line with the count of each outcome, the coverage
// line when there is one, and one line for each build that was declared rather than read from
// the service.
func Summary(w io.Writer, run domain.Run, coverage string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range ordered(run.Results) {
		line := plain(string(r.Scenario)) + "\t" + plain(string(r.Outcome))
		switch {
		case len(r.Reasons) > 0:
			head, more := cut(r.Reasons[0].Text, maxShown)
			line += "\t" + plain(head)
			if more > 0 {
				line += cutMark(more)
			}
		case r.Outcome != domain.OutcomePassed:
			line += "\tno reason recorded"
		}
		if _, err := fmt.Fprintln(tw, line); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, countLine(run.Results)); err != nil {
		return err
	}
	if coverage != "" {
		if _, err := fmt.Fprintln(w, plain(coverage)); err != nil {
			return err
		}
	}
	for _, b := range run.BuildsStart {
		if b.Declared {
			if _, err := fmt.Fprintf(w, "build: %s\n", plain(declaredBuild(b))); err != nil {
				return err
			}
		}
	}
	return nil
}

// plain keeps text on one line and drops control and format characters, so a service cannot
// inject a terminal sequence or reorder the text with a bidi control.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
}
