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
	"github.com/jancuk/semar/internal/engine/web"
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
	scenarios  []string
	all        bool
	noJourneys bool
	noPlan     bool
	env        string
	builds     []string
	base       string
	dir        string
	headed     bool
	// done is told how the scenarios of the run ended, and where the report is. semar record uses it to
	// say how the replay of a new recording went.
	done func(run domain.Run, report string)
}

func newRunCommand(o options) *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run [environment]",
		Short: "Run the tests of the change against an environment, and say where it stands",
		Long: "Run every scenario whose ticket is a ticket of the change, then every journey with criticality critical, and end with the status of the change.\n" +
			"The change is the commits from the base branch to HEAD. --all runs every scenario and every critical journey, and --scenario <id> runs the scenarios named and no journey.\n" +
			"A ticket of the change that has a ticket file and no use cases is not planned. On a terminal, run offers to plan it and asks first. With no terminal, or with --no-plan, it never plans, and its exit code is at least 2.\n" +
			"Exit 0 when everything that ran passed, 1 when something failed, and 2 otherwise.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if f.env != "" {
					return errors.New("name the environment once: as an argument or with --env")
				}
				f.env = args[0]
			}
			return runDaily(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
		},
	}
	cmd.Flags().StringArrayVar(&f.scenarios, "scenario", nil, "run the scenario with this ID, and no journey; repeat for more")
	cmd.Flags().BoolVar(&f.all, "all", false, "run every scenario and every critical journey, not only those of the change")
	cmd.Flags().BoolVar(&f.noJourneys, "no-journeys", false, "leave the critical journeys out")
	cmd.Flags().BoolVar(&f.noPlan, "no-plan", false, "never offer to plan a ticket that has no use cases")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment of semar.yaml to run against (the same as the argument); needed when there are several, and always for a production environment")
	cmd.Flags().StringArrayVar(&f.builds, "build", nil, "the commit that a service with no build probe runs, as <commit> or <service>=<commit>; repeat for more")
	cmd.Flags().StringVar(&f.base, "base", "", "the branch that the change is measured against (default: change.base, else the default branch of origin, else main)")
	cmd.Flags().BoolVar(&f.headed, "headed", false, "show the browser window of a web step")
	addDirFlag(cmd, &f.dir)
	return cmd
}

// finishedRun is a run that was written to the evidence store. An interrupted run has no run.json.
type finishedRun struct {
	record      domain.Run
	reportPath  string
	interrupted bool
	coverage    string // the use case line of the report
}

// executeScenarios replays the scenarios against one environment, writes the report, and closes the
// run. It prints nothing but the line that names the target on stderr.
func executeScenarios(ctx context.Context, stderr io.Writer, o options, dir string, cfg config.Config, envName string, env config.Environment, declared map[string]string, scenarios []domain.Scenario, headed bool, announce func()) (finishedRun, error) {
	cat, err := runCatalogue(stderr, dir, cfg, scenarios)
	if err != nil {
		return finishedRun{}, err
	}
	fmt.Fprintf(stderr, "semar: running against %s (%s, %s)\n", envName, env.Kind, effectsWords(env.Effects.Mode))
	r, err := newReplay(ctx, o, dir, cfg, envName, env, declared, scenarios, cat, headed)
	if err != nil {
		return finishedRun{}, err
	}
	defer r.web.Close()
	announce()
	record := domain.Run{ID: r.id, Environment: envName, EnvironmentKind: env.Kind, StartedAt: o.Clock()}
	results, start, end, runErr := r.runner.Run(ctx, scenarios)
	interrupted := ctx.Err() != nil
	if runErr != nil && !interrupted {
		return finishedRun{}, fmt.Errorf("run %s stopped: %w; its evidence may be incomplete, so it reports no outcome", r.id, runErr)
	}
	record.BuildsStart, record.BuildsEnd, record.Results = start, end, results
	if !interrupted {
		record.EndedAt = o.Clock()
	}

	acts, err := r.store.Activity(r.id)
	if err != nil {
		return finishedRun{}, err
	}
	byID := make(map[domain.ScenarioID]domain.Scenario, len(scenarios))
	for _, sc := range scenarios {
		byID[sc.ID] = sc
	}
	coverage, tickets := runCoverage(stderr, o, dir, scenarios, record)
	writeReport := func(rec domain.Run) error {
		var md bytes.Buffer
		if err := report.Markdown(&md, report.Data{Run: rec, Scenarios: byID, Activity: acts, Coverage: coverage, CoverageTickets: tickets}); err != nil {
			return err
		}
		return r.run.WriteFile(reportFile, md.Bytes())
	}
	if err := writeReport(record); err != nil {
		return finishedRun{}, err
	}
	// An interrupted run keeps no run.json, so that it loads as interrupted.
	if !interrupted {
		if err := r.run.Close(record); err != nil {
			// The report above says the run finished. Without a run.json it did not, so say so.
			unfinished := record
			unfinished.EndedAt = time.Time{}
			if werr := writeReport(unfinished); werr != nil {
				return finishedRun{}, errors.Join(err, fmt.Errorf("rewrite %s as unfinished: %w", reportFile, werr))
			}
			return finishedRun{}, err
		}
	}
	return finishedRun{record: record, reportPath: filepath.Join(r.store.Root(), "runs", string(r.id), reportFile), interrupted: interrupted, coverage: coverage}, nil
}

