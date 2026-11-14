package check

import "github.com/jancuk/semar/internal/domain"

const noServerError = "no server error"

// Invariants returns the fixed checks for one step. In this plan: no 5xx
// response, unless an expected result of the step asks for that status.
func Invariants(step domain.Step, a domain.Action) []Result {
	r := Result{Expected: noServerError}
	if a.Response == nil {
		r.State = domain.CheckNotEvaluated
		r.Detail = noResponse
		return []Result{r}
	}
	status := a.Response.Status
	r.Observed = statusText(status)
	switch {
	case status < 500:
		r.State = domain.CheckTrue
	case expectsStatus(step, status):
		r.State = domain.CheckTrue
		r.Detail = "the step expects " + statusText(status)
	default:
		r.State = domain.CheckFalse
		r.Detail = "the response status is 500 or above"
	}
	return []Result{r}
}

func expectsStatus(step domain.Step, status int) bool {
	for _, e := range step.Expect {
		if e.Check.Status != nil && *e.Check.Status == status {
			return true
		}
	}
	return false
}
