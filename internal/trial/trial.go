// Package trial sends the read-only steps of a drafted plan once, so that a wrong
// path or an empty capture shows up before the first run. It decides nothing
// about an expected result: it reports a status and the names of captures, never
// a value of a response.
package trial

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

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/stage/plan"
)

const (
	maxRequests  = 50
	defaultLimit = 2 * time.Minute

	skipNoAnswer = "the environment did not answer"
	skipSignIn   = "the sign-in for the trial failed"
	skipLimit    = "the trial budget was reached before any request of this draft was sent"

	whyWrites          = "writes"
	whyOnce            = "matches effects.once"
	whyBuild           = "the request could not be built"
	whyBudget          = "the trial budget was reached"
	whyNoFixtures      = "fixtures are not resolved in this trial"
	whyFindNotTried    = "find could not be tried"
	engineBudgetText   = "request budget reached" // the refusal of api.Engine when its budget is used
	engineSignInPrefix = "sign-in failed"
)

type Doer interface {
	Do(ctx context.Context, c api.Call) (api.Result, error)
}

type Policy interface {
	Classify(r effects.Request) effects.Class
	Admit(r effects.Request) error
	Once(r effects.Request) bool
}

// Finder is the FindOnly method of fixture.Resolver (Plan 17), declared here (G4).
type Finder interface {
	FindOnly(ctx context.Context, owner domain.ScenarioID, given []domain.Given, authProfile string, pass domain.Pass) ([]domain.GivenFind, error)
}

type Deps struct {
	Engine      Doer              // an api.Engine built with Budget 50 and Timeout 10s
	Policy      Policy            // the read-only policy of D1
	Finder      Finder            // nil: no fixture is resolved
	Builds      build.Prober      // nil: no build is read
	Probed      []string          // services with a build probe
	Services    map[string]string // service name to base URL, to classify before sending
	AuthProfile string
	Env         string
	Run         domain.RunID
	Clock       func() time.Time
	Limit       time.Duration // 2m when zero
}

// Runner keeps the state of one trial across the attempts of a plan: the builds it read, the
// requests it sent, and its time limit are for the whole trial, not for one call of Try. Only the
// time spent inside Try counts against the limit: the planner drafts between two calls.
type Runner struct {
	d Deps

	mu       sync.Mutex
	probed   bool
	builds   []domain.BuildRef
	used     time.Duration // spent inside earlier calls of Try
	deadline time.Time     // of the current call of Try
	overtime bool          // the context of the current call reached the limit
	sent     int
	spent    bool   // the engine refused a request for its budget
	dead     string // why no request is sent any more
}

// errTimeLimit is the cause of the context of a Try that reached the time limit.
var errTimeLimit = errors.New("the trial time limit was reached")

// overLimit reports whether err comes from the time limit of this trial rather than from the
// caller, and then marks the trial as out of time.
func (r *Runner) overLimit(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() == nil || !errors.Is(context.Cause(ctx), errTimeLimit) {
		return false
	}
	r.overtime = true
	return true
}

func New(d Deps) *Runner {
	if d.Clock == nil {
		d.Clock = time.Now
	}
	if d.Limit <= 0 {
		d.Limit = defaultLimit
	}
	d.Probed = slices.Clone(d.Probed)
	d.Services = maps.Clone(d.Services)
	return &Runner{d: d}
}

