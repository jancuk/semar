package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/pagelist"
)

// scriptedModel answers each turn with the next of its turns, and keeps what it was asked.
type scriptedModel struct {
	turns []Turn
	asked []TurnInput
	err   error
}

func (m *scriptedModel) turn(_ context.Context, in TurnInput) (Turn, error) {
	m.asked = append(m.asked, in)
	if m.err != nil {
		return nil, m.err
	}
	if len(m.asked) > len(m.turns) {
		return nil, fmt.Errorf("the scripted model has no turn %d", len(m.asked))
	}
	return m.turns[len(m.asked)-1], nil
}

// scriptedBrowser lists a page of three elements, does what it is told, and fails the actions that
// name a reference in fail.
type scriptedBrowser struct {
	acted    []Action
	prepared []Action
	unmet    map[string]string // a reference to what the page shows when a check of another form on it does not hold
	given    [][]AnswerGiven
	fail     map[string]string // a reference to why its action cannot be done
	shown    map[string]string // a reference to what a check reads
	listErr  error
	outputs  []GivenOutput
}

func (b *scriptedBrowser) List(context.Context) (pagelist.List, error) {
	if b.listErr != nil {
		return pagelist.List{}, b.listErr
	}
	return pagelist.Build("http://shop.example.com/orders", "Orders", []pagelist.Item{
		{Role: "textbox", Name: "Title", Acts: true}, {Role: "button", Name: "Save", Acts: true}, {Role: "text", Name: "Saved"},
	}), nil
}

func (b *scriptedBrowser) Act(_ context.Context, a Action) (string, error) {
	b.acted = append(b.acted, a)
	if why, ok := b.fail[a.Ref]; ok {
		return "", errors.New(why)
	}
	return b.shown[a.Ref], nil
}

func (b *scriptedBrowser) Prepare(_ context.Context, checks []Action) {
	b.prepared = append(b.prepared, checks...)
}

func (b *scriptedBrowser) Compare(_ context.Context, a Action) (string, bool, error) {
	if why, ok := b.fail[a.Ref]; ok {
		return "", false, errors.New(why)
	}
	if a.Kind != ActionCheck {
		why, unmet := b.unmet[a.Ref]
		return why, !unmet, nil
	}
	return b.shown[a.Ref], b.shown[a.Ref] == a.Expect, nil
}

func (b *scriptedBrowser) Given(_ context.Context, g []AnswerGiven) ([]GivenOutput, error) {
	b.given = append(b.given, g)
	return b.outputs, nil
}

func act(actions ...Action) TurnAct { return TurnAct{Actions: actions} }

var (
	fillTitle = Action{Kind: ActionFill, Ref: "e1", Text: "Board {{run.id}}"}
	clickSave = Action{Kind: ActionClick, Ref: "e2"}
	checkSave = Action{Kind: ActionCheck, Ref: "e3", Expect: "Saved"}
	finished  = TurnDone{Title: "A board is saved", Intent: "Before: nothing saved. Now: a saved board."}
)

func runFlow(t *testing.T, m *scriptedModel, b *scriptedBrowser, lim FlowLimits) FlowEnd {
	t.Helper()
	end, err := RunFlow(t.Context(), lim, "criterion text", m.turn, b)
	if err != nil {
		t.Fatal(err)
	}
	return end
}

func TestAFlowOfTwoTurnsThenDone(t *testing.T) {
	m := &scriptedModel{turns: []Turn{act(fillTitle, clickSave), act(checkSave), finished}}
	b := &scriptedBrowser{shown: map[string]string{"e3": "Saved"}}
	end := runFlow(t, m, b, DefaultFlowLimits)
	if end.Manual != "" || end.Done == nil || end.Done.Title != finished.Title {
		t.Fatalf("end = %+v", end)
	}
	if end.Turns != 3 || end.Actions != 3 || len(b.acted) != 3 {
		t.Errorf("turns %d, actions %d, acted %v", end.Turns, end.Actions, b.acted)
	}
	if len(end.Checks) != 1 || end.Checks[0] != (FlowCheck{Ref: "e3", Expect: "Saved", Shown: "Saved"}) {
		t.Errorf("checks = %+v", end.Checks)
	}
	second := m.asked[1]
	if second.Number != 2 || second.First || !strings.Contains(second.Page.String(), `e2 button "Save"`) || second.Criterion != "criterion text" {
		t.Errorf("the second turn was asked %+v", second)
	}
	if len(second.Done) != 2 || !strings.Contains(second.Done[0], "fill e1") || !strings.Contains(second.Done[1], "click e2") {
		t.Errorf("what was done: %v", second.Done)
	}
}

