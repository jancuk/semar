package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/jancuk/semar/internal/engine/web/webinput"
	"github.com/jancuk/semar/internal/pagelist"
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
	x.mu.Unlock()
	return l, nil
}

const (
	// RunIDVariable is the one variable that a text of the model may hold.
	RunIDVariable = "{{run.id}}"
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

func (x *Explorer) find(ref string) (found, error) {
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
	if state.Disabled {
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

// Fill replaces what a field holds by typing the text at it. {{run.id}} in the text is the ID of the
// run, and no other variable is allowed.
func (x *Explorer) Fill(ctx context.Context, ref, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.Contains(text, RunIDVariable) {
		if x.RunID == "" {
			return errors.New("the text holds {{run.id}}, and this run has no ID")
		}
		text = strings.ReplaceAll(text, RunIDVariable, x.RunID)
	}
	if strings.Contains(text, "{{") {
		return errors.New("the text holds {{, and {{run.id}} is the only variable that a text may hold")
	}
	t, err := x.find(ref)
	if err != nil {
		return err
	}
	defer t.release()
	if err := t.rd.fill(t.id, text); err != nil {
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

// checkPointJS scrolls to the element and gives a point on it where the pointer meets the element, or
// a part of it that shows all of its text, so that the recorder reads the element and not a cover.
const checkPointJS = `function(){
  const el = this;
  const norm = s => (s || '').replace(/\s+/g, ' ').trim();
  if (el.tagName === 'INPUT' && ((el.getAttribute('type') || '').toLowerCase() === 'password' || /password/i.test(el.getAttribute('autocomplete') || ''))) return 'secret';
  el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' });
  const text = (e) => (e.tagName === 'INPUT' || e.tagName === 'TEXTAREA') ? norm(e.value) : norm(e.innerText);
  const want = text(el);
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
	var secret string
	if json.Unmarshal(res.Result.Value, &secret) == nil && secret == "secret" {
		return "", fmt.Errorf("%s is a password field, and a password is never read", t.name)
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