// Try sends the steps of the scenarios that are reads, in order, and returns what each one
// found. It returns an error only for a recorder failure or a context that ended, with the
// steps that were done by then.
func (r *Runner) Try(ctx context.Context, scenarios []domain.Scenario, scripts []domain.Script) (plan.Trial, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tr := plan.Trial{Run: r.d.Run, Env: r.d.Env}
	if err := ctx.Err(); err != nil {
		return tr, fmt.Errorf("trial: %w", err)
	}
	start := r.d.Clock()
	left := r.d.Limit - r.used
	r.deadline, r.overtime = start.Add(left), false
	defer func() { r.used += max(r.d.Clock().Sub(start), 0) }()
	// The context bounds what the clock checks cannot: a find that waits, or a slow request.
	ctx, cancel := context.WithTimeoutCause(ctx, max(left, 0), errTimeLimit)
	defer cancel()

	if err := r.readBuilds(ctx); err != nil {
		return tr, err
	}
	tr.Builds = slices.Clone(r.builds)
	scriptOf := make(map[domain.ScenarioID]domain.Script, len(scripts))
	for _, s := range scripts {
		scriptOf[s.Scenario] = s
	}
	for _, sc := range scenarios {
		if r.dead != "" {
			break
		}
		s, ok := scriptOf[sc.ID]
		if !ok || len(s.Steps) == 0 {
			continue
		}
		if err := r.scenario(ctx, sc, s, &tr); err != nil {
			return tr, err
		}
	}
	tr.Skipped = r.dead
	if tr.Skipped == "" && limitedOut(tr.Steps) {
		tr.Skipped = skipLimit
	}
	return tr, nil
}

// limitedOut reports whether the limit or the budget kept every request of a draft from being
// sent. Such a draft was not tried, so it must not read as a draft with no problems.
func limitedOut(steps []plan.TrialStep) bool {
	limited := false
	for _, ts := range steps {
		if ts.Result == plan.TrialSent {
			return false
		}
		limited = limited || ts.Why == whyBudget
	}
	return limited
}

// readBuilds probes once. A service that cannot be read is left out, with no line: the trial
// is evidence about requests, and a missing build only shortens what plan.md says about it.
func (r *Runner) readBuilds(ctx context.Context) error {
	if r.probed || r.d.Builds == nil {
		return nil
	}
	var builds []domain.BuildRef
	for _, name := range r.d.Probed {
		b, err := r.d.Builds.Probe(ctx, name)
		if r.overLimit(ctx, ctx.Err()) {
			break
		}
		if ctx.Err() != nil {
			return fmt.Errorf("trial: %w", ctx.Err())
		}
		if err != nil {
			continue
		}
		builds = append(builds, b)
	}
	r.builds, r.probed = builds, true
	return nil
}

// scenarioRun is what the steps of one scenario know: the variables so far, which given
// entries found a record, and which step captures each name.
type scenarioRun struct {
	sc    domain.Scenario
	vars  map[string]string
	found map[string]bool
	owner map[string]domain.StepID
}

func (r *Runner) scenario(ctx context.Context, sc domain.Scenario, s domain.Script, tr *plan.Trial) error {
	run := scenarioRun{
		sc:    sc,
		vars:  map[string]string{"run.id": string(r.d.Run)},
		found: map[string]bool{},
		owner: map[string]domain.StepID{},
	}
	bound := make(map[domain.StepID]domain.ScriptStep, len(s.Steps))
	for _, ss := range s.Steps {
		bound[ss.Step] = ss
		for _, c := range ss.Captures {
			if _, dup := run.owner[c.Name]; !dup {
				run.owner[c.Name] = ss.Step
			}
		}
	}
	if err := r.find(ctx, &run, tr); err != nil {
		return err
	}
	for _, step := range sc.Steps {
		ss, ok := bound[step.ID]
		if !ok || ss.Request == nil {
			continue
		}
		if r.dead != "" {
			return nil
		}
		if err := r.step(ctx, &run, step, ss, tr); err != nil {
			return err
		}
	}
	return nil
}

