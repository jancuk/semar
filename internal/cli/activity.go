package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/activity"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/idcheck"
)

const latestRun = "latest"

var passes = []domain.Pass{domain.PassSetup, domain.PassScenario, domain.PassExplore, domain.PassSecurity, domain.PassCleanup}

type activityFlags struct {
	writes   bool
	scenario string
	pass     string
	dir      string
}

func newActivityCommand() *cobra.Command {
	var f activityFlags
	cmd := &cobra.Command{
		Use:   "activity <run id|latest>",
		Short: "Print what a run did, in order",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return printActivity(cmd.Context(), cmd.OutOrStdout(), f, args[0])
		},
	}
	cmd.Flags().BoolVar(&f.writes, "writes", false, "show only the actions that create, update, or delete")
	cmd.Flags().StringVar(&f.scenario, "scenario", "", "show only the actions of this scenario")
	cmd.Flags().StringVar(&f.pass, "pass", "", "show only the actions of this pass: setup, scenario, explore, security, or cleanup")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func printActivity(ctx context.Context, stdout io.Writer, f activityFlags, arg string) error {
	filter := activity.Filter{WritesOnly: f.writes, Scenario: domain.ScenarioID(f.scenario), Pass: domain.Pass(f.pass)}
	if f.scenario != "" {
		if err := idcheck.Check("scenario ID", f.scenario); err != nil {
			return err
		}
	}
	if f.pass != "" && !slices.Contains(passes, filter.Pass) {
		return fmt.Errorf("unknown pass %q: use setup, scenario, explore, security, or cleanup", f.pass)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("activity: %w", err)
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
			return fmt.Errorf("no run was found in %s", semarDir(f.dir))
		}
		id = runs[0]
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
