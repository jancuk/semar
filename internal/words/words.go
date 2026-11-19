// Package words says which command follows a stored result, and how a scenario that ran more than
// once came out.
package words

import "fmt"

// Next is the command that follows a word, or "" when none does. The subject is what the command
// names, such as a scenario ID.
func Next(word, subject string) string {
	if word == "stale" {
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
