package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/replay"
	"github.com/jancuk/semar/internal/report"
	"github.com/jancuk/semar/internal/status"
)

// selection is what semar run chose to run, and why, before it sends anything (Plan 26, D5).
type selection struct {
	change     string // the first line: what the run is of
	scenarios  []domain.Scenario
	journeys   []domain.Journey
	skipped    int // journey files that are not valid
	input      status.Input
	obs        []obligation.Located
	notPlanned []string
	gaps       []string
	changeSet  *domain.ChangeSet
}

func runDaily(ctx context.Context, stdout, stderr io.Writer, o options, f runFlags) error {
	if f.all && len(f.scenarios) > 0 {
		return errors.New("name the scenarios with --scenario <id>, or use --all, and not both")
	}
	if len(f.suites) > 0 && (f.all || len(f.scenarios) > 0) {
		return errors.New("--suite cannot be combined with --all or --scenario")
	}
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	noteIgnoredSettings(stderr, cfg)
	if f.retriesSet {
		cfg.Replay.Retries = f.retries
	}
	envName, env, err := cfg.Environment(f.env)
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
	env = withServiceAuth(cfg, env)

	answers := newLineReader(o.Stdin)
	sel, code, err := chooseRun(ctx, stdout, stderr, o, answers, f, cfg, envName)
	if err != nil {
		return err
	}
	var builds []domain.BuildRef
	announced := false
	// The header comes after the secrets are found and before the first request of the run. Reading
	// the builds is the first request, so it waits for the same moment.
	announce := func() {
		if announced {
			return
		}
		announced = true
		var buildsErr error
		builds, buildsErr = currentBuilds(ctx, f.dir, cfg, envName, f.builds)
		printHeader(stdout, sel, envName, env, builds, buildsErr)
	}
	if len(sel.scenarios) == 0 && len(sel.journeys) == 0 {
		printHeader(stdout, sel, envName, env, nil, nil)
		if sel.changeSet == nil {
			fmt.Fprintf(stdout, "\nno scenario ran: there is no scenario file in %s\n", scenariosDir(f.dir))
		} else {
			fmt.Fprintln(stdout, "\nnothing to run: no scenario of the change is written yet")
		}
		return exitError{max(code, ExitUndecided)}
	}

	var runs []finishedRun
	scenarioExit := 0
	if len(sel.scenarios) > 0 {
		var pause func(context.Context, replay.Pause)
		if f.pauseOnFail {
			pause = pauseForPerson(stdout, answers)
		}
		fin, err := executeScenarios(ctx, stderr, o, f.dir, cfg, envName, env, declared, sel.scenarios, f.headed, pause, announce)
		if err != nil {
			return err
		}
		runs = append(runs, fin)
		if f.done != nil {
			f.done(fin)
		}
		fmt.Fprintln(stdout)
		if err := printScenarioRun(stdout, envName, fin); err != nil {
			return err
		}
		deliverReports(stdout, stderr, o, f.reportDir, "", fin)
		scenarioExit = scenarioCode(fin)
	}
	if len(sel.journeys) > 0 && ctx.Err() == nil {
		fin, err := executeJourneys(ctx, stderr, o, f.dir, cfg, envName, env, declared, sel.journeys, f.headed, announce)
		if err != nil {
			return err
		}
		runs = append(runs, fin)
		if len(sel.scenarios) == 0 {
			fmt.Fprintln(stdout)
		}
		if err := report.Lines(stdout, fin.record); err != nil {
			return err
		}
		report.WriteBlocked(stdout, envName, "scenario", fin.blocked)
		report.WriteDocWarnings(stdout, envName, fin.docs)
		report.WriteMisses(stdout, string(fin.record.ID), fin.misses)
		fmt.Fprintf(stdout, "report: %s\n", fin.reportPath)
		sub := ""
		if len(sel.scenarios) > 0 {
			sub = "journeys"
		}
		deliverReports(stdout, stderr, o, f.reportDir, sub, fin)
		code = max(code, report.JourneyExitCode(fin.record.Journeys))
		if fin.interrupted {
			code = max(code, ExitUndecided)
		}
	}
	code = max(code, scenarioExit)
	if sel.skipped > 0 {
		fmt.Fprintf(stderr, "semar: %s was skipped, so this run does not cover every journey\n", plural(sel.skipped, "journey file"))
		code = max(code, ExitUndecided)
	}
	for _, fin := range runs {
		if fin.interrupted {
			fmt.Fprintf(stderr, "semar: the run was interrupted: run %s is incomplete\n", fin.record.ID)
			code = max(code, ExitUndecided)
		}
	}

	// The status that closes the run reads the builds that the run ended on.
	in := sel.input
	in.Environment, in.BuildsRead = envName, true
	for _, fin := range slices.Backward(runs) {
		if len(fin.record.BuildsEnd) > 0 {
			in.Builds = fin.record.BuildsEnd
			break
		}
	}
	if len(in.Builds) == 0 {
		in.Builds = builds
	}
	if len(sel.obs) > 0 || len(sel.notPlanned) > 0 {
		rep, err := statusReport(stderr, o, f.dir, in, sel.obs, "", in.Builds)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout)
		if err := status.Text(stdout, rep); err != nil {
			return err
		}
	}
	if code != ExitOK {
		return exitError{code}
	}
	return nil
}

