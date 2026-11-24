package flow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/script"
)

// The statuses of a run of a flow. A flow has no expected result, so no run of it passes: the best that
// it does is complete.
const (
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusRefused   = "refused"
)

// maxRunDepth stops a run that the check of the flows did not stop.
const maxRunDepth = MaxDepth + 1

// Doer is the engine of the app on a device.
type Doer interface {
	Do(ctx context.Context, c mobile.Call) (mobile.Result, error)
}

// scenarioStarter is what an engine has to make the next action start with the app reset.
type scenarioStarter interface{ NewScenario() }

// ScreenSource gives the screens file of an app.
type ScreenSource interface {
	Load(app string) (domain.Screens, error)
}

// Options changes how Run records its steps. The zero value is right for a flow that runs by itself.
type Options struct {
	// Use gives the use case of a step of the flow, by the name of the step. The default is the pass
	// scenario, with the scenario flow-<id> and the step <id>.<name>. A recording passes its own.
	Use func(step string) domain.UseCase
	// By is who acts. The default is replay.
	By domain.Actor
	// KeepState does not reset the app first: the flow runs on what the device shows.
	KeepState bool
	// Flows gives a flow that a step uses, with the digest of its file. A flow with a use step cannot run
	// without it.
	Flows func(id string) (domain.Flow, string, error)
	// Before is called before each step that sends an action, with the name of the step as
	// "<flow>.<step>". An error stops the run at that boundary: the status is refused, and the error text
	// is the reason. A tool server uses it so that a person can pause a running flow between two steps.
	// The run is not an error, so the steps that already ran stay in the result.
	Before func(ctx context.Context, step string) error
}

// StepRecord is what one step of a flow did.
type StepRecord struct {
	Name   string
	Action domain.Action
}

// RunResult is what a run of a flow left behind.
type RunResult struct {
	// Status is completed, failed, or refused. It is never passed.
	Status string
	// Flow is the version that ran, when the caller knows the digest.
	Flow  domain.FlowRef
	Steps []StepRecord
	// Captured holds the values that the flow captured, raw. Never store them.
	Captured map[string]string
	// Checked counts the checks of the flow that held.
	Checked int
	// Error is the first thing that stopped the flow: a refusal, an action that failed, a capture that
	// found no value. It is empty when the flow completed.
	Error string
}

// Bind gives the variables that the steps of a flow see: run.id, and each param with the value that
// the caller gave in with, or its default. The values of with are rendered in the scope of the caller,
// which is vars. A key that is no param, and a required param that is not given, are errors.
func Bind(f domain.Flow, with, vars map[string]string) (map[string]string, error) {
	rendered, err := script.RenderWith(with, vars)
	if err != nil {
		return nil, err
	}
	for _, k := range slices.Sorted(maps.Keys(rendered)) {
		if _, ok := f.Param(k); !ok {
			return nil, fmt.Errorf("flow %s has no param %s", f.ID, k)
		}
	}
	scope := map[string]string{}
	if id, ok := vars["run.id"]; ok {
		scope["run.id"] = id
	}
	for _, p := range f.Params {
		v, given := rendered[p.Name]
		switch {
		case given:
			scope[p.Name] = v
		case p.Default != nil:
			scope[p.Name] = *p.Default
		default:
			return nil, fmt.Errorf("flow %s needs the param %s", f.ID, p.Name)
		}
	}
	return scope, nil
}

// Run runs the flow on a device, step by step, through the engine, and stops at the first step that is
// refused, fails, or leaves a capture with no value. with gives the params and vars the scope of the
// caller. It reports completed, and never passed: the checks of a flow are on its steps and stop it when
// one is false or cannot be evaluated, but a flow says no more than that every step ran and every check held.
//
// The error is for what stopped Run before the flow could say anything: a flow that does not fit, a
// screens file that is missing or lacks an element, an engine fault, a device that cannot be used, or a
// cancelled context. A step that does not go well is a Status, with the Error text.
func Run(ctx context.Context, eng Doer, src ScreenSource, f domain.Flow, with, vars map[string]string) (RunResult, error) {
	return RunWith(ctx, eng, src, f, with, vars, Options{})
}

