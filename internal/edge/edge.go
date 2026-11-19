// Package edge drafts edge cases of an API operation from the API document, with no model. A case is
// a copy of a request that passed, with one change, and the status that the document declares for it.
// Everything here is a pure function of the document and the request.
package edge

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/openapi"
)

// Kind is what an edge case changes. Priority is the position of a kind in the table below.
type Kind string

const (
	RequiredField Kind = "required-field"
	WrongType     Kind = "wrong-type"
	OutOfRange    Kind = "out-of-range"
	NotInEnum     Kind = "not-in-enum"
	UnknownID     Kind = "unknown-id"
	NoSession     Kind = "no-session"
	// OtherAccount is not made by a rule of the table: the status of such a case is what another account
	// was answered when the plan tried the request, so the plan makes it after the trial.
	OtherAccount Kind = "other-account"
)

// Reasons that a case is left out, as the plan says them.
const (
	ReasonNoStatus   = "the API document declares no fitting status"
	ReasonDropped    = "a person dropped it in an earlier review"
	ReasonWrite      = "it writes, and a write is never tried as another account"
	ReasonNoAnswer   = "the environment gave no answer to try it as another account"
	ReasonServerFail = "the environment failed when the request was tried as another account"
	ReasonFixture    = "its base uses a fixture"
	ReasonTooLong    = "the value would be longer than the plan writes"
)

// Case is one edge case of one operation. Request is the base request with the one change. Status is
// the status that the document declares for it, and 0 for OtherAccount until the plan has tried it.
type Case struct {
	Kind      Kind
	On        string // the field, the parameter or the account that the case changes; "" for no session
	Title     string // what changes, as the end of a sentence about the request
	Status    int
	Cite      string // where the status comes from, as the expected result says it
	Request   domain.APIRequest
	NoSession bool
}

// Left is a case that the plan did not draft, and why.
type Left struct {
	Kind   Kind
	On     string
	Reason string
}

// change is one change of a kind, before the status is known.
type change struct {
	on, title string
	apply     func(r *domain.APIRequest)
	noSession bool
	skip      string // why the change cannot be drafted, when the rule found it and could not write it
}

// rule makes the changes of one kind and says which status the document declares for them.
type rule struct {
	kind   Kind
	status func(op openapi.Operation) (int, bool)
	find   func(op openapi.Operation, base domain.APIRequest) []change
}

// table lists the kinds in priority order. A new kind is one more row.
var table = []rule{
	{RequiredField, badRequest, requiredFields},
	{WrongType, badRequest, wrongTypes},
	{OutOfRange, badRequest, outOfRange},
	{NotInEnum, badRequest, notInEnum},
	{UnknownID, declares(404), unknownIDs},
	{NoSession, declares(401), noSession},
}

// Priority is the rank of a kind, from 0. OtherAccount ranks after every kind of the table.
func Priority(k Kind) int {
	if i := slices.IndexFunc(table, func(r rule) bool { return r.kind == k }); i >= 0 {
		return i
	}
	return len(table)
}

// Kinds lists the kinds of the table in priority order, then OtherAccount.
func Kinds() []Kind {
	out := make([]Kind, 0, len(table)+1)
	for _, r := range table {
		out = append(out, r.kind)
	}
	return append(out, OtherAccount)
}

// Generate lists the edge cases of one operation for one request of a draft that passed, in priority
// order, and the cases that it found and could not draft.
func Generate(op openapi.Operation, base domain.APIRequest) ([]Case, []Left) {
	var cases []Case
	var left []Left
	for _, r := range table {
		for _, ch := range r.find(op, base) {
			status, ok := r.status(op)
			switch {
			case ch.skip != "":
				left = append(left, Left{r.kind, ch.on, ch.skip})
				continue
			case !ok:
				left = append(left, Left{r.kind, ch.on, ReasonNoStatus})
				continue
			}
			req := cloneRequest(base)
			if ch.apply != nil {
				ch.apply(&req)
			}
			cases = append(cases, Case{
				Kind: r.kind, On: ch.on, Title: ch.title, Status: status, Request: req, NoSession: ch.noSession,
				Cite: fmt.Sprintf("the API document declares %d for %s %s", status, op.Method, op.Path),
			})
		}
	}
	return cases, left
}

