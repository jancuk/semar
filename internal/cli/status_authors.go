package cli

import (
	"strings"

	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/obligation"
)

// approvedByAuthors marks each use case whose every approval came from an author of a commit of the
// change (Plan 26, D11). The approvals of a use case are the confirmations of the scenarios that it
// covers and the approvals of the fixtures that those scenarios use, at the hash that holds now. A
// use case with no approval is not marked, and neither is one with an approval that has no email.
// It is a mark and nothing more: it changes no state and no exit code.
func approvedByAuthors(dir string, entries []obligation.Entry, change domain.ChangeSet) map[string]bool {
	authors := map[string]bool{}
	for _, c := range change.Commits {
		if e := strings.ToLower(strings.TrimSpace(c.AuthorEmail)); e != "" {
			authors[e] = true
		}
	}
	marked := map[string]bool{}
	if len(authors) == 0 {
		return marked
	}
	store := confirm.NewLocal(semarDir(dir))
	cat, _ := fixture.Load(dir, nil)
	for _, e := range entries {
		var emails []string
		ok := true
		for _, c := range e.Covers {
			if c.Scenario == "" {
				continue
			}
			sc, err := loadScenario(dir, c.Scenario)
			if err != nil {
				ok = false
				break
			}
			recs, err := store.Confirmations(sc.ID, domain.ExpectHash(sc))
			if err != nil {
				ok = false
				break
			}
			for _, r := range recs {
				emails = append(emails, r.Email)
			}
			for _, id := range fixturesOf(sc) {
				fx, found := cat.Get(id)
				if !found {
					continue
				}
				as, err := store.FixtureApprovals(id, domain.FixtureHash(fx))
				if err != nil {
					ok = false
					break
				}
				for _, a := range as {
					emails = append(emails, a.Email)
				}
			}
		}
		if !ok || len(emails) == 0 {
			continue
		}
		all := true
		for _, m := range emails {
			if !authors[strings.ToLower(strings.TrimSpace(m))] {
				all = false
			}
		}
		if all {
			marked[e.Obligation.ID] = true
		}
	}
	return marked
}
