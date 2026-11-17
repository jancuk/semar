package plan

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
)

// Accounted is an acceptance criterion with what cites it: a scenario with an expectation of the
// source ticket, a manual check, or a notDrafted entry (D9).
type Accounted struct {
	Criterion
	CitedBy []string // in the order scenarios, manual checks, not drafted; each once
}

// Accounting lists every acceptance criterion of the ticket files, in key and line order, with
// what cites it. A cite names a criterion when it names its file and one of its lines. ids are
// the IDs of the scenarios of the draft, in order; a missing ID is shown as "scenario <n>".
func Accounting(d Draft, tickets []knowledge.Ticket, ids []domain.ScenarioID) []Accounted {
	index := ticketIndex(tickets)
	var out []Accounted
	for _, key := range slices.Sorted(maps.Keys(index)) {
		for _, c := range Criteria(index[key]) {
			a := Accounted{Criterion: c}
			add := func(by string) {
				if !slices.Contains(a.CitedBy, by) {
					a.CitedBy = append(a.CitedBy, by)
				}
			}
			for i, sc := range d.Scenarios {
				if scenarioCites(sc, c) {
					if i < len(ids) {
						add(string(ids[i]))
					} else {
						add(fmt.Sprintf("scenario %d", i+1))
					}
				}
			}
			for _, w := range d.WebTests {
				if w.Ticket == c.Ticket && cites(w.Cite, c) {
					add("web test: " + oneLine(w.ID, 80))
				}
			}
			for _, m := range d.ManualChecks {
				if m.Ticket == c.Ticket && cites(m.Cite, c) {
					add("manual check: " + oneLine(m.Title, 80))
				}
			}
			for _, n := range d.NotDrafted {
				if n.Ticket == c.Ticket && cites(n.Cite, c) {
					add("not drafted: " + oneLine(n.What, 80))
				}
			}
			out = append(out, a)
		}
	}
	return out
}

// Unaccounted are the criteria that nothing cites.
func Unaccounted(d Draft, tickets []knowledge.Ticket) []Criterion {
	var out []Criterion
	for _, a := range Accounting(d, tickets, nil) {
		if len(a.CitedBy) == 0 {
			out = append(out, a.Criterion)
		}
	}
	return out
}

func scenarioCites(sc ScenarioDraft, c Criterion) bool {
	for _, st := range sc.Steps {
		for _, e := range st.Expect {
			if e.Source == sourceTicket && sc.Ticket == c.Ticket && cites(e.Why, c) {
				return true
			}
		}
	}
	return false
}

// cites says whether a cite names the file of the criterion and a line from the first to the
// last of the criterion.
func cites(text string, c Criterion) bool {
	m := citePattern.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return false
	}
	line := atoi(m[2])
	return cleanSlash(m[1]) == c.Path && line >= c.Line && line <= c.End
}

// unaccountedProblems is the problem of each criterion that nothing cites: the planner can fix
// it with a scenario, a manual check, or a notDrafted entry. The text of the criterion is not
// in the message, so repository text does not reach the prompt twice.
func unaccountedProblems(missing []Criterion) []Problem {
	ps := make([]Problem, len(missing))
	for i, c := range missing {
		ps[i] = Problem{Message: fmt.Sprintf("%s criterion %d is not accounted for: add a scenario with source ticket, a manual check, or a notDrafted entry that cites it", c.Cite(), c.Number)}
	}
	return ps
}
