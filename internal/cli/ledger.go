package cli

import (
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
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/obligation"
)

const maxDeferDays = 180

const dayLayout = "2006-01-02"

type ledgerFlags struct {
	tickets   []string
	env       string
	builds    []string
	run       string
	checklist bool
	dir       string
}

func newLedgerCommand(o options) *cobra.Command {
	var f ledgerFlags
	cmd := &cobra.Command{
		Use:   "ledger",
		Short: "Show the state of every use case, defer one, or record what a person checked",
		Long: "Show one state for each use case of .semar/obligations: verified, failed, blocked, needs_a_person, or deferred.\n" +
			"Exit 0 when every use case is verified or deferred and every manual result is passed, 1 when a use case failed or a manual result is failed, and 2 otherwise.\n" +
			"Without --env the builds are not read, so a pass on any build counts: the exit code is then 2 at best, and the first line says so.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return showLedger(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
		},
	}
	cmd.Flags().StringArrayVar(&f.tickets, "ticket", nil, "show only the use cases of this ticket, or of this group in the ID; repeat for more")
	cmd.Flags().StringVar(&f.env, "env", "", "read the builds that this environment of semar.yaml runs now, and count only results on them")
	cmd.Flags().StringArrayVar(&f.builds, "build", nil, "with --env, the commit that a service with no build probe runs, as <commit> or <service>=<commit>; repeat for more")
	cmd.Flags().StringVar(&f.run, "run", "", "read the results of this run only, and print the manual checklist after the table")
	cmd.Flags().BoolVar(&f.checklist, "checklist", false, "print the manual checklist instead of the table")
	cmd.PersistentFlags().StringVar(&f.dir, "dir", ".", "the root of the repository that holds .semar")
	cmd.AddCommand(newLedgerDeferCommand(o, &f.dir), newLedgerRecordCommand(o, &f.dir))
	return cmd
}

func showLedger(ctx context.Context, stdout, stderr io.Writer, o options, f ledgerFlags) error {
	for _, k := range f.tickets {
		if err := idcheck.Check("ticket", k); err != nil {
			return err
		}
	}
	if f.run != "" {
		if err := idcheck.Check("run ID", f.run); err != nil {
			return err
		}
	}
	if len(f.builds) > 0 && f.env == "" {
		return errors.New("--build needs --env: it names the build that a service without a probe runs now")
	}
	obs, err := loadObligations(f.dir)
	if err != nil {
		return err
	}
	if len(obs) == 0 {
		fmt.Fprintln(stdout, "no use case: there is no obligation in .semar/obligations")
		return exitError{ExitUndecided}
	}
	if len(f.tickets) > 0 {
		obs = slices.DeleteFunc(obs, func(l obligation.Located) bool { return !slices.Contains(f.tickets, ticketKey(l)) })
		if len(obs) == 0 {
			fmt.Fprintf(stdout, "no use case: no obligation belongs to %s\n", terminalSafe(strings.Join(f.tickets, ", ")))
			return exitError{ExitUndecided}
		}
	}

	var current []domain.BuildRef
	if f.env != "" {
		if current, err = currentBuilds(ctx, f); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(stderr, "semar: the builds that the environment runs now were not read; pass --env to count only results on the current builds")
	}
	entries, err := deriveEntries(stderr, o, f.dir, obs, domain.RunID(f.run), current)
	if err != nil {
		return err
	}

	switch {
	case f.checklist:
		err = printChecklist(stdout, entries)
	default:
		if err = printLedger(stdout, entries, f.env != ""); err == nil && f.run != "" {
			if needsAPerson(entries) {
				fmt.Fprintln(stdout)
				err = obligation.Checklist(stdout, entries, "")
			}
		}
	}
	if err != nil {
		return err
	}
	if code := ledgerExitCode(entries, f.env != ""); code != ExitOK {
		return exitError{code}
	}
	return nil
}

