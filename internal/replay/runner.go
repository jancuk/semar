// Package replay runs accepted scripts against a service with no model. The
// engine sends and records each step, and verdict decides each outcome from
// what was recorded.
package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/verdict"
)

const (
	defaultWorkers = 4

	reasonNoScript    = "no script was accepted"
	reasonInterrupted = "the run was interrupted"
	reasonNoFixtures  = "the scenario names fixtures, and this run has no fixture catalogue"

	// cleanupLimit bounds the cleanup of the fixtures of one scenario (Plan 17, D15).
	cleanupLimit = 2 * time.Minute
)

type Doer interface {
	Do(ctx context.Context, c api.Call) (api.Result, error)
}

type Scripts interface {
	Script(id domain.ScenarioID, h domain.Hash) (domain.Script, bool, error)
}

// Fixtures gives the state that the given entries of a scenario name, and removes it afterwards.
type Fixtures interface {
	Resolve(ctx context.Context, req fixture.Request) (*fixture.Resolution, error)
	Release(ctx context.Context, res *fixture.Resolution) []domain.FixtureRecord
	Outputs(id domain.FixtureID) ([]string, bool)
}

type Deps struct {
	Engine   Doer
	Scripts  Scripts
	Builds   build.Prober
	Services []string // names to probe
	Confirm  confirm.Store
	Trust    []domain.Source // the sources whose expectations a person must confirm
	Workers  int             // default 4
	Fixtures Fixtures        // nil: a scenario with given entries ends blocked
	Run      domain.RunID    // the value of {{run.id}}
}

type Runner struct{ d Deps }

func New(d Deps) *Runner {
	if d.Workers <= 0 {
		d.Workers = defaultWorkers
	}
	d.Services = slices.Clone(d.Services)
	d.Trust = slices.Clone(d.Trust)
	return &Runner{d: d}
}

// replayed is what one scenario left behind. Its outcome waits for the builds at the end of the run.
type replayed struct {
	steps     map[domain.StepID]verdict.StepEvidence
	blocked   []string
	notRun    string
	confirmed func(domain.Expectation) bool
	notes     []domain.Reason // why the scenario stopped early, when no recorded action says it
	fixtures  []domain.FixtureRecord
}

// Run replays each scenario and returns one result per scenario, in input order.
// The builds are probed once before the first scenario and once after the last.
// A build that cannot be read at the start blocks every scenario and nothing is sent.
// The error is the context's when the run was cancelled, joined with any engine
// error that stopped a scenario. The results are complete in both cases.
func (r *Runner) Run(ctx context.Context, scenarios []domain.Scenario) (results []domain.ScenarioResult, start, end []domain.BuildRef, err error) {
	runs := make([]replayed, len(scenarios))
	start, startFailed := r.probe(ctx)
	switch {
	case ctx.Err() != nil:
		for i, sc := range scenarios {
			runs[i] = r.unsent(sc, reasonInterrupted, nil)
		}
		return r.decide(scenarios, runs, start, nil, nil), start, nil, ctx.Err()
	case len(startFailed) > 0:
		blocked := make([]string, len(startFailed))
		for i, s := range startFailed {
			blocked[i] = fmt.Sprintf("the build of %s could not be read", s)
		}
		for i, sc := range scenarios {
			runs[i] = r.unsent(sc, "", blocked)
		}
		return r.decide(scenarios, runs, start, nil, nil), start, nil, nil
	}

	errs := make([]error, len(scenarios))
	var g errgroup.Group
	g.SetLimit(r.d.Workers)
	for i, sc := range scenarios {
		g.Go(func() error {
			if ctx.Err() != nil {
				runs[i] = r.unsent(sc, reasonInterrupted, nil)
				return nil
			}
			runs[i], errs[i] = r.replay(ctx, sc)
			return nil
		})
	}
	_ = g.Wait()

	var endFailed []string
	if ctx.Err() == nil {
		end, endFailed = r.probe(ctx)
	}
	return r.decide(scenarios, runs, start, end, endFailed), start, end, errors.Join(append(errs, ctx.Err())...)
}

