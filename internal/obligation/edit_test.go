package obligation_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/obligation"
)

const twoUseCases = `apiVersion: semar/v1
kind: Obligations
ticket: SHOP-482
obligations:
  - id: SHOP-482/manual-1
    title: Check the refund receipt on a phone
    source: criterion
    cite: docs/tickets/SHOP-482.md:14
    manual:
      kind: physical_device
      text: A person must look at a phone
      steps:
        - Open the receipt
      expected: The refund line is shown
      writtenBy: ai
  - id: SHOP-482/2
    title: A refund above the paid amount is refused
    source: criterion
    cite: docs/tickets/SHOP-482.md:15
    covers: [SHOP-482-02]
`

func oneFileRepo(t *testing.T, text string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".semar", "obligations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".semar", "obligations", "SHOP-482.plan.yaml"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func loaded(t *testing.T, dir, id string) obligation.Located {
	t.Helper()
	list, problems, err := obligation.Load(dir)
	if err != nil || len(problems) > 0 {
		t.Fatalf("load: %v %v", err, problems)
	}
	for _, l := range list {
		if l.ID == id {
			return l
		}
	}
	t.Fatalf("%s is not in the files", id)
	return obligation.Located{}
}

func TestEditChangesOneUseCaseAndKeepsTheRest(t *testing.T) {
	dir := oneFileRepo(t, twoUseCases)
	err := obligation.Edit(dir, "SHOP-482/manual-1", func(o *obligation.Obligation) error {
		o.Covers = append(o.Covers, "SHOP-482-01")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := loaded(t, dir, "SHOP-482/manual-1")
	if len(got.Covers) != 1 || got.Covers[0] != "SHOP-482-01" {
		t.Errorf("covers %v, want SHOP-482-01", got.Covers)
	}
	if got.Manual == nil || got.Manual.Text != "A person must look at a phone" || got.Manual.WrittenBy != "ai" || got.Cite != "docs/tickets/SHOP-482.md:14" {
		t.Errorf("the entry lost what the edit did not touch: %+v", got)
	}
	if other := loaded(t, dir, "SHOP-482/2"); len(other.Covers) != 1 || other.Covers[0] != "SHOP-482-02" {
		t.Errorf("the other use case changed: %+v", other)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".semar", "obligations", "SHOP-482.plan.yaml"))
	if err != nil || !strings.Contains(string(data), "ticket: SHOP-482") {
		t.Errorf("the file lost its ticket: %v\n%s", err, data)
	}
}

func TestEditRefusesWhatItCannotKeepValid(t *testing.T) {
	dir := oneFileRepo(t, twoUseCases)
	before, _ := os.ReadFile(filepath.Join(dir, ".semar", "obligations", "SHOP-482.plan.yaml"))
	if err := obligation.Edit(dir, "SHOP-482/none", func(*obligation.Obligation) error { return nil }); err == nil {
		t.Error("an edit of an unknown use case was accepted")
	}
	if err := obligation.Edit(dir, "SHOP-482/2", func(o *obligation.Obligation) error { o.Covers = []domain.ScenarioID{"../x"}; return nil }); err == nil {
		t.Error("an edit that made the file invalid was accepted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, ".semar", "obligations", "SHOP-482.plan.yaml"))
	if string(before) != string(after) {
		t.Errorf("a refused edit changed the file:\n%s", after)
	}
}

func TestRetiredScenariosAreKeptInTheFile(t *testing.T) {
	dir := oneFileRepo(t, twoUseCases)
	err := obligation.Edit(dir, "SHOP-482/2", func(o *obligation.Obligation) error {
		o.Covers = nil
		o.Retired = append(o.Retired, obligation.Retired{Scenario: "SHOP-482-02", Reason: "the refund rule moved to SHOP-490", User: "tester", Day: "2026-11-17"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := loaded(t, dir, "SHOP-482/2")
	if len(got.Covers) != 0 || len(got.Retired) != 1 || got.Retired[0].Scenario != "SHOP-482-02" || got.Retired[0].User != "tester" || got.Retired[0].Day != "2026-11-17" {
		t.Errorf("entry %+v", got)
	}
}

func TestARetiredEntryNeedsEveryField(t *testing.T) {
	dir := oneFileRepo(t, twoUseCases)
	for name, r := range map[string]obligation.Retired{
		"no reason":      {Scenario: "SHOP-482-02", User: "tester", Day: "2026-11-17"},
		"a long reason":  {Scenario: "SHOP-482-02", Reason: strings.Repeat("r", 301), User: "tester", Day: "2026-11-17"},
		"two lines":      {Scenario: "SHOP-482-02", Reason: "a\nb", User: "tester", Day: "2026-11-17"},
		"no user":        {Scenario: "SHOP-482-02", Reason: "r", Day: "2026-11-17"},
		"a bad day":      {Scenario: "SHOP-482-02", Reason: "r", User: "tester", Day: "yesterday"},
		"a bad scenario": {Scenario: "../x", Reason: "r", User: "tester", Day: "2026-11-17"},
	} {
		err := obligation.Edit(dir, "SHOP-482/2", func(o *obligation.Obligation) error { o.Retired = []obligation.Retired{r}; return nil })
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAUseCaseThatLostItsLastTestIsNoTestAndSaysWho(t *testing.T) {
	o := ob("g/a", func(o *obligation.Obligation) {
		o.Retired = []obligation.Retired{{Scenario: "A-1", Reason: "the rule moved", User: "tester", Day: "2026-11-17"}}
	})
	e := one(t, o, results(), nil, nil)
	if e.State != obligation.Blocked || e.NoReason {
		t.Fatalf("state %s, no reason %v: a retired test is a reason", e.State, e.NoReason)
	}
	mustReason(t, e, "A-1 was retired by tester on 2026-11-17: the rule moved")
	if len(e.Covers) != 0 {
		t.Errorf("covers %v, want none", e.Covers)
	}
}
