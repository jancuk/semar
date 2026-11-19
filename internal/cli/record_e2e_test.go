package cli_test

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/engine/web/webtest"
	"github.com/jancuk/semar/internal/sampleapp"
)

// refundShop is the refund page of the sample shop. With bug set, it refunds one more than the amount.
func refundShop(bug bool) string {
	extra := ""
	if bug {
		extra = " + 1"
	}
	return `<!doctype html><html><head><title>Refund</title></head><body>
<h1>Refund an order</h1>
<label>Amount <input id="amount" type="text"></label>
<label>Reason <input id="reason" type="text"></label>
<button id="refund">Refund</button>
<div id="queue" role="list" aria-label="Queue"><div class="card" role="listitem" draggable="true">Order o1</div></div>
<div id="archive" role="list" aria-label="Archive"><p>Drop here</p></div>
<p id="total">0.00</p>
<p id="status">open</p>
<script>
const $ = id => document.getElementById(id);
$('refund').addEventListener('click', () => {
  $('total').textContent = (parseFloat($('amount').value || '0')` + extra + `).toFixed(2);
  $('status').textContent = 'partly refunded';
});
const archive = $('archive');
let dragged = null;
document.addEventListener('dragstart', e => { dragged = e.target; e.dataTransfer.setData('text/plain', 'x'); });
archive.addEventListener('dragover', e => e.preventDefault());
archive.addEventListener('drop', e => { e.preventDefault(); archive.appendChild(dragged); });
</script></body></html>`
}

// signInShop is a sign-in page that welcomes only the password that the test knows.
const signInShop = `<!doctype html><html><head><title>Sign in</title></head><body>
<label>Email <input id="email" type="text"></label>
<label>Password <input id="pw" type="password"></label>
<button id="go">Sign in</button>
<p id="welcome">signed out</p>
<script>
document.getElementById('go').addEventListener('click', () => {
  const ok = document.getElementById('pw').value === 'hunter2-pass';
  document.getElementById('welcome').textContent = ok ? 'Welcome ' + document.getElementById('email').value : 'Rejected';
});
</script></body></html>`

// tabSessionShop keeps who signed in for as long as the tab lives, and shows no form to a person
// who is signed in, as an application with a session in sessionStorage does.
const tabSessionShop = `<!doctype html><html><head><title>Desk</title></head><body>
<form id="form"><label>Email <input id="email" type="text"></label><button id="go" type="button">Sign in</button></form>
<p id="welcome"></p>
<script>
const show = () => {
  const who = sessionStorage.getItem('who');
  if (who && document.getElementById('form')) document.getElementById('form').remove();
  document.getElementById('welcome').textContent = who ? 'Welcome ' + who : 'signed out';
};
document.getElementById('go').addEventListener('click', () => { sessionStorage.setItem('who', document.getElementById('email').value); show(); });
show();
</script></body></html>`

// orderShop is the page of one order: its number is in the address, the title, and the text.
func orderShop(id string) string {
	return `<!doctype html><html><head><title>Order ` + id + `</title></head><body>
<h1>Order ` + id + `</h1>
<label>Order number <input id="num" type="text"></label>
<p id="shown">` + id + `</p>
</body></html>`
}

type shopPages struct {
	refund atomic.Bool
	// refundExtra is put at the end of the refund page, as a page that loads from another host does.
	refundExtra atomic.Pointer[string]
}

func recordRepoWith(t *testing.T, o repoOptions) (*repo, *shopPages) {
	t.Helper()
	if _, err := web.FindChrome(""); err != nil {
		t.Skip("skipped: " + err.Error())
	}
	pages := &shopPages{}
	wrap := o.wrap
	o.wrap = func(next http.Handler) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("/refund", func(w http.ResponseWriter, _ *http.Request) {
			page := refundShop(pages.refund.Load())
			if extra := pages.refundExtra.Load(); extra != nil {
				page = strings.Replace(page, "</body>", *extra+"</body>", 1)
			}
			fmt.Fprint(w, page)
		})
		mux.HandleFunc("/signin", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, signInShop) })
		mux.HandleFunc("/desk", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tabSessionShop) })
		mux.HandleFunc("/echo", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `<!doctype html><title>Echo</title><label>Name <input id="name" type="text"></label><button id="save">Save</button><p id="out"></p>
<script>document.getElementById('save').addEventListener('click', () => { document.getElementById('out').textContent = 'Saved ' + document.getElementById('name').value; });</script>`)
		})
		mux.HandleFunc("/due", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `<!doctype html><title>Due</title><label>Due date <input id="due" type="date"></label><button id="save">Save</button><p id="shown"></p>
<script>document.getElementById('save').addEventListener('click', () => { document.getElementById('shown').textContent = 'Due ' + document.getElementById('due').value; });</script>`)
		})
		mux.HandleFunc("/braces", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `<!doctype html><title>Braces</title><p id="t">hello {{x}} world</p>`)
		})
		mux.HandleFunc("/orders/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, orderShop(strings.TrimPrefix(r.URL.Path, "/orders/")))
		})
		if wrap != nil {
			next = wrap(next)
		}
		mux.Handle("/", next)
		return mux
	}
	return newRepo(t, o), pages
}