// find asks for the record of each given entry. It never creates one.
func (r *Runner) find(ctx context.Context, run *scenarioRun, tr *plan.Trial) error {
	sc := run.sc
	if len(sc.Given) == 0 {
		return nil
	}
	notTried := func(g domain.Given, why string) plan.TrialStep {
		return plan.TrialStep{Scenario: sc.ID, Given: g.As, Result: plan.TrialNotTried, Why: why}
	}
	var finds []domain.GivenFind
	switch {
	case r.d.Finder == nil:
	case r.exhausted() != "":
	default:
		var err error
		finds, err = r.d.Finder.FindOnly(ctx, sc.ID, slices.Clone(sc.Given), r.authProfile(sc), domain.PassTrial)
		if r.overLimit(ctx, err) {
			err = nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("trial: %w", ctx.Err())
			}
			return fmt.Errorf("trial: find the fixtures of scenario %s: %w", sc.ID, err)
		}
	}
	byAlias := make(map[string]domain.GivenFind, len(finds))
	for _, f := range finds {
		byAlias[f.As] = f
	}
	for _, g := range sc.Given {
		var ts plan.TrialStep
		f, ok := byAlias[g.As]
		switch {
		case r.d.Finder == nil:
			ts = notTried(g, whyNoFixtures)
		case !ok:
			ts = notTried(g, whyFindNotTried)
			if why := r.exhausted(); why != "" {
				ts.Why = why
			}
		case f.Problem != "":
			ts = notTried(g, whyFindNotTried)
		default:
			ts = plan.TrialStep{Scenario: sc.ID, Given: g.As, Result: plan.TrialSent, Found: f.Found}
			if f.Found {
				run.found[g.As] = true
				for k, v := range f.Outputs {
					run.vars[g.As+"."+k] = v
				}
			}
		}
		if len(f.Actions) > 0 {
			ts.Action = f.Actions[len(f.Actions)-1]
		}
		tr.Steps = append(tr.Steps, ts)
	}
	return nil
}

// authProfile is the one account of the plan. A scenario that needs another account is tried as
// this one, and a 403 may be the reason (Plan 16, open point 5). An edge case is the exception: it
// changes the account on purpose, to another one or to none, so it is tried as the account it names.
func (r *Runner) authProfile(sc domain.Scenario) string {
	if sc.Edge != nil {
		return sc.AuthProfile
	}
	if r.d.AuthProfile != "" {
		return r.d.AuthProfile
	}
	return sc.AuthProfile
}

// exhausted says why no more requests may be sent, or "" when one may be.
func (r *Runner) exhausted() string {
	if r.spent || r.overtime || r.sent >= maxRequests || !r.d.Clock().Before(r.deadline) {
		return whyBudget
	}
	return ""
}

func (r *Runner) step(ctx context.Context, run *scenarioRun, step domain.Step, ss domain.ScriptStep, tr *plan.Trial) error {
	req := ss.Request
	ts := plan.TrialStep{Scenario: run.sc.ID, Step: step.ID, Method: strings.ToUpper(req.Method), Route: req.Path}
	if step.Hint != nil {
		ts.Route = step.Hint.Path
	}
	skip := func(why string) {
		ts.Result, ts.Why = plan.TrialNotTried, why
		tr.Steps = append(tr.Steps, ts)
	}

	if why := r.exhausted(); why != "" {
		skip(why)
		return nil
	}
	if step.As != "" && step.As != run.sc.AuthProfile {
		skip(fmt.Sprintf("acts as account %s, and a trial sends with the account of the plan only", step.As))
		return nil
	}
	if name := firstUnknown(*req, run.vars); name != "" {
		skip(r.needs(run, name))
		return nil
	}
	rendered, err := script.Render(*req, run.vars)
	if err != nil {
		skip(whyBuild)
		return nil
	}
	base, known := r.d.Services[rendered.Service]
	if !known {
		skip(whyBuild)
		return nil
	}
	u, err := api.JoinURL(base, rendered.Path, rendered.Query)
	if err != nil {
		skip(whyBuild)
		return nil
	}
	var body []byte
	if rendered.JSON != nil {
		if body, err = json.Marshal(rendered.JSON); err != nil {
			skip(whyBuild)
			return nil
		}
	}
	preq := effects.Request{Method: ts.Method, URL: u}
	switch {
	case r.d.Policy.Classify(preq) == effects.Write:
		skip(whyWrites)
		return nil
	case r.d.Policy.Once(preq):
		skip(whyOnce)
		return nil
	}
	if err := r.d.Policy.Admit(preq); err != nil {
		skip("denied: " + refusalKind(err))
		return nil
	}

	first := r.sent == 0
	res, err := r.d.Engine.Do(ctx, api.Call{
		Use:          domain.UseCase{Pass: domain.PassTrial, Scenario: run.sc.ID, Step: step.ID},
		By:           domain.ActorReplay,
		Service:      rendered.Service,
		Method:       rendered.Method,
		Path:         rendered.Path,
		PathTemplate: ts.Route,
		Query:        rendered.Query,
		Headers:      rendered.Headers,
		JSON:         body,
		AuthProfile:  r.authProfile(run.sc),
		Why:          step.Action,
		Captures:     ss.Captures,
	})
	if res.Action.ID != "" {
		r.note(&ts, res)
		tr.Steps = append(tr.Steps, ts)
	}
	if r.overLimit(ctx, err) {
		if res.Action.ID == "" {
			skip(whyBudget)
		}
		return nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("trial: %w", ctx.Err())
		}
		return fmt.Errorf("trial: scenario %s step %s: %w", run.sc.ID, step.ID, err)
	}
	if ts.Result == plan.TrialSent {
		r.sent++
		if first && res.Action.Response == nil {
			r.dead = skipNoAnswer
			if strings.HasPrefix(res.Action.Err, engineSignInPrefix) {
				r.dead = skipSignIn
			}
		}
		if ts.Status > 0 && ts.Status < 400 {
			maps.Copy(run.vars, res.Captured)
		}
	}
	return nil
}

