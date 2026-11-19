package cli_test

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/engine/web/webtest"
)

const (
	portalPassword = "portal-pass-91"
	portalPassVar  = "SHOP_WEB_PASSWORD"
)

// portalShop keeps who signed in in the tab, so its form is there at every start of a tab, and it
// shows a role alert when the password is wrong.
const portalShop = `<!doctype html><html><head><title>Portal</title></head><body>
<form id="form"><label>Email <input id="email" type="text"></label>
<label>Password <input id="pw" type="password"></label>
<button id="go" type="button">Sign in</button></form>
<div id="alert" role="alert" hidden></div>
<main id="main" hidden><h1>Portal</h1><button id="ping">Ping</button><p id="status">idle</p></main>
<script>
const $ = id => document.getElementById(id);
const show = () => {
  const who = sessionStorage.getItem('who');
  $('form').hidden = !!who;
  $('main').hidden = !who;
};
$('go').addEventListener('click', async () => {
  const ok = await (await fetch('/portal-check?pw=' + encodeURIComponent($('pw').value))).text();
  if (ok !== 'yes') { $('alert').hidden = false; $('alert').textContent = 'Wrong email or password'; return; }
  sessionStorage.setItem('who', $('email').value);
  show();
});
$('ping').addEventListener('click', () => { $('status').textContent = 'pong ' + sessionStorage.getItem('who'); });
show();
</script></body></html>`

func portalRepo(t *testing.T) *repo { return portalRepoWith(t, true) }

// portalRepoWith serves the portal page. With web set, the account of the repository signs in to it.
func portalRepoWith(t *testing.T, web bool) *repo {
	t.Helper()
	r, _ := recordRepoWith(t, repoOptions{wrap: func(next http.Handler) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("/portal", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, portalShop) })
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, portalShop) })
		mux.HandleFunc("/portal-check", func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Query().Get("pw") == portalPassword {
				fmt.Fprint(w, "yes")
				return
			}
			fmt.Fprint(w, "no")
		})
		mux.Handle("/", next)
		return mux
	}})
	t.Setenv(portalPassVar, portalPassword)
	if !web {
		return r
	}
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, `secrets: {TOKEN: "env:SEMAR_SAMPLE_TOKEN"}`,
			`secrets: {TOKEN: "env:SEMAR_SAMPLE_TOKEN"}`+"\n    web: {user: ana@example.com, password: \"env:"+portalPassVar+"\"}", 1)
	})
	return r
}

func ping(t *testing.T) person {
	return func(d webtest.Driver) {
		must(t, d.Click("#ping"))
		must(t, d.OptionClick("#status"))
	}
}

func TestRecordOpensTheWindowSignedInAndWritesNoSignInStep(t *testing.T) {
	r := portalRepo(t)
	ctx := t.Context()
	res := r.record(t, ctx, cli.ExitOK, ping(t), "SHOP-482-31", "--open", "/portal", "--service", "orders")
	contains(t, "the output", res.stdout,
		"Signed in as support-agent.",
		"Recorded 2 actions and 1 check.",
		"Replayed once: passed.",
	)
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-31.feature"))
	must(t, err)
	contains(t, "the scenario", string(feature), "Given Open /portal", "When Click ping", `Then the element status equals "pong ana@example.com"`)
	if strings.Contains(string(feature), "Type") {
		t.Errorf("the scenario holds a sign-in step:\n%s", feature)
	}
	r.assertNoSecret(t, []string{portalPassword})
	r.assertNoSecretInHome(t, portalPassword)
	if strings.Contains(res.stdout+res.stderr, portalPassword) {
		t.Error("an output holds the password")
	}
}

func TestTwoRecordedScenariosOfOneRunBothSignInByThemselves(t *testing.T) {
	r := portalRepo(t)
	ctx := t.Context()
	r.record(t, ctx, cli.ExitOK, ping(t), "SHOP-482-32", "--open", "/portal", "--service", "orders")
	r.record(t, ctx, cli.ExitOK, ping(t), "SHOP-482-33", "--open", "/portal", "--service", "orders")
	both := r.semar(t, ctx, "run", "--scenario", "SHOP-482-32", "--scenario", "SHOP-482-33")
	contains(t, "the run", both.stdout, "SHOP-482-32  passed", "SHOP-482-33  passed")
	if strings.Contains(both.stdout+both.stderr, portalPassword) {
		t.Error("an output holds the password")
	}
	r.assertNoSecret(t, []string{portalPassword})
}

func TestAWrongPasswordNamesTheAccountAndTheVariableAndIsNoFailure(t *testing.T) {
	r := portalRepo(t)
	ctx := t.Context()
	r.record(t, ctx, cli.ExitOK, ping(t), "SHOP-482-34", "--open", "/portal", "--service", "orders")

	const wrong = "not-the-password-5"
	t.Setenv(portalPassVar, wrong)
	res := r.semar(t, ctx, "run", "--scenario", "SHOP-482-34")
	if res.code == cli.ExitFailed || res.code == cli.ExitOK {
		t.Fatalf("exit %d, want a run that is neither passed nor failed\nstdout:\n%s\nstderr:\n%s", res.code, res.stdout, res.stderr)
	}
	if strings.Contains(res.stdout, "SHOP-482-34  failed") {
		t.Errorf("the scenario failed:\n%s", res.stdout)
	}
	report := r.lastReport(t)
	contains(t, "the report", report, "support-agent", portalPassVar, "Wrong email or password", "the sign-in form is still on the page")
	r.assertNoSecret(t, []string{wrong, portalPassword})
}
