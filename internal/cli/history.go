package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/activity"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/idcheck"
)

const latestRun = "latest"

var passes = []domain.Pass{domain.PassSetup, domain.PassScenario, domain.PassExplore, domain.PassSecurity, domain.PassCleanup, domain.PassTrial}

const defaultLogLength = 20

func newLogCommand() *cobra.Command {
	var n int
	var dir string
	cmd := &cobra.Command{
		Use:   "log [-n <count>]",
		Short: "List the stored runs, newest first",
		Long:  "List the stored runs, newest first: the run, the time, the environment, the builds, and how many results of each outcome it has. Read one with semar show <run>.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listRuns(cmd.OutOrStdout(), dir, n)
		},
	}
	cmd.Flags().IntVarP(&n, "count", "n", defaultLogLength, "how many runs to list")
	addDirFlag(cmd, &dir)
	return cmd
}

func listRuns(stdout io.Writer, dir string, n int) error {
	if n < 1 {
		return errors.New("-n must be at least 1")
	}
	if err := requireSemarDir(dir); err != nil {
		return err
	}
	store, err := evidence.Open(semarDir(dir))
	if err != nil {
		return err
	}
	ids, err := store.Runs()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Fprintln(stdout, "no run yet: semar run makes one")
		return nil
	}
	for _, id := range ids[:min(n, len(ids))] {
		run, _, err := store.LoadRun(id)
		switch {
		case errors.Is(err, evidence.ErrInterrupted):
			fmt.Fprintf(stdout, "%s  did not finish\n", terminalSafe(string(id)))
			continue
		case err != nil:
			fmt.Fprintf(stdout, "%s  could not be read: %s\n", terminalSafe(string(id)), oneLine(terminalSafe(err.Error())))
			continue
		}
		fmt.Fprintln(stdout, runLine(run))
	}
	return nil
}

// runLine is one line of the log: the run, its time, its environment, its builds, and its counts.
func runLine(run domain.Run) string {
	parts := []string{terminalSafe(string(run.ID)), run.StartedAt.UTC().Format("2006-01-02 15:04")}
	env := terminalSafe(oneLine(run.Environment))
	if run.Purpose != "" {
		env += " (" + terminalSafe(oneLine(run.Purpose)) + ")"
	}
	parts = append(parts, env)
	var builds []string
	for _, b := range run.BuildsEnd {
		builds = append(builds, fmt.Sprintf("%s %s", terminalSafe(oneLine(b.Service)), domain.ShortCommit(b.Commit)))
	}
	if len(builds) > 0 {
		parts = append(parts, strings.Join(builds, ", "))
	}
	parts = append(parts, outcomeCounts(run))
	return strings.Join(parts, "  ")
}

// outcomeCounts says how many results of each outcome the run has. A run of journeys counts its
// journeys.
func outcomeCounts(run domain.Run) string {
	counts := map[string]int{}
	var order []string
	add := func(o string) {
		if counts[o] == 0 {
			order = append(order, o)
		}
		counts[o]++
	}
	if len(run.Journeys) > 0 {
		for _, j := range run.Journeys {
			add(string(j.Outcome))
		}
	} else {
		for _, r := range run.Results {
			add(string(r.Outcome))
		}
	}
	if len(order) == 0 {
		return "no result"
	}
	slices.Sort(order)
	out := make([]string, len(order))
	for i, o := range order {
		out[i] = fmt.Sprintf("%s %d", strings.ReplaceAll(o, "_", " "), counts[o])
	}
	return strings.Join(out, ", ")
}

type showFlags struct {
	activity bool
	writes   bool
	scenario string
	pass     string
	dir      string
}

func newShowCommand() *cobra.Command {
	var f showFlags
	cmd := &cobra.Command{
		Use:   "show [run]",
		Short: "Print the report of a run, or what it did",
		Long:  "Print the report of a run, and of the newest run when none is named. With --activity, print what the run did, in order.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := latestRun
			if len(args) == 1 {
				arg = args[0]
			}
			return showRun(cmd.Context(), cmd.OutOrStdout(), f, arg)
		},
	}
	cmd.Flags().BoolVar(&f.activity, "activity", false, "print what the run did, in order, and not its report")
	cmd.Flags().BoolVar(&f.writes, "writes", false, "with --activity, show only the actions that create, update, or delete")
	cmd.Flags().StringVar(&f.scenario, "scenario", "", "with --activity, show only the actions of this scenario")
	cmd.Flags().StringVar(&f.pass, "pass", "", "with --activity, show only the actions of this pass: setup, scenario, explore, security, cleanup, or trial")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func showRun(ctx context.Context, stdout io.Writer, f showFlags, arg string) error {
	if !f.activity && (f.writes || f.scenario != "" || f.pass != "") {
		return errors.New("--writes, --scenario, and --pass filter the activity: add --activity")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("show: %w", err)
	}
	if err := requireSemarDir(f.dir); err != nil {
		return err
	}
	store, err := evidence.Open(semarDir(f.dir))
	if err != nil {
		return err
	}
	id := domain.RunID(arg)
	if arg == latestRun {
		runs, err := store.Runs()
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			return fmt.Errorf("no run was found in %s: semar run makes one", semarDir(f.dir))
		}
		id = runs[0]
	} else if err := idcheck.Check("run ID", arg); err != nil {
		return err
	}
	if f.activity {
		return printActivity(stdout, store, f, id)
	}
	data, err := os.ReadFile(filepath.Join(store.Root(), "runs", string(id), reportFile))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("run %s was not found, or it has no report", terminalSafe(oneLine(string(id))))
	}
	if err != nil {
		return err
	}
	_, err = stdout.Write([]byte(terminalSafe(string(data))))
	return err
}

func printActivity(stdout io.Writer, store *evidence.Store, f showFlags, id domain.RunID) error {
	filter := activity.Filter{WritesOnly: f.writes, Scenario: domain.ScenarioID(f.scenario), Pass: domain.Pass(f.pass)}
	if f.scenario != "" {
		if err := idcheck.Check("scenario ID", f.scenario); err != nil {
			return err
		}
	}
	if f.pass != "" && !slices.Contains(passes, filter.Pass) {
		return fmt.Errorf("unknown pass %q: use setup, scenario, explore, security, cleanup, or trial", f.pass)
	}
	_, acts, err := store.LoadRun(id)
	interrupted := errors.Is(err, evidence.ErrInterrupted)
	if interrupted {
		acts, err = store.Activity(id)
	}
	if err != nil {
		return err
	}
	if interrupted {
		fmt.Fprintf(stdout, "run %s: the run did not finish (it was interrupted or stopped by an error), so this activity may be incomplete and no hash covers it\n", id)
	}
	return activity.Timeline(stdout, acts, filter)
}

// requireSemarDir keeps a command that only reads from creating a .semar directory.
func requireSemarDir(repo string) error {
	dir := semarDir(repo)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("no Semar repository at %s: %w", repo, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("no Semar repository at %s: %s is not a directory", repo, dir)
	}
	return nil
}
