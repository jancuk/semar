// Package words says what a stored result means to a person and which command comes next. The
// stored outcomes do not change; this is the only place that renames them.
package words

import "strings"

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
