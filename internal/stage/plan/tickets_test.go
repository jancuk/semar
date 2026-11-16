package plan

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
)

const ticket482 = "docs/tickets/SHOP-482.md"

// ticketInput is input with the file of ticket SHOP-482 (12 lines) and two accounts.
func ticketInput(t *testing.T) Input {
	t.Helper()
	in := input(t)
	text := "# Refund above the total\n\nAcceptance criteria\n" + strings.Repeat("- a criterion\n", 9)
	in.Tickets = []knowledge.Ticket{{Key: "SHOP-482", Path: ticket482, Text: text}}
	in.Accounts = []Account{{Name: "default", Description: "the shop owner"}, {Name: "outsider", Description: "a customer of another shop"}}
	return in
}

// ticketDraft is goodDraft with a ticket expectation at the refund step, and edits.
func ticketDraft(t *testing.T, edit func(d *Draft)) Draft {
	t.Helper()
	d := draft(t)
	d.Scenarios[0].Steps[1].Expect[0].Source = "ticket"
	d.Scenarios[0].Steps[1].Expect[0].Why = ticket482 + ":4 a refund above the total is refused with 422"
	if edit != nil {
		edit(&d)
	}
	return d
}

func problemTexts(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "%s: %s\n", p.Path, p.Message)
	}
	return b.String()
}

func wantProblem(t *testing.T, ps []Problem, path, contains string) {
	t.Helper()
	for _, p := range ps {
		if p.Path == path && strings.Contains(p.Message, contains) {
			return
		}
	}
	t.Errorf("no problem at %s with %q in:\n%s", path, contains, problemTexts(ps))
}

func TestTicketExpectationIsKeptAsDraftedWithByModel(t *testing.T) {
	in := ticketInput(t)
	scs, _, ps := Validate(ticketDraft(t, nil), in)
	if len(ps) > 0 {
		t.Fatalf("problems: %s", problemTexts(ps))
	}
	got := scs[0].Steps[1].Expect
	if got[0].Source != domain.SourceTicket || got[0].Cite != ticket482+":4 a refund above the total is refused with 422" {
		t.Errorf("ticket expectation = %+v", got[0])
	}
	if got[1].Source != domain.SourceDiff {
		t.Errorf("source of the second expectation = %q, want diff by default", got[1].Source)
	}
	for _, sc := range scs {
		for _, st := range sc.Steps {
			for _, e := range st.Expect {
				if e.By != "model" {
					t.Errorf("%s step %s: by = %q, want model on every drafted expectation", sc.ID, st.ID, e.By)
				}
			}
		}
	}
}

func TestTicketCiteProblems(t *testing.T) {
	cite := "scenarios[0].steps[1].expect[0].why"
	cases := []struct {
		name string
		why  string
		want string
	}{
		{"a line that does not exist", ticket482 + ":13 x", ticket482 + " has 12 lines, so it has no line 13"},
		{"line zero", ticket482 + ":0 x", "names no line"},
		{"the ticket file of another ticket", "docs/tickets/SHOP-483.md:2 x", "is not the ticket file of SHOP-482; cite " + ticket482},
		{"a code file", "internal/refund/handler.go:41 x", "is not the ticket file of SHOP-482; cite " + ticket482},
		{"no cite", "a refund above the total is refused", "must start with " + ticket482 + ":<line>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, ps := Validate(ticketDraft(t, func(d *Draft) { d.Scenarios[0].Steps[1].Expect[0].Why = c.why }), ticketInput(t))
			wantProblem(t, ps, cite, c.want)
		})
	}
}

func TestTicketSourceNeedsATicketFile(t *testing.T) {
	in := ticketInput(t)
	d := ticketDraft(t, func(d *Draft) {
		d.Scenarios[1].Steps[1].Expect[0].Source = "ticket"
		d.Scenarios[1].Steps[1].Expect[0].Why = ticket482 + ":2 x"
	})
	_, _, ps := Validate(d, in)
	wantProblem(t, ps, "scenarios[1].steps[1].expect[0].source", "ticket SHOP-483 has no ticket file in the repository, so the source ticket cannot be used: use diff")

	in.Tickets = nil
	_, _, ps = Validate(ticketDraft(t, nil), in)
	wantProblem(t, ps, "scenarios[0].steps[1].expect[0].source", "ticket SHOP-482 has no ticket file")
}

func TestUnknownSourceIsAProblem(t *testing.T) {
	_, _, ps := Validate(ticketDraft(t, func(d *Draft) { d.Scenarios[0].Steps[1].Expect[1].Source = "person" }), ticketInput(t))
	wantProblem(t, ps, "scenarios[0].steps[1].expect[1].source", `source "person" must be diff or ticket`)
}

func TestStepAsMustBeAKnownAccount(t *testing.T) {
	in := ticketInput(t)
	scs, _, ps := Validate(ticketDraft(t, func(d *Draft) {
		d.Scenarios[0].Steps[0].As = "outsider"
		d.Scenarios[0].Steps[1].As = "outsider"
	}), in)
	if len(ps) > 0 {
		t.Fatalf("problems: %s", problemTexts(ps))
	}
	if scs[0].Steps[0].As != "outsider" || scs[0].Steps[1].As != "outsider" || scs[1].Steps[0].As != "" {
		t.Errorf("as = %q %q %q", scs[0].Steps[0].As, scs[0].Steps[1].As, scs[1].Steps[0].As)
	}
	_, _, ps = Validate(ticketDraft(t, func(d *Draft) { d.Scenarios[0].Steps[1].As = "root" }), in)
	wantProblem(t, ps, "scenarios[0].steps[1].as", `account "root" is not in the configuration; use one of default, outsider`)

	in.Accounts = nil
	_, _, ps = Validate(ticketDraft(t, func(d *Draft) { d.Scenarios[0].Steps[1].As = "outsider" }), in)
	wantProblem(t, ps, "scenarios[0].steps[1].as", `account "outsider" is not in the configuration`)
}

