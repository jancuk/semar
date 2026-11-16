package obligation

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

type State string

const (
	Verified     State = "verified"
	Failed       State = "failed"
	Blocked      State = "blocked"
	NeedsAPerson State = "needs_a_person"
	Deferred     State = "deferred"
)

// Cover is what one covering item gave.
type Cover struct {
	Scenario domain.ScenarioID
	Journey  string
	Found    bool   // a result exists
	Outcome  string // a scenario outcome or a journey outcome
	Run      domain.RunID
	Reason   string // the first reason of the result
	Builds   []domain.BuildRef
}

// Results is what the stored runs say. A scenario whose file exists is a key of Scenarios, with
// Found false when it has no result. A scenario that is no key was deleted. Journeys works the
// same way.
type Results struct {
	Scenarios map[domain.ScenarioID]Cover
	Journeys  map[string]Cover  // nil until Plan 18 wires it
	Current   []domain.BuildRef // nil when the builds were not read
}

type Entry struct {
	Obligation Located
	State      State
	Reasons    []string
	Covers     []Cover
	Manual     *ManualResult
	Deferral   *Deferral // the deferral that holds: its expiry is after now
	Expired    *Deferral // a deferral that was ignored because it expired
	NoReason   bool      // no cover, no gap, and no manual reason
}

// Derive gives each obligation one state (L1). The first row of the table wins: failed, then
// deferred, then needs a person, then verified, else blocked. It is pure, and it returns the
// entries in the order that the ledger lists them: no test and no reason, failed, blocked,
// needs a person, deferred, verified, each in the order of obs.
//
// A failed cover shows whatever the build was: a failure is never hidden by a newer build.
func Derive(obs []Located, r Results, deferrals map[string]Deferral, manual map[string]ManualResult, now time.Time) []Entry {
	entries := make([]Entry, 0, len(obs))
	for _, o := range obs {
		entries = append(entries, deriveOne(o, r, deferrals, manual, now))
	}
	slices.SortStableFunc(entries, func(a, b Entry) int { return rank(a) - rank(b) })
	return entries
}

func rank(e Entry) int {
	if e.NoReason {
		return 0
	}
	switch e.State {
	case Failed:
		return 1
	case Blocked:
		return 2
	case NeedsAPerson:
		return 3
	case Deferred:
		return 4
	}
	return 5
}

func deriveOne(o Located, r Results, deferrals map[string]Deferral, manual map[string]ManualResult, now time.Time) Entry {
	e := Entry{Obligation: o}
	if m, ok := manual[o.ID]; ok {
		e.Manual = &m
	}
	var notes []string // the reason that a deferral gave, for the state of another row
	if d, ok := deferrals[o.ID]; ok {
		if d.Until.After(now) {
			e.Deferral = &d
			notes = append(notes, fmt.Sprintf("deferred by %s until %s: %s", d.User, day(d.Until), d.Reason))
		} else {
			e.Expired = &d
			e.Reasons = append(e.Reasons, fmt.Sprintf("the deferral by %s expired on %s", d.User, day(d.Until)))
		}
	}

	var failed, notPassed []string
	passed := 0
	add := func(label string, c Cover, known bool) {
		e.Covers = append(e.Covers, c)
		switch {
		case !known:
			notPassed = append(notPassed, label+" was deleted: no scenario or journey covers this")
		case !c.Found:
			notPassed = append(notPassed, label+" has no result: run it")
		case c.Outcome == string(domain.OutcomeFailed):
			failed = append(failed, label+" failed in "+string(c.Run)+withReason(c.Reason))
		case c.Outcome != string(domain.OutcomePassed):
			notPassed = append(notPassed, fmt.Sprintf("%s is %s in %s%s", label, strings.ReplaceAll(c.Outcome, "_", " "), c.Run, withReason(c.Reason)))
		case !onCurrent(c, r.Current):
			notPassed = append(notPassed, fmt.Sprintf("%s ran %s: rerun on the current build", label, strings.Join(staleDetails(c, r.Current), "; ")))
		default:
			passed++
		}
	}
	for _, id := range o.Covers {
		c, known := r.Scenarios[id]
		c.Scenario = id
		add(string(id), c, known)
	}
	for _, id := range o.Journeys {
		label := "journey " + id
		if r.Journeys == nil {
			e.Covers = append(e.Covers, Cover{Journey: id})
			notPassed = append(notPassed, label+": journey results are not read yet")
			continue
		}
		c, known := r.Journeys[id]
		c.Journey = id
		add(label, c, known)
	}
	covered := len(e.Covers) > 0

	switch {
	case len(failed) > 0:
		e.State = Failed
		e.Reasons = append(e.Reasons, failed...)
		e.Reasons = append(e.Reasons, notes...)
	case e.Deferral != nil:
		e.State = Deferred
		e.Reasons = append(e.Reasons, notes...)
	case o.Manual != nil:
		e.State = NeedsAPerson
		e.Reasons = append(e.Reasons, o.Manual.Text)
		if e.Manual != nil {
			e.Reasons = append(e.Reasons, e.Manual.Result+", checked by a person")
		}
	case covered && len(notPassed) == 0 && passed == len(e.Covers):
		e.State = Verified
	default:
		e.State = Blocked
		e.Reasons = append(e.Reasons, notPassed...)
		if o.Gap != "" {
			e.Reasons = append(e.Reasons, o.Gap)
		}
		if !covered && o.Gap == "" {
			e.NoReason = true
			e.Reasons = append(e.Reasons, "use case with no test and no reason")
		}
	}
	return e
}

func withReason(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// onCurrent is true when the cover is not stale. With no builds read, every result counts.
func onCurrent(c Cover, current []domain.BuildRef) bool { return len(staleDetails(c, current)) == 0 }

// staleDetails says, for each service that the cover ran on, what it ran on and what runs now,
// when they differ or when the current build of the service was not read. A service that the
// cover did not run on is not compared: a journey touches some services, and the environment has
// more. With builds read and none recorded, the cover cannot be matched and is stale.
func staleDetails(c Cover, current []domain.BuildRef) []string {
	if current == nil {
		return nil
	}
	if len(c.Builds) == 0 && len(current) > 0 {
		return []string{"with no recorded build, so it cannot be matched to the current build"}
	}
	var out []string
	for _, ran := range c.Builds {
		i := slices.IndexFunc(current, func(b domain.BuildRef) bool { return b.Service == ran.Service })
		switch {
		case i < 0:
			out = append(out, fmt.Sprintf("on %s %s, and the build of %s was not read now", ran.Service, shortCommit(ran.Commit), ran.Service))
		case current[i].Commit != ran.Commit:
			out = append(out, fmt.Sprintf("on %s %s, and the environment runs %s", ran.Service, shortCommit(ran.Commit), shortCommit(current[i].Commit)))
		case current[i].ConfigHash != ran.ConfigHash:
			out = append(out, fmt.Sprintf("on %s %s with another configuration, and the environment runs %s", ran.Service, shortCommit(ran.Commit), shortCommit(current[i].Commit)))
		}
	}
	return out
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
