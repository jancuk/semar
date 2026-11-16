package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/script"
)

const (
	defaultEvery     = 2 * time.Second
	maxWait          = 15 * time.Second
	defaultLeaseWait = 2 * time.Minute
	leasePoll        = time.Second

	ViaFind   = "find"
	ViaCreate = "create"
	ViaNone   = "none"

	CleanupDone      = "done"
	CleanupFailed    = "failed"
	CleanupNone      = "none"
	CleanupNotNeeded = "not_needed"
)

type Approvals interface {
	FixtureApproved(id domain.FixtureID, h domain.Hash) (bool, error)
}

type Health interface {
	Latest(env string, id domain.FixtureID) (HealthRecord, bool, error)
}

type Doer interface {
	Do(ctx context.Context, c api.Call) (api.Result, error)
}

type Deps struct {
	Engine      Doer
	Policy      *effects.Policy
	Catalogue   Catalogue
	Approvals   Approvals
	Health      Health
	Leases      *Leases // nil refuses lease: exclusive with a blocked reason
	Environment string
	Services    map[string]string // service name to base URL, of this environment
	ServiceAuth map[string]string // service to auth profile, from configuration
	Data        map[string]string // environments.<env>.data
	Run         domain.RunID
	Clock       func() time.Time
	Sleep       func(ctx context.Context, d time.Duration) error // waits, or returns ctx.Err()
	LeaseWait   time.Duration                                    // 2m when zero
}

// Resolver gives the state that the given entries of a scenario name, through the run's engine,
// so every fixture request is admitted by the effects policy and recorded like a scenario step.
type Resolver struct{ d Deps }

