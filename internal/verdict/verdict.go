// Package verdict decides the outcome of one scenario from recorded evidence.
// It is pure: it does no I/O, reads no clock, and changes none of its arguments.
package verdict

import (
	"fmt"
	"slices"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
)

type StepEvidence struct {
	Action     *domain.Action // nil when the step did not run
	Checks     []check.Result // one per Step.Expect, in order
	Invariants []check.Result
}

type Input struct {
	Scenario    domain.Scenario
	Steps       map[domain.StepID]StepEvidence
	Confirmed   func(e domain.Expectation) bool // true when a person confirmed it, or its source needs no confirmation
	BuildsStart []domain.BuildRef
	BuildsEnd   []domain.BuildRef
	NotRun      string   // not empty: why the scenario was not run
	Blocked     []string // reasons found before the scenario ran
	Retried     bool
}

const (
	msgBuildChanged     = "the build changed during the run"
	msgNotConfirmed     = "an expected result is not confirmed"
	msgUnconfirmedFalse = "an expected result is not confirmed and not met"
	msgNotEvaluated     = "a check could not be evaluated"
	msgEffectUnknown    = "a write timed out and its effect is unknown"
	msgRetried          = "the script needed a retry"
	msgNoCheck          = "the scenario has no check besides the invariants"
	msgNoDetail         = "no result was recorded"
	msgMismatch         = "the recorded evidence does not match the scenario"
	msgNoBuild          = "no build was recorded"
)

// outcomeOrder returns the order in which the rules decide. The first rule that matched gives the outcome.
func outcomeOrder() [5]domain.Outcome {
	return [5]domain.Outcome{
		domain.OutcomeNotRun,
		domain.OutcomeBlocked,
		domain.OutcomeStale,
		domain.OutcomeFailed,
		domain.OutcomeInconclusive,
	}
}

