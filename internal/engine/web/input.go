package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/web/webinput"
)

// errCovered is the error of a click on an element that another element covers: a person could not
// have clicked it either.
var errCovered = errors.New("the element is covered by another element, so it was not clicked")

// center scrolls the element into view and gives the middle of its first box.
func (r *reader) center(id cdp.BackendNodeID) (x, y float64, err error) {
	if _, err := chromedp.Call(r.ctx, dom.ScrollIntoViewIfNeeded, dom.ScrollIntoViewIfNeededParams{BackendNodeID: id}); err != nil {
		return 0, 0, err
	}
	return r.point(id)
}

func (r *reader) point(id cdp.BackendNodeID) (x, y float64, err error) {
	res, err := chromedp.Call(r.ctx, dom.GetContentQuads, dom.GetContentQuadsParams{BackendNodeID: id})
	if err != nil {
		return 0, 0, err
	}
	for _, q := range res.Quads {
		if len(q) < 8 {
			continue
		}
		for i := 0; i < 8; i += 2 {
			x += q[i] / 4
			y += q[i+1] / 4
		}
		return x, y, nil
	}
	return 0, 0, errors.New("the element has no box on the page")
}

// click presses the left button on the middle of the element, as a person does.
func (r *reader) click(id cdp.BackendNodeID) error {
	x, y, err := r.center(id)
	if err != nil {
		return err
	}
	h, err := r.handle(id)
	if err != nil {
		return err
	}
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: `function(x, y){ const t = document.elementFromPoint(x, y); return !!t && (t === this || this.contains(t)); }`,
		ObjectID:            h,
		Arguments:           []*runtime.CallArgument{{Value: number(x)}, {Value: number(y)}},
		ReturnByValue:       ptr(true),
	})
	if err != nil {
		return err
	}
	if res.Result != nil && string(res.Result.Value) == "false" {
		return errCovered
	}
	return webinput.Click(r.ctx, x, y)
}

// drag holds the button on the middle of the first element, moves to the middle of the second, and
// lets go. It works for a page that listens for the mouse and for one that uses native drag and drop.
func (r *reader) drag(from, to cdp.BackendNodeID) error {
	if _, _, err := r.center(from); err != nil {
		return err
	}
	if _, _, err := r.center(to); err != nil {
		return err
	}
	fx, fy, err := r.point(from)
	if err != nil {
		return err
	}
	tx, ty, err := r.point(to)
	if err != nil {
		return err
	}
	if err := webinput.Drag(r.ctx, fx, fy, tx, ty); err != nil {
		if errors.Is(err, chromedp.ErrDragOutsideViewport) {
			return errors.New("the two elements of the drag are not on the screen at the same time")
		}
		return err
	}
	return nil
}

// fill replaces what an element holds with the text, by typing it at the element. A page or the
// browser may write into a field once it takes focus, as a saved sign-in does, so the field is
// read back after typing and typed again until it holds exactly the text.
func (r *reader) fill(id cdp.BackendNodeID, text string) error {
	h, err := r.handle(id)
	if err != nil {
		return err
	}
	if _, _, err := r.center(id); err != nil {
		return err
	}
	if form, ok, err := r.temporalForm(h); err != nil {
		return err
	} else if ok {
		return r.setTemporal(h, text, form)
	}
	for try := 0; try < 3; try++ {
		if try > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
			FunctionDeclaration: `function(){
  this.focus();
  if (typeof this.select === 'function') { this.select(); return true; }
  if (this.isContentEditable) { const s = getSelection(), g = document.createRange(); g.selectNodeContents(this); s.removeAllRanges(); s.addRange(g); return true; }
  return false;
}`,
			ObjectID:      h,
			ReturnByValue: ptr(true),
		})
		if err != nil {
			return err
		}
		if res.Result != nil && string(res.Result.Value) == "false" {
			return errors.New("the element is not a field that takes text")
		}
		if _, err := chromedp.Call(r.ctx, input.InsertText, input.InsertTextParams{Text: text}); err != nil {
			return err
		}
		same, err := r.holds(h, text)
		if err != nil {
			return err
		}
		if same {
			return nil
		}
	}
	return errors.New("the field held other text after typing: the page or a sign-in saved in the browser changed it")
}

// temporalForms are the fields that the browser draws with a picker, and the form that their value
// takes. Key events do not reach them in a way that holds from one browser to the next.
var temporalForms = map[string]string{
	"date": "2026-03-09", "datetime-local": "2026-03-09T14:30", "time": "14:30", "month": "2026-03", "week": "2026-W11",
}

