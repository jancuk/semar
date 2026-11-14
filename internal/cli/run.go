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
	dir       string
}

func newRunCommand(o options) *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run (--scenario <id>... | --all)",
		Short: "Replay scenarios against the test environment and write a report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScenarios(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
		},
	}
	cmd.Flags().StringArrayVar(&f.scenarios, "scenario", nil, "the ID of a scenario to run; repeat for more")
	cmd.Flags().BoolVar(&f.all, "all", false, "run every scenario in .semar/scenarios")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment of semar.yaml to run against; needed when there are several")
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
	scenarios, err := selectScenarios(f)
	if err != nil {
		return err
	}
	if len(scenarios) == 0 {
		fmt.Fprintf(stdout, "no scenario ran: there is no scenario file in %s\n", scenariosDir(f.dir))
		return exitError{ExitUndecided}
	}

	r, err := newReplay(ctx, o, f.dir, cfg, env, scenarios)
	if err != nil {
		return err
	}
	record := domain.Run{ID: r.id, Environment: envName, StartedAt: o.Clock()}
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

	if err := report.Summary(stdout, record); err != nil {
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

type replayRig struct {
	id     domain.RunID
	store  *evidence.Store
	run    *evidence.Run
	runner *replay.Runner
}

// newReplay builds every dependency of one run. The secrets come before anything
// is created or sent, so a missing secret leaves no trace. The redactor follows
// the store, which holds its key.
func newReplay(ctx context.Context, o options, repo string, cfg config.Config, env config.Environment, scenarios []domain.Scenario) (*replayRig, error) {
	secrets := secret.NewEnv(o.LookupEnv)
	sessions, err := loadSessions(ctx, cfg, secrets, scenarios)
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
		if s.Build != nil {
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

	runner := replay.New(replay.Deps{
		Engine:   engine,
		Scripts:  store,
		Builds:   build.NewHTTP(env.Services, nil),
		Services: probed,
		Confirm:  confirm.NewLocal(store.Root()),
		Trust:    cfg.Policy.Trust.Confirm,
		Workers:  cfg.Replay.Workers,
	})
	return &replayRig{id: id, store: store, run: run, runner: runner}, nil
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

// loadSessions resolves every secret of each auth profile the scenarios use, so the
// redactor knows all of them. Its errors name a reference, never a value.
func loadSessions(ctx context.Context, cfg config.Config, r secret.Resolver, scenarios []domain.Scenario) (map[string]session, error) {
	profiles := map[string]domain.ScenarioID{}
	for _, sc := range scenarios {
		if sc.AuthProfile != "" {
			profiles[sc.AuthProfile] = sc.ID
		}
	}
	sessions := make(map[string]session, len(profiles))
	for _, name := range slices.Sorted(maps.Keys(profiles)) {
		p, ok := cfg.AuthProfiles[name]
		switch {
		case !ok:
			return nil, fmt.Errorf("scenario %s uses auth profile %s, which semar.yaml does not define", profiles[name], name)
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
