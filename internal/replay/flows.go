package replay

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/verdict"
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

// useRun is a step with use, expanded: the flow and the items of it, in order.
type useRun struct {
	flow   domain.Flow
	digest string
	items  []useItem
}

// useItem is one step of a flow: a concrete device step with Step set to the step of the scenario, or
// a flow that the step uses.
type useItem struct {
	name  string
	step  domain.ScriptStep
	inner *useRun // set when the step uses a flow
}

// expansion is the script of a scenario with each use step read in. Flat holds the concrete device steps
// in order, with the step of the scenario as their Step, so that the steps that a script has to prepare,
// lock, and reset for are all seen, whether they are the script's own or a flow's, and however deep. Where
// names each one for a message.
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
	block := func(text string) {
		if !slices.Contains(blocked, text) {
			blocked = append(blocked, text)
		}
	}
	for _, ss := range s.Steps {
		if ss.Use == "" {
			exp.flat.Steps = append(exp.flat.Steps, ss)
			exp.where = append(exp.where, "step "+string(ss.Step))
			continue
		}
		if r.d.Flows == nil {
			block(reasonNoFlows)
			continue
		}
		u, ok := r.expandFlow(&exp, ss.Step, ss.Use, []string{ss.Use}, block)
		if ok {
			exp.uses[ss.Step] = u
		}
	}
	return exp, blocked
}

// expandFlow reads in one flow and the flows that it uses, and adds their device steps to exp.flat.
func (r *Runner) expandFlow(exp *expansion, step domain.StepID, id string, chain []string, block func(string)) (useRun, bool) {
	f, digest, err := r.d.Flows.Flow(id)
	if err != nil {
		text := fmt.Sprintf("the flow %s could not be used: %v", id, err)
		if errors.Is(err, flow.ErrNotFound) {
			text = fmt.Sprintf("the flow %s was not found: %s", id, flow.Path(id))
		}
		block(text)
		return useRun{}, false
	}
	u := useRun{flow: f, digest: digest}
	ok := true
	for i, fs := range flow.Steps(f) {
		name := subName(f, i)
		if fs.Use != "" {
			if slices.Contains(chain, fs.Use) || len(chain) >= flow.MaxDepth {
				block(fmt.Sprintf("the flow %s uses the flow %s, which makes the flows nest in a circle or deeper than %d", f.ID, fs.Use, flow.MaxDepth))
				ok = false
				continue
			}
			inner, innerOK := r.expandFlow(exp, step, fs.Use, append(slices.Clone(chain), fs.Use), block)
			if !innerOK {
				ok = false
				continue
			}
			u.items = append(u.items, useItem{name: name, step: fs, inner: &inner})
			continue
		}
		fs.Step = step
		u.items = append(u.items, useItem{name: name, step: fs})
		exp.flat.Steps = append(exp.flat.Steps, fs)
		exp.where = append(exp.where, fmt.Sprintf("step %s, %s, step %s", step, flowChain(chain), name))
	}
	return u, ok
}

func flowChain(ids []string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "flow " + id
	}
	return strings.Join(parts, " > ")
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

// useState is what the steps of one use step share while they run, however deep the flows nest.
type useState struct {
	done          []domain.Action
	captured      map[string]string
	shownCaptured map[string]string
	checks        []verdict.FlowCheck
}

// useStep runs the steps of one flow for the step of the scenario that uses it, and the steps of the flows
// that it uses. Every action keeps the step and the pass of the scenario step, and its reason names the
// flows and the step of the flow. The last action, or the one that failed, is the evidence of the step, and
// the ones before it are Prior. The flow sees only its params and its own captures: what it captures joins
// the variables of the scenario when it is done. The checks of the flow steps are kept as FlowChecks, and a
// false one stops the flow. It reports whether the steps of the scenario must stop.
func (r *Runner) useStep(ctx runCtx, u useRun, step domain.Step, ss domain.ScriptStep, vars, shown map[string]string) (stop bool) {
	run, note := ctx.run, ctx.note
	scope, err := flow.Bind(u.flow, ss.With, vars)
	if err != nil {
		note(step.ID, fmt.Sprintf("step %s could not be run: %v", step.ID, err))
		return true
	}
	showScope, err := flow.Bind(u.flow, ss.With, shown)
	if err != nil {
		note(step.ID, fmt.Sprintf("step %s could not be run: %v", step.ID, err))
		return true
	}
	run.noteFlow(u.flow, u.digest)
	st := &useState{captured: map[string]string{}, shownCaptured: map[string]string{}}
	if r.useItems(ctx, u, step, vars, shown, scope, showScope, []string{u.flow.ID}, st) {
		return true
	}
	maps.Copy(vars, st.captured)
	maps.Copy(shown, st.shownCaptured)
	return false
}