func New(d Deps) *Resolver {
	if d.Clock == nil {
		d.Clock = time.Now
	}
	if d.Sleep == nil {
		d.Sleep = sleep
	}
	if d.LeaseWait <= 0 {
		d.LeaseWait = defaultLeaseWait
	}
	d.Services = maps.Clone(d.Services)
	d.ServiceAuth = maps.Clone(d.ServiceAuth)
	d.Data = maps.Clone(d.Data)
	return &Resolver{d: d}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Request is one owner's given entries. Owner is a scenario ID, or a journey ID (Plan 18).
type Request struct {
	Owner       domain.ScenarioID
	Given       []domain.Given
	AuthProfile string      // used for a service that has no auth profile
	Pass        domain.Pass // setup for a run
}

type Resolution struct {
	Vars    map[string]string // <as>.<output> to the raw value. Never stored
	Blocked []string          // texts for verdict.Input.Blocked; empty when every state was given
	Records []domain.FixtureRecord

	entries []*entry
	once    sync.Once
}

// entry is one given entry as it resolves. vars holds raw values and is never stored.
type entry struct {
	given   domain.Given
	fixture domain.Fixture
	owner   domain.ScenarioID
	auth    string
	pass    domain.Pass
	params  map[string]string // the parameters and run.id
	vars    map[string]string // params and the captures of create, for cleanup
	made    bool              // a create write reached the service, or may have
	check   bool              // a health check: the last health record does not block it, and no lease is taken
	release []func()
	rec     domain.FixtureRecord
}

func records(entries []*entry) []domain.FixtureRecord {
	out := make([]domain.FixtureRecord, len(entries))
	for i, e := range entries {
		r := e.rec
		r.Outputs = maps.Clone(r.Outputs)
		r.Left = slices.Clone(r.Left)
		r.Actions = slices.Clone(r.Actions)
		out[i] = r
	}
	return out
}

// Outputs lists the outputs of a fixture of the catalogue, for script validation.
func (r *Resolver) Outputs(id domain.FixtureID) ([]string, bool) {
	f, ok := r.d.Catalogue.Get(id)
	if !ok {
		return nil, false
	}
	return slices.Clone(f.Outputs), true
}

// Resolve gives the state of each entry in order and stops at the first that fails. It returns an
// error only for a recorder failure or a cancelled context, and then still returns what it made,
// so that Release can clean it up. Every other failure is a text in Blocked.
func (r *Resolver) Resolve(ctx context.Context, req Request) (*Resolution, error) {
	res := &Resolution{Vars: map[string]string{}}
	defer func() { res.Records = records(res.entries) }()
	for _, g := range req.Given {
		e := r.newEntry(req.Owner, req.AuthProfile, req.Pass, g)
		res.entries = append(res.entries, e)
		blocked, err := r.resolveOne(ctx, e, res.Vars)
		if err != nil {
			return res, err
		}
		if len(blocked) > 0 {
			res.Blocked = blocked
			return res, nil
		}
	}
	return res, nil
}

func (r *Resolver) newEntry(owner domain.ScenarioID, auth string, pass domain.Pass, g domain.Given) *entry {
	return &entry{
		given: g, owner: owner, auth: auth, pass: pass,
		rec: domain.FixtureRecord{As: g.As, Fixture: g.Fixture, Via: ViaNone, Cleanup: CleanupNotNeeded, Actions: []domain.ActionID{}},
	}
}

// prepare runs the checks that need no request: load, approval, health, policy, and parameters.
func (r *Resolver) prepare(e *entry) []string {
	g := e.given
	f, ok := r.d.Catalogue.Get(g.Fixture)
	if !ok {
		return []string{fmt.Sprintf("fixture %s (as %s) is missing", g.Fixture, g.As)}
	}
	e.fixture = f
	h := domain.FixtureHash(f)
	e.rec.Version, e.rec.Hash = f.Version, h

	approved := false
	if r.d.Approvals != nil {
		var err error
		if approved, err = r.d.Approvals.FixtureApproved(f.ID, h); err != nil {
			return []string{fmt.Sprintf("fixture %s: its approvals could not be read: %v", f.ID, err)}
		}
	}
	if !approved {
		return []string{fmt.Sprintf("fixture %s is not approved: run semar confirm --fixture %s", f.ID, f.ID)}
	}
	if r.d.Health != nil && !e.check {
		rec, found, err := r.d.Health.Latest(r.d.Environment, f.ID)
		if err != nil {
			return []string{fmt.Sprintf("fixture %s: its health record could not be read: %v", f.ID, err)}
		}
		if found {
			if text := unhealthy(rec, f); text != "" {
				return []string{text}
			}
		}
	}
	var blocked []string
	for _, p := range CheckPolicy(f, r.d.Policy, r.d.Services) {
		blocked = append(blocked, fmt.Sprintf("fixture %s: %s: %s", f.ID, p.Path, r.inEnvironment(p.Message)))
	}
	if len(blocked) > 0 {
		return blocked
	}
	params, text := r.params(f, g)
	if text != "" {
		return []string{text}
	}
	e.params = params
	return nil
}

// inEnvironment names the environment in a message of CheckPolicy, which does not know it.
func (r *Resolver) inEnvironment(msg string) string {
	if r.d.Environment == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, "in this environment", "in environment "+r.d.Environment)
	return strings.ReplaceAll(msg, "of this environment", "of "+r.d.Environment)
}

// params fills each parameter from with, else its default. {{run.id}} and {{data.<key>}} are
// replaced in both. The result also holds run.id, so a request may use it directly.
func (r *Resolver) params(f domain.Fixture, g domain.Given) (map[string]string, string) {
	for _, k := range slices.Sorted(maps.Keys(g.With)) {
		if _, ok := f.Params[k]; !ok {
			return nil, fmt.Sprintf("fixture %s: with sets %s, which is not a parameter of the fixture", f.ID, k)
		}
	}
	out := map[string]string{"run.id": string(r.d.Run)}
	for _, name := range slices.Sorted(maps.Keys(f.Params)) {
		raw, ok := g.With[name]
		if !ok {
			d := f.Params[name].Default
			if d == nil {
				return nil, fmt.Sprintf("fixture %s: parameter %s has no value", f.ID, name)
			}
			raw = *d
		}
		var problem string
		v := variablePattern.ReplaceAllStringFunc(raw, func(m string) string {
			name := variablePattern.FindStringSubmatch(m)[1]
			if name == "run.id" {
				return string(r.d.Run)
			}
			if key, ok := strings.CutPrefix(name, "data."); ok {
				if val, ok := r.d.Data[key]; ok {
					return val
				}
				if problem == "" {
					problem = fmt.Sprintf("fixture %s: data %s is not set for environment %s", f.ID, key, r.d.Environment)
				}
				return m
			}
			if problem == "" {
				problem = fmt.Sprintf("fixture %s: parameter %s: {{%s}} is not run.id or data.<key>", f.ID, name, name)
			}
			return m
		})
		if problem != "" {
			return nil, problem
		}
		out[name] = v
	}
	return out, ""
}

