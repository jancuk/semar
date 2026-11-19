package plan

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/pagelist"
)

// A web flow is a model in the seat of the person of semar record. The model reads the page as a list
// of references, answers a turn with a few actions on references, and a browser does them. The rules
// of the flow, which are its limits, "given first only", and the stop at the first action that cannot be
// done, are all in RunFlow.

// ActionKind is what one action of a turn does.
type ActionKind string

const (
	ActionClick ActionKind = "click"
	ActionFill  ActionKind = "fill"
	ActionPress ActionKind = "press"
	ActionDrag  ActionKind = "drag"
	ActionCheck ActionKind = "check"
	// ActionSelect chooses the option of a select list by its label, which is Text. ActionHover holds the
	// pointer over an element. ActionTick and ActionUntick check and clear a box.
	ActionSelect ActionKind = "select"
	ActionHover  ActionKind = "hover"
	ActionTick   ActionKind = "tick"
	ActionUntick ActionKind = "untick"
	// The checks of a page that read more than the text of an element. ActionAbsent says that the element
	// is not on the page, ActionCount how many elements look like it, ActionState that it is hidden,
	// shown, enabled, disabled, checked, or not checked, ActionAttribute what one of its attributes holds,
	// and ActionPage what the path or the title of the page is.
	ActionAbsent    ActionKind = "absent"
	ActionCount     ActionKind = "count"
	ActionState     ActionKind = "state"
	ActionAttribute ActionKind = "attribute"
	ActionPage      ActionKind = "page"
)

// Action is one thing that a turn does, on an element of the page list. Only the fields of its kind
// are set: Text for a fill, Key for a press, To for a drag, and Expect for a check.
type Action struct {
	Kind   ActionKind
	Ref    string
	Text   string
	Key    string
	To     string
	Expect string
	// State, Attribute, Match, Page, and Count are the fields of the checks that read more than a text:
	// the state of an element, the attribute that it holds with Expect and Match (equals or contains),
	// path or title of the page with Expect and Match, and how many elements there are.
	State     string
	Attribute string
	Match     string
	Page      string
	Count     int
}

// Checks of the match of an attribute and of the page.
const (
	MatchEquals   = "equals"
	MatchContains = "contains"
)

// IsCheck says that the action reads the page and records what it must show, and does nothing to it.
func (a Action) IsCheck() bool {
	switch a.Kind {
	case ActionCheck, ActionAbsent, ActionCount, ActionState, ActionAttribute, ActionPage:
		return true
	}
	return false
}

// Claim is the check that the action asks for, as the engine of a run evaluates it. Its Element is the
// reference of the list, which the recording replaces by the name of the element.
func (a Action) Claim() domain.Check {
	c := domain.Check{Element: a.Ref}
	var operand any = a.Expect
	switch a.Kind {
	case ActionAbsent:
		c.Exists = new(bool)
		return c
	case ActionCount:
		c.Count = &a.Count
		return c
	case ActionState:
		c.State = a.State
		return c
	case ActionPage:
		c.Element, c.Page = "", a.Page
	case ActionAttribute:
		c.Attribute = a.Attribute
	}
	if a.Match == MatchContains {
		c.Contains = operand
	} else {
		c.Equals = operand
	}
	return c
}

// String is the action as the history of a flow shows it, with no value of the page.
func (a Action) String() string {
	switch a.Kind {
	case ActionFill:
		return fmt.Sprintf("fill %s with %s", a.Ref, strconv.Quote(a.Text))
	case ActionPress:
		if a.Ref == "" {
			return "press " + a.Key
		}
		return fmt.Sprintf("press %s in %s", a.Key, a.Ref)
	case ActionDrag:
		return fmt.Sprintf("drag %s to %s", a.Ref, a.To)
	case ActionCheck:
		return fmt.Sprintf("check %s equals %s", a.Ref, strconv.Quote(a.Expect))
	case ActionSelect:
		return fmt.Sprintf("select %s in %s", strconv.Quote(a.Text), a.Ref)
	case ActionAbsent:
		return fmt.Sprintf("check %s is not on the page", a.Ref)
	case ActionCount:
		return fmt.Sprintf("check there are %d elements like %s", a.Count, a.Ref)
	case ActionState:
		return fmt.Sprintf("check %s %s", a.Ref, domain.ElementStates[a.State])
	case ActionAttribute:
		return fmt.Sprintf("check the %s of %s %s %s", a.Attribute, a.Ref, a.Match, strconv.Quote(a.Expect))
	case ActionPage:
		return fmt.Sprintf("check the page %s %s %s", a.Page, a.Match, strconv.Quote(a.Expect))
	}
	return string(a.Kind) + " " + a.Ref
}

