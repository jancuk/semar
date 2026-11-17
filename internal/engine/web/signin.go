package web

import (
	"errors"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
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