func (e *entry) exclusive() bool {
	if e.check {
		return false
	}
	if e.given.Lease != "" {
		return e.given.Lease == domain.LeaseExclusive
	}
	return e.fixture.Lease == domain.LeaseExclusive
}

func (r *Resolver) resolveOne(ctx context.Context, e *entry, exposed map[string]string) ([]string, error) {
	if blocked := r.prepare(e); len(blocked) > 0 {
		return blocked, nil
	}
	f := e.fixture
	if e.exclusive() && r.d.Leases == nil {
		return []string{fmt.Sprintf("fixture %s (as %s) needs lease: exclusive, and this run has no lease directory", f.ID, e.given.As)}, nil
	}

	var fd found
	if len(f.Find) > 0 {
		var err error
		if fd, err = r.find(ctx, e); err != nil {
			return nil, err
		}
		if fd.block != "" {
			return []string{fd.block}, nil
		}
	}
	held := false
	if fd.ok && e.exclusive() {
		release, ok, err := r.d.Leases.TryLock(r.d.Environment, f.ID, fd.vars[f.Outputs[0]])
		if err != nil {
			return []string{fmt.Sprintf("fixture %s: the lease could not be taken: %v", f.ID, err)}, nil
		}
		if ok {
			e.release = append(e.release, release)
		}
		held = !ok
	}

	switch {
	case fd.ok && !held:
		r.expose(e, ViaFind, fd.vars, fd.redacted, exposed)
		return nil, nil
	case len(f.Create) == 0 && held:
		return r.waitForLease(ctx, e, fd, exposed)
	case len(f.Create) == 0:
		return []string{fmt.Sprintf("fixture %s: find gave nothing and the fixture has no create steps", f.ID)}, nil
	case r.d.Policy != nil && r.d.Policy.ReadOnly():
		return []string{fmt.Sprintf("this environment is read-only: fixture %s found no record and cannot create one", f.ID)}, nil
	}
	return r.create(ctx, e, exposed)
}

// waitForLease polls the lease of the record that find gave, for a fixture that cannot make another.
func (r *Resolver) waitForLease(ctx context.Context, e *entry, fd found, exposed map[string]string) ([]string, error) {
	f := e.fixture
	deadline := r.d.Clock().Add(r.d.LeaseWait)
	for {
		remaining := deadline.Sub(r.d.Clock())
		if remaining <= 0 {
			return []string{fmt.Sprintf("lease not available: fixture %s is held by another run on this machine (waited %s)", f.ID, r.d.LeaseWait)}, nil
		}
		if err := r.d.Sleep(ctx, min(leasePoll, remaining)); err != nil {
			return nil, err
		}
		release, ok, err := r.d.Leases.TryLock(r.d.Environment, f.ID, fd.vars[f.Outputs[0]])
		if err != nil {
			return []string{fmt.Sprintf("fixture %s: the lease could not be taken: %v", f.ID, err)}, nil
		}
		if ok {
			e.release = append(e.release, release)
			r.expose(e, ViaFind, fd.vars, fd.redacted, exposed)
			return nil, nil
		}
	}
}

