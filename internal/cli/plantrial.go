package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/openapi"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/secret"
	"github.com/jancuk/semar/internal/stage/plan"
	"github.com/jancuk/semar/internal/trial"
)

const (
	trialRequests       = 50
	trialRequestTimeout = 10 * time.Second
	baseDocumentLimit   = 5 << 20
	trialSkipped        = "skipped"
)

// trialRig is the trial of one plan: the run that holds its requests, and the runner that sends them.
type trialRig struct {
	runner  *trialRunner
	run     *evidence.Run
	id      domain.RunID
	env     string
	kind    string
	started time.Time
}

// trialRunner keeps the builds of the latest trial, so that the run of a plan that no draft survived
// still names the build that the environment ran, and why the latest trial sent nothing.
type trialRunner struct {
	*trial.Runner
	builds  []domain.BuildRef
	skipped string
}

func (t *trialRunner) Try(ctx context.Context, scenarios []domain.Scenario, scripts []domain.Script) (plan.Trial, error) {
	tr, err := t.Runner.Try(ctx, scenarios, scripts)
	if len(tr.Builds) > 0 {
		t.builds = tr.Builds
	}
	t.skipped = tr.Skipped
	if err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.skipped = "the trial stopped: " + err.Error() // as plan.Run records it
	}
	return tr, err
}

// planTrial decides whether the plan tries its read-only steps, and builds what the trial needs. A
// trial that cannot run is one line on standard error and a reason for plan.md, and the plan goes on
// without it (T6). cat is nil when the repository has no fixture catalogue. It runs before the status
// line starts, so no line of its own breaks the status line.
func planTrial(ctx context.Context, stderr io.Writer, o options, f planFlags, cfg config.Config, envName string, env config.Environment, in *plan.Input, cat *fixture.Catalogue) *trialRig {
	skip := func(why string) *trialRig {
		why = oneLine(terminalSafe(why))
		fmt.Fprintf(stderr, "semar: trial run skipped: %s\n", why)
		in.TrialOff = trialSkipped + ": " + why
		return nil
	}
	switch {
	case f.noTrial:
		in.TrialOff = "turned off with --no-trial"
		return nil
	case !cfg.Plan.TrialOn():
		in.TrialOff = "turned off by plan.trial in semar.yaml"
		return nil
	case env.Kind == config.KindProduction && f.env == "":
		return skip(envName + " is a production environment; name it with --env to try the reads against it")
	}

	secrets := secret.NewEnv(o.LookupEnv)
	needs := map[string]string{in.AuthProfile: "the trial run"}
	if cat != nil {
		for _, fx := range cat.Fixtures {
			addServiceProfiles(needs, env, fx, "of fixture "+string(fx.ID))
		}
	}
	// One profile at a time, so that the line names the profile that has no account: a fixture's
	// service may use another profile than the plan.
	sessions := map[string]session{}
	for _, name := range slices.Sorted(maps.Keys(needs)) {
		got, err := loadSessions(ctx, cfg, secrets, map[string]string{name: needs[name]})
		if err != nil {
			return skip(fmt.Sprintf("no account for %s: %v", name, err))
		}
		maps.Copy(sessions, got)
	}
	known := sessionSecrets(sessions)

	store, err := evidence.Open(semarDir(f.dir))
	if err != nil {
		return skip(err.Error())
	}
	key, err := store.RedactKey()
	if err != nil {
		return skip(err.Error())
	}
	red, err := redact.New(cfg.Redact, func() []string { return append(secrets.Loaded(), known...) }, key)
	if err != nil {
		return skip(err.Error())
	}
	// D1: the trial has its own policy, whatever mode the environment has for a run.
	readOnly := env
	readOnly.Effects.Mode = "read_only"
	policy, err := effects.New(readOnly)
	if err != nil {
		return skip(err.Error())
	}
	id := o.RunID()
	run, err := store.NewRun(id)
	if err != nil {
		return skip(err.Error())
	}
	rec := recorder.New(run, red, func(method string, u *url.URL) effects.Class {
		return policy.Classify(effects.Request{Method: method, URL: u})
	}, id)

	services := make(map[string]string, len(env.Services))
	var probed []string
	auth := map[string]string{}
	for _, s := range env.Services {
		services[s.Name] = s.BaseURL
		if s.Build != nil {
			probed = append(probed, s.Name)
		}
		if s.AuthProfile != "" {
			auth[s.Name] = s.AuthProfile
		}
	}
	timeout := o.TrialTimeout
	if timeout <= 0 {
		timeout = trialRequestTimeout
	}
	engine := api.New(api.Options{
		Policy:   policy,
		Recorder: rec,
		Services: services,
		Session: func(_ context.Context, profile string) (string, string, error) {
			s, ok := sessions[profile]
			if !ok {
				return "", "", fmt.Errorf("auth profile %s has no session", profile)
			}
			return s.header, s.value, nil
		},
		Run:     id,
		Version: Version,
		Timeout: timeout,
		Budget:  trialRequests,
		Clock:   o.Clock,
	})

	deps := trial.Deps{
		Engine:      engine,
		Policy:      policy,
		Builds:      build.NewDeclared(nil, build.NewHTTP(env.Services, nil)),
		Probed:      probed,
		Services:    services,
		AuthProfile: in.AuthProfile,
		Env:         envName,
		Run:         id,
		// The time limit is real elapsed time. The injected clock stamps evidence and may jump.
		Clock: time.Now,
		Limit: o.TrialLimit,
	}
	if cat != nil && len(cat.Fixtures) > 0 {
		// No leases: a find takes none, and the person's home is never opened for a trial.
		deps.Finder = fixture.New(fixture.Deps{
			Engine:      engine,
			Policy:      policy,
			Catalogue:   *cat,
			Approvals:   confirm.NewLocal(store.Root()),
			Health:      fixture.NewHealthStore(store.Root()),
			Environment: envName,
			Services:    services,
			ServiceAuth: auth,
			Data:        env.Data,
			Run:         id,
		})
	}
	rig := &trialRig{runner: &trialRunner{Runner: trial.New(deps)}, run: run, id: id, env: envName, kind: env.Kind, started: o.Clock()}
	in.Trial = rig.runner
	return rig
}

