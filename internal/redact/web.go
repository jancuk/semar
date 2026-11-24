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
	w.Title = s.text(w.Title)
	if w.Target != nil {
		t := locator(s, *w.Target)
		w.Target = &t
	}
	if w.Elements != nil {
		elements := make(map[string]domain.ElementState, len(w.Elements))
		for _, name := range slices.Sorted(maps.Keys(w.Elements)) {
			elements[name] = elementState(s, w.Elements[name])
		}
		w.Elements = elements
	}
	if w.Repair != nil {
		w.Repair = candidates(s, w.Repair)
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
	l.ID, l.Desc, l.Class, l.XPath = s.text(l.ID), s.text(l.Desc), s.text(l.Class), s.text(l.XPath)
	l.HasText = s.text(l.HasText)
	return l
}
