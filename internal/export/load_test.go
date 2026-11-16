package export_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/export"
)

func TestLoadRefusesInterrupted(t *testing.T) {
	repo := t.TempDir()
	store, err := evidence.Open(filepath.Join(repo, ".semar"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewRun("r-20261117T090000Z-1a2b"); err != nil {
		t.Fatal(err)
	}
	_, err = export.Load(store, repo, "r-20261117T090000Z-1a2b", "0.9.0")
	want := "run r-20261117T090000Z-1a2b is interrupted: it has no run.json, so it has no outcome to export"
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

func TestLoadRefusesBadRunID(t *testing.T) {
	repo := t.TempDir()
	store, err := evidence.Open(filepath.Join(repo, ".semar"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.RunID{"../x", "", "a/b", ".hidden"} {
		_, err := export.Load(store, repo, id, "0.9.0")
		if err == nil || !strings.Contains(err.Error(), "invalid run ID") {
			t.Errorf("Load(%q) error = %v, want the idcheck message", id, err)
		}
	}
}

func TestLoadRefusesUnknownRun(t *testing.T) {
	repo := t.TempDir()
	store, err := evidence.Open(filepath.Join(repo, ".semar"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := export.Load(store, repo, "r-none", "0.9.0"); err == nil || !strings.Contains(err.Error(), "r-none was not found") {
		t.Errorf("error = %v, want a not found error", err)
	}
}

func TestLoadRefusesChangedEvidence(t *testing.T) {
	repo := t.TempDir()
	store := writeRun(t, repo, runSpec{
		id:      "r-1",
		results: []domain.ScenarioResult{result("SHOP-482-01", domain.OutcomePassed)},
		actions: []actionSpec{{scenario: "SHOP-482-01", step: "refund", ms: 5}},
	})
	p := filepath.Join(repo, ".semar", "runs", "r-1", "exchanges", "SHOP-482-01", "0001-refund.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-2] ^= 1 // one byte
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = export.Load(store, repo, "r-1", "0.9.0")
	want := "the evidence of run r-1 changed after it was written: runs/r-1/exchanges/SHOP-482-01/0001-refund.json; nothing was exported"
	if err == nil || err.Error() != want {
		t.Errorf("error = %v\nwant %q", err, want)
	}
}

func TestLoadRefusesAnExtraFile(t *testing.T) {
	repo := t.TempDir()
	store := writeRun(t, repo, runSpec{id: "r-1", results: []domain.ScenarioResult{result("A-1", domain.OutcomePassed)}})
	writeFile(t, filepath.Join(repo, ".semar", "runs", "r-1", "extra.txt"), "x")
	_, err := export.Load(store, repo, "r-1", "0.9.0")
	if err == nil || !strings.Contains(err.Error(), "runs/r-1/extra.txt (not listed in run.json)") {
		t.Errorf("error = %v, want the extra file named with its reason", err)
	}
}

func TestLoadRefusesNoResults(t *testing.T) {
	repo := t.TempDir()
	store := writeRun(t, repo, runSpec{id: "r-1"})
	_, err := export.Load(store, repo, "r-1", "0.9.0")
	want := "run r-1 has no scenario result: a trial run or a fixture check has nothing to export"
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

func TestLoadDurationSumsActions(t *testing.T) {
	repo := t.TempDir()
	store := writeRun(t, repo, runSpec{
		id: "r-1",
		results: []domain.ScenarioResult{
			result("A-1", domain.OutcomePassed),
			result("A-2", domain.OutcomePassed),
			result("A-3", domain.OutcomeNotRun),
		},
		actions: []actionSpec{
			{scenario: "A-1", step: "setup", pass: domain.PassSetup, ms: 100},
			{scenario: "A-1", step: "act", ms: 300},
			{scenario: "A-2", step: "act", ms: 7},
			{scenario: "A-1", step: "clean", pass: domain.PassCleanup, ms: 12},
			{ms: 9000}, // no scenario: counts for none
		},
	})
	r, err := export.Load(store, repo, "r-1", "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	got := []int64{r.Scenarios[0].DurationMS, r.Scenarios[1].DurationMS, r.Scenarios[2].DurationMS}
	want := []int64{412, 7, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("duration of scenario %d = %d, want %d", i, got[i], want[i])
		}
	}
	if r.Scenarios[0].Unknown {
		t.Error("Unknown is set though every action was read")
	}
	if r.Version != "0.9.0" || r.Run.ID != "r-1" {
		t.Errorf("Run = %q %q", r.Version, r.Run.ID)
	}
}

func TestLoadLargeActionCountsAsUnknown(t *testing.T) {
	repo := t.TempDir()
	store := writeRun(t, repo, runSpec{
		id:      "r-1",
		results: []domain.ScenarioResult{result("A-1", domain.OutcomePassed)},
		actions: []actionSpec{{scenario: "A-1", step: "one", ms: 50}, {scenario: "A-1", step: "big", ms: 70}},
	})
	// grow one action file past the limit, then close the run again so that the hashes match
	big := filepath.Join(repo, ".semar", "runs", "r-1", "exchanges", "A-1", "0002-big.json")
	data, err := os.ReadFile(big)
	if err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat(" ", evidence.MaxActionBytes)
	if err := os.WriteFile(big, append(data, pad...), 0o600); err != nil {
		t.Fatal(err)
	}
	rehash(t, store, "r-1")
	r, err := export.Load(store, repo, "r-1", "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Scenarios[0].Unknown || r.Scenarios[0].DurationMS != 50 {
		t.Errorf("Unknown = %v, DurationMS = %d, want true and 50", r.Scenarios[0].Unknown, r.Scenarios[0].DurationMS)
	}
}

func TestLoadRefusesAnActionPathThatLeavesTheRun(t *testing.T) {
	repo := t.TempDir()
	store := writeRun(t, repo, runSpec{id: "r-1", results: []domain.ScenarioResult{result("A-1", domain.OutcomePassed)}})
	line := `{"seq":1,"at":"2026-11-17T09:00:00Z","actor":"replay","use":{"pass":"scenario","scenario":"A-1","step":"x"},"surface":"api","verb":"read","target":"GET /x","result":"200","traceId":"","evidence":"../../../outside.json"}` + "\n"
	writeFile(t, filepath.Join(repo, ".semar", "runs", "r-1", "activity.jsonl"), line)
	rehash(t, store, "r-1")
	_, err := export.Load(store, repo, "r-1", "0.9.0")
	if err == nil || !strings.Contains(err.Error(), "not local") {
		t.Errorf("error = %v, want a refusal of the path", err)
	}
}

func TestLoadTitleFromScenarioFile(t *testing.T) {
	repo := t.TempDir()
	p := writeScenario(t, repo, "SHOP-482", "SHOP-482-01", scenarioYAML)
	store := writeRun(t, repo, runSpec{id: "r-1", results: []domain.ScenarioResult{result("SHOP-482-01", domain.OutcomeFailed, "step refund-too-much: expected status 422")}})
	r, err := export.Load(store, repo, "r-1", "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	s := r.Scenarios[0]
	if s.Title != "Refund above the order total is rejected" || s.Ticket != "SHOP-482" {
		t.Errorf("title %q, ticket %q", s.Title, s.Ticket)
	}
	if s.File != ".semar/scenarios/SHOP-482/SHOP-482-01.yaml" {
		t.Errorf("File = %q", s.File)
	}
	if s.Result.Outcome != domain.OutcomeFailed {
		t.Errorf("Result = %+v", s.Result)
	}
	_ = p
}

func TestLoadScenarioGone(t *testing.T) {
	repo := t.TempDir()
	store := writeRun(t, repo, runSpec{id: "r-1", results: []domain.ScenarioResult{result("SHOP-482-01", domain.OutcomePassed)}})
	r, err := export.Load(store, repo, "r-1", "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	s := r.Scenarios[0]
	if s.Title != "" || s.File != "" || s.Ticket != "" || len(s.StepLines) != 0 {
		t.Errorf("a scenario with no file has %+v", s)
	}
}

func TestLoadScenarioFileIsNotTrusted(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, repo string)
	}{
		{"not YAML", func(t *testing.T, repo string) {
			writeScenario(t, repo, "x", "A-1", "title: [unclosed\n  - : :\n")
		}},
		{"title is not text", func(t *testing.T, repo string) {
			writeScenario(t, repo, "x", "A-1", "title: {a: b}\nsteps: 4\n")
		}},
		{"a file above the limit", func(t *testing.T, repo string) {
			writeScenario(t, repo, "x", "A-1", "title: big\n# "+strings.Repeat("x", 2<<20)+"\n")
		}},
		{"two files for one ID", func(t *testing.T, repo string) {
			writeScenario(t, repo, "x", "A-1", "title: one\n")
			writeScenario(t, repo, "y", "A-1", "title: two\n")
		}},
		{"a link", func(t *testing.T, repo string) {
			other := filepath.Join(t.TempDir(), "A-1.yaml")
			writeFile(t, other, "title: outside\n")
			if err := os.MkdirAll(filepath.Join(repo, ".semar", "scenarios"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(repo, ".semar", "scenarios", "A-1.yaml")); err != nil {
				t.Skip(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			tt.setup(t, repo)
			store := writeRun(t, repo, runSpec{id: "r-1", results: []domain.ScenarioResult{result("A-1", domain.OutcomePassed)}})
			r, err := export.Load(store, repo, "r-1", "0.9.0")
			if err != nil {
				t.Fatalf("Load = %v, want the scenario treated as gone", err)
			}
			if s := r.Scenarios[0]; s.Title != "" || s.File != "" {
				t.Errorf("Scenario = %+v, want no title and no file", s)
			}
		})
	}
}

func TestLoadScenarioWithAnInvalidIDIsNotLookedUp(t *testing.T) {
	repo := t.TempDir()
	store := writeRun(t, repo, runSpec{id: "r-1", results: []domain.ScenarioResult{result("../A-1", domain.OutcomePassed)}})
	r, err := export.Load(store, repo, "r-1", "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if r.Scenarios[0].File != "" {
		t.Errorf("File = %q", r.Scenarios[0].File)
	}
}

func TestLoadStepLines(t *testing.T) {
	repo := t.TempDir()
	writeScenario(t, repo, "SHOP-482", "SHOP-482-01", scenarioYAML)
	store := writeRun(t, repo, runSpec{id: "r-1", results: []domain.ScenarioResult{result("SHOP-482-01", domain.OutcomePassed)}})
	r, err := export.Load(store, repo, "r-1", "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	want := map[domain.StepID]int{"create-order": 8, "refund-too-much": 11}
	got := r.Scenarios[0].StepLines
	if len(got) != len(want) {
		t.Fatalf("StepLines = %v, want %v", got, want)
	}
	for id, line := range want {
		if got[id] != line {
			t.Errorf("line of %s = %d, want %d", id, got[id], line)
		}
	}
}

func TestLoadKeepsTheOrderOfResults(t *testing.T) {
	repo := t.TempDir()
	store := writeRun(t, repo, runSpec{id: "r-1", results: []domain.ScenarioResult{
		result("B-1", domain.OutcomePassed), result("A-1", domain.OutcomeFailed), result("C-1", domain.OutcomeBlocked),
	}})
	r, err := export.Load(store, repo, "r-1", "v")
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []domain.ScenarioID{"B-1", "A-1", "C-1"} {
		if r.Scenarios[i].Result.Scenario != id {
			t.Errorf("scenario %d = %s, want %s", i, r.Scenarios[i].Result.Scenario, id)
		}
	}
}

// checkout.pay is the scenario of a journey leg: a scenario file of that name does not label it.
func TestLoadLegScenarioIsNotLabelledByAScenarioFile(t *testing.T) {
	repo := t.TempDir()
	writeScenario(t, repo, "SHOP-1", "checkout.pay", "apiVersion: semar/v1\nkind: Scenario\nid: checkout.pay\ntitle: A real scenario\nticket: SHOP-1\nsteps: []\n")
	store := writeRun(t, repo, runSpec{id: "r-1", results: []domain.ScenarioResult{result("checkout.pay", domain.OutcomePassed)}})
	r, err := export.Load(store, repo, "r-1", "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Scenarios[0]; got.File != "" || got.Title != "" || got.Ticket != "" {
		t.Errorf("the leg took the labels of a file: %+v", got)
	}
}
