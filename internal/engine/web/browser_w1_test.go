package web_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/web"
)

const ordersPage = `<!doctype html><html><head><title>Orders dashboard</title></head><body>
<h1>Orders</h1>
<a id="docs" href="/docs/orders" data-kind="help">Docs</a>
<label>Status <select id="status"><option>All</option><option>Paid</option><option>Refunded</option></select></label>
<p id="chosen">none</p>
<label><input type="checkbox" id="agree"> Agree</label><p id="agreed">no</p>
<label><input type="checkbox" id="news" checked> News</label>
<button id="menu">Menu</button><div id="panel" style="display:none">Menu open</div>
<label>File <input type="file" id="file"></label><p id="fname">no file</p>
<button id="save" disabled>Save</button><button id="send">Send</button>
<input id="secret" type="password" value="hunter2">
<div id="spinner">Loading</div>
<div id="late-host"></div>
<div class="tpl">first</div><div class="tpl" style="display:none">second</div><div class="tpl" style="display:none">third</div>
<ul><li class="row">one</li><li class="row">two</li><li class="row">three</li></ul>
<script>
document.getElementById('status').addEventListener('change', e => { document.getElementById('chosen').textContent = 'chose ' + e.target.value; });
document.getElementById('agree').addEventListener('change', e => { document.getElementById('agreed').textContent = e.target.checked ? 'yes' : 'no'; });
document.getElementById('menu').addEventListener('mouseenter', () => { document.getElementById('panel').style.display = 'block'; });
document.getElementById('file').addEventListener('change', e => { document.getElementById('fname').textContent = e.target.files[0].name + ' ' + e.target.files[0].size; });
setTimeout(() => document.getElementById('spinner').remove(), 700);
setTimeout(() => { document.getElementById('late-host').innerHTML = '<p id="late">Report ready</p>'; }, 700);
</script></body></html>`

var ordersLocs = map[string]domain.Locator{
	"docs":    {CSS: "#docs"},
	"status":  {Role: "combobox", Name: "Status"},
	"chosen":  {CSS: "#chosen"},
	"agree":   {Role: "checkbox", Name: "Agree"},
	"news":    {Role: "checkbox", Name: "News"},
	"agreed":  {CSS: "#agreed"},
	"menu":    {Role: "button", Name: "Menu"},
	"panel":   {CSS: "#panel"},
	"file":    {CSS: "#file"},
	"fname":   {CSS: "#fname"},
	"save":    {Role: "button", Name: "Save"},
	"send":    {Role: "button", Name: "Send"},
	"secret":  {CSS: "#secret"},
	"spinner": {CSS: "#spinner"},
	"late":    {CSS: "#late"},
	"tpl":     {CSS: "div.tpl"},
	"rows":    {CSS: "li.row"},
	"nothing": {CSS: "#nothing"},
}

func ordersRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	needChrome(t)
	o.handler = func(mux *http.ServeMux) {
		mux.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, ordersPage) })
	}
	r := newRig(t, o)
	r.do(open("/orders"), nil)
	return r
}

// reads waits for the page and gives the action that reads the named elements, with the attributes of
// the first element when there are any.
func (r *rig) reads(attrs map[string][]string, names ...string) domain.Action {
	r.t.Helper()
	var watch []web.Watched
	for _, n := range names {
		watch = append(watch, web.Watched{Element: n, Attrs: attrs[n]})
	}
	return r.do(domain.WebAction{Do: domain.WebWait}, ordersLocs, watch...).Action
}

func verdict(c domain.Check, a domain.Action) check.Result { return check.Eval(c, a) }

func yes(b bool) *bool { return &b }

