package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/jancuk/semar/internal/datevar"
	"github.com/jancuk/semar/internal/engine/web/webinput"
	"github.com/jancuk/semar/internal/pagelist"
	"github.com/jancuk/semar/internal/script"
)

// pageListJS lists the elements of the page that a person can act on or read, and keeps them in
// window.__semarRefs, in the order that it returns them, so that a reference of the list can be
// turned back into an element. It reads no value of a password field.
//
//go:embed pagelist.js
var pageListJS string

// Explorer reads the page of a recorder's tab as a list of references and acts on a reference as a
// person acts on what they see: with the mouse and the keyboard of the browser, so that the recorder
// hears the same events as it hears from a person.
type Explorer struct {
	// RunID is typed for {{run.id}} in a text. The recorder then hears the ID, and the test that is
	// written keeps the variable.
	RunID string

	rec  *Recorder
	mu   sync.Mutex
	list pagelist.List
	// known holds what Prepare read of the elements of the last list that checks name.
	known map[string]probe
	// used holds the date variables that a text has had filled in, with the text each gave.
	used map[string]string
}

// NewExplorer starts to explore the tab of a recorder.
func NewExplorer(rec *Recorder) *Explorer { return &Explorer{rec: rec} }

// List reads the page as it is now. The references of an earlier list stop to be valid.
func (x *Explorer) List(ctx context.Context) (pagelist.List, error) {
	var raw struct {
		URL   string          `json:"url"`
		Title string          `json:"title"`
		Items []pagelist.Item `json:"items"`
	}
	text, err := chromedp.Run(x.rec.tab, chromedp.Evaluate[string](pageListJS))
	if err != nil {
		return pagelist.List{}, fmt.Errorf("the page could not be read: %w", err)
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return pagelist.List{}, errors.New("the page list is not readable")
	}
	l := pagelist.Build(raw.URL, raw.Title, raw.Items)
	x.mu.Lock()
	x.list = l
	x.known = nil
	x.mu.Unlock()
	return l, nil
}

const (
	// checkHeard is how long a check waits for the recorder to hear the Option-click.
	checkHeard = 3 * time.Second
)

// found is one element of the last list, found again on the page.
type found struct {
	rd   *reader
	id   cdp.BackendNodeID
	obj  runtime.RemoteObjectID
	name string // for an error: role and name as the list showed them
}

func (t found) release() { t.rd.release() }

const stateJS = `function(){
  const aria = this.getAttribute && this.getAttribute('aria-disabled');
  return { connected: this.isConnected, disabled: this.disabled === true || aria === 'true' };
}`

func (x *Explorer) find(ref string) (found, error) { return x.locate(ref, false) }

// locate finds an element of the last list again. An action refuses an element that is disabled, and a
// check that reads one does not.
func (x *Explorer) locate(ref string, allowDisabled bool) (found, error) {
	x.mu.Lock()
	e, ok := x.list.Find(ref)
	x.mu.Unlock()
	if !ok {
		return found{}, fmt.Errorf("%s is not in the last list: use a reference of the list", ref)
	}
	name := fmt.Sprintf("%s (%s %q)", ref, e.Role, e.Name)
	tab := x.rec.tab
	res, err := chromedp.Call(tab, runtime.Evaluate, runtime.EvaluateParams{
		Expression: "(window.__semarRefs || [])[" + strconv.Itoa(e.Index) + "]", ObjectGroup: objectGroup,
	})
	if err != nil {
		return found{}, err
	}
	gone := fmt.Errorf("%s is no longer on the page: the page changed since the list", name)
	if res.Result == nil || res.Result.ObjectID == "" {
		return found{}, gone
	}
	st, err := chromedp.Call(tab, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: stateJS, ObjectID: res.Result.ObjectID, ReturnByValue: ptr(true),
	})
	if err != nil {
		return found{}, err
	}
	var state struct{ Connected, Disabled bool }
	if st.Result == nil || json.Unmarshal(st.Result.Value, &state) != nil || !state.Connected {
		return found{}, gone
	}
	if state.Disabled && !allowDisabled {
		return found{}, fmt.Errorf("%s is disabled", name)
	}
	d, err := chromedp.Call(tab, dom.DescribeNode, dom.DescribeNodeParams{ObjectID: res.Result.ObjectID})
	if err != nil || d.Node == nil {
		return found{}, gone
	}
	return found{rd: newReader(tab, nil), id: d.Node.BackendNodeID, obj: res.Result.ObjectID, name: name}, nil
}

// Click presses the left button on the middle of the element, after it scrolls to it.
func (x *Explorer) Click(ctx context.Context, ref string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t, err := x.find(ref)
	if err != nil {
		return err
	}
	defer t.release()
	return t.rd.click(t.id)
}