// scenarioCode is the exit code of what the scenarios did.
func scenarioCode(fin finishedRun) int {
	if fin.interrupted {
		return ExitUndecided
	}
	return report.ExitCode(fin.record.Results)
}

// statusReport derives the state of the use cases from the stored runs and groups it.
func statusReport(stderr io.Writer, o options, dir string, in status.Input, obs []obligation.Located, only domain.RunID, current []domain.BuildRef) (status.Report, error) {
	entries, err := deriveEntries(stderr, o, dir, obs, only, current)
	if err != nil {
		return status.Report{}, err
	}
	in.Entries = entries
	in.Builds = current
	return status.Build(in), nil
}

// chooseRun picks the scenarios and journeys of a run. The code is an exit code that the choice
// already fixes: a ticket that is not planned leaves the run undecided.
func chooseRun(ctx context.Context, stdout, stderr io.Writer, o options, in *lineReader, f runFlags, cfg config.Config, envName string) (selection, int, error) {
	var sel selection
	switch {
	case len(f.scenarios) > 0:
		seen := map[domain.ScenarioID]bool{}
		for _, raw := range f.scenarios {
			id := domain.ScenarioID(raw)
			if seen[id] {
				continue
			}
			seen[id] = true
			sc, err := loadScenario(f.dir, id)
			if err != nil {
				return sel, 0, err
			}
			sel.scenarios = append(sel.scenarios, sc)
		}
		sel.change = "the scenarios named"
		sel.input.Scope = sel.change
		sel.input.Tickets = ticketsOf(sel.scenarios)
	case len(f.suites) > 0:
		scs, err := loadAllScenarios(f.dir)
		if err != nil {
			return sel, 0, err
		}
		if sel.scenarios, err = inSuites(scs, f.suites); err != nil {
			return sel, 0, err
		}
		sel.change = "the " + strings.Join(slices.Compact(slices.Sorted(slices.Values(f.suites))), ", ") + " suite"
		sel.input.Scope = sel.change
		sel.input.Tickets = ticketsOf(sel.scenarios)
	case f.all:
		scs, err := loadAllScenarios(f.dir)
		if err != nil {
			return sel, 0, err
		}
		sel.scenarios, sel.change = scs, "every scenario"
		sel.input.Scope = sel.change
		if err := addCriticalJourneys(stderr, f, &sel); err != nil {
			return sel, 0, err
		}
	default:
		return chooseChange(ctx, stdout, stderr, o, in, f, cfg, envName)
	}
	sel.obs = loadObligationsForRun(stderr, f.dir)
	if len(f.scenarios) > 0 || len(f.suites) > 0 {
		sel.obs = slices.DeleteFunc(sel.obs, func(l obligation.Located) bool {
			return !slices.Contains(sel.input.Tickets, ticketKey(l)) && !slices.ContainsFunc(l.Covers, func(id domain.ScenarioID) bool {
				return slices.ContainsFunc(sel.scenarios, func(sc domain.Scenario) bool { return sc.ID == id })
			})
		})
	}
	sel.gaps = surfaceGaps(o, f.dir, cfg, envName, sel.scenarios)
	return sel, 0, nil
}

// inSuites is the scenarios that are in any of the suites, in the order given. A suite that no scenario
// is in is an error that names the suites that exist.
func inSuites(scs []domain.Scenario, names []string) ([]domain.Scenario, error) {
	have := map[string]bool{}
	for _, sc := range scs {
		for _, s := range sc.Suites {
			have[s] = true
		}
	}
	for _, name := range names {
		if have[name] {
			continue
		}
		known := slices.Sorted(maps.Keys(have))
		if len(known) == 0 {
			return nil, fmt.Errorf("no scenario is in the suite %q: no scenario has a @suite: tag", name)
		}
		return nil, fmt.Errorf("no scenario is in the suite %q: the suites that exist are %s", name, strings.Join(known, ", "))
	}
	return slices.DeleteFunc(slices.Clone(scs), func(sc domain.Scenario) bool {
		return !slices.ContainsFunc(sc.Suites, func(s string) bool { return slices.Contains(names, s) })
	}), nil
}