// temporalForm gives the example form of the value of a date or time field, and false for any other element.
func (r *reader) temporalForm(h runtime.RemoteObjectID) (string, bool, error) {
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: `function(){ return this instanceof HTMLInputElement ? this.type : ''; }`,
		ObjectID:            h,
		ReturnByValue:       ptr(true),
	})
	if err != nil {
		return "", false, err
	}
	var kind string
	if res.Result != nil {
		_ = json.Unmarshal(res.Result.Value, &kind)
	}
	form, ok := temporalForms[kind]
	return form, ok, nil
}

// setTemporal sets the value through the native setter of the element, so that a framework that wraps
// the setter of the instance still sees a change, and then sends the events that a person's pick would.
func (r *reader) setTemporal(h runtime.RemoteObjectID, text, form string) error {
	arg, err := json.Marshal(text)
	if err != nil {
		return err
	}
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: `function(t){
  this.focus();
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(this, t);
  this.dispatchEvent(new Event('input', { bubbles: true }));
  this.dispatchEvent(new Event('change', { bubbles: true }));
  return this.value === t;
}`,
		ObjectID:      h,
		Arguments:     []*runtime.CallArgument{{Value: arg}},
		ReturnByValue: ptr(true),
	})
	if err != nil {
		return err
	}
	if res.Result == nil || string(res.Result.Value) != "true" {
		return fmt.Errorf("the field did not keep %q: a field of this kind takes its value in the form %s", text, form)
	}
	return nil
}

// holds says whether a field holds exactly the text. It returns only the answer, so a secret
// never comes back from the page.
func (r *reader) holds(h runtime.RemoteObjectID, text string) (bool, error) {
	arg, err := json.Marshal(text)
	if err != nil {
		return false, err
	}
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: `function(t){ return ('value' in this ? this.value : this.innerText) === t; }`,
		ObjectID:            h,
		Arguments:           []*runtime.CallArgument{{Value: arg}},
		ReturnByValue:       ptr(true),
	})
	if err != nil {
		return false, err
	}
	return res.Result != nil && string(res.Result.Value) == "true", nil
}

// focus puts the keyboard on the element.
func (r *reader) focus(id cdp.BackendNodeID) error {
	h, err := r.handle(id)
	if err != nil {
		return err
	}
	_, err = chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: `function(){ this.focus(); }`, ObjectID: h,
	})
	return err
}

func number(f float64) []byte { return []byte(fmt.Sprintf("%g", f)) }

var keyNames = map[string]string{
	"enter": kb.Enter, "return": kb.Enter, "tab": kb.Tab, "escape": kb.Escape, "esc": kb.Escape,
	"backspace": kb.Backspace, "delete": kb.Delete, "space": " ",
	"arrowup": kb.ArrowUp, "arrowdown": kb.ArrowDown, "arrowleft": kb.ArrowLeft, "arrowright": kb.ArrowRight,
	"home": kb.Home, "end": kb.End, "pageup": kb.PageUp, "pagedown": kb.PageDown,
}

// pressKey sends a key by its name, such as Enter or Escape, or a single character.
func pressKey(ctx context.Context, name string) error {
	key, ok := keyNames[strings.ToLower(name)]
	switch {
	case ok:
	case len([]rune(name)) == 1:
		key = name
	default:
		return fmt.Errorf("the key %q is not known: use Enter, Tab, Escape, Backspace, Delete, Space, an arrow, Home, End, PageUp, PageDown, or one character", name)
	}
	return webinput.Keys(ctx, key)
}

