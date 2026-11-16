package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/replay"
	"github.com/jancuk/semar/internal/report"
	"github.com/jancuk/semar/internal/secret"
)

const reportFile = "report.md"

type runFlags struct {
	scenarios []string
	all       bool
	env       string
	builds    []string
	dir       string
}

func newRunCommand(o options) *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run (--scenario <id>... | --all)",
		Short: "Replay scenarios against an environment and write a report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScenarios(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
		},
	}
	cmd.Flags().StringArrayVar(&f.scenarios, "scenario", nil, "the ID of a scenario to run; repeat for more")
	cmd.Flags().BoolVar(&f.all, "all", false, "run every scenario in .semar/scenarios")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment of semar.yaml to run against; needed when there are several, and always for a production environment")
	cmd.Flags().StringArrayVar(&f.builds, "build", nil, "the commit that a service with no build probe runs, as <commit> or <service>=<commit>; repeat for more")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func runScenarios(ctx context.Context, stdout, stderr io.Writer, o options, f runFlags) error {
	if (len(f.scenarios) > 0) == f.all {
		return errors.New("name the scenarios with --scenario <id>, or use --all, and not both")
	}
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	envName, env, err := cfg.Environment(f.env)
	if err != nil {
		return err
	}
	if env.Kind == config.KindProduction && f.env == "" {
		return fmt.Errorf("the environment %q is a production environment; name it with --env %s to run against it", envName, envName)
	}
	declared, err := build.ParseDeclared(f.builds, env.Services)
	if err != nil {
		return err
	}
	scenarios, err := selectScenarios(f)
	if err != nil {
		return err
	}
	if len(scenarios) == 0 {
		fmt.Fprintf(stdout, "no scenario ran: there is no scenario file in %s\n", scenariosDir(f.dir))
		return exitError{ExitUndecided}
	}

	cat, err := runCatalogue(stderr, f.dir, cfg, scenarios)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "semar: running against %s (%s, %s)\n", envName, env.Kind, effectsWords(env.Effects.Mode))
	r, err := newReplay(ctx, o, f.dir, cfg, envName, env, declared, scenarios, cat)
	if err != nil {
		return err
	}
	record := domain.Run{ID: r.id, Environment: envName, EnvironmentKind: env.Kind, StartedAt: o.Clock()}
	results, start, end, runErr := r.runner.Run(ctx, scenarios)
	interrupted := ctx.Err() != nil
	if runErr != nil && !interrupted {
		return fmt.Errorf("run %s stopped: %w; its evidence may be incomplete, so it reports no outcome", r.id, runErr)
	}
	record.BuildsStart, record.BuildsEnd, record.Results = start, end, results
	if !interrupted {
		record.EndedAt = o.Clock()
	}

	acts, err := r.store.Activity(r.id)
	if err != nil {
		return err
	}
	byID := make(map[domain.ScenarioID]domain.Scenario, len(scenarios))
	for _, sc := range scenarios {
		byID[sc.ID] = sc
	}
	writeReport := func(rec domain.Run) error {
		var md bytes.Buffer
		if err := report.Markdown(&md, report.Data{Run: rec, Scenarios: byID, Activity: acts}); err != nil {
			return err
		}
		return r.run.WriteFile(reportFile, md.Bytes())
	}
	if err := writeReport(record); err != nil {
		return err
	}
	// An interrupted run keeps no run.json, so that it loads as interrupted.
	if !interrupted {
		if err := r.run.Close(record); err != nil {
			// The report above says the run finished. Without a run.json it did not, so say so.
			unfinished := record
			unfinished.EndedAt = time.Time{}
			if werr := writeReport(unfinished); werr != nil {
				return errors.Join(err, fmt.Errorf("rewrite %s as unfinished: %w", reportFile, werr))
			}
			return err
		}
	}

	if err := report.Summary(stdout, record, ""); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "report: %s\n", filepath.Join(r.store.Root(), "runs", string(r.id), reportFile))
	if interrupted {
		fmt.Fprintf(stderr, "semar: the run was interrupted: run %s is incomplete\n", r.id)
		return exitError{ExitUndecided}
	}
	if code := report.ExitCode(results); code != ExitOK {
		return exitError{code}
	}
	return nil
}

// effectsWords says what a test may change in an environment of this effects mode.
func effectsWords(mode string) string {
	if mode == "read_only" {
		return "read only"
	}
	return "test data"
}

func selectScenarios(f runFlags) ([]domain.Scenario, error) {
	if f.all {
		return loadAllScenarios(f.dir)
	}
	var scs []domain.Scenario
	seen := map[domain.ScenarioID]bool{}
	for _, raw := range f.scenarios {
		id := domain.ScenarioID(raw)
		if seen[id] {
			continue
		}
		seen[id] = true
		sc, err := loadScenario(f.dir, id)
		if err != nil {
			return nil, err
		}
		scs = append(scs, sc)
	}
	return scs, nil
}

