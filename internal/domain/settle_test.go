package domain_test

import (
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestSettleIsTheLastAttemptAndFlakyOnlyAfterAPassFollowingAMiss(t *testing.T) {
	at := func(o ...domain.Outcome) []domain.Attempt {
		out := make([]domain.Attempt, len(o))
		for i, x := range o {
			out[i] = domain.Attempt{Outcome: x}
		}
		return out
	}
	cases := []struct {
		name     string
		attempts []domain.Attempt
		want     domain.Outcome
		flaky    bool
	}{
		{"one pass", at(domain.OutcomePassed), domain.OutcomePassed, false},
		{"one failure", at(domain.OutcomeFailed), domain.OutcomeFailed, false},
		{"a miss then a pass", at(domain.OutcomeFailed, domain.OutcomePassed), domain.OutcomePassed, true},
		{"an inconclusive attempt then a pass", at(domain.OutcomeInconclusive, domain.OutcomePassed), domain.OutcomePassed, true},
		{"every attempt fails", at(domain.OutcomeFailed, domain.OutcomeFailed, domain.OutcomeFailed), domain.OutcomeFailed, false},
		{"a pass the builds make stale", at(domain.OutcomeStale, domain.OutcomeStale), domain.OutcomeStale, false},
		{"a miss then a block", at(domain.OutcomeFailed, domain.OutcomeBlocked), domain.OutcomeBlocked, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, flaky := domain.Settle(c.attempts)
			if got.Outcome != c.want || flaky != c.flaky {
				t.Errorf("Settle = %s, flaky %v; want %s, flaky %v", got.Outcome, flaky, c.want, c.flaky)
			}
		})
	}
	if got, flaky := domain.Settle(nil); got.Outcome != "" || flaky {
		t.Errorf("Settle(nil) = %+v, %v", got, flaky)
	}
}
