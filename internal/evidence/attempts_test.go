package evidence_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestARunWithAttemptsStoresLoadsAndVerifies(t *testing.T) {
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= 2; seq++ {
		if _, err := r.WriteAction(action(seq, "refund", "too-much")); err != nil {
			t.Fatal(err)
		}
	}
	attempts := []domain.Attempt{
		{Outcome: domain.OutcomeFailed, Reasons: []domain.Reason{{Rule: domain.OutcomeFailed, Text: "step too-much: expected status 422, observed status 503 (source ticket)"}}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{"a-0001"}},
		{Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{"a-0002"}},
	}
	res := domain.ScenarioResult{Scenario: "refund", Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{"a-0002"},
		Attempts: attempts, AttemptsAllowed: 3, Flaky: true}
	if err := r.Close(domain.Run{ID: "r1", Environment: "staging", StartedAt: t0, EndedAt: t0, Results: []domain.ScenarioResult{res}}); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.Verify("r1"); err != nil || len(changed) != 0 {
		t.Fatalf("Verify = %v, %v", changed, err)
	}
	run, _, err := s.LoadRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if got := run.Results[0]; !reflect.DeepEqual(got.Attempts, attempts) || !got.Flaky || got.AttemptsAllowed != 3 {
		t.Errorf("loaded result %+v", got)
	}
}

func TestARunStoredBeforeAttemptsStillLoadsAndVerifies(t *testing.T) {
	s := openStore(t)
	closedRun(t, s, "r1")
	dir := filepath.Join(s.Root(), "runs", "r1")
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	old := `{"schema":1,"id":"r1","environment":"staging","startedAt":"2026-11-14T10:00:00Z","endedAt":"2026-11-14T10:00:00Z","buildsStart":[],"buildsEnd":[],` +
		`"results":[{"scenario":"refund","outcome":"passed","reasons":[],"checks":[],"actions":["a-0001"],"expectHash":"abc"}],` + string(data[strings.Index(string(data), `"files"`):])
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.Verify("r1"); err != nil || len(changed) != 0 {
		t.Fatalf("Verify = %v, %v", changed, err)
	}
	run, _, err := s.LoadRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if got := run.Results[0]; got.Outcome != domain.OutcomePassed || got.Attempts != nil || got.Flaky || got.AttemptsAllowed != 0 {
		t.Errorf("a result with no attempts loaded as %+v", got)
	}
}
