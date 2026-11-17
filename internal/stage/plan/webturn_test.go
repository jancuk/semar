package plan

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/pagelist"
)

func twoElementPage() pagelist.List {
	return pagelist.Build("http://shop.example.com/", "Shop", []pagelist.Item{
		{Role: "textbox", Name: "Title", Acts: true}, {Role: "button", Name: "Save", Acts: true},
	})
}

var boardFixture = fixture.Entry{ID: "board.shared", Description: "A board with one card", Outputs: []string{"board"}, Params: []fixture.ParamEntry{{Name: "owner", Required: true}}}

func decodeTurn(t *testing.T, raw string, first bool) (Turn, []Problem) {
	t.Helper()
	turn, ps, err := DecodeTurn([]byte(raw), twoElementPage(), first, []fixture.Entry{boardFixture})
	if err != nil {
		t.Fatalf("DecodeTurn: %v\n%s", err, raw)
	}
	return turn, ps
}

func TestDecodeTurnReadsEachKind(t *testing.T) {
	turn, ps := decodeTurn(t, `{"turn":{"kind":"act","actions":[
	  {"kind":"fill","ref":"e1","text":"Board {{run.id}}"},{"kind":"click","ref":"e2"},{"kind":"press","ref":"e1","key":"Enter"},
	  {"kind":"drag","ref":"e1","to":"e2"},{"kind":"check","ref":"e2","expect":"Save"}]}}`, false)
	a, ok := turn.(TurnAct)
	if !ok || len(ps) > 0 || len(a.Actions) != 5 || a.Actions[0] != (Action{Kind: ActionFill, Ref: "e1", Text: "Board {{run.id}}"}) ||
		a.Actions[3] != (Action{Kind: ActionDrag, Ref: "e1", To: "e2"}) || a.Actions[4].Expect != "Save" {
		t.Fatalf("turn = %#v, problems %v", turn, ps)
	}
	if d, ok := mustTurn(t, `{"turn":{"kind":"done","title":"A board is saved","intent":"Before: x. Now: y.","unproved":""}}`, false).(TurnDone); !ok || d.Title != "A board is saved" || d.Unproved != "" {
		t.Errorf("done = %#v", d)
	}
	if d, ok := mustTurn(t, `{"turn":{"kind":"done","title":"A card moves","intent":"Before: x. Now: y.","unproved":"A refused move puts the card back."}}`, false).(TurnDone); !ok || d.Unproved != "A refused move puts the card back." {
		t.Errorf("done with a part left = %#v", d)
	}
	if m, ok := mustTurn(t, `{"turn":{"kind":"manual","reason":"no such page"}}`, false).(TurnManual); !ok || m.Reason != "no such page" {
		t.Errorf("manual = %#v", m)
	}
	g, ok := mustTurn(t, `{"turn":{"kind":"given","given":[{"fixture":"board.shared","as":"b","with":[{"name":"owner","value":"ana"}]}]}}`, true).(TurnGiven)
	if !ok || len(g.Given) != 1 || g.Given[0].As != "b" {
		t.Errorf("given = %#v", g)
	}
}

func mustTurn(t *testing.T, raw string, first bool) Turn {
	t.Helper()
	turn, ps := decodeTurn(t, raw, first)
	if len(ps) > 0 {
		t.Fatalf("problems: %v\n%s", ps, raw)
	}
	return turn
}

func TestDecodeTurnAcceptsADateVariableInATextAndInACheck(t *testing.T) {
	turn, ps := decodeTurn(t, `{"turn":{"kind":"act","actions":[
	  {"kind":"fill","ref":"e1","text":"{{date.plus7d}}"},{"kind":"fill","ref":"e1","text":"Plan {{run.id}} for {{local.plus1d}}"},
	  {"kind":"check","ref":"e2","expect":"Due {{date.plus7d}}"}]}}`, false)
	a, ok := turn.(TurnAct)
	if !ok || len(ps) > 0 || a.Actions[0].Text != "{{date.plus7d}}" || a.Actions[2].Expect != "Due {{date.plus7d}}" {
		t.Fatalf("turn = %#v, problems %v", turn, ps)
	}
	for name, raw := range map[string]string{
		"a date name that is not a variable in a text":  `{"turn":{"kind":"act","actions":[{"kind":"fill","ref":"e1","text":"{{date.tomorrow}}"}]}}`,
		"a date name that is not a variable in a check": `{"turn":{"kind":"act","actions":[{"kind":"check","ref":"e2","expect":"{{date.plus0d}}"}]}}`,
		"another variable in a check":                   `{"turn":{"kind":"act","actions":[{"kind":"check","ref":"e2","expect":"{{b.board}}"}]}}`,
		"a half variable in a check":                    `{"turn":{"kind":"act","actions":[{"kind":"check","ref":"e2","expect":"Due {{date.today"}]}}`,
	} {
		_, ps := decodeTurn(t, raw, false)
		if got := messages(ps); !strings.Contains(got, "{{run.id}} and the date variables are the only variables") {
			t.Errorf("%s: problems:\n%s", name, got)
		}
	}
}

