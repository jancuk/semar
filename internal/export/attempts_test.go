package export_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/export"
)

// retriedRun has one scenario that passed on its second attempt and one that failed all three.
func retriedRun() export.Run {
	fail := domain.Attempt{Outcome: domain.OutcomeFailed, Reasons: []domain.Reason{{Rule: domain.OutcomeFailed, Text: "step pay: expected status 200, observed status 503 (source ticket)"}}}
	pass := domain.Attempt{Outcome: domain.OutcomePassed}
	flaky := result("PAY-1-01", domain.OutcomePassed)
	flaky.Attempts, flaky.AttemptsAllowed, flaky.Flaky = []domain.Attempt{fail, pass}, 3, true
	broken := result("PAY-1-02", domain.OutcomeFailed, fail.Reasons[0].Text)
	broken.Attempts, broken.AttemptsAllowed = []domain.Attempt{fail, fail, fail}, 3
	run := sampleRun()
	run.Scenarios = []export.Scenario{
		{Result: flaky, Title: "Pay an order", Ticket: "PAY-1", DurationMS: 900},
		{Result: broken, Title: "Pay twice", Ticket: "PAY-1", DurationMS: 700},
		{Result: result("PAY-1-03", domain.OutcomePassed), Title: "Read a payment", Ticket: "PAY-1", DurationMS: 10},
	}
	return run
}

func TestCTRFCarriesTheAttemptsAndTheFlakyMark(t *testing.T) {
	var rep struct {
		Results struct {
			Tests []struct {
				Name    string            `json:"name"`
				Status  string            `json:"status"`
				Retries int               `json:"retries"`
				Flaky   bool              `json:"flaky"`
				Extra   map[string]string `json:"extra"`
			} `json:"tests"`
		} `json:"results"`
	}
	if err := json.Unmarshal(render(t, func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }, retriedRun()), &rep); err != nil {
		t.Fatal(err)
	}
	tests := rep.Results.Tests
	if len(tests) != 3 {
		t.Fatalf("%d tests, want 3", len(tests))
	}
	if g := tests[0]; g.Status != "passed" || g.Retries != 1 || !g.Flaky || g.Extra["semarAttempts"] != "failed, passed" {
		t.Errorf("flaky test: %+v", g)
	}
	if g := tests[1]; g.Status != "failed" || g.Retries != 2 || g.Flaky || g.Extra["semarAttempts"] != "failed, failed, failed" {
		t.Errorf("failed test: %+v", g)
	}
	if g := tests[2]; g.Retries != 0 || g.Flaky || g.Extra["semarAttempts"] != "" {
		t.Errorf("a test that ran once carries attempts: %+v", g)
	}
}

func TestJUnitCarriesTheAttemptsAsFlakyAndRerunElements(t *testing.T) {
	data := string(render(t, writeJUnit, retriedRun()))
	s := decodeJUnit(t, []byte(data))
	if s.Failures != 1 || s.Errors != 0 || s.Tests != 3 {
		t.Errorf("counts: %d tests, %d failures, %d errors; a flaky pass is not a failure", s.Tests, s.Failures, s.Errors)
	}
	if n := strings.Count(data, "<flakyFailure "); n != 1 {
		t.Errorf("%d flakyFailure elements, want 1:\n%s", n, data)
	}
	if n := strings.Count(data, "<rerunFailure "); n != 2 {
		t.Errorf("%d rerunFailure elements, want 2:\n%s", n, data)
	}
	if !strings.Contains(data, `name="semar.flaky" value="true"`) || !strings.Contains(data, "attempts: 2, flaky: true") {
		t.Errorf("the flaky case does not say so:\n%s", data)
	}
	if strings.Count(data, "semar.attempts") != 2 {
		t.Errorf("a case that ran once holds the attempts property:\n%s", data)
	}
}
