package replay

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/script"
)

// FlowSource gives the flow of an ID and the digest of its file. flow.Loader makes one. The error says
// the flow is not there with flow.ErrNotFound.
type FlowSource interface {
	Flow(id string) (domain.Flow, string, error)
}

const reasonNoFlows = "the script uses a flow, and this run has no flows"

// flowResolver is the script check's way to the flows, or nil when this run has none, which makes the
// check accept a use step without looking into it. replay then blocks such a script itself.
func (r *Runner) flowResolver() script.FlowResolver {
	if r.d.Flows == nil {
		return nil
	}
	return func(id string) (domain.Flow, error) {
		f, _, err := r.d.Flows.Flow(id)
		return f, err
	}
}

// useRun is a step with use, expanded: the flow and the concrete steps of it.
type useRun struct {
	flow   domain.Flow
	digest string
	steps  []domain.ScriptStep // the steps of the flow, each with Step set to the step of the scenario
}

// expansion is the script of a scenario with each use step read in. Flat holds the concrete device steps
// in order, with the step of the scenario as their Step, so that the steps that a script has to prepare,
// lock, and reset for are all seen, whether they are the script's own or a flow's. Where names each one
// for a message.
type expansion struct {
	uses  map[domain.StepID]useRun
	flat  domain.Script
	where []string
}

// expand reads in the flows of the use steps of the script. It reports a flow that is not there as a
// reason that blocks the scenario. A script with no use step expands to itself.
func (r *Runner) expand(s domain.Script) (exp expansion, blocked []string) {
	exp.uses = map[domain.StepID]useRun{}
	exp.flat = s
	exp.flat.Steps = nil
	hasUse := slices.ContainsFunc(s.Steps, func(ss domain.ScriptStep) bool { return ss.Use != "" })
	if !hasUse {
		exp.flat = s
		return exp, nil
	}
	for _, ss := range s.Steps {
		if ss.Use == "" {
			exp.flat.Steps = append(exp.flat.Steps, ss)
			exp.where = append(exp.where, "step "+string(ss.Step))
			continue
		}
		if r.d.Flows == nil {
			if !slices.Contains(blocked, reasonNoFlows) {
				blocked = append(blocked, reasonNoFlows)
			}
			continue
		}
		f, digest, err := r.d.Flows.Flow(ss.Use)
		if err != nil {
			text := fmt.Sprintf("the flow %s could not be used: %v", ss.Use, err)
			if errors.Is(err, flow.ErrNotFound) {
				text = fmt.Sprintf("the flow %s was not found: %s", ss.Use, flow.Path(ss.Use))
			}
			if !slices.Contains(blocked, text) {
				blocked = append(blocked, text)
			}
			continue
		}
		u := useRun{flow: f, digest: digest}
		for _, fs := range flow.Steps(f) {
			fs.Step = ss.Step
			u.steps = append(u.steps, fs)
			exp.flat.Steps = append(exp.flat.Steps, fs)
			exp.where = append(exp.where, fmt.Sprintf("step %s, flow %s, step %s", ss.Step, f.ID, subName(f, len(u.steps)-1)))
		}
		exp.uses[ss.Step] = u
	}
	return exp, blocked
}

func subName(f domain.Flow, i int) string {
	if i < len(f.Steps) {
		return string(f.Steps[i].Step)
	}
	return ""
}

// noteFlow records that a flow ran, once, with its digest.
func (run *replayed) noteFlow(f domain.Flow, digest string) {
	ref := domain.FlowRef{ID: f.ID, Digest: digest}
	if !slices.Contains(run.flows, ref) {
		run.flows = append(run.flows, ref)
	}
}

// useStep runs the steps of one flow for the step of the scenario that uses it. Every action keeps the
// step and the pass of the scenario step, and its reason names the flow and the step of the flow. The
// last action, or the one that failed, is the evidence of the step, and the ones before it are Prior. The
// flow sees only its params and its own captures: what it captures joins the variables of the scenario
// when it is done. It reports whether the steps of the scenario must stop.
func (r *Runner) useStep(ctx runCtx, u useRun, step domain.Step, ss domain.ScriptStep, vars, shown map[string]string) (stop bool) {
	run, note := ctx.run, ctx.note
	scope, err := flow.Bind(u.flow, ss.With, vars)
	if err != nil {
		note(step.ID, fmt.Sprintf("step %s could not be run: %v", step.ID, err))
		return true
	}
	run.noteFlow(u.flow, u.digest)
	var done []domain.Action
	captured := map[string]string{}
	shownCaptured := map[string]string{}
	for i, fs := range u.steps {
		sub := step
		sub.Action = flow.Why(u.flow.ID, subName(u.flow, i), step.Action)
		res, err := r.send(ctx.ctx, ctx.wr, ctx.mr, run, ctx.sc, sub, fs, scope, ctx.pass, note)
		if !res.built {
			return true
		}
		if res.Action.ID != "" {
			have, showing := maps.Clone(vars), maps.Clone(shown)
			maps.Copy(have, captured)
			maps.Copy(have, res.Captured)
			maps.Copy(showing, shownCaptured)
			maps.Copy(showing, res.Action.Captures)
			ev := evaluate(step, res.Action, have, showing)
			ev.Prior = slices.Clone(done)
			run.steps[step.ID] = ev
			done = append(done, res.Action)
		}
		if r.stopAfter(ctx, step, res, err) {
			return true
		}
		maps.Copy(scope, res.Captured)
		maps.Copy(captured, res.Captured)
		maps.Copy(shownCaptured, res.Action.Captures)
		maps.Copy(run.captured, res.Captured)
		if len(res.Missing) > 0 {
			note(step.ID, fmt.Sprintf("step %s: flow %s, capture %s found no value, so the later steps were not sent", step.ID, u.flow.ID, joinNames(res.Missing)))
			return true
		}
	}
	maps.Copy(vars, captured)
	maps.Copy(shown, shownCaptured)
	return false
}