func TestDecodeTurnFindsEveryProblemInPlainWords(t *testing.T) {
	for name, tc := range map[string]struct {
		raw   string
		first bool
		want  string
	}{
		"a reference that is not in the list": {`{"turn":{"kind":"act","actions":[{"kind":"click","ref":"e9"}]}}`, false, "e9 is not in the list"},
		"no action":                           {`{"turn":{"kind":"act","actions":[]}}`, false, "1 to 5 actions"},
		"six actions":                         {`{"turn":{"kind":"act","actions":[` + strings.Repeat(`{"kind":"click","ref":"e1"},`, 5) + `{"kind":"click","ref":"e2"}]}}`, false, "1 to 5 actions"},
		"a fill with no text":                 {`{"turn":{"kind":"act","actions":[{"kind":"fill","ref":"e1","text":""}]}}`, false, "text"},
		"a check with no expectation":         {`{"turn":{"kind":"act","actions":[{"kind":"check","ref":"e1","expect":" "}]}}`, false, "expect"},
		"a key that is not known":             {`{"turn":{"kind":"act","actions":[{"kind":"press","ref":"e1","key":"Tab"}]}}`, false, "Enter or Escape"},
		"a variable that is not the run":      {`{"turn":{"kind":"act","actions":[{"kind":"fill","ref":"e1","text":"{{b.board}}"}]}}`, false, "{{run.id}}"},
		"a drag onto itself":                  {`{"turn":{"kind":"act","actions":[{"kind":"drag","ref":"e1","to":"e1"}]}}`, false, "itself"},
		"done with no title":                  {`{"turn":{"kind":"done","title":"","intent":"x"}}`, false, "title"},
		"manual with no reason":               {`{"turn":{"kind":"manual","reason":""}}`, false, "reason"},
		"given later":                         {`{"turn":{"kind":"given","given":[{"fixture":"board.shared","as":"b","with":[{"name":"owner","value":"a"}]}]}}`, false, "first answer"},
		"a fixture that does not exist":       {`{"turn":{"kind":"given","given":[{"fixture":"nope","as":"b","with":[]}]}}`, true, "not in the catalogue"},
		"a required parameter left out":       {`{"turn":{"kind":"given","given":[{"fixture":"board.shared","as":"b","with":[]}]}}`, true, "owner"},
		"a given with no fixture":             {`{"turn":{"kind":"given","given":[]}}`, true, "at least one"},
	} {
		t.Run(name, func(t *testing.T) {
			_, ps := decodeTurn(t, tc.raw, tc.first)
			if got := messages(ps); !strings.Contains(got, tc.want) {
				t.Errorf("problems:\n%swant one with %q", got, tc.want)
			}
		})
	}
}

func TestDecodeTurnRefusesWhatIsNotATurn(t *testing.T) {
	for name, raw := range map[string]string{
		"an unknown kind":  `{"turn":{"kind":"click"}}`,
		"an unknown field": `{"turn":{"kind":"manual","reason":"x","also":1}}`,
		"data after":       `{"turn":{"kind":"manual","reason":"x"}} {}`,
		"not json":         `click e1`,
	} {
		if _, _, err := DecodeTurn([]byte(raw), twoElementPage(), false, nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestTheTurnSchemaIsClosedAndNamesTheReferencesOfThePage(t *testing.T) {
	schema := string(TurnSchema(twoElementPage(), false, []fixture.Entry{boardFixture}))
	for _, want := range []string{`"unproved"`, `"enum":["e1","e2"]`, `"enum":["act"]`, `"enum":["done"]`, `"enum":["manual"]`, `"enum":["Enter","Escape"]`,
		`"enum":["click"]`, `"enum":["fill"]`, `"enum":["drag"]`, `"enum":["check"]`, `"additionalProperties":false`} {
		if !strings.Contains(schema, want) {
			t.Errorf("the schema lacks %s:\n%s", want, schema)
		}
	}
	if strings.Contains(schema, `"given"`) {
		t.Error("a later turn may not answer given, and its schema must not offer it")
	}
	first := string(TurnSchema(twoElementPage(), true, []fixture.Entry{boardFixture}))
	if !strings.Contains(first, `"enum":["given"]`) || !strings.Contains(first, `"enum":["board.shared"]`) {
		t.Errorf("the first turn does not offer the fixtures:\n%s", first)
	}
	if noFixtures := string(TurnSchema(twoElementPage(), true, nil)); strings.Contains(noFixtures, `"enum":["given"]`) {
		t.Error("with no fixture in the catalogue, given is not offered")
	}
	var any map[string]any
	if err := json.Unmarshal([]byte(schema), &any); err != nil {
		t.Fatal(err)
	}
}

func TestTheTurnPromptHoldsThePageTheHistoryAndTheRules(t *testing.T) {
	in := TurnInput{
		Criterion: "docs/tickets/SHOP-482.md:9\nWeb: a card shows an Overdue badge.", Number: 2, First: false, Page: twoElementPage(),
		Done: []string{"fill e1 with \"Board {{run.id}}\""}, Note: "Action 2 could not be done.",
		Given: []GivenOutput{{Alias: "b", Name: "board", Value: "b_81"}}, Reading: "- Open the board.\nExpected: Overdue",
	}
	text, err := in.Prompt(nil, nil, nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Web: a card shows an Overdue badge.", `e2 button "Save"`, "1. fill e1", "Action 2 could not be done.", "`b.board` is `b_81`",
		"turn 2 of 12", "Open the board."} {
		if !strings.Contains(text, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "`given`") {
		t.Error("a later turn is not told about given")
	}
	first := TurnInput{Criterion: "c", Number: 1, First: true, Page: twoElementPage()}
	text, err = first.Prompt(nil, nil, []fixture.Entry{boardFixture}, 12)
	if err != nil || !strings.Contains(text, "board.shared") || !strings.Contains(text, "`given`") {
		t.Errorf("the first prompt does not list the fixtures: %v\n%s", err, text)
	}
	_ = domain.FixtureID("")
}
