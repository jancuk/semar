package cli

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/obligation"
)

const maxDeferDays = 180

const dayLayout = "2006-01-02"

type deferFlags struct {
	reason, until string
	dir           string
}

func newDeferCommand(o options) *cobra.Command {
	var f deferFlags
	cmd := &cobra.Command{
		Use:   "defer <id> --until <YYYY-MM-DD> --reason <text>",
		Short: "Leave a use case untested until a day, under your name",
		Long:  "Store a deferral. The day is after today and at most 180 days ahead, and the use case shows as deferred until it passes. A use case that failed still shows as failed.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deferUseCase(cmd.OutOrStdout(), o, f.dir, args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.reason, "reason", "", "why the use case is left untested: one line of at most 300 characters")
	cmd.Flags().StringVar(&f.until, "until", "", "the last day of the deferral, as YYYY-MM-DD")
	addDirFlag(cmd, &f.dir)
	return cmd
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
