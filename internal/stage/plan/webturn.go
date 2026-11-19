package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/datevar"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/pagelist"
	"github.com/jancuk/semar/internal/prompt"
)

// The kinds of answer to a turn.
const (
	turnAct    = "act"
	turnDone   = "done"
	turnManual = "manual"
	turnGiven  = "given"
)

const (
	maxTurnActions = 5
	runIDVariable  = "{{run.id}}"
)

var aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// turnWire is the JSON of a turn: one object whose kind says which of its fields hold the answer.
type turnWire struct {
	Kind     string        `json:"kind"`
	Actions  []actionWire  `json:"actions,omitempty"`
	Title    string        `json:"title,omitempty"`
	Intent   string        `json:"intent,omitempty"`
	Unproved string        `json:"unproved,omitempty"`
	Reason   string        `json:"reason,omitempty"`
	Given    []AnswerGiven `json:"given,omitempty"`
}

type actionWire struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Text   string `json:"text,omitempty"`
	Key    string `json:"key,omitempty"`
	To     string `json:"to,omitempty"`
	Expect string `json:"expect,omitempty"`
}

// DecodeTurn reads the output of a job strictly: an unknown field and data after the object are
// errors, and an unknown kind is one. What the page and the catalogue can say is checked against them,
// and each thing that is wrong comes back as a problem in plain words. The turn is only meaningful
// when there is no problem.
func DecodeTurn(data []byte, page pagelist.List, first bool, catalogue []fixture.Entry) (Turn, []Problem, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var e struct {
		Turn turnWire `json:"turn"`
	}
	if err := dec.Decode(&e); err != nil {
		return nil, nil, err
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New("data follows the answer object")
	}
	var ps problems
	w := e.Turn
	switch w.Kind {
	case turnAct:
		return actTurn(w, page, &ps), ps.list, nil
	case turnDone:
		if blank(w.Title) {
			ps.add("turn.title", "a done answer needs a title that names the requirement")
		}
		if blank(w.Intent) {
			ps.add("turn.intent", "a done answer needs an intent: what was true before the change and what is true now")
		}
		return TurnDone{Title: w.Title, Intent: w.Intent, Unproved: strings.TrimSpace(w.Unproved)}, ps.list, nil
	case turnManual:
		if blank(w.Reason) {
			ps.add("turn.reason", "a manual answer needs the reason, in one sentence")
		}
		return TurnManual{Reason: w.Reason}, ps.list, nil
	case turnGiven:
		if !first {
			ps.add("turn.kind", "given is allowed in the first answer only")
		}
		checkGiven(w.Given, catalogue, &ps)
		return TurnGiven{Given: w.Given}, ps.list, nil
	}
	return nil, nil, fmt.Errorf("the turn kind %q must be one of %s, %s, %s, %s", w.Kind, turnAct, turnDone, turnManual, turnGiven)
}

func actTurn(w turnWire, page pagelist.List, ps *problems) Turn {
	n := len(w.Actions)
	if n < 1 || n > maxTurnActions {
		ps.add("turn.actions", "a turn has 1 to %d actions, and this one has %d", maxTurnActions, n)
	}
	var out []Action
	for i, a := range w.Actions {
		path := fmt.Sprintf("turn.actions[%d]", i)
		act := Action{Kind: ActionKind(a.Kind), Ref: a.Ref, Text: a.Text, Key: a.Key, To: a.To, Expect: a.Expect}
		ref := func(field, r string, optional bool) {
			if _, ok := page.Find(r); !ok && !(optional && r == "") {
				ps.add(path+"."+field, "%s is not in the list of the page: use a reference of the list", r)
			}
		}
		switch act.Kind {
		case ActionClick, ActionHover, ActionTick, ActionUntick:
			ref("ref", a.Ref, false)
		case ActionSelect:
			ref("ref", a.Ref, false)
			if blank(a.Text) {
				ps.add(path+".text", "a select needs the label of the option to choose")
			} else {
				checkTextVariables(path+".text", "the text", a.Text, ps)
			}
		case ActionFill:
			ref("ref", a.Ref, false)
			switch {
			case a.Text == "":
				ps.add(path+".text", "a fill needs the text to type")
			default:
				checkTextVariables(path+".text", "the text", a.Text, ps)
			}
		case ActionPress:
			ref("ref", a.Ref, true)
			if a.Key != "Enter" && a.Key != "Escape" {
				ps.add(path+".key", "a press is Enter or Escape, and this one is %q", a.Key)
			}
		case ActionDrag:
			ref("ref", a.Ref, false)
			ref("to", a.To, false)
			if a.Ref == a.To {
				ps.add(path+".to", "the element cannot be dragged onto itself")
			}
		case ActionCheck:
			ref("ref", a.Ref, false)
			if blank(a.Expect) {
				ps.add(path+".expect", "a check needs expect, the text that the element must show")
			} else {
				checkTextVariables(path+".expect", "expect", a.Expect, ps)
			}
		default:
			ps.add(path+".kind", "the action kind %q must be one of click, fill, select, hover, tick, untick, press, drag, check", a.Kind)
		}
		out = append(out, act)
	}
	return TurnAct{Actions: out}
}