func TestAnActionThatFailsIsReportedAndTheRestOfTheTurnIsNotDone(t *testing.T) {
	m := &scriptedModel{turns: []Turn{act(clickSave, fillTitle), act(checkSave), finished}}
	b := &scriptedBrowser{fail: map[string]string{"e2": "e2 is disabled"}, shown: map[string]string{"e3": "Saved"}}
	end := runFlow(t, m, b, DefaultFlowLimits)
	if end.Done == nil {
		t.Fatalf("end = %+v", end)
	}
	if len(b.acted) != 2 || b.acted[1].Ref != "e3" {
		t.Errorf("acted %v: the fill after the failed click must not run", b.acted)
	}
	note := m.asked[1].Note
	for _, want := range []string{"click e2", "e2 is disabled", "fill e1", "were not done"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note %q lacks %q", note, want)
		}
	}
	if end.Actions != 2 {
		t.Errorf("a failed action counts: actions = %d, want 2", end.Actions)
	}
}

func TestGivenIsAnsweredFirstAndTheModelGetsTheOutputs(t *testing.T) {
	g := TurnGiven{Given: []AnswerGiven{{Fixture: "board.shared", As: "b"}}}
	m := &scriptedModel{turns: []Turn{g, act(checkSave), finished}}
	b := &scriptedBrowser{shown: map[string]string{"e3": "Saved"}, outputs: []GivenOutput{{Alias: "b", Name: "board", Value: "b_81"}}}
	end := runFlow(t, m, b, DefaultFlowLimits)
	if end.Done == nil || len(b.given) != 1 || b.given[0][0].Fixture != "board.shared" {
		t.Fatalf("end %+v, given %v", end, b.given)
	}
	if !m.asked[0].First || m.asked[1].First {
		t.Errorf("only the first turn may answer given: %v %v", m.asked[0].First, m.asked[1].First)
	}
	if got := m.asked[1].Given; len(got) != 1 || got[0].Value != "b_81" {
		t.Errorf("the second turn got the outputs %+v", got)
	}
	if len(end.Given) != 1 || end.Given[0].As != "b" {
		t.Errorf("the flow does not say which fixtures it made: %+v", end.Given)
	}
}

func TestGivenAfterAnActionIsRefusedAndNothingIsMade(t *testing.T) {
	g := TurnGiven{Given: []AnswerGiven{{Fixture: "board.shared", As: "b"}}}
	m := &scriptedModel{turns: []Turn{act(fillTitle), g, act(checkSave), finished}}
	b := &scriptedBrowser{shown: map[string]string{"e3": "Saved"}}
	end := runFlow(t, m, b, DefaultFlowLimits)
	if end.Done == nil || len(b.given) != 0 {
		t.Fatalf("end %+v, given %v", end, b.given)
	}
	if note := m.asked[2].Note; !strings.Contains(note, "given") || !strings.Contains(note, "first") {
		t.Errorf("the note after the refused given: %q", note)
	}
}

func TestADoneWithNoCheckIsAManualCheck(t *testing.T) {
	m := &scriptedModel{turns: []Turn{act(fillTitle), finished}}
	end := runFlow(t, m, &scriptedBrowser{}, DefaultFlowLimits)
	if end.Done != nil || !strings.Contains(end.Manual, "no check") {
		t.Errorf("end = %+v", end)
	}
}

func TestAFailedCheckIsNoCheck(t *testing.T) {
	m := &scriptedModel{turns: []Turn{act(checkSave), finished}}
	end := runFlow(t, m, &scriptedBrowser{fail: map[string]string{"e3": "e3 is covered"}}, DefaultFlowLimits)
	if end.Done != nil || !strings.Contains(end.Manual, "no check") {
		t.Errorf("end = %+v", end)
	}
}

func TestTheModelSaysManual(t *testing.T) {
	m := &scriptedModel{turns: []Turn{act(fillTitle), TurnManual{Reason: "the page has no due date"}}}
	end := runFlow(t, m, &scriptedBrowser{}, DefaultFlowLimits)
	if end.Done != nil || end.Manual != "the page has no due date" {
		t.Errorf("end = %+v", end)
	}
}

func TestTheLimitsEndAFlowAsAManualCheck(t *testing.T) {
	five := act(clickSave, clickSave, clickSave, clickSave, clickSave)
	forever := func(n int) []Turn {
		turns := make([]Turn, n)
		for i := range turns {
			turns[i] = act(fillTitle)
		}
		return turns
	}
	for name, tc := range map[string]struct {
		turns []Turn
		lim   FlowLimits
		want  string
		acted int
	}{
		"12 turns": {forever(20), DefaultFlowLimits, "12 turns", 12},
		"40 actions": {[]Turn{five, five, five, five, five, five, five, five, five, five}, FlowLimits{Turns: 12, Actions: 40, Time: time.Minute},
			"40 actions", 40},
	} {
		t.Run(name, func(t *testing.T) {
			m := &scriptedModel{turns: tc.turns}
			b := &scriptedBrowser{}
			end := runFlow(t, m, b, tc.lim)
			if end.Done != nil || !strings.Contains(end.Manual, tc.want) || len(b.acted) != tc.acted {
				t.Errorf("end = %+v, acted %d, want %d and %q", end, len(b.acted), tc.acted, tc.want)
			}
		})
	}
}

