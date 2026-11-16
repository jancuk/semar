package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/fixture"
)

func newFixtureCommand(o options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fixture",
		Short: "List the fixtures of the repository, or check that they still work",
	}
	cmd.AddCommand(newFixtureListCommand(), newFixtureCheckCommand(o))
	return cmd
}

type fixtureListFlags struct {
	env string
	dir string
}

func newFixtureListCommand() *cobra.Command {
	var f fixtureListFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the fixtures with their approval, their health, and the scenarios that use them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listFixtures(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f)
		},
	}
	cmd.Flags().StringVar(&f.env, "env", "", "show the health of each fixture on this environment, and check each fixture against its effects policy")
	addDirFlag(cmd, &f.dir)
	return cmd
}

// listFixtures prints one line per valid fixture, then every file that cannot be used. A missing
// semar.yaml is not an error here, because the list is also how a person finds out what is wrong
// with a fixture file.
func listFixtures(ctx context.Context, stdout, stderr io.Writer, f fixtureListFlags) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fixture list: %w", err)
	}
	cfg, err := config.Load(f.dir)
	if err != nil && !(errors.Is(err, fs.ErrNotExist) && f.env == "") {
		return err
	}
	cat, err := fixture.Load(f.dir, allServices(cfg))
	if err != nil {
		return err
	}

	var env config.Environment
	var policy *effects.Policy
	var health *fixture.HealthStore
	if f.env != "" {
		name, e, err := cfg.Environment(f.env)
		if err != nil {
			return err
		}
		f.env, env = name, e
		if policy, err = effects.New(env); err != nil {
			return fmt.Errorf("semar.yaml: %w", err)
		}
		health = fixture.NewHealthStore(semarDir(f.dir))
	}

	users := map[domain.FixtureID][]string{}
	scs, err := loadAllScenarios(f.dir)
	if err != nil {
		fmt.Fprintf(stderr, "semar: the scenarios could not be read, so the list does not say which use a fixture: %v\n", err)
	}
	for _, sc := range scs {
		for _, g := range sc.Given {
			if !slices.Contains(users[g.Fixture], string(sc.ID)) {
				users[g.Fixture] = append(users[g.Fixture], string(sc.ID))
			}
		}
	}

	approvals := confirm.NewLocal(semarDir(f.dir))
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, fx := range cat.Fixtures {
		approval := "not approved"
		if ok, err := approvals.FixtureApproved(fx.ID, domain.FixtureHash(fx)); err != nil {
			approval = "approval unreadable: " + oneLine(terminalSafe(err.Error()))
		} else if ok {
			approval = "approved"
		}
		cells := []string{string(fx.ID), fx.Version, fx.Owner, approval}
		if health != nil {
			cells = append(cells, healthWords(health, f.env, fx))
		}
		used := "used by no scenario"
		if ids := users[fx.ID]; len(ids) > 0 {
			slices.Sort(ids)
			used = "used by " + strings.Join(ids, ", ")
		}
		cells = append(cells, used)
		for i, c := range cells {
			cells[i] = oneLine(terminalSafe(c))
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(cat.Fixtures) == 0 && len(cat.Problems) == 0 {
		fmt.Fprintf(stdout, "there is no fixture file in %s\n", fixturesDir(f.dir))
	}
	for _, p := range cat.Problems {
		fmt.Fprintf(stdout, "invalid: %s\n", oneLine(terminalSafe(p.String())))
	}
	if policy != nil {
		services := map[string]string{}
		for _, s := range env.Services {
			services[s.Name] = s.BaseURL
		}
		for _, fx := range cat.Fixtures {
			for _, p := range fixture.CheckPolicy(fx, policy, services) {
				fmt.Fprintf(stdout, "policy of %s: %s: %s: %s\n", terminalSafe(f.env), fx.ID, terminalSafe(p.Path), oneLine(terminalSafe(p.Message)))
			}
		}
	}
	if len(cat.Problems) > 0 {
		return exitError{ExitUndecided}
	}
	return nil
}

func fixturesDir(repo string) string { return filepath.Join(semarDir(repo), "fixtures") }

// healthWords says what the last check of the fixture, as it is now, found on the environment.
func healthWords(h *fixture.HealthStore, env string, fx domain.Fixture) string {
	rec, found, err := h.Latest(env, fx.ID)
	switch {
	case err != nil:
		return "health unreadable: " + oneLine(terminalSafe(err.Error()))
	case !found:
		return "not checked on " + env
	case rec.Hash != domain.FixtureHash(fx):
		return "not checked on " + env + " in this version"
	}
	when := rec.At.UTC().Format(time.RFC3339)
	if rec.Healthy {
		return "healthy on " + env + " at " + when
	}
	return "unhealthy on " + env + " at " + when + ": " + rec.Reason
}

type fixtureCheckFlags struct {
	all         bool
	env         string
	authProfile string
	builds      []string
	dir         string
}

func newFixtureCheckCommand(o options) *cobra.Command {
	var f fixtureCheckFlags
	cmd := &cobra.Command{
		Use:   "check (<id>... | --all) --env <name>",
		Short: "Run find, create, and cleanup of fixtures on an environment and record their health",
		Long: "Run find, then create, then cleanup of each fixture on an environment, and record whether it still works.\n" +
			"A fixture that is not approved is not run. A fixture that failed its last check blocks the scenarios that use it.",
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0 && !f.all:
				return errors.New("name the fixtures to check, or use --all")
			case len(args) > 0 && f.all:
				return errors.New("name the fixtures, or use --all, and not both")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return checkFixtures(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f, args)
		},
	}
	cmd.Flags().BoolVar(&f.all, "all", false, "check every fixture in .semar/fixtures")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment of semar.yaml to check on; needed when there are several, and always for a production environment")
	cmd.Flags().StringVar(&f.authProfile, "auth-profile", "", "the auth profile for a service that sets none (default: the only one, or the one named default)")
	cmd.Flags().StringArrayVar(&f.builds, "build", nil, "the commit that a service with no build probe runs, as <commit> or <service>=<commit>; repeat for more")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func checkFixtures(ctx context.Context, stdout, stderr io.Writer, o options, f fixtureCheckFlags, ids []string) error {
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	envName, env, err := cfg.Environment(f.env)
	if err != nil {
		return err
	}
	if env.Kind == config.KindProduction && f.env == "" {
		return fmt.Errorf("the environment %q is a production environment; name it with --env %s to check fixtures on it", envName, envName)
	}
	declared, err := build.ParseDeclared(f.builds, env.Services)
	if err != nil {
		return err
	}
	cat, err := fixture.Load(f.dir, allServices(cfg))
	if err != nil {
		return err
	}
	for _, p := range cat.Problems {
		fmt.Fprintf(stderr, "semar: fixture file %s: %s; it is not checked\n", terminalSafe(p.File), terminalSafe(oneLine(problemText(p))))
	}
	chosen, err := chooseFixtures(cat, ids, f.all)
	if err != nil {
		return err
	}
	if len(chosen) == 0 {
		fmt.Fprintf(stdout, "no fixture was checked: there is no valid fixture file in %s\n", fixturesDir(f.dir))
		return exitError{ExitUndecided}
	}

	// A step to a service that sets no auth profile goes out with this one.
	fallback := f.authProfile
	if fallback == "" {
		fallback, _ = pickAuthProfile(cfg, "")
	}
	needs := map[string]string{}
	if fallback != "" {
		needs[fallback] = "the fixture check"
	}
	for _, fx := range chosen {
		addServiceProfiles(needs, env, fx, "of fixture "+string(fx.ID))
	}

	fmt.Fprintf(stderr, "semar: checking fixtures on %s (%s, %s)\n", envName, env.Kind, effectsWords(env.Effects.Mode))
	rg, err := newEnvRig(ctx, o, f.dir, cfg, env, declared, needs)
	if err != nil {
		return err
	}
	resolver, err := rg.resolver(o, envName, env, cat)
	if err != nil {
		return err
	}
	health := fixture.NewHealthStore(rg.store.Root())

	record := domain.Run{ID: rg.id, Environment: envName, EnvironmentKind: env.Kind, Purpose: domain.PurposeFixtureCheck, StartedAt: o.Clock()}
	record.BuildsStart = rg.readBuilds(ctx, stderr)
	var healthy, unhealthy, partial int
	for _, fx := range chosen {
		res, err := resolver.Check(ctx, fx.ID, fallback)
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("check of fixture %s stopped: %w; its evidence may be incomplete", fx.ID, err)
		}
		if res.Checked {
			rec := res.Record
			rec.At = o.Clock().UTC()
			rec.Builds = record.BuildsStart
			if rec.Builds == nil {
				rec.Builds = []domain.BuildRef{}
			}
			if err := health.Put(rec); err != nil {
				return err
			}
		}
		fmt.Fprintln(stdout, checkLine(fx, res))
		for _, left := range res.Left {
			fmt.Fprintf(stdout, "  still exists: %s\n", oneLine(terminalSafe(left)))
		}
		switch {
		case ctx.Err() != nil:
			partial++
		case !res.Checked || (res.Record.Cleanup == fixture.CleanupFailed):
			partial++
		case res.Record.Healthy:
			healthy++
		default:
			unhealthy++
		}
		if ctx.Err() != nil {
			break
		}
	}
	interrupted := ctx.Err() != nil
	if !interrupted {
		record.BuildsEnd = rg.readBuilds(ctx, io.Discard)
		record.EndedAt = o.Clock()
		// An interrupted run keeps no run.json, so that it loads as interrupted.
		if err := rg.run.Close(record); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "%s: %d healthy, %d unhealthy, %d not fully checked\n", plural(len(chosen), "fixture"), healthy, unhealthy, partial)
	fmt.Fprintf(stdout, "run: %s\n", rg.id)
	switch {
	case interrupted:
		fmt.Fprintf(stderr, "semar: the check was interrupted: run %s is incomplete\n", rg.id)
		return exitError{ExitUndecided}
	case unhealthy > 0:
		return exitError{ExitFailed}
	case partial > 0:
		return exitError{ExitUndecided}
	}
	return nil
}

