package export_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/export"
)

// journeyRun is a run whose two legs passed. The journey result is the argument.
func journeyRun(j domain.JourneyResult) export.Run {
	r := sampleRun()
	r.Scenarios = []export.Scenario{
		{Result: result("checkout.pay", domain.OutcomePassed), DurationMS: 100},
		{Result: result("checkout.ship", domain.OutcomePassed), DurationMS: 200},
	}
	r.Run.Journeys = []domain.JourneyResult{j}
	return r
}

func journeyResult(o domain.JourneyOutcome, first domain.LegID, reasons ...string) domain.JourneyResult {
	j := domain.JourneyResult{Journey: "checkout", Outcome: o, FirstFailing: first, Legs: []domain.LegResult{
		{Leg: "pay", Scenario: "checkout.pay", Outcome: domain.OutcomePassed},
		{Leg: "ship", Scenario: "checkout.ship", Outcome: domain.OutcomePassed},
	}}
	for _, text := range reasons {
		j.Reasons = append(j.Reasons, domain.Reason{Rule: domain.OutcomeFailed, Text: text})
	}
	return j
}

func TestJUnitExportsTheJourneyOutcome(t *testing.T) {
	tests := []struct {
		outcome domain.JourneyOutcome
		element string
	}{
		{domain.JourneyPassed, ""},
		{domain.JourneyFailed, "failure"},
		{domain.JourneyBlocked, "error"},
		{domain.JourneyStale, "error"},
		{domain.JourneyNotRun, "skipped"},
	}
	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			j := journeyResult(tt.outcome, "", "invariant 1 is false: a, b")
			s := decodeJUnit(t, render(t, writeJUnit, journeyRun(j)))
			var found *junitCase
			for i := range s.Suites {
				for k := range s.Suites[i].Cases {
					if s.Suites[i].Cases[k].Name == "journey checkout" {
						found = &s.Suites[i].Cases[k]
					}
				}
			}
			if found == nil {
				t.Fatalf("no test case for the journey:\n%s", render(t, writeJUnit, journeyRun(j)))
			}
			var got, msg string
			switch {
			case found.Failure != nil:
				got, msg = "failure", found.Failure.Message
			case found.Error != nil:
				got, msg = "error", found.Error.Message
			case found.Skipped != nil:
				got, msg = "skipped", found.Skipped.Message
			}
			if got != tt.element {
				t.Errorf("element %q, want %q", got, tt.element)
			}
			if tt.element != "" && !strings.Contains(msg, "invariant 1 is false") {
				t.Errorf("message %q lacks the reason", msg)
			}
			if want := "semar outcome: " + string(tt.outcome); found.SystemOut != want {
				t.Errorf("system-out = %q, want %q", found.SystemOut, want)
			}
		})
	}
}

func TestJUnitCountsAFailedJourney(t *testing.T) {
	s := decodeJUnit(t, render(t, writeJUnit, journeyRun(journeyResult(domain.JourneyFailed, "", "invariant 1 is false: a, b"))))
	if s.Failures != 1 || s.Tests != 3 {
		t.Errorf("tests=%d failures=%d, want 3 and 1", s.Tests, s.Failures)
	}
}

func TestCTRFExportsTheJourneyOutcome(t *testing.T) {
	tests := []struct {
		outcome domain.JourneyOutcome
		want    string
	}{
		{domain.JourneyPassed, "passed"},
		{domain.JourneyFailed, "failed"},
		{domain.JourneyBlocked, "other"},
		{domain.JourneyStale, "other"},
		{domain.JourneyNotRun, "skipped"},
	}
	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			j := journeyResult(tt.outcome, "", "invariant 1 is false: a, b")
			m := decodeCTRF(t, render(t, func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }, journeyRun(j)))
			for _, test := range m.Results.Tests {
				if test.Name != "journey checkout" {
					continue
				}
				if test.Status != tt.want {
					t.Errorf("status %q, want %q", test.Status, tt.want)
				}
				if test.Extra["semarOutcome"] != string(tt.outcome) {
					t.Errorf("extra.semarOutcome = %q", test.Extra["semarOutcome"])
				}
				if tt.outcome != domain.JourneyPassed && (test.Message == nil || !strings.Contains(*test.Message, "invariant 1 is false")) {
					t.Errorf("message = %v", test.Message)
				}
				return
			}
			t.Fatal("no test for the journey")
		})
	}
}

func TestCTRFCountsAFailedJourney(t *testing.T) {
	m := decodeCTRF(t, render(t, func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }, journeyRun(journeyResult(domain.JourneyFailed, "", "x"))))
	if *m.Results.Summary.Failed != 1 || *m.Results.Summary.Tests != 3 {
		t.Errorf("tests=%d failed=%d, want 3 and 1", *m.Results.Summary.Tests, *m.Results.Summary.Failed)
	}
}

func TestSARIFHoldsAFailedJourney(t *testing.T) {
	j := journeyResult(domain.JourneyFailed, "ship", "leg ship failed: expected status 200", "invariant 1 is false: a, b")
	m := decodeSARIF(t, render(t, writeSARIF, journeyRun(j)))
	res := *m.Runs[0].Results
	if len(res) != 1 {
		t.Fatalf("want one result, got %d", len(res))
	}
	msg := res[0].Message.Text
	for _, want := range []string{"checkout", "ship", "invariant 1 is false: a, b"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if res[0].Level != "error" || res[0].RuleIndex == nil || m.Runs[0].Tool.Driver.Rules[*res[0].RuleIndex].ID != res[0].RuleID {
		t.Errorf("rule or level wrong: %+v", res[0])
	}
	if res[0].Properties["semarJourney"] != "checkout" || res[0].PartialFingerprints["semar/v1"] == "" {
		t.Errorf("properties or fingerprint missing: %+v", res[0])
	}
}

func TestSARIFOnlyAFailedJourneyIsAFinding(t *testing.T) {
	for _, o := range []domain.JourneyOutcome{domain.JourneyPassed, domain.JourneyBlocked, domain.JourneyStale, domain.JourneyNotRun} {
		m := decodeSARIF(t, render(t, writeSARIF, journeyRun(journeyResult(o, "", "x"))))
		if n := len(*m.Runs[0].Results); n != 0 {
			t.Errorf("%s: %d results, want none", o, n)
		}
	}
}
