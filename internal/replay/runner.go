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
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/verdict"
	"github.com/jancuk/semar/internal/words"
)

const (
	defaultWorkers = 4

	reasonNoScript    = "no script was accepted"
	reasonNoFit       = "no script fits the steps of %s: the steps, the account, or the given changed since its script was stored"
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
	// ScriptsOf returns every stored script of the scenario, in no set order.
	ScriptsOf(id domain.ScenarioID) ([]domain.Script, error)
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
	Services []string     // names to probe
	Workers  int          // default 4
	Fixtures Fixtures     // nil: a scenario with given entries ends blocked
	Run      domain.RunID // the value of {{run.id}}
	// Accounts are the auth profiles of the configuration that a step may act as (steps[].as).
	// The profile of a scenario itself is always known.
	Accounts []string
	// NoToken are the accounts that have no token. A scenario with an API step as one of them ends
	// blocked before it sends anything.
	NoToken []string
	// Web is the browser engine and Screens the screens files that its steps use. Without them, a
	// scenario with a web step ends blocked.
	Web     WebDoer
	Screens ScreenSource
	// Mobile is the engine of the app on a device. Without it, a scenario with a step on a device ends
	// blocked. MobileServices are the services that have an android app in this environment: a step on
	// another service ends blocked. Nil does not check.
	Mobile         MobileDoer
	MobileServices []string
	// Retries is how many more times a scenario is replayed after an attempt that failed or was
	// inconclusive. Zero replays nothing.
	Retries int
	// Once says whether sending the call would use a request of the once list of the environment. Nil
	// means the environment has none.
	Once func(api.Call) bool
}

type Runner struct {
	d Deps
	// webMu holds one scenario with web steps at a time, so that two of them never act on one page.
	webMu sync.Mutex
	// mobileMu does the same for the device. A scenario that holds both takes webMu first.
	mobileMu sync.Mutex
}

func New(d Deps) *Runner {
	if d.Workers <= 0 {
		d.Workers = defaultWorkers
	}
	d.Services = slices.Clone(d.Services)
	d.Accounts = slices.Clone(d.Accounts)
	d.NoToken = slices.Clone(d.NoToken)
	return &Runner{d: d}
}

// replayed is what one scenario left behind. Its outcome waits for the builds at the end of the run.
type replayed struct {
	steps      map[domain.StepID]verdict.StepEvidence
	blocked    []string
	notRun     string
	scriptHash domain.Hash     // the expectation hash that the script that ran was stored under
	notes      []domain.Reason // why the scenario stopped early, when no recorded action says it
	fixtures   []domain.FixtureRecord
	captured   map[string]string // every capture of every step, raw. Never stored
	last       *api.Call         // the call of the last step that was sent
	sentOnce   bool              // a request of the once list was sent, or may have been
	earlier    []replayed        // the attempts before this one, oldest first
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
			runs[i], errs[i] = r.attempts(ctx, sc)
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
		all := append(slices.Clone(run.earlier), run)
		attempts := make([]domain.Attempt, len(all))
		var fixtures []domain.FixtureRecord
		var res domain.ScenarioResult
		for j, a := range all {
			res = judge(sc, a, start, end)
			for _, s := range endFailed {
				res.Reasons = append(res.Reasons, domain.Reason{Rule: domain.OutcomeInconclusive, Text: fmt.Sprintf("the build of %s could not be read after the run", s)})
			}
			res.Reasons = append(res.Reasons, a.notes...)
			attempts[j] = domain.Attempt{Outcome: res.Outcome, Reasons: res.Reasons, Checks: res.Checks, Actions: res.Actions}
			fixtures = append(fixtures, a.fixtures...)
		}
		deciding, flaky := domain.Settle(attempts)
		res.Outcome, res.Reasons, res.Checks, res.Actions = deciding.Outcome, deciding.Reasons, deciding.Checks, deciding.Actions
		res.Flaky, res.Fixtures = flaky, fixtures
		res.ScriptHash = run.scriptHash
		if len(attempts) > 1 {
			res.Attempts, res.AttemptsAllowed = attempts, r.d.Retries+1
		}
		results[i] = res
	}
	return results
}

func judge(sc domain.Scenario, run replayed, start, end []domain.BuildRef) domain.ScenarioResult {
	return verdict.Compute(verdict.Input{
		Scenario:    sc,
		Steps:       run.steps,
		BuildsStart: start,
		BuildsEnd:   end,
		NotRun:      run.notRun,
		Blocked:     run.blocked,
	})
}

