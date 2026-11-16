package fixture

import (
	"fmt"
	"net/url"
	"slices"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
)

// CheckPolicy adds the rules that need an environment (P1 to P4). A variable of a path stands for the
// segment x, so a pattern such as /v1/orders/* matches it. The rules are checked again on each
// rendered request at run time, because a pattern can match a value that x did not.
//
// With a nil policy only P1 is checked. The messages say "this environment" because the policy does
// not carry the name of the environment; the caller that knows it may add it.
func CheckPolicy(f domain.Fixture, p *effects.Policy, envServices []string) []Problem {
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
			if !slices.Contains(envServices, r.Service) {
				add(where, "service %s is not in this environment", r.Service)
			}
			if p == nil {
				continue
			}
			req := effects.Request{Method: r.Method, URL: &url.URL{Path: variablePattern.ReplaceAllString(r.Path, "x")}}
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