// loadObligations reads every file of .semar/obligations. A problem in any file is an error that
// lists each one, so that no state is derived from a file that cannot be trusted.
func loadObligations(repo string) ([]obligation.Located, error) {
	list, problems, err := obligation.Load(repo)
	if err != nil {
		return nil, errors.New(oneLine(terminalSafe(err.Error())))
	}
	if len(problems) > 0 {
		lines := make([]string, len(problems))
		for i, p := range problems {
			lines[i] = oneLine(terminalSafe(p.String()))
		}
		return nil, fmt.Errorf("%s\nfix the obligations files; nothing was derived", strings.Join(lines, "\n"))
	}
	return list, nil
}

// ticketKey is the ticket of an obligation, or the group of its ID when it has none.
func ticketKey(l obligation.Located) string {
	if l.Ticket != "" {
		return l.Ticket
	}
	group, _, _ := strings.Cut(l.ID, "/")
	return group
}

// currentBuilds reads the builds that the environment runs now. A build that cannot be read is an
// error, because counting every result as current would claim more than was seen.
func currentBuilds(ctx context.Context, f ledgerFlags) ([]domain.BuildRef, error) {
	cfg, err := config.Load(f.dir)
	if err != nil {
		return nil, err
	}
	_, env, err := cfg.Environment(f.env)
	if err != nil {
		return nil, err
	}
	declared, err := build.ParseDeclared(f.builds, env.Services)
	if err != nil {
		return nil, err
	}
	prober := build.NewDeclared(declared, build.NewHTTP(env.Services, nil))
	var out []domain.BuildRef
	for _, s := range env.Services {
		if _, ok := declared[s.Name]; !ok && s.Build == nil {
			continue
		}
		b, err := prober.Probe(ctx, s.Name)
		if err != nil {
			return nil, fmt.Errorf("the build of %s could not be read (%s), so results cannot be matched to the current build; leave out --env to skip the match",
				terminalSafe(s.Name), oneLine(terminalSafe(err.Error())))
		}
		out = append(out, b)
	}
	return out, nil
}

// deriveEntries gives the state of each obligation from the stored runs, or from the run named by
// only, and from the stored deferrals and manual results.
func deriveEntries(stderr io.Writer, o options, repo string, obs []obligation.Located, only domain.RunID, current []domain.BuildRef) ([]obligation.Entry, error) {
	res, err := storedResults(stderr, repo, only, obs)
	if err != nil {
		return nil, err
	}
	res.Current = current
	return deriveWith(o, repo, obs, res)
}

func deriveWith(o options, repo string, obs []obligation.Located, res obligation.Results) ([]obligation.Entry, error) {
	store := obligation.NewStore(semarDir(repo))
	now := o.Clock()
	deferrals, problems, err := store.Deferrals(now)
	if err != nil {
		return nil, err
	}
	manual, more, err := store.Manual(now)
	if err != nil {
		return nil, err
	}
	// The store is in the repository under test. A record that the commands would have refused is
	// never counted, and it stops the derivation as a problem in an obligations file does.
	if problems = append(problems, more...); len(problems) > 0 {
		lines := make([]string, len(problems))
		for i, p := range problems {
			lines[i] = oneLine(terminalSafe(p))
		}
		return nil, fmt.Errorf("%s\nfix or remove those records; nothing was derived", strings.Join(lines, "\n"))
	}
	return obligation.Derive(obs, res, deferrals, manual, now), nil
}