// note turns a recorded action into the fields of a step. It copies a status, an error that the
// engine wrote without a URL or a value, and the names of captures. It never copies a body or a header.
func (r *Runner) note(ts *plan.TrialStep, res api.Result) {
	a := res.Action
	ts.Action = a.ID
	if a.Refused != "" && a.Response == nil {
		ts.Result, ts.Why = plan.TrialNotTried, refusedWhy(a.Refused)
		if ts.Why == whyBudget {
			r.spent = true
		}
		return
	}
	ts.Result = plan.TrialSent
	if a.Response != nil {
		ts.Status = a.Response.Status
	}
	ts.Err = a.Err
	ts.Doc = a.Doc
	switch {
	case a.Refused != "":
		// A redirect that the policy refused: the response is a 3xx and nothing was captured from it.
		ts.Err = a.Refused
	case a.Response != nil && a.Response.Truncated:
		ts.Err = "the response was larger than the limit, so no capture was read"
	case a.Err == "":
		ts.Missing = slices.Clone(res.Missing)
	}
}

// refusedWhy maps the refusal that the engine recorded to a fixed reason. The engine writes a
// refusal as "<kind>: <fixed text>", a redirect refusal in words, or its budget text.
func refusedWhy(text string) string {
	if text == engineBudgetText {
		return whyBudget
	}
	kind, _, _ := strings.Cut(text, ":")
	switch effects.RefusalKind(kind) {
	case effects.HostNotAllowed, effects.MethodNotAllowed, effects.Denied, effects.ReadOnly:
		return "denied: " + kind
	}
	return "denied: refused"
}

func refusalKind(err error) string {
	var ref *effects.Refusal
	if errors.As(err, &ref) {
		return string(ref.Kind)
	}
	return "refused"
}

// firstUnknown names the first variable of the request that has no value yet.
func firstUnknown(req domain.APIRequest, vars map[string]string) string {
	var name string
	_ = script.RequestProblems(req, func(n string) bool {
		if _, ok := vars[n]; !ok && name == "" {
			name = n
		}
		return true
	})
	return name
}

// needs says why a variable has no value. It names the variable, a fixture, or a step of the
// scenario, never a value.
func (r *Runner) needs(run *scenarioRun, name string) string {
	head, _, dotted := strings.Cut(name, ".")
	if dotted {
		for _, g := range run.sc.Given {
			if g.As != head {
				continue
			}
			switch {
			case r.d.Finder == nil:
				return fmt.Sprintf("needs {{%s}}, and fixtures are not resolved in this trial", name)
			case !run.found[head]:
				return fmt.Sprintf("needs {{%s}}, and fixture %s found no record; a trial never creates one", name, g.Fixture)
			}
			break
		}
		return whyBuild
	}
	if id, ok := run.owner[name]; ok {
		return fmt.Sprintf("needs {{%s}}, which step %s captures, and that step was not tried", name, id)
	}
	return whyBuild
}