func recordRepo(t *testing.T) (*repo, *shopPages) {
	return recordRepoWith(t, repoOptions{app: sampleapp.Options{}})
}

// person is what a person does in the window, sent as real mouse and key events.
type person func(d webtest.Driver)

func (p person) in(ctx context.Context) context.Context {
	return cli.WithRecording(ctx, func(tab context.Context) {
		d := webtest.Driver{Tab: tab}
		deadline := time.Now().Add(15 * time.Second)
		for !d.Ready() && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		p(d)
	})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// refundFlow is two fills, a click, a drag, and two checks.
func refundFlow(t *testing.T) person {
	return func(d webtest.Driver) {
		must(t, d.Type("#amount", "5"))
		must(t, d.Type("#reason", "late delivery"))
		must(t, d.Click("#refund"))
		must(t, d.Drag(".card", "#archive"))
		must(t, d.OptionClick("#total"))
		must(t, d.OptionClick("#status"))
	}
}

func (r *repo) record(t *testing.T, ctx context.Context, want int, p person, args ...string) result {
	t.Helper()
	res := r.semar(t, p.in(ctx), append([]string{"record"}, args...)...)
	if res.code != want {
		t.Fatalf("semar record %v exited %d, want %d\nstdout:\n%s\nstderr:\n%s", args, res.code, want, res.stdout, res.stderr)
	}
	return res
}

func (r *repo) run(t *testing.T, ctx context.Context, want int, args ...string) result {
	t.Helper()
	res := r.semar(t, ctx, append([]string{"run"}, args...)...)
	if res.code != want {
		t.Fatalf("semar run %v exited %d, want %d\nstdout:\n%s\nstderr:\n%s", args, res.code, want, res.stdout, res.stderr)
	}
	return res
}

func TestRecordWritesATestThatReplaysAndFailsWhenACheckIsWrong(t *testing.T) {
	r, pages := recordRepo(t)
	ctx := t.Context()

	res := r.record(t, ctx, cli.ExitOK, refundFlow(t), "SHOP-482-07", "--open", "/refund", "--service", "orders")
	contains(t, "the output", res.stdout,
		"A Chrome window is open at http://127.0.0.1:",
		"Hold Option and click what must be true. Close the window to finish.",
		"Recorded 5 actions and 2 checks.",
		"Wrote .semar/screens/orders.yaml (1 screen, 7 elements)",
		".semar/scenarios/SHOP-482/SHOP-482-07.feature",
		"the script of SHOP-482-07",
		"Replayed once: passed.",
		"Next: semar run",
	)
	if strings.Contains(res.stdout, "semar review") {
		t.Errorf("the output points to semar review:\n%s", res.stdout)
	}

	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-07.feature"))
	must(t, err)
	contains(t, "the scenario", string(feature),
		"@id:SHOP-482-07 @ticket:SHOP-482 @surface:web @auth:support-agent",
		"Given Open /refund",
		`When Type "5" in amount`,
		`And Type "late delivery" in reason`,
		"And Click refund",
		"And Drag order-o1 to archive",
		`Then the element total equals "5.00"`,
		`And the element status equals "partly refunded"`,
		"# person",
	)
	screensFile, err := os.ReadFile(r.path(".semar", "screens", "orders.yaml"))
	must(t, err)
	contains(t, "the screens", string(screensFile),
		"app: orders", "  Refund:",
		"amount: { role: textbox, name: Amount }",
		"refund: { role: button, name: Refund }",
		"archive: { css: \"#archive\" }",
		"total: { css: \"#total\" }",
	)

	passed := r.run(t, ctx, cli.ExitOK, "--scenario", "SHOP-482-07")
	contains(t, "the run", passed.stdout, "SHOP-482-07  passed")

	// The page changes so that a check is wrong: the run fails, and does not end undecided.
	pages.refund.Store(true)
	failed := r.run(t, ctx, cli.ExitFailed, "--scenario", "SHOP-482-07")
	contains(t, "the run", failed.stdout, "SHOP-482-07  failed")
	contains(t, "the report", r.lastReport(t), "6.00")
}

// A date that a person keys into a date field is one step with the value of the field, and the replay
// sets the same value: the page shows it again.
func TestRecordWritesADateFieldAsOneFillThatReplays(t *testing.T) {
	r, _ := recordRepo(t)
	ctx := t.Context()
	flow := person(func(d webtest.Driver) {
		_, err := chromedp.Run(d.Tab, chromedp.Evaluate[bool](`document.getElementById('due').focus() === undefined`))
		must(t, err)
		must(t, d.Keys("03092026"))
		must(t, d.Click("#save"))
		must(t, d.OptionClick("#shown"))
	})
	r.record(t, ctx, cli.ExitOK, flow, "SHOP-482-08", "--open", "/due", "--service", "orders")
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-08.feature"))
	must(t, err)
	if !regexp.MustCompile(`When Type "\d{4}-\d{2}-\d{2}" in due-date\n\s+# step: fill-due-date\n\s+And Click save\n`).Match(feature) {
		t.Errorf("the scenario has no single fill of the date field before the click:\n%s", feature)
	}
	passed := r.run(t, ctx, cli.ExitOK, "--scenario", "SHOP-482-08")
	contains(t, "the run", passed.stdout, "SHOP-482-08  passed")
}

// Each scenario signs in by itself, so the second must not start in the tab that the first left signed in.
func TestTwoWebScenariosOfOneRunDoNotShareTheStateOfATab(t *testing.T) {
	r, _ := recordRepo(t)
	ctx := t.Context()
	signIn := func(email string) person {
		return func(d webtest.Driver) {
			must(t, d.Type("#email", email))
			must(t, d.Click("#go"))
			must(t, d.OptionClick("#welcome"))
		}
	}
	r.record(t, ctx, cli.ExitOK, signIn("ana@example.com"), "SHOP-482-21", "--open", "/desk", "--service", "orders")
	r.record(t, ctx, cli.ExitOK, signIn("ben@example.com"), "SHOP-482-22", "--open", "/desk", "--service", "orders")

	both := r.semar(t, ctx, "run", "--scenario", "SHOP-482-21", "--scenario", "SHOP-482-22")
	contains(t, "the run", both.stdout, "SHOP-482-21  passed", "SHOP-482-22  passed")
}

func TestRecordWithNoCheckWritesNothingAndSaysHow(t *testing.T) {
	r, _ := recordRepo(t)
	res := r.record(t, t.Context(), cli.ExitError, func(d webtest.Driver) {
		must(t, d.Type("#amount", "5"))
		must(t, d.Click("#refund"))
	}, "SHOP-482-09", "--open", "/refund")
	contains(t, "the output", res.stdout, "Recorded 3 actions and no check.")
	contains(t, "the message", res.stderr, "nothing was written", "a test needs at least one check", "hold Option and click what must be true")
	for _, p := range []string{r.path(".semar", "screens"), r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-09.feature")} {
		if exists(t, p) {
			t.Errorf("%s was made for a recording that has no check", p)
		}
	}
}

func TestRecordRefusesAScenarioThatExistsUntilItIsToldToReplaceIt(t *testing.T) {
	r, _ := recordRepo(t)
	ctx := t.Context()
	r.record(t, ctx, cli.ExitOK, refundFlow(t), "SHOP-482-07", "--open", "/refund", "--no-replay")

	opened := false
	res := r.record(t, ctx, cli.ExitError, func(webtest.Driver) { opened = true }, "SHOP-482-07", "--open", "/refund")
	contains(t, "the error", res.stderr, "scenario SHOP-482-07 exists already", ".semar/scenarios/SHOP-482/SHOP-482-07.feature", "--replace")
	if opened {
		t.Error("a window was opened for a recording that was refused")
	}

	before, err := os.ReadFile(r.path(".semar", "screens", "orders.yaml"))
	must(t, err)
	res = r.record(t, ctx, cli.ExitOK, refundFlow(t), "SHOP-482-07", "--open", "/refund", "--replace", "--no-replay")
	if strings.Contains(res.stdout, "Replayed once") {
		t.Errorf("--no-replay still replayed:\n%s", res.stdout)
	}
	after, err := os.ReadFile(r.path(".semar", "screens", "orders.yaml"))
	must(t, err)
	if string(before) != string(after) {
		t.Errorf("a second recording of the same page changed the screens file:\n%s\n---\n%s", before, after)
	}
}

func TestRecordRefusesATextThatTheEngineWouldReadAsAVariable(t *testing.T) {
	r, _ := recordRepo(t)
	res := r.record(t, t.Context(), cli.ExitError, func(d webtest.Driver) {
		must(t, d.OptionClick("#t"))
	}, "SHOP-482-14", "--open", "/braces")
	contains(t, "the error", res.stderr, "nothing was written", "step wait-t", "x is used before any step captures it")
	if exists(t, r.path(".semar", "screens")) || exists(t, r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-14.feature")) {
		t.Error("a file was written for a recording that makes an invalid script")
	}
}

func TestRecordWillNotRunWhereARecordingWouldBeRefused(t *testing.T) {
	r, _ := recordRepo(t)
	opened := false
	drive := func(webtest.Driver) { opened = true }

	r.rewriteConfig(t, func(s string) string { return strings.Replace(s, "mode: test_data", "mode: read_only", 1) })
	res := r.record(t, t.Context(), cli.ExitError, drive, "SHOP-482-07", "--open", "/refund")
	contains(t, "the error", res.stderr, "read only")

	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(strings.Replace(s, "mode: read_only", "mode: test_data", 1), "kind: test", "kind: production", 1)
	})
	res = r.record(t, t.Context(), cli.ExitError, drive, "SHOP-482-07", "local", "--open", "/refund")
	contains(t, "the error", res.stderr, "production")
	if opened {
		t.Error("a window was opened")
	}
}