// storedResults reads the newest result of each scenario that an obligation covers from the
// complete runs of scenarios and of journeys, newest first, and the newest result of each journey
// that an obligation names. A run of another purpose is never read: a trial and a fixture check
// send requests with no verdict.
func storedResults(stderr io.Writer, repo string, only domain.RunID, obs []obligation.Located) (obligation.Results, error) {
	files, err := scenarioFiles(repo)
	if err != nil {
		return obligation.Results{}, err
	}
	res := obligation.Results{Scenarios: make(map[domain.ScenarioID]obligation.Cover, len(files))}
	for id := range files {
		res.Scenarios[id] = obligation.Cover{Scenario: id}
	}
	missing := map[domain.ScenarioID]bool{}
	for _, l := range obs {
		for _, id := range l.Covers {
			if _, ok := res.Scenarios[id]; ok {
				missing[id] = true
			}
		}
	}
	missingJourneys, err := journeyCovers(&res, repo, obs)
	if err != nil {
		return obligation.Results{}, err
	}
	if len(missing) == 0 && len(missingJourneys) == 0 {
		return res, nil
	}

	store, err := evidence.Open(semarDir(repo))
	if err != nil {
		return obligation.Results{}, err
	}
	ids := []domain.RunID{only}
	if only == "" {
		if ids, err = store.Runs(); err != nil {
			return obligation.Results{}, err
		}
	}
	for _, id := range ids {
		if len(missing) == 0 && len(missingJourneys) == 0 {
			break
		}
		run, _, err := store.LoadRun(id)
		switch {
		case only != "" && errors.Is(err, evidence.ErrInterrupted):
			return obligation.Results{}, fmt.Errorf("run %s did not finish, so it has no results", id)
		case errors.Is(err, evidence.ErrInterrupted):
			continue
		case only != "" && err != nil:
			return obligation.Results{}, err
		case err != nil:
			fmt.Fprintf(stderr, "semar: run %s could not be read, so its results are not counted: %s\n", terminalSafe(string(id)), oneLine(terminalSafe(err.Error())))
			continue
		case !scenarioRun(run):
			if only != "" {
				return obligation.Results{}, fmt.Errorf("run %s is a run for %s, not of scenarios, so it has no results to count", id, terminalSafe(run.Purpose))
			}
			continue
		}
		for _, r := range run.Results {
			if missing[r.Scenario] {
				res.Scenarios[r.Scenario] = coverOf(repo, run, r)
				delete(missing, r.Scenario)
			}
		}
		for _, jr := range run.Journeys {
			if missingJourneys[jr.Journey] {
				res.Journeys[string(jr.Journey)] = journeyCoverOf(run, jr)
				delete(missingJourneys, jr.Journey)
			}
		}
	}
	return res, nil
}

// journeyCovers makes the journeys that an obligation names known: a key of Results.Journeys for
// each journey file, with no result yet. With no obligation that names a journey it leaves
// Results.Journeys alone, and the repository's journeys are not read. A journey file that is not
// valid is known too, so that a use case is not told its journey was deleted. It returns the
// journeys that still need a result.
func journeyCovers(res *obligation.Results, repo string, obs []obligation.Located) (map[domain.JourneyID]bool, error) {
	named := map[domain.JourneyID]bool{}
	for _, l := range obs {
		for _, id := range l.Journeys {
			named[domain.JourneyID(id)] = true
		}
	}
	if len(named) == 0 {
		return nil, nil
	}
	js, problems, err := loadJourneys(repo)
	if err != nil {
		return nil, err
	}
	res.Journeys = map[string]obligation.Cover{}
	for _, j := range js {
		res.Journeys[string(j.ID)] = obligation.Cover{Journey: string(j.ID)}
	}
	for _, p := range problems {
		stem := strings.TrimSuffix(strings.TrimPrefix(p.File, journeysRel+"/"), filepath.Ext(p.File))
		if _, ok := res.Journeys[stem]; !ok {
			res.Journeys[stem] = obligation.Cover{Journey: stem}
		}
	}
	missing := map[domain.JourneyID]bool{}
	for id := range named {
		if _, ok := res.Journeys[string(id)]; ok {
			missing[id] = true
		}
	}
	return missing, nil
}

