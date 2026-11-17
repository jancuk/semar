package record_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/screens"
)

const criterionCite = "docs/tickets/SHOP-482.md:9 a refund shows its total"

// A flow gives its checks a claim each: what the criterion says the element shows, and the cite of the
// criterion. The element still shows what the page shows, which the recording read.
func TestAClaimMakesTheCheckATicketExpectationWithItsCite(t *testing.T) {
	o := options()
	o.Claims = []record.Claim{
		{Expect: "5.00", Cite: criterionCite},
		{Expect: "refunded", Cite: criterionCite},
	}
	d := build(t, refundFlow(), o)
	text := feature(t, d)
	for _, want := range []string{
		`Then the element refund-total equals "5.00"`,
		`And the element status equals "refunded"`,
		"# ticket " + criterionCite + ", by model",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the scenario lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "# person") {
		t.Errorf("a check of a flow has the source person:\n%s", text)
	}
	for _, st := range d.Scenario.Steps {
		for _, e := range st.Expect {
			if e.Source != domain.SourceTicket || e.Cite != criterionCite || e.By != "model" {
				t.Errorf("expectation = %+v", e)
			}
		}
	}
}

func TestACheckThatThePageDoesNotMeetIsListedAsFailingNow(t *testing.T) {
	o := options()
	o.Claims = []record.Claim{{Expect: "5.00", Cite: criterionCite}, {Expect: "refunded", Cite: criterionCite}}
	d := build(t, refundFlow(), o)
	if len(d.FailsNow) != 1 {
		t.Fatalf("fails now = %+v", d.FailsNow)
	}
	f := d.FailsNow[0]
	if f.Element != "status" || f.Expect != "refunded" || f.Shown != "partly refunded" || f.Cite != criterionCite {
		t.Errorf("fails now = %+v", f)
	}
}

func TestSpacesDoNotMakeAClaimDiffer(t *testing.T) {
	o := options()
	o.Claims = []record.Claim{{Expect: " 5.00 ", Cite: criterionCite}, {Expect: "partly   refunded", Cite: criterionCite}}
	d := build(t, refundFlow(), o)
	if len(d.FailsNow) != 0 {
		t.Errorf("fails now = %+v", d.FailsNow)
	}
}

func TestAClaimWithFixtureValuesKeepsTheVariables(t *testing.T) {
	o := options()
	o.Values = record.NewValues(map[string]string{"run.id": "r-test-0001"})
	events := refundFlow()
	events[4].Value = "Board r-test-0001"
	o.Claims = []record.Claim{{Expect: "Board r-test-0001", Cite: criterionCite}, {Expect: "partly refunded", Cite: criterionCite}}
	d := build(t, events, o)
	if text := feature(t, d); !strings.Contains(text, `equals "Board {{run.id}}"`) || len(d.FailsNow) != 0 {
		t.Errorf("fails now %+v\n%s", d.FailsNow, text)
	}
}

func TestMoreChecksThanClaimsIsAnError(t *testing.T) {
	o := options()
	o.Claims = []record.Claim{{Expect: "5.00", Cite: criterionCite}}
	if _, err := record.Build(refundFlow(), o); err == nil || !strings.Contains(err.Error(), "2 checks") {
		t.Errorf("err = %v", err)
	}
}

func TestWriteGivesTheWayBack(t *testing.T) {
	repo := t.TempDir()
	if _, err := record.Write(repo, build(t, refundFlow(), options()), writeOptions()); err != nil {
		t.Fatal(err)
	}
	screensFile := filepath.Join(repo, ".semar", "screens", "shop.yaml")
	before, _ := os.ReadFile(screensFile)

	o := options()
	o.ID = "SHOP-482-08"
	o.Existing, _ = screens.Load(repo, "shop")
	page := onPage("/cards/c1", "Card c1")
	events := []record.Event{
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/cards/c1"}},
		{Kind: record.KindCheck, Page: page, Target: css("#due", "due"), Value: "2026-12-01"},
	}
	res, err := record.Write(repo, build(t, events, o), writeOptions())
	if err != nil {
		t.Fatal(err)
	}
	if during, _ := os.ReadFile(screensFile); string(during) == string(before) {
		t.Fatal("the second recording did not add its screen")
	}
	if err := res.Undo(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(screensFile); string(after) != string(before) {
		t.Errorf("the screens file after the undo:\n%s\nwant:\n%s", after, before)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(res.ScenarioPath))); err == nil {
		t.Error("the scenario file is still there")
	}
	if _, err := os.Stat(filepath.Join(repo, ".semar", "scenarios", "SHOP-482", "SHOP-482-07.feature")); err != nil {
		t.Errorf("the undo removed the first scenario: %v", err)
	}
}

func TestUndoRemovesTheFilesAndFoldersThatTheWriteMade(t *testing.T) {
	repo := t.TempDir()
	res, err := record.Write(repo, build(t, refundFlow(), options()), writeOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Undo(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(repo)
	if len(entries) != 0 {
		t.Errorf("the repository holds %v after the undo", entries)
	}
}
