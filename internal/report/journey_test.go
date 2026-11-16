package report_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/report"
)

// journeyFixture is a run of two journeys: checkout failed on its last leg, and refund passed.
func journeyFixture() report.Data {
	t0 := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	services := []domain.BuildRef{
		{Service: "orders", Commit: "9f3c2ab", ConfigHash: "sha256:11aa"},
		{Service: "payments", Commit: "c0ffee1", ConfigHash: "sha256:22bb"},
	}
	manifest := domain.Manifest{Services: services, Hash: domain.ManifestHash(services)}
	run := domain.Run{
		Schema: 1, ID: "r-journey", Environment: "local", EnvironmentKind: "test", Purpose: domain.PurposeJourney,
		StartedAt: t0, EndedAt: t0.Add(time.Minute), BuildsStart: services, BuildsEnd: services,
		Results: []domain.ScenarioResult{
			{Scenario: "checkout.place", Outcome: domain.OutcomePassed},
			{Scenario: "checkout.pay", Outcome: domain.OutcomePassed},
			{Scenario: "checkout.settled", Outcome: domain.OutcomeFailed, Reasons: []domain.Reason{
				{Rule: domain.OutcomeFailed, Text: "waited 30s and $.data.status was never paid (6 attempts)", Step: "read"},
			}},
			{Scenario: "refund.ask", Outcome: domain.OutcomePassed},
		},
	}
	checkout := domain.JourneyResult{
		Journey: "checkout", Manifest: manifest, ManifestEnd: &manifest, Outcome: domain.JourneyFailed, FirstFailing: "settled",
		Legs: []domain.LegResult{
			{Leg: "place", Scenario: "checkout.place", Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}},
			{Leg: "pay", Scenario: "checkout.pay", Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}},
			{Leg: "settled", Scenario: "checkout.settled", Outcome: domain.OutcomeFailed, Reasons: []domain.Reason{
				{Rule: domain.OutcomeFailed, Text: "waited 30s and $.data.status was never paid (6 attempts)", Step: "read"},
			}},
		},
		Invariants: []domain.InvariantResult{{Index: 0, State: domain.CheckFalse, Values: []string{"1000", "1000", "999"}}},
		Reasons: []domain.Reason{
			{Rule: domain.OutcomeFailed, Text: "leg settled failed: waited 30s and $.data.status was never paid (6 attempts)"},
			{Rule: domain.OutcomeFailed, Text: "invariant 1 is false: 1000, 1000, 999"},
		},
	}
	one := domain.Manifest{Services: services[:1], Hash: domain.ManifestHash(services[:1])}
	refund := domain.JourneyResult{
		Journey: "refund", Manifest: one, ManifestEnd: &one, Outcome: domain.JourneyPassed,
		Legs:       []domain.LegResult{{Leg: "ask", Scenario: "refund.ask", Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}}},
		Invariants: []domain.InvariantResult{}, Reasons: []domain.Reason{},
	}
	run.Journeys = []domain.JourneyResult{checkout, refund}
	return report.Data{
		Run: run, Journeys: run.Journeys,
		JourneyTitles: map[domain.JourneyID]string{"checkout": "An order is placed, paid, and *shown* as paid", "refund": "A refund"},
		Scenarios: map[domain.ScenarioID]domain.Scenario{
			"checkout.settled": {ID: "checkout.settled", Title: "An order is placed: settled"},
		},
		Coverage: "3 use cases: 2 verified, 1 failed", CoverageTickets: []string{"SHOP-482"},
	}
}

func TestReportJourneySection(t *testing.T) {
	compareGolden(t, "report.journey.golden.md", []byte(render(t, journeyFixture())))
}

func TestReportJourneySectionComesAfterTheCoverageLine(t *testing.T) {
	out := render(t, journeyFixture())
	cov, sec, env := strings.Index(out, "3 use cases"), strings.Index(out, "## Journeys"), strings.Index(out, "- Environment:")
	if cov < 0 || sec < cov || env < sec {
		t.Errorf("order: coverage %d, journeys %d, facts %d:\n%s", cov, sec, env, out)
	}
}

func TestReportWithoutJourneysHasNoSection(t *testing.T) {
	if out := render(t, fixture()); strings.Contains(out, "Journeys") {
		t.Errorf("a run without journeys has a journeys section:\n%s", out)
	}
}

func TestReportLegReproducesThroughItsJourney(t *testing.T) {
	out := render(t, journeyFixture())
	if !strings.Contains(out, "Reproduce: semar journey run checkout --env local") {
		t.Errorf("a failed leg does not name its journey:\n%s", out)
	}
	if strings.Contains(out, "semar run --scenario checkout.settled") {
		t.Errorf("a leg has no scenario file, so it cannot be run alone:\n%s", out)
	}
}

