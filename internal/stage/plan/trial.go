package plan

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/openapi"
)

// accepted is a draft that passed validation, with what the stage knows of it.
type accepted struct {
	draft     Draft
	scenarios []domain.Scenario
	scripts   []domain.Script
	notes     []Note
	attempt   int
	output    []byte
	trial     *Trial
	after     []domain.ScenarioID
	// unaccounted are the criteria that nothing cites (D9).
	unaccounted []Criterion
}

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

// TrialCounts says what the trial of an accepted plan did, for the line that semar plan prints.
// Steps counts the script steps that the trial listed, sent or not. Back is how many problems went
// back to the planner over all attempts, and SecondLook how many steps of the accepted draft need a person.
type TrialCounts struct{ Sent, Steps, Back, SecondLook int }

func (o Output) TrialCounts(in Input) TrialCounts {
	var c TrialCounts
	if o.Trial == nil || o.Trial.Skipped != "" {
		return c
	}
	for _, ts := range o.Trial.Steps {
		if ts.Step == "" {
			continue
		}
		c.Steps++
		if ts.Result == TrialSent {
			c.Sent++
		}
	}
	_, second := trialProblems(*o.Trial, o.Scenarios, in.BaseAPI, headDocuments(in.Knowledge), in.AuthProfile)
	c.Back, c.SecondLook = o.TrialReturned, len(second)
	return c
}

// secondLookText is what plan.md says beside a step that needs a person.
func secondLookText(status int) string {
	if status == 404 {
		return "404: the route is not on the environment yet, or the path is wrong"
	}
	return fmt.Sprintf("%d: the service failed; this may be a defect", status)
}

// headDocuments are the API documents of the configuration, as read at the head commit.
func headDocuments(sources []knowledge.Source) []openapi.Document {
	var docs []openapi.Document
	for _, s := range sources {
		if s.Usable() && s.API != nil {
			docs = append(docs, *s.API)
		}
	}
	return docs
}

// scenarioKey matches a scenario between drafts by its ticket and title (D8).
func scenarioKey(sc domain.Scenario) string { return sc.Ticket + "\x00" + normalTitle(sc.Title) }

// routeKey is the method and route hint of the first step, which a rename keeps. It is "" when the
// first step has no hint.
func routeKey(sc domain.Scenario) string {
	if len(sc.Steps) == 0 || sc.Steps[0].Hint == nil {
		return ""
	}
	h := sc.Steps[0].Hint
	return strings.ToUpper(h.Method) + " " + h.Path
}

// frozenScenario is a scenario as the first trial that saw it had it.
type frozenScenario struct {
	key, title, route string
	steps             map[domain.StepID][]domain.Expectation
}

// frozenSet holds the expected results of every scenario that a trial has seen, in the order the
// trials saw them.
type frozenSet struct {
	byKey map[string]*frozenScenario
	all   []*frozenScenario
}

func newFrozen() *frozenSet { return &frozenSet{byKey: map[string]*frozenScenario{}} }

// freeze keeps the expected results of every scenario that a trial has seen. The first trial
// of a scenario decides, so that a scenario that is dropped and drafted again is held to it.
func freeze(frozen *frozenSet, scenarios []domain.Scenario) {
	for _, sc := range scenarios {
		key := scenarioKey(sc)
		if _, ok := frozen.byKey[key]; ok {
			continue
		}
		steps := make(map[domain.StepID][]domain.Expectation, len(sc.Steps))
		for _, st := range sc.Steps {
			steps[st.ID] = st.Expect
		}
		fs := &frozenScenario{key: key, title: sc.Title, route: routeKey(sc), steps: steps}
		frozen.byKey[key] = fs
		frozen.all = append(frozen.all, fs)
	}
}

// afterTrial lists the scenarios that no trial has seen yet.
func afterTrial(frozen *frozenSet, scenarios []domain.Scenario) []domain.ScenarioID {
	if len(frozen.all) == 0 {
		return nil
	}
	var ids []domain.ScenarioID
	for _, sc := range scenarios {
		if _, ok := frozen.byKey[scenarioKey(sc)]; !ok {
			ids = append(ids, sc.ID)
		}
	}
	return ids
}

// changedExpectations is D8: a draft written after a trial keeps the expected results of every
// scenario that it keeps. Only the checks count. A new cite or a new action is a repair. A new step
// with expected results in a kept scenario is a change.
//
// A scenario is matched by ticket and title. One that matches none is matched by the method and
// route of its first step against the tried scenarios that this draft dropped, so that a rename
// cannot carry a changed expected result. It passes when it keeps the expected results of one of them.
func changedExpectations(frozen *frozenSet, scenarios []domain.Scenario) []Problem {
	present := make(map[string]bool, len(scenarios))
	for _, sc := range scenarios {
		present[scenarioKey(sc)] = true
	}
	var ps []Problem
	for i, sc := range scenarios {
		if before, ok := frozen.byKey[scenarioKey(sc)]; ok {
			msg := fmt.Sprintf("the expected results of %q changed after the trial run; an expected result comes from the code, "+
				"never from what the environment answered: keep the expected results of the earlier draft", sc.Title)
			ps = append(ps, stepChanges(before.steps, sc, i, msg)...)
			continue
		}
		route := routeKey(sc)
		if route == "" {
			continue
		}
		var first []Problem
		kept := false
		for _, fs := range frozen.all {
			if fs.route != route || present[fs.key] {
				continue
			}
			msg := fmt.Sprintf("the expected results of %q, which has the first step of %q, changed after the trial run; an expected result comes from the code, "+
				"never from what the environment answered: keep the expected results of the earlier draft", sc.Title, fs.title)
			changes := stepChanges(fs.steps, sc, i, msg)
			if len(changes) == 0 {
				kept = true
				break
			}
			if first == nil {
				first = changes
			}
		}
		if !kept {
			ps = append(ps, first...)
		}
	}
	return ps
}

// stepChanges compares the steps of a scenario with the expected results that a trial froze.
func stepChanges(before map[domain.StepID][]domain.Expectation, sc domain.Scenario, i int, msg string) []Problem {
	var ps []Problem
	seen := map[domain.StepID]bool{}
	for j, st := range sc.Steps {
		seen[st.ID] = true
		want, ok := before[st.ID]
		// A step that the tried draft did not have may not bring expected results of its own.
		if ok && !sameChecks(want, st.Expect) || !ok && len(st.Expect) > 0 {
			ps = append(ps, Problem{Path: fmt.Sprintf("scenarios[%d].steps[%d].expect", i, j), Message: msg})
		}
	}
	for _, id := range slices.Sorted(maps.Keys(before)) {
		if !seen[id] && len(before[id]) > 0 {
			ps = append(ps, Problem{Path: fmt.Sprintf("scenarios[%d]", i), Message: msg})
			break
		}
	}
	return ps
}

func sameChecks(a, b []domain.Expectation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(a[i].Check, b[i].Check) {
			return false
		}
	}
	return true
}