// decide gives verdict the end builds only when every service was read, so a
// build that could not be read is never taken for one that did not change.
func (r *Runner) decide(scenarios []domain.Scenario, runs []replayed, start, end []domain.BuildRef, endFailed []string) []domain.ScenarioResult {
	if len(endFailed) > 0 {
		end = nil
	}
	results := make([]domain.ScenarioResult, len(scenarios))
	for i, sc := range scenarios {
		run := runs[i]
		res := verdict.Compute(verdict.Input{
			Scenario:    sc,
			Steps:       run.steps,
			Confirmed:   run.confirmed,
			BuildsStart: start,
			BuildsEnd:   end,
			NotRun:      run.notRun,
			Blocked:     run.blocked,
		})
		for _, s := range endFailed {
			res.Reasons = append(res.Reasons, domain.Reason{Rule: domain.OutcomeInconclusive, Text: fmt.Sprintf("the build of %s could not be read after the run", s)})
		}
		res.Reasons = append(res.Reasons, run.notes...)
		res.Fixtures = run.fixtures
		results[i] = res
	}
	return results
}

// unsent is a scenario that was decided before any of its steps could be sent.
func (r *Runner) unsent(sc domain.Scenario, notRun string, blocked []string) replayed {
	run := replayed{notRun: notRun, blocked: blocked}
	run.confirmed = r.confirmation(sc, &run)
	return run
}

func (r *Runner) probe(ctx context.Context) (builds []domain.BuildRef, failed []string) {
	for _, s := range r.d.Services {
		b, err := r.d.Builds.Probe(ctx, s)
		if err != nil {
			failed = append(failed, s)
			continue
		}
		builds = append(builds, b)
	}
	return builds, failed
}

// confirmation reads the store once per scenario. An expectation counts as confirmed when its
// source needs no person, or when a record names its source. A store that cannot be read confirms nothing.
func (r *Runner) confirmation(sc domain.Scenario, run *replayed) func(domain.Expectation) bool {
	needs := func(e domain.Expectation) bool { return confirm.Needs(e.Source, r.d.Trust) }
	var sources []domain.Source
	if r.d.Confirm != nil && slices.ContainsFunc(sc.Steps, func(st domain.Step) bool { return slices.ContainsFunc(st.Expect, needs) }) {
		rec, found, err := r.d.Confirm.Get(sc.ID, domain.ExpectHash(sc))
		switch {
		case err != nil:
			run.notes = append(run.notes, domain.Reason{Rule: domain.OutcomeInconclusive, Text: "the confirmations could not be read: " + err.Error()})
		case found:
			sources = rec.Sources
		}
	}
	return func(e domain.Expectation) bool { return !needs(e) || slices.Contains(sources, e.Source) }
}

// replay sends the steps in order and never sends one twice. It stops at the first
// step that has no request in the script, was refused, failed, has no response, or captured no value: a later
// step then has no action. A response with any status lets the scenario go on.
//
// The given entries are resolved after the script is validated, so a bad script sends nothing.
// Their cleanup runs when the scenario ends, also after a failure or an interrupt.
func (r *Runner) replay(ctx context.Context, sc domain.Scenario) (run replayed, doErr error) {
	run = replayed{steps: map[domain.StepID]verdict.StepEvidence{}}
	run.confirmed = r.confirmation(sc, &run)

	s, found, err := r.d.Scripts.Script(sc.ID, domain.ExpectHash(sc))
	switch {
	case err != nil:
		run.blocked = []string{"the script could not be read: " + err.Error()}
		return run, nil
	case !found:
		run.blocked = []string{reasonNoScript}
		return run, nil
	}
	if problems := script.ValidateWith(sc, s, r.outputs(sc)); len(problems) > 0 {
		for _, p := range problems {
			text := "the script does not fit the scenario: " + p.Message
			if p.Step != "" {
				text = fmt.Sprintf("the script does not fit the scenario: step %s: %s", p.Step, p.Message)
			}
			run.blocked = append(run.blocked, text)
		}
		return run, nil
	}

	vars := map[string]string{"run.id": string(r.d.Run)}
	if len(sc.Given) > 0 {
		if r.d.Fixtures == nil {
			run.blocked = []string{reasonNoFixtures}
			return run, nil
		}
		res, err := r.d.Fixtures.Resolve(ctx, fixture.Request{Owner: sc.ID, Given: slices.Clone(sc.Given), AuthProfile: sc.AuthProfile, Pass: domain.PassSetup})
		defer func() {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupLimit)
			defer cancel()
			run.fixtures = r.d.Fixtures.Release(cctx, res)
		}()
		switch {
		case err != nil && ctx.Err() != nil:
			run.notRun = reasonInterrupted
			return run, nil
		case err != nil:
			run.notes = append(run.notes, domain.Reason{Rule: domain.OutcomeInconclusive, Text: "the fixtures could not be sent or recorded"})
			return run, fmt.Errorf("replay %s fixtures: %w", sc.ID, err)
		case len(res.Blocked) > 0:
			run.blocked = slices.Clone(res.Blocked)
			return run, nil
		}
		maps.Copy(vars, res.Vars)
	}
	return run, r.steps(ctx, sc, s, &run, vars)
}

