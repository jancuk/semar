package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const signInStyle = `<style>.gone{display:none}</style>`

var signInPages = map[string]struct {
	html   string
	found  bool
	user   string
	submit string // the id of the control that sends; "enter" when there is none
}{
	"plain": {`<form id="f"><label>Email <input type="email" id="email"></label>
<label>Password <input type="password" id="pw"></label><button id="go">Sign in</button></form>`, true, "email", "go"},
	"script button": {`<form id="f"><input type="text" id="user"><input type="password" id="pw">
<button type="button" id="go" onclick="1">Sign in</button></form>`, true, "user", "go"},
	"submit input":    {`<form id="f"><input id="user"><input type="password" id="pw"><input type="submit" id="go" value="Go"></form>`, true, "user", "go"},
	"no button":       {`<form id="f"><input type="text" id="user"><input type="password" id="pw"></form>`, true, "user", "enter"},
	"no form element": {`<div><input type="tel" id="phone"><input type="password" id="pw"></div><button id="go">Go</button>`, true, "phone", "go"},
	"last user field before the password": {`<form id="f"><input type="text" id="search"><input type="email" id="email">
<input type="hidden" id="csrf"><input type="password" id="pw"><button id="go">Go</button></form>`, true, "email", "go"},
	"a hidden user field": {`<form id="f"><input type="text" id="decoy" class="gone"><input type="text" id="user">
<input type="password" id="pw"><button id="go">Go</button></form>`, true, "user", "go"},
	"another form's field": {`<form><input type="text" id="search"></form>
<form id="f"><input type="text" id="user"><input type="password" id="pw"><button id="go">Go</button></form>`, true, "user", "go"},
	"a reset button is no way to send": {`<form id="f"><input id="user"><input type="password" id="pw">
<button type="reset" id="clear">Clear</button><button id="go">Go</button></form>`, true, "user", "go"},
	"two password fields":                 {`<form id="f"><input id="user"><input type="password" id="new"><input type="password" id="again"><button>Set</button></form>`, false, "", ""},
	"a hidden password field":             {`<form id="f"><input id="user"><input type="password" id="pw" class="gone"><button>Go</button></form>`, false, "", ""},
	"one visible and one hidden password": {`<form id="f"><input id="user"><input type="password" id="decoy" class="gone"><input type="password" id="pw"><button id="go">Go</button></form>`, true, "user", "go"},
	"a password and no user field":        {`<form id="f"><input type="password" id="pw"><button>Go</button></form>`, false, "", ""},
	"no form":                             {`<h1>Orders</h1><p>Welcome back</p><input type="text" id="search">`, false, "", ""},
}

func tabOf(t *testing.T) context.Context {
	t.Helper()
	chrome, err := FindChrome("")
	if err != nil {
		t.Skip("skipped: " + err.Error())
	}
	alloc, cancelAlloc := allocator(chrome, t.TempDir(), false)
	t.Cleanup(cancelAlloc)
	tab, cancel := chromedp.NewContext(alloc)
	t.Cleanup(cancel)
	tab, cancelTimeout := context.WithTimeout(tab, 60*time.Second)
	t.Cleanup(cancelTimeout)
	return tab
}

func idOf(t *testing.T, rd *reader, id cdp.BackendNodeID) string {
	t.Helper()
	h, err := rd.handle(id)
	if err != nil {
		t.Fatal(err)
	}
	res, err := chromedp.Call(rd.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: `function(){ return this.id; }`, ObjectID: h, ReturnByValue: ptr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(res.Result.Value)
}

func TestFindSignInForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<!doctype html><title>x</title>"+signInStyle+signInPages[r.URL.Query().Get("page")].html)
	}))
	defer srv.Close()
	tab := tabOf(t)
	for name, want := range signInPages {
		t.Run(name, func(t *testing.T) {
			if err := chromedp.Do(tab, chromedp.Navigate(srv.URL+"/?page="+name)); err != nil {
				t.Fatal(err)
			}
			rd := newReader(tab, nil)
			defer rd.release()
			form, ok, err := rd.signInForm()
			if err != nil {
				t.Fatal(err)
			}
			if ok != want.found {
				t.Fatalf("found = %v, want %v", ok, want.found)
			}
			if !ok {
				return
			}
			if got := idOf(t, rd, form.user); got != `"`+want.user+`"` {
				t.Errorf("user field = %s, want %s", got, want.user)
			}
			if got := idOf(t, rd, form.password); got != `"pw"` {
				t.Errorf("password field = %s", got)
			}
			switch {
			case want.submit == "enter" && form.submit != 0:
				t.Errorf("sends with %s, want Enter", idOf(t, rd, form.submit))
			case want.submit != "enter" && form.submit == 0:
				t.Errorf("sends with Enter, want %s", want.submit)
			case want.submit != "enter":
				if got := idOf(t, rd, form.submit); got != `"`+want.submit+`"` {
					t.Errorf("sends with %s, want %s", got, want.submit)
				}
			}
		})
	}
}
