package journey

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/replay"
	"github.com/jancuk/semar/internal/verdict"
)

const (
	// The intervals of a wait are those of a fixture's until: the first wait, doubled up to a limit.
	defaultEvery = 2 * time.Second
	maxEvery     = 15 * time.Second
	// cleanupLimit bounds the cleanup of the journey's fixtures (Plan 17, D15).
	cleanupLimit = 2 * time.Minute

	reasonInterrupted = "the run was interrupted"
	reasonNoFixtures  = "the journey names fixtures, and this run has no fixture catalogue"
	reasonNoGiven     = "the fixtures of the journey were not given"
)

// Single runs one leg as a scenario, and sends the last call of a leg again for a wait. Both go
// through the engine of the run, so every request of a journey is admitted by the effects policy
// and recorded like a scenario step.
type Single interface {
	RunOne(ctx context.Context, in replay.Single) (replay.Replayed, error)
	Resend(ctx context.Context, c api.Call, attempt int, step domain.Step) (verdict.StepEvidence, map[string]string, error)
}

type Fixtures interface {
	Resolve(ctx context.Context, req fixture.Request) (*fixture.Resolution, error)
	Release(ctx context.Context, res *fixture.Resolution) []domain.FixtureRecord
}

// Catalogue gives a fixture by its ID, so that the services its steps call join the manifest.
type Catalogue interface {
	Get(id domain.FixtureID) (domain.Fixture, bool)
}

type Deps struct {
	Replay    Single    // *replay.Runner
	Fixtures  Fixtures  // *fixture.Resolver; nil blocks a journey with given
	Catalogue Catalogue // fixture.Catalogue; nil adds no fixture service to the manifest
	Builds    build.Prober
	Services  map[string]string // service name to auth profile, "" when none
	Accounts  []string          // the auth profiles of the configuration that a leg or a step may act as
	Clock     func() time.Time
	Sleep     func(ctx context.Context, d time.Duration) error // waits, or returns ctx.Err()
}

type Runner struct{ d Deps }