// Turn is the answer of a model to one turn. It is one of TurnAct, TurnDone, TurnManual, and
// TurnGiven, and no other type can be one.
type Turn interface{ isTurn() }

// TurnAct does 1 to 5 actions, in order.
type TurnAct struct{ Actions []Action }

// TurnDone says that the flow proved the criterion, or the part of it that the page shows. Title and
// Intent are those of the written test. Unproved is what the checks did not prove, or empty: a person
// still checks that.
type TurnDone struct{ Title, Intent, Unproved string }

// TurnManual says that a person must do the check, and why.
type TurnManual struct{ Reason string }

// TurnGiven names fixtures that prepare data. It is allowed in the first answer only.
type TurnGiven struct{ Given []AnswerGiven }

func (TurnAct) isTurn()    {}
func (TurnDone) isTurn()   {}
func (TurnManual) isTurn() {}
func (TurnGiven) isTurn()  {}

// GivenOutput is one value that a fixture of the flow gave.
type GivenOutput struct{ Alias, Name, Value string }

// TurnInput is what the model is told in one turn.
type TurnInput struct {
	Criterion string
	// Ticket is the key of the ticket of the criterion: its file may name a date on purpose.
	Ticket string
	// Reading is what a first job wrote for a person to do, as a help.
	Reading string
	Number  int  // of this turn, from 1
	First   bool // given may be answered
	Page    pagelist.List
	Done    []string // the actions that were done so far, one line each
	Note    string   // what went wrong with the last answer: an action that could not be done, or a refusal
	Given   []GivenOutput
	Checks  int // the checks recorded so far
}

// FlowModel answers a turn. An error that is a *NoTurnError or ErrCostLimit ends the flow as a manual
// check, and any other error ends the plan.
type FlowModel func(ctx context.Context, in TurnInput) (Turn, error)

// FlowBrowser is the browser of one flow, signed in and recording. The plan stage does not know
// Chrome: the command that runs the plan gives this.
type FlowBrowser interface {
	// List reads the page as it is now. The references of the last list are valid until the next.
	List(ctx context.Context) (pagelist.List, error)
	// Act does one action as a person does it. For a check it returns what the element shows.
	Act(ctx context.Context, a Action) (shown string, err error)
	// Prepare reads, from the list that is current, what the checks of a turn that read more than a text
	// need of their elements, before an action of the turn takes an element off the page. A check that an
	// element is gone names an element that the last action removed.
	Prepare(ctx context.Context, checks []Action)
	// Compare decides a check with no check recorded, and says whether it holds. For a text it reads what
	// the element shows and compares it with the expected text, where the ID of the run and its variable
	// are the same text. For another form shown is what the page showed instead. An error says why the
	// check cannot be decided, such as a locator that matches several elements.
	Compare(ctx context.Context, a Action) (shown string, same bool, err error)
	// Given makes the fixtures, and loads the first page again.
	Given(ctx context.Context, given []AnswerGiven) ([]GivenOutput, error)
}

// NoTurnError says that a model gave no answer that a turn can use, even after a correction.
type NoTurnError struct{ Reason string }

func (e *NoTurnError) Error() string { return e.Reason }

// ErrCostLimit says that the cost limit refused the job of a turn.
var ErrCostLimit = errors.New("the cost limit was reached")

