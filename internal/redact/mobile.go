package redact

import (
	"maps"
	"slices"

	"github.com/jancuk/semar/internal/domain"
)

// mobileRecord removes the secrets from what a device action recorded: the link that an open opened,
// the text of the elements that were read, and the locators, which may hold a value that a step typed.
// The screenshot is a path, and the engine writes no secret into a path. A typed secret is never in the
// record at all, because the engine does not put what it typed there: this is the second guard.
func mobileRecord(s *scrubber, m domain.MobileRecord) domain.MobileRecord {
	m.Link = s.text(m.Link)
	m.Package = s.text(m.Package)
	m.Device = s.text(m.Device)
	if m.Target != nil {
		t := locator(s, *m.Target)
		m.Target = &t
	}
	if m.Elements != nil {
		elements := make(map[string]domain.ElementState, len(m.Elements))
		for _, name := range slices.Sorted(maps.Keys(m.Elements)) {
			elements[name] = elementState(s, m.Elements[name])
		}
		m.Elements = elements
	}
	if m.Repair != nil {
		m.Repair = candidates(s, m.Repair)
	}
	if m.Nearby != nil {
		m.Nearby = candidates(s, m.Nearby)
	}
	if m.Crash != nil {
		c := *m.Crash
		c.Summary = s.text(c.Summary)
		m.Crash = &c
	}
	return m
}

// elementState removes the secrets from what was read of one element.
func elementState(s *scrubber, st domain.ElementState) domain.ElementState {
	st.Text = s.text(st.Text)
	if st.Attrs != nil {
		attrs := make(map[string]string, len(st.Attrs))
		for k, v := range st.Attrs {
			attrs[k] = s.text(v)
		}
		st.Attrs = attrs
	}
	if st.Locator != nil {
		l := locator(s, *st.Locator)
		st.Locator = &l
	}
	return st
}

func candidates(s *scrubber, in []domain.Candidate) []domain.Candidate {
	out := make([]domain.Candidate, len(in))
	for i, c := range in {
		c.Locator = locator(s, c.Locator)
		c.Why = s.text(c.Why)
		out[i] = c
	}
	return out
}