var textVariable = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*)\}\}`)

// checkTextVariables refuses a "{{" in a text of a page that is not {{run.id}} or a date variable.
// The program fills in the text that the page shows or takes, so no other variable has a value there.
func checkTextVariables(path, what string, text string, ps *problems) {
	for _, m := range textVariable.FindAllStringSubmatch(text, -1) {
		if m[1] != "run.id" && !datevar.Is(m[1]) {
			ps.add(path, "%s holds %s, and %s and the date variables are the only variables that a text may hold", what, m[0], runIDVariable)
			return
		}
	}
	if strings.Contains(textVariable.ReplaceAllString(text, ""), "{{") {
		ps.add(path, "%s holds {{ that is not a variable, and %s and the date variables are the only variables that a text may hold", what, runIDVariable)
	}
}

// turnDates refuses a date near today that an action types or expects, as the rule of D7 does for a
// request. A text of a page shows words around a date, so a date at the start of a word counts.
func turnDates(t Turn, rule dateRule, ticket string, ps *problems) {
	act, ok := t.(TurnAct)
	if !ok || !rule.on {
		return
	}
	for i, a := range act.Actions {
		path := fmt.Sprintf("turn.actions[%d]", i)
		switch a.Kind {
		case ActionFill:
			if msg := rule.problemInWords(a.Text, ticket); msg != "" {
				ps.add(path+".text", "the text %s", msg)
			}
		case ActionCheck:
			if msg := rule.problemInWords(a.Expect, ticket); msg != "" {
				ps.add(path+".expect", "expect %s", msg)
			}
		}
	}
}

// checkGiven applies the rules of a given list of a scenario to the fixtures of a flow: each fixture is
// in the catalogue, each required parameter is given, and the name of what it gives is one word.
func checkGiven(given []AnswerGiven, catalogue []fixture.Entry, ps *problems) {
	if len(given) == 0 {
		ps.add("turn.given", "name at least one fixture of the catalogue, or answer another kind")
	}
	seen := map[string]bool{}
	for i, g := range given {
		path := fmt.Sprintf("turn.given[%d]", i)
		j := slices.IndexFunc(catalogue, func(e fixture.Entry) bool { return string(e.ID) == g.Fixture })
		if j < 0 {
			ps.add(path+".fixture", "the fixture %q is not in the catalogue", g.Fixture)
			continue
		}
		if !aliasPattern.MatchString(g.As) || seen[g.As] {
			ps.add(path+".as", "name what the fixture gives with one lower case word, each different, such as board")
		}
		seen[g.As] = true
		given := map[string]bool{}
		for _, w := range g.With {
			if _, ok := w.Value.(string); !ok {
				ps.add(path+".with", "the value of the fixture parameter %s must be a string", w.Name)
			}
			given[w.Name] = true
		}
		for _, p := range catalogue[j].Params {
			if p.Required && !given[p.Name] {
				ps.add(path+".with", "the fixture %s needs the parameter %s", g.Fixture, p.Name)
			}
		}
	}
}

// TurnSchema is the JSON Schema of the answer to one turn. It is closed like the schema of a
// criterion: the kind is one of act, done, and manual, and given when the turn is the first and the
// catalogue has a fixture. An action names an element by a reference of the page list, so a model
// cannot name one that is not there.
func TurnSchema(page pagelist.List, first bool, catalogue []fixture.Entry) json.RawMessage {
	refs := make([]string, len(page.Elements))
	for i, e := range page.Elements {
		refs[i] = e.Ref
	}
	ref := text("An element of the page list, such as e3.")
	if len(refs) > 0 {
		ref = enum("string", refs...)
		ref["description"] = "An element of the page list."
	}
	pressRef := map[string]any{"description": "The field to press the key in, or an empty text to press it on the page.", "type": "string", "enum": append([]any{""}, anyStrings(refs)...)}
	actions := []any{
		object(map[string]any{"kind": enum("string", "click"), "ref": ref}),
		object(map[string]any{"kind": enum("string", "fill"), "ref": ref, "text": text("The text to type. It replaces what the field holds. {{run.id}} is the ID of the run. For a date field write {{date.today}}, {{date.plus7d}}, or {{date.minus3d}}, and for a datetime-local field {{local.now}}, {{local.plus2h}}, or {{local.minus1d}}, and never type a date that is near today.")}),
		object(map[string]any{"kind": enum("string", "press"), "ref": pressRef, "key": enum("string", "Enter", "Escape")}),
		object(map[string]any{"kind": enum("string", "drag"), "ref": ref, "to": ref}),
		object(map[string]any{"kind": enum("string", "select"), "ref": ref, "text": text("The label of the option to choose, as the list of the element shows it in options.")}),
		object(map[string]any{"kind": enum("string", "hover"), "ref": ref}),
		object(map[string]any{"kind": enum("string", "tick"), "ref": ref}),
		object(map[string]any{"kind": enum("string", "untick"), "ref": ref}),
		object(map[string]any{"kind": enum("string", "check"), "ref": ref, "expect": text("The text that the element must show, written from the criterion. {{run.id}} and the date variables are filled in as in a text.")}),
	}
	branches := []any{
		object(map[string]any{"kind": enum("string", turnAct), "actions": array(map[string]any{"anyOf": actions}, "1 to 5 actions, done in order.")}),
		object(map[string]any{"kind": enum("string", turnDone), "title": text("A short title that names the requirement."), "intent": text("Two sentences: what was true before the change and what is true now."), "unproved": text("The part of the criterion that your checks did not prove, in one sentence, or an empty text when they prove all of it.")}),
		object(map[string]any{"kind": enum("string", turnManual), "reason": text("Why a person must do the check, in one sentence.")}),
	}
	if first && len(catalogue) > 0 {
		shim := Packet{Fixtures: catalogue}
		branches = append(branches, object(map[string]any{"kind": enum("string", turnGiven), "given": array(shim.givenSchema(), "The fixtures that prepare the data.")}))
	}
	root := object(map[string]any{"turn": map[string]any{"anyOf": branches}})
	data, err := json.Marshal(root)
	if err != nil {
		panic(fmt.Sprintf("encode the turn schema: %v", err)) // the schema holds strings and maps
	}
	return data
}

func anyStrings(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// Prompt is the text of the job of one turn. With a previous output and problems it is a correction.
func (in TurnInput) Prompt(previous []byte, problems []Problem, catalogue []fixture.Entry, maxTurns int) (string, error) {
	d := prompt.WebData{
		Criterion: fenced("text", in.Criterion),
		Reading:   in.Reading,
		Page:      fenced("text", in.Page.String()),
		Note:      in.Note,
		Turn:      in.Number,
		MaxTurns:  maxTurns,
	}
	if in.First && len(catalogue) > 0 {
		d.Fixtures = fixtureList(catalogue)
	}
	var data []string
	for _, g := range in.Given {
		data = append(data, fmt.Sprintf("- %s is %s.", code(g.Alias+"."+g.Name), code(g.Value)))
	}
	d.Data = strings.Join(data, "\n")
	var done []string
	for i, line := range in.Done {
		done = append(done, strconv.Itoa(i+1)+". "+oneLine(line, maxProblemRunes))
	}
	d.Done = strings.Join(done, "\n")
	if len(problems) > 0 {
		d.Correction = true
		d.Problems = problemList(problems)
		if len(previous) > 0 {
			d.Previous = fenced("json", string(previous))
		} else {
			d.Previous = "The output was empty."
		}
	}
	return prompt.Web(d)
}

// turnModel is the model of a flow at one rung of the ladder. A turn is one job with no tools, run by
// the same function as the jobs of the criteria, so it counts in the model use and in the cost limit. An
// answer with a problem goes back once to the same model with the problems in plain words, and then the
// flow ends as a manual check.
func (rs *runState) turnModel(rung int, catalogue []fixture.Entry, maxTurns int) FlowModel {
	return func(ctx context.Context, in TurnInput) (Turn, error) {
		var previous []byte
		var ps []Problem
		for attempt := range 2 {
			if rs.overLimit() {
				return nil, ErrCostLimit
			}
			text, err := in.Prompt(previous, ps, catalogue, maxTurns)
			if err != nil {
				return nil, err
			}
			raw, soft, err := rs.send(ctx, rung, text, TurnSchema(in.Page, in.First, catalogue), attempt > 0, true)
			if err != nil {
				return nil, err
			}
			previous = raw
			if soft != "" {
				ps = []Problem{{Message: "the job returned no answer in the schema: " + soft}}
				continue
			}
			turn, found, err := DecodeTurn(raw, in.Page, in.First, catalogue)
			if err == nil && len(found) == 0 {
				var dated problems
				turnDates(turn, newDateRule(rs.in.Now), ticketText(rs.in.Tickets, in.Ticket), &dated)
				found = dated.list
			}
			switch {
			case err != nil:
				ps = []Problem{{Message: "the output is not an answer in the schema: " + err.Error()}}
			case len(found) > 0:
				ps = found
			default:
				return turn, nil
			}
		}
		return nil, &NoTurnError{Reason: "the model gave no answer in the schema, also after a correction: " + firstProblem(ps)}
	}
}