// Compute returns the one outcome and every reason. The same input always gives the same result.
func Compute(in Input) domain.ScenarioResult {
	res := domain.ScenarioResult{
		Scenario: in.Scenario.ID,
		Reasons:  []domain.Reason{},
		Checks:   []domain.CheckResult{},
		Actions:  []domain.ActionID{},
	}
	byRule := map[domain.Outcome][]domain.Reason{}
	add := func(rule domain.Outcome, step domain.StepID, text string) {
		byRule[rule] = append(byRule[rule], domain.Reason{Rule: rule, Text: text, Step: step})
	}

	if in.NotRun != "" {
		add(domain.OutcomeNotRun, "", in.NotRun)
	}
	for _, text := range in.Blocked {
		add(domain.OutcomeBlocked, "", text)
	}
	buildsRecorded := len(in.BuildsStart) > 0 && len(in.BuildsEnd) > 0
	if buildsRecorded && buildsDiffer(in.BuildsStart, in.BuildsEnd) {
		add(domain.OutcomeStale, "", msgBuildChanged)
	}

	expectations := 0
	seen := map[domain.StepID]bool{}
	for _, step := range in.Scenario.Steps {
		if seen[step.ID] {
			add(domain.OutcomeInconclusive, step.ID, fmt.Sprintf("%s: step %s appears more than once in the scenario", msgMismatch, step.ID))
			continue
		}
		seen[step.ID] = true
		ev := in.Steps[step.ID]
		a := ev.Action
		if a != nil {
			res.Actions = append(res.Actions, a.ID)
		}
		refused := a != nil && a.Refused != ""
		noResponse := !step.Setup && !refused && a != nil && (a.Err != "" || a.Response == nil)
		if len(ev.Checks) > len(step.Expect) {
			add(domain.OutcomeInconclusive, step.ID, fmt.Sprintf("%s: step %s has %d check results for %d expectations", msgMismatch, step.ID, len(ev.Checks), len(step.Expect)))
		}

		switch {
		case refused:
			add(domain.OutcomeBlocked, step.ID, fmt.Sprintf("the environment does not allow a step %s: %s", step.ID, a.Refused))
		case step.Setup:
			if why := setupFailure(a); why != "" {
				add(domain.OutcomeBlocked, step.ID, fmt.Sprintf("setup failed: %s %s", step.ID, why))
			}
		}

		for i, e := range step.Expect {
			if !step.Setup {
				expectations++
			}
			confirmed := in.Confirmed != nil && in.Confirmed(e)
			r := check.Result{State: domain.CheckNotEvaluated, Detail: msgNoDetail}
			if i < len(ev.Checks) {
				r = ev.Checks[i]
			}
			res.Checks = append(res.Checks, domain.CheckResult{
				Step: step.ID, Index: i, Source: string(e.Source),
				State: r.State, Expected: r.Expected, Observed: r.Observed, Detail: r.Detail,
				Confirmed: confirmed,
			})
			if refused {
				continue
			}
			switch {
			case r.State == domain.CheckFalse && confirmed:
				text := fmt.Sprintf("step %s: expected %s, observed %s (source %s)", step.ID, r.Expected, r.Observed, e.Source)
				if r.Detail != "" {
					text += ": " + r.Detail
				}
				add(domain.OutcomeFailed, step.ID, text)
			case r.State == domain.CheckFalse:
				add(domain.OutcomeInconclusive, step.ID, msgUnconfirmedFalse)
			case !confirmed:
				add(domain.OutcomeInconclusive, step.ID, msgNotConfirmed)
			}
			if r.State != domain.CheckTrue && r.State != domain.CheckFalse && a != nil && !noResponse {
				text := msgNotEvaluated
				if r.Detail != "" {
					text += ": " + r.Detail
				}
				add(domain.OutcomeInconclusive, step.ID, text)
			}
		}

		for _, inv := range ev.Invariants {
			res.Checks = append(res.Checks, domain.CheckResult{
				Step: step.ID, Index: -1, Source: domain.SourceInvariant,
				State: inv.State, Expected: inv.Expected, Observed: inv.Observed, Detail: inv.Detail,
				Confirmed: true,
			})
			switch {
			case refused:
			case inv.State == domain.CheckFalse && step.Setup:
				add(domain.OutcomeBlocked, step.ID, fmt.Sprintf("setup failed: %s %s was not met", step.ID, inv.Expected))
			case inv.State == domain.CheckFalse:
				text := fmt.Sprintf("step %s broke an invariant: expected %s, observed %s", step.ID, inv.Expected, inv.Observed)
				if inv.Detail != "" {
					text += ": " + inv.Detail
				}
				add(domain.OutcomeFailed, step.ID, text)
			}
			if inv.State != domain.CheckTrue && inv.State != domain.CheckFalse && !refused && !noResponse {
				text := msgNotEvaluated
				if inv.Detail != "" {
					text += ": " + inv.Detail
				}
				add(domain.OutcomeInconclusive, step.ID, text)
			}
		}

		if a == nil && !step.Setup {
			add(domain.OutcomeInconclusive, step.ID, fmt.Sprintf("step %s has no recorded result", step.ID))
		}
		if noResponse {
			cause := "no response was recorded"
			if a.Err != "" {
				cause = a.Err
			}
			add(domain.OutcomeInconclusive, step.ID, fmt.Sprintf("step %s failed: %s", step.ID, cause))
		}
		if a != nil && a.EffectUnknown {
			add(domain.OutcomeInconclusive, step.ID, msgEffectUnknown)
		}
	}
	var unknown []string
	for id := range in.Steps {
		if !seen[id] {
			unknown = append(unknown, string(id))
		}
	}
	slices.Sort(unknown)
	for _, id := range unknown {
		add(domain.OutcomeInconclusive, domain.StepID(id), fmt.Sprintf("%s: the evidence holds step %s, which the scenario does not have", msgMismatch, id))
	}
	if !buildsRecorded {
		add(domain.OutcomeInconclusive, "", msgNoBuild)
	}
	if in.Retried {
		add(domain.OutcomeInconclusive, "", msgRetried)
	}
	if expectations == 0 {
		add(domain.OutcomeInconclusive, "", msgNoCheck)
	}

	res.Outcome = domain.OutcomePassed
	for _, rule := range outcomeOrder() {
		if len(byRule[rule]) > 0 && res.Outcome == domain.OutcomePassed {
			res.Outcome = rule
		}
		res.Reasons = append(res.Reasons, byRule[rule]...)
	}
	return res
}

func setupFailure(a *domain.Action) string {
	switch {
	case a == nil:
		return "has no recorded result"
	case a.Err != "":
		return "failed: " + a.Err
	case a.Response == nil:
		return "has no response"
	case a.Response.Status >= 400:
		return fmt.Sprintf("answered %d", a.Response.Status)
	}
	return ""
}

func buildsDiffer(start, end []domain.BuildRef) bool {
	s, e := slices.Clone(start), slices.Clone(end)
	cmp := func(x, y domain.BuildRef) int {
		if c := compare(x.Service, y.Service); c != 0 {
			return c
		}
		if c := compare(x.Commit, y.Commit); c != 0 {
			return c
		}
		return compare(x.ConfigHash, y.ConfigHash)
	}
	slices.SortFunc(s, cmp)
	slices.SortFunc(e, cmp)
	return !slices.Equal(s, e)
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
