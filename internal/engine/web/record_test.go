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
