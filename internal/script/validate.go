package script

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/datevar"
	"github.com/jancuk/semar/internal/domain"
)

type Problem struct {
	Step    domain.StepID
	Message string
}

var knownMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}

var (
	// clientHeaders frame the message or the connection, so the HTTP client sets them.
	clientHeaders = []string{"Host", "Content-Length", "Transfer-Encoding", "Connection", "Upgrade", "TE", "Trailer", "Keep-Alive", "Proxy-Connection"}
	// engineHeaders are set on every request by the API engine, which drops a script's value.
	engineHeaders = []string{"traceparent", "baggage", "User-Agent", "X-Semar-Run"}
)

// Validate returns every reason the script cannot stand for the scenario.
// An empty hash never matches, even when both sides are empty.
func Validate(sc domain.Scenario, s domain.Script) []Problem {
	return ValidateWith(sc, s, nil)
}

// ValidateWith is Validate for a scenario with given entries. outputs maps each alias to the
// output names of its fixture. With nil outputs, any <as>.<name> of a declared alias is accepted.
// run.id is always known. An alias counts as an earlier capture for every step.
func ValidateWith(sc domain.Scenario, s domain.Script, outputs map[string][]string) []Problem {
	return validate(sc, s, outputs, false)
}

// ValidateShapeWith is ValidateWith for a script found by its shape hash: it requires
// s.ShapeHash == domain.ShapeHash(sc) in place of the expectation-hash check.
func ValidateShapeWith(sc domain.Scenario, s domain.Script, outputs map[string][]string) []Problem {
	return validate(sc, s, outputs, true)
}