// effectsWords says what a test may change in an environment of this effects mode.
func effectsWords(mode string) string {
	if mode == "read_only" {
		return "read only"
	}
	return "test data"
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
	secrets  *secret.Env
	recorder *recorder.Recorder
	services map[string]string
	id       domain.RunID
	store    *evidence.Store
	run      *evidence.Run
	policy   *effects.Policy
	engine   *api.Engine
	builds   build.Prober
	probed   []string // services that have a build to read
}

type replayRig struct {
	*envRig
	runner *replay.Runner
	web    *web.Engine
}

// newReplay builds every dependency of one run. The secrets come before anything
// is created or sent, so a missing secret leaves no trace. The redactor follows
// the store, which holds its key. cat is nil when no scenario names a fixture.
func newReplay(ctx context.Context, o options, repo string, cfg config.Config, envName string, env config.Environment, declared map[string]string, scenarios []domain.Scenario, cat *fixture.Catalogue, headed bool) (*replayRig, error) {
	rg, err := newEnvRig(ctx, o, repo, cfg, env, declared, sessionNeeds(cfg, env, scenarios, cat))
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
		Accounts: slices.Sorted(maps.Keys(cfg.AuthProfiles)),
	}
	if cat != nil {
		fx, err := rg.resolver(o, envName, env, *cat)
		if err != nil {
			return nil, err
		}
		deps.Fixtures = fx
	}
	eng := rg.webEngine(o, envName, env, headed)
	deps.Web, deps.Screens = eng, screenSource{repo: repo, approvals: confirm.NewLocal(rg.store.Root()), trusted: trustedScreens(ctx)}
	return &replayRig{envRig: rg, runner: replay.New(deps), web: eng}, nil
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
		secrets: secrets, recorder: rec, services: services,
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
	services := make(map[string]string, len(env.Services))
	auth := map[string]string{}
	for _, s := range env.Services {
		services[s.Name] = s.BaseURL
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
		Services:    services,
		ServiceAuth: auth,
		Data:        env.Data,
		Run:         rg.id,
	}), nil
}

// sessionNeeds maps each auth profile that a run uses to the first reason it needs it: a scenario,
// or the service of a fixture that a scenario names.
func sessionNeeds(cfg config.Config, env config.Environment, scenarios []domain.Scenario, cat *fixture.Catalogue) map[string]string {
	needs := map[string]string{}
	for _, sc := range scenarios {
		if sc.AuthProfile != "" {
			needs[sc.AuthProfile] = "scenario " + string(sc.ID)
		}
	}
	// A step that acts as an account the configuration lacks is not a reason to stop the run: the
	// runner blocks that scenario, with the account's name.
	for _, sc := range scenarios {
		for _, st := range sc.Steps {
			if _, known := cfg.AuthProfiles[st.As]; known && st.As != "" {
				if _, ok := needs[st.As]; !ok {
					needs[st.As] = "step " + string(st.ID) + " of scenario " + string(sc.ID)
				}
			}
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
