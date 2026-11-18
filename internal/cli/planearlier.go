package cli

import (
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
		if l.Source == obligation.SourceCriterion && l.Manual != nil && strings.Contains(l.ID, "/manual-") {
			e.Manual = true
			if l.Cite != "" {
				cites = append(cites, l.Cite)
			}
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