// attempts replays a scenario until it passes or cannot pass by being replayed again. Each replay
// starts with a clean browser and fresh variables. The error is that of the last replay.
func (r *Runner) attempts(ctx context.Context, sc domain.Scenario) (replayed, error) {
	var earlier []replayed
	for {
		run, err := r.replay(ctx, sc)
		if err != nil || ctx.Err() != nil || len(earlier) >= r.d.Retries || !retriable(sc, run) {
			run.earlier = earlier
			return run, err
		}
		if run.sentOnce {
			run.notes = append(run.notes, domain.Reason{Rule: domain.OutcomeInconclusive, Text: words.NotRunAgain})
			run.earlier = earlier
			return run, nil
		}
		earlier = append(earlier, run)
	}
}

// retriable says whether an attempt ended in a way that another attempt could change: it failed, it
// was inconclusive, or it was blocked only because a setup
// step got no good answer. Any other block is the environment's rule or a missing file, which a
// second attempt cannot change. The builds are judged once for the
// whole run, so this reads the attempt with stand-in builds that never differ.
func retriable(sc domain.Scenario, run replayed) bool {
	same := []domain.BuildRef{{Service: "-", Commit: "-"}}
	res := judge(sc, run, same, same)
	switch res.Outcome {
	case domain.OutcomeFailed:
		return true
	case domain.OutcomeInconclusive:
		return true
	case domain.OutcomeBlocked:
		return !slices.ContainsFunc(res.Reasons, func(reason domain.Reason) bool {
			return reason.Rule == domain.OutcomeBlocked && !strings.HasPrefix(reason.Text, verdict.SetupFailedPrefix)
		})
	}
	return false
}

