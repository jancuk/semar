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
