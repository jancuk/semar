package redact

import (
	"maps"
	"slices"

	"github.com/jancuk/semar/internal/domain"
)

// webRecord removes the secrets from what a browser action recorded: the address of the page, the
// text of the elements that were read, and the locators, which may hold a value that a step filled in.
// The screenshot is a path, and the engine writes no secret into a path.
func webRecord(s *scrubber, w domain.WebRecord) domain.WebRecord {
	w.URL = s.text(w.URL)
	if w.Target != nil {
		t := locator(s, *w.Target)
		w.Target = &t
	}
	if w.Elements != nil {
		elements := make(map[string]domain.ElementState, len(w.Elements))
		for _, name := range slices.Sorted(maps.Keys(w.Elements)) {
			st := w.Elements[name]
			st.Text = s.text(st.Text)
			if st.Locator != nil {
				l := locator(s, *st.Locator)
				st.Locator = &l
			}
			elements[name] = st
		}
		w.Elements = elements
	}
	if w.Repair != nil {
		repair := make([]domain.Candidate, len(w.Repair))
		for i, c := range w.Repair {
			c.Locator = locator(s, c.Locator)
			c.Why = s.text(c.Why)
			repair[i] = c
		}
		w.Repair = repair
	}
	if w.Blocked != nil {
		blocked := make([]string, len(w.Blocked))
		for i, h := range w.Blocked {
			blocked[i] = s.text(h)
		}
		w.Blocked = blocked
	}
	if w.SignIn != nil {
		w.SignIn = &domain.WebSignIn{Account: s.text(w.SignIn.Account), User: s.text(w.SignIn.User)}
	}
	return w
}

func locator(s *scrubber, l domain.Locator) domain.Locator {
	l.TestID, l.Role, l.Name = s.text(l.TestID), s.text(l.Role), s.text(l.Name)
	l.Text, l.CSS = s.text(l.Text), s.text(l.CSS)
	return l
}
