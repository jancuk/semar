// Package words says what a stored result means to a person and which command comes next. The
// stored outcomes do not change; this is the only place that renames them.
package words

import (
	"fmt"
	"strings"
)

// Of is the word for a stored outcome and its reasons. An approval that is missing, whether of a
// scenario, a fixture, or a screens file, is "needs review" because a person can settle it. A
// result that was not run keeps its stored word, and so does every other outcome.
func Of(outcome string, reasons []string) string {
	if outcome != "inconclusive" && outcome != "blocked" {
		return outcome
	}
	for _, r := range reasons {
		if NeedsApproval(r) {
			return "needs review"
		}
	}
	return outcome
}

// NeedsApproval is true for the text of a reason that says that a person has not approved something.
func NeedsApproval(reason string) bool {
	return strings.Contains(reason, "not confirmed") || strings.Contains(reason, "not approved")
}

// Next is the command that follows a word, or "" when none does. The subject is what the command
// names, such as a scenario ID.
func Next(word, subject string) string {
	switch word {
	case "needs review":
		return "semar review " + subject
	case "stale":
		return "semar run"
	}
	return ""
}

// Line is one line of a result: the subject, the word, the detail when there is one, and the next
// command when there is one.
func Line(subject, word, detail string) string {
	line := subject + "  " + word
	if detail != "" {
		line += "  " + detail
	}
	if next := Next(word, subject); next != "" {
		line += " → " + next
	}
	return line
}

// NotRunAgain is the reason of a scenario that failed and was not run again, because it sent a
// request of the once list of its environment.
const NotRunAgain = "the scenario sent a request that effects.once lists, so it was not run again"

// Attempts says how a scenario that ran more than once came out, or "" when it ran once. ran is the
// number of attempts it made and allowed the number the run gave it.
func Attempts(outcome string, flaky bool, ran, allowed int) string {
	allowed = max(allowed, ran)
	switch {
	case ran < 2:
		return ""
	case flaky:
		return fmt.Sprintf("flaky, passed on attempt %d of %d", ran, allowed)
	case ran == allowed:
		return fmt.Sprintf("%s on all %d attempts", outcome, ran)
	}
	return fmt.Sprintf("%s on attempt %d of %d", outcome, ran, allowed)
}
