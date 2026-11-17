package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/record"
)

const refundPage = `<!doctype html><html><head><title>Refund</title></head><body>
<h1>Refund an order</h1>
<label>Amount <input id="amount" type="text"></label>
<input id="note" type="text" placeholder="Note">
<button id="refund">Refund</button>
<a href="/done" id="to-done">Finish</a>
<p id="status">idle</p>
<script>
const status = document.getElementById('status');
document.getElementById('refund').addEventListener('click', () => { status.textContent = 'refunded ' + document.getElementById('amount').value; });
document.getElementById('amount').addEventListener('keydown', e => { if (e.key === 'Enter') status.textContent = 'entered'; });
document.addEventListener('keydown', e => { if (e.key === 'Escape') status.textContent = 'escaped'; });
</script></body></html>`

const donePage = `<!doctype html><html><head><title>Done</title></head><body><h1>All done</h1><p id="result">finished</p></body></html>`

// recRig is a recorder on a real Chrome with no window, in front of a local page. A test sends input
// to the page through the protocol, as a person does with the mouse and the keyboard.
type recRig struct {
	t    *testing.T
	rec  *web.Recorder
	srv  *httptest.Server
	seen int
}

func newRecRig(t *testing.T, pages map[string]string) *recRig {
	t.Helper()
	needChrome(t)
	mux := http.NewServeMux()
	for path, body := range pages {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return recRigOn(t, srv)
}

// recRigOn starts a recorder in front of a server that the test already has.
func recRigOn(t *testing.T, srv *httptest.Server) *recRig {
	t.Helper()
	needChrome(t)
	rec, err := web.StartRecorder(context.Background(), web.RecorderOptions{
		Home: t.TempDir(), Environment: "test", Account: "default", Hidden: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Finish() })
	return &recRig{t: t, rec: rec, srv: srv}
}

// open types an address, as a person does in the address bar.
func (r *recRig) open(path string) {
	r.t.Helper()
	if err := r.rec.Open(r.srv.URL + path); err != nil {
		r.t.Fatal(err)
	}
	r.waitSettled()
}

func (r *recRig) waitSettled() {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ready, err := chromedp.Run(r.rec.Context(), chromedp.Evaluate[bool](`document.readyState === 'complete' && !!window.__semarRec`))
		if err == nil && ready {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.t.Fatal("the page did not load with the recorder in it")
}

func (r *recRig) center(sel string) (float64, float64) {
	r.t.Helper()
	pt, err := chromedp.Run(r.rec.Context(), chromedp.Evaluate[[2]float64](fmt.Sprintf(
		`(() => { const e = document.querySelector(%q); e.scrollIntoView({block: 'center'}); const b = e.getBoundingClientRect(); return [b.x + b.width / 2, b.y + b.height / 2]; })()`, sel)))
	if err != nil {
		r.t.Fatalf("find %s: %v", sel, err)
	}
	return pt[0], pt[1]
}

func (r *recRig) click(sel string) {
	r.t.Helper()
	x, y := r.center(sel)
	if err := chromedp.Do(r.rec.Context(), chromedp.MouseClickXY(x, y)); err != nil {
		r.t.Fatal(err)
	}
}

// altClick presses, and lets go of, the left button with the Option key held down.
func (r *recRig) altClick(sel string) {
	r.t.Helper()
	x, y := r.center(sel)
	for _, typ := range []input.DispatchMouseEventType{chromedp.MousePressed, chromedp.MouseReleased} {
		err := chromedp.Do(r.rec.Context(), chromedp.MouseEvent(typ, x, y, func(p *input.DispatchMouseEventParams) {
			p.Button, p.ClickCount, p.Modifiers = input.MouseButtonLeft, 1, int64(chromedp.ModifierAlt)
		}))
		if err != nil {
			r.t.Fatal(err)
		}
	}
}

// typeInto focuses a field with a click and sends one key event for each character.
func (r *recRig) typeInto(sel, text string) {
	r.t.Helper()
	r.click(sel)
	if err := chromedp.Do(r.rec.Context(), chromedp.KeyEvent(text)); err != nil {
		r.t.Fatal(err)
	}
}

func (r *recRig) key(k string) {
	r.t.Helper()
	if err := chromedp.Do(r.rec.Context(), chromedp.KeyEvent(k)); err != nil {
		r.t.Fatal(err)
	}
}

// drag presses on one element, moves in steps to another, and lets go.
func (r *recRig) drag(from, to string) {
	r.t.Helper()
	fx, fy := r.center(from)
	tx, ty := r.center(to)
	if err := chromedp.Do(r.rec.Context(), chromedp.DragAndDropXY(fx, fy, tx, ty)); err != nil {
		r.t.Fatal(err)
	}
}

// events waits for n more events than the test has taken, and gives them.
func (r *recRig) events(n int) []record.Event {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		all := r.rec.Events()
		if len(all) >= r.seen+n {
			got := all[r.seen : r.seen+n]
			r.seen += n
			return got
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("waited for %d events and got %d: %s", n, len(all)-r.seen, describeEvents(all[r.seen:]))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// quiet says that no event comes for a moment, which is how a test shows that an action is not recorded.
func (r *recRig) quiet() {
	r.t.Helper()
	time.Sleep(400 * time.Millisecond)
	if all := r.rec.Events(); len(all) > r.seen {
		r.t.Fatalf("an event came that was not expected: %s", describeEvents(all[r.seen:]))
	}
}

func describeEvents(evs []record.Event) string {
	b, _ := json.Marshal(evs)
	return string(b)
}

func pathOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Path
}

func TestRecorderSendsClicksFieldsKeysAndAddresses(t *testing.T) {
	r := newRecRig(t, map[string]string{"/refund": refundPage, "/done": donePage})

	r.open("/refund")
	ev := r.events(1)[0]
	if ev.Kind != record.KindOpen || pathOf(t, ev.Page.URL) != "/refund" {
		t.Fatalf("typing an address: %s", describeEvents([]record.Event{ev}))
	}

	// A field is read when it loses focus, and a click on a button is one event.
	r.typeInto("#amount", "25")
	r.click("#refund")
	got := r.events(2)
	if got[0].Kind != record.KindFill || got[0].Value != "25" || got[0].Page.Title != "Refund" {
		t.Errorf("the field: %s", describeEvents(got[:1]))
	}
	if got[1].Kind != record.KindClick || got[1].Target == nil {
		t.Errorf("the click: %s", describeEvents(got[1:]))
	}

	// Enter in a field sends the text first and then the key, so that a replay types before it presses.
	r.typeInto("#note", "paid")
	r.key(kb.Enter)
	got = r.events(2)
	if got[0].Kind != record.KindFill || got[0].Value != "paid" {
		t.Errorf("the text before Enter: %s", describeEvents(got[:1]))
	}
	if got[1].Kind != record.KindPress || got[1].Value != "Enter" || got[1].Target == nil {
		t.Errorf("Enter: %s", describeEvents(got[1:]))
	}

	r.key(kb.Escape)
	got = r.events(1)
	if got[0].Kind != record.KindPress || got[0].Value != "Escape" || got[0].Target != nil {
		t.Errorf("Escape: %s", describeEvents(got))
	}

	// Another key is no step, and a field that nobody changed since it was sent is not sent again.
	r.key(kb.Tab)
	r.quiet()

	// A link that a click follows is a click, and the move that it caused is not a step.
	r.click("#to-done")
	got = r.events(1)
	if got[0].Kind != record.KindClick {
		t.Errorf("the link: %s", describeEvents(got))
	}
	r.waitSettled()
	r.quiet()

	// An address that is typed is a step.
	if err := r.rec.Open(r.srv.URL + "/refund?from=done"); err != nil {
		t.Fatal(err)
	}
	got = r.events(1)
	if got[0].Kind != record.KindOpen || !strings.HasSuffix(got[0].Page.URL, "/refund?from=done") {
		t.Errorf("the second address: %s", describeEvents(got))
	}
}

const dragPage = `<!doctype html><html><head><title>Board</title></head><body>
<div id="todo" role="list" aria-label="Todo">
  <div class="card" role="listitem" draggable="true">Buy milk</div>
  <div class="card" role="listitem" draggable="true">Buy eggs</div>
</div>
<div id="doing" role="list" aria-label="Doing"><p class="hint">Drop here</p></div>
<div id="track" style="width:300px;height:30px;background:#ddd;position:relative">
  <div id="handle" style="width:30px;height:30px;background:#36c;position:absolute;left:0"></div>
  <div id="end" style="width:30px;height:30px;background:#c63;position:absolute;right:0"></div>
</div>
<p id="status">idle</p>
<script>
const doing = document.getElementById('doing'), status = document.getElementById('status');
let dragged = null;
document.addEventListener('dragstart', e => { dragged = e.target; e.dataTransfer.setData('text/plain', e.target.textContent); });
doing.addEventListener('dragover', e => e.preventDefault());
doing.addEventListener('drop', e => { e.preventDefault(); doing.appendChild(dragged); status.textContent = 'moved ' + dragged.textContent; });
const handle = document.getElementById('handle');
let holding = false;
handle.addEventListener('mousedown', () => { holding = true; });
document.addEventListener('mouseup', () => { holding = false; });
document.addEventListener('click', () => { status.textContent = 'clicked'; });
</script></body></html>`

func TestRecorderSendsADragAndNoClickAfterIt(t *testing.T) {
	r := newRecRig(t, map[string]string{"/board": dragPage})
	r.open("/board")
	r.events(1)

	// A card of the browser's own drag and drop.
	r.drag(".card", "#doing")
	got := r.events(1)
	if got[0].Kind != record.KindDrag || got[0].Target == nil || got[0].To == nil {
		t.Fatalf("the card: %s", describeEvents(got))
	}
	if got[0].Target.Hint != "Buy milk" || got[0].To.Locator.CSS != "#doing" {
		t.Errorf("the card is %q and the list is %+v", got[0].Target.Hint, got[0].To.Locator)
	}
	r.quiet()

	// A handle that the page moves with the mouse. The click that follows the release is not a step.
	r.drag("#handle", "#end")
	got = r.events(1)
	if got[0].Kind != record.KindDrag || got[0].Target.Locator.CSS != "#handle" || got[0].To.Locator.CSS != "#end" {
		t.Fatalf("the handle: %s", describeEvents(got))
	}
	r.quiet()

	// Pressing and letting go on the same element is a click.
	r.click("#end")
	if got = r.events(1); got[0].Kind != record.KindClick {
		t.Errorf("a click: %s", describeEvents(got))
	}
}

const locatorPage = `<!doctype html><html><head><title>Locators</title></head><body>
<button id="a1" data-testid="save-order">Save</button>
<button id="d1" data-testid="dup">One</button><button id="d2" data-testid="dup">Two</button>
<button id="p1">Place order</button><button id="p2" style="display:none">Place order</button>
<button id="c1">Cancel</button><button id="c2">Cancel</button>
<label>Email <input type="text" id="email"></label>
<input type="text" id="q" placeholder="Search">
<p class="note" id="n1">Remember to pay</p>
<span id="m1" data-qa="qa-mark">mark</span>
<a href="/nowhere" id="home" onclick="event.preventDefault()">Home</a>
<h2 id="h">Totals</h2>
<div role="button" id="x" aria-label="Close dialog">x</div>
<div class="box"><div class="item" style="height:20px;width:20px;background:#ccc"></div><div class="item" id="second" style="height:20px;width:20px;background:#999"></div></div>
</body></html>`

func TestRecorderChoosesLocatorsInTheOrderOfThePlan(t *testing.T) {
	rig := newRig(t, rigOptions{handler: func(mux *http.ServeMux) {
		mux.HandleFunc("/locators", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, locatorPage) })
	}})
	r := recRigOn(t, rig.srv)
	r.open("/locators")
	r.events(1)

	cases := []struct {
		sel  string
		want domain.Locator
		hint string
	}{
		{"#a1", domain.Locator{TestID: "save-order"}, "save-order"},
		{"#d1", domain.Locator{Role: "button", Name: "One"}, "One"},
		{"#p1", domain.Locator{Role: "button", Name: "Place order"}, "Place order"},
		{"#c2", domain.Locator{CSS: "#c2"}, "Cancel"},
		{"#home", domain.Locator{Role: "link", Name: "Home"}, "Home"},
		{"#h", domain.Locator{Role: "heading", Name: "Totals"}, "Totals"},
		{"#x", domain.Locator{Role: "button", Name: "Close dialog"}, "Close dialog"},
		{"#n1", domain.Locator{Text: "Remember to pay"}, "Remember to pay"},
		{"#m1", domain.Locator{CSS: `[data-qa="qa-mark"]`}, "qa-mark"},
		{"#second", domain.Locator{CSS: "#second"}, "second"},
	}
	for _, c := range cases {
		r.click(c.sel)
		ev := r.events(1)[0]
		if ev.Kind != record.KindClick || ev.Target == nil {
			t.Fatalf("%s: %s", c.sel, describeEvents([]record.Event{ev}))
		}
		if ev.Target.Locator != c.want || ev.Target.Hint != c.hint {
			t.Errorf("%s: locator %+v and hint %q, want %+v and %q", c.sel, ev.Target.Locator, ev.Target.Hint, c.want, c.hint)
		}
		// The engine of a replay must find that one element again by the same locator.
		res := rig.do(domain.WebAction{Do: domain.WebOpen, Path: "/locators"}, nil)
		_ = res
		got := rig.do(domain.WebAction{Do: domain.WebWait}, map[string]domain.Locator{"e": ev.Target.Locator}, text("e")).Action.Web.Elements["e"]
		if !got.Found || got.Count != 1 {
			t.Errorf("%s: the engine reads %+v for %+v", c.sel, got, ev.Target.Locator)
		}
	}

	// Fields are found by the label or the placeholder.
	r.typeInto("#email", "a@b.c")
	r.click("#a1")
	got := r.events(2)
	if got[0].Kind != record.KindFill || got[0].Target.Locator != (domain.Locator{Role: "textbox", Name: "Email"}) {
		t.Errorf("the email field: %s", describeEvents(got[:1]))
	}
	r.typeInto("#q", "milk")
	r.click("#a1")
	got = r.events(2)
	if got[0].Target.Locator != (domain.Locator{Role: "textbox", Name: "Search"}) {
		t.Errorf("the search field: %s", describeEvents(got[:1]))
	}
}

const checkPage = `<!doctype html><html><head><title>Totals</title></head><body>
<h2 id="h">Totals</h2>
<button id="go">Go</button>
<p id="status">idle</p>
<p id="sum">Total: <b>5.00</b></p>
<label>Amount <input id="amount" type="text" value="5"></label>
<label>Password <input id="pw" type="password" value="hunter2"></label>
<script>
document.getElementById('go').addEventListener('click', () => { document.getElementById('status').textContent = 'clicked'; });
document.addEventListener('mousedown', () => { document.getElementById('status').textContent = 'mousedown'; });
</script></body></html>`

func TestOptionClickRecordsACheckAndTheCheckedPageNeverSeesIt(t *testing.T) {
	r := newRecRig(t, map[string]string{"/totals": checkPage})
	r.open("/totals")
	r.events(1)

	for _, c := range []struct {
		sel   string
		want  string
		loc   domain.Locator
		hint  string
		label string
	}{
		{"#status", "idle", domain.Locator{CSS: "#status"}, "status", "a paragraph"},
		{"#go", "Go", domain.Locator{CSS: "#go"}, "go", "a button: its own text is what the check reads, so it is not the way to find it"},
		{"#h", "Totals", domain.Locator{CSS: "#h"}, "h", "a heading, for the same reason"},
		{"#amount", "5", domain.Locator{Role: "textbox", Name: "Amount"}, "Amount", "a field, found by its label"},
		{"#sum b", "5.00", domain.Locator{CSS: "#sum > b"}, "b", "text inside a paragraph"},
	} {
		r.altClick(c.sel)
		ev := r.events(1)[0]
		if ev.Kind != record.KindCheck || ev.Value != c.want || ev.Target == nil || ev.Target.Locator != c.loc || ev.Target.Hint != c.hint {
			t.Errorf("%s: %s, want the text %q at %+v called %q", c.label, describeEvents([]record.Event{ev}), c.want, c.loc, c.hint)
		}
		if ev.Page.Title != "Totals" {
			t.Errorf("%s: the page is %+v", c.label, ev.Page)
		}
	}
	r.quiet()

	// The page saw nothing: no mousedown, and no click on the button.
	status, err := chromedp.Run(r.rec.Context(), chromedp.Evaluate[string](`document.getElementById('status').textContent`))
	if err != nil || status != "idle" {
		t.Errorf("the page saw the Option click: status is %q, %v", status, err)
	}

	// The element shows an outline for a moment.
	r.altClick("#status")
	r.events(1)
	outline, err := chromedp.Run(r.rec.Context(), chromedp.Evaluate[string](`getComputedStyle(document.getElementById('status')).outlineStyle`))
	if err != nil || outline == "none" {
		t.Errorf("the checked element has outline %q, %v", outline, err)
	}

	// A password is never a check.
	r.altClick("#pw")
	r.quiet()

	// A click with no Option key is still a click.
	r.click("#go")
	if ev := r.events(1)[0]; ev.Kind != record.KindClick {
		t.Errorf("a click: %s", describeEvents([]record.Event{ev}))
	}
}

const signInPage = `<!doctype html><html><head><title>Sign in</title></head><body>
<label>Email <input type="text" id="email"></label>
<label>Password <input type="password" id="pw"></label>
<label>Code <input type="text" id="code" autocomplete="one-time-code"></label>
<label>New <input type="text" id="np" autocomplete="new-password"></label>
<button id="go">Sign in</button>
</body></html>`

func TestAPasswordIsNeverReadOrSent(t *testing.T) {
	const password = "hunter2-correct-horse"
	r := newRecRig(t, map[string]string{"/signin": signInPage})
	r.open("/signin?next=1#top")
	r.events(1)

	r.typeInto("#email", "ana@example.com")
	r.typeInto("#pw", password)
	r.key(kb.Enter)
	r.typeInto("#np", password+"-2")
	r.click("#go")
	got := r.events(5)
	// The email, the password and its Enter, the field that asks for a new password, and the button.
	if got[0].Kind != record.KindFill || got[0].Value != "ana@example.com" || got[0].Secret {
		t.Errorf("the email: %s", describeEvents(got[:1]))
	}
	for _, i := range []int{1, 3} {
		if got[i].Kind != record.KindFill || !got[i].Secret || got[i].Value != "" {
			t.Errorf("event %d should say that a secret was typed and nothing of it: %s", i, describeEvents(got[i:i+1]))
		}
	}
	if got[2].Kind != record.KindPress || got[2].Value != "Enter" {
		t.Errorf("Enter in the password field: %s", describeEvents(got[2:3]))
	}

	// The page that an event names is its path: a form that sends a password in the address would
	// otherwise put it in every event after it.
	if u := got[0].Page.URL; strings.ContainsAny(u, "?#") {
		t.Errorf("the page of a typed text has a query or a fragment: %s", u)
	}

	// Nothing that the recorder holds has the password, in any form that it was typed.
	all := describeEvents(r.rec.Events())
	if strings.Contains(all, password) || strings.Contains(all, "hunter2") {
		t.Errorf("a password is in the events: %s", all)
	}
	r.rec.Finish()
}
