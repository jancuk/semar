package record_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/screens"
)

func writeOptions() record.WriteOptions {
	return record.WriteOptions{App: "shop", Subject: "Refunds"}
}

func TestWriteMakesTheScreensAndTheScenario(t *testing.T) {
	repo := t.TempDir()
	d := build(t, refundFlow(), options())
	res, err := record.Write(repo, d, writeOptions())
	if err != nil {
		t.Fatal(err)
	}
	if res.ScreensPath != ".semar/screens/shop.yaml" || res.ScenarioPath != ".semar/scenarios/SHOP-482/SHOP-482-07.feature" {
		t.Errorf("paths = %+v", res)
	}
	if res.Screens != 1 || res.Elements != 5 {
		t.Errorf("the file holds %d screens and %d elements", res.Screens, res.Elements)
	}
	s, err := screens.Load(repo, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := s.Locator("Order-o-4471", "amount"); !ok || l.Name != "Amount" {
		t.Errorf("the screen is named after the title of the page: %+v", s.Screens)
	}
	sc, err := scenario.Load(filepath.Join(repo, filepath.FromSlash(res.ScenarioPath)))
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != d.Scenario.ID || len(sc.Steps) != len(d.Scenario.Steps) {
		t.Errorf("scenario = %+v", sc)
	}
}

func TestASecondRecordingAddsToTheScreensOfTheFirstAndLeavesItsElementsAlone(t *testing.T) {
	repo := t.TempDir()
	if _, err := record.Write(repo, build(t, refundFlow(), options()), writeOptions()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(repo, ".semar", "screens", "shop.yaml"))

	o := options()
	o.ID = "SHOP-482-08"
	o.Existing, _ = screens.Load(repo, "shop")
	page := onPage("/orders/o_4471", "Order o_4471")
	events := refundFlow()[:3]
	events = append(events, record.Event{Kind: record.KindClick, Page: page, Target: css("#cancel", "Cancel")},
		record.Event{Kind: record.KindCheck, Page: page, Target: css("#status", "status"), Value: "cancelled"})
	res, err := record.Write(repo, build(t, events, o), writeOptions())
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(repo, ".semar", "screens", "shop.yaml"))
	if !strings.HasPrefix(string(after), strings.TrimSuffix(string(before), "\n")) {
		t.Errorf("the first recording's lines changed:\n%s\nafter\n%s", before, after)
	}
	if !strings.Contains(string(after), "cancel: { css: \"#cancel\" }") {
		t.Errorf("the new element is missing:\n%s", after)
	}
	if res.Elements != 6 {
		t.Errorf("elements = %d", res.Elements)
	}
}

func TestWriteRefusesAScenarioThatExistsUnlessTheCallerReplacesIt(t *testing.T) {
	repo := t.TempDir()
	d := build(t, refundFlow(), options())
	res, err := record.Write(repo, d, writeOptions())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, filepath.FromSlash(res.ScenarioPath))
	if err := os.WriteFile(path, []byte("changed by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The second recording reads the screens file that the first one made.
	o2 := options()
	o2.Existing, _ = screens.Load(repo, "shop")
	d = build(t, refundFlow(), o2)
	if _, err := record.Write(repo, d, writeOptions()); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("error = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "changed by hand\n" {
		t.Errorf("the scenario was written over: %s", data)
	}
	o := writeOptions()
	o.Replace = []string{res.ScenarioPath}
	if _, err := record.Write(repo, d, o); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "Scenario: Recorded on") {
		t.Errorf("the scenario was not replaced: %s", data)
	}
}

func TestWriteRemovesTheOldFileOfAReplacedScenarioInAnotherFolder(t *testing.T) {
	repo := t.TempDir()
	old := filepath.Join(repo, ".semar", "scenarios", "OLD")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(old, "SHOP-482-07.feature")
	if err := os.WriteFile(oldFile, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := writeOptions()
	o.Replace = []string{".semar/scenarios/OLD/SHOP-482-07.feature"}
	if _, err := record.Write(repo, build(t, refundFlow(), options()), o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldFile); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the old file is still there: %v", err)
	}
}

func TestWriteWritesNothingWhenTheScenarioCannotBeWritten(t *testing.T) {
	repo := t.TempDir()
	d := build(t, refundFlow(), options())
	d.Scenario.Steps[0].Action = "two\nlines"
	if _, err := record.Write(repo, d, writeOptions()); err == nil {
		t.Fatal("a scenario that Gherkin cannot carry was written")
	}
	if _, err := os.Stat(filepath.Join(repo, ".semar")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a file or a folder was made: %v", err)
	}
}

func TestWriteRefusesALinkWhereAFolderShouldBe(t *testing.T) {
	repo := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".semar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(repo, ".semar", "screens")); err != nil {
		t.Skip("no symbolic links here")
	}
	if _, err := record.Write(repo, build(t, refundFlow(), options()), writeOptions()); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("error = %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) > 0 {
		t.Errorf("a file went through the link: %v", entries)
	}
}
