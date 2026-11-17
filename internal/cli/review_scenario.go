package cli

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/scenario"
)

// requirement is a cited line of a ticket: where it is, and its text as the file has it.
type requirement struct{ cite, text string }

// requirementsOf are the distinct cited lines of the expected results that need a confirmation, in
// the order that they come. A cite that names no line of a file is no requirement. A use case with
// its own cite stands in when no expected result has one.
func requirementsOf(dir string, use *obligation.Located, us []unconfirmed) []requirement {
	var cites []string
	add := func(cite string) {
		file, n, ok := citedLine(cite)
		if !ok {
			return
		}
		if c := fmt.Sprintf("%s:%d", file, n); !slices.Contains(cites, c) {
			cites = append(cites, c)
		}
	}
	for _, u := range us {
		for _, e := range u.needs {
			add(e.Cite)
		}
	}
	if len(cites) == 0 && use != nil {
		add(use.Cite)
	}
	out := make([]requirement, len(cites))
	for i, c := range cites {
		file, n, _ := citedLine(c)
		text := "line not found"
		if t, ok := citedLineText(dir, file, n); ok {
			text = fmt.Sprintf("%q", strings.TrimSpace(numbering.ReplaceAllString(strings.TrimSpace(t), "")))
		}
		out[i] = requirement{terminalSafe(oneLine(c)), text}
	}
	return out
}

var numbering = regexp.MustCompile(`^[0-9]+\.\s+`)

// showScenarioItem prints a use case that has scenarios to confirm. It opens with the requirement,
// which is the cited lines of the ticket, and then shows each test as its Gherkin file has it.
func showScenarioItem(w io.Writer, dir string, n, total int, use *obligation.Located, us []unconfirmed, fixtureState func(domain.FixtureID) string) {
	ticket := us[0].sc.Ticket
	if use != nil {
		ticket = ticketKey(*use)
	}
	reqs := requirementsOf(dir, use, us)
	var opening string
	switch {
	case len(reqs) > 0:
		cites := make([]string, len(reqs))
		for i, r := range reqs {
			cites[i] = r.cite
		}
		fmt.Fprintf(w, "\n%d of %d · %s · %s\n", n, total, terminalSafe(oneLine(ticket)), strings.Join(cites, ", "))
		for _, r := range reqs {
			fmt.Fprintf(w, "  %s\n", r.text)
		}
	case use != nil:
		opening = terminalSafe(oneLine(use.Title))
		fmt.Fprintf(w, "\n%d of %d · %s\n  %q\n", n, total, terminalSafe(oneLine(use.ID)), opening)
	default:
		opening = terminalSafe(oneLine(us[0].sc.Title))
		fmt.Fprintf(w, "\n%d of %d · scenario %s\n  %q\n", n, total, us[0].sc.ID, opening)
	}
	for _, u := range us {
		sc := u.sc
		fmt.Fprintf(w, "\n  Test %s · %s · as %s\n", sc.ID, surfaceWord(sc.Surface), accountOf(sc))
		if title := terminalSafe(oneLine(sc.Title)); title != opening {
			fmt.Fprintf(w, "    %s\n", title)
		}
		for _, l := range scenario.Lines(sc) {
			text := l.Text
			if e := l.Expect; e != nil {
				text += expectationNote(*e)
			}
			fmt.Fprintf(w, "    %-5s %s\n", l.Keyword, terminalSafe(oneLine(text)))
		}
		for _, id := range fixturesOf(sc) {
			fmt.Fprintf(w, "    Uses the fixture %s (%s)\n", id, fixtureState(id))
		}
	}
}

// expectationNote says that a model wrote an expected result, and where it came from when that is not
// the ticket that the item opens with.
func expectationNote(e domain.Expectation) string {
	var parts []string
	if e.Source != domain.SourceTicket {
		parts = append(parts, "source "+string(e.Source))
	}
	if e.By == "model" {
		parts = append(parts, "by model")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
