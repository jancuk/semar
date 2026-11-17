package activity

import (
	"net/url"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
)

// fromWebAction describes a browser action. A write is an update: a click or a drag may move data, but
// the action does not name the record, so it has no object and the ledger of data touched leaves it out.
func fromWebAction(a domain.Action, class effects.Class, evidencePath string) domain.Activity {
	w := a.Web
	verb := domain.VerbRead
	switch {
	case class == effects.Write:
		verb = domain.VerbUpdate
	case domain.WebOp(w.Op) == domain.WebOpen:
		verb = domain.VerbVisit
	}
	target := w.Op
	switch {
	case w.Op == string(domain.WebOpen):
		target += " " + pathOf(w.URL) + fragmentOf(w.URL)
	case w.Screen != "":
		target += " " + w.Screen
	}
	return domain.Activity{
		Seq:        a.Seq,
		At:         a.At,
		Actor:      a.By,
		Use:        a.Use,
		Surface:    a.Surface,
		Verb:       verb,
		Target:     strings.TrimSpace(target),
		Result:     webResult(a),
		Why:        a.Why,
		TraceID:    a.TraceID,
		Evidence:   evidencePath,
		Screenshot: w.Screenshot,
	}
}

func webResult(a domain.Action) string {
	switch {
	case a.Refused != "":
		return "refused: " + a.Refused
	case a.Err != "":
		return "error: " + a.Err
	}
	return "done"
}

// fragmentOf is the route that an app keeps after the hash, such as #/boards/b-1. The query is left
// out, as it is for a request.
func fragmentOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Fragment != "" {
		return "#" + u.Fragment
	}
	return ""
}