func New(d Deps) *Runner {
	if d.Clock == nil {
		d.Clock = time.Now
	}
	if d.Sleep == nil {
		d.Sleep = sleep
	}
	d.Services = maps.Clone(d.Services)
	d.Accounts = slices.Clone(d.Accounts)
	return &Runner{d: d}
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

// legRun is one leg as it ran. rep holds raw captures and is never stored.
type legRun struct {
	leg      domain.Leg
	sc       domain.Scenario
	script   domain.Script
	notRun   string // not empty: the leg did not start, and why
	rep      replay.Replayed
	attempts []domain.ActionID // the earlier attempts of a wait, oldest first
	waited   string            // why the wait timed out; "" otherwise
	missing  []string          // outputs that have no value
}

// Run runs one journey and returns its result and the results of its legs, in run order. The
// error is the context's when the run was interrupted, joined with any engine error; the results
// are complete in both cases and hold the evidence of every leg that ran.
func (r *Runner) Run(ctx context.Context, j domain.Journey) (jr domain.JourneyResult, results []domain.ScenarioResult, err error) {
	order, err := Order(j)
	if err != nil {
		return domain.JourneyResult{}, nil, fmt.Errorf("journey %s: %w", j.ID, err)
	}
	runs := make([]*legRun, len(order))
	for i, id := range order {
		leg := j.Legs[slices.IndexFunc(j.Legs, func(l domain.Leg) bool { return l.ID == id })]
		auth := r.d.Services[leg.Service]
		if auth == "" {
			auth = j.AuthProfile
		}
		sc, sr := legScenario(j, leg, auth)
		runs[i] = &legRun{leg: leg, sc: sc, script: sr}
	}
	var errs []error
	in := Outcome{}
	var end *domain.Manifest
	var invariants []domain.InvariantResult
	finish := func() (domain.JourneyResult, []domain.ScenarioResult, error) {
		in.Legs, results = r.judgeAll(runs, in.Start, end)
		in.Invariants = invariants
		in.End = end
		outcome, first, reasons := Decide(in)
		jr.Journey, jr.Manifest, jr.ManifestEnd = j.ID, in.Start, end
		jr.Outcome, jr.FirstFailing, jr.Reasons = outcome, first, reasons
		jr.Legs, jr.Invariants = in.Legs, invariants
		if jr.Invariants == nil {
			jr.Invariants = []domain.InvariantResult{}
		}
		return jr, results, errors.Join(append(errs, ctx.Err())...)
	}
	notStarted := func(from int, why string) {
		for _, lr := range runs[from:] {
			if lr.notRun == "" {
				lr.notRun = why
			}
		}
	}

	if ctx.Err() != nil {
		notStarted(0, reasonInterrupted)
		return finish()
	}
	if blocked := r.unknownAccounts(runs); len(blocked) > 0 {
		// Nothing is read or sent: the builds are not probed for a journey that cannot run.
		in.Start = domain.Manifest{Services: []domain.BuildRef{}, Hash: domain.ManifestHash(nil)}
		in.AccountProblem = blocked
		notStarted(0, "nothing was sent: "+blocked[0])
		return finish()
	}
	services := r.manifestServices(j)
	start, problems := r.read(ctx, services)
	in.Start = start
	switch {
	case ctx.Err() != nil:
		notStarted(0, reasonInterrupted)
		return finish()
	case len(problems) > 0:
		in.ManifestProblem = strings.Join(problems, "; ")
		notStarted(0, in.ManifestProblem)
		return finish()
	}

	vars := map[string]string{}
	shown := map[string]string{} // the redacted value of each variable, for the report
	if len(j.Given) > 0 {
		res, blocked, gerr := r.given(ctx, j)
		if r.d.Fixtures != nil {
			defer func() {
				cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupLimit)
				defer cancel()
				jr.Fixtures = r.d.Fixtures.Release(cctx, res)
			}()
		}
		switch {
		case gerr != nil && ctx.Err() != nil:
			notStarted(0, reasonInterrupted)
			return finish()
		case gerr != nil:
			errs = append(errs, gerr)
		}
		if len(blocked) > 0 {
			in.FixtureProblem = blocked
			notStarted(0, reasonNoGiven)
			end = r.readEnd(ctx, services)
			return finish()
		}
		maps.Copy(vars, res.Vars)
		for _, rec := range res.Records {
			for name, v := range rec.Outputs {
				shown[rec.As+"."+name] = v
			}
		}
	}

	allPassed := true
	for i, lr := range runs {
		if ctx.Err() != nil {
			notStarted(i, reasonInterrupted)
			allPassed = false
			break
		}
		if err := r.runLeg(ctx, j, lr, vars); err != nil {
			errs = append(errs, err)
		}
		if prov := r.judge(lr, start.Services, start.Services); prov.Outcome != domain.OutcomePassed {
			why := fmt.Sprintf("stopped after leg %s", lr.leg.ID)
			if ctx.Err() != nil {
				why = reasonInterrupted
			}
			notStarted(i+1, why)
			allPassed = false
			break
		}
		for _, name := range lr.leg.Outputs {
			key := string(lr.leg.ID) + "." + name
			vars[key] = lr.rep.Captured[name]
			shown[key] = recorded(lr, name)
		}
	}
	if allPassed {
		invariants = evalInvariants(j, vars, shown)
	}
	end = r.readEnd(ctx, services)
	return finish()
}

// unknownAccounts gives one text for each auth profile that a leg or a step acts as and the
// configuration lacks. The profile of the leg's service is the leg's own and is always known.
func (r *Runner) unknownAccounts(runs []*legRun) []string {
	var blocked []string
	for _, lr := range runs {
		for _, st := range lr.sc.Steps {
			if st.As == "" || st.As == lr.sc.AuthProfile || slices.Contains(r.d.Accounts, st.As) {
				continue
			}
			if text := "account " + st.As + " is not in the configuration"; !slices.Contains(blocked, text) {
				blocked = append(blocked, text)
			}
		}
	}
	return blocked
}