func TestRecordKeepsAPasswordOutOfEveryFileAndStream(t *testing.T) {
	const password = "hunter2-pass"
	r, _ := recordRepo(t)
	t.Setenv("SHOP_PASSWORD", password)
	res := r.record(t, t.Context(), cli.ExitOK, func(d webtest.Driver) {
		must(t, d.Type("#email", "ana@example.com"))
		must(t, d.Type("#pw", password))
		must(t, d.Click("#go"))
		must(t, d.OptionClick("#welcome"))
	}, "SHOP-482-11", "--open", "/signin", "--secret-env", "SHOP_PASSWORD")
	contains(t, "the output", res.stdout, "Replayed once: passed.")

	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-11.feature"))
	must(t, err)
	contains(t, "the scenario", string(feature), "Type the secret env:SHOP_PASSWORD in password", `Then the element welcome equals "Welcome ana@example.com"`)

	r.assertNoSecret(t, []string{password})
	r.assertNoSecretInHome(t, password)
}

func TestRecordAsksForTheNameOfAPasswordOnATerminal(t *testing.T) {
	const password = "hunter2-pass"
	r, _ := recordRepo(t)
	t.Setenv("SHOP_PASSWORD", password)
	p := person(func(d webtest.Driver) {
		must(t, d.Type("#pw", password))
		must(t, d.Click("#go"))
		must(t, d.OptionClick("#welcome"))
	})

	// With no terminal, the missing name is an error and nothing is written.
	res := r.record(t, t.Context(), cli.ExitError, p, "SHOP-482-12", "--open", "/signin")
	contains(t, "the error", res.stderr, "--secret-env")
	if exists(t, r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-12.feature")) {
		t.Error("a scenario was written without the name of the password")
	}

	r.opts.Terminal = func() bool { return true }
	r.opts.Stdin = strings.NewReader("SHOP_PASSWORD\n")
	res = r.record(t, t.Context(), cli.ExitOK, p, "SHOP-482-12", "--open", "/signin", "--no-replay")
	contains(t, "the question", res.stdout, "environment variable that holds the password")
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-12.feature"))
	must(t, err)
	contains(t, "the scenario", string(feature), "env:SHOP_PASSWORD")
}

