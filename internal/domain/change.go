package domain

import "time"

// ChangeCommit is one commit of a compared range.
type ChangeCommit struct {
	ID      string // full hash
	Subject string
	Body    string
	Author  string // name only
	At      time.Time
	Merge   bool
	Tickets []string // keys found in the subject and the body, sorted
	Files   []string // paths the commit touched. Empty for a merge
}

// TicketChange is what one ticket key brought into a range.
type TicketChange struct {
	Key     string
	Commits []string // commit IDs, oldest first
	Files   []string // changed files that a commit of this ticket touched, sorted
}

// FileChange is one file of the net difference of a range.
type FileChange struct {
	Path    string
	OldPath string // set for a rename
	Status  string // "added" | "modified" | "deleted" | "renamed"
	Kind    string // "source" | "test" | "docs" | "generated" | "migration" | "config"
	Added   int
	Deleted int
	Binary  bool
	Tickets []string
}

// ChangeSet is the difference between two branches.
type ChangeSet struct {
	Base, Head             string // the names as given
	BaseCommit, HeadCommit string
	MergeBase              string
	Commits                []ChangeCommit // reachable from Head and not from Base, oldest first
	Tickets                []TicketChange // sorted by key
	NoTicket               []string       // IDs of commits that are not merges and carry no key
	Files                  []FileChange   // the difference between MergeBase and HeadCommit, sorted by path
}
