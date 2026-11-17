package web_test

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

const (
	rightPassword = "right-pass-7"
	wrongPassword = "wrong-pass-3"
)

// deskPage keeps who signed in for as long as the tab lives, as an application with a session in
// sessionStorage does: its form is there at every start of a tab.
const deskPage = `<!doctype html><title>Desk</title>
<form id="form"><label>Email <input type="text" id="email"></label>
<label>Password <input type="password" id="pw"></label>
<button type="button" id="go">Sign in</button></form>
<div id="alert" role="alert" style="display:none"></div>
<p id="welcome"></p>
<script>
const show = () => {
  const who = sessionStorage.getItem('who');
  document.getElementById('form').style.display = who ? 'none' : '';
  document.getElementById('welcome').textContent = who ? 'Welcome ' + who : 'signed out';
};
document.getElementById('go').addEventListener('click', async () => {
  const email = document.getElementById('email').value, pw = document.getElementById('pw').value;
  const ok = await (await fetch('/check?pw=' + encodeURIComponent(pw))).text();
  const alertBox = document.getElementById('alert');
  if (ok !== 'yes') { alertBox.style.display = ''; alertBox.textContent = 'Wrong email or password'; return; }
  sessionStorage.setItem('who', email);
  show();
  if (new URLSearchParams(location.search).get('then')) location.href = '/home';
});
show();
</script>`

const homePage = `<!doctype html><title>Home</title><h1>Home</h1><p id="welcome">Welcome home</p>`

func signInHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/desk", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, deskPage) })
	mux.HandleFunc("/home", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, homePage) })
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pw") == rightPassword {
			fmt.Fprint(w, "yes")
			return
		}
		fmt.Fprint(w, "no")
	})
}

func signInRig(t *testing.T, password string) *rig {
	t.Helper()
	needChrome(t)
	return newRig(t, rigOptions{
		handler:       signInHandlers,
		secrets:       map[string]string{"SHOP_WEB_PASSWORD": password},
		accounts:      map[string]config.WebSignIn{"default": {User: "ana@example.com", Password: "env:SHOP_WEB_PASSWORD"}},
		actionTimeout: 12 * time.Second,
	})
}

var welcome = map[string]domain.Locator{"welcome": {CSS: "#welcome"}}

func TestOpenSignsInToAFormThatAppearsAtEveryStart(t *testing.T) {
	r := signInRig(t, rightPassword)
	for i := 1; i <= 2; i++ {
		res := r.do(open("/desk"), welcome, text("welcome"))
		if got := res.Action.Web.Elements["welcome"].Text; got != "Welcome ana@example.com" {
			t.Fatalf("scenario %d: the page shows %q, want it signed in", i, got)
		}
		si := res.Action.Web.SignIn
		if si == nil || si.Account != "default" || si.User != "ana@example.com" {
			t.Fatalf("scenario %d: the stored action says %+v", i, si)
		}
		if res.Action.Err != "" {
			t.Fatalf("scenario %d: err = %q", i, res.Action.Err)
		}
		r.eng.NewScenario()
	}
}

func TestOpenGoesBackToTheAddressAfterTheSignInSentThePersonElsewhere(t *testing.T) {
	r := signInRig(t, rightPassword)
	res := r.do(open("/desk?then=home"), welcome, text("welcome"))
	if res.Action.Err != "" || res.Action.Web.SignIn == nil {
		t.Fatalf("action = %+v, web = %+v", res.Action, res.Action.Web)
	}
	if !strings.HasSuffix(res.Action.Web.URL, "/desk?then=home") {
		t.Errorf("the step ends at %s, want the address that it opened", res.Action.Web.URL)
	}
	if got := res.Action.Web.Elements["welcome"].Text; got != "Welcome ana@example.com" {
		t.Errorf("the page shows %q", got)
	}
}

func TestSignInThatDoesNotFinishNamesTheAccountTheAlertAndWhatToCheck(t *testing.T) {
	r := signInRig(t, wrongPassword)
	res := r.do(open("/desk"), welcome, text("welcome"))
	err := res.Action.Err
	for _, want := range []string{"default", "the sign-in form is still on the page", "Wrong email or password", "web.user", "SHOP_WEB_PASSWORD"} {
		if !strings.Contains(err, want) {
			t.Errorf("the error %q lacks %q", err, want)
		}
	}
	if res.Action.Web.SignIn != nil {
		t.Errorf("a sign-in that did not finish is stored as one that did: %+v", res.Action.Web.SignIn)
	}
	if strings.Contains(err, wrongPassword) {
		t.Errorf("the error holds the password: %q", err)
	}
}

func TestAnAccountWithNoWebSignInIsLeftAtTheForm(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{handler: signInHandlers, secrets: map[string]string{"SHOP_WEB_PASSWORD": rightPassword}})
	res := r.do(open("/desk"), welcome, text("welcome"))
	if res.Action.Err != "" || res.Action.Web.SignIn != nil {
		t.Fatalf("err = %q, sign-in = %+v", res.Action.Err, res.Action.Web.SignIn)
	}
	if got := res.Action.Web.Elements["welcome"].Text; got != "signed out" {
		t.Errorf("the page shows %q, want it left signed out", got)
	}
}

func TestAnAccountOtherThanTheOneOfTheStepIsNotSignedIn(t *testing.T) {
	r := signInRig(t, rightPassword)
	r.as = "ben"
	res := r.do(open("/desk"), welcome, text("welcome"))
	if res.Action.Web.SignIn != nil || res.Action.Web.Elements["welcome"].Text != "signed out" {
		t.Fatalf("the account ben has no web sign-in: %+v, %q", res.Action.Web.SignIn, res.Action.Web.Elements["welcome"].Text)
	}
}

func TestAPageWithNoFormIsLeftAsItIs(t *testing.T) {
	r := signInRig(t, rightPassword)
	res := r.do(open("/home"), welcome, text("welcome"))
	if res.Action.Err != "" || res.Action.Web.SignIn != nil {
		t.Fatalf("err = %q, sign-in = %+v", res.Action.Err, res.Action.Web.SignIn)
	}
	if got := res.Action.Web.Elements["welcome"].Text; got != "Welcome home" {
		t.Errorf("the page shows %q", got)
	}
}

func TestAPasswordIsInNoStoredActionAndNoFile(t *testing.T) {
	for _, password := range []string{rightPassword, wrongPassword} {
		r := signInRig(t, password)
		res := r.do(open("/desk"), welcome, text("welcome"))
		r.eng.Close()
		blob := fmt.Sprintf("%+v", res.Action)
		if strings.Contains(blob, password) {
			t.Errorf("the stored action holds the password: %s", blob)
		}
		err := fs.WalkDir(os.DirFS(r.store.Root()), ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(r.store.Root() + "/" + p)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), password) {
				t.Errorf("%s holds the password", p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