func (r *Resolver) create(ctx context.Context, e *entry, exposed map[string]string) ([]string, error) {
	f := e.fixture
	e.vars = maps.Clone(e.params)
	redacted := map[string]string{}
	for i, st := range f.Create {
		o, err := r.runStep(ctx, e, e.pass, domain.PhaseCreate, i, e.vars)
		if o.wrote && (o.unknown || (o.status > 0 && o.status < 400)) {
			e.made = true
		}
		if err != nil {
			return nil, err
		}
		label := stepLabel(domain.PhaseCreate, i, st)
		switch {
		case o.unknown:
			return []string{fmt.Sprintf("fixture effect unknown: fixture %s %s may have reached the service; nothing was sent again", f.ID, label)}, nil
		case o.policy || o.timedOut:
			return []string{fmt.Sprintf("fixture %s: %s %s", f.ID, label, o.failure)}, nil
		case o.failure != "":
			how := ""
			if len(f.Find) > 0 {
				how = "find gave nothing and "
			}
			return []string{fmt.Sprintf("fixture %s: %s%s %s", f.ID, how, label, o.failure)}, nil
		}
		maps.Copy(e.vars, o.captured)
		maps.Copy(redacted, o.action.Captures)
	}
	for _, name := range f.Outputs {
		if _, ok := e.vars[name]; !ok {
			return []string{fmt.Sprintf("fixture %s: output %s has no value", f.ID, name)}, nil
		}
	}
	if e.exclusive() {
		release, ok, err := r.d.Leases.TryLock(r.d.Environment, f.ID, e.vars[f.Outputs[0]])
		switch {
		case err != nil:
			return []string{fmt.Sprintf("fixture %s: the lease could not be taken: %v", f.ID, err)}, nil
		case !ok:
			return []string{fmt.Sprintf("lease not available: fixture %s made a record that another run on this machine holds", f.ID)}, nil
		}
		e.release = append(e.release, release)
	}
	r.expose(e, ViaCreate, e.vars, redacted, exposed)
	return nil, nil
}

// expose publishes the outputs as <as>.<name>. The record keeps only the redacted values.
func (r *Resolver) expose(e *entry, via string, vars, redacted map[string]string, exposed map[string]string) {
	e.rec.Via = via
	e.rec.Outputs = map[string]string{}
	for _, name := range e.fixture.Outputs {
		exposed[e.given.As+"."+name] = vars[name]
		if v, ok := redacted[name]; ok {
			e.rec.Outputs[name] = v
		}
	}
}

// found is what the find steps of one entry gave.
type found struct {
	ok       bool
	problem  string            // why a step failed with something other than "nothing", for FindOnly
	block    string            // a request the policy refuses: the entry is blocked
	vars     map[string]string // raw
	redacted map[string]string
}

// find sends the find steps in order. A step that answers 404, captures nothing, or fails gives
// nothing, and the failure stays in the recorded action.
func (r *Resolver) find(ctx context.Context, e *entry) (found, error) {
	f := e.fixture
	fd := found{vars: maps.Clone(e.params), redacted: map[string]string{}}
	for i, st := range f.Find {
		o, err := r.runStep(ctx, e, e.pass, domain.PhaseFind, i, fd.vars)
		if err != nil {
			return found{}, err
		}
		label := stepLabel(domain.PhaseFind, i, st)
		switch {
		case o.policy:
			fd.block = fmt.Sprintf("fixture %s: %s %s", f.ID, label, o.failure)
			return fd, nil
		case o.nothing:
			return fd, nil
		case o.failure != "":
			fd.problem = fmt.Sprintf("fixture %s: %s %s", f.ID, label, o.failure)
			return fd, nil
		}
		maps.Copy(fd.vars, o.captured)
		maps.Copy(fd.redacted, o.action.Captures)
	}
	for _, name := range f.Outputs {
		if _, ok := fd.vars[name]; !ok {
			return fd, nil
		}
	}
	fd.ok = true
	return fd, nil
}