// FlowLimits bound one flow. A flow that reaches one ends as a manual check.
type FlowLimits struct {
	Turns   int
	Actions int
	Time    time.Duration
}

// DefaultFlowLimits are the limits of D8: 12 turns, 40 actions, and 3 minutes.
var DefaultFlowLimits = FlowLimits{Turns: 12, Actions: 40, Time: 3 * time.Minute}

// FlowCheck is one check that a flow made: what the model expected, which comes from the criterion,
// and what the element showed. Check is the claim of a check that reads more than a text, and Expect is
// then empty.
type FlowCheck struct {
	Ref, Expect, Shown string
	Check              *domain.Check
}

// FlowEnd is how a flow ended. Exactly one of Done and Manual is set.
type FlowEnd struct {
	Done    *TurnDone
	Manual  string // why a person must do the check
	Given   []AnswerGiven
	Outputs []GivenOutput
	Checks  []FlowCheck
	Turns   int
	Actions int
}

// RunFlow plays the turns of one flow. It lists the page, asks the model, and does what the model
// answers, until the model is done, says manual, or reaches a limit. A flow ends as done only when
// it made a check. The rules are these:
//
//   - given is allowed in the first answer only, and a later one is refused with a note;
//   - the actions of a turn stop at the first that cannot be done, and the next turn is told which
//     and why;
//   - a failed action counts as an action, and so does a failed check count as no check.
func RunFlow(ctx context.Context, lim FlowLimits, criterion string, model FlowModel, br FlowBrowser) (FlowEnd, error) {
	return playFlow(ctx, lim, TurnInput{Criterion: criterion}, model, br)
}

func playFlow(ctx context.Context, lim FlowLimits, in TurnInput, model FlowModel, br FlowBrowser) (FlowEnd, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, lim.Time)
	defer cancel()
	var end FlowEnd
	// doubted holds the checks that the model was told differ from the page, so that each is asked once.
	doubted := map[string]bool{}
	// over says whether the flow must stop now because of the clock or the caller. A cancel of the
	// caller is an error of the plan, and the time limit is a manual check.
	over := func() (bool, error) {
		switch {
		case parent.Err() != nil:
			return true, parent.Err()
		case ctx.Err() != nil:
			end.Manual = "the flow was not finished after " + timeWords(lim.Time)
			return true, nil
		}
		return false, nil
	}
	manual := func(format string, args ...any) (FlowEnd, error) {
		end.Manual = fmt.Sprintf(format, args...)
		return end, nil
	}
	in.First = true
	for {
		if stop, err := over(); stop {
			return end, err
		}
		if end.Turns >= lim.Turns {
			return manual("the model did not finish after %d turns", lim.Turns)
		}
		page, err := br.List(ctx)
		if err != nil {
			if stop, perr := over(); stop {
				return end, perr
			}
			return manual("the page could not be read: %s", oneLine(err.Error(), maxProblemRunes))
		}
		end.Turns++
		in.Number, in.Page, in.Checks = end.Turns, page, len(end.Checks)
		turn, err := model(ctx, in)
		in.Note = ""
		var noTurn *NoTurnError
		switch {
		case errors.As(err, &noTurn):
			return manual("%s", noTurn.Reason)
		case errors.Is(err, ErrCostLimit):
			return manual("not explored: the cost limit was reached")
		case err != nil:
			if stop, perr := over(); stop {
				return end, perr
			}
			return end, err
		}
		switch t := turn.(type) {
		case TurnManual:
			return manual("%s", t.Reason)
		case TurnDone:
			if len(end.Checks) == 0 {
				return manual("the model was done and no check was recorded, so nothing proves the criterion")
			}
			end.Done = &t
			return end, nil
		case TurnGiven:
			if !in.First {
				in.Note = "given is allowed in the first answer only, so nothing was made. Do the flow with the data that the page has."
				continue
			}
			out, err := br.Given(ctx, t.Given)
			if err != nil {
				if stop, perr := over(); stop {
					return end, perr
				}
				return manual("the fixtures could not be made: %s", oneLine(err.Error(), maxProblemRunes))
			}
			end.Given, end.Outputs, in.Given, in.First = t.Given, out, out, false
		case TurnAct:
			in.First = false
			var forms []Action
			for _, a := range t.Actions {
				if a.IsCheck() && a.Kind != ActionCheck {
					forms = append(forms, a)
				}
			}
			if len(forms) > 0 {
				br.Prepare(ctx, forms)
			}
			for i, a := range t.Actions {
				if end.Actions >= lim.Actions {
					return manual("the model did not finish after %d actions", lim.Actions)
				}
				end.Actions++
				if a.IsCheck() {
					// An element that cannot be read is left to Act, which says why.
					key := a.String() + "\x00"
					if a.Kind == ActionCheck {
						key = a.Expect + "\x00"
					}
					if shown, same, err := br.Compare(ctx, a); err == nil && !same && !doubted[key+shown] {
						doubted[key+shown] = true
						in.Note = doubtNote(i+1, t.Actions, shown)
						in.Done = append(in.Done, a.String()+" (not recorded: "+doubtWhy(a)+")")
						break
					}
				}
				shown, err := br.Act(ctx, a)
				if err != nil {
					if stop, perr := over(); stop {
						return end, perr
					}
					in.Note = failedNote(i+1, t.Actions, err)
					in.Done = append(in.Done, a.String()+" (it could not be done)")
					break
				}
				if a.IsCheck() {
					fc := FlowCheck{Ref: a.Ref, Expect: a.Expect, Shown: shown}
					if a.Kind != ActionCheck {
						claim := a.Claim()
						fc.Expect, fc.Check = "", &claim
					}
					end.Checks = append(end.Checks, fc)
				}
				in.Done = append(in.Done, a.String())
			}
		}
	}
}

