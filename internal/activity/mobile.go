package activity

import (
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
	}
}

// withoutQuery cuts a link at its query or its fragment, which may hold a value.
func withoutQuery(link string) string {
	if i := strings.IndexAny(link, "?#"); i >= 0 {
		return link[:i]
	}
	return link
}
