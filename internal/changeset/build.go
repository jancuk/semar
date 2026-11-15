// Package changeset turns a range of commits into tickets, commits, and files.
package changeset

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/vcs"
)

// DefaultTicketPattern is the key of a tracker such as Jira.
const DefaultTicketPattern = `[A-Z][A-Z0-9]+-[0-9]+`

var defaultTicket = regexp.MustCompile(DefaultTicketPattern)

// Repo is what Build reads from a repository.
type Repo interface {
	MergeBase(ctx context.Context, a, b string) (string, error)
	Log(ctx context.Context, from, to string) ([]vcs.Commit, error)
	DiffStat(ctx context.Context, base, head string) ([]vcs.FileStat, error)
}

// Options says which range to read and how to find ticket keys.
type Options struct {
	Base, Head             string // names, for the report
	BaseCommit, HeadCommit string
	Ticket                 *regexp.Regexp // nil means DefaultTicketPattern
}

// Build reads the commits that Head has and Base lacks, and the net difference
// from their merge base. A merge adds its keys and no files, and a file belongs
// to the tickets of the commits that touched it.
func Build(ctx context.Context, repo Repo, o Options) (domain.ChangeSet, error) {
	if o.BaseCommit == "" || o.HeadCommit == "" {
		return domain.ChangeSet{}, errors.New("build change set: base and head commits are required")
	}
	re := o.Ticket
	if re == nil {
		re = defaultTicket
	}
	mergeBase, err := repo.MergeBase(ctx, o.BaseCommit, o.HeadCommit)
	if err != nil {
		return domain.ChangeSet{}, fmt.Errorf("build change set: %w", err)
	}
	log, err := repo.Log(ctx, o.BaseCommit, o.HeadCommit)
	if err != nil {
		return domain.ChangeSet{}, fmt.Errorf("build change set: %w", err)
	}
	stats, err := repo.DiffStat(ctx, mergeBase, o.HeadCommit)
	if err != nil {
		return domain.ChangeSet{}, fmt.Errorf("build change set: %w", err)
	}

	cs := domain.ChangeSet{
		Base: o.Base, Head: o.Head,
		BaseCommit: o.BaseCommit, HeadCommit: o.HeadCommit,
		MergeBase: mergeBase,
	}

	files := make([]domain.FileChange, len(stats))
	byName := make(map[string]int, len(stats))
	for i, s := range stats {
		files[i] = domain.FileChange{
			Path: s.Path, OldPath: s.OldPath, Status: s.Status, Kind: Kind(s.Path),
			Added: s.Added, Deleted: s.Deleted, Binary: s.Binary,
		}
		byName[s.Path] = i
		if s.OldPath != "" {
			byName[s.OldPath] = i
		}
	}

	ticketOrder := map[string]*domain.TicketChange{}
	ticketFiles := map[string]map[string]bool{}
	fileTickets := make([]map[string]bool, len(files))
	for _, c := range log {
		keys := findKeys(re, c.Subject+"\n"+c.Body)
		for _, k := range keys {
			if idcheck.Check("ticket key", k) != nil {
				return domain.ChangeSet{}, fmt.Errorf("ticket key %q in commit %s cannot be used as a name: change tickets.pattern so that a key holds only letters, digits, \".\", \"_\", and \"-\", and starts with a letter or a digit", k, c.ID)
			}
		}
		merge := len(c.Parents) > 1
		cc := domain.ChangeCommit{
			ID: c.ID, Subject: c.Subject, Body: c.Body, Author: c.Author, At: c.At,
			Merge: merge, Tickets: keys,
		}
		if !merge {
			cc.Files = c.Files
			if len(keys) == 0 {
				cs.NoTicket = append(cs.NoTicket, c.ID)
			}
		}
		cs.Commits = append(cs.Commits, cc)
		for _, k := range keys {
			tc := ticketOrder[k]
			if tc == nil {
				tc = &domain.TicketChange{Key: k}
				ticketOrder[k] = tc
				ticketFiles[k] = map[string]bool{}
			}
			tc.Commits = append(tc.Commits, c.ID)
			if merge {
				continue
			}
			for _, p := range c.Files {
				i, ok := byName[p]
				if !ok {
					continue
				}
				ticketFiles[k][files[i].Path] = true
				if fileTickets[i] == nil {
					fileTickets[i] = map[string]bool{}
				}
				fileTickets[i][k] = true
			}
		}
	}

	for k, tc := range ticketOrder {
		tc.Files = sortedKeys(ticketFiles[k])
		cs.Tickets = append(cs.Tickets, *tc)
	}
	sort.Slice(cs.Tickets, func(i, j int) bool { return cs.Tickets[i].Key < cs.Tickets[j].Key })
	for i := range files {
		files[i].Tickets = sortedKeys(fileTickets[i])
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	cs.Files = files
	return cs, nil
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// maxKeyText bounds the text of one commit that is searched for keys.
const maxKeyText = 64 << 10

// findKeys returns the sorted, distinct matches of re that are not part of a
// longer run of letters and digits. The caller's pattern need not hold \b.
// Matches are searched in the first maxKeyText bytes only.
func findKeys(re *regexp.Regexp, text string) []string {
	search := text[:min(len(text), maxKeyText)]
	found := map[string]bool{}
	for pos := 0; pos <= len(search); {
		loc := re.FindStringIndex(search[pos:])
		if loc == nil {
			break
		}
		start, end := pos+loc[0], pos+loc[1]
		if start == end || (start > 0 && isAlnum(text[start-1])) || (end < len(text) && isAlnum(text[end])) {
			// No key starts inside this run, since each such position follows a letter
			// or a digit. Restarting one byte later instead makes a long run quadratic.
			pos = start + 1
			for pos < len(search) && isAlnum(search[pos-1]) && isAlnum(search[pos]) {
				pos++
			}
			continue
		}
		found[text[start:end]] = true
		pos = end
	}
	return sortedKeys(found)
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}
