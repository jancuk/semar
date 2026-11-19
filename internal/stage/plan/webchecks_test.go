package plan

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/pagelist"
)

func TestDecodeTurnReadsEveryFormOfACheckAndGivesItsClaim(t *testing.T) {
	turn, ps := decodeTurn(t, `{"turn":{"kind":"act","actions":[
	  {"kind":"absent","ref":"e1"},
	  {"kind":"count","ref":"e1","count":3},
	  {"kind":"state","ref":"e2","state":"disabled"},
	  {"kind":"attribute","ref":"e1","attribute":"placeholder","match":"equals","expect":"Search orders"},
	  {"kind":"page","page":"path","match":"equals","expect":"/orders"}]}}`, false)
	a, ok := turn.(TurnAct)
	if !ok || len(ps) > 0 || len(a.Actions) != 5 {
		t.Fatalf("turn = %#v, problems %v", turn, ps)
	}
	more := mustTurn(t, `{"turn":{"kind":"act","actions":[{"kind":"attribute","ref":"e1","attribute":"href","match":"contains","expect":"/orders/"}]}}`, false).(TurnAct)
	a.Actions = append(a.Actions[:4:4], more.Actions[0], a.Actions[4])
	var claims []domain.Check
	for _, act := range a.Actions {
		if !act.IsCheck() {
			t.Errorf("%s is not a check", act)
		}
		claims = append(claims, act.Claim())
	}
	if claims[0].Exists == nil || *claims[0].Exists || claims[0].Element != "e1" {
		t.Errorf("absent = %+v", claims[0])
	}
	if claims[1].Count == nil || *claims[1].Count != 3 {
		t.Errorf("count = %+v", claims[1])
	}
	if claims[2].State != "disabled" || claims[2].Element != "e2" {
		t.Errorf("state = %+v", claims[2])
	}
	if claims[3].Attribute != "placeholder" || claims[3].Equals != "Search orders" || claims[4].Contains != "/orders/" || claims[4].Equals != nil {
		t.Errorf("attributes = %+v %+v", claims[3], claims[4])
	}
	if claims[5].Page != "path" || claims[5].Element != "" || claims[5].Equals != "/orders" {
		t.Errorf("page = %+v", claims[5])
	}
	for i, want := range []string{"is not on the page", "3 elements like e1", "e2 is disabled", `the placeholder of e1 equals "Search orders"`, `the href of e1 contains "/orders/"`, `the page path equals "/orders"`} {
		if got := a.Actions[i].String(); !strings.Contains(got, want) {
			t.Errorf("action %d reads %q, want %q in it", i, got, want)
		}
	}
}

func TestDecodeTurnRefusesAFormThatCodeCannotEvaluate(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"an element that is not in the list": {`{"kind":"absent","ref":"e9"}`, "e9 is not in the list"},
		"a count of none":                    {`{"kind":"count","ref":"e1","count":0}`, "use absent for none"},
		"a count with no number":             {`{"kind":"count","ref":"e1"}`, "count"},
		"a state that is not one":            {`{"kind":"state","ref":"e1","state":"green"}`, "must be one of checked, disabled, enabled, hidden, unchecked, visible"},
		"an attribute that is not listed":    {`{"kind":"attribute","ref":"e1","attribute":"onclick","match":"equals","expect":"x"}`, "must be one of placeholder"},
		"a match that is not one":            {`{"kind":"attribute","ref":"e1","attribute":"title","match":"like","expect":"x"}`, "equals or contains"},
		"an attribute with no text":          {`{"kind":"attribute","ref":"e1","attribute":"title","match":"equals","expect":" "}`, "needs expect"},
		"a page that is not path or title":   {`{"kind":"page","page":"host","match":"equals","expect":"x"}`, "path or title"},
		"a path that does not start with /":  {`{"kind":"page","page":"path","match":"equals","expect":"orders"}`, "starts with /"},
		"a page check that names an element": {`{"kind":"page","ref":"e1","page":"title","match":"equals","expect":"Orders"}`, "names no element"},
		"a variable that is not the run":     {`{"kind":"page","page":"title","match":"contains","expect":"{{b.board}}"}`, "{{run.id}}"},
		"another variable in an attribute":   {`{"kind":"attribute","ref":"e1","attribute":"title","match":"equals","expect":"{{b.board}}"}`, "{{run.id}}"},
	} {
		t.Run(name, func(t *testing.T) {
			_, ps := decodeTurn(t, `{"turn":{"kind":"act","actions":[`+tc.raw+`]}}`, false)
			if got := messages(ps); !strings.Contains(got, tc.want) {
				t.Errorf("problems:\n%swant one with %q", got, tc.want)
			}
		})
	}
}