// RunWith is Run with options.
func RunWith(ctx context.Context, eng Doer, src ScreenSource, f domain.Flow, with, vars map[string]string, o Options) (RunResult, error) {
	res := RunResult{Flow: domain.FlowRef{ID: f.ID}, Captured: map[string]string{}}
	var resolve Resolver
	if o.Flows != nil {
		resolve = func(id string) (domain.Flow, error) {
			inner, _, err := o.Flows(id)
			return inner, err
		}
	}
	if ps := ValidateWith(f, f.ID, resolve); len(ps) > 0 {
		return res, fmt.Errorf("flow %s does not fit: %s", f.ID, ps[0])
	}
	if eng == nil || src == nil {
		return res, errors.New("a flow needs an engine and the screens files to run")
	}
	if resolve == nil && slices.ContainsFunc(f.Steps, func(st domain.ScriptStep) bool { return st.Use != "" }) {
		return res, fmt.Errorf("flow %s uses another flow, and this run has no flows", f.ID)
	}
	scope, err := Bind(f, with, vars)
	if err != nil {
		return res, err
	}
	files, err := prepare(f, src, o.Flows, 1)
	if err != nil {
		return res, err
	}
	if !o.KeepState {
		if s, ok := eng.(scenarioStarter); ok {
			s.NewScenario()
		}
	}
	r := &runner{eng: eng, files: files, o: o, res: &res}
	if r.o.Use == nil {
		r.o.Use = func(step string) domain.UseCase {
			return domain.UseCase{Pass: domain.PassScenario, Scenario: domain.ScenarioID("flow-" + f.ID), Step: domain.StepID(f.ID + "." + step)}
		}
	}
	if r.o.By == "" {
		r.o.By = domain.ActorReplay
	}
	if _, err := r.run(ctx, f, scope, []string{f.ID}, ""); err != nil {
		return res, err
	}
	if res.Status == "" {
		res.Status = StatusCompleted
	}
	return res, nil
}

// runner is what the flows of one run share.
type runner struct {
	eng   Doer
	files map[string]domain.Screens
	o     Options
	res   *RunResult
}

// run sends the steps of one flow, and the steps of the flows that it uses, in order. chain holds the
// flows from the outermost to this one, and top the name of the step of the outermost flow that is
// running. It reports true when the run is over, with the status set unless it is an error.
func (r *runner) run(ctx context.Context, f domain.Flow, scope map[string]string, chain []string, top string) (bool, error) {
	for _, st := range Steps(f) {
		name := top
		if len(chain) == 1 {
			name = string(st.Step)
		}
		if st.Use != "" {
			inner, _, err := r.o.Flows(st.Use)
			if err != nil {
				return true, fmt.Errorf("%s, step %s: %w", whereOf(chain), st.Step, err)
			}
			innerScope, err := Bind(inner, st.With, scope)
			if err != nil {
				return true, fmt.Errorf("%s, step %s: %w", whereOf(chain), st.Step, err)
			}
			before := maps.Clone(r.res.Captured)
			stop, err := r.run(ctx, inner, innerScope, append(slices.Clone(chain), inner.ID), name)
			if err != nil || stop {
				return true, err
			}
			// What the used flow captured is seen by the steps after it.
			for k, v := range r.res.Captured {
				if _, had := before[k]; !had {
					scope[k] = v
				}
			}
			continue
		}
		if r.o.Before != nil {
			if err := ctx.Err(); err != nil {
				return true, err
			}
			if err := r.o.Before(ctx, chain[len(chain)-1]+"."+string(st.Step)); err != nil {
				r.res.Status, r.res.Error = StatusRefused, err.Error()
				return true, nil
			}
		}
		call, err := Call(r.files[st.Mobile.App], st, scope)
		if err != nil {
			return true, fmt.Errorf("%s, step %s: %w", whereOf(chain), st.Step, err)
		}
		call.Use, call.By = r.o.Use(name), r.o.By
		call.Why = WhyChain(chain, string(st.Step), "")
		out, err := r.eng.Do(ctx, call)
		if out.Action.ID != "" || out.Action.Surface != "" {
			r.res.Steps = append(r.res.Steps, StepRecord{Name: string(st.Step), Action: out.Action})
		}
		if err != nil {
			return true, fmt.Errorf("%s, step %s: %w", whereOf(chain), st.Step, err)
		}
		switch a := out.Action; {
		case a.Refused != "":
			r.res.Status, r.res.Error = StatusRefused, fmt.Sprintf("step %s was refused: %s", st.Step, a.Refused)
			return true, nil
		case a.Err != "":
			r.res.Status, r.res.Error = StatusFailed, fmt.Sprintf("step %s failed: %s", st.Step, a.Err)
			return true, nil
		case len(out.Missing) > 0:
			r.res.Status, r.res.Error = StatusFailed, fmt.Sprintf("step %s: capture %s found no value", st.Step, strings.Join(out.Missing, ", "))
			return true, nil
		}
		maps.Copy(scope, out.Captured)
		maps.Copy(r.res.Captured, out.Captured)
		for _, e := range st.Expect {
			got := EvalExpect(e.Check, out.Action, scope, scope)
			if got.State != domain.CheckTrue {
				r.res.Status = StatusFailed
				if got.State == domain.CheckFalse {
					r.res.Error = fmt.Sprintf("step %s: expected %s, observed %s", st.Step, got.Expected, got.Observed)
				} else {
					r.res.Error = fmt.Sprintf("step %s: a check could not be evaluated: %s", st.Step, got.Detail)
				}
				return true, nil
			}
			r.res.Checked++
		}
	}
	return false, nil
}