// journeyCoverOf is the result of a journey as a use case sees it. The builds are the manifest at
// the end, or at the start when the end was not read.
func journeyCoverOf(run domain.Run, jr domain.JourneyResult) obligation.Cover {
	m := jr.Manifest
	if jr.ManifestEnd != nil {
		m = *jr.ManifestEnd
	}
	c := obligation.Cover{Journey: string(jr.Journey), Found: true, Outcome: string(jr.Outcome), Run: run.ID, Builds: m.Services}
	if len(jr.Reasons) > 0 {
		c.Reason = jr.Reasons[0].Text
	}
	return c
}

// scenarioRun is true for a run that the ledger reads: a run of scenarios, or of a journey.
func scenarioRun(run domain.Run) bool {
	return run.Purpose == "" || run.Purpose == domain.PurposeJourney
}

// coverOf is the result of a scenario as a use case sees it. A result that stored the expectation
// hash of its scenario counts only while the scenario file has the same one. A result of an older
// run has none, and counts as it always did.
func coverOf(repo string, run domain.Run, r domain.ScenarioResult) obligation.Cover {
	c := obligation.Cover{Scenario: r.Scenario, Found: true, Outcome: string(r.Outcome), Run: run.ID, Builds: run.BuildsEnd}
	if r.ExpectHash != "" {
		sc, err := loadScenario(repo, r.Scenario)
		c.ScenarioChanged = err != nil || domain.ExpectHash(sc) != r.ExpectHash
	}
	if len(r.Reasons) > 0 {
		c.Reason = r.Reasons[0].Text
	}
	return c
}

var groupNames = map[obligation.State]string{
	obligation.Failed:       "Failed",
	obligation.Blocked:      "Blocked",
	obligation.NeedsAPerson: "Needs a person",
	obligation.Deferred:     "Deferred",
	obligation.Verified:     "Verified",
}

func groupName(e obligation.Entry) string {
	if e.NoReason {
		return "No test and no reason"
	}
	return groupNames[e.State]
}

// buildsUnread ends the summary line when the builds that the environment runs now were not read.
const buildsUnread = ". Builds were not read, so no result is matched to the current build."