func TestRecordMakesAFixtureAndWritesItsValuesAsVariables(t *testing.T) {
	r, _ := recordRepoWith(t, repoOptions{tree: "e2e-fixtures", app: sampleapp.Options{}, payments: &sampleapp.PaymentOptions{}})
	ctx := t.Context()

	res := r.record(t, ctx, cli.ExitOK, func(d webtest.Driver) {
		must(t, d.Type("#num", "o-1"))
		must(t, d.Click("h1"))
		must(t, d.OptionClick("#shown"))
	}, "SHOP-482-13", "--given", "order.paid as o", "--open", "/orders/{{o.id}}", "--service", "orders", "--as", "support-agent")
	contains(t, "the output", res.stdout,
		"Made the fixture order.paid.",
		"A Chrome window is open at http://127.0.0.1:",
		"/orders/o-1",
		"Replayed once: passed.",
	)
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-13.feature"))
	must(t, err)
	text := string(feature)
	contains(t, "the scenario", text,
		"Given the fixture order.paid as o",
		"And Open /orders/{{o.id}}",
		`Type "{{o.id}}" in order-number`,
		`Then the element shown equals "{{o.id}}"`,
	)
	screensFile, err := os.ReadFile(r.path(".semar", "screens", "orders.yaml"))
	must(t, err)
	for name, data := range map[string]string{"the scenario": text, "the screens": string(screensFile)} {
		if strings.Contains(data, "o-1") {
			t.Errorf("%s holds the ID that the fixture gave:\n%s", name, data)
		}
	}
	// The fixture of the recording was cleaned up, and so was the one of the replay.
	if got := r.orderStatus(t, "o-1"); got != sampleapp.StatusCancelled {
		t.Errorf("order o-1 is %s, want cancelled", got)
	}
}