// FindOnly sends the find steps of each entry and never a create. Plan 16 uses it with pass trial.
// Outputs of a found record are keyed by output name. No lease is taken.
func (r *Resolver) FindOnly(ctx context.Context, owner domain.ScenarioID, given []domain.Given, authProfile string, pass domain.Pass) ([]domain.GivenFind, error) {
	out := make([]domain.GivenFind, 0, len(given))
	for _, g := range given {
		gf := domain.GivenFind{As: g.As, Fixture: g.Fixture}
		e := r.newEntry(owner, authProfile, pass, g)
		if blocked := r.prepare(e); len(blocked) > 0 {
			gf.Problem = blocked[0]
			out = append(out, gf)
			continue
		}
		var fd found
		var err error
		if len(e.fixture.Find) > 0 {
			fd, err = r.find(ctx, e)
		}
		if len(e.rec.Actions) > 0 {
			gf.Actions = slices.Clone(e.rec.Actions)
		}
		if err != nil {
			return append(out, gf), err
		}
		switch {
		case fd.block != "":
			gf.Problem = fd.block
		case fd.problem != "":
			gf.Problem = fd.problem
		case len(e.fixture.Find) == 0:
			gf.Problem = fmt.Sprintf("fixture %s has no find steps", g.Fixture)
		case fd.ok:
			gf.Found = true
			gf.Outputs = map[string]string{}
			for _, name := range e.fixture.Outputs {
				gf.Outputs[name] = fd.vars[name]
			}
		}
		out = append(out, gf)
	}
	return out, nil
}

func stepLabel(phase string, i int, st domain.FixtureStep) string {
	return fmt.Sprintf("%s step %d (%s %s)", phase, i+1, st.Request.Method, st.Request.Path)
}

func stepsOf(f domain.Fixture, phase string) []domain.FixtureStep {
	switch phase {
	case domain.PhaseFind:
		return f.Find
	case domain.PhaseCreate:
		return f.Create
	}
	return f.Cleanup
}

// outcome is what one request of a fixture step did. failure is "" when the step answered below
// 400 and captured every value. Its text never holds a rendered value.
type outcome struct {
	action    domain.Action // the stored action; empty when nothing was sent
	captured  map[string]string
	missing   []string // capture names that gave no value
	status    int
	failure   string
	wrote     bool // the request was a write that the engine sent or tried to send
	unknown   bool // a write whose effect is unknown
	policy    bool // refused by the resolver before the engine: once, or a find that writes
	nothing   bool // a find step that found nothing: 404 or a capture with no value
	timedOut  bool
	notSent   bool
	renderErr error
}

// runStep sends one step, and repeats a step with until while its condition is false and it
// answers below 400. The waits come from Deps.Sleep and the elapsed time from Deps.Clock.
func (r *Resolver) runStep(ctx context.Context, e *entry, pass domain.Pass, phase string, i int, vars map[string]string) (outcome, error) {
	st := stepsOf(e.fixture, phase)[i]
	u := st.Until
	if u == nil {
		o, err := r.send(ctx, e, pass, phase, i, 0, vars)
		finish(&o, phase)
		return o, err
	}
	start := r.d.Clock()
	wait := u.Every
	if wait <= 0 {
		wait = defaultEvery
	}
	for attempt := 1; ; attempt++ {
		o, err := r.send(ctx, e, pass, phase, i, attempt, vars)
		if err != nil || o.failure != "" {
			finish(&o, phase)
			return o, err
		}
		if check.Eval(domain.Check{JSONPath: u.JSONPath, Equals: u.Equals}, o.action).State == domain.CheckTrue {
			finish(&o, phase)
			return o, nil
		}
		remaining := u.Timeout - r.d.Clock().Sub(start)
		if remaining <= 0 {
			o.failure = fmt.Sprintf("waited %s and %s was never %v (%d attempts)", u.Timeout, u.JSONPath, u.Equals, attempt)
			o.timedOut = true
			return o, nil
		}
		if err := r.d.Sleep(ctx, min(wait, remaining)); err != nil {
			return o, err
		}
		wait = min(wait*2, maxWait)
	}
}

// finish turns captures with no value into a failure, and marks what counts as nothing for find.
func finish(o *outcome, phase string) {
	if o.failure == "" && len(o.missing) > 0 {
		o.failure = "captured no value for " + strings.Join(o.missing, ", ")
		o.nothing = phase == domain.PhaseFind
	}
	if phase == domain.PhaseFind && o.status == 404 {
		o.nothing = true
	}
}