// printLedger writes the summary line, then the entries in groups, in the order that Derive gives.
func printLedger(w io.Writer, entries []obligation.Entry, buildsRead bool) error {
	line := obligation.SummaryLine(entries)
	if !buildsRead {
		line = strings.TrimSuffix(line, ".") + buildsUnread
	}
	if _, err := fmt.Fprintln(w, oneLine(terminalSafe(line))); err != nil {
		return err
	}
	for i := 0; i < len(entries); {
		name := groupName(entries[i])
		j := i
		for j < len(entries) && groupName(entries[j]) == name {
			j++
		}
		if _, err := fmt.Fprintf(w, "\n%s (%d)\n", name, j-i); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, e := range entries[i:j] {
			row := "  " + oneLine(terminalSafe(e.Obligation.ID)) + "\t" + oneLine(terminalSafe(e.Obligation.Title))
			if len(e.Reasons) > 0 {
				row += "\t" + oneLine(terminalSafe(e.Reasons[0]))
			}
			fmt.Fprintln(tw, row)
			for _, more := range e.Reasons[min(1, len(e.Reasons)):] {
				fmt.Fprintf(tw, "\t\t%s\n", oneLine(terminalSafe(more)))
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		i = j
	}
	return nil
}

func needsAPerson(entries []obligation.Entry) bool {
	return slices.ContainsFunc(entries, func(e obligation.Entry) bool {
		return e.State == obligation.NeedsAPerson && e.Obligation.Manual != nil
	})
}

func printChecklist(w io.Writer, entries []obligation.Entry) error {
	if !needsAPerson(entries) {
		_, err := fmt.Fprintln(w, "no use case needs a person")
		return err
	}
	return obligation.Checklist(w, entries, "")
}

// ledgerExitCode is 0 when every use case is verified or deferred and no manual result failed, 1
// when a use case failed or a manual result failed, and 2 otherwise. With the builds not read, a
// pass on any build counts, so nothing is confirmed current and the code is 2 at best.
func ledgerExitCode(entries []obligation.Entry, buildsRead bool) int {
	code := ExitOK
	for _, e := range entries {
		switch {
		case e.State == obligation.Failed || (e.Manual != nil && e.Manual.Result == "failed"):
			return ExitFailed
		case e.State != obligation.Verified && e.State != obligation.Deferred:
			code = ExitUndecided
		}
	}
	if !buildsRead && code == ExitOK {
		return ExitUndecided
	}
	return code
}

type deferFlags struct {
	reason, until string
}

func newLedgerDeferCommand(o options, dir *string) *cobra.Command {
	var f deferFlags
	cmd := &cobra.Command{
		Use:   "defer <id> --reason <text> --until <YYYY-MM-DD>",
		Short: "Leave a use case untested until a day, under your name",
		Long:  "Store a deferral. The day is after today and at most 180 days ahead, and the use case shows as deferred until it passes. A use case that failed still shows as failed.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deferUseCase(cmd.OutOrStdout(), o, *dir, args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.reason, "reason", "", "why the use case is left untested: one line of at most 300 characters")
	cmd.Flags().StringVar(&f.until, "until", "", "the last day of the deferral, as YYYY-MM-DD")
	return cmd
}

// person is the name from the person's own environment, never from the repository.
func person(o options, what string) (string, error) {
	name, err := o.User()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("%s has no name; set git config user.name", what)
	}
	return name, nil
}

func findObligation(obs []obligation.Located, id string) (obligation.Located, error) {
	i := slices.IndexFunc(obs, func(l obligation.Located) bool { return l.ID == id })
	if i < 0 {
		return obligation.Located{}, fmt.Errorf("obligation %s was not found in .semar/obligations; list them with semar ledger", terminalSafe(oneLine(id)))
	}
	return obs[i], nil
}

func deferUseCase(stdout io.Writer, o options, repo, id string, f deferFlags) error {
	switch {
	case f.reason == "":
		return errors.New("--reason is required: say why the use case is left untested")
	case f.until == "":
		return errors.New("--until is required: the last day of the deferral, as YYYY-MM-DD")
	}
	now := o.Clock().UTC()
	day, err := time.Parse(dayLayout, f.until)
	if err != nil {
		return fmt.Errorf("--until %q is not a day: use YYYY-MM-DD", terminalSafe(oneLine(f.until)))
	}
	today := now.Truncate(24 * time.Hour)
	limit := today.AddDate(0, 0, maxDeferDays)
	switch {
	case !day.After(today):
		return fmt.Errorf("--until %s must be after today (%s)", f.until, today.Format(dayLayout))
	case day.After(limit):
		return fmt.Errorf("--until %s is too far ahead: a deferral is at most %d days, so the last day is %s", f.until, maxDeferDays, limit.Format(dayLayout))
	}
	obs, err := loadObligations(repo)
	if err != nil {
		return err
	}
	if _, err := findObligation(obs, id); err != nil {
		return err
	}
	name, err := person(o, "the person who defers")
	if err != nil {
		return err
	}
	until := day.Add(24*time.Hour - time.Second)
	d := obligation.Deferral{Obligation: id, User: name, Identity: obligation.LocalIdentity, At: now, Reason: f.reason, Until: until}
	if err := obligation.NewStore(semarDir(repo)).Defer(d); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "deferred %s until %s as %s (%s)\n", terminalSafe(id), f.until, terminalSafe(oneLine(name)), obligation.LocalIdentity)
	return nil
}

type recordFlags struct{ notes string }

func newLedgerRecordCommand(o options, dir *string) *cobra.Command {
	var f recordFlags
	cmd := &cobra.Command{
		Use:   "record <id> passed|failed --notes <text>",
		Short: "Record what a person saw when they ran the manual checklist of a use case",
		Long:  "Store the result of a manual check as a new record, under your name. It is counted apart and shown as checked by a person. It never makes a use case verified.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return recordResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), o, *dir, args[0], args[1], f)
		},
	}
	cmd.Flags().StringVar(&f.notes, "notes", "", "what you saw, at most 2000 characters")
	return cmd
}