func TestElementStateChecks(t *testing.T) {
	r := ordersRig(t, rigOptions{})
	a := r.reads(nil, "save", "send", "news", "agree", "panel", "nothing", "docs")
	for _, c := range []struct {
		name  string
		check domain.Check
		want  domain.CheckState
	}{
		{"disabled button is disabled", domain.Check{Element: "save", State: "disabled"}, domain.CheckTrue},
		{"disabled button is not enabled", domain.Check{Element: "save", State: "enabled"}, domain.CheckFalse},
		{"button is enabled", domain.Check{Element: "send", State: "enabled"}, domain.CheckTrue},
		{"box that is checked", domain.Check{Element: "news", State: "checked"}, domain.CheckTrue},
		{"box that is not checked", domain.Check{Element: "agree", State: "unchecked"}, domain.CheckTrue},
		{"box that is not checked is not checked", domain.Check{Element: "agree", State: "checked"}, domain.CheckFalse},
		{"hidden panel is hidden", domain.Check{Element: "panel", State: "hidden"}, domain.CheckTrue},
		{"hidden panel is not visible", domain.Check{Element: "panel", State: "visible"}, domain.CheckFalse},
		{"shown button is visible", domain.Check{Element: "send", State: "visible"}, domain.CheckTrue},
		{"an element that is not there is hidden", domain.Check{Element: "nothing", State: "hidden"}, domain.CheckTrue},
		{"an element that is not there is not visible", domain.Check{Element: "nothing", State: "visible"}, domain.CheckFalse},
		{"an element that is not there is not on the page", domain.Check{Element: "nothing", Exists: yes(false)}, domain.CheckTrue},
	} {
		if got := verdict(c.check, a); got.State != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got.State, got.Detail, c.want)
		}
	}
}

func TestAttributeChecksAndAPasswordValueIsNeverRead(t *testing.T) {
	r := ordersRig(t, rigOptions{})
	a := r.reads(map[string][]string{"docs": {"href", "data-kind", "title"}, "secret": {"value", "type"}}, "docs", "secret")
	for _, c := range []struct {
		name  string
		check domain.Check
		want  domain.CheckState
	}{
		{"href equals", domain.Check{Element: "docs", Attribute: "href", Equals: "/docs/orders"}, domain.CheckTrue},
		{"href contains", domain.Check{Element: "docs", Attribute: "href", Contains: "orders"}, domain.CheckTrue},
		{"href differs", domain.Check{Element: "docs", Attribute: "href", Equals: "/docs/invoices"}, domain.CheckFalse},
		{"data attribute", domain.Check{Element: "docs", Attribute: "data-kind", Equals: "help"}, domain.CheckTrue},
		{"an attribute the element lacks does not equal", domain.Check{Element: "docs", Attribute: "title", Equals: "x"}, domain.CheckFalse},
		{"an attribute the element lacks does not equal, notEquals", domain.Check{Element: "docs", Attribute: "title", NotEquals: "x"}, domain.CheckTrue},
		{"a password has no value to read", domain.Check{Element: "secret", Attribute: "value", Equals: "hunter2"}, domain.CheckFalse},
		{"another attribute of a password is read", domain.Check{Element: "secret", Attribute: "type", Equals: "password"}, domain.CheckTrue},
	} {
		if got := verdict(c.check, a); got.State != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got.State, got.Detail, c.want)
		}
	}
	if st := a.Web.Elements["secret"]; st.Attrs["value"] != "" {
		t.Errorf("the value of the password was read: %+v", st.Attrs)
	}
}

func TestPageChecks(t *testing.T) {
	r := ordersRig(t, rigOptions{})
	a := r.reads(nil)
	for _, c := range []struct {
		check domain.Check
		want  domain.CheckState
	}{
		{domain.Check{Page: "path", Equals: "/orders"}, domain.CheckTrue},
		{domain.Check{Page: "path", Contains: "ord"}, domain.CheckTrue},
		{domain.Check{Page: "path", NotEquals: "/orders"}, domain.CheckFalse},
		{domain.Check{Page: "title", Equals: "Orders dashboard"}, domain.CheckTrue},
		{domain.Check{Page: "title", Contains: "Invoices"}, domain.CheckFalse},
	} {
		if got := verdict(c.check, a); got.State != c.want {
			t.Errorf("%+v: %s (%s), want %s", c.check, got.State, got.Detail, c.want)
		}
	}
}