func TestDecodeTurnNeverReadsAnAttributeOfAPasswordField(t *testing.T) {
	page := pagelist.Build("http://shop.example.com/", "Shop", []pagelist.Item{{Role: "textbox", Name: "Password", Secret: true, Acts: true}})
	_, ps, err := DecodeTurn([]byte(`{"turn":{"kind":"act","actions":[{"kind":"attribute","ref":"e1","attribute":"value","match":"equals","expect":"x"}]}}`), page, false, nil)
	if err != nil || !strings.Contains(messages(ps), "password field") {
		t.Errorf("problems %v, err %v", ps, err)
	}
}

func TestTheSchemaOfATurnCarriesEveryFormAndKeepsTheOthersOut(t *testing.T) {
	raw := string(TurnSchema(twoElementPage(), false, nil))
	for _, want := range []string{`"absent"`, `"count"`, `"state"`, `"attribute"`, `"page"`, `"disabled"`, `"unchecked"`, `"placeholder"`, `"aria-label"`, `"equals"`, `"contains"`, `"path"`, `"title"`, `"integer"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("the schema lacks %s", want)
		}
	}
	if strings.Contains(raw, "gherkin") || strings.Contains(raw, "Gherkin") {
		t.Error("the model may not write Gherkin")
	}
}

func TestAFlowPreparesTheChecksOfOtherFormsBeforeItDoesTheActionsOfATurn(t *testing.T) {
	gone := Action{Kind: ActionAbsent, Ref: "e3"}
	m := &scriptedModel{turns: []Turn{act(clickSave, checkSave, gone), finished}}
	b := &scriptedBrowser{shown: map[string]string{"e3": "Saved"}}
	end := runFlow(t, m, b, DefaultFlowLimits)
	if end.Done == nil || len(b.prepared) != 1 || b.prepared[0] != gone {
		t.Fatalf("end %+v, prepared %v", end, b.prepared)
	}
	if len(end.Checks) != 2 || end.Checks[0].Check != nil || end.Checks[1].Check == nil || end.Checks[1].Expect != "" || end.Checks[1].Check.Exists == nil {
		t.Errorf("checks = %+v", end.Checks)
	}
}

func TestACheckOfAnotherFormThatThePageDoesNotMeetIsDoubtedOnceAndThenRecorded(t *testing.T) {
	disabled := Action{Kind: ActionState, Ref: "e2", State: "disabled"}
	m := &scriptedModel{turns: []Turn{act(disabled, checkSave), act(disabled), finished}}
	b := &scriptedBrowser{shown: map[string]string{"e3": "Saved"}, unmet: map[string]string{"e2": "shown, enabled, not checked"}}
	end := runFlow(t, m, b, DefaultFlowLimits)
	if end.Done == nil || len(end.Checks) != 1 || end.Checks[0].Check == nil || end.Checks[0].Check.State != "disabled" {
		t.Fatalf("end = %+v", end)
	}
	note := m.asked[1].Note
	for _, want := range []string{"check e2 is disabled", "does not hold", "shown, enabled, not checked", "Nothing was recorded", "check e3 equals"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note lacks %q: %s", want, note)
		}
	}
	if !strings.Contains(m.asked[2].Page.String(), `e2 button "Save"`) || m.asked[2].Note != "" {
		t.Errorf("the model was asked twice: %q", m.asked[2].Note)
	}
}

// A refusal because the locator matches several elements costs one action, and the next turn counts
// against the limit of turns like any other.
func TestARefusedCheckGivesTheModelOneMoreTurnThatCountsAgainstTheLimit(t *testing.T) {
	refused := Action{Kind: ActionState, Ref: "e2", State: "enabled"}
	m := &scriptedModel{turns: []Turn{act(refused), act(refused), act(refused)}}
	b := &scriptedBrowser{fail: map[string]string{"e2": "the locator matches 2 elements: { css: a }, { css: b }: e2 does not name one element, so name another element of the list for this check"}}
	end := runFlow(t, m, b, FlowLimits{Turns: 3, Actions: 40, Time: DefaultFlowLimits.Time})
	if end.Done != nil || !strings.Contains(end.Manual, "did not finish after 3 turns") || end.Turns != 3 || len(end.Checks) != 0 {
		t.Fatalf("end = %+v", end)
	}
	if note := m.asked[1].Note; !strings.Contains(note, "the locator matches 2 elements") || !strings.Contains(note, "could not be done") {
		t.Errorf("the note = %s", note)
	}
}