// doubtNote tells the model that its check expects a text that the element does not show, before
// the check is recorded. A check that differs is written as a test that fails now, so the model
// sends it again only when the page is what is wrong.
func doubtNote(n int, actions []Action, shown string) string {
	a := actions[n-1]
	var b strings.Builder
	if a.Kind == ActionCheck {
		fmt.Fprintf(&b, "Action %d of your last answer, %s, expects %q, and the element shows %q. Nothing was recorded.", n, a, a.Expect, oneLine(shown, maxProblemRunes))
		b.WriteString(" A check whose text differs from the page is written as a test that fails now. That is right only when the criterion requires your text and the page is wrong: then send the same check again. When your text was wrong, send the check with the text that the criterion requires.")
	} else {
		fmt.Fprintf(&b, "Action %d of your last answer, %s, does not hold: the page shows %s. Nothing was recorded.", n, a, oneLine(shown, maxProblemRunes))
		b.WriteString(" A check that the page does not meet is written as a test that fails now. That is right only when the criterion requires it and the page is wrong: then send the same check again. When your check was wrong, send the check that the criterion requires.")
	}
	b.WriteString(notDone(actions[n:]))
	return b.String()
}

// doubtWhy is the words after "not recorded" in the history of a flow, for a check that the page does not meet.
func doubtWhy(a Action) string {
	if a.Kind == ActionCheck {
		return "the element shows other text"
	}
	return "the page does not meet it"
}

// notDone lists the actions of a turn that were left out.
func notDone(rest []Action) string {
	if len(rest) == 0 {
		return ""
	}
	list := make([]string, len(rest))
	for i, a := range rest {
		list[i] = a.String()
	}
	return " The actions after it were not done: " + strings.Join(list, "; ") + "."
}

// failedNote tells the model which action of its turn failed and why, and which were not done.
func failedNote(n int, actions []Action, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Action %d of your last answer, %s, could not be done: %s.", n, actions[n-1], strings.TrimRight(oneLine(err.Error(), maxProblemRunes), "."))
	b.WriteString(notDone(actions[n:]))
	return b.String()
}

// timeWords writes a limit of time for a person: 3 minutes, 30s.
func timeWords(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return plural(int(d/time.Minute), "minute")
	}
	return d.String()
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
