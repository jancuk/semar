package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/report"
)

func attemptsRun() domain.Run {
	fail := domain.Attempt{Outcome: domain.OutcomeFailed, Reasons: []domain.Reason{{Rule: domain.OutcomeFailed, Text: "step pay: expected status 200, observed status 503 (source ticket)"}}}
	flaky := domain.ScenarioResult{Scenario: "PAY-1-01", Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{},
		Attempts: []domain.Attempt{fail, {Outcome: domain.OutcomePassed}}, AttemptsAllowed: 3, Flaky: true}
	broken := domain.ScenarioResult{Scenario: "PAY-1-02", Outcome: domain.OutcomeFailed, Reasons: fail.Reasons, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{},
		Attempts: []domain.Attempt{fail, fail, fail}, AttemptsAllowed: 3}
	once := domain.ScenarioResult{Scenario: "PAY-1-03", Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{}}
	return domain.Run{ID: "r-1", Environment: "local", Results: []domain.ScenarioResult{flaky, broken, once}}
}

func TestTheRunLinesNameTheAttempts(t *testing.T) {
	var b bytes.Buffer
	if err := report.Lines(&b, attemptsRun()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"PAY-1-01  passed  flaky, passed on attempt 2 of 3", "(failed on all 3 attempts)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the lines lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(strings.Split(out, "PAY-1-03")[1], "attempt") {
		t.Errorf("a scenario that ran once is marked:\n%s", out)
	}
}

func TestTheReportNamesTheFlakyScenarioAndEveryAttempt(t *testing.T) {
	var b bytes.Buffer
	err := report.Markdown(&b, report.Data{Run: attemptsRun(), Scenarios: map[domain.ScenarioID]domain.Scenario{}})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"- PAY-1-01 (flaky, passed on attempt 2 of 3)",
		"  - Attempt 1: failed: step pay: expected status 200, observed status 503 (source ticket)",
		"  - Attempt 2: passed",
		"Outcome: **failed** (failed on all 3 attempts)",
		"- Attempt 3: failed: step pay",
		"flaky 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}

func TestAFlakyPassStillExitsZero(t *testing.T) {
	run := attemptsRun()
	if got := report.ExitCode(run.Results[:1]); got != 0 {
		t.Errorf("a flaky pass exits %d, want 0", got)
	}
	if got := report.ExitCode(run.Results[1:2]); got != 1 {
		t.Errorf("a scenario that failed every attempt exits %d, want 1", got)
	}
}