// given resolves the journey's fixtures once, for every leg. It returns the texts that block the journey.
func (r *Runner) given(ctx context.Context, j domain.Journey) (*fixture.Resolution, []string, error) {
	if r.d.Fixtures == nil {
		return nil, []string{reasonNoFixtures}, nil
	}
	res, err := r.d.Fixtures.Resolve(ctx, fixture.Request{
		Owner: domain.ScenarioID(j.ID), Given: slices.Clone(j.Given), AuthProfile: j.AuthProfile, Pass: domain.PassSetup,
	})
	switch {
	case err != nil:
		return res, []string{"the fixtures could not be sent or recorded"}, fmt.Errorf("journey %s fixtures: %w", j.ID, err)
	case res == nil:
		return nil, []string{"the fixtures gave no result"}, nil
	case len(res.Blocked) > 0:
		return res, slices.Clone(res.Blocked), nil
	}
	return res, nil, nil
}

// manifestServices is every service that a leg names or that a step of a given fixture calls, sorted.
func (r *Runner) manifestServices(j domain.Journey) []string {
	set := map[string]bool{}
	for _, l := range j.Legs {
		set[l.Service] = true
	}
	if r.d.Catalogue != nil {
		for _, g := range j.Given {
			f, ok := r.d.Catalogue.Get(g.Fixture)
			if !ok {
				continue
			}
			for _, st := range slices.Concat(f.Find, f.Create, f.Cleanup) {
				if st.Request.Service != "" {
					set[st.Request.Service] = true
				}
			}
		}
	}
	delete(set, "")
	return slices.Sorted(maps.Keys(set))
}

// read probes every service. A service that cannot be read is left out of the manifest and named.
func (r *Runner) read(ctx context.Context, services []string) (domain.Manifest, []string) {
	m := domain.Manifest{Services: []domain.BuildRef{}}
	var problems []string
	for _, s := range services {
		b, err := r.d.Builds.Probe(ctx, s)
		if err != nil {
			problems = append(problems, fmt.Sprintf("the build of %s could not be read", s))
			continue
		}
		m.Services = append(m.Services, b)
	}
	m.Hash = domain.ManifestHash(m.Services)
	return m, problems
}

// readEnd is the manifest at the end, or nil when the run was interrupted or a part could not be read.
func (r *Runner) readEnd(ctx context.Context, services []string) *domain.Manifest {
	if ctx.Err() != nil {
		return nil
	}
	m, problems := r.read(ctx, services)
	if len(problems) > 0 || ctx.Err() != nil {
		return nil
	}
	return &m
}

// runLeg sends the steps of one leg, then repeats its last step while it waits. A leg reads the
// given outputs and the outputs of every leg it needs, directly or through other legs.
func (r *Runner) runLeg(ctx context.Context, j domain.Journey, lr *legRun, vars map[string]string) error {
	in := map[string]string{}
	for k, v := range vars {
		head, _, _ := strings.Cut(k, ".")
		if !isLeg(j, head) || slices.Contains(needed(j, lr.leg.ID), domain.LegID(head)) {
			in[k] = v
		}
	}
	rep, err := r.d.Replay.RunOne(ctx, replay.Single{Scenario: lr.sc, Script: lr.script, Vars: in})
	lr.rep = rep
	if lr.rep.Steps == nil {
		lr.rep.Steps = map[domain.StepID]verdict.StepEvidence{}
	}
	if lr.rep.Captured == nil {
		lr.rep.Captured = map[string]string{}
	}
	if err == nil && ctx.Err() == nil && lr.leg.Wait != nil {
		err = r.wait(ctx, lr)
	}
	for _, name := range lr.leg.Outputs {
		if _, ok := lr.rep.Captured[name]; !ok {
			lr.missing = append(lr.missing, name)
		}
	}
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("journey %s leg %s: %w", j.ID, lr.leg.ID, err)
	}
	return nil
}

