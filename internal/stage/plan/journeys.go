package plan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/script"
)

// legCall is one route that a leg of a journey calls and that drafted scenarios call too.
type legCall struct {
	Leg          domain.LegID
	Method, Path string
	Scenarios    []domain.ScenarioID
}

// rerun is a journey with the routes of its legs that the plan touches.
type rerun struct {
	Journey domain.Journey
	Calls   []legCall
}

// journeysToRerun lists the journeys with a leg step that calls a route which a drafted step of the
// plan calls: the same service, the same method, and the same path template, with the matcher of
// scripts applied in both directions, so that a parameter on either side fits. The impact map of
// the TRD does not exist yet, and a drafted hint is a route that code checked against the source
// (Plan 18, D14). A journey that no drafted step touches is left out.
func journeysToRerun(journeys []domain.Journey, scenarios []domain.Scenario, scripts []domain.Script) []rerun {
	services := map[domain.ScenarioID]map[domain.StepID]string{}
	for _, sc := range scripts {
		m := map[domain.StepID]string{}
		for _, st := range sc.Steps {
			if st.Request != nil {
				m[st.Step] = st.Request.Service
			}
		}
		services[sc.Scenario] = m
	}
	var out []rerun
	for _, j := range journeys {
		r := rerun{Journey: j}
		for _, leg := range j.Legs {
			var calls []legCall
			for _, st := range leg.Steps {
				if st.Hint == nil {
					continue
				}
				c := legCall{Leg: leg.ID, Method: st.Hint.Method, Path: st.Hint.Path}
				for _, sc := range scenarios {
					if touches(sc, services[sc.ID], leg.Service, *st.Hint) && !slices.Contains(c.Scenarios, sc.ID) {
						c.Scenarios = append(c.Scenarios, sc.ID)
					}
				}
				// Two steps of a leg may call one route, and it is listed once.
				if len(c.Scenarios) > 0 && !slices.ContainsFunc(calls, func(x legCall) bool { return x.Method == c.Method && x.Path == c.Path }) {
					calls = append(calls, c)
				}
			}
			r.Calls = append(r.Calls, calls...)
		}
		if len(r.Calls) > 0 {
			out = append(out, r)
		}
	}
	return out
}

// touches reports whether a step of the scenario calls the hint's route on the service.
func touches(sc domain.Scenario, steps map[domain.StepID]string, service string, hint domain.RequestHint) bool {
	for _, st := range sc.Steps {
		if st.Hint == nil || steps[st.ID] != service || st.Hint.Method != hint.Method {
			continue
		}
		if script.MatchPath(st.Hint.Path, hint.Path) || script.MatchPath(hint.Path, st.Hint.Path) {
			return true
		}
	}
	return false
}

// writeJourneysToRerun is the section that says which business flows to run again. It is left out
// when the repository has no journey file.
func writeJourneysToRerun(b *strings.Builder, in Input, out Output) {
	if len(in.Journeys) == 0 {
		return
	}
	b.WriteString("\n## Journeys to rerun\n\n")
	reruns := journeysToRerun(in.Journeys, out.Scenarios, out.Scripts)
	if len(reruns) == 0 {
		b.WriteString("No journey calls a route that this plan tests.\n")
		return
	}
	value := func(s string) string { return shown(s, maxValueShown) }
	for _, r := range reruns {
		clauses := make([]string, len(r.Calls))
		for i, c := range r.Calls {
			clauses[i] = fmt.Sprintf("leg %s calls %s %s, which %s", value(string(c.Leg)), value(c.Method), value(c.Path), testers(c.Scenarios, value))
		}
		run := "semar journey run " + value(string(r.Journey.ID))
		if in.Environment != "" {
			run += " --env " + value(in.Environment)
		}
		fmt.Fprintf(b, "- %s (%s, owner %s): %s. Run: %s\n", value(string(r.Journey.ID)), value(r.Journey.Criticality), value(r.Journey.Owner), strings.Join(clauses, "; "), run)
	}
}

// testers is "SHOP-482-05 tests", "A and B test", or "A, B, and C test".
func testers(ids []domain.ScenarioID, value func(string) string) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = value(string(id))
	}
	switch len(names) {
	case 1:
		return names[0] + " tests"
	case 2:
		return names[0] + " and " + names[1] + " test"
	}
	return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1] + " test"
}