// OtherAccountCase is the case of the request as another account. Its status is set after the trial.
func OtherAccountCase(base domain.APIRequest, account string) Case {
	return Case{Kind: OtherAccount, On: account, Title: "as account " + account, Request: cloneRequest(base)}
}

// badRequest is the status of a case that sends a value the document does not allow: 400 first, then
// 422, then another 4xx that the operation declares. 401, 403, 404 and 405 never are, because each
// says something else than that the request is malformed.
func badRequest(op openapi.Operation) (int, bool) {
	for _, want := range []int{400, 422} {
		if declared(op, want) {
			return want, true
		}
	}
	var others []int
	for _, s := range op.Statuses {
		n, err := strconv.Atoi(s)
		if err != nil || n < 400 || n > 499 || slices.Contains([]int{400, 401, 403, 404, 405, 422}, n) {
			continue
		}
		others = append(others, n)
	}
	if len(others) == 0 {
		return 0, false
	}
	sort.Ints(others)
	return others[0], true
}

func declares(status int) func(openapi.Operation) (int, bool) {
	return func(op openapi.Operation) (int, bool) { return status, declared(op, status) }
}

// declared says whether the operation lists exactly this status. A range such as 4XX and default do not
// name one status, so no expected result can rest on them.
func declared(op openapi.Operation, status int) bool {
	return slices.Contains(op.Statuses, strconv.Itoa(status))
}

// Candidate is a case with the operation and the place in the plan that it came from.
type Candidate struct {
	Case
	Operation string // the key of the operation, with the ticket in front when the plan has several
	Order     int    // the order in which the plan found it
}

// Select keeps the first perOperation cases of each operation and the first total cases of the plan, by
// priority and then by the order in which they were found. It returns the kept cases in that order, and
// the others as left out.
func Select(in []Candidate, perOperation, total int) ([]Candidate, []Left) {
	byPriority := func(a, b Candidate) int {
		if d := Priority(a.Kind) - Priority(b.Kind); d != 0 {
			return d
		}
		return a.Order - b.Order
	}
	var left []Left
	var kept []Candidate
	seen := map[string]int{}
	for _, c := range slices.SortedStableFunc(slices.Values(in), byPriority) {
		if seen[c.Operation] >= perOperation {
			left = append(left, Left{c.Kind, c.On, fmt.Sprintf("it is over the limit of %d for one operation", perOperation)})
			continue
		}
		seen[c.Operation]++
		kept = append(kept, c)
	}
	if len(kept) > total {
		for _, c := range kept[total:] {
			left = append(left, Left{c.Kind, c.On, fmt.Sprintf("it is over the limit of %d for one plan", total)})
		}
		kept = kept[:total]
	}
	slices.SortStableFunc(kept, func(a, b Candidate) int { return a.Order - b.Order })
	return kept, left
}

// CountLeft sums the reasons, in the order of their first appearance.
func CountLeft(left []Left) (reasons []string, counts map[string]int) {
	counts = map[string]int{}
	for _, l := range left {
		if counts[l.Reason] == 0 {
			reasons = append(reasons, l.Reason)
		}
		counts[l.Reason]++
	}
	return reasons, counts
}

// Summary is the line that semar plan prints: how many cases it drafted, how many it left out, and why.
func Summary(drafted int, left []Left) string {
	var b strings.Builder
	fmt.Fprintf(&b, "edge cases: %d drafted", drafted)
	if len(left) > 0 {
		reasons, counts := CountLeft(left)
		parts := make([]string, len(reasons))
		for i, r := range reasons {
			parts[i] = fmt.Sprintf("%d because %s", counts[r], r)
		}
		fmt.Fprintf(&b, ", %d left out (%s)", len(left), strings.Join(parts, "; "))
	}
	if drafted > 0 {
		b.WriteString("; each needs your review")
	}
	return b.String()
}