// wait repeats the last step of the leg until its condition holds or the timeout passes. Each
// attempt is a new recorded action, and the step's evidence is the last attempt.
func (r *Runner) wait(ctx context.Context, lr *legRun) error {
	w := lr.leg.Wait
	last := lr.sc.Steps[len(lr.sc.Steps)-1]
	ev, ok := lr.rep.Steps[last.ID]
	if !ok || !answered(ev.Action) || lr.rep.Last == nil || lr.rep.Last.Use.Step != last.ID {
		return nil
	}
	call := *lr.rep.Last
	cond := domain.Check{JSONPath: w.Until.JSONPath, Equals: w.Until.Equals}
	every := w.Every
	if every <= 0 {
		every = defaultEvery
	}
	began := r.d.Clock()
	for attempt := 1; ; attempt++ {
		if check.Eval(cond, *ev.Action).State == domain.CheckTrue {
			return nil
		}
		remaining := w.Timeout - r.d.Clock().Sub(began)
		if remaining <= 0 {
			lr.waited = fmt.Sprintf("waited %s and %s was never %v (%d attempts)", w.Timeout, w.Until.JSONPath, w.Until.Equals, attempt)
			return nil
		}
		if err := r.d.Sleep(ctx, min(every, remaining)); err != nil {
			lr.note(last.ID, reasonInterrupted)
			return nil
		}
		every = min(every*2, maxEvery)
		next, captured, err := r.d.Replay.Resend(ctx, call, attempt+1, last)
		if next.Action != nil {
			lr.attempts = append(lr.attempts, ev.Action.ID)
			lr.rep.Steps[last.ID] = next
			// The values of this step are the last attempt's, so a value it did not capture is gone.
			for _, c := range lr.leg.Steps[len(lr.leg.Steps)-1].Captures {
				delete(lr.rep.Captured, c.Name)
			}
			maps.Copy(lr.rep.Captured, captured)
			ev = next
		}
		switch {
		case err != nil && ctx.Err() != nil:
			lr.note(last.ID, reasonInterrupted)
			return nil
		case err != nil:
			lr.note(last.ID, fmt.Sprintf("step %s could not be sent or recorded", last.ID))
			return err
		case !answered(ev.Action):
			return nil
		case ev.Action.Response.Status > 399:
			// The condition did not hold, and it will not be read again: the leg fails even when its
			// expectations are met by the error answer.
			lr.waited = fmt.Sprintf("wait ended: answered %d", ev.Action.Response.Status)
			return nil
		}
	}
}

func (lr *legRun) note(step domain.StepID, text string) {
	lr.rep.Notes = append(lr.rep.Notes, domain.Reason{Rule: domain.OutcomeInconclusive, Text: text, Step: step})
}

func answered(a *domain.Action) bool {
	return a != nil && a.Response != nil && a.Response.Status >= 100 && a.Refused == "" && a.Err == ""
}

// judgeAll decides every leg with the end builds, or with the start builds when the end could not
// be read: Decide then makes the journey stale.
func (r *Runner) judgeAll(runs []*legRun, start domain.Manifest, end *domain.Manifest) ([]domain.LegResult, []domain.ScenarioResult) {
	endBuilds := start.Services
	if end != nil {
		endBuilds = end.Services
	}
	legs := make([]domain.LegResult, len(runs))
	results := make([]domain.ScenarioResult, len(runs))
	for i, lr := range runs {
		res := r.judge(lr, start.Services, endBuilds)
		results[i] = res
		legs[i] = domain.LegResult{Leg: lr.leg.ID, Scenario: res.Scenario, Outcome: res.Outcome, Reasons: slices.Clone(res.Reasons), As: ActedAs(lr.sc)}
	}
	return legs, results
}