func ticketsOf(scs []domain.Scenario) []string {
	var out []string
	for _, sc := range scs {
		if sc.Ticket != "" && !slices.Contains(out, sc.Ticket) {
			out = append(out, sc.Ticket)
		}
	}
	slices.Sort(out)
	return out
}

func addCriticalJourneys(stderr io.Writer, f runFlags, sel *selection) error {
	if f.noJourneys {
		return nil
	}
	js, skipped, err := selectJourneys(stderr, f.dir, nil, true)
	if err != nil {
		return err
	}
	sel.journeys, sel.skipped = js, skipped
	return nil
}

// chooseChange selects the scenarios of the tickets of the change, and then the critical journeys.
func chooseChange(ctx context.Context, stdout, stderr io.Writer, o options, in *lineReader, f runFlags, cfg config.Config, envName string) (selection, int, error) {
	var sel selection
	lc, err := readLocalChange(ctx, f.dir, cfg, f.base)
	if err != nil {
		return sel, 0, err
	}
	if !lc.Repo {
		return sel, 0, errors.New("this directory is not in a git repository, so there is no change to run: name scenarios with --scenario, or run every one with --all")
	}
	cs := lc.Change
	sel.changeSet = &cs
	sel.input.Branch, sel.input.Base = cs.Head, cs.Base
	sel.input.Tickets = changeTickets(cs)
	sel.change = fmt.Sprintf("%s against %s: %s", terminalSafe(cs.Head), terminalSafe(cs.Base), strings.Join(sel.input.Tickets, ", "))
	for _, id := range cs.NoTicket {
		sel.input.NoTicket = append(sel.input.NoTicket, noTicketCommit(lc, id))
	}
	if len(sel.input.Tickets) == 0 {
		sel.change = fmt.Sprintf("%s against %s: no ticket", terminalSafe(cs.Head), terminalSafe(cs.Base))
		fmt.Fprintf(stdout, "Change    %s\n", sel.change)
		fmt.Fprintln(stdout, "No commit of the change names a ticket, so no test was chosen for it. Run every test with semar run --all, or name scenarios with --scenario.")
		return sel, 0, exitError{ExitUndecided}
	}

	obs := loadObligationsForRun(stderr, f.dir)
	unplanned := func() []string {
		have := map[string]bool{}
		for _, l := range obs {
			have[ticketKey(l)] = true
		}
		var out []string
		for _, t := range sel.input.Tickets {
			if !have[t] {
				out = append(out, t)
			}
		}
		return out
	}
	notPlanned := unplanned()
	planned := slices.DeleteFunc(slices.Clone(sel.input.Tickets), func(t string) bool { return slices.Contains(notPlanned, t) })
	fresh, missing := ticketsWithFiles(f.dir, cfg, notPlanned), ticketsWithMissingCriteria(f.dir, cfg, planned)
	if len(fresh)+len(missing) > 0 && !f.noPlan && o.Terminal() {
		est := o.PlanEstimate(ctx, o, f.dir, cfg, slices.Concat(fresh, missing))
		if offerToPlan(ctx, stdout, in, est, fresh, missing) {
			if err := planTheChange(ctx, stdout, stderr, o, f, envName); err != nil {
				return sel, 0, err
			}
			obs = loadObligationsForRun(stderr, f.dir)
			notPlanned = unplanned()
		}
	}
	sel.obs = slices.DeleteFunc(slices.Clone(obs), func(l obligation.Located) bool { return !slices.Contains(sel.input.Tickets, ticketKey(l)) })
	sel.notPlanned, sel.input.NotPlanned = notPlanned, notPlanned

	scs, err := loadAllScenarios(f.dir)
	if err != nil {
		return sel, 0, err
	}
	for _, sc := range scs {
		if slices.Contains(sel.input.Tickets, sc.Ticket) {
			sel.scenarios = append(sel.scenarios, sc)
		}
	}
	if err := addCriticalJourneys(stderr, f, &sel); err != nil {
		return sel, 0, err
	}
	for _, t := range notPlanned {
		sel.gaps = append(sel.gaps, t+" is not planned → semar plan")
	}
	if len(sel.input.NoTicket) > 0 {
		sel.gaps = append(sel.gaps, status.NoTicketLine(sel.input.NoTicket))
	}
	sel.gaps = append(sel.gaps, surfaceGaps(o, f.dir, cfg, envName, sel.scenarios)...)
	code := ExitOK
	if len(notPlanned) > 0 {
		code = ExitUndecided
	}
	return sel, code, nil
}

