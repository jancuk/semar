package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/changeset"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/words"
)

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
func currentBuilds(ctx context.Context, dir string, cfg config.Config, envName string, values []string) ([]domain.BuildRef, error) {
	_, env, err := cfg.Environment(envName)
	if err != nil {
		return nil, err
	}
	declared, err := readDeclaredBuilds(ctx, dir, values, env.Services)
	if err != nil {
		return nil, err
	}
	prober := declared.prober(env.Services)
	var out []domain.BuildRef
	for _, s := range env.Services {
		if !declared.has(s.Name) && s.Build == nil {
			continue
		}
		b, err := prober.Probe(ctx, s.Name)
		if err != nil {
			return nil, fmt.Errorf("the build of %s could not be read (%s), so results cannot be matched to the current build; leave out the environment to skip the match",
				terminalSafe(s.Name), oneLine(terminalSafe(err.Error())))
		}
		out = append(out, b)
	}
	return out, nil
}

// loadConfigOrNone reads semar.yaml. A repository with none gets the empty configuration, which
// names no environment, so that a command that only reads can still list the use cases.
func loadConfigOrNone(dir string) (config.Config, error) {
	cfg, err := config.Load(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return config.Config{Tickets: config.Tickets{Pattern: changeset.DefaultTicketPattern}}, nil
	}
	return cfg, err
}

// readBuilds reads the builds for a command that matches results to them. A named environment must
// be readable. With none named and exactly one in the configuration, that one is read, and when it
// cannot be, the command goes on with the builds not read and says so.
func readBuilds(ctx context.Context, stderr io.Writer, dir string, cfg config.Config, named string, declared []string) (envName string, builds []domain.BuildRef, read bool, err error) {
	if len(declared) > 0 && named == "" {
		return "", nil, false, errors.New("--build needs an environment: it names the build that a service without a probe runs now")
	}
	envName = named
	if envName == "" && len(cfg.Environments) == 1 {
		for n := range cfg.Environments {
			envName = n
		}
		builds, err = currentBuilds(ctx, dir, cfg, envName, nil)
		if err != nil {
			fmt.Fprintf(stderr, "semar: %s; the results are not matched to the current build\n", oneLine(terminalSafe(err.Error())))
			return envName, nil, false, nil
		}
		return envName, builds, true, nil
	}
	if envName == "" {
		fmt.Fprintln(stderr, "semar: the builds that the environment runs now were not read; name an environment to count only results on the current builds")
		return "", nil, false, nil
	}
	builds, err = currentBuilds(ctx, dir, cfg, envName, declared)
	if err != nil {
		return "", nil, false, err
	}
	return envName, builds, true, nil
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
	links, moreLinks, err := store.Links(now)
	if err != nil {
		return nil, err
	}
	res.Links = links
	more = append(more, moreLinks...)
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
	c := obligation.Cover{Journey: string(jr.Journey), Found: true, Outcome: string(jr.Outcome), Run: run.ID, Builds: m.Services, Word: reasonWord(string(jr.Outcome), jr.Reasons)}
	if len(jr.Reasons) > 0 {
		c.Reason = jr.Reasons[0].Text
	}
	return c
}

// scenarioRun is true for a run that the ledger reads: a run of scenarios, or of a journey.
func scenarioRun(run domain.Run) bool {
	return run.Purpose == "" || run.Purpose == domain.PurposeJourney
}

// reasonWord is the word of a stored result when it is not the outcome itself.
func reasonWord(outcome string, reasons []domain.Reason) string {
	texts := make([]string, len(reasons))
	for i, r := range reasons {
		texts[i] = r.Text
	}
	if w := words.Of(outcome, texts); w != outcome {
		return w
	}
	return ""
}

// coverOf is the result of a scenario as a use case sees it. A result that stored the expectation
// hash of its scenario counts only while the scenario file has the same one. A result of an older
// run has none, and counts as it always did.
func coverOf(repo string, run domain.Run, r domain.ScenarioResult) obligation.Cover {
	c := obligation.Cover{Scenario: r.Scenario, Found: true, Outcome: string(r.Outcome), Run: run.ID, Builds: run.BuildsEnd, Word: reasonWord(string(r.Outcome), r.Reasons), Attempts: words.Attempts(string(r.Outcome), r.Flaky, len(r.Attempts), r.AttemptsAllowed)}
	c.ByModel, c.Results = r.ModelCount()
	if r.ExpectHash != "" {
		sc, err := loadScenario(repo, r.Scenario)
		c.ScenarioChanged = err != nil || domain.ExpectHash(sc) != r.ExpectHash
	}
	if len(r.Reasons) > 0 {
		c.Reason = r.Reasons[0].Text
	}
	return c
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
		return obligation.Located{}, fmt.Errorf("obligation %s was not found in .semar/obligations; list them with semar status --all", terminalSafe(oneLine(id)))
	}
	return obs[i], nil
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
		return skip(fmt.Errorf("%s; semar status names every problem", problems[0]))
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
