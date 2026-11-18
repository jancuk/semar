package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/stage/plan"
)

const (
	earlierFile = "docs/tickets/SHOP-482.md"
	earlierCite = earlierFile + ":5 A refund above the total is refused"
)

func earlierScenario(id, ticket, cite string) domain.Scenario {
	sc := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: domain.ScenarioID(id), Title: "Title of " + id, Ticket: ticket, Surface: domain.SurfaceAPI, AuthProfile: "default",
		Steps: []domain.Step{{ID: "s", Action: "do it", Expect: []domain.Expectation{{Check: domain.Check{JSONPath: "$.a", Equals: "b"}, Source: domain.SourceDiff}}}},
	}
	if cite != "" {
		sc.Steps[0].Expect = append(sc.Steps[0].Expect, domain.Expectation{Check: domain.Check{JSONPath: "$.c", Equals: "d"}, Source: domain.SourceTicket, Cite: cite})
	}
	return sc
}

// earlierRepo writes scenario files and one obligations file into a directory.
func earlierRepo(t *testing.T, scenarios []domain.Scenario, ticket string, entries ...obligation.Obligation) (string, []domain.Scenario) {
	t.Helper()
	dir := t.TempDir()
	for _, sc := range scenarios {
		data, err := scenario.Render(sc, "")
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, ".semar", "scenarios", sc.Ticket, string(sc.ID)+".feature")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := obligation.Marshal(obligation.File{APIVersion: "semar/v1", Kind: "Obligations", Ticket: ticket, Obligations: entries})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, obligationsFile(ticket))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	scs, err := loadAllScenarios(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, scs
}

func TestEarlierReadsTheEntriesOfAnObligationsFile(t *testing.T) {
	key := strings.Repeat("a", 64)
	ref := &obligation.CriterionRef{Number: 1, Key: key}
	dir, scs := earlierRepo(t,
		[]domain.Scenario{earlierScenario("SHOP-482-01", "SHOP-482", earlierCite), earlierScenario("SHOP-482-03", "SHOP-482", earlierCite)},
		"SHOP-482",
		obligation.Obligation{ID: "SHOP-482/scenario-SHOP-482-01", Title: "T", Source: obligation.SourceDiff, Covers: []domain.ScenarioID{"SHOP-482-01"}, Criterion: ref},
		// The file of this scenario was removed.
		obligation.Obligation{ID: "SHOP-482/scenario-SHOP-482-02", Title: "T2", Source: obligation.SourceDiff, Covers: []domain.ScenarioID{"SHOP-482-02"}, Criterion: ref},
		// An entry of a plan before the key: it has no criterion, and its scenario holds the cite.
		obligation.Obligation{ID: "SHOP-482/scenario-SHOP-482-03", Title: "T3", Source: obligation.SourceDiff, Covers: []domain.ScenarioID{"SHOP-482-03"}},
		obligation.Obligation{ID: "SHOP-482/manual-1", Title: "M", Source: obligation.SourceCriterion, Cite: earlierFile + ":6 The owner sees it", Manual: &obligation.Manual{Kind: "judgement", Text: "t", Steps: []string{"s"}, Expected: "e", WrittenBy: "ai"}},
		obligation.Obligation{ID: "SHOP-482/not-drafted-1", Title: "N", Source: obligation.SourceDiff, Gap: "not drafted: needs x", Criterion: &obligation.CriterionRef{Number: 3, Key: strings.Repeat("b", 64)}},
		obligation.Obligation{ID: "SHOP-482/uncovered-1", Title: "U", Source: obligation.SourceDiff, Gap: "g"},
	)
	got := readEarlier(dir, map[string]bool{"SHOP-482": true}, scs)["SHOP-482"]
	want := []plan.Earlier{
		{Number: 1, Key: key, Tests: []domain.ScenarioID{"SHOP-482-01"}},
		{Number: 1, Key: key},
		{Cites: []string{earlierCite}, Tests: []domain.ScenarioID{"SHOP-482-03"}},
		{Cites: []string{earlierFile + ":6 The owner sees it"}, Manual: true},
		{Number: 3, Key: strings.Repeat("b", 64)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Number != w.Number || g.Key != w.Key || g.Manual != w.Manual || strings.Join(g.Cites, "|") != strings.Join(w.Cites, "|") || len(g.Tests) != len(w.Tests) || (len(w.Tests) > 0 && g.Tests[0] != w.Tests[0]) {
			t.Errorf("entry %d is %+v, want %+v", i, g, w)
		}
	}
}

func TestEarlierIgnoresATicketOutsideTheChangeAndAFileWithProblems(t *testing.T) {
	ref := &obligation.CriterionRef{Number: 1, Key: strings.Repeat("a", 64)}
	dir, scs := earlierRepo(t, []domain.Scenario{earlierScenario("SHOP-482-01", "SHOP-482", "")}, "SHOP-482",
		obligation.Obligation{ID: "SHOP-482/scenario-SHOP-482-01", Title: "T", Source: obligation.SourceDiff, Covers: []domain.ScenarioID{"SHOP-482-01"}, Criterion: ref})
	if got := readEarlier(dir, map[string]bool{"SHOP-999": true}, scs); len(got) != 0 {
		t.Errorf("an entry of a ticket outside the change was read: %+v", got)
	}
	bad := "apiVersion: semar/v1\nkind: Obligations\nticket: SHOP-482\nobligations:\n  - id: SHOP-482/a\n    title: T\n    source: diff\n    criterion: {number: 1, key: nope}\n"
	if err := os.WriteFile(filepath.Join(dir, obligationsFile("SHOP-482")), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readEarlier(dir, map[string]bool{"SHOP-482": true}, scs); len(got) != 0 {
		t.Errorf("a file with a problem was believed: %+v", got)
	}
}

func TestEarlierNeverBelievesAScenarioOfAnotherTicket(t *testing.T) {
	ref := &obligation.CriterionRef{Number: 1, Key: strings.Repeat("a", 64)}
	dir, scs := earlierRepo(t, []domain.Scenario{earlierScenario("SHOP-483-01", "SHOP-483", "")}, "SHOP-482",
		obligation.Obligation{ID: "SHOP-482/scenario-SHOP-483-01", Title: "T", Source: obligation.SourceDiff, Covers: []domain.ScenarioID{"SHOP-483-01"}, Criterion: ref})
	got := readEarlier(dir, map[string]bool{"SHOP-482": true}, scs)["SHOP-482"]
	if len(got) != 1 || len(got[0].Tests) != 0 {
		t.Errorf("a scenario of another ticket counts as a test: %+v", got)
	}
}

// A web test that proves part of a criterion leaves a manual check for the rest. That check belongs to
// the test: when the test is removed with semar rm, the next plan must write the test again.
func TestTheManualCheckForTheRestOfAWebTestDoesNotKeepACriterionByItself(t *testing.T) {
	cite := earlierFile + ":7 Web: the order page shows the refund"
	dir, scs := earlierRepo(t, nil, "SHOP-482",
		obligation.Obligation{ID: "SHOP-482/manual-1", Title: plan.RestOfCriterion + "The order page shows the refund does not prove", Source: obligation.SourceCriterion, Cite: cite,
			Manual: &obligation.Manual{Kind: "judgement", Text: "t", Steps: []string{"s"}, Expected: "e", WrittenBy: "ai"}})
	got := readEarlier(dir, map[string]bool{"SHOP-482": true}, scs)["SHOP-482"]
	if len(got) != 1 || got[0].Manual || len(got[0].Cites) != 1 {
		t.Fatalf("the entry is %+v, want one entry with its cite that is no manual check of its own", got)
	}
}
