package cli

import (
	"context"
	"regexp"

	"github.com/jancuk/semar/internal/changeset"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/vcs"
)

// localChange is the change of the checked-out branch, read from the clone with no fetch (Plan 26,
// D3). Repo is false when dir is not in a git work tree, and then there is no change to read.
type localChange struct {
	Repo   bool
	Git    *vcs.Git
	Change domain.ChangeSet
}

// readLocalChange reads the commits that HEAD has and the base branch lacks. The base is base,
// else change.base of the configuration, else the default branch of origin, else main, else master.
func readLocalChange(ctx context.Context, dir string, cfg config.Config, base string) (localChange, error) {
	g, err := vcs.Open(dir)
	if err != nil {
		return localChange{}, nil
	}
	pattern := cfg.Tickets.Pattern
	if pattern == "" {
		pattern = changeset.DefaultTicketPattern
	}
	ticket, err := regexp.Compile(pattern)
	if err != nil {
		return localChange{}, err
	}
	cs, err := changeset.Local(ctx, g, changeset.LocalOptions{FlagBase: base, ConfigBase: cfg.Change.Base, Ticket: ticket})
	if err != nil {
		return localChange{}, err
	}
	return localChange{Repo: true, Git: g, Change: cs}, nil
}

// changeTickets is the key of each ticket of the change, in order.
func changeTickets(cs domain.ChangeSet) []string {
	keys := make([]string, len(cs.Tickets))
	for i, t := range cs.Tickets {
		keys[i] = t.Key
	}
	return keys
}