// outputs maps each alias to the outputs of its fixture, for script validation. With a fixture
// that is not in the catalogue it returns nil, which accepts any output: Resolve then blocks the
// scenario as "missing" before any request.
func (r *Runner) outputs(sc domain.Scenario) map[string][]string {
	if len(sc.Given) == 0 || r.d.Fixtures == nil {
		return nil
	}
	out := make(map[string][]string, len(sc.Given))
	for _, g := range sc.Given {
		outs, ok := r.d.Fixtures.Outputs(g.Fixture)
		if !ok {
			return nil
		}
		out[g.As] = outs
	}
	return out
}

func (r *Runner) steps(ctx context.Context, sc domain.Scenario, s domain.Script, run *replayed, vars map[string]string) error {
	bound := make(map[domain.StepID]domain.ScriptStep, len(s.Steps))
	for _, ss := range s.Steps {
		bound[ss.Step] = ss
	}
	note := func(step domain.StepID, text string) {
		run.notes = append(run.notes, domain.Reason{Rule: domain.OutcomeInconclusive, Text: text, Step: step})
	}
	var doErr error
	interrupted := false
	for i, step := range sc.Steps {
		ss, ok := bound[step.ID]
		if !ok || ss.Request == nil {
			note(step.ID, fmt.Sprintf("step %s has no request in the script, so the later steps were not sent", step.ID))
			break
		}
		call, err := buildCall(sc, step, ss, vars)
		if err != nil {
			note(step.ID, fmt.Sprintf("step %s could not be sent: the request could not be built", step.ID))
			break
		}
		res, err := r.d.Engine.Do(ctx, call)
		if res.Action.ID != "" {
			run.steps[step.ID] = evaluate(step, res.Action)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				interrupted = true
			} else {
				note(step.ID, fmt.Sprintf("step %s could not be sent or recorded", step.ID))
				doErr = fmt.Errorf("replay %s step %s: %w", sc.ID, step.ID, err)
			}
			break
		}
		if a := res.Action; a.Response == nil || a.Refused != "" || a.Err != "" {
			break
		}
		if len(res.Missing) > 0 {
			if i < len(sc.Steps)-1 {
				note(step.ID, fmt.Sprintf("step %s: capture %s found no value, so the later steps were not sent", step.ID, strings.Join(res.Missing, ", ")))
			}
			break
		}
		maps.Copy(vars, res.Captured)
	}
	switch {
	case ctx.Err() != nil && len(run.steps) == 0:
		run.notRun = reasonInterrupted
	case interrupted:
		note("", reasonInterrupted)
	}
	return doErr
}

func buildCall(sc domain.Scenario, step domain.Step, ss domain.ScriptStep, vars map[string]string) (api.Call, error) {
	req, err := script.Render(*ss.Request, vars)
	if err != nil {
		return api.Call{}, err
	}
	var body []byte
	if req.JSON != nil {
		if body, err = json.Marshal(req.JSON); err != nil {
			return api.Call{}, fmt.Errorf("json: %w", err)
		}
	}
	pass := domain.PassScenario
	if step.Setup {
		pass = domain.PassSetup
	}
	template := ss.Request.Path
	if step.Hint != nil {
		template = step.Hint.Path
	}
	return api.Call{
		Use:          domain.UseCase{Pass: pass, Scenario: sc.ID, Step: step.ID},
		By:           domain.ActorReplay,
		Service:      req.Service,
		Method:       req.Method,
		Path:         req.Path,
		PathTemplate: template,
		Query:        req.Query,
		Headers:      req.Headers,
		JSON:         body,
		AuthProfile:  sc.AuthProfile,
		Why:          step.Action,
		Captures:     ss.Captures,
	}, nil
}

// evaluate reads only the redacted, stored action.
func evaluate(step domain.Step, a domain.Action) verdict.StepEvidence {
	ev := verdict.StepEvidence{Action: &a, Invariants: check.Invariants(step, a)}
	for _, e := range step.Expect {
		ev.Checks = append(ev.Checks, check.Eval(e.Check, a))
	}
	return ev
}
