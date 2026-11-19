package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
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
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/journey"
	"github.com/jancuk/semar/internal/replay"
	"github.com/jancuk/semar/internal/report"
)

const journeysRel = ".semar/journeys"

func newJourneyCommand(o options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "journey",
		Short: "List the journeys of the repository, or run them against an environment",
	}
	cmd.AddCommand(newJourneyListCommand(o), newJourneyRunCommand(o))
	return cmd
}

// loadJourneys reads .semar/journeys. A file that cannot be used is a problem, and the valid files
// stay. An error is one that the repository caused, such as a link, and text from it is made safe.
func loadJourneys(repo string) ([]domain.Journey, []journey.Problem, error) {
	js, ps, err := journey.Load(repo)
	if err != nil {
		return nil, nil, errors.New(oneLine(terminalSafe(err.Error())))
	}
	return js, ps, nil
}

func problemLine(p journey.Problem) string { return oneLine(terminalSafe(p.String())) }

// withServiceAuth gives every service that names no auth profile the one that fixture check uses:
// the only profile, or the one named default. Without it a journey's fixture and a leg on such a
// service went out with no session, while fixture check of the same fixture signed in.
func withServiceAuth(cfg config.Config, env config.Environment) config.Environment {
	fallback, err := pickAuthProfile(cfg, "")
	if err != nil {
		return env
	}
	services := slices.Clone(env.Services)
	for i := range services {
		if services[i].AuthProfile == "" {
			services[i].AuthProfile = fallback
		}
	}
	env.Services = services
	return env
}

// legAuth is the auth profile of a leg: the one of its service in the environment, else the
// journey's (Plan 18, D7). Both the run and the confirmation use it, because the profile is part of
// the hash that a confirmation names.
func legAuth(env config.Environment, j domain.Journey, service string) string {
	for _, s := range env.Services {
		if s.Name == service && s.AuthProfile != "" {
			return s.AuthProfile
		}
	}
	return j.AuthProfile
}

// ---- semar journey list

type journeyListFlags struct {
	env    string
	builds []string
	dir    string
}

func newJourneyListCommand(o options) *cobra.Command {
	var f journeyListFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the journeys with their last result, and mark a result stale when a build has changed",
		Long: "List each journey of .semar/journeys with its criticality, its owner, its legs, and its last stored result.\n" +
			"With --env, read the builds that the environment runs now, and show a result as stale when any of them differs from the manifest of the result.\n" +
			"Exit 0, or 2 when a journey file is not valid.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listJourneys(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
		},
	}
	cmd.Flags().StringVar(&f.env, "env", "", "read the builds that this environment of semar.yaml runs now, and mark a result stale when one differs")
	cmd.Flags().StringArrayVar(&f.builds, "build", nil, "with --env, the commit that a service with no build probe runs, as <commit> or <service>=<commit>; repeat for more")
	addDirFlag(cmd, &f.dir)
	return cmd
}

// storedJourney is the newest stored result of a journey.
type storedJourney struct {
	run domain.RunID
	res domain.JourneyResult
}