func TestOperandVariablesAreChecked(t *testing.T) {
	in := ticketInput(t)
	ok := func(d *Draft) {
		d.Scenarios[0].Steps[1].Expect[1].Check.Equals = "{{order_id}}"
	}
	if _, _, ps := Validate(ticketDraft(t, ok), in); len(ps) > 0 {
		t.Fatalf("a captured variable in an operand: %s", problemTexts(ps))
	}
	_, _, ps := Validate(ticketDraft(t, func(d *Draft) { d.Scenarios[0].Steps[1].Expect[1].Check.Equals = "{{nope}}" }), in)
	if !strings.Contains(problemTexts(ps), "nope") {
		t.Errorf("an unknown variable in an operand is not a problem:\n%s", problemTexts(ps))
	}
	_, _, ps = Validate(ticketDraft(t, func(d *Draft) { d.Scenarios[0].Steps[1].Expect[1].Check.Equals = "{{secret.TOKEN}}" }), in)
	if !strings.Contains(problemTexts(ps), "secret") {
		t.Errorf("a secret in an operand is not a problem:\n%s", problemTexts(ps))
	}
}

func manual(edit func(m *ManualCheckDraft)) ManualCheckDraft {
	m := ManualCheckDraft{
		Ticket: "SHOP-482", Title: "The refused refund shows an error banner",
		Steps:    []string{"Open the order in the app.", "Request a refund of more than the total."},
		Expected: "A red banner says the refund is above the total.",
		Cite:     ticket482 + ":5 the app shows an error banner",
	}
	if edit != nil {
		edit(&m)
	}
	return m
}

func TestManualChecksAreValidated(t *testing.T) {
	in := ticketInput(t)
	with := func(m ManualCheckDraft) Draft {
		return ticketDraft(t, func(d *Draft) { d.ManualChecks = []ManualCheckDraft{m} })
	}
	if _, _, ps := Validate(with(manual(nil)), in); len(ps) > 0 {
		t.Fatalf("a good manual check: %s", problemTexts(ps))
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = "step"
	}
	cases := []struct {
		name  string
		check ManualCheckDraft
		path  string
		want  string
	}{
		{"unknown ticket", manual(func(m *ManualCheckDraft) { m.Ticket = "SHOP-9" }), "manualChecks[0].ticket", `ticket "SHOP-9" is not in the change set`},
		{"ticket without a file", manual(func(m *ManualCheckDraft) { m.Ticket = "SHOP-483" }), "manualChecks[0].ticket", "ticket SHOP-483 has no ticket file"},
		{"cite beyond the file", manual(func(m *ManualCheckDraft) { m.Cite = ticket482 + ":40 x" }), "manualChecks[0].cite", "has 12 lines"},
		{"cite of another file", manual(func(m *ManualCheckDraft) { m.Cite = "internal/refund/handler.go:3 x" }), "manualChecks[0].cite", "is not the ticket file of SHOP-482"},
		{"no title", manual(func(m *ManualCheckDraft) { m.Title = " " }), "manualChecks[0].title", "needs a title"},
		{"no expected result", manual(func(m *ManualCheckDraft) { m.Expected = "" }), "manualChecks[0].expected", "needs an expected result"},
		{"no step", manual(func(m *ManualCheckDraft) { m.Steps = nil }), "manualChecks[0].steps", "1 to 20 steps"},
		{"21 steps", manual(func(m *ManualCheckDraft) { m.Steps = many }), "manualChecks[0].steps", "1 to 20 steps"},
		{"an empty step", manual(func(m *ManualCheckDraft) { m.Steps = []string{"ok", " "} }), "manualChecks[0].steps[1]", "is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, ps := Validate(with(c.check), in)
			wantProblem(t, ps, c.path, c.want)
		})
	}
}

func TestADifferenceMayCiteATicketFile(t *testing.T) {
	in := ticketInput(t)
	d := ticketDraft(t, func(d *Draft) {
		d.Differences = []Difference{{Ticket: "SHOP-482", Document: ticket482 + ":4 refused with 422", Code: "internal/refund/handler.go:41 accepts it with 200", What: "the status"}}
	})
	if _, _, ps := Validate(d, in); len(ps) > 0 {
		t.Fatalf("a difference that cites a ticket file: %s", problemTexts(ps))
	}
	in.Tickets = nil
	_, _, ps := Validate(d, in)
	wantProblem(t, ps, "differences", "no document was listed for you to read")
}

func TestDraftSchemaHasTheNewFields(t *testing.T) {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(Schema(), &s); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Properties["manualChecks"]; !ok {
		t.Error("the schema has no manualChecks")
	}
	text := string(Schema())
	for _, want := range []string{`"source": {"type": "string", "enum": ["diff", "ticket"]}`, `"as": {"type": "string"}`} {
		if !strings.Contains(text, want) {
			t.Errorf("the schema lacks %s", want)
		}
	}
	d, err := DecodeDraft([]byte(`{"summary":"s","tickets":[],"scenarios":[],"notDrafted":[],"uncovered":[],"manualChecks":[{"ticket":"A-1","title":"t","steps":["x"],"expected":"e","cite":"f:1"}]}`))
	if err != nil || len(d.ManualChecks) != 1 {
		t.Errorf("decode: %v %+v", err, d.ManualChecks)
	}
}
