package scenario

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/domain"
)

type Origin int

const (
	FromFile  Origin = iota // written or edited by a person
	FromAgent               // submitted by an agent
)

type Problem struct{ Path, Message string }

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const idRule = "must match ^[A-Za-z0-9][A-Za-z0-9._-]*$"

func Validate(s domain.Scenario, origin Origin) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	if s.APIVersion != "semar/v1" {
		add("apiVersion", "apiVersion must be semar/v1")
	}
	if s.Kind != "Scenario" {
		add("kind", "kind must be Scenario")
	}
	if !idPattern.MatchString(string(s.ID)) {
		add("id", "id "+idRule)
	}
	if s.Ticket == "" {
		add("ticket", "ticket is required")
	}
	switch s.Surface {
	case domain.SurfaceAPI, domain.SurfaceWeb, domain.SurfaceAndroid:
	default:
		add("surface", "surface must be api, web, or android")
	}
	if len(s.Steps) == 0 {
		add("steps", "at least one step is required")
	}

	seen := map[domain.StepID]bool{}
	for i, st := range s.Steps {
		sp := fmt.Sprintf("steps[%d]", i)
		if !idPattern.MatchString(string(st.ID)) {
			add(sp+".id", "id "+idRule)
		}
		if seen[st.ID] {
			add(sp+".id", "duplicate step id %q", st.ID)
		}
		seen[st.ID] = true
		if st.Setup && len(st.Expect) > 0 {
			add(sp+".expect", "a setup step has no expected result")
		}
		for j, e := range st.Expect {
			ps = append(ps, validateExpectation(fmt.Sprintf("%s.expect[%d]", sp, j), e, origin)...)
		}
	}
	return ps
}

func validateExpectation(path string, e domain.Expectation, origin Origin) []Problem {
	var ps []Problem
	add := func(p, format string, args ...any) {
		ps = append(ps, Problem{Path: p, Message: fmt.Sprintf(format, args...)})
	}

	switch e.Source {
	case domain.SourceTicket, domain.SourceTestcase, domain.SourceDiff, domain.SourceBaseline:
	case domain.SourcePerson:
		if origin == FromAgent {
			add(path+".source", "an agent cannot use the source person")
		}
	case domain.SourceInvariant:
		add(path+".source", "invariants are fixed in Semar and cannot appear in a file")
	default:
		add(path+".source", "source must be one of ticket, testcase, person, diff, baseline")
	}
	if e.Source == domain.SourceTicket && e.Cite == "" {
		add(path+".cite", "cite is required for the source ticket")
	}

	c := e.Check
	cp := path + ".check"
	kinds := 0
	if c.Status != nil {
		kinds++
	}
	if c.JSONPath != "" {
		kinds++
	}
	if c.Header != "" {
		kinds++
	}
	if kinds != 1 {
		add(cp, "a check needs exactly one of status, jsonpath, header")
	}

	if c.JSONPath != "" {
		if _, err := jsonpath.Parse(c.JSONPath); err != nil {
			add(cp+".jsonpath", "jsonpath does not parse: %v", err)
		}
	}
	if c.JSONPath != "" || c.Header != "" {
		ops := 0
		for _, set := range []bool{
			c.Equals != nil, c.NotEquals != nil, c.Contains != nil, c.Exists != nil,
			c.Greater != nil, c.Less != nil, c.Count != nil, c.EqualsPath != "",
		} {
			if set {
				ops++
			}
		}
		if ops != 1 {
			add(cp, "a jsonpath or header check needs exactly one operator")
		}
	}

	for _, b := range []struct {
		name string
		val  *float64
	}{{"greater", c.Greater}, {"less", c.Less}} {
		if b.val != nil && (math.IsNaN(*b.val) || math.IsInf(*b.val, 0)) {
			add(cp, "%s must be a finite number", b.name)
		}
	}

	for _, v := range []struct {
		name string
		val  any
	}{{"equals", c.Equals}, {"notEquals", c.NotEquals}, {"contains", c.Contains}} {
		if v.val == nil {
			continue
		}
		if _, err := json.Marshal(v.val); err != nil {
			add(cp, "%s cannot be encoded as JSON: %v", v.name, err)
		}
	}
	return ps
}
