package report

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/words"
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
	if n := flakyCount(results); n > 0 {
		parts = append(parts, fmt.Sprintf("flaky %d", n))
	}
	return strings.Join(parts, ", ")
}

func flakyCount(results []domain.ScenarioResult) int {
	n := 0
	for _, r := range results {
		if r.Flaky {
			n++
		}
	}
	return n
}

// attemptNote says how a scenario that ran more than once came out, or "" when it ran once.
func attemptNote(r domain.ScenarioResult) string {
	return words.Attempts(string(r.Outcome), r.Flaky, len(r.Attempts), r.AttemptsAllowed)
}

// modelNote says how many expected results of the result a model wrote, or "" when none.
func modelNote(r domain.ScenarioResult) string {
	byModel, results := r.ModelCount()
	if byModel == 0 {
		return ""
	}
	return fmt.Sprintf("%d of %d results by model", byModel, results)
}

// runModelCount sums ModelCount over the scenarios of the run. A journey run counts nothing: its
// legs are not expected results that a model wrote (Plan 36, D13a).
func runModelCount(run domain.Run) (byModel, results int) {
	if len(run.Journeys) > 0 {
		return 0, 0
	}
	for _, r := range run.Results {
		b, n := r.ModelCount()
		byModel, results = byModel+b, results+n
	}
	return byModel, results
}

// Summary writes one line per scenario, one line with the count of each outcome, the coverage
// line when there is one, and one line for each build that was declared rather than read from
// the service.
func Summary(w io.Writer, run domain.Run, coverage string) error {
	if err := Lines(w, run); err != nil {
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
	return DeclaredBuilds(w, run)
}

// Lines writes one line for each scenario of the run, or for each journey and its legs: the ID, the
// word for the result, why, and the command that comes next.
func Lines(w io.Writer, run domain.Run) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if len(run.Journeys) > 0 {
		// A journey run prints each leg under its journey, in place of the loose legs.
		for _, line := range journeyLines(run.Journeys) {
			if _, err := fmt.Fprintln(tw, line); err != nil {
				return err
			}
		}
	} else {
		for _, r := range ordered(run.Results) {
			word := resultWord(string(r.Outcome), r.Reasons)
			line := plain(string(r.Scenario)) + "\t" + plain(word)
			switch {
			case len(r.Reasons) > 0:
				head, more := cut(shownReason(r.Reasons), maxShown)
				line += "\t" + plain(head)
				if more > 0 {
					line += cutMark(more)
				}
			case r.Outcome != domain.OutcomePassed:
				line += "\tno reason recorded"
			}
			if slices.ContainsFunc(r.Reasons, func(reason domain.Reason) bool { return reason.Text == words.NotRunAgain }) {
				line += " (" + words.NotRunAgain + ")"
			}
			if note := attemptNote(r); note != "" {
				if r.Outcome == domain.OutcomePassed {
					line += "\t" + note
				} else {
					line += " (" + note + ")"
				}
			}
			if note := modelNote(r); note != "" {
				line += "  (" + note + ")"
			}
			line += nextMark(line, word, string(r.Scenario))
			if _, err := fmt.Fprintln(tw, line); err != nil {
				return err
			}
		}
	}
	return tw.Flush()
}

// shownReason is the reason that a result line shows: the first one that is a cause. A step that was
// never sent has no recorded result, and that is a consequence of the reason that stopped the run,
// such as a screens file that is not approved.
func shownReason(reasons []domain.Reason) string {
	for _, r := range reasons {
		if !strings.HasSuffix(r.Text, "has no recorded result") {
			return r.Text
		}
	}
	return reasons[0].Text
}

// DeclaredBuilds names each build that a person declared, because nothing read it from the service.
func DeclaredBuilds(w io.Writer, run domain.Run) error {
	for _, b := range run.BuildsStart {
		if b.Declared {
			if _, err := fmt.Fprintf(w, "build: %s\n", plain(declaredBuild(b))); err != nil {
				return err
			}
		}
	}
	return nil
}

// resultWord is the word that a person reads for a stored outcome (Plan 26, D7).
func resultWord(outcome string, reasons []domain.Reason) string {
	texts := make([]string, len(reasons))
	for i, r := range reasons {
		texts[i] = r.Text
	}
	return words.Of(outcome, texts)
}

// nextMark ends a line with the command that follows its word, or with nothing.
func nextMark(line, word, subject string) string {
	next := words.Next(word, plain(subject))
	// A reason that names its own command, such as the review of a screens file, is the next step.
	if next == "" || strings.Contains(line, strings.TrimSpace(words.Next(word, ""))) {
		return ""
	}
	return " → " + next
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
