package edge

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/script"
)

// Rule is the author that a script of an edge case names, where a model would be named.
var Rule = domain.ProviderRef{Instance: "semar", Model: "edge-rules", Harness: "rule"}

// IDOf is the ID of the n-th edge case of a ticket, such as PROJ-12-E01.
func IDOf(ticket string, n int) domain.ScenarioID {
	return domain.ScenarioID(fmt.Sprintf("%s-E%02d", ticket, n))
}

// Number is n when id is an edge case ID of the ticket, such as PROJ-12-E07, and 0 otherwise.
func Number(ticket string, id domain.ScenarioID) int {
	rest, ok := strings.CutPrefix(string(id), ticket+"-E")
	if !ok || rest == "" {
		return 0
	}
	n := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// Build makes the scenario and the script of one edge case from the scenario and the script of its base:
// the steps before the changed one that give it a value, and the changed step with the case's request and
// one expected result. A scenario that holds a fixture is refused for a case that changes the account,
// because the fixture would run as the other account.
func Build(base domain.Scenario, bs domain.Script, step domain.StepID, c Case, id domain.ScenarioID, intent string) (domain.Scenario, domain.Script, error) {
	at := slices.IndexFunc(base.Steps, func(s domain.Step) bool { return s.ID == step })
	sat := slices.IndexFunc(bs.Steps, func(s domain.ScriptStep) bool { return s.Step == step })
	if at < 0 || sat < 0 || bs.Steps[sat].Request == nil {
		return domain.Scenario{}, domain.Script{}, fmt.Errorf("scenario %s has no API step %s", base.ID, step)
	}
	profile := base.AuthProfile
	switch c.Kind {
	case NoSession:
		profile = ""
	case OtherAccount:
		profile = c.On
	}
	changesAccount := profile != base.AuthProfile

	req := cloneRequest(c.Request)
	needed := variablesOf(req)
	keep := map[domain.StepID]bool{}
	for i := at - 1; i >= 0; i-- {
		ss, ok := scriptStepOf(bs, base.Steps[i].ID)
		if !ok || ss.Request == nil && ss.Web == nil {
			continue
		}
		if !slices.ContainsFunc(ss.Captures, func(cp domain.Capture) bool { return slices.Contains(needed, cp.Name) }) {
			continue
		}
		keep[base.Steps[i].ID] = true
		if ss.Request != nil {
			needed = append(needed, variablesOf(*ss.Request)...)
		}
	}
	var given []domain.Given
	for _, g := range base.Given {
		if slices.ContainsFunc(needed, func(n string) bool { head, _, _ := strings.Cut(n, "."); return head == g.As }) {
			given = append(given, g)
		}
	}
	if changesAccount && len(given) > 0 {
		return domain.Scenario{}, domain.Script{}, fmt.Errorf("%s", ReasonFixture)
	}

	sc := domain.Scenario{
		APIVersion: base.APIVersion, Kind: base.Kind, ID: id, Ticket: base.Ticket, Surface: domain.SurfaceAPI,
		Title:  "Edge case: " + sentence(base.Steps[at], c),
		Intent: intent, AuthProfile: profile, Given: given,
		Edge: &domain.Edge{Kind: string(c.Kind), Base: base.ID, On: c.On},
	}
	if sc.APIVersion == "" {
		sc.APIVersion = "semar/v1"
	}
	if sc.Kind == "" {
		sc.Kind = "Scenario"
	}
	out := domain.Script{APIVersion: bs.APIVersion, Kind: bs.Kind, Scenario: id, AuthoredBy: Rule}
	for i := 0; i < at; i++ {
		if !keep[base.Steps[i].ID] {
			continue
		}
		st := base.Steps[i]
		st.Setup, st.Expect = true, nil
		if changesAccount {
			st.As = base.AuthProfile
		}
		sc.Steps = append(sc.Steps, st)
		ss, _ := scriptStepOf(bs, st.ID)
		out.Steps = append(out.Steps, ss)
	}
	target := base.Steps[at]
	target.Action = "Send " + sentence(target, c)
	target.As = ""
	status := c.Status
	target.Expect = []domain.Expectation{{Check: domain.Check{Status: &status}, Source: domain.SourceRule, Cite: c.Cite}}
	sc.Steps = append(sc.Steps, target)
	ts := bs.Steps[sat]
	ts.Request = &req
	out.Steps = append(out.Steps, ts)
	out.ExpectHash = domain.ExpectHash(sc)
	out.ShapeHash = domain.ShapeHash(sc)
	return sc, out, nil
}

// sentence is the method and route of the step, with what the case changes.
func sentence(st domain.Step, c Case) string {
	route := ""
	if st.Hint != nil {
		route = st.Hint.Method + " " + st.Hint.Path
	}
	return strings.TrimSpace(route + " " + c.Title)
}

func scriptStepOf(s domain.Script, id domain.StepID) (domain.ScriptStep, bool) {
	for _, ss := range s.Steps {
		if ss.Step == id {
			return ss, true
		}
	}
	return domain.ScriptStep{}, false
}

// variablesOf lists the variables that a request names.
func variablesOf(r domain.APIRequest) []string {
	var names []string
	_ = script.RequestProblems(r, func(n string) bool { names = append(names, n); return true })
	return names
}

// KeyOf names what an edge case changes, so that a case that a person dropped is not drafted again for
// the same ticket, operation, kind and target. It is "" for a scenario that is no edge case.
func KeyOf(sc domain.Scenario) string {
	if sc.Edge == nil || len(sc.Steps) == 0 {
		return ""
	}
	return Key(sc.Ticket, sc.Steps[len(sc.Steps)-1].Hint, Kind(sc.Edge.Kind), sc.Edge.On)
}

// Key is the key of the case that changes target on the operation of hint.
func Key(ticket string, hint *domain.RequestHint, kind Kind, on string) string {
	route := ""
	if hint != nil {
		route = strings.ToUpper(hint.Method) + " " + hint.Path
	}
	return strings.Join([]string{ticket, route, string(kind), on}, "|")
}