func TestRecordInterruptedWritesNothingAndCleansUpItsFixture(t *testing.T) {
	r, _ := recordRepoWith(t, repoOptions{tree: "e2e-fixtures", app: sampleapp.Options{}, payments: &sampleapp.PaymentOptions{}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	res := r.record(t, ctx, cli.ExitError, func(d webtest.Driver) {
		must(t, d.OptionClick("#shown"))
		cancel()
	}, "SHOP-482-15", "--given", "order.paid as o", "--open", "/orders/{{o.id}}", "--service", "orders", "--as", "support-agent")
	contains(t, "the error", res.stderr, "the recording was interrupted, and nothing was written")
	if exists(t, r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-15.feature")) || exists(t, r.path(".semar", "screens")) {
		t.Error("a file was written for an interrupted recording")
	}
	if got := r.orderStatus(t, "o-1"); got != sampleapp.StatusCancelled {
		t.Errorf("order o-1 is %s, want cancelled by the cleanup", got)
	}
}

func TestRecordHasNoWindowToCloseWithoutChrome(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "    effects:", "    web: {chrome: /does/not/exist}\n    effects:", 1)
	})
	res := r.semar(t, t.Context(), "record", "SHOP-482-07")
	if res.code != cli.ExitError {
		t.Fatalf("exit %d\n%s", res.code, res.stderr)
	}
	contains(t, "the error", res.stderr, "Chrome was not found at the path of web.chrome, /does/not/exist")
}

func (r *repo) assertNoSecretInHome(t *testing.T, secret string) {
	t.Helper()
	needles := forms(secret)
	home := r.opts.Home
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// The profile is Chrome's own, and not a file of Semar.
		if d.IsDir() && d.Name() == "browser" {
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, n := range needles {
			if strings.Contains(string(data), n) {
				t.Errorf("%s holds the password as %q", p, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecordSaysWhichHostsTheReplayBlocked(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "x") }))
	t.Cleanup(cdn.Close)
	r, pages := recordRepo(t)
	extra := `<img src="` + cdn.URL + `/pixel.png">`
	pages.refundExtra.Store(&extra)
	res := r.record(t, t.Context(), cli.ExitOK, refundFlow(t), "SHOP-482-09", "--open", "/refund", "--service", "orders")
	contains(t, "the output", res.stdout,
		"Replayed once: passed.\n",
		"blocked hosts: the browser did not reach 1 host, because environments.local.hosts does not list them\n",
		"  "+strings.TrimPrefix(cdn.URL, "http://")+": SHOP-482-09\n",
	)
}