// unsent is a scenario that was decided before any of its steps could be sent.
func (r *Runner) unsent(sc domain.Scenario, notRun string, blocked []string) replayed {
	run := replayed{notRun: notRun, blocked: blocked}
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

// replay sends the steps in order and never sends one twice. It stops at the first
// step that has no request in the script, was refused, failed, has no response, or captured no value: a later
// step then has no action. A response with any status lets the scenario go on.
//
// The given entries are resolved after the script is validated, so a bad script sends nothing.
// Their cleanup runs when the scenario ends, also after a failure or an interrupt.
func (r *Runner) replay(ctx context.Context, sc domain.Scenario) (run replayed, doErr error) {
	run = newReplayed()

	if blocked := r.unknownAccounts(sc); len(blocked) > 0 {
		run.blocked = blocked
		return run, nil
	}
	s, byShape, blocked := r.findScript(sc)
	if len(blocked) > 0 {
		run.blocked = blocked
		return run, nil
	}
	run.scriptHash = s.ExpectHash
	validate := script.ValidateWith
	if byShape {
		validate = script.ValidateShapeWith
	}
	if blocked := scriptBlocked(validate(sc, s, r.outputs(sc))); len(blocked) > 0 {
		run.blocked = blocked
		return run, nil
	}

	if blocked := r.noTokenSteps(sc, s); len(blocked) > 0 {
		run.blocked = blocked
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
	return run, r.steps(ctx, sc, s, &run, vars, domain.PassScenario)
}

// findScript takes the script of the scenario's expectation hash. Without one it takes the newest
// script stored for the same shape, which a script stored before Plan 36 never has. It reports
// why when there is none.
func (r *Runner) findScript(sc domain.Scenario) (s domain.Script, byShape bool, blocked []string) {
	s, found, err := r.d.Scripts.Script(sc.ID, domain.ExpectHash(sc))
	switch {
	case err != nil:
		return s, false, []string{"the script could not be read: " + err.Error()}
	case found:
		return s, false, nil
	}
	all, err := r.d.Scripts.ScriptsOf(sc.ID)
	switch {
	case err != nil:
		return s, false, []string{"the script could not be read: " + err.Error()}
	case len(all) == 0:
		return s, false, []string{reasonNoScript}
	}
	shape := domain.ShapeHash(sc)
	best := -1
	for i, c := range all {
		if shape == "" || c.ShapeHash != shape {
			continue
		}
		if best < 0 || c.StoredAt.After(all[best].StoredAt) ||
			(c.StoredAt.Equal(all[best].StoredAt) && c.ExpectHash > all[best].ExpectHash) {
			best = i
		}
	}
	if best < 0 {
		return s, false, []string{fmt.Sprintf(reasonNoFit, sc.ID)}
	}
	return all[best], true, nil
}

// unknownAccounts gives one reason for each auth profile that a step acts as and the configuration
// does not have. The scenario's own profile is not checked here: it is the configuration's to supply.
func (r *Runner) unknownAccounts(sc domain.Scenario) []string {
	var blocked []string
	for _, st := range sc.Steps {
		if st.As == "" || st.As == sc.AuthProfile || slices.Contains(r.d.Accounts, st.As) {
			continue
		}
		text := fmt.Sprintf("account %s is not in the configuration", st.As)
		if !slices.Contains(blocked, text) {
			blocked = append(blocked, text)
		}
	}
	return blocked
}

// noTokenSteps gives one reason for each account with no token that an API step acts as. A web step
// of such an account needs no token.
func (r *Runner) noTokenSteps(sc domain.Scenario, s domain.Script) []string {
	if len(r.d.NoToken) == 0 {
		return nil
	}
	api := map[domain.StepID]bool{}
	for _, ss := range s.Steps {
		api[ss.Step] = ss.Request != nil
	}
	var blocked []string
	for _, st := range sc.Steps {
		account := sc.AuthProfile
		if st.As != "" {
			account = st.As
		}
		if !api[st.ID] || !slices.Contains(r.d.NoToken, account) {
			continue
		}
		text := fmt.Sprintf("the account %s has no token, so an API step as it is not sent; add a session and secrets to it in semar.yaml", account)
		if !slices.Contains(blocked, text) {
			blocked = append(blocked, text)
		}
	}
	return blocked
}

func newReplayed() replayed {
	return replayed{steps: map[domain.StepID]verdict.StepEvidence{}, captured: map[string]string{}}
}

// scriptBlocked turns the problems of a script into the reasons that block its scenario.
func scriptBlocked(problems []script.Problem) []string {
	var blocked []string
	for _, p := range problems {
		text := "the script does not fit the scenario: " + p.Message
		if p.Step != "" {
			text = fmt.Sprintf("the script does not fit the scenario: step %s: %s", p.Step, p.Message)
		}
		blocked = append(blocked, text)
	}
	return blocked
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

// steps sends the steps of sc. A step that is not a setup step belongs to pass. It writes to vars
// and to run, so each call needs its own.
func (r *Runner) steps(ctx context.Context, sc domain.Scenario, s domain.Script, run *replayed, vars map[string]string, pass domain.Pass) error {
	bound := make(map[domain.StepID]domain.ScriptStep, len(s.Steps))
	for _, ss := range s.Steps {
		bound[ss.Step] = ss
	}
	note := func(step domain.StepID, text string) {
		run.notes = append(run.notes, domain.Reason{Rule: domain.OutcomeInconclusive, Text: text, Step: step})
	}
	var wr *webRun
	if slices.ContainsFunc(s.Steps, func(ss domain.ScriptStep) bool { return ss.Web != nil }) {
		r.webMu.Lock()
		defer r.webMu.Unlock()
		var proceed bool
		if wr, proceed = r.prepareWeb(sc, s, run); !proceed {
			return nil
		}
		if wr != nil {
			r.d.Web.NewScenario()
		}
	}
	var mr *webRun
	if slices.ContainsFunc(s.Steps, func(ss domain.ScriptStep) bool { return ss.Mobile != nil }) {
		r.mobileMu.Lock()
		defer r.mobileMu.Unlock()
		var proceed bool
		if mr, proceed = r.prepareMobile(sc, s, run); !proceed {
			return nil
		}
		if mr != nil {
			r.d.Mobile.NewScenario()
		}
	}
	var doErr error
	interrupted := false
	// shown holds the variables as a report may show them: a capture is the redacted value of
	// the stored action, so a secret that a response held is never printed.
	shown := maps.Clone(vars)
	for i, step := range sc.Steps {
		ss, ok := bound[step.ID]
		if !ok || (ss.Request == nil && ss.Web == nil && ss.Mobile == nil) {
			what := "request"
			switch sc.Surface {
			case domain.SurfaceWeb:
				what = "web action"
			case domain.SurfaceAndroid:
				what = "device action"
			}
			note(step.ID, fmt.Sprintf("step %s has no %s in the script, so the later steps were not sent", step.ID, what))
			break
		}
		res, err := r.send(ctx, wr, mr, run, sc, step, ss, vars, pass, note)
		if !res.built {
			break
		}
		if res.Action.ID != "" {
			have, showing := maps.Clone(vars), maps.Clone(shown)
			maps.Copy(have, res.Captured)
			maps.Copy(showing, res.Action.Captures)
			run.steps[step.ID] = evaluate(step, res.Action, have, showing)
		}
		if err != nil {
			if reason, blocked := blockedByBrowser(err); blocked {
				run.blocked = append(run.blocked, reason)
			} else if reason, blocked := blockedByDevice(err); blocked {
				run.blocked = append(run.blocked, reason)
			} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				interrupted = true
			} else {
				note(step.ID, fmt.Sprintf("step %s could not be sent or recorded", step.ID))
				doErr = fmt.Errorf("replay %s step %s: %w", sc.ID, step.ID, err)
			}
			break
		}
		if a := res.Action; (a.UI() == nil && a.Response == nil) || a.Refused != "" || a.Err != "" {
			break
		}
		maps.Copy(run.captured, res.Captured)
		if len(res.Missing) > 0 {
			if i < len(sc.Steps)-1 {
				note(step.ID, fmt.Sprintf("step %s: capture %s found no value, so the later steps were not sent", step.ID, strings.Join(res.Missing, ", ")))
			}
			break
		}
		maps.Copy(vars, res.Captured)
		maps.Copy(shown, res.Action.Captures)
	}
	switch {
	case ctx.Err() != nil && len(run.steps) == 0:
		run.notRun = reasonInterrupted
	case interrupted:
		note("", reasonInterrupted)
	}
	return doErr
}

// sent is the answer of an engine to one step, whichever engine it was. built is false when the step
// could not be built, and the note says why.
type sent struct {
	Action   domain.Action
	Captured map[string]string
	Missing  []string
	built    bool
}

// send hands one step to the engine of its surface: the browser for a web step, the device for a mobile
// step, the API engine for a request. It is the one place where they meet, so that a step is recorded,
// judged, and stopped on in one way.
func (r *Runner) send(ctx context.Context, wr, mr *webRun, run *replayed, sc domain.Scenario, step domain.Step, ss domain.ScriptStep, vars map[string]string, pass domain.Pass, note func(domain.StepID, string)) (sent, error) {
	if ss.Web != nil {
		call, err := wr.webCall(sc, step, ss, vars, pass)
		if err != nil {
			note(step.ID, fmt.Sprintf("step %s could not be run: %v", step.ID, err))
			return sent{}, nil
		}
		res, err := r.d.Web.Do(ctx, call)
		return sent{res.Action, res.Captured, res.Missing, true}, err
	}
	if ss.Mobile != nil {
		call, err := mr.mobileCall(sc, step, ss, vars, pass)
		if err != nil {
			note(step.ID, fmt.Sprintf("step %s could not be run: %v", step.ID, err))
			return sent{}, nil
		}
		res, err := r.d.Mobile.Do(ctx, call)
		return sent{res.Action, res.Captured, res.Missing, true}, err
	}
	call, err := buildCall(sc, step, ss, vars, pass)
	if err != nil {
		note(step.ID, fmt.Sprintf("step %s could not be sent: the request could not be built", step.ID))
		return sent{}, nil
	}
	once := r.d.Once != nil && r.d.Once(call)
	res, err := r.d.Engine.Do(ctx, call)
	if once && res.Action.Refused == "" {
		run.sentOnce = true
	}
	run.last = &call
	return sent{res.Action, res.Captured, res.Missing, true}, err
}

func buildCall(sc domain.Scenario, step domain.Step, ss domain.ScriptStep, vars map[string]string, pass domain.Pass) (api.Call, error) {
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
	if step.Setup {
		pass = domain.PassSetup
	}
	template := ss.Request.Path
	if step.Hint != nil {
		template = step.Hint.Path
	}
	profile := sc.AuthProfile
	if step.As != "" {
		profile = step.As
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
		AuthProfile:  profile,
		Why:          step.Action,
		Captures:     ss.Captures,
	}, nil
}

// evaluate reads only the redacted, stored action. A variable in a string operand is filled in
// from vars before the check; shown holds the same variables as a report may print them.
func evaluate(step domain.Step, a domain.Action, vars, shown map[string]string) verdict.StepEvidence {
	ev := verdict.StepEvidence{Action: &a}
	if a.UI() == nil {
		// An action on a screen has no response to hold an invariant.
		ev.Invariants = check.Invariants(step, a)
	}
	if a.UI() != nil && a.Err != "" {
		// The action did not happen, for instance because its element was not found. The screen is as it
		// was before the step, so what it shows says nothing about what the step was to do: no check is
		// decided, and the step ends inconclusive and never failed.
		for _, e := range step.Expect {
			ev.Checks = append(ev.Checks, check.Result{
				State: domain.CheckNotEvaluated, Expected: check.Describe(e.Check), Detail: "the step did not finish: " + a.Err,
			})
		}
		return ev
	}
	for _, e := range step.Expect {
		ev.Checks = append(ev.Checks, evalExpectation(e.Check, a, vars, shown))
	}
	return ev
}

func evalExpectation(c domain.Check, a domain.Action, vars, shown map[string]string) check.Result {
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
