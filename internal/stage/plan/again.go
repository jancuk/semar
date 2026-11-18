package plan

import (
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
)

// Earlier is one entry of an obligations file of a ticket, as far as a second plan needs it. The command
// reads it, and trusts nothing in it beyond what a scenario file confirms: Tests holds only scenarios
// whose files exist.
type Earlier struct {
	// Number and Key are the criterion that a plan after Plan 33 wrote the entry for. A plan before it
	// left them empty, and Cites links the entry instead.
	Number int
	Key    string
	// Cites are the cites of the criterion that an entry without a key holds: the ticket cites of its
	// scenarios, or its own cite when it is a manual check.
	Cites  []string
	Tests  []domain.ScenarioID
	Manual bool // the entry is a manual check of the criterion
}

// maxCiteText is how much of a cite an obligation keeps, so a cite that an entry holds is compared at
// that length.
const maxCiteText = 500

// State is what a second plan does about a criterion.
type State string

const (
	// StateKept: an entry of the criterion has a test that exists, or is a manual check. Nothing is planned.
	StateKept State = "kept"
	// StateNew: the criterion is planned.
	StateNew State = "new"
	// StateChanged: the criterion is planned, and an entry with its number holds the key of an earlier
	// text. A person decides about the earlier tests.
	StateChanged State = "changed"
)

// Standing is a criterion and what the plans before it decided.
type Standing struct {
	Criterion
	State State
	// Tests are the tests that keep a kept criterion, or that were written for the earlier text of a
	// changed one.
	Tests []domain.ScenarioID
	// Manual says that a manual check of the criterion keeps it, or that one was written for the earlier text.
	Manual bool
	// Entered says that an obligation entry of the criterion exists, with no test. A case that was not
	// drafted is one.
	Entered bool
}

// Removed is what plans before this one wrote for a criterion that the ticket file no longer has. Number
// is 0 for an entry that holds no key, and Cite is then the only link that it has.
type Removed struct {
	Ticket string
	Number int
	Cite   string
	Tests  []domain.ScenarioID
	Manual bool
}

// Again is the standing of every criterion of the ticket files, in key and line order, and the entries
// that no criterion has any more.
type Again struct {
	Criteria []Standing
	Removed  []Removed
}

// Count is the criteria in a state.
func (a Again) Count(s State) int {
	n := 0
	for _, c := range a.Criteria {
		if c.State == s {
			n++
		}
	}
	return n
}

// AllKept says that the ticket files have criteria and every one is kept: there is nothing to plan.
func (a Again) AllKept() bool { return len(a.Criteria) > 0 && a.Count(StateKept) == len(a.Criteria) }

// Tickets are the keys of the tickets that have criteria, in order.
func (a Again) Tickets() []string {
	var out []string
	for _, c := range a.Criteria {
		if !slices.Contains(out, c.Ticket) {
			out = append(out, c.Ticket)
		}
	}
	return out
}

// Classify gives each criterion of each ticket file one state from what the plans before it decided (D4
// to D6, D8). It reads nothing and calls no model.
func Classify(tickets []knowledge.Ticket, earlier map[string][]Earlier) Again {
	var a Again
	index := ticketIndex(tickets)
	for _, key := range slices.Sorted(maps.Keys(index)) {
		st, rm := classifyTicket(Criteria(index[key]), earlier[key])
		a.Criteria = append(a.Criteria, st...)
		for i := range rm {
			rm[i].Ticket = key
		}
		a.Removed = append(a.Removed, rm...)
	}
	return a
}

func classifyTicket(criteria []Criterion, entries []Earlier) ([]Standing, []Removed) {
	today := map[string]bool{}
	for _, c := range criteria {
		today[c.Key] = true
	}
	byKey := map[string][]Earlier{}
	var dead []Earlier
	claimed := map[int]bool{}
	for _, e := range entries {
		if e.Key != "" {
			if today[e.Key] {
				byKey[e.Key] = append(byKey[e.Key], e)
			} else {
				dead = append(dead, e)
			}
			continue
		}
		matched := false
		for _, cite := range e.Cites {
			if i := matchCite(criteria, claimed, cite); i >= 0 {
				byKey[criteria[i].Key] = append(byKey[criteria[i].Key], e)
				claimed[i], matched = true, true
			}
		}
		if !matched {
			dead = append(dead, e)
		}
	}

	consumed := map[int]bool{}
	out := make([]Standing, len(criteria))
	for i, c := range criteria {
		s := Standing{Criterion: c, State: StateNew}
		for _, e := range byKey[c.Key] {
			s.Entered = true
			s.Tests = appendNew(s.Tests, e.Tests)
			s.Manual = s.Manual || e.Manual
		}
		if len(s.Tests) > 0 || s.Manual {
			s.State = StateKept
			out[i] = s
			continue
		}
		s.Tests = nil
		for j, e := range dead {
			if e.Key != "" && e.Number == c.Number {
				s.State = StateChanged
				s.Tests = appendNew(s.Tests, e.Tests)
				s.Manual = s.Manual || e.Manual
				consumed[j] = true
			}
		}
		out[i] = s
	}

	var removed []Removed
	for j, e := range dead {
		if consumed[j] || (len(e.Tests) == 0 && !e.Manual) {
			continue
		}
		r := Removed{Number: e.Number, Tests: slices.Clone(e.Tests), Manual: e.Manual}
		if e.Key == "" && len(e.Cites) > 0 {
			r.Cite = citeText(e.Cites[0])
		}
		removed = append(removed, r)
	}
	return out, removed
}

func appendNew(list, more []domain.ScenarioID) []domain.ScenarioID {
	for _, id := range more {
		if !slices.Contains(list, id) {
			list = append(list, id)
		}
	}
	return list
}

// matchCite is the criterion whose first sentence a cite holds after its file and line: the first one that
// no entry claimed yet, else the first. The line is not read, because it moves when the file is edited.
func matchCite(criteria []Criterion, claimed map[int]bool, cite string) int {
	text := citeText(cite)
	if text == "" {
		return -1
	}
	first := -1
	for i, c := range criteria {
		if citeText(c.Cite()+" "+whyText(c.Full)) != text {
			continue
		}
		if !claimed[i] {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	return first
}

// citeText is a cite without its file and line, on one line.
func citeText(cite string) string {
	cite = strings.TrimSpace(cite)
	if m := citePattern.FindStringSubmatch(cite); m != nil {
		cite = strings.TrimPrefix(cite, m[1]+":"+m[2])
	}
	return oneLine(strings.TrimLeft(cite, ": \t"), maxCiteText)
}