// runCatalogue loads the fixtures for a run whose scenarios name any. It is nil for a run that
// names none, so that a repository with no fixtures never reads .semar/fixtures. A file that is not
// valid stays out of the catalogue, and a scenario that names it is blocked as missing.
func runCatalogue(stderr io.Writer, repo string, cfg config.Config, scenarios []domain.Scenario) (*fixture.Catalogue, error) {
	if !slices.ContainsFunc(scenarios, func(sc domain.Scenario) bool { return len(sc.Given) > 0 }) {
		return nil, nil
	}
	cat, err := fixture.Load(repo, allServices(cfg))
	if err != nil {
		return nil, err
	}
	for _, p := range cat.Problems {
		fmt.Fprintf(stderr, "semar: fixture file %s: %s; this run does not use it\n", terminalSafe(p.File), terminalSafe(oneLine(problemText(p))))
	}
	return &cat, nil
}

// problemText is what a fixture problem says without its file.
func problemText(p fixture.Problem) string {
	if p.Path == "" {
		return p.Message
	}
	return p.Path + ": " + p.Message
}

// allServices lists the services of every environment, for the rule that a fixture names a service
// that some environment has.
func allServices(cfg config.Config) []string {
	var names []string
	for _, env := range cfg.Environments {
		for _, s := range env.Services {
			names = append(names, s.Name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// envRig is what a run against one environment needs before it sends anything: the evidence run,
// the effects policy, the recorder, and the engine that every request goes through.
type envRig struct {
	id     domain.RunID
	store  *evidence.Store
	run    *evidence.Run
	policy *effects.Policy
	engine *api.Engine
	builds build.Prober
	probed []string // services that have a build to read
}

type replayRig struct {
	*envRig
	runner *replay.Runner
}

// newReplay builds every dependency of one run. The secrets come before anything
// is created or sent, so a missing secret leaves no trace. The redactor follows
// the store, which holds its key. cat is nil when no scenario names a fixture.
func newReplay(ctx context.Context, o options, repo string, cfg config.Config, envName string, env config.Environment, declared map[string]string, scenarios []domain.Scenario, cat *fixture.Catalogue) (*replayRig, error) {
	rg, err := newEnvRig(ctx, o, repo, cfg, env, declared, sessionNeeds(env, scenarios, cat))
	if err != nil {
		return nil, err
	}
	deps := replay.Deps{
		Engine:   rg.engine,
		Scripts:  rg.store,
		Builds:   rg.builds,
		Services: rg.probed,
		Confirm:  confirm.NewLocal(rg.store.Root()),
		Trust:    cfg.Policy.Trust.Confirm,
		Workers:  cfg.Replay.Workers,
		Run:      rg.id,
	}
	if cat != nil {
		fx, err := rg.resolver(o, envName, env, *cat)
		if err != nil {
			return nil, err
		}
		deps.Fixtures = fx
	}
	return &replayRig{envRig: rg, runner: replay.New(deps)}, nil
}

// newEnvRig loads the sessions of the profiles named in needs, which maps a profile to whoever
// needs it, and builds the engine of one run.
func newEnvRig(ctx context.Context, o options, repo string, cfg config.Config, env config.Environment, declared map[string]string, needs map[string]string) (*envRig, error) {
	secrets := secret.NewEnv(o.LookupEnv)
	sessions, err := loadSessions(ctx, cfg, secrets, needs)
	if err != nil {
		return nil, err
	}
	// A session value is a secret even when it has no placeholder, so the redactor gets every one.
	known := sessionSecrets(sessions)

	store, err := evidence.Open(semarDir(repo))
	if err != nil {
		return nil, err
	}
	key, err := store.RedactKey()
	if err != nil {
		return nil, err
	}
	red, err := redact.New(cfg.Redact, func() []string { return append(secrets.Loaded(), known...) }, key)
	if err != nil {
		return nil, fmt.Errorf("semar.yaml: %w", err)
	}

	policy, err := effects.New(env)
	if err != nil {
		return nil, fmt.Errorf("semar.yaml: %w", err)
	}

	id := o.RunID()
	run, err := store.NewRun(id)
	if err != nil {
		return nil, err
	}
	classify := func(method string, u *url.URL) effects.Class {
		return policy.Classify(effects.Request{Method: method, URL: u})
	}
	rec := recorder.New(run, red, classify, id)

	services := make(map[string]string, len(env.Services))
	var probed []string
	for _, s := range env.Services {
		services[s.Name] = s.BaseURL
		if _, ok := declared[s.Name]; ok || s.Build != nil {
			probed = append(probed, s.Name)
		}
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
		Clock:   o.Clock,
	})
	return &envRig{
		id: id, store: store, run: run, policy: policy, engine: engine, probed: probed,
		builds: build.NewDeclared(declared, build.NewHTTP(env.Services, nil)),
	}, nil
}

// resolver gives the fixtures of the catalogue through the engine of this run, so that every
// fixture request meets the same policy and recorder as a step. The leases live in the person's
// Semar home. A home that cannot be found leaves the leases out, and a fixture that needs one is
// then blocked with that reason.
func (rg *envRig) resolver(o options, envName string, env config.Environment, cat fixture.Catalogue) (*fixture.Resolver, error) {
	var leases *fixture.Leases
	if home, err := o.home(); err == nil {
		if leases, err = fixture.OpenLeases(home); err != nil {
			return nil, err
		}
	}
	names := make([]string, len(env.Services))
	auth := map[string]string{}
	for i, s := range env.Services {
		names[i] = s.Name
		if s.AuthProfile != "" {
			auth[s.Name] = s.AuthProfile
		}
	}
	return fixture.New(fixture.Deps{
		Engine:      rg.engine,
		Policy:      rg.policy,
		Catalogue:   cat,
		Approvals:   confirm.NewLocal(rg.store.Root()),
		Health:      fixture.NewHealthStore(rg.store.Root()),
		Leases:      leases,
		Environment: envName,
		EnvServices: names,
		ServiceAuth: auth,
		Data:        env.Data,
		Run:         rg.id,
	}), nil
}

// sessionNeeds maps each auth profile that a run uses to the first reason it needs it: a scenario,
// or the service of a fixture that a scenario names.
func sessionNeeds(env config.Environment, scenarios []domain.Scenario, cat *fixture.Catalogue) map[string]string {
	needs := map[string]string{}
	for _, sc := range scenarios {
		if sc.AuthProfile != "" {
			needs[sc.AuthProfile] = "scenario " + string(sc.ID)
		}
	}
	if cat == nil {
		return needs
	}
	for _, sc := range scenarios {
		for _, g := range sc.Given {
			if f, ok := cat.Get(g.Fixture); ok {
				addServiceProfiles(needs, env, f, "of fixture "+string(f.ID)+" for scenario "+string(sc.ID))
			}
		}
	}
	return needs
}

// addServiceProfiles adds the auth profile of every service that a step of the fixture calls.
func addServiceProfiles(needs map[string]string, env config.Environment, f domain.Fixture, why string) {
	for _, steps := range [][]domain.FixtureStep{f.Find, f.Create, f.Cleanup} {
		for _, st := range steps {
			for _, s := range env.Services {
				if s.Name != st.Request.Service || s.AuthProfile == "" {
					continue
				}
				if _, ok := needs[s.AuthProfile]; !ok {
					needs[s.AuthProfile] = "service " + s.Name + " " + why
				}
			}
		}
	}
}

type session struct{ header, value string }

// sessionSecrets lists what the redactor must hide of each session value: the whole value and, when it
// has a scheme such as "Bearer ", the part after the first space, so that a response that echoes only the token is hidden too.
func sessionSecrets(sessions map[string]session) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(sessions)) {
		v := sessions[name].value
		out = append(out, v)
		if _, rest, ok := strings.Cut(v, " "); ok {
			out = append(out, rest)
		}
	}
	return slices.DeleteFunc(out, func(v string) bool { return v == "" })
}

// loadSessions resolves every secret of each auth profile in needs, which maps a profile to whoever
// needs it, so the redactor knows all of them. Its errors name a reference, never a value.
func loadSessions(ctx context.Context, cfg config.Config, r secret.Resolver, needs map[string]string) (map[string]session, error) {
	sessions := make(map[string]session, len(needs))
	for _, name := range slices.Sorted(maps.Keys(needs)) {
		p, ok := cfg.AuthProfiles[name]
		switch {
		case !ok:
			return nil, fmt.Errorf("%s uses auth profile %s, which semar.yaml does not define", needs[name], name)
		case p.Session == nil || p.Session.Header == "":
			return nil, fmt.Errorf("auth profile %s in semar.yaml has no session header", name)
		}
		values := make(map[string]string, len(p.Secrets))
		for _, secretName := range slices.Sorted(maps.Keys(p.Secrets)) {
			v, err := r.Resolve(ctx, p.Secrets[secretName])
			if err != nil {
				return nil, fmt.Errorf("auth profile %s: secret %s: %w", name, secretName, err)
			}
			values[secretName] = v
		}
		value, err := secret.Expand(p.Session.Value, values)
		if err != nil {
			return nil, fmt.Errorf("auth profile %s: session value: %w", name, err)
		}
		sessions[name] = session{header: p.Session.Header, value: value}
	}
	return sessions, nil
}
