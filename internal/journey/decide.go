package journey

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// Outcome is everything Decide reads. Start is the manifest formed before the first leg, and End
// the one read after the last, or nil when it could not be read.
type Outcome struct {
	ManifestProblem string
	FixtureProblem  []string
	Legs            []domain.LegResult // in run order; legs that did not start are not_run
	Invariants      []domain.InvariantResult
	Start           domain.Manifest
	End             *domain.Manifest
	Filtered        bool
}

const (
	reasonNotSelected = "the journey was not selected"
	reasonNoEnd       = "the manifest could not be read at the end"
)

// Decide is the outcome table of a journey (TRD, Outcome). The first matching row gives the
// outcome, and the reasons of every matching row are kept in row order. The first failing leg is
// the first leg in run order that ran and did not pass. Decide is pure.
func Decide(in Outcome) (domain.JourneyOutcome, domain.LegID, []domain.Reason) {
	var first *domain.LegResult
	for i := range in.Legs {
		if o := in.Legs[i].Outcome; o != domain.OutcomePassed && o != domain.OutcomeNotRun {
			first = &in.Legs[i]
			break
		}
	}
	var rows [4][]domain.Reason // not_run, blocked, stale, failed
	add := func(row int, rule domain.Outcome, text string) {
		rows[row] = append(rows[row], domain.Reason{Rule: rule, Text: text})
	}

	if in.Filtered {
		add(0, domain.OutcomeNotRun, reasonNotSelected)
	}

	if in.ManifestProblem != "" {
		add(1, domain.OutcomeBlocked, in.ManifestProblem)
	}
	for _, p := range in.FixtureProblem {
		add(1, domain.OutcomeBlocked, p)
	}
	if first != nil && (first.Outcome == domain.OutcomeBlocked || first.Outcome == domain.OutcomeInconclusive) {
		add(1, domain.OutcomeBlocked, legText(*first, "ended "+string(first.Outcome)))
	}
	for _, inv := range in.Invariants {
		if inv.State != domain.CheckTrue && inv.State != domain.CheckFalse {
			add(1, domain.OutcomeBlocked, fmt.Sprintf("invariant %d could not be evaluated: %s", inv.Index+1, inv.Detail))
		}
	}

	started := !in.Filtered && in.ManifestProblem == ""
	switch {
	case in.End == nil && started:
		add(2, domain.OutcomeStale, reasonNoEnd)
	case in.End != nil && in.End.Hash != in.Start.Hash:
		for _, c := range changes(in.Start.Services, in.End.Services) {
			add(2, domain.OutcomeStale, "a build changed during the journey: "+c)
		}
	}
	for _, l := range in.Legs {
		if l.Outcome == domain.OutcomeStale {
			add(2, domain.OutcomeStale, legText(l, "ended stale"))
		}
	}

	if first != nil && first.Outcome == domain.OutcomeFailed {
		add(3, domain.OutcomeFailed, legText(*first, "failed"))
	}
	for _, inv := range in.Invariants {
		if inv.State == domain.CheckFalse {
			add(3, domain.OutcomeFailed, fmt.Sprintf("invariant %d is false: %s", inv.Index+1, strings.Join(inv.Values, ", ")))
		}
	}

	outcomes := [4]domain.JourneyOutcome{domain.JourneyNotRun, domain.JourneyBlocked, domain.JourneyStale, domain.JourneyFailed}
	out := domain.JourneyPassed
	reasons := []domain.Reason{}
	for i, r := range rows {
		if len(r) > 0 && out == domain.JourneyPassed {
			out = outcomes[i]
		}
		reasons = append(reasons, r...)
	}
	// A leg that did not run, when no row explains why, must not read as a pass.
	if out == domain.JourneyPassed {
		if i := slices.IndexFunc(in.Legs, func(l domain.LegResult) bool { return l.Outcome != domain.OutcomePassed }); i >= 0 {
			out = domain.JourneyNotRun
			reasons = append(reasons, domain.Reason{Rule: domain.OutcomeNotRun, Text: legText(in.Legs[i], "was not run")})
		}
	}

	var firstID domain.LegID
	if first != nil && out != domain.JourneyPassed {
		firstID = first.Leg
	}
	return out, firstID, reasons
}

// legText is "leg pay <what>: <its first reason>".
func legText(l domain.LegResult, what string) string {
	text := fmt.Sprintf("leg %s %s", l.Leg, what)
	if len(l.Reasons) > 0 {
		text += ": " + l.Reasons[0].Text
	}
	return text
}

// changes names each service whose build differs between two manifests, in service order.
func changes(start, end []domain.BuildRef) []string {
	byService := func(bs []domain.BuildRef) map[string]domain.BuildRef {
		m := make(map[string]domain.BuildRef, len(bs))
		for _, b := range bs {
			m[b.Service] = b
		}
		return m
	}
	s, e := byService(start), byService(end)
	var names []string
	for n := range s {
		names = append(names, n)
	}
	for n := range e {
		if _, ok := s[n]; !ok {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	var out []string
	for _, n := range names {
		a, inStart := s[n]
		b, inEnd := e[n]
		switch {
		case !inStart:
			out = append(out, fmt.Sprintf("%s was not read at the start, then %s", n, b.Commit))
		case !inEnd:
			out = append(out, fmt.Sprintf("%s %s, then was not read", n, a.Commit))
		case a.Commit != b.Commit:
			out = append(out, fmt.Sprintf("%s %s then %s", n, a.Commit, b.Commit))
		case a.ConfigHash != b.ConfigHash:
			out = append(out, fmt.Sprintf("%s %s with another configuration", n, a.Commit))
		}
	}
	return out
}