// EvalExpect decides one check of a flow step on the stored action, with the variables of the flow
// filled in from vars. shown holds the same variables as a report may print them. A variable that has no value leaves the check not evaluated.
func EvalExpect(c domain.Check, a domain.Action, vars, shown map[string]string) check.Result {
	if len(domain.OperandVariables(c)) == 0 {
		return check.Eval(c, a)
	}
	template := check.Describe(c)
	filled, missing := domain.FillOperands(c, script.Lookup(vars))
	if len(missing) > 0 {
		return check.Result{
			State:    domain.CheckNotEvaluated,
			Expected: template,
			Detail:   "no value for " + strings.Join(missing, ", ") + " in the expected value",
		}
	}
	res := check.Eval(filled, a)
	display, _ := domain.FillOperands(c, script.Lookup(shown))
	res.Expected = check.Describe(display) + " (written as " + template + ")"
	return res
}

func whereOf(chain []string) string {
	return "flow " + strings.Join(chain, " > ")
}

// Why is the text that an action of a flow step carries as its reason, so that a report tells the steps
// of a flow apart: "flow login, step email", and the action of the scenario step after a colon.
func Why(flowID, step, parent string) string { return WhyChain([]string{flowID}, step, parent) }

// WhyChain is Why for a step of a flow that another flow uses: "flow outer > flow inner, step x".
func WhyChain(chain []string, step, parent string) string {
	flows := make([]string, len(chain))
	for i, id := range chain {
		flows[i] = "flow " + id
	}
	w := fmt.Sprintf("%s, step %s", strings.Join(flows, " > "), step)
	if parent != "" {
		w += ": " + parent
	}
	return w
}

