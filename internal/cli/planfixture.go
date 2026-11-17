package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/stage/plan"
)

// fixtureRig runs a fixture that a plan drafted, once, on the test environment, and cleans up. It
// has its own run in the evidence store, with the purpose of a fixture check, so that every request is
// admitted by the effects policy and recorded like a request of a scenario.
type fixtureRig struct {
	o       options
	dir     string
	cfg     config.Config
	envName string
	env     config.Environment
	profile string // the account that the scenarios act as
	skip    string // why no fixture is run; "" when it is

	mu      sync.Mutex
	rg      *envRig
	started time.Time
	failed  string
}

// planFixtureTrial decides whether the plan runs the fixtures that it drafts. A fixture is run on a
// test environment whose effects policy allows writes, unless --no-trial or plan.trial turned the
// trial off. The answer is a rig that says why when it runs nothing, so plan.md can say it.
func planFixtureTrial(o options, f planFlags, cfg config.Config, envName string, env config.Environment, profile string) *fixtureRig {
	t := &fixtureRig{o: o, dir: f.dir, cfg: cfg, envName: envName, env: env, profile: profile}
	switch {
	case f.noTrial:
		t.skip = "turned off with --no-trial"
	case !cfg.Plan.TrialOn():
		t.skip = "turned off by plan.trial in semar.yaml"
	case env.Kind == config.KindProduction:
		t.skip = envName + " is a production environment"
	default:
		if policy, err := effects.New(env); err != nil {
			t.skip = err.Error()
		} else if policy.ReadOnly() {
			t.skip = envName + " allows only reads, and a fixture writes"
		}
	}
	return t
}

// approved says that the fixture of a trial counts as approved: the person has not seen it yet, and
// this run is what the plan does before the person looks.
type approved struct{}

func (approved) FixtureApproved(domain.FixtureID, domain.Hash) (bool, error) { return true, nil }

// open builds the run on the first fixture. Every session that a fixture step may need is loaded
// here, so that a missing account is one reason and not one for each fixture.
func (t *fixtureRig) open(ctx context.Context) (*envRig, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rg != nil || t.failed != "" {
		return t.rg, t.failed
	}
	needs := map[string]string{t.profile: "the run of a drafted fixture"}
	for _, s := range t.env.Services {
		if s.AuthProfile != "" {
			needs[s.AuthProfile] = "service " + s.Name + " of a drafted fixture"
		}
	}
	rg, err := newEnvRig(ctx, t.o, t.dir, t.cfg, t.env, nil, needs)
	if err != nil {
		t.failed = "the run could not start: " + oneLine(terminalSafe(err.Error()))
		return nil, t.failed
	}
	t.rg, t.started = rg, t.o.Clock()
	return rg, ""
}

// TryFixture sends the create steps of the fixture, and then its cleanup. A step that the environment
// refuses, or whose request the effects policy does not admit, is a problem for the model to repair.
func (t *fixtureRig) TryFixture(ctx context.Context, fx domain.Fixture) (plan.FixtureTrial, error) {
	if t.skip != "" {
		return plan.FixtureTrial{Skipped: t.skip}, nil
	}
	rg, why := t.open(ctx)
	if rg == nil {
		return plan.FixtureTrial{Skipped: why}, nil
	}
	services := make(map[string]string, len(t.env.Services))
	auth := map[string]string{}
	for _, s := range t.env.Services {
		services[s.Name] = s.BaseURL
		if s.AuthProfile != "" {
			auth[s.Name] = s.AuthProfile
		}
	}
	resolver := fixture.New(fixture.Deps{
		Engine: rg.engine, Policy: rg.policy, Catalogue: fixture.Catalogue{Fixtures: []domain.Fixture{fx}},
		Approvals: approved{}, Environment: t.envName, Services: services, ServiceAuth: auth, Data: t.env.Data, Run: rg.id,
	})
	res, err := resolver.Check(ctx, fx.ID, t.profile)
	if err != nil {
		return plan.FixtureTrial{}, err
	}
	out := plan.FixtureTrial{Ran: res.Checked, Requests: len(res.Actions), Left: slices.Clone(res.Left)}
	rec := res.Record
	switch {
	case !res.Checked:
		out.Problems = []string{"trial: " + oneLine(terminalSafe(res.Note))}
	case !rec.Healthy:
		out.Problems = []string{"trial: " + oneLine(terminalSafe(rec.Reason))}
	case rec.Cleanup == fixture.CleanupFailed:
		out.Problems = []string{"trial: the cleanup of the fixture failed, so what it made may still exist: " + oneLine(terminalSafe(strings.Join(res.Left, "; ")))}
	default:
		out.CleanedUp = rec.Cleanup == fixture.CleanupDone
	}
	return out, nil
}

// close ends the run, if one was opened. The run has no result, so it is never exported or counted.
func (t *fixtureRig) close(stderr io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rg == nil {
		return
	}
	rec := domain.Run{
		ID: t.rg.id, Environment: t.envName, EnvironmentKind: t.env.Kind, Purpose: domain.PurposeFixtureCheck,
		StartedAt: t.started, EndedAt: t.o.Clock(), BuildsStart: []domain.BuildRef{}, Results: []domain.ScenarioResult{},
	}
	if err := t.rg.run.Close(rec); err != nil {
		fmt.Fprintf(stderr, "semar: the run %s of the drafted fixtures could not be closed: %s\n", t.rg.id, oneLine(terminalSafe(err.Error())))
	}
}
