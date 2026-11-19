// Package status turns the derived state of the use cases into the groups that a person reads, the
// verdict of a release, and the text that semar status prints.
package status

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/obligation"
)

// Group is the one place that a use case is in.
type Group string

const (
	Verified    Group = "verified"
	Failing     Group = "failing"
	Blocked     Group = "blocked"
	NeedsReview Group = "needs review"
	Stale       Group = "stale"
	ForAPerson  Group = "for a person"
	NoTest      Group = "no test"
	Deferred    Group = "deferred"
)

// Groups is the order that a person reads them in.
var Groups = []Group{Verified, Failing, Blocked, NeedsReview, Stale, ForAPerson, NoTest, Deferred}

// Classify gives a use case its group. Every entry is in exactly one.
func Classify(e obligation.Entry) Group {
	switch e.State {
	case obligation.Failed:
		return Failing
	case obligation.Deferred:
		return Deferred
	case obligation.Verified:
		return Verified
	case obligation.NeedsAPerson:
		if e.Manual != nil && e.Manual.Result == "failed" {
			return Failing
		}
		return ForAPerson
	}
	switch {
	case e.NoReason || len(e.Covers) == 0:
		return NoTest
	case e.Review:
		return NeedsReview
	case e.Stale:
		return Stale
	}
	return Blocked
}

// ExitCode is 0 when every use case is verified or deferred, 1 when one failed or a person
// recorded a failure, and 2 otherwise. With the builds not read, a pass on any build counts, so
// nothing is confirmed current and the code is 2 at best.
func ExitCode(entries []obligation.Entry, buildsRead bool) int {
	code := 0
	for _, e := range entries {
		switch {
		case e.State == obligation.Failed || (e.Manual != nil && e.Manual.Result == "failed"):
			return 1
		case e.State != obligation.Verified && e.State != obligation.Deferred:
			code = 2
		}
	}
	if !buildsRead && code == 0 {
		return 2
	}
	return code
}

// Input is what status reads. Entries are in the order that obligation.Derive gives.
type Input struct {
	Branch, Base string // empty Branch: the directory is not in a git repository, or Scope says what was run
	Scope        string // what a run picked when it did not read a change, such as "the smoke suite"
	Environment  string // empty: no environment, so no build was read
	Builds       []domain.BuildRef
	BuildsRead   bool
	Tickets      []string // the tickets of the change, or nil for every use case
	Entries      []obligation.Entry
	NotPlanned   []string        // tickets of the change that have no use case
	NoTicket     []string        // commits of the change that name no ticket, as short IDs and subjects
	ByAuthor     map[string]bool // use cases whose every approval came from an author of the change
	Links        map[string]obligation.Link
}

// Item is one use case with its group, what to show for it, and the command that comes next.
type Item struct {
	Entry    obligation.Entry
	Group    Group
	Notes    []string
	Next     string // the command for this use case, with its ID
	Command  string // the command that every use case of the group shares, or "" when there is none
	ByAuthor bool
}

// Report is the whole of status, for the text and for the HTML file.
type Report struct {
	Input
	Items      []Item // in the order of Groups, and in the order of Entries within a group
	Counts     map[Group]int
	Code       int
	Failing    int
	Open       int
	Deferred   int
	ShipReason string
}

// Ship is true when the exit code is 0.
func (r Report) Ship() bool { return r.Code == 0 }

// Build groups the entries and gives the verdict.
func Build(in Input) Report {
	r := Report{Input: in, Counts: map[Group]int{}}
	for _, e := range in.Entries {
		g := Classify(e)
		it := Item{Entry: e, Group: g, Next: next(g, e), Command: command(g, e), ByAuthor: in.ByAuthor[e.Obligation.ID]}
		it.Notes = notes(g, it)
		r.Items = append(r.Items, it)
		r.Counts[g]++
	}
	slices.SortStableFunc(r.Items, func(a, b Item) int {
		return slices.Index(Groups, a.Group) - slices.Index(Groups, b.Group)
	})
	r.Code = ExitCode(in.Entries, in.BuildsRead)
	if len(in.NotPlanned) > 0 && r.Code == 0 {
		r.Code = 2
	}
	r.Failing, r.Deferred = r.Counts[Failing], r.Counts[Deferred]
	r.Open = len(in.NotPlanned)
	for _, g := range []Group{Blocked, NeedsReview, Stale, ForAPerson, NoTest} {
		r.Open += r.Counts[g]
	}
	r.ShipReason = shipReason(r)
	return r
}