func TestACheckWaitsForItsCondition(t *testing.T) {
	r := ordersRig(t, rigOptions{checkTimeout: 5 * time.Second})
	until := []domain.Check{{Element: "late", Equals: "Report ready"}, {Element: "spinner", Exists: yes(false)}}
	began := time.Now()
	res, err := r.tryUntil(domain.WebAction{Do: domain.WebWait}, ordersLocs, until, text("late"), text("spinner"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range until {
		if got := verdict(c, res.Action); got.State != domain.CheckTrue {
			t.Errorf("%+v: %s (%s)", c, got.State, got.Detail)
		}
	}
	if took := time.Since(began); took > 4*time.Second {
		t.Errorf("the check waited %s for a page that was ready after about a second", took)
	}
}

func TestACheckThatNeverHoldsEndsAtTheTimeLimit(t *testing.T) {
	r := ordersRig(t, rigOptions{checkTimeout: 700 * time.Millisecond})
	until := []domain.Check{{Element: "chosen", Equals: "never"}}
	began := time.Now()
	res, err := r.tryUntil(domain.WebAction{Do: domain.WebWait}, ordersLocs, until, text("chosen"))
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(began)
	if got := verdict(until[0], res.Action); got.State != domain.CheckFalse || got.Observed != "none" {
		t.Errorf("verdict = %+v", got)
	}
	if took < 600*time.Millisecond || took > 4*time.Second {
		t.Errorf("the check took %s with a limit of 700ms", took)
	}
}

func TestACheckThatAnElementIsNotThereIsNotAnsweredBeforeThePageIsQuiet(t *testing.T) {
	r := ordersRig(t, rigOptions{checkTimeout: 5 * time.Second})
	until := []domain.Check{{Element: "nothing", Exists: yes(false)}}
	began := time.Now()
	res, err := r.tryUntil(domain.WebAction{Do: domain.WebWait}, ordersLocs, until, text("nothing"))
	if err != nil {
		t.Fatal(err)
	}
	if got := verdict(until[0], res.Action); got.State != domain.CheckTrue {
		t.Errorf("verdict = %+v", got)
	}
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("an absence that held at once took %s", took)
	}
}

func TestALocatorThatMatchesSeveralElementsIsNotAPass(t *testing.T) {
	r := ordersRig(t, rigOptions{checkTimeout: 300 * time.Millisecond})
	until := []domain.Check{{Element: "tpl", Equals: "first"}, {Element: "rows", Equals: "one"}, {Element: "tpl", State: "visible"}, {Element: "tpl", Attribute: "class", Equals: "tpl"}}
	res, err := r.tryUntil(domain.WebAction{Do: domain.WebWait}, ordersLocs, until, text("tpl"), text("rows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range until {
		got := verdict(c, res.Action)
		if got.State != domain.CheckNotEvaluated || !strings.HasPrefix(got.Detail, "the locator matches ") || !strings.Contains(got.Detail, " elements") {
			t.Errorf("%+v: %s %q", c, got.State, got.Detail)
		}
	}
	if st := res.Action.Web.Elements["tpl"]; st.Count != 3 || !st.Found {
		t.Fatalf("tpl = %+v: the page has three of them and one is shown, which is the case that passed before", st)
	}
	if got := verdict(until[0], res.Action); !strings.Contains(got.Detail, "the locator matches 3 elements: ") || !strings.Contains(got.Detail, "text: first") {
		t.Errorf("the reason does not name the elements: %q", got.Detail)
	}
	named := map[string]int{}
	for _, cand := range res.Action.Web.Repair {
		named[cand.Element]++
	}
	if named["tpl"] != 3 || named["rows"] != 3 {
		t.Errorf("the elements were not named: %+v", res.Action.Web.Repair)
	}
	for _, c := range []domain.Check{{Element: "tpl", Exists: yes(true)}, {Element: "rows", Count: ptr(3)}, {Element: "tpl", State: "hidden"}} {
		if got := verdict(c, res.Action); got.State == domain.CheckNotEvaluated {
			t.Errorf("%+v asks how many or whether any are shown, and is no reason to refuse: %+v", c, got)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestSelectAnOptionByItsLabel(t *testing.T) {
	r := ordersRig(t, rigOptions{})
	w := act(domain.WebSelect, "status")
	w.Value = "Refunded"
	res := r.do(w, ordersLocs, text("chosen"), text("status"))
	if res.Action.Err != "" {
		t.Fatalf("action = %+v", res.Action)
	}
	if got := res.Action.Web.Elements["chosen"].Text; got != "chose Refunded" {
		t.Errorf("the page heard %q", got)
	}
	if got := res.Action.Web.Elements["status"].Text; got != "Refunded" {
		t.Errorf("the list shows %q", got)
	}
	w.Value = "Cancelled"
	res = r.do(w, ordersLocs)
	if !strings.Contains(res.Action.Err, `no option with the label "Cancelled"`) {
		t.Errorf("error = %q", res.Action.Err)
	}
}

func TestHoverShowsWhatOnlyAHoverShows(t *testing.T) {
	r := ordersRig(t, rigOptions{})
	if a := r.reads(nil, "panel"); a.Web.Elements["panel"].Visible {
		t.Fatal("the panel shows before the hover")
	}
	res := r.do(act(domain.WebHover, "menu"), ordersLocs, text("panel"))
	if res.Action.Err != "" || res.Action.Refused != "" {
		t.Fatalf("action = %+v", res.Action)
	}
	if st := res.Action.Web.Elements["panel"]; !st.Found || st.Text != "Menu open" {
		t.Errorf("panel = %+v", st)
	}
}

func TestCheckAndUncheckABoxAndLeaveOneThatIsInTheState(t *testing.T) {
	r := ordersRig(t, rigOptions{})
	for _, step := range []struct {
		do      domain.WebOp
		element string
		checked bool
		heard   string
	}{
		{domain.WebCheck, "agree", true, "yes"},
		{domain.WebCheck, "agree", true, "yes"},
		{domain.WebUncheck, "agree", false, "no"},
		{domain.WebUncheck, "news", false, ""},
		{domain.WebCheck, "news", true, ""},
	} {
		res := r.do(act(step.do, step.element), ordersLocs, text(step.element), text("agreed"))
		if res.Action.Err != "" {
			t.Fatalf("%s %s: %s", step.do, step.element, res.Action.Err)
		}
		if got := res.Action.Web.Elements[step.element].Checked; got != step.checked {
			t.Errorf("%s %s: checked = %v", step.do, step.element, got)
		}
		if step.heard != "" && res.Action.Web.Elements["agreed"].Text != step.heard {
			t.Errorf("%s %s: the page shows %q", step.do, step.element, res.Action.Web.Elements["agreed"].Text)
		}
	}
	res := r.do(act(domain.WebCheck, "send"), ordersLocs)
	if !strings.Contains(res.Action.Err, "not a checkbox") {
		t.Errorf("error = %q", res.Action.Err)
	}
}

func TestUploadAFileFromTheFilesFolder(t *testing.T) {
	files := filepath.Join(t.TempDir(), ".semar", "files")
	if err := os.MkdirAll(filepath.Join(files, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "docs", "invoice.txt"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(filepath.Dir(files)), "outside.txt")
	if err := os.WriteFile(outside, []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(files, "link.txt")); err != nil {
		t.Fatal(err)
	}
	r := ordersRig(t, rigOptions{uploads: files})

	w := act(domain.WebUpload, "file")
	w.Value = "docs/invoice.txt"
	res := r.do(w, ordersLocs, text("fname"))
	if res.Action.Err != "" || res.Action.Refused != "" {
		t.Fatalf("action = %+v", res.Action)
	}
	if got := res.Action.Web.Elements["fname"].Text; got != "invoice.txt 5" {
		t.Errorf("the page heard %q", got)
	}

	for _, refused := range []struct{ value, want string }{
		{"../outside.txt", "must stay below"},
		{"docs/../../outside.txt", "must stay below"},
		{outside, "no absolute part"},
		{"link.txt", "leaves .semar/files/"},
		{"docs/missing.txt", "was not found"},
		{"docs", "is not a file"},
	} {
		w.Value = refused.value
		res, err := r.try(w, ordersLocs)
		if err != nil {
			t.Fatalf("%s: %v", refused.value, err)
		}
		if !strings.Contains(res.Action.Refused, refused.want) {
			t.Errorf("%s: refused = %q, want %q", refused.value, res.Action.Refused, refused.want)
		}
		if strings.Contains(res.Action.Refused, filepath.Dir(files)) {
			t.Errorf("%s: the refusal holds a path of the machine: %q", refused.value, res.Action.Refused)
		}
	}
	if a := r.reads(nil, "fname"); a.Web.Elements["fname"].Text != "invoice.txt 5" {
		t.Errorf("a refused upload changed the page: %q", a.Web.Elements["fname"].Text)
	}
}

func TestAnUploadWithNoFolderIsRefused(t *testing.T) {
	r := ordersRig(t, rigOptions{})
	w := act(domain.WebUpload, "file")
	w.Value = "invoice.txt"
	res := r.do(w, ordersLocs)
	if !strings.Contains(res.Action.Refused, "needs the repository") {
		t.Errorf("refused = %q", res.Action.Refused)
	}
}
