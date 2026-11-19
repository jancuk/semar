package check

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/redact"
)

// NotFound is why a step or a check that needs one element could not use it. A locator that matches
// nothing is the case: Semar cannot tell a changed page from an old script.
const NotFound = "an element was not found: either the product changed or the script is out of date"

// matchesMany is why a check of one element is not decided when the locator matches several. The
// check could hold for one of them and not for the others, so no answer is a pass.
const matchesMany = "the locator matches %d elements"

// evalElement decides a check of the page or of an element against what the engine read after the
// action. Only exists, count, hidden, and visible may fail on an element that is not there. A check
// that reads one element, which is not found or which the locator does not pick out alone, is not
// evaluated, so the step ends inconclusive and never failed (TRD rule 5).
func evalElement(res *Result, c domain.Check, op string, w *domain.WebRecord) {
	if w == nil {
		notEvaluated(res, "nothing was read: the step is not a browser step")
		return
	}
	if c.Page != "" {
		evalPage(res, c, op, w)
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
	case "state":
		evalElementState(res, c, st, w)
	case "equals", "notEquals", "contains":
		if c.Attribute != "" {
			evalElementAttribute(res, c, op, st, w)
		} else {
			evalElementText(res, c, op, st, w)
		}
	default:
		notEvaluated(res, "the operator "+op+" is not supported on an element check")
	}
}

// alone says whether the locator picked out one element. When it did not, it sets the reason.
func alone(res *Result, c domain.Check, st domain.ElementState, w *domain.WebRecord) bool {
	switch {
	case st.Count > 1:
		reason := fmt.Sprintf(matchesMany, st.Count)
		if names := namesOf(w, c.Element); names != "" {
			reason += ": " + names
		}
		notEvaluated(res, reason+". Make the locator of element "+c.Element+" match one element")
		return false
	case st.Count == 0:
		notEvaluated(res, fmt.Sprintf("%s (element %s matched 0)", NotFound, c.Element))
		return false
	}
	return true
}

// namesOf lists the elements that the engine named for an element that matched several, as the repair
// names its candidates.
func namesOf(w *domain.WebRecord, element string) string {
	var out []string
	for _, cand := range w.Repair {
		if cand.Element == element {
			out = append(out, cand.Locator.String())
		}
	}
	return strings.Join(out, ", ")
}

func evalElementText(res *Result, c domain.Check, op string, st domain.ElementState, w *domain.WebRecord) {
	if !alone(res, c, st, w) {
		return
	}
	if !st.Found {
		notEvaluated(res, fmt.Sprintf("%s (element %s is not visible)", NotFound, c.Element))
		return
	}
	if redact.HasMarker(st.Text) {
		notEvaluated(res, redactedValue)
		return
	}
	res.Observed = st.Text
	res.State = compareText(st.Text, c, op)
}

func evalElementAttribute(res *Result, c domain.Check, op string, st domain.ElementState, w *domain.WebRecord) {
	if !alone(res, c, st, w) {
		return
	}
	got, has := st.Attrs[c.Attribute]
	if !has {
		res.Observed = "no attribute " + c.Attribute
		res.State = state(op == "notEquals")
		return
	}
	if redact.HasMarker(got) {
		notEvaluated(res, redactedValue)
		return
	}
	res.Observed = got
	res.State = compareText(got, c, op)
}

func compareText(got string, c domain.Check, op string) domain.CheckState {
	switch op {
	case "equals":
		return state(got == text(c.Equals))
	case "notEquals":
		return state(got != text(c.NotEquals))
	}
	return state(strings.Contains(got, text(c.Contains)))
}

func evalElementState(res *Result, c domain.Check, st domain.ElementState, w *domain.WebRecord) {
	switch c.State {
	case "hidden":
		res.State = state(!st.Visible)
		return
	case "visible":
		if st.Count == 0 {
			res.State = domain.CheckFalse
			return
		}
	}
	if !alone(res, c, st, w) {
		return
	}
	switch c.State {
	case "visible":
		res.State = state(st.Visible)
	case "enabled":
		res.State = state(st.Enabled)
	case "disabled":
		res.State = state(!st.Enabled)
	case "checked":
		res.State = state(st.Checked)
	case "unchecked":
		res.State = state(!st.Checked)
	}
}

func evalPage(res *Result, c domain.Check, op string, w *domain.WebRecord) {
	var got string
	switch c.Page {
	case domain.PageTitle:
		got = w.Title
	default:
		u, err := url.Parse(w.URL)
		if err != nil || w.URL == "" {
			notEvaluated(res, "the address of the page was not recorded")
			return
		}
		got = u.Path
		if got == "" {
			got = "/"
		}
	}
	res.Observed = got
	if redact.HasMarker(got) {
		notEvaluated(res, redactedValue)
		return
	}
	res.State = compareText(got, c, op)
}

func observedElement(st domain.ElementState) string {
	switch {
	case st.Count == 0:
		return "no element"
	case st.Count > 1:
		return strconv.Itoa(st.Count) + " elements"
	case st.Found:
		return "1 element: " + st.Text
	case st.Count == 1:
		return "1 element, not visible"
	}
	return strconv.Itoa(st.Count) + " elements"
}
