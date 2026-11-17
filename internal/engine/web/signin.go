package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

// signInForm is a sign-in form of a page: the field of the user, the field of the password, and how
// the form is sent. A zero submit means that Enter is pressed in the password field.
type signInForm struct {
	user, password cdp.BackendNodeID
	submit         cdp.BackendNodeID
}

// signInJS gives [user, password] or [user, password, submit] for the sign-in form of the page, and
// [] when there is none. The password field is the one visible password input, and a page with two
// sets a password, so it has no sign-in form. The user field is the last visible text, email, or tel
// input before it, in the same form. The submit control is the one of the form, else the first visible
// button after the password field.
const signInJS = `function(){
  const vis = el => {
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none' || el.getClientRects().length === 0) return false;
    const b = el.getBoundingClientRect();
    return b.width > 0 && b.height > 0 && cs.opacity !== '0';
  };
  const usable = el => vis(el) && !el.disabled;
  const inputs = Array.from(document.querySelectorAll('input'));
  const passwords = inputs.filter(el => el.type === 'password' && vis(el));
  if (passwords.length !== 1) return [];
  const pw = passwords[0];
  const before = el => !!(el.compareDocumentPosition(pw) & Node.DOCUMENT_POSITION_FOLLOWING);
  const after = el => !!(el.compareDocumentPosition(pw) & Node.DOCUMENT_POSITION_PRECEDING);
  const users = inputs.filter(el => ['text', 'email', 'tel'].includes(el.type) && usable(el) && el.form === pw.form && before(el));
  if (users.length === 0) return [];
  const sends = el => (el.tagName === 'BUTTON' ? el.type !== 'button' && el.type !== 'reset' : el.type === 'submit') && usable(el);
  let submit = pw.form ? Array.from(pw.form.querySelectorAll('button, input[type=submit]')).find(sends) : undefined;
  if (!submit) submit = Array.from(document.querySelectorAll('button')).find(el => el.type !== 'reset' && usable(el) && after(el));
  const found = [users[users.length - 1], pw];
  if (submit) found.push(submit);
  return found;
}`

// signInForm reads the page for its sign-in form. The bool is false when the page has none.
func (r *reader) signInForm() (signInForm, bool, error) {
	doc, err := r.document()
	if err != nil {
		return signInForm{}, false, err
	}
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: signInJS, ObjectID: doc, ObjectGroup: objectGroup,
	})
	if err != nil {
		return signInForm{}, false, err
	}
	if res.ExceptionDetails != nil {
		return signInForm{}, false, errors.New("the page could not be searched for a sign-in form: " + exceptionText(res.ExceptionDetails))
	}
	if res.Result == nil || res.Result.ObjectID == "" {
		return signInForm{}, false, nil
	}
	ids, err := r.elementsOf(res.Result.ObjectID)
	if err != nil || len(ids) < 2 {
		return signInForm{}, false, err
	}
	form := signInForm{user: ids[0], password: ids[1]}
	if len(ids) == 3 {
		form.submit = ids[2]
	}
	return form, true, nil
}

// send presses the submit control of the form, or Enter in the password field when it has none.
func (r *reader) send(f signInForm) error {
	if f.submit != 0 {
		return r.click(f.submit)
	}
	if err := r.focus(f.password); err != nil {
		return err
	}
	return pressKey(r.ctx, "enter")
}

// alertJS gives the text of the first visible element with the role alert.
const alertJS = `function(){
  for (const el of document.querySelectorAll('[role=alert]')) {
    const cs = getComputedStyle(el);
    if (cs.visibility !== 'hidden' && cs.display !== 'none' && el.getClientRects().length > 0) return (el.innerText || '').replace(/\s+/g, ' ').trim();
  }
  return '';
}`

func (r *reader) alertText() string {
	doc, err := r.document()
	if err != nil {
		return ""
	}
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: alertJS, ObjectID: doc, ReturnByValue: ptr(true),
	})
	if err != nil || res.Result == nil {
		return ""
	}
	var text string
	if json.Unmarshal(res.Result.Value, &text) != nil {
		return ""
	}
	return clip(text)
}