var textVariable = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*)\}\}`)

// Expand fills in the variables of a text: {{run.id}} is the ID of the run, and a date variable is the
// day or the time that the time of the run gives. No other variable has a value. The date variables
// that it fills in are kept, for Variables.
func (x *Explorer) Expand(text string) (string, error) {
	if strings.Contains(textVariable.ReplaceAllString(text, ""), "{{") {
		return "", errors.New("the text holds {{ that is not a variable, and {{run.id}} and the date variables are the only variables that a text may hold")
	}
	var err error
	out := textVariable.ReplaceAllStringFunc(text, func(m string) string {
		name := textVariable.FindStringSubmatch(m)[1]
		if err != nil {
			return m
		}
		if name != "run.id" && !datevar.Is(name) {
			err = fmt.Errorf("the text holds %s, and {{run.id}} and the date variables are the only variables that a text may hold", m)
			return m
		}
		if x.RunID == "" {
			err = fmt.Errorf("the text holds %s, and this run has no ID", m)
			return m
		}
		val, _, rerr := script.Resolve(map[string]string{"run.id": x.RunID}, name)
		if rerr != nil {
			err = rerr
			return m
		}
		if name != "run.id" {
			x.mu.Lock()
			if x.used == nil {
				x.used = map[string]string{}
			}
			x.used[name] = val
			x.mu.Unlock()
		}
		return val
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// Variables are the date variables that texts have had filled in so far, with the text of each. A
// recording keeps such a text as its variable, so that the test it writes follows the run.
func (x *Explorer) Variables() map[string]string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return maps.Clone(x.used)
}

// Fill replaces what a field holds by typing the text at it. The variables of the text are filled in
// as Expand does.
func (x *Explorer) Fill(ctx context.Context, ref, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	text, err := x.Expand(text)
	if err != nil {
		return err
	}
	t, err := x.find(ref)
	if err != nil {
		return err
	}
	defer t.release()
	if kind := untypedKind(t.name); kind != "" {
		return fmt.Errorf("%s is a %s field, which a flow cannot type into", t.name, kind)
	}
	if err := t.rd.fill(t.id, text); err != nil {
		return fmt.Errorf("%s: %w", t.name, err)
	}
	// A value that is set by a script comes with events that the page marks as not from a person, so
	// the recorder does not hear it. It is asked to, as it would hear the person who picked the date.
	if _, picker, err := t.rd.temporalForm(t.obj); err == nil && picker {
		_, err := chromedp.Call(x.rec.tab, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
			FunctionDeclaration: `function(){ if (window.__semarHear) window.__semarHear(this); }`, ObjectID: t.obj,
		})
		return err
	}
	return nil
}

// hear asks the recorder to file an event of the page that a script did, as it files one of a person.
func (x *Explorer) hear(t found, hook string) error {
	_, err := chromedp.Call(x.rec.tab, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: `function(){ if (window.` + hook + `) window.` + hook + `(this); }`, ObjectID: t.obj,
	})
	return err
}

// Select chooses the option of a list by its label.
func (x *Explorer) Select(ctx context.Context, ref, label string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	label, err := x.Expand(label)
	if err != nil {
		return err
	}
	t, err := x.find(ref)
	if err != nil {
		return err
	}
	defer t.release()
	if err := t.rd.selectOption(t.id, label); err != nil {
		return fmt.Errorf("%s: %w", t.name, err)
	}
	return x.hear(t, "__semarSelect")
}

// Hover holds the pointer over the element.
func (x *Explorer) Hover(ctx context.Context, ref string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t, err := x.find(ref)
	if err != nil {
		return err
	}
	defer t.release()
	if err := t.rd.hover(t.id); err != nil {
		return fmt.Errorf("%s: %w", t.name, err)
	}
	return x.hear(t, "__semarHover")
}

// SetChecked checks or clears a box with a click. A box that is in the state already is not clicked, and
// the recording has nothing to say of it.
func (x *Explorer) SetChecked(ctx context.Context, ref string, want bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t, err := x.find(ref)
	if err != nil {
		return err
	}
	defer t.release()
	if err := t.rd.setChecked(t.id, want); err != nil {
		return fmt.Errorf("%s: %w", t.name, err)
	}
	return nil
}

// Press sends Enter or Escape, to the element when there is a reference, and to the page when there
// is none.
func (x *Explorer) Press(ctx context.Context, ref, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var code string
	switch key {
	case "Enter":
		code = kb.Enter
	case "Escape":
		code = kb.Escape
	default:
		return fmt.Errorf("the key %q is not one that a flow presses: use Enter or Escape", key)
	}
	if ref != "" {
		t, err := x.find(ref)
		if err != nil {
			return err
		}
		defer t.release()
		if err := t.rd.focus(t.id); err != nil {
			return err
		}
	}
	return webinput.Keys(x.rec.tab, code)
}

// Drag holds the button on the first element, moves to the second, and lets go.
func (x *Explorer) Drag(ctx context.Context, from, to string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a, err := x.find(from)
	if err != nil {
		return err
	}
	defer a.release()
	b, err := x.find(to)
	if err != nil {
		return err
	}
	defer b.release()
	return a.rd.drag(a.id, b.id)
}

// shownJS gives the text that a check of the element would read. It must read as checkPointJS does.
const shownJS = `function(){
  const el = this;
  const norm = s => (s || '').replace(/\s+/g, ' ').trim();
  if (el.tagName === 'INPUT' && ((el.getAttribute('type') || '').toLowerCase() === 'password' || /password/i.test(el.getAttribute('autocomplete') || ''))) return null;
  return (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') ? norm(el.value) : norm(el.innerText);
}`

// Shows gives the text that the element shows, as a check reads it, and records nothing.
func (x *Explorer) Shows(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	t, err := x.find(ref)
	if err != nil {
		return "", err
	}
	defer t.release()
	res, err := chromedp.Call(x.rec.tab, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: shownJS, ObjectID: t.obj, ReturnByValue: ptr(true),
	})
	if err != nil {
		return "", err
	}
	var text string
	if res.Result == nil || json.Unmarshal(res.Result.Value, &text) != nil {
		return "", fmt.Errorf("%s cannot be read", t.name)
	}
	return text, nil
}

// checkPointJS scrolls to the element and gives a point on it where the pointer meets the element, or
// a part of it that shows all of its text, so that the recorder reads the element and not a cover.
const checkPointJS = `function(){
  const el = this;
  const norm = s => (s || '').replace(/\s+/g, ' ').trim();
  if (el.tagName === 'INPUT' && ((el.getAttribute('type') || '').toLowerCase() === 'password' || /password/i.test(el.getAttribute('autocomplete') || ''))) return 'secret';
  el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' });
  const text = (e) => (e.tagName === 'INPUT' || e.tagName === 'TEXTAREA') ? norm(e.value) : norm(e.innerText);
  const want = text(el);
  if (!want) return 'empty';
  const b = el.getBoundingClientRect();
  for (const fy of [0.5, 0.25, 0.75, 0.1, 0.9]) {
    for (const fx of [0.5, 0.25, 0.75, 0.1, 0.9]) {
      const x = b.left + b.width * fx, y = b.top + b.height * fy;
      const hit = document.elementFromPoint(x, y);
      if (hit === el || (hit && el.contains(hit) && text(hit) === want)) return [x, y];
    }
  }
  return null;
}`

// Check holds Option and clicks the element: the page sees nothing, the recorder hears a check, and
// the text that the element shows is the result. It returns that text.
func (x *Explorer) Check(ctx context.Context, ref string) (shown string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	t, err := x.find(ref)
	if err != nil {
		return "", err
	}
	defer t.release()
	res, err := chromedp.Call(x.rec.tab, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: checkPointJS, ObjectID: t.obj, ReturnByValue: ptr(true),
	})
	if err != nil {
		return "", err
	}
	if res.Result == nil {
		return "", fmt.Errorf("%s cannot be read", t.name)
	}
	var word string
	if json.Unmarshal(res.Result.Value, &word) == nil {
		switch word {
		case "secret":
			return "", fmt.Errorf("%s is a password field, and a password is never read", t.name)
		case "empty":
			return "", fmt.Errorf("%s shows no text, so there is nothing to check: check an element that shows the result", t.name)
		}
	}
	if err := x.refuseMany(t); err != nil {
		return "", err
	}
	var pt []float64
	if json.Unmarshal(res.Result.Value, &pt) != nil || len(pt) != 2 {
		return "", fmt.Errorf("%s is covered by another element, so it cannot be read: check an element that is on top", t.name)
	}
	before := len(x.rec.Events())
	if err := webinput.OptionClick(x.rec.tab, pt[0], pt[1]); err != nil {
		return "", err
	}
	deadline := time.Now().Add(checkHeard)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		for _, ev := range x.rec.Events()[before:] {
			if ev.Kind == "check" {
				return ev.Value, nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return "", fmt.Errorf("%s was not read: the page did not report a check", t.name)
}

// untypedKind is the kind of a field that a flow cannot type into, from the role that the list gave it:
// the browser draws these with a picker of its own, and no text of a value fits them. A date or a time
// field is not one of them: its value is set, in the form that the list shows.
func untypedKind(name string) string {
	_, rest, _ := strings.Cut(name, " (")
	role, _, _ := strings.Cut(rest, " ")
	switch role {
	case "color", "slider", "file":
		return role
	}
	return ""
}