// judge is the verdict of a leg, then the wait rule (D9) and the output rule.
func (r *Runner) judge(lr *legRun, start, end []domain.BuildRef) domain.ScenarioResult {
	if lr.notRun != "" {
		return verdict.Compute(verdict.Input{Scenario: lr.sc, NotRun: lr.notRun, BuildsStart: start, BuildsEnd: end})
	}
	res := verdict.Compute(verdict.Input{
		Scenario: lr.sc, Steps: lr.rep.Steps,
		BuildsStart: start, BuildsEnd: end, Blocked: lr.rep.Blocked,
	})
	res.Reasons = append(res.Reasons, lr.rep.Notes...)
	// A step that was never sent has no recorded result, and that is a consequence. The reason that
	// stopped the leg, such as a screens file that nobody approved, comes first.
	slices.SortStableFunc(res.Reasons, func(a, b domain.Reason) int {
		return boolInt(unsent(a)) - boolInt(unsent(b))
	})
	if len(lr.attempts) > 0 {
		at := len(res.Actions)
		if n := len(res.Actions); n > 0 {
			at = n - 1
		}
		res.Actions = slices.Insert(res.Actions, at, lr.attempts...)
	}
	if lr.waited != "" {
		at := slices.IndexFunc(res.Reasons, func(x domain.Reason) bool {
			return x.Rule != domain.OutcomeNotRun && x.Rule != domain.OutcomeBlocked && x.Rule != domain.OutcomeStale
		})
		if at < 0 {
			at = len(res.Reasons)
		}
		last := lr.sc.Steps[len(lr.sc.Steps)-1].ID
		res.Reasons = slices.Insert(res.Reasons, at, domain.Reason{Rule: domain.OutcomeFailed, Text: lr.waited, Step: last})
		if res.Outcome == domain.OutcomePassed {
			res.Outcome = domain.OutcomeFailed
		}
	}
	if len(lr.missing) > 0 && res.Outcome == domain.OutcomePassed {
		res.Outcome = domain.OutcomeBlocked
		var add []domain.Reason
		for _, name := range lr.missing {
			add = append(add, domain.Reason{Rule: domain.OutcomeBlocked, Text: fmt.Sprintf("output %s has no value", name)})
		}
		res.Reasons = append(add, res.Reasons...)
	}
	return res
}

// recorded is the redacted value of a capture, as the leg's recorded actions hold it. The last
// step that captured the name wins, as it does for the raw value.
func recorded(lr *legRun, name string) string {
	v := ""
	for _, st := range lr.sc.Steps {
		if ev, ok := lr.rep.Steps[st.ID]; ok && ev.Action != nil {
			if c, ok := ev.Action.Captures[name]; ok {
				v = c
			}
		}
	}
	return v
}

// evalInvariants compares the raw values of each invariant. The values in the result are the
// redacted ones, so a secret is never shown and never compared.
func evalInvariants(j domain.Journey, raw, shown map[string]string) []domain.InvariantResult {
	out := make([]domain.InvariantResult, 0, len(j.Invariants))
	for i, inv := range j.Invariants {
		ir := domain.InvariantResult{Index: i, Values: make([]string, len(inv.Equal))}
		values := make([]string, 0, len(inv.Equal))
		problem := ""
		for k, e := range inv.Equal {
			m := oneVariable.FindStringSubmatch(e)
			if m == nil {
				problem = cmp.Or(problem, fmt.Sprintf("%s is not one variable", e))
				continue
			}
			name := m[1] + "." + m[2]
			v, ok := raw[name]
			ir.Values[k] = shown[name]
			switch {
			case !ok:
				problem = cmp.Or(problem, fmt.Sprintf("{{%s}} was not captured", name))
			case redact.HasMarker(v) || redact.HasMarker(shown[name]):
				problem = cmp.Or(problem, "a value holds a redaction marker")
			}
			values = append(values, v)
		}
		if problem == "" {
			equal, ok := check.Same(values)
			switch {
			case !ok:
				problem = "the values cannot be compared exactly"
			case equal:
				ir.State = domain.CheckTrue
			default:
				ir.State = domain.CheckFalse
			}
		}
		if problem != "" {
			ir.State, ir.Detail = domain.CheckNotEvaluated, problem
		}
		out = append(out, ir)
	}
	return out
}

func isLeg(j domain.Journey, id string) bool {
	return slices.ContainsFunc(j.Legs, func(l domain.Leg) bool { return string(l.ID) == id })
}

// needed is every leg that leg needs, directly or through other legs.
func needed(j domain.Journey, leg domain.LegID) []domain.LegID {
	byID := map[domain.LegID]domain.Leg{}
	for _, l := range j.Legs {
		byID[l.ID] = l
	}
	var out []domain.LegID
	todo := slices.Clone(byID[leg].Needs)
	for len(todo) > 0 {
		n := todo[0]
		todo = todo[1:]
		if slices.Contains(out, n) {
			continue
		}
		out = append(out, n)
		todo = append(todo, byID[n].Needs...)
	}
	return out
}

func unsent(r domain.Reason) bool {
	return r.Rule == domain.OutcomeInconclusive && strings.HasSuffix(r.Text, "has no recorded result")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
