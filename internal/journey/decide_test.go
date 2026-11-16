package journey

import (
	"reflect"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func manifestOf(builds ...domain.BuildRef) domain.Manifest {
	return domain.Manifest{Services: builds, Hash: domain.ManifestHash(builds)}
}

func legResult(id domain.LegID, o domain.Outcome, reasons ...string) domain.LegResult {
	lr := domain.LegResult{Leg: id, Scenario: domain.ScenarioID("checkout." + string(id)), Outcome: o, Reasons: []domain.Reason{}}
	for _, r := range reasons {
		lr.Reasons = append(lr.Reasons, domain.Reason{Rule: o, Text: r})
	}
	return lr
}

func TestDecideTable(t *testing.T) {
	orders := domain.BuildRef{Service: "orders", Commit: "9f3c2ab"}
	changed := domain.BuildRef{Service: "orders", Commit: "c0ffee1"}
	payments := domain.BuildRef{Service: "payments", Commit: "abc1234"}
	start := manifestOf(orders, payments)
	same := manifestOf(orders, payments)
	moved := manifestOf(changed, payments)

	passedLegs := []domain.LegResult{legResult("place", domain.OutcomePassed), legResult("pay", domain.OutcomePassed), legResult("settled", domain.OutcomePassed)}
	trueInv := []domain.InvariantResult{{Index: 0, State: domain.CheckTrue, Values: []string{"1000", "1000", "1000"}}}
	falseInv := []domain.InvariantResult{{Index: 0, State: domain.CheckFalse, Values: []string{"1000", "1001", "1000"}}}
	unreadInv := []domain.InvariantResult{{Index: 0, State: domain.CheckNotEvaluated, Values: []string{"1000", "", "1000"}, Detail: "{{pay.amount}} was not captured"}}
	failedPay := []domain.LegResult{
		legResult("place", domain.OutcomePassed),
		legResult("pay", domain.OutcomeFailed, "step pay: expected status 202, observed status 500 (source person)"),
		legResult("settled", domain.OutcomeNotRun, "stopped after leg pay"),
	}
	blockedPay := []domain.LegResult{
		legResult("place", domain.OutcomePassed),
		legResult("pay", domain.OutcomeBlocked, "the environment does not allow a step pay: write", "second"),
		legResult("settled", domain.OutcomeNotRun, "stopped after leg pay"),
	}
	reason := func(o domain.Outcome, text string) domain.Reason { return domain.Reason{Rule: o, Text: text} }

	cases := []struct {
		name    string
		in      Outcome
		want    domain.JourneyOutcome
		first   domain.LegID
		reasons []domain.Reason
	}{
		{
			name: "row 5: every leg passed, every invariant held, the manifest held",
			in:   Outcome{Legs: passedLegs, Invariants: trueInv, Start: start, End: &same},
			want: domain.JourneyPassed, reasons: []domain.Reason{},
		},
		{
			name: "row 1: filtered wins over every other row",
			in:   Outcome{Filtered: true, ManifestProblem: "the build of payments could not be read", Start: start},
			want: domain.JourneyNotRun,
			reasons: []domain.Reason{
				reason(domain.OutcomeNotRun, "the journey was not selected"),
				reason(domain.OutcomeBlocked, "the build of payments could not be read"),
			},
		},
		{
			name:    "row 2: the manifest could not be formed",
			in:      Outcome{ManifestProblem: "the build of payments could not be read", Start: start},
			want:    domain.JourneyBlocked,
			reasons: []domain.Reason{reason(domain.OutcomeBlocked, "the build of payments could not be read")},
		},
		{
			name:    "row 2: a fixture problem",
			in:      Outcome{FixtureProblem: []string{"fixture order.paid is missing", "second"}, Start: start, End: &same},
			want:    domain.JourneyBlocked,
			reasons: []domain.Reason{reason(domain.OutcomeBlocked, "fixture order.paid is missing"), reason(domain.OutcomeBlocked, "second")},
		},
		{
			name: "row 2: the first leg that did not pass ended blocked",
			in:   Outcome{Legs: blockedPay, Start: start, End: &same},
			want: domain.JourneyBlocked, first: "pay",
			reasons: []domain.Reason{reason(domain.OutcomeBlocked, "leg pay ended blocked: the environment does not allow a step pay: write")},
		},
		{
			name: "row 2: the first leg that did not pass ended inconclusive",
			in: Outcome{Legs: []domain.LegResult{
				legResult("place", domain.OutcomeInconclusive, "an expected result is not confirmed"),
				legResult("pay", domain.OutcomeNotRun, "stopped after leg place"),
			}, Start: start, End: &same},
			want: domain.JourneyBlocked, first: "place",
			reasons: []domain.Reason{reason(domain.OutcomeBlocked, "leg place ended inconclusive: an expected result is not confirmed")},
		},
		{
			name:    "row 2: an invariant could not be evaluated",
			in:      Outcome{Legs: passedLegs, Invariants: unreadInv, Start: start, End: &same},
			want:    domain.JourneyBlocked,
			reasons: []domain.Reason{reason(domain.OutcomeBlocked, "invariant 1 could not be evaluated: {{pay.amount}} was not captured")},
		},
		{
			name: "row 3: the manifest at the end differs",
			in:   Outcome{Legs: passedLegs, Invariants: trueInv, Start: start, End: &moved},
			want: domain.JourneyStale,
			reasons: []domain.Reason{
				reason(domain.OutcomeStale, "a build changed during the journey: orders 9f3c2ab then c0ffee1"),
			},
		},
		{
			name:    "row 3: the manifest could not be read at the end",
			in:      Outcome{Legs: passedLegs, Invariants: trueInv, Start: start},
			want:    domain.JourneyStale,
			reasons: []domain.Reason{reason(domain.OutcomeStale, "the manifest could not be read at the end")},
		},
		{
			name: "row 3: a leg ended stale",
			in: Outcome{Legs: []domain.LegResult{
				legResult("place", domain.OutcomeStale, "the build changed during the run"),
				legResult("pay", domain.OutcomePassed),
			}, Start: start, End: &same},
			want: domain.JourneyStale, first: "place",
			reasons: []domain.Reason{reason(domain.OutcomeStale, "leg place ended stale: the build changed during the run")},
		},
		{
			name: "row 4: the first leg that did not pass ended failed",
			in:   Outcome{Legs: failedPay, Start: start, End: &same},
			want: domain.JourneyFailed, first: "pay",
			reasons: []domain.Reason{reason(domain.OutcomeFailed, "leg pay failed: step pay: expected status 202, observed status 500 (source person)")},
		},
		{
			name:    "row 4: an invariant is false, with its values",
			in:      Outcome{Legs: passedLegs, Invariants: falseInv, Start: start, End: &same},
			want:    domain.JourneyFailed,
			reasons: []domain.Reason{reason(domain.OutcomeFailed, "invariant 1 is false: 1000, 1001, 1000")},
		},
		{
			name: "rows 2 and 4: blocked wins, and the false invariant stays a reason",
			in: Outcome{Legs: passedLegs, Invariants: []domain.InvariantResult{
				falseInv[0], {Index: 1, State: domain.CheckNotEvaluated, Detail: "a value holds a redaction marker"},
			}, Start: start, End: &same},
			want: domain.JourneyBlocked,
			reasons: []domain.Reason{
				reason(domain.OutcomeBlocked, "invariant 2 could not be evaluated: a value holds a redaction marker"),
				reason(domain.OutcomeFailed, "invariant 1 is false: 1000, 1001, 1000"),
			},
		},
		{
			name: "rows 3 and 4: stale wins over failed, and both reasons stay",
			in:   Outcome{Legs: failedPay, Start: start, End: &moved},
			want: domain.JourneyStale, first: "pay",
			reasons: []domain.Reason{
				reason(domain.OutcomeStale, "a build changed during the journey: orders 9f3c2ab then c0ffee1"),
				reason(domain.OutcomeFailed, "leg pay failed: step pay: expected status 202, observed status 500 (source person)"),
			},
		},
		{
			name: "rows 2 and 3: blocked wins over stale",
			in:   Outcome{Legs: blockedPay, Start: start},
			want: domain.JourneyBlocked, first: "pay",
			reasons: []domain.Reason{
				reason(domain.OutcomeBlocked, "leg pay ended blocked: the environment does not allow a step pay: write"),
				reason(domain.OutcomeStale, "the manifest could not be read at the end"),
			},
		},
		{
			name: "a journey interrupted before its first leg is not_run, and not stale: nothing was sent",
			in: Outcome{Legs: []domain.LegResult{
				legResult("place", domain.OutcomeNotRun, "the run was interrupted"),
				legResult("pay", domain.OutcomeNotRun, "the run was interrupted"),
			}, Start: start},
			want:    domain.JourneyNotRun,
			reasons: []domain.Reason{reason(domain.OutcomeNotRun, "leg place was not run: the run was interrupted")},
		},
		{
			name: "a not_run leg after a leg that ran, with no end manifest, is stale",
			in: Outcome{Legs: []domain.LegResult{
				legResult("place", domain.OutcomePassed),
				legResult("pay", domain.OutcomeNotRun, "the run was interrupted"),
			}, Start: start},
			want: domain.JourneyStale,
			reasons: []domain.Reason{
				reason(domain.OutcomeStale, "the manifest could not be read at the end"),
			},
		},
		{
			name: "a leg that did not run, with nothing else wrong, is not a pass",
			in: Outcome{Legs: []domain.LegResult{
				legResult("place", domain.OutcomePassed),
				legResult("pay", domain.OutcomeNotRun, "the run was interrupted"),
			}, Start: start, End: &same},
			want:    domain.JourneyNotRun,
			reasons: []domain.Reason{reason(domain.OutcomeNotRun, "leg pay was not run: the run was interrupted")},
		},
		{
			name: "a service added at the end and a configuration change are named",
			in: Outcome{Legs: passedLegs, Start: manifestOf(orders), End: func() *domain.Manifest {
				m := manifestOf(domain.BuildRef{Service: "orders", Commit: "9f3c2ab", ConfigHash: "c2"}, payments)
				return &m
			}()},
			want: domain.JourneyStale,
			reasons: []domain.Reason{
				reason(domain.OutcomeStale, "a build changed during the journey: orders 9f3c2ab with another configuration"),
				reason(domain.OutcomeStale, "a build changed during the journey: payments was not read at the start, then abc1234"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, first, reasons := Decide(tc.in)
			if got != tc.want {
				t.Errorf("outcome = %s, want %s", got, tc.want)
			}
			if first != tc.first {
				t.Errorf("first failing = %q, want %q", first, tc.first)
			}
			if !reflect.DeepEqual(reasons, tc.reasons) {
				t.Errorf("reasons:\n got %+v\nwant %+v", reasons, tc.reasons)
			}
		})
	}
}

func TestNoInconclusiveJourney(t *testing.T) {
	start := manifestOf(domain.BuildRef{Service: "orders", Commit: "9f3c2ab"})
	for _, o := range []domain.Outcome{domain.OutcomePassed, domain.OutcomeFailed, domain.OutcomeBlocked, domain.OutcomeInconclusive, domain.OutcomeNotRun, domain.OutcomeStale} {
		for _, end := range []*domain.Manifest{&start, nil} {
			got, first, _ := Decide(Outcome{Legs: []domain.LegResult{legResult("place", o, "why")}, Start: start, End: end})
			switch got {
			case domain.JourneyPassed, domain.JourneyFailed, domain.JourneyBlocked, domain.JourneyStale, domain.JourneyNotRun:
			default:
				t.Errorf("leg %s gave journey %q", o, got)
			}
			if o == domain.OutcomeInconclusive && got != domain.JourneyBlocked {
				t.Errorf("an inconclusive leg gave %s, want blocked", got)
			}
			if got != domain.JourneyPassed && o != domain.OutcomeNotRun && o != domain.OutcomePassed && first != "place" {
				t.Errorf("leg %s: first failing = %q", o, first)
			}
		}
	}
	_, _, reasons := Decide(Outcome{Legs: []domain.LegResult{legResult("place", domain.OutcomeInconclusive, "step create has no recorded result")}, Start: start, End: &start})
	if len(reasons) != 1 || reasons[0].Text != "leg place ended inconclusive: step create has no recorded result" || reasons[0].Rule != domain.OutcomeBlocked {
		t.Errorf("reasons = %+v", reasons)
	}
}
