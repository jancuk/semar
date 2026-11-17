package plan

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/fake"
)

// turnPlanner answers each job with the next of its outputs, and keeps the jobs.
type turnPlanner struct {
	mu   sync.Mutex
	jobs []provider.Job
	outs []string
}

func (p *turnPlanner) fake() *fake.Fake {
	return fake.NewFunc("p", func(j provider.Job) (provider.Result, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.jobs = append(p.jobs, j)
		out := p.outs[min(len(p.jobs), len(p.outs))-1]
		return provider.Result{Output: json.RawMessage(out), Model: j.Model, Turns: 1, InputTokens: 100, OutputTokens: 10, CostUSD: 0.01}, nil
	})
}

const (
	clickTurn  = `{"turn":{"kind":"act","actions":[{"kind":"click","ref":"e2"}]}}`
	badRefTurn = `{"turn":{"kind":"act","actions":[{"kind":"click","ref":"e9"}]}}`
)

func turnRun(t *testing.T, outs ...string) (*runState, *turnPlanner) {
	t.Helper()
	in := shopInput(t)
	p := &turnPlanner{outs: outs}
	in.Ladder = ladder(Rung{Planner: p.fake(), Model: "haiku", Reasoning: "low"})
	in.JobDir = t.TempDir()
	return newRunState(in), p
}

func TestATurnIsAJobWithNoToolsThatThePlanCounts(t *testing.T) {
	rs, p := turnRun(t, clickTurn)
	turn, err := rs.turnModel(0, nil, 12)(t.Context(), TurnInput{Criterion: "c", Number: 1, First: true, Page: twoElementPage()})
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := turn.(TurnAct); !ok || a.Actions[0].Ref != "e2" {
		t.Fatalf("turn = %#v", turn)
	}
	j := p.jobs[0]
	if !j.NoTools || j.MaxTurns != CriterionMaxTurns || j.Model != "haiku" || j.Reasoning != "low" || j.Workdir != rs.in.JobDir || !strings.Contains(string(j.Schema), `"enum":["e1","e2"]`) {
		t.Errorf("job = %+v", j)
	}
	if !strings.Contains(j.Prompt, `e2 button "Save"`) {
		t.Errorf("the prompt lacks the page:\n%s", j.Prompt)
	}
	if rs.webUses[0].Calls != 1 || rs.uses[0].Calls != 0 || rs.calls != 1 || rs.total.CostUSD != 0.01 || rs.webUses[0].Result.InputTokens != 100 {
		t.Errorf("web uses %+v, uses %+v, calls %d, cost %v: a turn counts apart from a criterion, and in the total", rs.webUses[0], rs.uses[0], rs.calls, rs.total.CostUSD)
	}
}

func TestAnAnswerWithAProblemGoesBackOnceAndThenTheFlowEnds(t *testing.T) {
	rs, p := turnRun(t, badRefTurn, clickTurn)
	turn, err := rs.turnModel(0, nil, 12)(t.Context(), TurnInput{Criterion: "c", Number: 1, Page: twoElementPage()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := turn.(TurnAct); !ok || len(p.jobs) != 2 || rs.webCorr != 1 || rs.corr != 0 {
		t.Fatalf("turn %#v, jobs %d, corrections %d", turn, len(p.jobs), rs.webCorr)
	}
	for _, want := range []string{"Your previous answer was rejected", "e9 is not in the list of the page"} {
		if !strings.Contains(p.jobs[1].Prompt, want) {
			t.Errorf("the correction lacks %q", want)
		}
	}

	rs, p = turnRun(t, badRefTurn)
	_, err = rs.turnModel(0, nil, 12)(t.Context(), TurnInput{Criterion: "c", Number: 1, Page: twoElementPage()})
	var no *NoTurnError
	if !errors.As(err, &no) || !strings.Contains(no.Reason, "e9") || len(p.jobs) != 2 {
		t.Errorf("err %v after %d jobs", err, len(p.jobs))
	}

	rs, _ = turnRun(t, "not json")
	if _, err = rs.turnModel(0, nil, 12)(t.Context(), TurnInput{Page: twoElementPage()}); !errors.As(err, &no) {
		t.Errorf("an output that is no JSON: %v", err)
	}
}

func TestATurnDoesNotStartOnceTheCostLimitIsReached(t *testing.T) {
	rs, p := turnRun(t, clickTurn)
	rs.in.MaxCost = 0.01
	model := rs.turnModel(0, nil, 12)
	if _, err := model(t.Context(), TurnInput{Page: twoElementPage()}); err != nil {
		t.Fatal(err)
	}
	if _, err := model(t.Context(), TurnInput{Page: twoElementPage()}); !errors.Is(err, ErrCostLimit) || len(p.jobs) != 1 || !rs.limited {
		t.Errorf("err %v after %d jobs, limited %v", err, len(p.jobs), rs.limited)
	}
}
