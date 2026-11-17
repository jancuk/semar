package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/changeset"
	"github.com/jancuk/semar/internal/codehost"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/vcs"
)

type diffFlags struct {
	diff    string
	noFetch bool
	base    string // with no diff, the branch that the change of this branch is measured against
}

func addDiffFlags(cmd *cobra.Command, f *diffFlags) {
	cmd.Flags().StringVar(&f.diff, "diff", "", "a compare link of the code host, or a range such as main..release")
	cmd.Flags().BoolVar(&f.noFetch, "no-fetch", false, "use the branches as they are in this clone, without fetching origin")
}

func newChangesCommand() *cobra.Command {
	var f diffFlags
	var dir string
	cmd := &cobra.Command{
		Use:   "changes --diff <link or range>",
		Short: "Print the tickets, commits, and files between two branches, with no model",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printChanges(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, f)
		},
	}
	addDiffFlags(cmd, &f)
	addDirFlag(cmd, &dir)
	return cmd
}

// printChanges reads the configuration only for the ticket pattern, and starts no provider (R20).
func printChanges(ctx context.Context, stdout, stderr io.Writer, dir string, f diffFlags) error {
	cfg, err := config.Load(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		cfg = config.Config{Tickets: config.Tickets{Pattern: changeset.DefaultTicketPattern}}
	case err != nil:
		return err
	}
	ticket, err := regexp.Compile(cfg.Tickets.Pattern)
	if err != nil {
		return fmt.Errorf("tickets.pattern: %w", err)
	}
	_, cs, err := readChange(ctx, stderr, dir, f, ticket)
	if err != nil {
		return err
	}
	return changeset.Table(stdout, cs)
}

// readChange resolves the two branches of a link or a range and reads the change between them.
// A link must name the repository of the origin remote (R8).
func readChange(ctx context.Context, stderr io.Writer, dir string, f diffFlags, ticket *regexp.Regexp) (*vcs.Git, domain.ChangeSet, error) {
	if f.diff == "" {
		return nil, domain.ChangeSet{}, errors.New("--diff is required: a compare link of the code host, or a range such as main..release")
	}
	cmp, err := codehost.ParseCompare(f.diff, compareParsers()...)
	if err != nil {
		return nil, domain.ChangeSet{}, err
	}
	g, err := vcs.Open(dir)
	if err != nil {
		return nil, domain.ChangeSet{}, err
	}
	remote, remoteErr := g.RemoteURL(ctx, "origin")
	if cmp.Host != "" {
		named := cmp.Host + "/" + cmp.Repo
		if remoteErr != nil {
			return nil, domain.ChangeSet{}, fmt.Errorf("the link names the repository %s, and %s has no origin remote to compare it with", named, dir)
		}
		if !codehost.SameRepo(cmp, remote) {
			return nil, domain.ChangeSet{}, fmt.Errorf("the link names the repository %s, and the origin remote of %s is %s; run the command in a clone of %s", named, dir, displayRemote(remote), named)
		}
	}
	if !f.noFetch && remoteErr == nil {
		fmt.Fprintf(stderr, "semar: fetching %s and %s from origin\n", cmp.Base, cmp.Head)
		var gone *vcs.BranchGoneError
		switch err := g.Fetch(ctx, "origin", cmp.Base, cmp.Head); {
		case errors.As(err, &gone):
			return nil, domain.ChangeSet{}, err
		case err != nil:
			return nil, domain.ChangeSet{}, fmt.Errorf("%w; pass --no-fetch to use the branches as they are in this clone", err)
		}
	}
	base, err := g.Resolve(ctx, "origin", cmp.Base)
	if err != nil {
		return nil, domain.ChangeSet{}, err
	}
	head, err := g.Resolve(ctx, "origin", cmp.Head)
	if err != nil {
		return nil, domain.ChangeSet{}, err
	}
	cs, err := changeset.Build(ctx, g, changeset.Options{
		Base: cmp.Base, Head: cmp.Head, BaseCommit: base, HeadCommit: head, Ticket: ticket,
	})
	if err != nil {
		return nil, domain.ChangeSet{}, err
	}
	return g, cs, nil
}

// displayRemote drops the user information of a remote URL, which can hold a token.
func displayRemote(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "(a remote URL that does not parse)"
	}
	u.User = nil
	return u.String()
}

// readPlanChange reads the change to plan: the one that --diff names, or with no --diff the change of
// the checked-out branch against its base, from the clone, with no fetch (Plan 26, D18).
func readPlanChange(ctx context.Context, stderr io.Writer, dir string, f diffFlags, cfg config.Config, ticket *regexp.Regexp) (*vcs.Git, domain.ChangeSet, error) {
	if f.diff != "" {
		return readChange(ctx, stderr, dir, f, ticket)
	}
	lc, err := readLocalChange(ctx, dir, cfg, f.base)
	if err != nil {
		return nil, domain.ChangeSet{}, err
	}
	if !lc.Repo {
		return nil, domain.ChangeSet{}, errors.New("this directory is not in a git repository, so there is no change to plan: pass --diff <link or range>")
	}
	return lc.Git, lc.Change, nil
}