func TestAFlowThatTakesTooLongIsAManualCheck(t *testing.T) {
	slow := func(ctx context.Context, _ TurnInput) (Turn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	end, err := RunFlow(t.Context(), FlowLimits{Turns: 12, Actions: 40, Time: 30 * time.Millisecond}, "c", slow, &scriptedBrowser{})
	if err != nil || end.Done != nil || !strings.Contains(end.Manual, "after 30ms") {
		t.Errorf("end %+v, err %v", end, err)
	}
	if got := timeWords(3 * time.Minute); got != "3 minutes" {
		t.Errorf("3 minutes are written %q", got)
	}
}

func TestACancelStopsTheFlowWithAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := RunFlow(ctx, DefaultFlowLimits, "c", (&scriptedModel{}).turn, &scriptedBrowser{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestAModelThatGivesNoUsableAnswerOrIsOverTheLimitEndsTheFlow(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"no turn":    {&NoTurnError{Reason: "the answer is not in the schema"}, "the answer is not in the schema"},
		"cost limit": {ErrCostLimit, "cost limit"},
	} {
		t.Run(name, func(t *testing.T) {
			end := runFlow(t, &scriptedModel{err: tc.err}, &scriptedBrowser{}, DefaultFlowLimits)
			if end.Done != nil || !strings.Contains(end.Manual, tc.want) {
				t.Errorf("end = %+v", end)
			}
		})
	}
	boom := errors.New("the provider failed")
	if _, err := RunFlow(t.Context(), DefaultFlowLimits, "c", (&scriptedModel{err: boom}).turn, &scriptedBrowser{}); !errors.Is(err, boom) {
		t.Errorf("a provider error must stop the plan: %v", err)
	}
}

func TestAPageThatCannotBeReadEndsTheFlow(t *testing.T) {
	end := runFlow(t, &scriptedModel{}, &scriptedBrowser{listErr: errors.New("the browser is gone")}, DefaultFlowLimits)
	if end.Done != nil || !strings.Contains(end.Manual, "the browser is gone") {
		t.Errorf("end = %+v", end)
	}
}

func checkOf(expect string) Action { return Action{Kind: ActionCheck, Ref: "e3", Expect: expect} }

// A text that differs from the page is the model's mistake more often than a defect, and it would be
// written as a test that fails: the model is told what the element shows before anything is recorded.
func TestACheckThatDiffersFromThePageIsNotRecordedUntilTheModelHasSeenWhatThePageShows(t *testing.T) {
	m := &scriptedModel{turns: []Turn{act(checkOf("Stored"), clickSave), act(checkOf("Saved")), finished}}
	b := &scriptedBrowser{shown: map[string]string{"e3": "Saved"}}
	end := runFlow(t, m, b, DefaultFlowLimits)
	if end.Manual != "" || len(end.Checks) != 1 || end.Checks[0] != (FlowCheck{Ref: "e3", Expect: "Saved", Shown: "Saved"}) {
		t.Fatalf("end = %+v", end)
	}
	if len(b.acted) != 1 {
		t.Errorf("the browser did %v: the first check and the click after it must not be done", b.acted)
	}
	note := m.asked[1].Note
	for _, want := range []string{`"Stored"`, `"Saved"`, "Nothing was recorded", "click e2"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note lacks %s: %s", want, note)
		}
	}
}

func TestACheckThatDiffersIsRecordedWhenTheModelSendsItAgain(t *testing.T) {
	m := &scriptedModel{turns: []Turn{act(checkOf("Stored")), act(checkOf("Stored")), finished}}
	b := &scriptedBrowser{shown: map[string]string{"e3": "Saved"}}
	end := runFlow(t, m, b, DefaultFlowLimits)
	if end.Manual != "" || len(end.Checks) != 1 || end.Checks[0] != (FlowCheck{Ref: "e3", Expect: "Stored", Shown: "Saved"}) {
		t.Fatalf("end = %+v", end)
	}
	if m.asked[2].Note != "" {
		t.Errorf("the model was asked twice: %s", m.asked[2].Note)
	}
}

// The texts of the manual check that code writes must pass the limits that a draft is held to: a
// text that is one character too long made the plan refuse its own draft.
func TestTheManualCheckOfAnUnprovedPartFitsTheLimitsOfADraft(t *testing.T) {
	long := strings.Repeat("the order after a reload ", 40)
	m := restOfManual(ManualCheckDraft{Ticket: "SHOP-482", Surface: "web", Cite: "docs/tickets/SHOP-482.md:11 x"}, strings.Repeat("A long title ", 40), oneLine(long, maxManualText))
	if n := len([]rune(m.Title)); n > maxManualTitle {
		t.Errorf("the title has %d characters, and the limit is %d", n, maxManualTitle)
	}
	if n := len([]rune(m.Expected)); n > maxManualText {
		t.Errorf("the expected result has %d characters, and the limit is %d", n, maxManualText)
	}
	for _, s := range m.Steps {
		if n := len([]rune(s)); n > maxManualText {
			t.Errorf("a step has %d characters, and the limit is %d", n, maxManualText)
		}
	}
}