func next(g Group, e obligation.Entry) string {
	switch g {
	case NeedsReview:
		for _, c := range e.Covers {
			if c.Word == "needs review" {
				if c.Journey != "" {
					return "semar review " + c.Journey
				}
				return "semar review " + string(c.Scenario)
			}
		}
		return "semar review"
	case Stale:
		return "semar run"
	case Blocked:
		if e.NotRun {
			return "semar run"
		}
	case ForAPerson:
		return "semar check " + e.Obligation.ID + " pass"
	case NoTest:
		return "semar plan"
	}
	return ""
}

// command is next with the ID left out, so that a group can name it once.
func command(g Group, e obligation.Entry) string {
	switch g {
	case NeedsReview:
		return "semar review"
	case ForAPerson:
		return "semar check <id> pass"
	}
	return next(g, e)
}

// notes are the lines that a verified use case shows, because it has something to say. Every other
// group shows the reasons of the entry.
func notes(g Group, it Item) []string {
	e := it.Entry
	if g != Verified {
		return nil
	}
	var out []string
	switch {
	case e.Link != nil:
		out = append(out, fmt.Sprintf("by test %s, linked by %s", e.Link.Scenario, e.Link.User))
	case e.Manual != nil:
		out = append(out, "checked by "+e.Manual.User)
	}
	if it.ByAuthor {
		out = append(out, "approved by an author of the change")
	}
	for _, c := range e.Covers {
		if strings.HasPrefix(c.Attempts, "flaky") {
			out = append(out, fmt.Sprintf("%s: %s", c.Scenario, c.Attempts))
		}
	}
	return out
}

func shipReason(r Report) string {
	var parts []string
	if r.Failing > 0 {
		parts = append(parts, fmt.Sprintf("%d failing", r.Failing))
	}
	if r.Open > 0 {
		parts = append(parts, fmt.Sprintf("%d open", r.Open))
	}
	if r.Ship() && r.Deferred > 0 {
		parts = append(parts, fmt.Sprintf("%d deferred", r.Deferred))
	}
	s := strings.Join(parts, ", ")
	if s != "" {
		s += "."
	}
	if !r.BuildsRead {
		if s != "" {
			s += " "
		}
		s += "The builds were not read."
	}
	return s
}

// Lead is the first line.
func (r Report) Lead() string {
	var parts []string
	switch {
	case r.Scope != "":
		parts = append(parts, strings.ToUpper(r.Scope[:1])+r.Scope[1:])
	case r.Branch == "":
		parts = append(parts, "Not in a git repository")
	default:
		parts = append(parts, fmt.Sprintf("On %s against %s", r.Branch, r.Base))
	}
	switch {
	case r.Environment == "":
		parts = append(parts, "no environment")
	default:
		parts = append(parts, r.Environment)
	}
	if b := builds(r.Builds); b != "" {
		parts = append(parts, b)
	}
	return strings.Join(parts, " · ")
}

func builds(bs []domain.BuildRef) string {
	var out []string
	for _, b := range bs {
		out = append(out, fmt.Sprintf("%s at %s", b.Service, domain.ShortCommit(b.Commit)))
	}
	return strings.Join(out, ", ")
}

// Count is the second line.
func (r Report) Count() string {
	n := len(r.Entries)
	noun := "use cases"
	if n == 1 {
		noun = "use case"
	}
	line := fmt.Sprintf("%d %s", n, noun)
	if len(r.Tickets) > 0 {
		line += " of " + strings.Join(r.Tickets, ", ")
	}
	return line
}

// Verdict is the last line.
func (r Report) Verdict() string {
	verdict := "Ship: no."
	if r.Ship() {
		verdict = "Ship: yes."
	}
	if r.ShipReason != "" {
		verdict += " " + r.ShipReason
	}
	return verdict
}
