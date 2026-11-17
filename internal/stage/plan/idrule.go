package plan

import (
	"encoding/json"
	"regexp"
	"slices"

	"github.com/jancuk/semar/internal/knowledge"
)

var idName = regexp.MustCompile(`(?:^id$|Id$|ID$|_id$|Ids$|IDs$|_ids$)`)

// idRule is D9 of Plan 31: a step that expects no refusal may not send an ID that no step and no
// fixture gave. An ID is a query parameter or a top-level body field whose name says so, or whose
// format is uuid.
type idRule struct {
	ticket  string // the text of the ticket file of the criterion, which may name an ID on purpose
	refusal bool   // the step expects a status of 400 to 499, and sends a wrong ID on purpose
}

// typedIn says whether v, the value of a parameter or a field, is an ID that nothing gave. A value
// that holds a variable, a value of an enum of the document, and a value that the ticket names as a
// whole word are kept. A value that is not text or a number is not an ID.
func (r idRule) typedIn(name, format string, enum []string, v any) bool {
	if r.refusal || (!idName.MatchString(name) && format != "uuid") {
		return false
	}
	var text string
	switch x := v.(type) {
	case string:
		text = x
	case json.Number:
		text = x.String()
	default:
		return false
	}
	return !refPattern.MatchString(text) && !slices.Contains(enum, text) && !namesWord(r.ticket, text)
}

// namesWord says whether text holds word, with no letter, digit, underscore, or hyphen on either side.
func namesWord(text, word string) bool {
	if word == "" || text == "" {
		return false
	}
	re := regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(word) + `(?:$|[^A-Za-z0-9_-])`)
	return re.MatchString(text)
}

func ticketText(tickets []knowledge.Ticket, key string) string {
	for _, t := range tickets {
		if t.Key == key {
			return t.Text
		}
	}
	return ""
}