// send renders one step, checks the rendered request against the parts of the policy that the
// engine does not enforce, and sends it through the engine.
func (r *Resolver) send(ctx context.Context, e *entry, pass domain.Pass, phase string, i, attempt int, vars map[string]string) (outcome, error) {
	f := e.fixture
	st := stepsOf(f, phase)[i]
	rendered, err := script.Render(st.Request, vars)
	if err != nil {
		return outcome{notSent: true, renderErr: err, failure: "could not be built: " + err.Error()}, nil
	}
	// The engine joins the path to the base URL and keeps its path prefix; the policy is matched
	// against that URL, so the resolver joins it the same way.
	base, known := r.d.Services[rendered.Service]
	if !known {
		return outcome{notSent: true, failure: "could not be built: service " + rendered.Service + " is not in " + r.envName()}, nil
	}
	u, err := api.JoinURL(base, rendered.Path, rendered.Query)
	if err != nil {
		return outcome{notSent: true, renderErr: err, failure: "could not be built: " + err.Error()}, nil
	}
	preq := effects.Request{Method: rendered.Method, URL: u}
	if r.d.Policy != nil {
		if phase == domain.PhaseFind && r.d.Policy.Classify(preq) != effects.Read {
			return outcome{notSent: true, policy: true, failure: fmt.Sprintf("writes under the effects policy of %s; a find step must read", r.envName())}, nil
		}
		if r.d.Policy.Once(preq) {
			return outcome{notSent: true, policy: true, failure: "matches effects.once, and a fixture runs on every run"}, nil
		}
	}
	var body []byte
	if rendered.JSON != nil {
		if body, err = json.Marshal(rendered.JSON); err != nil {
			return outcome{notSent: true, renderErr: err, failure: "could not be built: its json body does not encode"}, nil
		}
	}
	auth := r.d.ServiceAuth[rendered.Service]
	if auth == "" {
		auth = e.auth
	}
	res, err := r.d.Engine.Do(ctx, api.Call{
		Use: domain.UseCase{
			Pass: pass, Scenario: e.owner, Attempt: attempt,
			Fixture: &domain.FixtureUse{ID: f.ID, As: e.given.As, Phase: phase, Index: i + 1},
		},
		By:           domain.ActorReplay,
		Service:      rendered.Service,
		Method:       rendered.Method,
		Path:         rendered.Path,
		PathTemplate: st.Request.Path,
		Query:        rendered.Query,
		Headers:      rendered.Headers,
		JSON:         body,
		AuthProfile:  auth,
		Why:          fmt.Sprintf("fixture %s (as %s): %s step %d", f.ID, e.given.As, phase, i+1),
		Captures:     st.Captures,
	})
	o := outcome{action: res.Action, captured: res.Captured, missing: res.Missing}
	if res.Action.ID != "" {
		e.rec.Actions = append(e.rec.Actions, res.Action.ID)
		o.wrote = r.d.Policy != nil && r.d.Policy.Classify(preq) == effects.Write && res.Action.Refused == ""
	}
	if err != nil {
		return o, fmt.Errorf("fixture %s %s: %w", f.ID, stepLabel(phase, i, st), err)
	}
	a := res.Action
	if a.Response != nil {
		o.status = a.Response.Status
	}
	switch {
	case a.Refused != "":
		o.failure = "was refused: " + a.Refused
	case a.EffectUnknown:
		o.unknown = true
		o.failure = "may have reached the service, and its effect is unknown"
	case a.Err != "":
		o.failure = "failed: " + a.Err
	case a.Response == nil:
		o.failure = "has no response"
	case a.Response.Status > 399:
		o.failure = fmt.Sprintf("answered %d", a.Response.Status)
	}
	return o, nil
}

func (r *Resolver) envName() string {
	if r.d.Environment == "" {
		return "this environment"
	}
	return r.d.Environment
}