// accountName is the account of a step: an empty name is default.
func accountName(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

// signInState is what the engine found at a page that an open reached.
type signInState int

const (
	noSignInForm signInState = iota
	signedIn
	signInUnfinished
)

// signInAfterOpen signs in to the form that the page of an open shows, when it shows one, and stores
// who signed in on the action. A sign-in that does not finish is an error, which no scenario reads as
// a failure of the product.
func (e *Engine) signInAfterOpen(ctx context.Context, s *session, account string, who config.WebSignIn, opened *url.URL, a *domain.Action) error {
	s.settle(ctx, e.o.Settle)
	state, alert, err := e.signIn(ctx, s, account, who)
	switch {
	case err != nil:
		return err
	case state == signInUnfinished:
		return errors.New(unfinishedText(account, who, alert))
	case state == signedIn:
		a.Web.SignIn = &domain.WebSignIn{Account: account, User: who.User}
		return e.backToAddress(ctx, s, opened)
	}
	return nil
}

// signIn fills the form of the page, sends it, and waits until the form is gone. The alert is what the
// page says when the form is still there.
func (e *Engine) signIn(ctx context.Context, s *session, account string, who config.WebSignIn) (state signInState, alert string, err error) {
	rd := newReader(ctx, nil)
	defer rd.release()
	form, found, err := rd.signInForm()
	if err != nil || !found {
		return noSignInForm, "", err
	}
	if e.o.Secrets == nil {
		return noSignInForm, "", errors.New("the sign-in password could not be read: the engine has no secret source")
	}
	password, err := e.o.Secrets.Resolve(ctx, who.Password)
	if err != nil {
		return noSignInForm, "", fmt.Errorf("the sign-in password of %s could not be read: %w", account, err)
	}
	if err := rd.fill(form.user, who.User); err != nil {
		return noSignInForm, "", fmt.Errorf("the user of %s could not be typed: %w", account, err)
	}
	if err := rd.fill(form.password, password); err != nil {
		return noSignInForm, "", fmt.Errorf("the password of %s could not be typed: %w", account, err)
	}
	if err := rd.send(form); err != nil {
		return noSignInForm, "", fmt.Errorf("the sign-in form of %s could not be sent: %w", account, err)
	}
	rd.release()

	// A third of the time of an action is the most that a sign-in may take, so that the page can still
	// be read and photographed when it does not finish.
	wait, cancel := context.WithTimeout(ctx, e.o.ActionTimeout/3)
	defer cancel()
	s.settle(wait, e.o.Settle)
	if formGoneBefore(wait) {
		return signedIn, "", nil
	}
	after := newReader(ctx, nil)
	defer after.release()
	return signInUnfinished, after.alertText(), nil
}

// formGoneBefore waits until the page shows no sign-in form, and says whether that happened before
// ctx ended. A page that cannot be read in the middle of a navigation is read again.
func formGoneBefore(ctx context.Context) bool {
	for {
		rd := newReader(ctx, nil)
		_, found, err := rd.signInForm()
		rd.release()
		if err == nil && !found {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(poll):
		}
	}
}

// backToAddress opens the address of the step again when the sign-in sent the tab elsewhere, as an
// application that shows its home page after a sign-in does.
func (e *Engine) backToAddress(ctx context.Context, s *session, opened *url.URL) error {
	s.settle(ctx, e.o.Settle)
	loc, err := s.location(ctx)
	if err != nil {
		return nil
	}
	if now, err := url.Parse(loc); err == nil && sameAddress(now, opened) {
		return nil
	}
	return s.navigate(ctx, opened.String())
}

func sameAddress(a, b *url.URL) bool {
	x, y := *a, *b
	x.Fragment, y.Fragment = "", ""
	return x.String() == y.String()
}

func unfinishedText(account string, who config.WebSignIn, alert string) string {
	text := "signing in as " + account + " did not finish: the sign-in form is still on the page"
	if alert != "" {
		text += fmt.Sprintf(" and the page says %q", alert)
	}
	return text + "; check authProfiles." + account + ".web.user and the variable " + who.PasswordEnv() + " of its password"
}