func recordResult(stdout, stderr io.Writer, o options, repo, id, result string, f recordFlags) error {
	if result != "passed" && result != "failed" {
		return fmt.Errorf("the result must be passed or failed, not %q", terminalSafe(oneLine(result)))
	}
	obs, err := loadObligations(repo)
	if err != nil {
		return err
	}
	l, err := findObligation(obs, id)
	if err != nil {
		return err
	}
	entries, err := deriveEntries(stderr, o, repo, []obligation.Located{l}, "", nil)
	if err != nil {
		return err
	}
	if e := entries[0]; e.State != obligation.NeedsAPerson {
		return fmt.Errorf("%s is %s, and only a use case in the state needs_a_person takes the result of a person", terminalSafe(id), e.State)
	}
	name, err := person(o, "the person who records")
	if err != nil {
		return err
	}
	m := obligation.ManualResult{Obligation: id, Result: result, User: name, Identity: obligation.LocalIdentity, At: o.Clock().UTC(), Notes: f.notes}
	if err := obligation.NewStore(semarDir(repo)).Record(m); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "recorded %s for %s as %s (%s), checked by a person\n", result, terminalSafe(id), terminalSafe(oneLine(name)), obligation.LocalIdentity)
	return nil
}

// runCoverage gives the line that opens the report of a run, and the tickets that its detail
// command names. It counts the use cases whose ticket or covers touch a scenario of the run, with
// the results of this run first and its builds as the current ones (D10). Anything that goes wrong
// leaves the line out and says so on stderr, because a report must not fail for a ledger. A run
// that did not finish has no end builds and partial results, so it gives no line.
func runCoverage(stderr io.Writer, o options, repo string, scenarios []domain.Scenario, rec domain.Run) (string, []string) {
	if rec.EndedAt.IsZero() {
		return "", nil
	}
	skip := func(err error) (string, []string) {
		fmt.Fprintf(stderr, "semar: the use case line is left out of the report: %s\n", oneLine(terminalSafe(err.Error())))
		return "", nil
	}
	all, problems, err := obligation.Load(repo)
	switch {
	case err != nil:
		return skip(err)
	case len(problems) > 0:
		return skip(fmt.Errorf("%s; semar ledger names every problem", problems[0]))
	}
	ran := map[domain.ScenarioID]bool{}
	tickets := map[string]bool{}
	for _, sc := range scenarios {
		ran[sc.ID] = true
		if sc.Ticket != "" {
			tickets[sc.Ticket] = true
		}
	}
	journeys := map[string]bool{}
	for _, jr := range rec.Journeys {
		journeys[string(jr.Journey)] = true
	}
	obs := slices.DeleteFunc(all, func(l obligation.Located) bool {
		return !tickets[l.Ticket] && !slices.ContainsFunc(l.Covers, func(id domain.ScenarioID) bool { return ran[id] }) &&
			!slices.ContainsFunc(l.Journeys, func(id string) bool { return journeys[id] })
	})
	if len(obs) == 0 {
		return "", nil
	}
	res, err := storedResults(stderr, repo, "", obs)
	if err != nil {
		return skip(err)
	}
	for _, r := range rec.Results {
		if _, ok := res.Scenarios[r.Scenario]; ok {
			res.Scenarios[r.Scenario] = coverOf(repo, rec, r)
		}
	}
	for _, jr := range rec.Journeys {
		if _, ok := res.Journeys[string(jr.Journey)]; ok {
			res.Journeys[string(jr.Journey)] = journeyCoverOf(rec, jr)
		}
	}
	res.Current = rec.BuildsEnd
	entries, err := deriveWith(o, repo, obs, res)
	if err != nil {
		return skip(err)
	}
	keys := map[string]bool{}
	for _, l := range obs {
		keys[ticketKey(l)] = true
	}
	return obligation.SummaryLine(entries), slices.Sorted(maps.Keys(keys))
}