func TestReportJourneyHostileValues(t *testing.T) {
	d := journeyFixture()
	d.JourneyTitles["checkout"] = hostile("title")
	jr := &d.Journeys[0]
	jr.Journey = domain.JourneyID(hostile("journey"))
	jr.FirstFailing = domain.LegID(hostile("first"))
	jr.Reasons[0].Text = hostile("reason")
	jr.Legs[2].Reasons[0].Text = hostile("legreason")
	jr.Legs[1].Leg = domain.LegID(hostile("leg"))
	jr.Invariants[0].Values[0] = hostile("value")
	jr.Invariants[0].Detail = hostile("detail")
	jr.Manifest.Services = []domain.BuildRef{{Service: hostile("service"), Commit: hostile("commit")}}
	d.JourneyTitles[jr.Journey] = hostile("title2")
	d.Run.Journeys = d.Journeys
	out := render(t, d)
	for _, bad := range []string{"<script>", "</script>", "[l](", "http://", "https://", "www.", "$x$", "\u202e", "\u200b"} {
		if strings.Contains(out, bad) {
			t.Errorf("report contains %q:\n%s", bad, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "# Semar report") && !strings.HasPrefix(line, "### ") &&
			!strings.HasPrefix(line, "## Data touched") && line != "## Journeys" {
			t.Errorf("a value started a heading: %q", line)
		}
	}
}

func TestReportJourneyCutsLongValues(t *testing.T) {
	d := journeyFixture()
	d.JourneyTitles["checkout"] = strings.Repeat("t", 5000)
	d.Journeys[0].Reasons[0].Text = strings.Repeat("r", 5000)
	if out := render(t, d); strings.Contains(out, strings.Repeat("t", 300)) || strings.Contains(out, strings.Repeat("r", 300)) {
		t.Errorf("a long value was not cut:\n%s", out)
	}
}

func TestReportJourneyEndManifest(t *testing.T) {
	d := journeyFixture()
	d.Journeys[0].ManifestEnd = nil
	if out := render(t, d); !strings.Contains(out, "- Manifest at the end: not read") {
		t.Errorf("a journey with no end manifest:\n%s", out)
	}
	d = journeyFixture()
	changed := d.Journeys[0].Manifest
	changed.Services = []domain.BuildRef{{Service: "orders", Commit: "bad0bad", ConfigHash: "sha256:11aa"}}
	changed.Hash = domain.ManifestHash(changed.Services)
	d.Journeys[0].ManifestEnd = &changed
	if out := render(t, d); !strings.Contains(out, "- Manifest at the end: orders bad0bad") {
		t.Errorf("a journey whose builds changed:\n%s", out)
	}
}

func TestSummaryJourneyGolden(t *testing.T) {
	var b bytes.Buffer
	if err := report.Summary(&b, journeyFixture().Run, "3 use cases: 2 verified, 1 failed"); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "summary.journey.golden.txt", b.Bytes())
}

func TestSummaryJourneyWithoutAFailingLeg(t *testing.T) {
	run := journeyFixture().Run
	run.Journeys = run.Journeys[:1]
	run.Journeys[0].FirstFailing = ""
	run.Journeys[0].Legs = nil
	run.Journeys[0].Reasons = []domain.Reason{{Rule: domain.OutcomeBlocked, Text: "the build of payments could not be read"}}
	run.Journeys[0].Outcome = domain.JourneyBlocked
	var b bytes.Buffer
	if err := report.Summary(&b, run, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "checkout  blocked  the build of payments could not be read\n") {
		t.Errorf("summary:\n%s", b.String())
	}
}

func TestSummaryJourneyIsOneLineAndClean(t *testing.T) {
	run := journeyFixture().Run
	run.Journeys[0].Legs[2].Reasons[0].Text = "evil \x1b[31mred\x1b[0m\nline\u202e"
	var b bytes.Buffer
	if err := report.Summary(&b, run, ""); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(b.String(), "\x1b\u202e") || strings.Contains(b.String(), "red\nline") {
		t.Errorf("summary holds a control character:\n%q", b.String())
	}
}

func TestJourneyExitCode(t *testing.T) {
	cases := []struct {
		name     string
		outcomes []domain.JourneyOutcome
		want     int
	}{
		{"all passed", []domain.JourneyOutcome{"passed", "passed"}, 0},
		{"failed wins", []domain.JourneyOutcome{"passed", "stale", "failed"}, 1},
		{"blocked", []domain.JourneyOutcome{"passed", "blocked"}, 2},
		{"stale", []domain.JourneyOutcome{"stale"}, 2},
		{"not run", []domain.JourneyOutcome{"not_run"}, 2},
		{"none", nil, 2},
		{"unknown is not passed", []domain.JourneyOutcome{"passed", "inconclusive"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var js []domain.JourneyResult
			for _, o := range c.outcomes {
				js = append(js, domain.JourneyResult{Outcome: o})
			}
			if got := report.JourneyExitCode(js); got != c.want {
				t.Errorf("JourneyExitCode = %d, want %d", got, c.want)
			}
		})
	}
}
