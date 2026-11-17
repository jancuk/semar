package changeset

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/jancuk/semar/internal/domain"
)

// LocalRepo is what Local reads from a clone, with no network.
type LocalRepo interface {
	Repo
	CurrentBranch(ctx context.Context) (string, error)
	DefaultBranch(ctx context.Context, remote string) (string, error)
	Resolve(ctx context.Context, remote, name string) (string, error)
}

// LocalOptions names the base of a change. FlagBase comes from --base and ConfigBase from change.base
// in semar.yaml. Ticket is the pattern of a ticket key, and nil is the default one.
type LocalOptions struct {
	FlagBase, ConfigBase string
	Ticket               *regexp.Regexp
}

// Local reads the change of the checked-out branch: the commits that HEAD has and the base branch
// lacks. The base is the flag, else the configuration, else the default branch of origin as the
// clone knows it, else main, else master. It reads the clone as it is and never fetches.
func Local(ctx context.Context, repo LocalRepo, o LocalOptions) (domain.ChangeSet, error) {
	head, err := repo.Resolve(ctx, "", "HEAD")
	if err != nil {
		return domain.ChangeSet{}, fmt.Errorf("read the change: %w", err)
	}
	headName, err := repo.CurrentBranch(ctx)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	if headName == "" {
		headName = "HEAD"
	}

	name, commit, err := resolveBase(ctx, repo, o)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	return Build(ctx, repo, Options{Base: name, Head: headName, BaseCommit: commit, HeadCommit: head, Ticket: o.Ticket})
}

func resolveBase(ctx context.Context, repo LocalRepo, o LocalOptions) (name, commit string, err error) {
	for _, named := range []struct{ name, from string }{{o.FlagBase, "--base"}, {o.ConfigBase, "change.base"}} {
		if named.name == "" {
			continue
		}
		c, err := repo.Resolve(ctx, "origin", named.name)
		if err != nil {
			return "", "", fmt.Errorf("the base branch %s (from %s) does not exist in this clone: %w", named.name, named.from, err)
		}
		return named.name, c, nil
	}
	candidates := []string{"main", "master"}
	if def, err := repo.DefaultBranch(ctx, "origin"); err != nil {
		return "", "", err
	} else if def != "" {
		candidates = append([]string{def}, candidates...)
	}
	for _, n := range candidates {
		if c, err := repo.Resolve(ctx, "origin", n); err == nil {
			return n, c, nil
		}
	}
	return "", "", errors.New("no base branch was found: this clone has no default branch of origin, no main, and no master; set change.base in .semar/semar.yaml or pass --base <branch>")
}