// waitFor looks for the element until it is found or the time is up, and gives the last reading.
func waitFor(ctx context.Context, locs map[string]domain.Locator, name string, limit time.Duration) (resolved, *reader, error) {
	deadline := time.Now().Add(limit)
	for {
		rd := newReader(ctx, locs)
		res, err := rd.resolve(name)
		if err == nil && res.state.Found || time.Now().After(deadline) || ctx.Err() != nil {
			return res, rd, err
		}
		rd.release()
		select {
		case <-ctx.Done():
			return res, rd, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// hover moves the pointer onto the middle of the element, as a person does to open a menu.
func (r *reader) hover(id cdp.BackendNodeID) error {
	x, y, err := r.center(id)
	if err != nil {
		return err
	}
	return webinput.Hover(r.ctx, x, y)
}

// selectOption chooses the option of a list whose label is the text. The page hears an input and a
// change, as it does when a person picks from the list that the browser draws.
func (r *reader) selectOption(id cdp.BackendNodeID, label string) error {
	h, err := r.handle(id)
	if err != nil {
		return err
	}
	arg, _ := json.Marshal(label)
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: `function(label){
  if (this.tagName !== 'SELECT') return 'notlist';
  if (this.disabled) return 'disabled';
  const norm = s => (s || '').replace(/\s+/g, ' ').trim();
  const hit = Array.from(this.options).filter(o => norm(o.text) === norm(label));
  if (hit.length === 0) return 'none';
  if (hit.length > 1) return 'many';
  if (hit[0].disabled) return 'optiondisabled';
  this.focus();
  Array.from(this.options).forEach(o => { o.selected = o === hit[0]; });
  this.dispatchEvent(new Event('input', { bubbles: true }));
  this.dispatchEvent(new Event('change', { bubbles: true }));
  return 'ok';
}`,
		ObjectID:      h,
		Arguments:     []*runtime.CallArgument{{Value: arg}},
		ReturnByValue: ptr(true),
	})
	if err != nil {
		return err
	}
	var word string
	if res.Result != nil {
		_ = json.Unmarshal(res.Result.Value, &word)
	}
	switch word {
	case "ok":
		return nil
	case "notlist":
		return errors.New("the element is not a select list")
	case "disabled":
		return errors.New("the list is disabled")
	case "none":
		return fmt.Errorf("the list has no option with the label %q", label)
	case "many":
		return fmt.Errorf("the list has more than one option with the label %q", label)
	case "optiondisabled":
		return fmt.Errorf("the option %q is disabled", label)
	}
	return errors.New("the list could not be set")
}

// boxState says what kind of box the element is, whether it is checked, and whether it is disabled.
func (r *reader) boxState(h runtime.RemoteObjectID) (kind string, checked, disabled bool, err error) {
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: `function(){
  const role = (this.getAttribute('role') || '').toLowerCase();
  let kind = '';
  if (this.tagName === 'INPUT' && (this.type === 'checkbox' || this.type === 'radio')) kind = this.type;
  else if (role === 'checkbox' || role === 'switch') kind = 'checkbox';
  else if (role === 'radio') kind = 'radio';
  const checked = kind && this.tagName === 'INPUT' ? this.checked : this.getAttribute('aria-checked') === 'true';
  return { kind: kind, checked: !!checked, disabled: this.matches(':disabled') || this.getAttribute('aria-disabled') === 'true' };
}`,
		ObjectID:      h,
		ReturnByValue: ptr(true),
	})
	if err != nil {
		return "", false, false, err
	}
	var out struct {
		Kind              string
		Checked, Disabled bool
	}
	if res.Result == nil || json.Unmarshal(res.Result.Value, &out) != nil {
		return "", false, false, errors.New("the element could not be read")
	}
	return out.Kind, out.Checked, out.Disabled, nil
}

// setChecked makes a checkbox, a switch, or a radio button checked or not. A box that is in the state
// already is left alone, so the step gives the same result when it runs twice. The box is read again
// after the click, because a page may keep it as it was.
func (r *reader) setChecked(id cdp.BackendNodeID, want bool) error {
	h, err := r.handle(id)
	if err != nil {
		return err
	}
	kind, checked, disabled, err := r.boxState(h)
	switch {
	case err != nil:
		return err
	case kind == "":
		return errors.New("the element is not a checkbox, a switch, or a radio button")
	case disabled:
		return errors.New("the box is disabled")
	case checked == want:
		return nil
	case !want && kind == "radio":
		return errors.New("a radio button is cleared by choosing another one")
	}
	if err := r.click(id); err != nil {
		return err
	}
	for range 10 {
		if _, now, _, err := r.boxState(h); err == nil && now == want {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("the box did not change after the click")
}

// upload puts a file in a file field, which sends the input and change events of a chosen file.
func (r *reader) upload(id cdp.BackendNodeID, file string) error {
	h, err := r.handle(id)
	if err != nil {
		return err
	}
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: `function(){ return this.tagName === 'INPUT' && this.type === 'file' && !this.disabled; }`,
		ObjectID:            h,
		ReturnByValue:       ptr(true),
	})
	if err != nil {
		return err
	}
	if res.Result == nil || string(res.Result.Value) != "true" {
		return errors.New("the element is not a file field that takes a file")
	}
	_, err = chromedp.Call(r.ctx, dom.SetFileInputFiles, dom.SetFileInputFilesParams{Files: []string{file}, BackendNodeID: id})
	return err
}
