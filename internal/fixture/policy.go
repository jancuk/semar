package fixture

import (
	"fmt"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
)

// CheckPolicy adds the rules that need an environment (P1 to P4). services maps each service of the
// environment to its base URL. Each path is joined to the base URL as the engine joins it, so a
// pattern is matched against the path that is sent, a prefix of the base URL included. A variable of
// a path stands for the segment x, so a pattern such as /v1/orders/* matches it. The rules are checked again on each
// rendered request at run time, because a pattern can match a value that x did not.
//
// With a nil policy only P1 is checked. The messages say "this environment" because the policy does
// not carry the name of the environment; the caller that knows it may add it.
func CheckPolicy(f domain.Fixture, p *effects.Policy, services map[string]string) []Problem {
	var ps []Problem
	add := func(where, format string, args ...any) {
		ps = append(ps, Problem{Path: where, Message: fmt.Sprintf(format, args...)})
	}
	for _, phase := range []struct {
		name  string
		steps []domain.FixtureStep
	}{{domain.PhaseFind, f.Find}, {domain.PhaseCreate, f.Create}, {domain.PhaseCleanup, f.Cleanup}} {
		for i, st := range phase.steps {
			where := fmt.Sprintf("%s[%d]", phase.name, i)
			r := st.Request
			base, ok := services[r.Service]
			if !ok {
				add(where, "service %s is not in this environment", r.Service)
			}
			if p == nil || !ok {
				continue
			}
			u, err := api.JoinURL(base, variablePattern.ReplaceAllString(r.Path, "x"), nil)
			if err != nil {
				add(where, "the base url of service %s does not parse, or the path is not a path", r.Service)
				continue
			}
			req := effects.Request{Method: r.Method, URL: u}
			label := r.Method + " " + r.Path
			if phase.name == domain.PhaseFind && p.Classify(req) != effects.Read {
				add(where, "%s writes under the effects policy of this environment; a find step must read: list it under effects.reads if it only reads", label)
			}
			if p.Once(req) {
				add(where, "%s matches effects.once, and a fixture runs on every run", label)
			}
			if p.Denied(req) {
				add(where, "%s is on the deny list of this environment", label)
			}
		}
	}
	return ps
}
