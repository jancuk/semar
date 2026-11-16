package replay

import (
	"context"
	"errors"
	"maps"
	"regexp"
	"slices"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/verdict"
)

// Single is one scenario that a caller runs by itself, such as a leg of a journey.
type Single struct {
	Scenario domain.Scenario
	Script   domain.Script
	// Vars are preset variables named <name>.<output>, such as <leg>.<name>. Only these names
	// can be used in the script. A value is raw: it is redacted when an action is recorded and
	// is never stored by RunOne. {{run.id}} is always the run of the Runner.
	Vars map[string]string
	// Pass is the pass of the steps that are not setup steps. The default is the scenario pass.
	Pass domain.Pass
}

// Replayed is what one scenario left behind. The caller decides the outcome with verdict.Compute,
// from the builds that it read.
type Replayed struct {
	Steps     map[domain.StepID]verdict.StepEvidence
	Captured  map[string]string // every capture of every step; raw, never stored
	Blocked   []string
	Notes     []domain.Reason
	Confirmed func(domain.Expectation) bool
	Last      *api.Call // the call of the last step sent, for a wait
}

// reservedHeads are not preset variables: run.id is the Runner's, and the other two are refused in a script.
var reservedHeads = []string{"run", "secret", "data"}

var presetName = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_]*)\.([A-Za-z0-9_]+)$`)

// RunOne sends the steps of one scenario as Run does, and nothing more: it does not probe the
// builds and does not resolve fixtures, so a scenario with given entries is blocked, and the
// caller passes their outputs in Vars. The error is the context's, joined with an engine error
// that stopped the scenario. Neither Vars nor the script is changed.
func (r *Runner) RunOne(ctx context.Context, in Single) (Replayed, error) {
	run := newReplayed()
	run.confirmed = r.confirmation(in.Scenario, &run)
	out := func(err error) (Replayed, error) {
		if run.notRun != "" {
			run.notes = append(run.notes, domain.Reason{Rule: domain.OutcomeInconclusive, Text: run.notRun})
		}
		return Replayed{
			Steps: run.steps, Captured: run.captured, Blocked: run.blocked, Notes: run.notes,
			Confirmed: run.confirmed, Last: run.last,
		}, errors.Join(err, ctx.Err())
	}

	if len(in.Scenario.Given) > 0 {
		run.blocked = []string{"the scenario has given entries, and RunOne does not resolve given entries: pass their outputs in Vars"}
		return out(nil)
	}
	if blocked := r.unknownAccounts(in.Scenario); len(blocked) > 0 {
		run.blocked = blocked
		return out(nil)
	}
	vars, outputs := presets(in.Vars)
	if blocked := scriptBlocked(script.ValidateWith(withHeads(in), withHash(in), outputs)); len(blocked) > 0 {
		run.blocked = blocked
		return out(nil)
	}
	vars["run.id"] = string(r.d.Run)
	pass := in.Pass
	if pass == "" {
		pass = domain.PassScenario
	}
	return out(r.steps(ctx, in.Scenario, in.Script, &run, vars, pass))
}

// presets keeps the variables that a script can name, as a copy, and the output names of each head.
func presets(in map[string]string) (vars map[string]string, outputs map[string][]string) {
	vars, outputs = map[string]string{}, map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(in)) {
		m := presetName.FindStringSubmatch(name)
		if m == nil || slices.Contains(reservedHeads, m[1]) {
			continue
		}
		vars[name] = in[name]
		outputs[m[1]] = append(outputs[m[1]], m[2])
	}
	return vars, outputs
}

// withHeads is the scenario as script validation reads it: each head of a preset variable stands
// for a given entry. The hash is covered by withHash.
func withHeads(in Single) domain.Scenario {
	_, outputs := presets(in.Vars)
	sc := in.Scenario
	for _, head := range slices.Sorted(maps.Keys(outputs)) {
		sc.Given = append(sc.Given, domain.Given{Fixture: "preset", As: head})
	}
	return sc
}

// withHash re-binds the script to the scenario that withHeads made, but only when it was bound to
// the real one. A script that was written for other expected results keeps its hash and is refused.
func withHash(in Single) domain.Script {
	s := in.Script
	if s.ExpectHash != "" && s.ExpectHash == domain.ExpectHash(in.Scenario) {
		s.ExpectHash = domain.ExpectHash(withHeads(in))
	}
	return s
}

// Resend sends call c again as attempt number attempt, and evaluates the checks and the invariants
// of step on the new action. It returns the captured values of that call. c is not changed.
// The evidence has no action when the engine failed before one was recorded.
func (r *Runner) Resend(ctx context.Context, c api.Call, attempt int, step domain.Step) (verdict.StepEvidence, map[string]string, error) {
	c.Use.Attempt = attempt
	res, err := r.d.Engine.Do(ctx, c)
	var ev verdict.StepEvidence
	if res.Action.ID != "" {
		ev = evaluate(step, res.Action, nil, nil)
	}
	return ev, res.Captured, err
}
