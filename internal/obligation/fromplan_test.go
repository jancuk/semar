package obligation_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/stage/plan"
)

func changeSet() domain.ChangeSet {
	return domain.ChangeSet{Tickets: []domain.TicketChange{
		{Key: "SHOP-481", Files: []string{"internal/refund/a.go"}},
		{Key: "SHOP-482", Files: []string{"internal/refund/a.go", "internal/refund/notify.go"}},
	}}
}

func scenarios(ids ...string) []domain.Scenario {
	var out []domain.Scenario
	for _, id := range ids {
		out = append(out, domain.Scenario{ID: domain.ScenarioID(id), Ticket: "SHOP-482", Title: "Title of " + id})
	}
	return out
}

func fullDraft() plan.Draft {
	return plan.Draft{
		NotDrafted: []plan.NotDrafted{{Ticket: "SHOP-482", What: "A refund of a paid order by another customer", Needs: "fixture order.paid"}},
		Uncovered:  []plan.Uncovered{{File: "internal/refund/notify.go", Reason: "it only sends an email"}},
		Differences: []plan.Difference{{
			Ticket: "SHOP-482", Document: "docs/refunds.md:14: the refusal is 409.", Code: "internal/refund/handler.go:41: the refusal is 422.",
			What: "the status of a refund above the total",
		}},
	}
}

func ids(f obligation.File) []string {
	var out []string
	for _, o := range f.Obligations {
		out = append(out, o.ID)
	}
	return out
}