// useItems runs the items of one flow. scope and showScope are the variables of that flow, as the steps
// read them and as a report may show them.
func (r *Runner) useItems(ctx runCtx, u useRun, step domain.Step, vars, shown, scope, showScope map[string]string, chain []string, st *useState) (stop bool) {
	run, note := ctx.run, ctx.note
	for _, it := range u.items {
		if it.inner != nil {
			inner := *it.inner
			innerScope, err := flow.Bind(inner.flow, it.step.With, scope)
			if err == nil {
				var innerShow map[string]string
				if innerShow, err = flow.Bind(inner.flow, it.step.With, showScope); err == nil {
					run.noteFlow(inner.flow, inner.digest)
					had := maps.Clone(st.captured)
					if r.useItems(ctx, inner, step, vars, shown, innerScope, innerShow, append(slices.Clone(chain), inner.flow.ID), st) {
						return true
					}
					for k, v := range st.captured {
						if _, before := had[k]; !before {
							scope[k] = v
							showScope[k] = st.shownCaptured[k]
						}
					}
					continue
				}
			}
			note(step.ID, fmt.Sprintf("step %s could not be run: %v", step.ID, err))
			return true
		}
		sub := step
		sub.Action = flow.WhyChain(chain, it.name, step.Action)
		sub.Expect = it.step.Expect
		res, err := r.send(ctx.ctx, ctx.wr, ctx.mr, run, ctx.sc, sub, it.step, scope, ctx.pass, note)
		if !res.built {
			return true
		}
		if res.Action.ID != "" {
			have, showing := maps.Clone(vars), maps.Clone(shown)
			maps.Copy(have, st.captured)
			maps.Copy(have, res.Captured)
			maps.Copy(showing, st.shownCaptured)
			maps.Copy(showing, res.Action.Captures)
			// The checks of the flow step are not the step's own: evaluate sees only the step.
			ev := evaluate(step, res.Action, have, showing)
			ev.Prior = slices.Clone(st.done)
			ev.FlowChecks = slices.Clone(st.checks)
			run.steps[step.ID] = ev
			r.pause(ctx.ctx, ctx.sc, step, it.step, u.flow.ID, res.Action, verdict.StepEvidence{Checks: nil})
			st.done = append(st.done, res.Action)
		}
		if r.stopAfter(ctx, step, res, err) {
			return true
		}
		maps.Copy(scope, res.Captured)
		maps.Copy(showScope, res.Action.Captures)
		maps.Copy(st.captured, res.Captured)
		maps.Copy(st.shownCaptured, res.Action.Captures)
		maps.Copy(run.captured, res.Captured)
		if len(res.Missing) > 0 {
			note(step.ID, fmt.Sprintf("step %s: %s, capture %s found no value, so the later steps were not sent", step.ID, flowChain(chain), joinNames(res.Missing)))
			return true
		}
		// A check that is false stops the flow, and so does one that could not be evaluated, as in flow.RunWith.
		falsed := false
		for _, e := range it.step.Expect {
			got := flow.EvalExpect(e.Check, res.Action, scope, showScope)
			st.checks = append(st.checks, verdict.FlowCheck{Flow: u.flow.ID, Step: it.name, Result: got})
			falsed = falsed || got.State != domain.CheckTrue
		}
		if len(it.step.Expect) > 0 {
			ev := run.steps[step.ID]
			ev.FlowChecks = slices.Clone(st.checks)
			run.steps[step.ID] = ev
			if falsed {
				// Only the checks of this step: the ones before it held, or the flow had stopped.
				r.pause(ctx.ctx, ctx.sc, step, it.step, u.flow.ID, res.Action, verdict.StepEvidence{FlowChecks: st.checks[len(st.checks)-len(it.step.Expect):]})
			}
		}
		if falsed {
			return true
		}
	}
	return false
}
