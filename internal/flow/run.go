package flow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

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
// caller. It reports completed, and never passed: a flow has no check, so it can say only that every
// step ran.
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
	if ps := Validate(f, f.ID); len(ps) > 0 {
		return res, fmt.Errorf("flow %s does not fit: %s", f.ID, ps[0])
	}
	if eng == nil || src == nil {
		return res, errors.New("a flow needs an engine and the screens files to run")
	}
	scope, err := Bind(f, with, vars)
	if err != nil {
		return res, err
	}
	steps := Steps(f)
	files, err := prepare(f, steps, src)
	if err != nil {
		return res, err
	}
	if !o.KeepState {
		if s, ok := eng.(scenarioStarter); ok {
			s.NewScenario()
		}
	}
	use, by := o.Use, o.By
	if use == nil {
		use = func(step string) domain.UseCase {
			return domain.UseCase{Pass: domain.PassScenario, Scenario: domain.ScenarioID("flow-" + f.ID), Step: domain.StepID(f.ID + "." + step)}
		}
	}
	if by == "" {
		by = domain.ActorReplay
	}
	for _, st := range steps {
		call, err := Call(files[st.Mobile.App], st, scope)
		if err != nil {
			return res, fmt.Errorf("flow %s, step %s: %w", f.ID, st.Step, err)
		}
		call.Use, call.By = use(string(st.Step)), by
		call.Why = Why(f.ID, string(st.Step), "")
		out, err := eng.Do(ctx, call)
		if out.Action.ID != "" || out.Action.Surface != "" {
			res.Steps = append(res.Steps, StepRecord{Name: string(st.Step), Action: out.Action})
		}
		if err != nil {
			return res, fmt.Errorf("flow %s, step %s: %w", f.ID, st.Step, err)
		}
		switch a := out.Action; {
		case a.Refused != "":
			res.Status, res.Error = StatusRefused, fmt.Sprintf("step %s was refused: %s", st.Step, a.Refused)
			return res, nil
		case a.Err != "":
			res.Status, res.Error = StatusFailed, fmt.Sprintf("step %s failed: %s", st.Step, a.Err)
			return res, nil
		case len(out.Missing) > 0:
			res.Status, res.Error = StatusFailed, fmt.Sprintf("step %s: capture %s found no value", st.Step, strings.Join(out.Missing, ", "))
			return res, nil
		}
		maps.Copy(scope, out.Captured)
		maps.Copy(res.Captured, out.Captured)
	}
	res.Status = StatusCompleted
	return res, nil
}

// Why is the text that an action of a flow step carries as its reason, so that a report tells the steps
// of a flow apart: "flow login, step email", and the action of the scenario step after a colon.
func Why(flowID, step, parent string) string {
	w := fmt.Sprintf("flow %s, step %s", flowID, step)
	if parent != "" {
		w += ": " + parent
	}
	return w
}

// prepare loads the screens file of each app that the steps use, and checks every step against it, so
// that no step is sent when a screen or an element is missing.
func prepare(f domain.Flow, steps []domain.ScriptStep, src ScreenSource) (map[string]domain.Screens, error) {
	var apps []string
	uses := map[string][]screens.Use{}
	for _, st := range steps {
		app := st.Mobile.App
		if !slices.Contains(apps, app) {
			apps = append(apps, app)
		}
		uses[app] = append(uses[app], screens.MobileUse(fmt.Sprintf("flow %s, step %s", f.ID, st.Step), *st.Mobile, captureElements(st)))
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
	names := slices.Concat(m.Elements(), captureElements(st))
	locs := map[string]domain.Locator{}
	for _, name := range screens.Needed(scr, m.Screen, names) {
		raw, _ := scr.Locator(m.Screen, name)
		loc, err := script.RenderLocator(raw, scope)
		if err != nil {
			return mobile.Call{}, fmt.Errorf("the locator of element %s: %w", name, err)
		}
		locs[name] = loc
	}
	var watch []domain.Watched
	for _, name := range captureElements(st) {
		watch = append(watch, domain.Watched{Element: name})
	}
	return mobile.Call{Action: m, Locators: locs, Watch: watch, Captures: st.Captures}, nil
}