// chooseFixtures takes the named fixtures from the catalogue, or all of them. A name that has no
// valid file is an error, and says what is wrong with a file that exists but is not valid.
func chooseFixtures(cat fixture.Catalogue, ids []string, all bool) ([]domain.Fixture, error) {
	if all {
		return slices.Clone(cat.Fixtures), nil
	}
	var out []domain.Fixture
	seen := map[string]bool{}
	for _, raw := range ids {
		if seen[raw] {
			continue
		}
		seen[raw] = true
		fx, ok := cat.Get(domain.FixtureID(raw))
		if ok {
			out = append(out, fx)
			continue
		}
		for _, p := range cat.Problems {
			if strings.HasPrefix(p.File, fixturesRel+"/"+raw+".") {
				return nil, fmt.Errorf("fixture %s is not valid, so it cannot be checked: %s", terminalSafe(raw), terminalSafe(oneLine(p.String())))
			}
		}
		return nil, fmt.Errorf("fixture %s was not found in %s", terminalSafe(raw), fixturesRel)
	}
	return out, nil
}

const fixturesRel = ".semar/fixtures"

// checkLine says how one fixture fared, on one line.
func checkLine(fx domain.Fixture, res fixture.CheckResult) string {
	head := fmt.Sprintf("%s %s: ", fx.ID, fx.Version)
	rec := res.Record
	steps := fmt.Sprintf("find %s, create %s, cleanup %s", rec.Find, rec.Create, strings.ReplaceAll(rec.Cleanup, "_", " "))
	var line string
	switch {
	case !res.Checked:
		line = head + "not fully checked: " + res.Note
	case rec.Healthy && rec.Cleanup == fixture.CleanupFailed:
		line = head + "healthy, but not fully checked (" + steps + ")"
	case rec.Healthy:
		line = head + "healthy (" + steps + ")"
	default:
		line = head + "unhealthy (" + steps + "): " + rec.Reason
	}
	return oneLine(terminalSafe(line))
}

// readBuilds probes the services that have a build. A build that cannot be read is left out of
// the health record, and one line says so.
func (rg *envRig) readBuilds(ctx context.Context, stderr io.Writer) []domain.BuildRef {
	var out []domain.BuildRef
	for _, s := range rg.probed {
		b, err := rg.builds.Probe(ctx, s)
		if err != nil {
			fmt.Fprintf(stderr, "semar: the build of %s could not be read, so the health record leaves it out\n", terminalSafe(s))
			continue
		}
		out = append(out, b)
	}
	return out
}