func validate(sc domain.Scenario, s domain.Script, outputs map[string][]string, byShape bool) []Problem {
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
	if byShape {
		switch want := domain.ShapeHash(sc); {
		case want == "":
			add("", "the scenario has no shape hash")
		case s.ShapeHash == "":
			add("", "the script has no shape hash")
		case s.ShapeHash != want:
			add("", "the script was written for other steps")
		}
	} else {
		want := domain.ExpectHash(sc)
		switch {
		case want == "":
			add("", "the scenario has no expectation hash")
		case s.ExpectHash == "":
			add("", "the script has no expectation hash")
		case s.ExpectHash != want:
			add("", "the script was written for other expected results")
		}
	}

	index := make(map[domain.StepID]int, len(sc.Steps))
	for i, st := range sc.Steps {
		index[st.ID] = i
	}

	bound := map[domain.StepID]bool{}
	captured := map[string]bool{}
	unknown := variableCheck(sc, captured, outputs)
	lastIndex, outOfOrder := -1, false
	started := map[string]bool{} // services whose app a mobile step has started
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

		switch {
		case ss.Request != nil && ss.Web != nil && ss.Mobile == nil:
			add(ss.Step, "a step has a request or a web action, and not both")
		case ss.Mobile != nil && (ss.Request != nil || ss.Web != nil):
			add(ss.Step, "a step has a request, a web action, or a mobile action, and not more than one")
		case ss.Request == nil && ss.Web == nil && ss.Mobile == nil && len(ss.Captures) > 0:
			add(ss.Step, "a capture needs a request or a web action")
		}
		if sc.Surface == domain.SurfaceAndroid && ss.Web != nil {
			add(ss.Step, "an android scenario has no web action: use a mobile action, or a request to set up")
		}
		if ss.Mobile != nil {
			for _, m := range ss.Mobile.Problems() {
				add(ss.Step, "mobile: %s", m)
			}
			for _, name := range ss.Mobile.Variables() {
				if msg := unknown(name); msg != "" {
					add(ss.Step, "mobile: %s", msg)
				}
			}
			if sc.Surface != domain.SurfaceAndroid {
				add(ss.Step, "a mobile action needs a scenario with the surface android")
			}
			if known && sc.Steps[i].As != "" && sc.Steps[i].As != sc.AuthProfile {
				add(ss.Step, "mobile: a device has one signed-in account, so as cannot be %s", sc.Steps[i].As)
			}
			if svc := ss.Mobile.Service; !started[svc] {
				started[svc] = true
				if do := ss.Mobile.Do; do != domain.MobileLaunch && do != domain.MobileOpenLink {
					add(ss.Step, "mobile: the first mobile step of a service must be launch or open, and this one is %s", do)
				}
			}
		}
		if ss.Web != nil {
			for _, m := range ss.Web.Problems() {
				add(ss.Step, "web: %s", m)
			}
			if m := ss.Web.UploadProblem(); m != "" {
				add(ss.Step, "web: %s", m)
			}
			for _, name := range ss.Web.Variables() {
				if msg := unknown(name); msg != "" {
					add(ss.Step, "web: %s", msg)
				}
			}
		}
		if ss.Request != nil {
			var hint *domain.RequestHint
			if known {
				hint = sc.Steps[i].Hint
			}
			ps = append(ps, validateRequestWith(ss, hint, len(captured) > 0, unknown)...)
		}

		seen := map[string]bool{}
		for _, c := range ss.Captures {
			switch {
			case c.Name == "":
				add(ss.Step, "a capture has no name")
			case !namePattern.MatchString(c.Name):
				add(ss.Step, "capture name %s is not a valid variable name", c.Name)
			}
			switch {
			case c.JSONPath != "" && c.Element != "":
				add(ss.Step, "capture %s takes a jsonpath or an element, and not both", c.Name)
			case c.Element != "":
				if ss.Web == nil && ss.Mobile == nil {
					add(ss.Step, "capture %s names an element, so the step needs %s", c.Name, needsScreenAction(sc))
				}
			case c.JSONPath == "":
				add(ss.Step, "capture %s needs a jsonpath or an element", c.Name)
			default:
				if _, err := jsonpath.Parse(c.JSONPath); err != nil {
					add(ss.Step, "capture %s: jsonpath does not parse: %v", c.Name, err)
				}
				if ss.Web != nil {
					add(ss.Step, "capture %s names a jsonpath, and a web step has no response to read: use an element", c.Name)
				}
				if ss.Mobile != nil {
					add(ss.Step, "capture %s names a jsonpath, and a mobile step has no response to read: use an element", c.Name)
				}
			}
			if c.Name != "" && (captured[c.Name] || seen[c.Name]) {
				add(ss.Step, "capture %s is defined more than once", c.Name)
			}
			seen[c.Name] = true
		}
		for name := range seen {
			captured[name] = true
		}
		if known {
			for j, e := range sc.Steps[i].Expect {
				if e.Check.Element != "" && ss.Web == nil && ss.Mobile == nil {
					add(ss.Step, "expect[%d] checks element %s, so the step needs %s", j, e.Check.Element, needsScreenAction(sc))
				}
				if e.Check.Page != "" && ss.Web == nil {
					if ss.Mobile != nil {
						add(ss.Step, "expect[%d] checks the page %s, and a mobile step has no page: check an element", j, e.Check.Page)
					} else {
						add(ss.Step, "expect[%d] checks the page %s, so the step needs a web action", j, e.Check.Page)
					}
				}
				if ss.Web != nil && (e.Check.Status != nil || e.Check.JSONPath != "" || e.Check.Header != "") {
					add(ss.Step, "expect[%d] reads a response, and a web step has none: check an element", j)
				}
				if ss.Mobile != nil && (e.Check.Status != nil || e.Check.JSONPath != "" || e.Check.Header != "") {
					add(ss.Step, "expect[%d] reads a response, and a mobile step has none: check an element", j)
				}
				for _, o := range []struct {
					op string
					v  any
				}{{"equals", e.Check.Equals}, {"notEquals", e.Check.NotEquals}, {"contains", e.Check.Contains}} {
					text, isText := o.v.(string)
					if !isText {
						continue
					}
					for _, name := range domain.OperandVariables(domain.Check{Equals: text}) {
						if msg := unknown(name); msg != "" {
							msg = strings.Replace(msg, "a script cannot use a secret", "a check cannot use a secret", 1)
							add(ss.Step, "expect[%d].check.%s: %s", j, o.op, msg)
						}
					}
				}
			}
		}
	}
	if outOfOrder {
		add("", "steps are not in the scenario's order")
	}

	for _, st := range sc.Steps {
		if len(st.Expect) > 0 && !hasAction(s, st.ID) {
			add(st.ID, "step %s has an expected result and no request or web action", st.ID)
		}
	}
	return ps
}

func hasAction(s domain.Script, id domain.StepID) bool {
	return slices.ContainsFunc(s.Steps, func(ss domain.ScriptStep) bool {
		return ss.Step == id && (ss.Request != nil || ss.Web != nil || ss.Mobile != nil)
	})
}

// needsScreenAction names the action that a step needs to read an element, for a scenario.
func needsScreenAction(sc domain.Scenario) string {
	if sc.Surface == domain.SurfaceAndroid {
		return "a mobile action"
	}
	return "a web action"
}

// RequestProblems holds the request rules of Validate that need no scenario step: a service, a
// known method, a path, header names and values, and well-formed variables. known says whether a
// variable may be used. Each problem is plain text, and one that has a place starts with it.
func RequestProblems(r domain.APIRequest, known func(name string) bool) []string {
	out := requestProblems(r)
	for _, t := range templates(r) {
		for _, m := range variablePattern.FindAllStringSubmatch(t.text, -1) {
			if !known(m[1]) && !datevar.Is(m[1]) {
				out = append(out, fmt.Sprintf("%s: {{%s}} is not a known variable", t.where, m[1]))
			}
		}
	}
	return out
}

