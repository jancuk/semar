package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/status"
)

type statusFlags struct {
	tickets   []string
	env       string
	builds    []string
	run       string
	base      string
	all       bool
	checklist bool
	html      string
	dir       string
}

func newStatusCommand(o options) *cobra.Command {
	var f statusFlags
	cmd := &cobra.Command{
		Use:   "status [environment]",
		Short: "Show where the change stands: what is verified, what fails, and what waits for a person",
		Long: "Show each use case of the change in one group: verified, failing, blocked, needs review, stale, for a person, no test, or deferred.\n" +
			"The change is the commits from the base branch to HEAD, and its tickets are the ones that those commits name. --all shows every use case.\n" +
			"Exit 0 when every use case is verified or deferred, 1 when one failed or a person recorded a failure, and 2 otherwise.\n" +
			"With no environment, and several in semar.yaml, the builds are not read, so a pass on any build counts: the exit code is then 2 at best.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if f.env != "" {
					return errors.New("name the environment once: as an argument or with --env")
				}
				f.env = args[0]
			}
			return showStatus(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
		},
	}
	cmd.Flags().StringArrayVar(&f.tickets, "ticket", nil, "show only the use cases of this ticket, or of this group in the ID; repeat for more")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment of semar.yaml whose builds count (the same as the argument)")
	cmd.Flags().StringArrayVar(&f.builds, "build", nil, "with an environment, the commit that a service with no build probe runs, as <commit> or <service>=<commit>; repeat for more")
	cmd.Flags().StringVar(&f.run, "run", "", "read the results of this run only, and print the manual checklist after the table")
	cmd.Flags().StringVar(&f.base, "base", "", "the branch that the change is measured against (default: change.base, else the default branch of origin, else main)")
	cmd.Flags().BoolVar(&f.all, "all", false, "show every use case, not only those of the change")
	cmd.Flags().BoolVar(&f.checklist, "checklist", false, "print the manual checklist instead of the table")
	cmd.Flags().StringVar(&f.html, "html", "", "also write the status as one HTML file that needs no network, for a person with no command line")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func showStatus(ctx context.Context, stdout, stderr io.Writer, o options, f statusFlags) error {
	for _, k := range f.tickets {
		if err := idcheck.Check("ticket", k); err != nil {
			return err
		}
	}
	if len(f.builds) > 0 && f.env == "" {
		return errors.New("--build needs an environment: it names the build that a service without a probe runs now")
	}
	if f.run != "" {
		if err := idcheck.Check("run ID", f.run); err != nil {
			return err
		}
	}
	cfg, err := loadConfigOrNone(f.dir)
	if err != nil {
		return err
	}
	all, err := loadObligations(f.dir)
	if err != nil {
		return err
	}

	in := status.Input{}
	var changed *domain.ChangeSet
	var obs []obligation.Located
	switch {
	case len(f.tickets) > 0:
		in.Tickets = f.tickets
		in.Scope = "the tickets named"
		obs = slices.DeleteFunc(slices.Clone(all), func(l obligation.Located) bool { return !slices.Contains(f.tickets, ticketKey(l)) })
		if len(obs) == 0 {
			fmt.Fprintf(stdout, "no use case: no obligation belongs to %s\n", terminalSafe(strings.Join(f.tickets, ", ")))
			return exitError{ExitUndecided}
		}
	default:
		lc, err := readLocalChange(ctx, f.dir, cfg, f.base)
		if err != nil {
			return err
		}
		if lc.Repo {
			in.Branch, in.Base = lc.Change.Head, lc.Change.Base
		}
		if !lc.Repo || f.all {
			obs = all
			break
		}
		in.Tickets = changeTickets(lc.Change)
		if len(in.Tickets) == 0 {
			fmt.Fprintf(stdout, "On %s against %s\nno ticket: no commit of the change names one, so there is no use case to show. Name them with --ticket, or show every use case with --all\n",
				terminalSafe(lc.Change.Head), terminalSafe(lc.Change.Base))
			return exitError{ExitUndecided}
		}
		have := map[string]bool{}
		for _, l := range all {
			have[ticketKey(l)] = true
			if slices.Contains(in.Tickets, ticketKey(l)) {
				obs = append(obs, l)
			}
		}
		for _, t := range in.Tickets {
			if !have[t] {
				in.NotPlanned = append(in.NotPlanned, t)
			}
		}
		changed = &lc.Change
		for _, id := range lc.Change.NoTicket {
			in.NoTicket = append(in.NoTicket, noTicketCommit(lc, id))
		}
	}
	if len(obs) == 0 && len(in.NotPlanned) == 0 {
		fmt.Fprintln(stdout, "no use case: there is no obligation in .semar/obligations")
		return exitError{ExitUndecided}
	}

	var current []domain.BuildRef
	in.Environment, current, in.BuildsRead, err = readBuilds(ctx, stderr, f.dir, cfg, f.env, f.builds)
	if err != nil {
		return err
	}
	rep, err := statusReport(stderr, o, f.dir, in, obs, domain.RunID(f.run), current, changed)
	if err != nil {
		return err
	}
	in = rep.Input
	if f.html != "" {
		var page bytes.Buffer
		if err := status.HTML(&page, rep); err != nil {
			return err
		}
		if err := atomicfile.Write(f.html, page.Bytes()); err != nil {
			return fmt.Errorf("write %s: %w", f.html, err)
		}
		fmt.Fprintf(stderr, "semar: wrote %s\n", f.html)
	}
	if f.checklist {
		if err := printChecklist(stdout, in.Entries); err != nil {
			return err
		}
	} else {
		if err := status.Text(stdout, rep); err != nil {
			return err
		}
		if f.run != "" && needsAPerson(in.Entries) {
			fmt.Fprintln(stdout)
			if err := obligation.Checklist(stdout, in.Entries, ""); err != nil {
				return err
			}
		}
	}
	if rep.Code != ExitOK {
		return exitError{rep.Code}
	}
	return nil
}

// noTicketCommit is a commit of the change that names no ticket, as its short ID and its subject.
func noTicketCommit(lc localChange, id string) string {
	for _, c := range lc.Change.Commits {
		if c.ID == id {
			return fmt.Sprintf("%s %q", shortID(c.ID), c.Subject)
		}
	}
	return shortID(id)
}

func shortID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}
