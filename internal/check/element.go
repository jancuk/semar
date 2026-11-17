package check

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/redact"
)

// NotFound is why a step or a check that needs one element could not use it. A locator that matches
// nothing, or several elements, is the same case: Semar cannot tell a changed page from an old script.
const NotFound = "an element was not found: either the product changed or the script is out of date"

// evalElement decides a check of an element against what the engine read after the action. Only exists
// and count may fail on an element that is not there. A check of the text of an element that was not
// found is not evaluated, so the step ends inconclusive and never failed (TRD rule 5).
func evalElement(res *Result, c domain.Check, op string, w *domain.WebRecord) {
	if w == nil {
		notEvaluated(res, "no element was read: the step is not a browser step")
		return
	}
	st, read := w.Elements[c.Element]
	if !read {
		notEvaluated(res, "the step did not read element "+c.Element)
		return
	}
	res.Observed = observedElement(st)
	switch op {
	case "exists":
		res.State = state((st.Count > 0) == *c.Exists)
	case "count":
		res.State = state(st.Count == *c.Count)
	case "equals", "notEquals", "contains":
		evalElementText(res, c, op, c.Element, st)
	default:
		notEvaluated(res, "the operator "+op+" is not supported on an element check")
	}
}

func evalElementText(res *Result, c domain.Check, op, name string, st domain.ElementState) {
	if !st.Found {
		notEvaluated(res, fmt.Sprintf("%s (element %s matched %d)", NotFound, name, st.Count))
		return
	}
	if redact.HasMarker(st.Text) {
		notEvaluated(res, redactedValue)
		return
	}
	res.Observed = st.Text
	switch op {
	case "equals":
		res.State = state(st.Text == text(c.Equals))
	case "notEquals":
		res.State = state(st.Text != text(c.NotEquals))
	default:
		res.State = state(strings.Contains(st.Text, text(c.Contains)))
	}
}

func observedElement(st domain.ElementState) string {
	switch {
	case st.Count == 0:
		return "no element"
	case st.Found:
		return "1 element: " + st.Text
	case st.Count == 1:
		return "1 element, not visible"
	}
	return strconv.Itoa(st.Count) + " elements"
}