// planTheChange runs semar plan for the change of this branch, as if the person had typed it.
func planTheChange(ctx context.Context, stdout, stderr io.Writer, o options, f runFlags, envName string) error {
	cmd := newPlanCommand(o)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	args := []string{"--dir", f.dir, "--env", envName}
	if f.base != "" {
		args = append(args, "--base", f.base)
	}
	cmd.SetArgs(args)
	var exit exitError
	if err := cmd.ExecuteContext(ctx); err != nil && !errors.As(err, &exit) {
		return err
	}
	return nil
}

func printHeader(w io.Writer, sel selection, envName string, env config.Environment, builds []domain.BuildRef, buildsErr error) {
	fmt.Fprintf(w, "Change    %s\n", sel.change)
	target := fmt.Sprintf("%s (%s)", terminalSafe(envName), env.Kind)
	switch {
	case buildsErr != nil:
		target += ", build not read"
	case len(builds) > 0:
		parts := make([]string, len(builds))
		for i, b := range builds {
			parts[i] = fmt.Sprintf("%s at %s", terminalSafe(b.Service), domain.ShortCommit(b.Commit))
		}
		target += ", " + strings.Join(parts, ", ")
	}
	fmt.Fprintf(w, "Target    %s\n", target)
	selected := plural(len(sel.scenarios), "scenario")
	if sel.journeys != nil || len(sel.journeys) > 0 {
		selected += ", " + plural(len(sel.journeys), "critical journey")
	}
	fmt.Fprintf(w, "Selected  %s\n", strings.ReplaceAll(selected, "critical journeys", "critical journeys"))
	for i, g := range sel.gaps {
		label := "Gaps      "
		if i > 0 {
			label = "          "
		}
		fmt.Fprintf(w, "%s%s\n", label, terminalSafe(oneLine(g)))
	}
}

// loadObligationsForRun reads the use cases for the status that ends a run. A run must not fail for
// a use case file, so a problem leaves the status out and says so on standard error.
func loadObligationsForRun(stderr io.Writer, dir string) []obligation.Located {
	list, problems, err := obligation.Load(dir)
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "semar: the status is left out of the run: %s\n", oneLine(terminalSafe(err.Error())))
		return nil
	case len(problems) > 0:
		fmt.Fprintf(stderr, "semar: the status is left out of the run: %s; semar status names every problem\n", oneLine(terminalSafe(problems[0].String())))
		return nil
	}
	return list
}

// printScenarioRun prints the results of a run of scenarios, the builds that a person declared, the
// blocked hosts, and where the report is.
func printScenarioRun(stdout io.Writer, envName string, fin finishedRun) error {
	if err := report.Lines(stdout, fin.record); err != nil {
		return err
	}
	if err := report.DeclaredBuilds(stdout, fin.record); err != nil {
		return err
	}
	report.WriteBlocked(stdout, envName, "scenario", fin.blocked)
	report.WriteDocWarnings(stdout, envName, fin.docs)
	report.WriteMisses(stdout, string(fin.record.ID), fin.misses)
	fmt.Fprintf(stdout, "report: %s\n", fin.reportPath)
	return nil
}

// pauseForPerson is the callback of --pause-on-fail. It says what went wrong and which elements of the
// screen look most like the one that is missing, then waits for a line from the same reader that the run
// asks its questions with. The read ends when the run is cancelled, so Ctrl+C still stops the run.
func pauseForPerson(stdout io.Writer, answers *lineReader) func(context.Context, replay.Pause) {
	return func(ctx context.Context, p replay.Pause) {
		where := fmt.Sprintf("scenario %s step %s", p.Scenario, p.Step)
		if p.Flow != "" {
			where += " (flow " + p.Flow + ")"
		}
		fmt.Fprintf(stdout, "\npaused at %s: %s\n", terminalSafe(oneLine(where)), terminalSafe(oneLine(p.Reason)))
		if p.Screen != "" {
			fmt.Fprintf(stdout, "  screen: %s\n", terminalSafe(oneLine(p.Screen)))
		}
		if p.Screenshot != "" {
			fmt.Fprintf(stdout, "  screenshot: %s, in the run directory\n", terminalSafe(oneLine(p.Screenshot)))
		}
		if len(p.Nearby) > 0 {
			fmt.Fprintln(stdout, "  closest elements on the screen:")
			for _, c := range p.Nearby {
				fmt.Fprintf(stdout, "    %s\n", terminalSafe(oneLine(c.Why)))
			}
		}
		fmt.Fprintln(stdout, "The device stays on this screen. Press Enter to go on.")
		answers.read(ctx)
	}
}