func listJourneys(ctx context.Context, stdout, stderr io.Writer, o options, f journeyListFlags) error {
	if len(f.builds) > 0 && f.env == "" {
		return errors.New("--build needs --env: it names the build that a service without a probe runs now")
	}
	js, problems, err := loadJourneys(f.dir)
	if err != nil {
		return err
	}
	stored, err := lastJourneyResults(stderr, f.dir)
	if err != nil {
		return err
	}
	var live *liveBuilds
	if f.env != "" {
		if live, err = newLiveBuilds(ctx, f); err != nil {
			return err
		}
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, j := range js {
		legs := plural(len(j.Legs), "leg")
		status := "no result yet"
		if s, ok := stored[j.ID]; ok {
			status = fmt.Sprintf("%s in %s on manifest %s", s.res.Outcome, s.run, shortHash(s.res.Manifest.Hash))
			if live != nil {
				status = live.mark(ctx, j, s, status)
			}
		}
		cells := []string{string(j.ID), j.Criticality, j.Owner, legs, status}
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
	if len(js) == 0 && len(problems) == 0 {
		fmt.Fprintf(stdout, "there is no journey file in %s\n", filepath.Join(semarDir(f.dir), "journeys"))
	}
	for _, p := range problems {
		fmt.Fprintf(stdout, "invalid: %s\n", problemLine(p))
	}
	if len(problems) > 0 {
		return exitError{ExitUndecided}
	}
	return nil
}

func shortHash(h domain.Hash) string { return string(h[:min(len(h), 12)]) }

// lastJourneyResults is the newest result of each journey in the complete runs of journeys.
func lastJourneyResults(stderr io.Writer, repo string) (map[domain.JourneyID]storedJourney, error) {
	store, err := evidence.Open(semarDir(repo))
	if err != nil {
		return nil, err
	}
	ids, err := store.Runs()
	if err != nil {
		return nil, err
	}
	out := map[domain.JourneyID]storedJourney{}
	for _, id := range ids {
		run, _, err := store.LoadRun(id)
		switch {
		case errors.Is(err, evidence.ErrInterrupted):
			continue
		case err != nil:
			fmt.Fprintf(stderr, "semar: run %s could not be read, so its results are not counted: %s\n", terminalSafe(string(id)), oneLine(terminalSafe(err.Error())))
			continue
		case run.Purpose != domain.PurposeJourney:
			continue
		}
		for _, jr := range run.Journeys {
			if _, ok := out[jr.Journey]; !ok {
				out[jr.Journey] = storedJourney{run: run.ID, res: jr}
			}
		}
	}
	return out, nil
}

// liveBuilds reads the builds that an environment runs now, once for each service.
type liveBuilds struct {
	env    config.Environment
	prober build.Prober
	read   map[string]*domain.BuildRef // nil: the build could not be read
}

func newLiveBuilds(ctx context.Context, f journeyListFlags) (*liveBuilds, error) {
	cfg, err := config.Load(f.dir)
	if err != nil {
		return nil, err
	}
	_, env, err := cfg.Environment(f.env)
	if err != nil {
		return nil, err
	}
	declared, err := readDeclaredBuilds(ctx, f.dir, f.builds, env.Services)
	if err != nil {
		return nil, err
	}
	return &liveBuilds{env: env, prober: declared.prober(env.Services), read: map[string]*domain.BuildRef{}}, nil
}

func (l *liveBuilds) build(ctx context.Context, service string) *domain.BuildRef {
	if b, ok := l.read[service]; ok {
		return b
	}
	var out *domain.BuildRef
	if b, err := l.prober.Probe(ctx, service); err == nil {
		out = &b
	}
	l.read[service] = out
	return out
}

// mark says whether the stored result still holds: stale with the part that changed when a service
// of its manifest, or of the journey, runs another build now. A build that cannot be read is
// said, and does not make the result stale.
func (l *liveBuilds) mark(ctx context.Context, j domain.Journey, s storedJourney, status string) string {
	names := map[string]bool{}
	for _, b := range s.res.Manifest.Services {
		names[b.Service] = true
	}
	for _, leg := range j.Legs {
		names[leg.Service] = true
	}
	var now, was []domain.BuildRef
	var unread []string
	for _, name := range slices.Sorted(maps.Keys(names)) {
		cur := l.build(ctx, name)
		if cur == nil {
			unread = append(unread, "the build of "+name+" could not be read")
			continue
		}
		now = append(now, *cur)
		if i := slices.IndexFunc(s.res.Manifest.Services, func(b domain.BuildRef) bool { return b.Service == name }); i >= 0 {
			was = append(was, s.res.Manifest.Services[i])
		}
	}
	out := status
	if changed := journey.Changes(was, now); len(changed) > 0 {
		out = fmt.Sprintf("stale (was %s in %s): %s", s.res.Outcome, s.run, strings.Join(changed, "; "))
	}
	if len(unread) > 0 {
		out += "; not checked: " + strings.Join(unread, ", ")
	}
	return out
}

// ---- semar journey run

type journeyRunFlags struct {
	critical bool
	all      bool
	env      string
	builds   []string
	dir      string
	headed   bool
}

func newJourneyRunCommand(o options) *cobra.Command {
	var f journeyRunFlags
	cmd := &cobra.Command{
		Use:   "run (<id>... | --critical | --all)",
		Short: "Run journeys against an environment, leg by leg, and write a report",
		Long: "Run each journey one leg at a time in the order of its needs, check its invariants, and read the builds again at the end.\n" +
			"A journey whose expected results nobody confirmed is blocked, and nothing is sent for it. Review them with semar review <id>.\n" +
			"Exit 0 when every journey passed, 1 when one failed, and 2 otherwise, including a journey file that was skipped because it is not valid.",
		Args: func(_ *cobra.Command, args []string) error {
			n := len(args)
			if f.critical {
				n++
			}
			if f.all {
				n++
			}
			if f.critical && f.all || (f.critical || f.all) && len(args) > 0 || n == 0 {
				return errors.New("name the journeys, or use --critical, or use --all, and only one of them")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJourneys(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f, args)
		},
	}
	cmd.Flags().BoolVar(&f.critical, "critical", false, "run every journey with criticality critical")
	cmd.Flags().BoolVar(&f.all, "all", false, "run every valid journey in .semar/journeys")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment of semar.yaml to run against; needed when there are several, and always for a production environment")
	cmd.Flags().StringArrayVar(&f.builds, "build", nil, "the commit that a service with no build probe runs, as <commit> or <service>=<commit>; repeat for more")
	cmd.Flags().BoolVar(&f.headed, "headed", false, "show the browser window of a web leg")
	addDirFlag(cmd, &f.dir)
	return cmd
}

// selectJourneys takes the journeys that a run names. A named journey that is missing, or not
// valid, is an error before anything is sent. With --critical or --all, a file that is not valid
// is skipped with one line, and counted, because a skipped journey must not read as a pass.
func selectJourneys(stderr io.Writer, repo string, ids []string, critical bool) ([]domain.Journey, int, error) {
	js, problems, err := loadJourneys(repo)
	if err != nil {
		return nil, 0, err
	}
	if len(ids) > 0 {
		var out []domain.Journey
		seen := map[string]bool{}
		for _, raw := range ids {
			if seen[raw] {
				continue
			}
			seen[raw] = true
			if err := idcheck.Check("journey ID", raw); err != nil {
				return nil, 0, err
			}
			i := slices.IndexFunc(js, func(j domain.Journey) bool { return string(j.ID) == raw })
			if i >= 0 {
				out = append(out, js[i])
				continue
			}
			for _, p := range problems {
				if strings.HasPrefix(p.File, journeysRel+"/"+raw+".") {
					return nil, 0, fmt.Errorf("journey %s is not valid, so it cannot be run: %s", raw, problemLine(p))
				}
			}
			return nil, 0, fmt.Errorf("journey %s was not found in %s", raw, journeysRel)
		}
		return out, 0, nil
	}
	for _, p := range problems {
		fmt.Fprintf(stderr, "semar: journey file %s; this run skips it\n", problemLine(p))
	}
	if critical {
		js = slices.DeleteFunc(js, func(j domain.Journey) bool { return j.Criticality != "critical" })
	}
	return js, len(problems), nil
}

func runJourneys(ctx context.Context, stdout, stderr io.Writer, o options, f journeyRunFlags, ids []string) error {
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	envName, env, err := cfg.Environment(f.env)
	env = withServiceAuth(cfg, env)
	if err != nil {
		return err
	}
	if env.Kind == config.KindProduction && f.env == "" {
		return fmt.Errorf("the environment %q is a production environment; name it with --env %s to run against it", envName, envName)
	}
	declared, err := readDeclaredBuilds(ctx, f.dir, f.builds, env.Services)
	if err != nil {
		return err
	}
	chosen, skipped, err := selectJourneys(stderr, f.dir, ids, f.critical)
	if err != nil {
		return err
	}
	if len(chosen) == 0 {
		what := "there is no valid journey file in " + journeysRel
		if f.critical {
			what = "no valid journey in " + journeysRel + " has criticality critical"
		}
		fmt.Fprintf(stdout, "no journey ran: %s\n", what)
		return exitError{ExitUndecided}
	}

	fin, err := executeJourneys(ctx, stderr, o, f.dir, cfg, envName, env, declared, chosen, f.headed, func() {})
	if err != nil {
		return err
	}
	record, coverage, interrupted := fin.record, fin.coverage, fin.interrupted
	if err := report.Summary(stdout, record, coverage); err != nil {
		return err
	}
	report.WriteBlocked(stdout, envName, "scenario", fin.blocked)
	report.WriteDocWarnings(stdout, envName, fin.docs)
	fmt.Fprintf(stdout, "report: %s\n", fin.reportPath)
	if interrupted {
		fmt.Fprintf(stderr, "semar: the run was interrupted: run %s is incomplete\n", record.ID)
		return exitError{ExitUndecided}
	}
	code := report.JourneyExitCode(record.Journeys)
	if skipped > 0 {
		fmt.Fprintf(stderr, "semar: %s was skipped, so this run does not cover every journey\n", plural(skipped, "journey file"))
		if code == ExitOK {
			code = ExitUndecided
		}
	}
	if code != ExitOK {
		return exitError{code}
	}
	return nil
}

// executeJourneys runs the journeys against one environment, writes the report, and closes the run.
func executeJourneys(ctx context.Context, stderr io.Writer, o options, dir string, cfg config.Config, envName string, env config.Environment, declared declaredBuilds, chosen []domain.Journey, headed bool, announce func()) (finishedRun, error) {
	var cat *fixture.Catalogue
	if slices.ContainsFunc(chosen, func(j domain.Journey) bool { return len(j.Given) > 0 }) {
		c, err := fixture.Load(dir, allServices(cfg), fixtureAccounts(cfg))
		if err != nil {
			return finishedRun{}, err
		}
		for _, p := range c.Problems {
			fmt.Fprintf(stderr, "semar: fixture file %s: %s; this run does not use it\n", terminalSafe(p.File), terminalSafe(oneLine(problemText(p))))
		}
		cat = &c
	}

	fmt.Fprintf(stderr, "semar: running against %s (%s, %s)\n", envName, env.Kind, effectsWords(env.Effects.Mode))
	rg, err := newEnvRig(ctx, o, dir, cfg, env, declared, journeyNeeds(cfg, env, chosen, cat))
	if err != nil {
		return finishedRun{}, err
	}
	confirmations := confirm.NewLocal(rg.store.Root())
	deps := replay.Deps{
		Engine: rg.engine, Builds: rg.builds, Confirm: confirmations, Trust: cfg.Policy.Trust.Confirm, Run: rg.id,
		Accounts: slices.Sorted(maps.Keys(cfg.AuthProfiles)), NoToken: noTokenAccounts(cfg),
	}
	eng := rg.webEngine(o, dir, cfg, envName, env, headed)
	defer eng.Close()
	announce()
	deps.Web, deps.Screens = eng, screenSource{repo: dir, approvals: confirmations}
	jdeps := journey.Deps{Builds: rg.builds, Services: map[string]string{}, Clock: o.Clock, Sleep: o.Sleep, Accounts: deps.Accounts}
	for _, s := range env.Services {
		jdeps.Services[s.Name] = s.AuthProfile
	}
	if cat != nil {
		fx, err := rg.resolver(o, envName, env, *cat)
		if err != nil {
			return finishedRun{}, err
		}
		deps.Fixtures, jdeps.Fixtures, jdeps.Catalogue = fx, fx, cat
	}
	rep := replay.New(deps)
	jdeps.Replay = rep
	runner := journey.New(jdeps)

	record := domain.Run{ID: rg.id, Environment: envName, EnvironmentKind: env.Kind, Purpose: domain.PurposeJourney, StartedAt: o.Clock()}
	scenarios := map[domain.ScenarioID]domain.Scenario{}
	titles := map[domain.JourneyID]string{}
	var runErr error
	for _, j := range chosen {
		titles[j.ID] = j.Title
		for _, l := range j.Legs {
			sc, _ := journey.LegScenario(j, l.ID, legAuth(env, j, l.Service))
			scenarios[sc.ID] = sc
		}
		if ctx.Err() != nil {
			break
		}
		jr, results, ok := unconfirmedJourney(confirmations, cfg.Policy.Trust.Confirm, env, j)
		if !ok {
			var err error
			jr, results, err = runner.Run(ctx, j)
			runErr = errors.Join(runErr, err)
		}
		record.Results = append(record.Results, results...)
		record.Journeys = append(record.Journeys, jr)
	}
	interrupted := ctx.Err() != nil
	if runErr != nil && !interrupted {
		return finishedRun{}, fmt.Errorf("run %s stopped: %w; its evidence may be incomplete, so it reports no outcome", rg.id, runErr)
	}
	record.BuildsStart, record.BuildsEnd = manifestBuilds(record.Journeys)
	if record.Results == nil {
		record.Results = []domain.ScenarioResult{}
	}
	if !interrupted {
		record.EndedAt = o.Clock()
	}

	acts, err := rg.store.Activity(rg.id)
	if err != nil {
		return finishedRun{}, err
	}
	coverage, tickets := runCoverage(stderr, o, dir, nil, record)
	writeReport := func(rec domain.Run) error {
		var md bytes.Buffer
		data := report.Data{Run: rec, Scenarios: scenarios, Activity: acts, Coverage: coverage, CoverageTickets: tickets, Journeys: rec.Journeys, JourneyTitles: titles}
		if err := report.Markdown(&md, data); err != nil {
			return err
		}
		return rg.run.WriteFile(reportFile, md.Bytes())
	}
	if err := writeReport(record); err != nil {
		return finishedRun{}, err
	}
	// An interrupted run keeps no run.json, so that it loads as interrupted.
	if !interrupted {
		if err := rg.run.Close(record); err != nil {
			unfinished := record
			unfinished.EndedAt = time.Time{}
			if werr := writeReport(unfinished); werr != nil {
				return finishedRun{}, errors.Join(err, fmt.Errorf("rewrite %s as unfinished: %w", reportFile, werr))
			}
			return finishedRun{}, err
		}
	}
	return finishedRun{record: record, reportPath: filepath.Join(rg.store.Root(), "runs", string(rg.id), reportFile), interrupted: interrupted, coverage: coverage, blocked: report.BlockedHosts(acts), docs: report.DocFindings(acts)}, nil
}

// legStepAccounts is the as of each step of a leg that has one.
func legStepAccounts(l domain.Leg) []string {
	var out []string
	for _, st := range l.Steps {
		if st.As != "" {
			out = append(out, st.As)
		}
	}
	return out
}

// journeyNeeds maps each auth profile that the journeys use to the first reason it is needed: the
// profile of a leg's service, the journey's own, or the profile of a service that a fixture calls.
func journeyNeeds(cfg config.Config, env config.Environment, js []domain.Journey, cat *fixture.Catalogue) map[string]string {
	needs := map[string]string{}
	add := func(profile, why string) {
		if _, ok := needs[profile]; profile != "" && !ok {
			needs[profile] = why
		}
	}
	for _, j := range js {
		for _, l := range j.Legs {
			add(legAuth(env, j, l.Service), "leg "+string(l.ID)+" of journey "+string(j.ID))
			// An account that the configuration lacks is no reason to stop: the runner blocks the
			// journey and names it.
			for _, as := range append([]string{l.As}, legStepAccounts(l)...) {
				if _, known := cfg.AuthProfiles[as]; known {
					add(as, "leg "+string(l.ID)+" of journey "+string(j.ID)+" acts as it")
				}
			}
		}
		if len(j.Given) == 0 || cat == nil {
			continue
		}
		add(j.AuthProfile, "journey "+string(j.ID))
		for _, g := range j.Given {
			if f, ok := cat.Get(g.Fixture); ok {
				addServiceProfiles(needs, env, f, "of fixture "+string(f.ID)+" for journey "+string(j.ID))
			}
		}
	}
	return needs
}

// manifestBuilds is the builds of every journey of the run, one for each service: the start builds
// of the first journey that read the service, and its end builds, as far as they were read.
func manifestBuilds(js []domain.JourneyResult) (start, end []domain.BuildRef) {
	start, end = []domain.BuildRef{}, []domain.BuildRef{}
	add := func(list *[]domain.BuildRef, from []domain.BuildRef) {
		for _, b := range from {
			if !slices.ContainsFunc(*list, func(x domain.BuildRef) bool { return x.Service == b.Service }) {
				*list = append(*list, b)
			}
		}
	}
	for _, j := range js {
		add(&start, j.Manifest.Services)
		if j.ManifestEnd != nil {
			add(&end, j.ManifestEnd.Services)
		}
	}
	byService := func(a, b domain.BuildRef) int { return strings.Compare(a.Service, b.Service) }
	slices.SortFunc(start, byService)
	slices.SortFunc(end, byService)
	return start, end
}

// unconfirmedJourney answers a journey that has a leg whose expected results a person must confirm
// and has not. Such a journey sends nothing, so a flow of writes never starts without the means to
// judge it. ok is false when every leg is confirmed, and the journey runs.
func unconfirmedJourney(store confirm.Store, trust []domain.Source, env config.Environment, j domain.Journey) (domain.JourneyResult, []domain.ScenarioResult, bool) {
	order, err := journey.Order(j)
	if err != nil {
		return domain.JourneyResult{}, nil, false
	}
	whys := make([]string, len(order))
	asOf := make([][]string, len(order))
	first := -1
	for i, id := range order {
		leg := j.Legs[slices.IndexFunc(j.Legs, func(l domain.Leg) bool { return l.ID == id })]
		sc, _ := journey.LegScenario(j, id, legAuth(env, j, leg.Service))
		asOf[i] = journey.ActedAs(sc)
		if whys[i] = unconfirmedWhy(store, trust, sc, j.ID); whys[i] != "" && first < 0 {
			first = i
		}
	}
	if first < 0 {
		return domain.JourneyResult{}, nil, false
	}

	legs := make([]domain.LegResult, len(order))
	results := make([]domain.ScenarioResult, len(order))
	for i, id := range order {
		sid := domain.ScenarioID(string(j.ID) + "." + string(id))
		outcome, reason := domain.OutcomeNotRun, domain.Reason{
			Rule: domain.OutcomeNotRun, Text: "nothing was sent: the expected results of leg " + string(order[first]) + " are not confirmed",
		}
		if whys[i] != "" {
			outcome, reason = domain.OutcomeInconclusive, domain.Reason{Rule: domain.OutcomeInconclusive, Text: whys[i]}
		}
		legs[i] = domain.LegResult{Leg: id, Scenario: sid, Outcome: outcome, Reasons: []domain.Reason{reason}, As: asOf[i]}
		results[i] = domain.ScenarioResult{Scenario: sid, Outcome: outcome, Reasons: []domain.Reason{reason}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{}}
	}
	// The builds were not read, because nothing was sent: the manifest is empty at both ends.
	empty := domain.Manifest{Services: []domain.BuildRef{}, Hash: domain.ManifestHash(nil)}
	in := journey.Outcome{Legs: legs, Start: empty, End: &empty}
	outcome, firstLeg, reasons := journey.Decide(in)
	return domain.JourneyResult{
		Journey: j.ID, Manifest: empty, ManifestEnd: &empty, Outcome: outcome, FirstFailing: firstLeg,
		Legs: legs, Invariants: []domain.InvariantResult{}, Reasons: reasons,
	}, results, true
}

// unconfirmedWhy is why the scenario of a leg cannot be judged, or "" when no person has to
// confirm its expected results or one has.
func unconfirmedWhy(store confirm.Store, trust []domain.Source, sc domain.Scenario, id domain.JourneyID) string {
	var needed []domain.Source
	for _, st := range sc.Steps {
		for _, e := range st.Expect {
			if confirm.NeedsExpectation(e, trust) {
				needed = append(needed, e.Source)
			}
		}
	}
	if len(needed) == 0 {
		return ""
	}
	rec, found, err := store.Get(sc.ID, domain.ExpectHash(sc))
	switch {
	case err != nil:
		return "the confirmations could not be read: " + err.Error()
	case !found || slices.ContainsFunc(needed, func(s domain.Source) bool { return !slices.Contains(rec.Sources, s) }):
		return "an expected result is not confirmed: run semar review " + string(id)
	}
	return ""
}