// prepare loads the screens file of each app that the steps of the flow, and of the flows it uses, need,
// and checks every step against it, so that no step is sent when a screen or an element is missing.
func prepare(f domain.Flow, src ScreenSource, flows func(string) (domain.Flow, string, error), depth int) (map[string]domain.Screens, error) {
	var apps []string
	uses := map[string][]screens.Use{}
	var collect func(f domain.Flow, chain []string, depth int) error
	collect = func(f domain.Flow, chain []string, depth int) error {
		if depth > maxRunDepth {
			return fmt.Errorf("flows are nested more than %d deep: %s", MaxDepth, strings.Join(chain, " > "))
		}
		for _, st := range Steps(f) {
			if st.Use != "" {
				if flows == nil {
					return fmt.Errorf("flow %s uses flow %s, and this run has no flows", f.ID, st.Use)
				}
				inner, _, err := flows(st.Use)
				if err != nil {
					return fmt.Errorf("flow %s, step %s: %w", f.ID, st.Step, err)
				}
				if err := collect(inner, append(slices.Clone(chain), inner.ID), depth+1); err != nil {
					return err
				}
				continue
			}
			app := st.Mobile.App
			if !slices.Contains(apps, app) {
				apps = append(apps, app)
			}
			uses[app] = append(uses[app], screens.MobileUse(whereOf(chain)+", step "+string(st.Step), *st.Mobile, readElements(st)))
		}
		return nil
	}
	if err := collect(f, []string{f.ID}, depth); err != nil {
		return nil, err
	}
	files := map[string]domain.Screens{}
	var problems []string
	for _, app := range apps {
		scr, err := src.Load(app)
		switch {
		case errors.Is(err, screens.ErrNotFound):
			problems = append(problems, fmt.Sprintf("the screens of %s were not found: %s", app, screens.Path(app)))
			continue
		case err != nil:
			problems = append(problems, fmt.Sprintf("the screens of %s could not be used: %v", app, err))
			continue
		}
		files[app] = scr
		for _, msg := range screens.Check(scr, uses[app]) {
			problems = append(problems, "the flow does not fit the screens of "+app+": "+msg)
		}
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	return files, nil
}

// readElements lists the elements that a step reads after its action: those of its captures and its checks.
func readElements(st domain.ScriptStep) []string {
	out := captureElements(st)
	for _, e := range st.Expect {
		if e.Check.Element != "" && !slices.Contains(out, e.Check.Element) {
			out = append(out, e.Check.Element)
		}
	}
	return out
}

func captureElements(st domain.ScriptStep) []string {
	var out []string
	for _, c := range st.Captures {
		if c.Element != "" && !slices.Contains(out, c.Element) {
			out = append(out, c.Element)
		}
	}
	return out
}

// Call builds the call of one concrete step of a flow, with the variables of scope filled in: the
// action, the locator of every element that it and its captures read, and the elements to watch. The
// caller sets Use, By, and Why. scr is the screens file of the app of the step.
func Call(scr domain.Screens, st domain.ScriptStep, scope map[string]string) (mobile.Call, error) {
	m, err := script.RenderMobile(*st.Mobile, scope)
	if err != nil {
		return mobile.Call{}, err
	}
	names := slices.Concat(m.Elements(), readElements(st))
	locs := map[string]domain.Locator{}
	for _, name := range screens.Needed(scr, m.Screen, names) {
		raw, _ := scr.Locator(m.Screen, name)
		loc, err := script.RenderLocator(raw, scope)
		if err != nil {
			return mobile.Call{}, fmt.Errorf("the locator of element %s: %w", name, err)
		}
		locs[name] = loc
	}
	watch, until := watching(st, scope)
	return mobile.Call{Action: m, Locators: locs, Watch: watch, Until: until, Captures: st.Captures}, nil
}

// watching lists the elements that the engine reads after the action, and the checks that it waits for. An
// element that a check asks only for its existence or its number, and that no capture and no text check
// reads, is read for presence only: its absence is no reason for a repair.
func watching(st domain.ScriptStep, scope map[string]string) ([]domain.Watched, []domain.Check) {
	var order []string
	presence := map[string]bool{}
	use := func(name string, only bool) {
		if name == "" {
			return
		}
		if _, seen := presence[name]; !seen {
			order = append(order, name)
			presence[name] = only
			return
		}
		presence[name] = presence[name] && only
	}
	attrs := map[string][]string{}
	var until []domain.Check
	for _, e := range st.Expect {
		c := e.Check
		use(c.Element, c.PresenceOnly())
		if c.Attribute != "" && !slices.Contains(attrs[c.Element], c.Attribute) {
			attrs[c.Element] = append(attrs[c.Element], c.Attribute)
		}
		if len(domain.OperandVariables(c)) > 0 {
			var missing []string
			if c, missing = domain.FillOperands(c, script.Lookup(scope)); len(missing) > 0 {
				continue
			}
		}
		until = append(until, c)
	}
	for _, c := range st.Captures {
		use(c.Element, false)
	}
	var watch []domain.Watched
	for _, name := range order {
		watch = append(watch, domain.Watched{Element: name, Presence: presence[name], Attrs: attrs[name]})
	}
	return watch, until
}
