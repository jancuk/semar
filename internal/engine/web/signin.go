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
	"github.com/jancuk/semar/internal/secret"
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

// signInTab is a tab that Semar signs in to: the engine's session and the window of a recording
// both are one.
type signInTab interface {
	// settle waits until the page looks still.
	settle(ctx context.Context)
	location(ctx context.Context) (string, error)
	navigate(ctx context.Context, url string) error
}

// signer signs in to the tab with the account that a step or a recording names.
type signer struct {
	tab     signInTab
	secrets secret.Resolver
	// limit is the most that the wait for the form to go may take. It is a part of the time of an
	// action, so that the page can still be read and photographed when the sign-in does not finish.
	limit time.Duration
}

// afterOpen signs in to the form that the page shows, when it shows one, and then opens the address
// of the step again if the sign-in sent the tab elsewhere, as an application that shows its home page
// after a sign-in does. It says whether it signed in. A sign-in that does not finish is an error,
// which no scenario reads as a failure of the product.
func (g signer) afterOpen(ctx context.Context, account string, who config.WebSignIn, opened *url.URL) (bool, error) {
	g.tab.settle(ctx)
	state, alert, err := g.signIn(ctx, account, who)
	switch {
	case err != nil:
		return false, err
	case state == signInUnfinished:
		return false, errors.New(unfinishedText(account, who, alert))
	case state == noSignInForm:
		return false, nil
	}
	g.tab.settle(ctx)
	if loc, err := g.tab.location(ctx); err == nil {
		if now, err := url.Parse(loc); err == nil && sameAddress(now, opened) {
			return true, nil
		}
	}
	return true, g.tab.navigate(ctx, opened.String())
}

// signIn fills the form of the page, sends it, and waits until the form is gone. The alert is what the
// page says when the form is still there.
func (g signer) signIn(ctx context.Context, account string, who config.WebSignIn) (state signInState, alert string, err error) {
	rd := newReader(ctx, nil)
	defer rd.release()
	form, found, err := rd.signInForm()
	if err != nil || !found {
		return noSignInForm, "", err
	}
	if g.secrets == nil {
		return noSignInForm, "", errors.New("the sign-in password could not be read: there is no secret source")
	}
	password, err := g.secrets.Resolve(ctx, who.Password)
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

	wait, cancel := context.WithTimeout(ctx, g.limit)
	defer cancel()
	g.tab.settle(wait)
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

func sameAddress(a, b *url.URL) bool {
	x, y := *a, *b
	x.Fragment, y.Fragment = "", ""
	return x.String() == y.String()
}

// unfinishedText is one short line: a report cuts a reason at 200 characters, so the alert, which
// is the page's own words and the longest part, comes last.
func unfinishedText(account string, who config.WebSignIn, alert string) string {
	text := "signing in as " + account + " did not finish: the sign-in form is still on the page. Check web.user and " + who.PasswordEnv() + "."
	if alert != "" {
		text += fmt.Sprintf(" The page says %q", clipTo(alert, maxAlertShown))
	}
	return text
}

const maxAlertShown = 60

func clipTo(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// sessionTab is the tab of an engine session.
type sessionTab struct {
	s     *session
	limit time.Duration
}

func (t sessionTab) settle(ctx context.Context) { t.s.settle(ctx, t.limit) }
func (t sessionTab) location(ctx context.Context) (string, error) {
	return t.s.location(ctx)
}
func (t sessionTab) navigate(ctx context.Context, url string) error { return t.s.navigate(ctx, url) }