// requestProblems is RequestProblems without the check that a variable is known.
func requestProblems(r domain.APIRequest) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

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
	} else if err := checkPath(r.Path); err != nil {
		add("%v", err)
	}
	if percentBeforeVariable.MatchString(r.Path) {
		add("in path %s a variable directly follows a percent sign and could finish an escape", r.Path)
	}
	for _, name := range slices.Sorted(maps.Keys(r.Headers)) {
		for _, msg := range headerProblems(name, r.Headers[name]) {
			add("%s", msg)
		}
	}
	for _, m := range templateProblems(r) {
		add("%s", m)
	}
	return out
}

// variableCheck gives the problem of one variable name, or "" when a step may use it. captured is
// read when the check runs, so it sees the captures of the steps before the one in hand.
func variableCheck(sc domain.Scenario, captured map[string]bool, outputs map[string][]string) func(name string) string {
	fixtureOf := make(map[string]domain.FixtureID, len(sc.Given))
	for _, g := range sc.Given {
		if _, dup := fixtureOf[g.As]; !dup {
			fixtureOf[g.As] = g.Fixture
		}
	}
	return func(name string) string {
		if name == "run.id" || captured[name] || datevar.Is(name) {
			return ""
		}
		head, rest, dotted := strings.Cut(name, ".")
		if !dotted {
			return fmt.Sprintf("%s is used before any step captures it", name)
		}
		switch head {
		case "secret":
			return fmt.Sprintf("malformed variable {{%s}}: a script cannot use a secret", name)
		case "data":
			return fmt.Sprintf("%s cannot be used in a script: put a data value in a fixture parameter or in a with value", name)
		}
		fx, ok := fixtureOf[head]
		if !ok {
			return fmt.Sprintf("%s is not a capture, run.id, or an output of a given entry: no given entry has as %s", name, head)
		}
		if outputs == nil {
			if namePattern.MatchString(rest) {
				return ""
			}
			return fmt.Sprintf("%s is not an output of fixture %s", name, fx)
		}
		outs := outputs[head]
		if slices.Contains(outs, rest) {
			return ""
		}
		if len(outs) == 0 {
			return fmt.Sprintf("%s is not an output of fixture %s; it has no known outputs", name, fx)
		}
		return fmt.Sprintf("%s is not an output of fixture %s; its outputs are %s", name, fx, strings.Join(outs, ", "))
	}
}

func validateRequest(ss domain.ScriptStep, hint *domain.RequestHint, hasEarlierCapture bool, captured map[string]bool) []Problem {
	return validateRequestWith(ss, hint, hasEarlierCapture, variableCheck(domain.Scenario{}, captured, nil))
}

func validateRequestWith(ss domain.ScriptStep, hint *domain.RequestHint, hasEarlierCapture bool, unknown func(name string) string) []Problem {
	var ps []Problem
	add := func(format string, args ...any) {
		ps = append(ps, Problem{Step: ss.Step, Message: fmt.Sprintf(format, args...)})
	}
	r := ss.Request

	for _, m := range requestProblems(*r) {
		add("%s", m)
	}

	names := variables(*r)
	sort.Strings(names)
	for i, n := range names {
		if i > 0 && names[i-1] == n {
			continue
		}
		if msg := unknown(n); msg != "" {
			add("%s", msg)
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

// headerProblems holds the rules for a request header of a script, written by hand or drafted.
// A variable's value is checked again when the request is rendered.
func headerProblems(name, value string) []string {
	var ps []string
	named := func(list []string) bool {
		return slices.ContainsFunc(list, func(h string) bool { return strings.EqualFold(h, name) })
	}
	switch {
	case !isToken(name):
		ps = append(ps, fmt.Sprintf("header name %q is not a valid HTTP header name", name))
	case named(clientHeaders):
		ps = append(ps, fmt.Sprintf("header %s is set by the HTTP client and cannot be in a script", name))
	case named(engineHeaders):
		ps = append(ps, fmt.Sprintf("header %s is set by Semar on every request and cannot be in a script", name))
	}
	if strings.ContainsFunc(value, illegalInHeaderValue) {
		ps = append(ps, fmt.Sprintf("the value of header %q holds a control character", name))
	}
	return ps
}

// illegalInHeaderValue is a control character other than the horizontal tab, which RFC 9110
// allows inside a field value.
func illegalInHeaderValue(r rune) bool { return r != '\t' && unicode.IsControl(r) }

// isToken reports whether s is a token of RFC 9110: one or more tchar.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0) {
			return false
		}
	}
	return true
}
