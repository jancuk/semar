package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/stage/plan"
)

// readEarlier is what the obligations files of the tickets of the change say about their criteria. A
// scenario counts as a test only when its file is there and names the same ticket: the obligations
// files are repository text, and a file that claims a test cannot make the plan skip a criterion. A file
// with a problem is not believed, and the plan writes the obligations of its ticket only after it is
// fixed. An unreadable directory gives nothing, and the plan treats every criterion as new.
func readEarlier(repo string, inChange map[string]bool, scs []domain.Scenario) map[string][]plan.Earlier {
	uses, problems, err := obligation.Load(repo)
	if err != nil {
		return nil
	}
	broken := map[string]bool{}
	for _, p := range problems {
		broken[p.File] = true
	}
	scenarios := map[domain.ScenarioID]domain.Scenario{}
	for _, sc := range scs {
		scenarios[sc.ID] = sc
	}
	out := map[string][]plan.Earlier{}
	for _, l := range uses {
		if broken[l.File] || !inChange[l.Ticket] {
			continue
		}
		e := plan.Earlier{}
		if l.Criterion != nil {
			e.Number, e.Key = l.Criterion.Number, l.Criterion.Key
		}
		var cites []string
		for _, id := range l.Covers {
			sc, ok := scenarios[id]
			if !ok || sc.Ticket != l.Ticket {
				continue
			}
			e.Tests = append(e.Tests, id)
			for _, st := range sc.Steps {
				for _, x := range st.Expect {
					if x.Source == domain.SourceTicket && x.Cite != "" && !slices.Contains(cites, x.Cite) {
						cites = append(cites, x.Cite)
					}
				}
			}
		}
		// The check for the rest of a web test goes with that test: alone it keeps no criterion, so a
		// test that semar rm removed is written again.
		if l.Source == obligation.SourceCriterion && l.Manual != nil && strings.Contains(l.ID, "/manual-") && !strings.HasPrefix(l.Title, plan.RestOfCriterion) {
			e.Manual = true
		}
		if l.Cite != "" {
			cites = append(cites, l.Cite)
		}
		if e.Key == "" {
			e.Cites = cites
		}
		if e.Key == "" && len(e.Cites) == 0 {
			continue
		}
		out[l.Ticket] = append(out[l.Ticket], e)
	}
	return out
}

// againLines end the output of a plan that found something that earlier plans had decided: how many
// criteria were kept, planned, and removed, and for each test of an earlier text the command that
// removes it. The plan never removes a test itself, because a person approved it (D7). A plan that
// found nothing of the kind has no line.
func againLines(a *plan.Again) []string {
	if a == nil || (a.Count(plan.StateKept) == 0 && a.Count(plan.StateChanged) == 0 && len(a.Removed) == 0) {
		return nil
	}
	changed := a.Count(plan.StateChanged)
	lines := []string{fmt.Sprintf("criteria: %d, %d kept, %d planned (%d changed), %d removed",
		len(a.Criteria), a.Count(plan.StateKept), a.Count(plan.StateNew)+changed, changed, len(a.Removed))}
	for _, s := range a.Criteria {
		if s.State != plan.StateChanged {
			continue
		}
		for _, id := range s.Tests {
			lines = append(lines, fmt.Sprintf("%s was written for an earlier text of criterion %d. Read both tests. To remove the old one: semar rm %s --reason \"criterion %d changed\"",
				terminalSafe(string(id)), s.Number, terminalSafe(string(id)), s.Number))
		}
	}
	for _, r := range a.Removed {
		for _, id := range r.Tests {
			if r.Number > 0 {
				lines = append(lines, fmt.Sprintf("%s was written for a criterion that %s no longer has (it was criterion %d). To remove it: semar rm %s --reason \"criterion %d was removed\"",
					terminalSafe(string(id)), terminalSafe(r.Ticket), r.Number, terminalSafe(string(id)), r.Number))
				continue
			}
			lines = append(lines, fmt.Sprintf("%s was written for a criterion that %s no longer has. To remove it: semar rm %s --reason \"its criterion was removed\"",
				terminalSafe(string(id)), terminalSafe(r.Ticket), terminalSafe(string(id))))
		}
	}
	return lines
}
