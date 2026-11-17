package evidence_test

import (
	"fmt"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
)

func runOf(t *testing.T, s *evidence.Store, id domain.RunID, purpose string, results ...domain.ScenarioResult) {
	t.Helper()
	r, err := s.NewRun(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(domain.Run{ID: id, Environment: "staging", Purpose: purpose, StartedAt: t0, EndedAt: t0, Results: results}); err != nil {
		t.Fatal(err)
	}
}

func resultOf(scenario domain.ScenarioID, h domain.Hash) domain.ScenarioResult {
	return domain.ScenarioResult{Scenario: scenario, ExpectHash: h, Outcome: domain.OutcomeInconclusive, Checks: []domain.CheckResult{
		{Step: "create", Index: 0, State: domain.CheckFalse, Expected: "status 201", Observed: "200"},
	}}
}

func TestLastRunOfIsTheNewestRunWithTheSameExpectationHash(t *testing.T) {
	s := openStore(t)
	runOf(t, s, "r1", "", resultOf("S-1", "h1"))
	runOf(t, s, "r2", "", resultOf("S-1", "h1"))
	runOf(t, s, "r3", "", resultOf("S-1", "h2"), resultOf("S-2", "h1"))
	runOf(t, s, "r4", domain.PurposeTrial, resultOf("S-1", "h1"))
	runOf(t, s, "r5", domain.PurposeFixtureCheck, resultOf("S-1", "h1"))
	runOf(t, s, "r6", "", resultOf("S-1", ""))

	got, ok, err := s.LastRunOf("S-1", "h1")
	if err != nil || !ok {
		t.Fatalf("LastRunOf = %v, %v", ok, err)
	}
	if got.Run != "r2" || got.Result.Scenario != "S-1" || len(got.Result.Checks) != 1 || got.Result.Checks[0].Observed != "200" {
		t.Errorf("last run = %+v, want the result of S-1 in r2", got)
	}
	if _, ok, err := s.LastRunOf("S-1", "h3"); ok || err != nil {
		t.Errorf("LastRunOf for a hash that no run has = %v, %v", ok, err)
	}
}

func TestLastRunOfLooksAtTheThirtyNewestRunsOnly(t *testing.T) {
	s := openStore(t)
	runOf(t, s, "r00", "", resultOf("S-1", "h1"))
	for i := 1; i <= 30; i++ {
		runOf(t, s, domain.RunID(fmt.Sprintf("r%02d", i)), "", resultOf("S-2", "h1"))
	}
	if _, ok, err := s.LastRunOf("S-1", "h1"); ok || err != nil {
		t.Errorf("LastRunOf found a run that is older than the 30 newest: %v, %v", ok, err)
	}
}

func TestLastRunOfSkipsARunThatCannotBeRead(t *testing.T) {
	s := openStore(t)
	runOf(t, s, "r1", "", resultOf("S-1", "h1"))
	if _, err := s.NewRun("r2"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.LastRunOf("S-1", "h1")
	if err != nil || !ok || got.Run != "r1" {
		t.Errorf("LastRunOf = %+v, %v, %v, want r1: r2 was interrupted", got, ok, err)
	}
}

func TestLastRunOfInAStoreWithNoRuns(t *testing.T) {
	s := openStore(t)
	if _, ok, err := s.LastRunOf("S-1", "h1"); ok || err != nil {
		t.Errorf("LastRunOf = %v, %v", ok, err)
	}
}

func TestExchangesAreTheLastActionOfEachStepOfTheScenario(t *testing.T) {
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	write := func(seq int, scenario, step string, pass domain.Pass, status int) {
		a := action(seq, scenario, step)
		a.Use.Pass = pass
		a.Response.Status = status
		rel, err := r.WriteAction(a)
		if err != nil {
			t.Fatal(err)
		}
		act := activity(seq)
		act.Use, act.Evidence = a.Use, rel
		if err := r.AppendActivity(act); err != nil {
			t.Fatal(err)
		}
	}
	write(1, "S-1", "make", domain.PassSetup, 201)
	write(2, "S-1", "read", domain.PassScenario, 404)
	write(3, "S-1", "read", domain.PassScenario, 200)
	write(4, "S-2", "read", domain.PassScenario, 500)
	write(5, "S-1", "clean", domain.PassCleanup, 204)
	if err := r.Close(domain.Run{ID: "r1", Environment: "staging", StartedAt: t0, EndedAt: t0}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Exchanges("r1", "S-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["make"].Response.Status != 201 || got["read"].Response.Status != 200 {
		t.Errorf("exchanges = %+v, want make 201 and read 200 only", got)
	}
}
