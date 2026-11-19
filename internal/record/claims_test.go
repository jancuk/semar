package record_test

import (
	"errors"
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

func no() *bool { v := false; return &v }

// A flow that decides a check of another form itself gives it as a claim, and the scenario holds the
// check in the words of its form.
func TestAClaimOfAnotherFormMakesATicketExpectationInTheWordsOfItsForm(t *testing.T) {
	page := onPage("/orders/o_4471", "Order o_4471")
	three := 3
	events := []record.Event{
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/orders/o_4471"}},
		{Kind: record.KindClick, Page: page, Target: el("button", "Remove")},
		{Kind: record.KindCheck, Page: page, Target: el("button", "Remove row"), Probe: &record.Probe{Held: true}},
		{Kind: record.KindCheck, Page: page, Target: css("#rows > li", "li-items"), Probe: &record.Probe{Held: true}},
		{Kind: record.KindCheck, Page: page, Target: el("button", "Save"), Probe: &record.Probe{Held: false, Shown: "shown, enabled, not checked"}},
		{Kind: record.KindCheck, Page: page, Target: el("textbox", "Search"), Probe: &record.Probe{Held: true}},
		{Kind: record.KindCheck, Page: page, Probe: &record.Probe{Held: true}},
	}
	o := options()
	o.Claims = []record.Claim{
		{Cite: criterionCite, Check: &domain.Check{Exists: no()}},
		{Cite: criterionCite, Check: &domain.Check{Count: &three}},
		{Cite: criterionCite, Check: &domain.Check{State: "disabled"}},
		{Cite: criterionCite, Check: &domain.Check{Attribute: "placeholder", Equals: "Search orders"}},
		{Cite: criterionCite, Check: &domain.Check{Page: "path", Equals: "/orders/o_4471"}},
	}
	d := build(t, events, o)
	text := feature(t, d)
	for _, want := range []string{
		"the element remove-row does not exist",
		"the element li-items has 3 items",
		"the element save is disabled",
		`the element search attribute placeholder equals "Search orders"`,
		`the page path equals "/orders/o_4471"`,
		"# ticket " + criterionCite + ", by model",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the scenario lacks %q:\n%s", want, text)
		}
	}
	if len(d.FailsNow) != 1 || d.FailsNow[0].Rule != "element save is disabled" || d.FailsNow[0].Shown != "shown, enabled, not checked" {
		t.Errorf("fails now = %+v", d.FailsNow)
	}
	if d.Checks != 5 {
		t.Errorf("checks = %d", d.Checks)
	}
	// The check of an element that may be gone waits for the page, and never for the element.
	for i, st := range d.Script.Steps {
		if st.Web.Do == domain.WebWait && st.Web.Element != "" {
			t.Errorf("step %d waits for the element %s", i, st.Web.Element)
		}
	}
}

func TestAClaimOfAnotherFormKeepsTheVariablesOfTheFixtures(t *testing.T) {
	o := options()
	o.Values = record.NewValues(map[string]string{"run.id": "r-test-0001"})
	events := []record.Event{
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/orders"}},
		{Kind: record.KindCheck, Page: onPage("/orders", "Orders r-test-0001"), Probe: &record.Probe{Held: true}},
	}
	o.Claims = []record.Claim{{Cite: criterionCite, Check: &domain.Check{Page: "title", Contains: "r-test-0001"}}}
	d := build(t, events, o)
	if text := feature(t, d); !strings.Contains(text, `the page title contains "{{run.id}}"`) {
		t.Errorf("scenario:\n%s", text)
	}
	o.Claims = []record.Claim{{Cite: criterionCite, Check: &domain.Check{Page: "title", Contains: "Orders r-20261118T004030Z-96e2"}}}
	if _, err := record.Build(events, o); err == nil || !errors.Is(err, record.ErrOtherRun) {
		t.Errorf("a claim that names a record of another run: %v", err)
	}
}
