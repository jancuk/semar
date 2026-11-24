package activity

import (
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
)

// fromMobileAction describes a device action. A write is an update: a tap may move data, but the
// action does not name the record, so it has no object and the ledger of data touched leaves it out.
func fromMobileAction(a domain.Action, class effects.Class, evidencePath string) domain.Activity {
	m := a.Mobile
	verb := domain.VerbRead
	switch {
	case class == effects.Write:
		verb = domain.VerbUpdate
	case domain.MobileOp(m.Op) == domain.MobileOpenLink || domain.MobileOp(m.Op) == domain.MobileLaunch:
		verb = domain.VerbVisit
	}
	target := m.Op
	switch {
	case m.Op == string(domain.MobileOpenLink):
		target += " " + withoutQuery(m.Link)
	case m.Screen != "":
		target += " " + m.Screen
	case m.Package != "":
		target += " " + m.Package
	}
	result := "done"
	switch {
	case a.Refused != "":
		result = "refused: " + a.Refused
	case a.Err != "":
		result = "error: " + a.Err
	}
	if m.Recovered {
		result += " (the UiAutomator2 server was restarted during this step)"
	}
	var crashFile string
	if m.Crash != nil {
		result += "; the app crashed: " + m.Crash.Summary
		crashFile = m.Crash.File
	}
	return domain.Activity{
		Seq:        a.Seq,
		At:         a.At,
		Actor:      a.By,
		Use:        a.Use,
		Surface:    a.Surface,
		Verb:       verb,
		Target:     strings.TrimSpace(target),
		Result:     result,
		Why:        a.Why,
		TraceID:    a.TraceID,
		Evidence:   evidencePath,
		Screenshot: m.Screenshot,
		Misses:     mobileMisses(m),
		CrashFile:  crashFile,
		LogcatFile: m.LogcatFile,
	}
}

// mobileMisses lists the elements that the action looked for and did not find, in the order of their
// names, with the candidates and the nearby elements that the action stored for each. An element that
// a check only wanted to be absent is not a miss.
func mobileMisses(m *domain.MobileRecord) []domain.ElementMiss {
	var out []domain.ElementMiss
	for _, name := range slices.Sorted(maps.Keys(m.Elements)) {
		st := m.Elements[name]
		if st.Count != 0 || st.Locator == nil || st.Presence {
			continue
		}
		miss := domain.ElementMiss{App: m.App, Screen: m.Screen, Element: name, Looked: *st.Locator}
		for _, c := range m.Repair {
			if c.Element == name {
				miss.Repair = append(miss.Repair, c)
			}
		}
		for _, c := range m.Nearby {
			if c.Element == name {
				miss.Nearby = append(miss.Nearby, c)
			}
		}
		out = append(out, miss)
	}
	return out
}

// withoutQuery cuts a link at its query or its fragment, which may hold a value.
func withoutQuery(link string) string {
	if i := strings.IndexAny(link, "?#"); i >= 0 {
		return link[:i]
	}
	return link
}
