package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/fake"
)

// manyCriteria is a ticket of n criteria that only a person can check, so that each is a manual check.
func manyCriteria(n int) knowledge.Ticket {
	var b strings.Builder
	b.WriteString("# SHOP-482: Refunds of an order\n\n## Acceptance criteria\n\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%d. Web: the order page number %d shows the refund.\n", i, i)
	}
	return knowledge.Ticket{Key: "SHOP-482", Path: ticket482, Text: b.String()}
}

func costed(id string, cost float64, ran *atomic.Int64) *fake.Fake {
	return fake.NewFunc(id, func(j provider.Job) (provider.Result, error) {
		ran.Add(1)
		return provider.Result{Output: json.RawMessage(manualAnswer), Model: j.Model, Turns: 1, InputTokens: 10, CostUSD: cost}, nil
	})
}

func TestTheCostLimitStopsNewJobsAndAccountsForTheCriteriaItDidNotReach(t *testing.T) {
	in := shopInput(t)
	in.Tickets = []knowledge.Ticket{manyCriteria(12)}
	in.MaxCost = 0.02
	var ran atomic.Int64
	in.Ladder = ladder(Rung{Planner: costed("p", 0.01, &ran), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	st := out.Pipeline
	jobs := int(ran.Load())
	if jobs < 2 || jobs > 2+MaxParallelJobs || jobs >= 12 {
		t.Fatalf("the plan ran %d jobs for 12 criteria with a limit of two jobs", jobs)
	}
	if !out.CostLimit || st.NotPlanned != 12-jobs || st.Unanswered != 0 || len(out.Draft.ManualChecks) != jobs {
		t.Fatalf("cost limit %v, not planned %d, unanswered %d, manual checks %d, jobs %d", out.CostLimit, st.NotPlanned, st.Unanswered, len(out.Draft.ManualChecks), jobs)
	}
	if len(out.Unaccounted) != 0 || len(out.Draft.NotDrafted) != 12-jobs {
		t.Errorf("every criterion is accounted for: unaccounted %v, not drafted %d", out.Unaccounted, len(out.Draft.NotDrafted))
	}
	for _, nd := range out.Draft.NotDrafted {
		if !strings.Contains(nd.Needs, "not planned: the cost limit of $0.02 was reached") || nd.Cite == "" {
			t.Errorf("not drafted = %+v", nd)
		}
	}
}

func TestAProviderThatReportsNoCostIsNotBound(t *testing.T) {
	in := shopInput(t)
	in.Tickets = []knowledge.Ticket{manyCriteria(6)}
	in.MaxCost = 0.0001
	var ran atomic.Int64
	in.Ladder = ladder(Rung{Planner: costed("p", 0, &ran), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 6 || out.CostLimit || out.Pipeline.NotPlanned != 0 {
		t.Errorf("ran %d, cost limit %v, not planned %d", ran.Load(), out.CostLimit, out.Pipeline.NotPlanned)
	}
}

func TestNoLimitPlansEveryCriterion(t *testing.T) {
	in := shopInput(t)
	in.Tickets = []knowledge.Ticket{manyCriteria(6)}
	var ran atomic.Int64
	in.Ladder = ladder(Rung{Planner: costed("p", 5, &ran), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil || ran.Load() != 6 || out.CostLimit {
		t.Errorf("ran %d, cost limit %v, err %v", ran.Load(), out.CostLimit, err)
	}
}

func TestTheSinglePlannerStopsBetweenAttemptsOverTheLimit(t *testing.T) {
	in := shopInput(t)
	in.MaxCost = 1.5
	p := fake.NewFunc("p", func(provider.Job) (provider.Result, error) {
		return provider.Result{Output: json.RawMessage(`{"summary":"x","tickets":[],"scenarios":[],"notDrafted":[],"uncovered":[]}`), CostUSD: 1}, nil
	})
	out, err := Run(context.Background(), p, in)
	var limit *CostLimitError
	if !errors.As(err, &limit) {
		t.Fatalf("err = %v, want a cost limit error", err)
	}
	if limit.Limit != 1.5 || limit.Spent != 2 || limit.Attempts != 2 || out.Result.CostUSD != 2 {
		t.Errorf("limit error = %+v, cost %v", limit, out.Result.CostUSD)
	}
}
