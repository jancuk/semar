package script

import (
	"fmt"
	"slices"
	"sort"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/domain"
)

type Problem struct {
	Step    domain.StepID
	Message string
}

var knownMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}

// Validate returns every reason the script cannot stand for the scenario.
// An empty hash never matches, even when both sides are empty.
func Validate(sc domain.Scenario, s domain.Script) []Problem {
	var ps []Problem
	add := func(step domain.StepID, format string, args ...any) {
		ps = append(ps, Problem{Step: step, Message: fmt.Sprintf(format, args...)})
	}

	if s.APIVersion != "semar/v1" {
		add("", "apiVersion must be semar/v1")
	}
	if s.Kind != "Script" {
		add("", "kind must be Script")
	}
	if s.Scenario != sc.ID {
		add("", "the script is for scenario %s and not %s", s.Scenario, sc.ID)
	}
	want := domain.ExpectHash(sc)
	switch {
	case want == "":
		add("", "the scenario has no expectation hash")
	case s.ExpectHash == "":
		add("", "the script has no expectation hash")
	case s.ExpectHash != want:
		add("", "the script was written for other expected results")
	}

	index := make(map[domain.StepID]int, len(sc.Steps))
	for i, st := range sc.Steps {
		index[st.ID] = i
	}

	bound := map[domain.StepID]bool{}
	captured := map[string]bool{}
	lastIndex, outOfOrder := -1, false
	for _, ss := range s.Steps {
		i, known := index[ss.Step]
		if !known {
			add(ss.Step, "step %s is not in the scenario", ss.Step)
		} else if bound[ss.Step] {
			add(ss.Step, "step %s is bound more than once", ss.Step)
		} else {
			if i < lastIndex {
				outOfOrder = true
			}
			lastIndex = max(lastIndex, i)
		}
		bound[ss.Step] = true

		if ss.Request != nil {
			var hint *domain.RequestHint
			if known {
				hint = sc.Steps[i].Hint
			}
			ps = append(ps, validateRequest(ss, hint, len(captured) > 0, captured)...)
		}

		seen := map[string]bool{}
		for _, c := range ss.Captures {
			switch {
			case c.Name == "":
				add(ss.Step, "a capture has no name")
			case !namePattern.MatchString(c.Name):
				add(ss.Step, "capture name %s is not a valid variable name", c.Name)
			}
			if _, err := jsonpath.Parse(c.JSONPath); err != nil {
				add(ss.Step, "capture %s: jsonpath does not parse: %v", c.Name, err)
			}
			if c.Name != "" && (captured[c.Name] || seen[c.Name]) {
				add(ss.Step, "capture %s is defined more than once", c.Name)
			}
			seen[c.Name] = true
		}
		for name := range seen {
			captured[name] = true
		}
	}
	if outOfOrder {
		add("", "steps are not in the scenario's order")
	}

	for _, st := range sc.Steps {
		if len(st.Expect) > 0 && !hasRequest(s, st.ID) {
			add(st.ID, "step %s has an expected result and no request", st.ID)
		}
	}
	return ps
}

func hasRequest(s domain.Script, id domain.StepID) bool {
	return slices.ContainsFunc(s.Steps, func(ss domain.ScriptStep) bool { return ss.Step == id && ss.Request != nil })
}

func validateRequest(ss domain.ScriptStep, hint *domain.RequestHint, hasEarlierCapture bool, captured map[string]bool) []Problem {
	var ps []Problem
	add := func(format string, args ...any) {
		ps = append(ps, Problem{Step: ss.Step, Message: fmt.Sprintf(format, args...)})
	}
	r := ss.Request

	if r.Service == "" {
		add("the request has no service")
	}
	switch {
	case r.Method == "":
		add("the request has no method")
	case !slices.Contains(knownMethods, r.Method):
		add("method %s is not one of %v", r.Method, knownMethods)
	}
	if len(r.Path) == 0 || r.Path[0] != '/' {
		add("path %s must start with /", r.Path)
	}

	names := variables(*r)
	sort.Strings(names)
	for i, n := range names {
		if (i == 0 || names[i-1] != n) && !captured[n] {
			add("%s is used before any step captures it", n)
		}
	}

	if hint == nil {
		return ps
	}
	if r.Method != hint.Method {
		add("the request is %s and the step expects %s", r.Method, hint.Method)
	}
	if !MatchPath(hint.Path, r.Path) {
		add("the request path %s does not match %s", r.Path, hint.Path)
	} else if hasEarlierCapture && !usesVariableAtParams(hint.Path, r.Path) {
		add("the request path %s does not use a value from an earlier step", r.Path)
	}
	return ps
}

// usesVariableAtParams requires a {{variable}} in every position where the hint has {param}.
// The caller has already matched the two paths, so both split cleanly.
func usesVariableAtParams(hint, actual string) bool {
	h, _ := segments(hint)
	a, _ := segments(actual)
	for i := range h {
		if isParam(h[i]) && !variablePattern.MatchString(a[i]) {
			return false
		}
	}
	return true
}
