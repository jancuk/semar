package plan

import (
	"context"
	"fmt"
	"slices"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/openapi"
)

// maxTrialProblems bounds what one trial sends back to the planner.
const maxTrialProblems = 20

// Trialer sends the read-only steps of an accepted draft once. A nil Trialer turns the trial off.
type Trialer interface {
	Try(ctx context.Context, scenarios []domain.Scenario, scripts []domain.Script) (Trial, error)
}

// Trial is what one trial run found. It holds statuses and names, never a value of a response.
type Trial struct {
	Skipped string            // why nothing was sent, as one line; "" when the trial ran
	Run     domain.RunID      // the run that holds the actions
	Env     string            // the environment name
	Builds  []domain.BuildRef // read once, before the first request
	Steps   []TrialStep
}

type TrialResult string

const (
	TrialSent     TrialResult = "sent"
	TrialNotTried TrialResult = "not_tried"
)

type TrialStep struct {
	Scenario domain.ScenarioID
	Step     domain.StepID // "" for a given entry
	Given    string        // the alias of a given entry
	Result   TrialResult
	Why      string // for not_tried, a fixed reason
	Method   string
	Route    string   // the step's hint, or the fixture's path template
	Status   int      // 0 when no response
	Missing  []string // capture names that found no value
	Err      string   // a transport error, from the recorded action
	Found    bool     // for a given entry: find gave a record
	Action   domain.ActionID
}

// trialProblems turns a trial into the problems that go back to the planner and the steps that
// a person should look at. It reads statuses and capture names only, never a response value
// (D7). base holds the API documents at the merge base and head the documents at the head
// commit, as in D5. A skipped trial gives nothing.
func trialProblems(t Trial, scenarios []domain.Scenario, base, head []openapi.Document, profile string) (back []Problem, secondLook []TrialStep) {
	if t.Skipped != "" {
		return nil, nil
	}
	var all []Problem
	for _, ts := range t.Steps {
		if ts.Result != TrialSent || ts.Given != "" || ts.Step == "" || ts.Status == 0 {
			continue
		}
		i := slices.IndexFunc(scenarios, func(sc domain.Scenario) bool { return sc.ID == ts.Scenario })
		if i < 0 {
			continue
		}
		j := slices.IndexFunc(scenarios[i].Steps, func(st domain.Step) bool { return st.ID == ts.Step })
		if j < 0 {
			continue
		}
		path := fmt.Sprintf("scenarios[%d].steps[%d]", i, j)
		on := fmt.Sprintf("%s %s", ts.Method, ts.Route)
		refusalExpected := expectsRefusal(scenarios[i].Steps[j])
		problem := func(format string, args ...any) {
			all = append(all, Problem{Path: path, Message: "trial: " + fmt.Sprintf(format, args...)})
		}
		switch {
		case ts.Status >= 500:
			secondLook = append(secondLook, ts)
		case refusalExpected && ts.Status >= 400:
		case ts.Status == 404:
			switch notFound(ts, base, head) {
			case routeHadBefore:
				problem("the environment answered 404 for %s, a route that the API document had before the change: an ID in the path is likely wrong", on)
			case routeNowhere:
				problem("the environment answered 404 for %s, and no API document has this route: the route is likely wrong", on)
			default:
				secondLook = append(secondLook, ts)
			}
		case ts.Status == 405:
			problem("the environment answered 405 for %s: the method is likely wrong for this route", on)
		case ts.Status == 400 || ts.Status == 422:
			problem("the environment answered %d for %s: a query parameter or a header is likely wrong", ts.Status, on)
		case ts.Status == 401 || ts.Status == 403:
			problem("the environment answered %d for %s as auth profile %s: the route may need another account, or a header is wrong", ts.Status, on, profile)
		case ts.Status < 400 && !refusalExpected:
			for _, name := range ts.Missing {
				problem("capture %s found no value in the response of %s: the JSONPath is likely wrong, or the environment holds no such record and the scenario needs a fixture", name, on)
			}
		}
	}
	if len(all) > maxTrialProblems {
		rest := len(all) - maxTrialProblems
		all = append(all[:maxTrialProblems], Problem{Message: fmt.Sprintf("trial: and %d more problems of the same kinds", rest)})
	}
	return all, secondLook
}

// expectsRefusal reads the scenario, never the response: a step that expects a status from 400
// to 499 is testing a refusal, so a refusal from the environment is no problem (D6).
func expectsRefusal(st domain.Step) bool {
	return slices.ContainsFunc(st.Expect, func(e domain.Expectation) bool {
		return e.Check.Status != nil && *e.Check.Status >= 400 && *e.Check.Status < 500
	})
}

type notFoundKind int

const (
	routeNew       notFoundKind = iota // a second look
	routeHadBefore                     // the API document had it at the merge base
	routeNowhere                       // no API document has it
)

// notFound judges a 404 with the API documents (D5). A route that only the head document has
// may be new and not deployed yet. With no document, code cannot tell.
func notFound(ts TrialStep, base, head []openapi.Document) notFoundKind {
	has := func(docs []openapi.Document) bool {
		return slices.ContainsFunc(docs, func(d openapi.Document) bool { return d.Lookup(ts.Method, ts.Route).PathKnown })
	}
	switch {
	case has(base):
		return routeHadBefore
	case len(base) == 0 && len(head) == 0, has(head):
		return routeNew
	}
	return routeNowhere
}

// secondLookText is what plan.md says beside a step that needs a person.
func secondLookText(status int) string {
	if status == 404 {
		return "404: the route is not on the environment yet, or the path is wrong"
	}
	return fmt.Sprintf("%d: the service failed; this may be a defect", status)
}
