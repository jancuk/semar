package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

func TestManifestHashSortsServices(t *testing.T) {
	a := domain.BuildRef{Service: "orders", Commit: "9f3c2ab", ConfigHash: "c1"}
	b := domain.BuildRef{Service: "payments", Commit: "c0ffee1", ConfigHash: "c2"}
	c := domain.BuildRef{Service: "billing", Commit: "abc1234", ConfigHash: "c3"}

	h := domain.ManifestHash([]domain.BuildRef{a, b, c})
	if len(h) != 64 {
		t.Fatalf("want a SHA-256 hex string, got %q", h)
	}
	for _, order := range [][]domain.BuildRef{{c, b, a}, {b, a, c}, {c, a, b}} {
		if got := domain.ManifestHash(order); got != h {
			t.Fatalf("order %v gave %s, want %s", order, got, h)
		}
	}
}

func TestManifestHashDoesNotReorderItsInput(t *testing.T) {
	in := []domain.BuildRef{{Service: "payments"}, {Service: "orders"}}
	domain.ManifestHash(in)
	if in[0].Service != "payments" {
		t.Fatalf("the input was sorted in place: %v", in)
	}
}

// The value must change only on purpose: a stored result is valid for its hash alone.
func TestManifestHashGolden(t *testing.T) {
	const want = "0a7da4d2ce8b7c0664aa8e4bdcc43f8e8569e1bd2188ff88077d63ccda237e6c"
	got := domain.ManifestHash([]domain.BuildRef{
		{Service: "payments", Commit: "c0ffee1", ConfigHash: "cfg-2"},
		{Service: "orders", Commit: "9f3c2ab", ConfigHash: "cfg-1"},
	})
	if string(got) != want {
		t.Fatalf("ManifestHash = %s, want %s", got, want)
	}
}

func TestManifestHashChangesWithCommitAndConfig(t *testing.T) {
	base := []domain.BuildRef{
		{Service: "orders", Commit: "9f3c2ab", ConfigHash: "c1"},
		{Service: "payments", Commit: "c0ffee1", ConfigHash: "c2"},
	}
	h := domain.ManifestHash(base)

	changes := map[string]func([]domain.BuildRef){
		"commit of the first":  func(b []domain.BuildRef) { b[0].Commit = "0000000" },
		"commit of the second": func(b []domain.BuildRef) { b[1].Commit = "0000000" },
		"config of the first":  func(b []domain.BuildRef) { b[0].ConfigHash = "other" },
		"config of the second": func(b []domain.BuildRef) { b[1].ConfigHash = "other" },
		"service name":         func(b []domain.BuildRef) { b[1].Service = "billing" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			in := append([]domain.BuildRef(nil), base...)
			change(in)
			if got := domain.ManifestHash(in); got == h {
				t.Fatalf("the hash did not change")
			}
		})
	}

	t.Run("a service added", func(t *testing.T) {
		in := append(append([]domain.BuildRef(nil), base...), domain.BuildRef{Service: "billing", Commit: "1", ConfigHash: "2"})
		if domain.ManifestHash(in) == h {
			t.Fatal("the hash did not change")
		}
	})

	// Declared says who named the commit, not what was built.
	t.Run("declared does not change it", func(t *testing.T) {
		in := append([]domain.BuildRef(nil), base...)
		in[0].Declared = true
		if domain.ManifestHash(in) != h {
			t.Fatal("the hash changed with Declared")
		}
	})
}

func TestManifestHashOfNoServices(t *testing.T) {
	if domain.ManifestHash(nil) != domain.ManifestHash([]domain.BuildRef{}) {
		t.Fatal("nil and empty must hash alike")
	}
	if domain.ManifestHash(nil) == "" {
		t.Fatal("the hash of no services must not be empty")
	}
}

func TestRunJSONOmitsEmptyJourneys(t *testing.T) {
	run := domain.Run{
		Schema:      1,
		ID:          "r-1",
		Environment: "local",
		StartedAt:   time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		EndedAt:     time.Date(2026, 11, 14, 10, 1, 0, 0, time.UTC),
		BuildsStart: []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab", ConfigHash: "c1"}},
		BuildsEnd:   []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab", ConfigHash: "c1"}},
		Results:     []domain.ScenarioResult{{Scenario: "SHOP-482-01", Outcome: domain.OutcomePassed, Reasons: []domain.Reason{}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{"a-1"}}},
		Files:       map[string]string{"run.json": "abc"},
	}
	got, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	// A run without journeys must encode as it did before the field existed.
	const want = `{"schema":1,"id":"r-1","environment":"local","startedAt":"2026-11-14T10:00:00Z","endedAt":"2026-11-14T10:01:00Z","buildsStart":[{"service":"orders","commit":"9f3c2ab","configHash":"c1"}],"buildsEnd":[{"service":"orders","commit":"9f3c2ab","configHash":"c1"}],"results":[{"scenario":"SHOP-482-01","outcome":"passed","reasons":[],"checks":[],"actions":["a-1"]}],"files":{"run.json":"abc"}}`
	if string(got) != want {
		t.Fatalf("a run without journeys changed its encoding:\n got %s\nwant %s", got, want)
	}
	if strings.Contains(string(got), "journeys") {
		t.Fatalf("journeys must not be encoded when empty: %s", got)
	}

	run.Purpose = domain.PurposeJourney
	run.Journeys = []domain.JourneyResult{{Journey: "checkout", Outcome: domain.JourneyPassed}}
	got, err = json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"purpose":"journey"`, `"journeys":[{"journey":"checkout"`, `"outcome":"passed"`} {
		if !strings.Contains(string(got), s) {
			t.Errorf("missing %s in %s", s, got)
		}
	}
}

func TestJourneyResultJSON(t *testing.T) {
	res := domain.JourneyResult{
		Journey:      "checkout",
		Manifest:     domain.Manifest{Services: []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab", ConfigHash: "c1"}}, Hash: "h1"},
		Outcome:      domain.JourneyFailed,
		FirstFailing: "settled",
		Legs:         []domain.LegResult{{Leg: "settled", Scenario: "checkout.settled", Outcome: domain.OutcomeFailed, Reasons: []domain.Reason{}}},
		Invariants:   []domain.InvariantResult{{Index: 0, State: domain.CheckFalse, Values: []string{"1000", "999"}, Detail: "values differ"}},
		Reasons:      []domain.Reason{{Rule: domain.OutcomeFailed, Text: "leg settled failed"}},
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, k := range []string{`"journey"`, `"manifest"`, `"services"`, `"hash"`, `"outcome"`, `"firstFailing"`, `"legs"`, `"leg"`, `"scenario"`, `"invariants"`, `"index"`, `"state"`, `"values"`, `"detail"`, `"reasons"`} {
		if !strings.Contains(s, k) {
			t.Errorf("missing key %s in %s", k, s)
		}
	}
	for _, k := range []string{"manifestEnd", "fixtures"} {
		if strings.Contains(s, k) {
			t.Errorf("%s must be omitted when empty: %s", k, s)
		}
	}
}

func TestJourneyOutcomesAreFive(t *testing.T) {
	want := map[domain.JourneyOutcome]string{
		domain.JourneyPassed:  "passed",
		domain.JourneyFailed:  "failed",
		domain.JourneyBlocked: "blocked",
		domain.JourneyStale:   "stale",
		domain.JourneyNotRun:  "not_run",
	}
	for o, s := range want {
		if string(o) != s {
			t.Errorf("%q != %q", o, s)
		}
	}
	if domain.PurposeJourney != "journey" {
		t.Fatal("purpose constant")
	}
}
