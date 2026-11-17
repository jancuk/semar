package scenario

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
)

type Origin int

const (
	FromFile  Origin = iota // written or edited by a person
	FromAgent               // submitted by an agent
)

type Problem struct{ Path, Message string }

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const idRule = "must match ^[A-Za-z0-9][A-Za-z0-9._-]*$"

const (
	maxGiven      = 8
	maxFixtureID  = 64
	maxWithValue  = 256
	fixtureIDRule = "must match ^[a-z][a-z0-9_-]*(\\.[a-z][a-z0-9_-]*)*$"
)

var (
	fixtureIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)*$`)
	namePattern      = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// ReservedHeads are the names that head a variable the run gives, so a given entry or a leg may not
// take one.
var ReservedHeads = []string{"run", "data", "secret", "date", "datetime", "local"}

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
	ps = append(ps, validateGiven(s.Given)...)

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
		if st.As != "" {
			if err := idcheck.Check("auth profile name", st.As); err != nil {
				add(sp+".as", "%v", err)
			}
		}
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
	switch e.By {
	case "", "model":
	default:
		add(path+".by", "by must be model or empty")
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
	if c.Element != "" {
		kinds++
	}
	if kinds != 1 {
		add(cp, "a check needs exactly one of status, jsonpath, header, element")
	}

	if c.JSONPath != "" {
		if _, err := jsonpath.Parse(c.JSONPath); err != nil {
			add(cp+".jsonpath", "jsonpath does not parse: %v", err)
		}
	}
	if c.Element != "" {
		ps = append(ps, elementCheckProblems(cp, c)...)
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
		ps = append(ps, operandProblems(cp+"."+v.name, v.val)...)
	}
	return ps
}

func validateGiven(given []domain.Given) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if len(given) > maxGiven {
		add("given", "at most %d given entries are allowed, there are %d", maxGiven, len(given))
	}
	aliases := map[string]bool{}
	for i, g := range given {
		p := fmt.Sprintf("given[%d]", i)
		switch id := string(g.Fixture); {
		case !fixtureIDPattern.MatchString(id):
			add(p+".fixture", "fixture %q "+fixtureIDRule, id)
		case len(id) > maxFixtureID:
			add(p+".fixture", "fixture %q is longer than %d bytes", id, maxFixtureID)
		default:
			if err := idcheck.Check("fixture ID", id); err != nil {
				add(p+".fixture", "%v", err)
			}
		}
		switch {
		case !namePattern.MatchString(g.As):
			add(p+".as", "as %q must match ^[a-z][a-z0-9_]*$", g.As)
		case slices.Contains(ReservedHeads, g.As):
			add(p+".as", "the name %s is reserved", g.As)
		case aliases[g.As]:
			add(p+".as", "%s is used twice", g.As)
		}
		aliases[g.As] = true

		for _, k := range slices.Sorted(maps.Keys(g.With)) {
			wp := fmt.Sprintf("%s.with.%s", p, k)
			if !namePattern.MatchString(k) || k == "run" || k == "data" || k == "secret" {
				add(p+".with", "key %q must match ^[a-z][a-z0-9_]*$ and not be run, data, or secret", k)
			}
			v := g.With[k]
			switch {
			case len(v) > maxWithValue:
				add(wp, "the value is longer than %d bytes", maxWithValue)
			case !utf8.ValidString(v):
				add(wp, "the value is not valid UTF-8")
			case strings.ContainsFunc(v, unicode.IsControl):
				add(wp, "the value holds a control character")
			}
		}
		switch g.Lease {
		case "", domain.LeaseExclusive:
		default:
			add(p+".lease", "lease must be exclusive or empty")
		}
	}
	return ps
}

// operandProblems refuses a secret, a fixture data value, and a malformed variable in a string
// operand. Whether a name is known depends on the script, so script validation checks that.
func operandProblems(path string, v any) []Problem {
	text, ok := v.(string)
	if !ok {
		return nil
	}
	c := domain.Check{Equals: text}
	var ps []Problem
	if domain.OperandMalformed(c) != "" {
		ps = append(ps, Problem{Path: path, Message: "malformed variable in " + strconv.Quote(text)})
	}
	for _, name := range domain.OperandVariables(c) {
		switch head, _, _ := strings.Cut(name, "."); head {
		case "secret":
			ps = append(ps, Problem{Path: path, Message: fmt.Sprintf("{{%s}}: a check cannot use a secret", name)})
		case "data":
			ps = append(ps, Problem{Path: path, Message: fmt.Sprintf("{{%s}}: a check cannot use a data value: put it in a with value and use the fixture output", name)})
		}
	}
	return ps
}

// elementCheckProblems holds the rules of a check on an element: one operator, and one that reads a
// page. The element is named by the ID that the screens file gives it.
func elementCheckProblems(path string, c domain.Check) []Problem {
	var ps []Problem
	add := func(p, format string, args ...any) {
		ps = append(ps, Problem{Path: p, Message: fmt.Sprintf(format, args...)})
	}
	if !elementNamePattern.MatchString(c.Element) {
		add(path+".element", "element %q must start with a letter or a digit and hold letters, digits, - and _", c.Element)
	}
	var ops []string
	for _, o := range []struct {
		name string
		set  bool
	}{
		{"equals", c.Equals != nil}, {"notEquals", c.NotEquals != nil}, {"contains", c.Contains != nil},
		{"exists", c.Exists != nil}, {"count", c.Count != nil},
		{"greater", c.Greater != nil}, {"less", c.Less != nil}, {"equalsPath", c.EqualsPath != ""},
	} {
		if o.set {
			ops = append(ops, o.name)
		}
	}
	switch {
	case len(ops) != 1:
		add(path, "an element check needs exactly one operator: equals, notEquals, contains, exists, or count")
	case ops[0] == "greater" || ops[0] == "less" || ops[0] == "equalsPath":
		add(path, "the operator %s is not for an element: use equals, notEquals, contains, exists, or count", ops[0])
	}
	if c.Count != nil && *c.Count < 0 {
		add(path+".count", "count must not be negative")
	}
	return ps
}

var elementNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