func TestFromPlanOnePerItem(t *testing.T) {
	got, err := obligation.FromPlan(changeSet(), fullDraft(), scenarios("SHOP-482-01", "SHOP-482-02"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got files for %v", got)
	}
	f := got["SHOP-482"]
	want := []string{"SHOP-482/scenario-SHOP-482-01", "SHOP-482/scenario-SHOP-482-02", "SHOP-482/not-drafted-1", "SHOP-482/uncovered-1", "SHOP-482/difference-1"}
	if !reflect.DeepEqual(ids(f), want) {
		t.Fatalf("ids %v, want %v", ids(f), want)
	}
	if f.APIVersion != "semar/v1" || f.Kind != "Obligations" || f.Ticket != "SHOP-482" {
		t.Errorf("header %q %q %q", f.APIVersion, f.Kind, f.Ticket)
	}
	sc, nd, un, df := f.Obligations[0], f.Obligations[2], f.Obligations[3], f.Obligations[4]
	if sc.Title != "Title of SHOP-482-01" || sc.Source != obligation.SourceDiff || !reflect.DeepEqual(sc.Covers, []domain.ScenarioID{"SHOP-482-01"}) {
		t.Errorf("scenario obligation %+v", sc)
	}
	if nd.Title != "A refund of a paid order by another customer" || nd.Gap != "not drafted: needs fixture order.paid" || nd.Source != obligation.SourceDiff {
		t.Errorf("not drafted %+v", nd)
	}
	if un.Title != "Changes in internal/refund/notify.go" || un.Gap != "no scenario covers internal/refund/notify.go: it only sends an email" {
		t.Errorf("uncovered %+v", un)
	}
	if df.Source != obligation.SourceKnowledge || df.Cite != "docs/refunds.md:14" || df.Title != "The document and the code differ: the status of a refund above the total" {
		t.Errorf("difference %+v", df)
	}
	m := df.Manual
	if m == nil || m.Kind != "judgement" || m.WrittenBy != "code" || m.Text != "A person decides whether the document or the code is right" || m.Expected != "The document and the code agree" {
		t.Fatalf("difference manual %+v", m)
	}
	wantSteps := []string{
		"Read docs/refunds.md:14: the refusal is 409.",
		"Read internal/refund/handler.go:41: the refusal is 422.",
		"Decide which one is right, and change the other, or the scenario.",
	}
	if !reflect.DeepEqual(m.Steps, wantSteps) {
		t.Errorf("steps %q", m.Steps)
	}
	if ps := obligation.Validate(f, "SHOP-482.plan.yaml"); len(ps) > 0 {
		t.Errorf("the file has problems: %v", ps)
	}
	if _, err := obligation.Marshal(f); err != nil {
		t.Errorf("Marshal: %v", err)
	}
}

func TestFromPlanKeepsExisting(t *testing.T) {
	first, err := obligation.FromPlan(changeSet(), fullDraft(), scenarios("SHOP-482-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f := first["SHOP-482"]
	f.Obligations[0].Title = "Edited by a person"
	f.Obligations[1].Gap = "edited gap"
	f.Obligations = f.Obligations[:2] // a person removed the other entries

	draft := plan.Draft{
		NotDrafted: []plan.NotDrafted{{Ticket: "SHOP-482", What: "Another case", Needs: "x"}},
		Uncovered:  []plan.Uncovered{{File: "internal/refund/a.go", Reason: "r"}},
	}
	got, err := obligation.FromPlan(changeSet(), draft, scenarios("SHOP-482-01", "SHOP-482-03"), map[string]obligation.File{"SHOP-482": f})
	if err != nil {
		t.Fatal(err)
	}
	g := got["SHOP-482"]
	if g.Obligations[0].Title != "Edited by a person" {
		t.Errorf("an edited title was changed: %q", g.Obligations[0].Title)
	}
	if g.Obligations[1].ID != "SHOP-482/not-drafted-1" || g.Obligations[1].Gap != "edited gap" {
		t.Errorf("an edited gap was changed: %+v", g.Obligations[1])
	}
	want := []string{"SHOP-482/scenario-SHOP-482-01", "SHOP-482/not-drafted-1", "SHOP-482/scenario-SHOP-482-03", "SHOP-482/not-drafted-2"}
	if !reflect.DeepEqual(ids(g), want) {
		t.Fatalf("ids %v, want %v", ids(g), want)
	}
	// The uncovered file belongs to the first ticket that holds it, SHOP-481.
	if g2 := got["SHOP-481"]; len(g2.Obligations) != 1 || g2.Obligations[0].ID != "SHOP-481/uncovered-1" {
		t.Errorf("SHOP-481: %+v", g2)
	}
	if len(f.Obligations) != 2 {
		t.Error("FromPlan changed the file that it was given")
	}
}

func TestFromPlanContinuesAfterTheHighestNumber(t *testing.T) {
	existing := obligation.File{Obligations: []obligation.Obligation{
		{ID: "SHOP-482/not-drafted-7", Title: "Old", Source: obligation.SourceDiff, Gap: "g"},
		{ID: "SHOP-482/not-drafted-2", Title: "Older", Source: obligation.SourceDiff, Gap: "g"},
		{ID: "SHOP-482/difference-3", Title: "D", Source: obligation.SourceKnowledge},
	}}
	got, err := obligation.FromPlan(changeSet(), plan.Draft{
		NotDrafted: []plan.NotDrafted{{Ticket: "SHOP-482", What: "New", Needs: "n"}},
	}, nil, map[string]obligation.File{"SHOP-482": existing})
	if err != nil {
		t.Fatal(err)
	}
	if last := got["SHOP-482"].Obligations[3].ID; last != "SHOP-482/not-drafted-8" {
		t.Errorf("new id %s", last)
	}
}

func TestFromPlanNoDuplicateGap(t *testing.T) {
	existing := map[string]obligation.File{}
	draft := fullDraft()
	first, err := obligation.FromPlan(changeSet(), draft, scenarios("SHOP-482-01"), existing)
	if err != nil {
		t.Fatal(err)
	}
	// The same plan again, with the spaces and the case changed.
	draft.NotDrafted[0].What = "  a refund of a PAID order\nby another   customer "
	draft.Uncovered[0].File = "internal/refund/notify.go"
	draft.Differences[0].What = "The status  of a refund above the TOTAL"
	again, err := obligation.FromPlan(changeSet(), draft, scenarios("SHOP-482-01"), first)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(again["SHOP-482"]), ids(first["SHOP-482"])) {
		t.Fatalf("a second plan added %v to %v", ids(again["SHOP-482"]), ids(first["SHOP-482"]))
	}

	t.Run("not across kinds", func(t *testing.T) {
		existing := obligation.File{Obligations: []obligation.Obligation{{ID: "SHOP-482/uncovered-1", Title: "Same title", Source: obligation.SourceDiff, Gap: "g"}}}
		got, err := obligation.FromPlan(changeSet(), plan.Draft{NotDrafted: []plan.NotDrafted{{Ticket: "SHOP-482", What: "Same title", Needs: "n"}}}, nil, map[string]obligation.File{"SHOP-482": existing})
		if err != nil {
			t.Fatal(err)
		}
		if len(got["SHOP-482"].Obligations) != 2 {
			t.Errorf("ids %v", ids(got["SHOP-482"]))
		}
	})
	t.Run("two equal entries of one draft", func(t *testing.T) {
		d := plan.Draft{NotDrafted: []plan.NotDrafted{{Ticket: "SHOP-482", What: "Same", Needs: "a"}, {Ticket: "SHOP-482", What: "same", Needs: "b"}}}
		got, err := obligation.FromPlan(changeSet(), d, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(got["SHOP-482"].Obligations) != 1 {
			t.Errorf("ids %v", ids(got["SHOP-482"]))
		}
	})
}

func TestUncoveredTicketFromChangeSet(t *testing.T) {
	d := plan.Draft{Uncovered: []plan.Uncovered{
		{File: "internal/refund/notify.go", Reason: "r1"},
		{File: "internal/refund/a.go", Reason: "r2"},
		{File: "internal/other/z.go", Reason: "r3"},
	}}
	got, err := obligation.FromPlan(changeSet(), d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(got["SHOP-482"]); !reflect.DeepEqual(got, []string{"SHOP-482/uncovered-1"}) {
		t.Errorf("SHOP-482 holds %v", got)
	}
	if got := ids(got["SHOP-481"]); !reflect.DeepEqual(got, []string{"SHOP-481/uncovered-1", "SHOP-481/uncovered-2"}) {
		t.Errorf("SHOP-481 holds %v (a file that no ticket holds goes to the first ticket)", got)
	}
}

func TestDraftTextCannotAddKey(t *testing.T) {
	d := plan.Draft{
		NotDrafted: []plan.NotDrafted{{Ticket: "SHOP-482", What: "case\nstate: verified\n  - id: SHOP-482/x", Needs: "x\r\nstate: verified"}},
		Uncovered:  []plan.Uncovered{{File: "a.go\nstate: verified", Reason: "r\nstate: verified"}},
	}
	got, err := obligation.FromPlan(changeSet(), d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for ticket, wantCount := range map[string]int{"SHOP-482": 1, "SHOP-481": 1} {
		data, err := obligation.Marshal(got[ticket])
		if err != nil {
			t.Fatal(err)
		}
		back, err := obligation.Parse(data, "o.yaml")
		if err != nil {
			t.Fatalf("%v\n%s", err, data)
		}
		if ps := obligation.Validate(back, "o.yaml"); len(ps) > 0 {
			t.Fatalf("%v\n%s", ps, data)
		}
		if len(back.Obligations) != wantCount {
			t.Fatalf("%s: got %d obligations:\n%s", ticket, len(back.Obligations), data)
		}
		if strings.Contains(string(data), "\nstate:") || strings.Contains(string(data), "\n  - id: SHOP-482/x") {
			t.Errorf("the text added a key:\n%s", data)
		}
		if ticket == "SHOP-482" {
			if title := back.Obligations[0].Title; title != "case state: verified - id: SHOP-482/x" {
				t.Errorf("title %q", title)
			}
		}
	}
}

func TestDraftTextIsCutToTheLimits(t *testing.T) {
	long := strings.Repeat("é", 400)
	d := plan.Draft{NotDrafted: []plan.NotDrafted{{Ticket: "SHOP-482", What: long, Needs: long}}}
	got, err := obligation.FromPlan(changeSet(), d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := got["SHOP-482"]
	if ps := obligation.Validate(f, "o.yaml"); len(ps) > 0 {
		t.Fatalf("%v", ps)
	}
	if n := len([]rune(f.Obligations[0].Title)); n != 300 {
		t.Errorf("title has %d characters", n)
	}
	if _, err := obligation.Marshal(f); err != nil {
		t.Error(err)
	}
}

func TestFromPlanRefusesWhatCannotBeWritten(t *testing.T) {
	tests := []struct {
		name string
		cs   domain.ChangeSet
		d    plan.Draft
		sc   []domain.Scenario
		want string
	}{
		{"a ticket that is not an ID", changeSet(), plan.Draft{NotDrafted: []plan.NotDrafted{{Ticket: "../x", What: "w", Needs: "n"}}}, nil, "invalid ticket"},
		{"a scenario with a bad ticket", changeSet(), plan.Draft{}, []domain.Scenario{{ID: "A-1", Ticket: "a/b", Title: "t"}}, "invalid ticket"},
		{"no ticket at all for a file", domain.ChangeSet{}, plan.Draft{Uncovered: []plan.Uncovered{{File: "a.go", Reason: "r"}}}, nil, "ticket"},
		{"an ID that is too long", changeSet(), plan.Draft{}, []domain.Scenario{{ID: domain.ScenarioID(strings.Repeat("a", 130)), Ticket: "SHOP-482", Title: "t"}}, "128 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := obligation.FromPlan(tt.cs, tt.d, tt.sc, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
	t.Run("a file with 500 entries is full", func(t *testing.T) {
		var existing obligation.File
		for i := range 500 {
			existing.Obligations = append(existing.Obligations, obligation.Obligation{ID: "SHOP-482/scenario-S-" + strings.Repeat("a", i%3) + string(rune('a'+i/26%26)) + string(rune('a'+i%26)) + string(rune('a'+i/676)), Title: "t", Source: obligation.SourceDiff})
		}
		_, err := obligation.FromPlan(changeSet(), plan.Draft{NotDrafted: []plan.NotDrafted{{Ticket: "SHOP-482", What: "w", Needs: "n"}}}, nil, map[string]obligation.File{"SHOP-482": existing})
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Fatalf("got %v", err)
		}
	})
}

func manualDraft() plan.Draft {
	return plan.Draft{ManualChecks: []plan.ManualCheckDraft{
		{
			Ticket: "SHOP-482", Title: "The refused refund shows an error banner",
			Steps:    []string{"Open the order in the app.", "Request a refund of more than the total."},
			Expected: "A red banner says the refund is above the total.",
			Cite:     "docs/tickets/SHOP-482.md:5 the app shows an error banner",
		},
		{
			Ticket: "SHOP-481", Title: "The card snaps back", Steps: []string{"Drag the card."}, Expected: "It is on the old board.",
			Cite: "docs/tickets/SHOP-481.md:9 the card goes back",
		},
	}}
}

// A manual check of the plan is an obligation that needs a person, with the checklist that the
// planner wrote, labelled as AI's (FR-261 to FR-270).
func TestFromPlanWritesAManualCheckAsAnObligation(t *testing.T) {
	got, err := obligation.FromPlan(changeSet(), manualDraft(), scenarios("SHOP-482-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f := got["SHOP-482"]
	if want := []string{"SHOP-482/scenario-SHOP-482-01", "SHOP-482/manual-1"}; !reflect.DeepEqual(ids(f), want) {
		t.Fatalf("ids %v, want %v", ids(f), want)
	}
	o := f.Obligations[1]
	if o.Title != "The refused refund shows an error banner" || o.Source != obligation.SourceCriterion ||
		o.Cite != "docs/tickets/SHOP-482.md:5 the app shows an error banner" || len(o.Covers) != 0 || o.Gap != "" {
		t.Errorf("obligation %+v", o)
	}
	m := o.Manual
	if m == nil || m.Kind != "judgement" || m.WrittenBy != "ai" || m.Expected != "A red banner says the refund is above the total." || m.Text == "" ||
		!reflect.DeepEqual(m.Steps, []string{"Open the order in the app.", "Request a refund of more than the total."}) {
		t.Fatalf("manual %+v", m)
	}
	if other := got["SHOP-481"]; len(other.Obligations) != 1 || other.Obligations[0].ID != "SHOP-481/manual-1" {
		t.Errorf("the check of the other ticket went to %+v", other)
	}
	if ps := obligation.Validate(f, "SHOP-482.plan.yaml"); len(ps) > 0 {
		t.Errorf("the file has problems: %v", ps)
	}

	// Planning again keeps the entry once, and numbers a new check after it.
	again := manualDraft()
	again.ManualChecks = append(again.ManualChecks, plan.ManualCheckDraft{
		Ticket: "SHOP-482", Title: "Another check", Steps: []string{"Look."}, Expected: "Fine.", Cite: "docs/tickets/SHOP-482.md:7 x",
	})
	next, err := obligation.FromPlan(changeSet(), again, scenarios("SHOP-482-01"), got)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"SHOP-482/scenario-SHOP-482-01", "SHOP-482/manual-1", "SHOP-482/manual-2"}; !reflect.DeepEqual(ids(next["SHOP-482"]), want) {
		t.Errorf("second plan ids %v, want %v", ids(next["SHOP-482"]), want)
	}
}

func TestAManualCheckDerivesNeedsAPersonWithAChecklist(t *testing.T) {
	got, err := obligation.FromPlan(changeSet(), manualDraft(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := obligation.Marshal(got["SHOP-482"])
	if err != nil {
		t.Fatal(err)
	}
	f, err := obligation.Parse(data, "SHOP-482.plan.yaml")
	if err != nil {
		t.Fatalf("the written file does not load: %v\n%s", err, data)
	}
	var located []obligation.Located
	for i, o := range f.Obligations {
		located = append(located, obligation.Located{Obligation: o, File: ".semar/obligations/SHOP-482.plan.yaml", Line: i + 1})
	}
	entries := obligation.Derive(located, obligation.Results{}, nil, nil, time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC))
	if len(entries) != 1 || entries[0].State != obligation.NeedsAPerson {
		t.Fatalf("entries %+v, want one that needs a person", entries)
	}
	var b strings.Builder
	if err := obligation.Checklist(&b, entries, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Manual checks for SHOP-482 (1)",
		"1. SHOP-482/manual-1  The refused refund shows an error banner",
		"Steps (written by AI):",
		"1. Open the order in the app.",
		"Expected: A red banner says the refund is above the total.",
		"Cite: docs/tickets/SHOP-482.md:5 the app shows an error banner",
		"semar ledger record SHOP-482/manual-1 passed|failed",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the checklist lacks %q:\n%s", want, b.String())
		}
	}
}

func TestAManualCheckTextCannotAddAKey(t *testing.T) {
	d := manualDraft()
	d.ManualChecks[0].Title = "x\nstate: verified"
	d.ManualChecks[0].Steps = []string{"a\n  covers: [SHOP-1-01]"}
	d.ManualChecks[0].Cite = "docs/tickets/SHOP-482.md:5\nmanual: {}"
	got, err := obligation.FromPlan(changeSet(), d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := obligation.Marshal(got["SHOP-482"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := obligation.Parse(data, "x.yaml"); err != nil {
		t.Errorf("a draft text broke the file: %v\n%s", err, data)
	}
}