// close ends the run that holds the trial's requests, whether the plan was accepted or not (D10).
// The run has no result, so it is never exported or counted. A failure is a line and nothing more.
func (t *trialRig) close(stderr io.Writer, now time.Time) {
	// A trial that stopped while it ran gets the one skip line of T6. The status line is down by now.
	if t.runner.skipped != "" {
		fmt.Fprintf(stderr, "semar: trial run skipped: %s\n", oneLine(terminalSafe(t.runner.skipped)))
	}
	builds := slices.Clone(t.runner.builds)
	if builds == nil {
		builds = []domain.BuildRef{}
	}
	rec := domain.Run{
		ID: t.id, Environment: t.env, EnvironmentKind: t.kind, Purpose: domain.PurposeTrial,
		StartedAt: t.started, EndedAt: now, BuildsStart: builds, Results: []domain.ScenarioResult{},
	}
	if err := t.run.Close(rec); err != nil {
		fmt.Fprintf(stderr, "semar: the trial run %s could not be closed: %s\n", t.id, oneLine(terminalSafe(err.Error())))
	}
}

// shower is the part of vcs.Git that reads a file at a commit.
type shower interface {
	Show(ctx context.Context, commit, path string, limit int64) ([]byte, error)
}

// baseDocuments are the API documents as they were at the merge base. A document that the change
// did not touch is the head document. A changed one is read from git, and one that cannot be read
// or parsed is left out with no line: the plan then judges a 404 with fewer documents (D5).
func baseDocuments(ctx context.Context, g shower, cs domain.ChangeSet, sources []knowledge.Source) []openapi.Document {
	var docs []openapi.Document
	for _, s := range sources {
		if !s.Usable() || s.API == nil {
			continue
		}
		if !slices.ContainsFunc(cs.Files, func(fc domain.FileChange) bool { return fc.Path == s.Path || fc.OldPath == s.Path }) {
			docs = append(docs, *s.API)
			continue
		}
		data, err := g.Show(ctx, cs.MergeBase, s.Path, baseDocumentLimit)
		if err != nil {
			continue
		}
		doc, err := openapi.Parse(data, s.Path)
		if err != nil {
			continue
		}
		docs = append(docs, doc)
	}
	return docs
}

// trialSummary is the line that follows the plan on standard output. It is empty when the person
// turned the trial off.
func trialSummary(in plan.Input, out plan.Output) string {
	switch {
	case out.Trial == nil && !strings.HasPrefix(in.TrialOff, trialSkipped):
		return ""
	case out.Trial == nil, out.Trial.Skipped != "":
		return "trial: skipped"
	}
	c := out.TrialCounts(in)
	return fmt.Sprintf("tried: %d of %d steps, %d problems returned, %d for a second look", c.Sent, c.Steps, c.Back, c.SecondLook)
}
