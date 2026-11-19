package web_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/record"
)

const formsPage = `<!doctype html><html><head><title>Orders dashboard</title></head><body>
<h1>Orders</h1>
<label>Search <input id="q" type="search" placeholder="Search orders"></label>
<a href="/orders/new">New order</a>
<button id="save" disabled>Save</button>
<label><input type="checkbox" id="paid" checked> Paid</label>
<button id="del">Remove first</button>
<ul id="rows"><li>row one</li><li>row two</li><li>row three</li></ul>
<div id="banner" data-testid="banner">Welcome back <button id="close">Close</button></div>
<div data-testid="dup">Alpha</div><div data-testid="dup" style="display:none">Beta</div>
<input type="password" id="pw" value="hunter2-secret" autocomplete="current-password">
<script>
document.getElementById('del').addEventListener('click', () => document.querySelector('#rows li').remove());
document.getElementById('close').addEventListener('click', () => { document.getElementById('banner').style.display = 'none'; });
</script></body></html>`

// forms opens the page and gives the explorer and its list.
func formsRig(t *testing.T) (*recRig, *web.Explorer, func(checks ...domain.Check)) {
	t.Helper()
	r := newRecRig(t, map[string]string{"/orders": formsPage})
	x, _ := r.explore("/orders")
	prepare := func(checks ...domain.Check) { x.Prepare(t.Context(), checks) }
	return r, x, prepare
}

func TestACheckOfEveryFormIsDecidedOnThePageAsARunDecidesIt(t *testing.T) {
	r, x, prepare := formsRig(t)
	l, err := x.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	save := refOf(t, l, "button", "Save")
	paid := refOf(t, l, "checkbox", "Paid")
	search := refOf(t, l, "textbox", "Search")
	link := refOf(t, l, "link", "New order")
	row := refOf(t, l, "text", "row one")
	for name, tc := range map[string]struct {
		check domain.Check
		held  bool
		shown string
	}{
		"disabled":             {domain.Check{Element: save, State: "disabled"}, true, ""},
		"enabled":              {domain.Check{Element: save, State: "enabled"}, false, "shown, disabled, not checked"},
		"checked":              {domain.Check{Element: paid, State: "checked"}, true, ""},
		"unchecked":            {domain.Check{Element: paid, State: "unchecked"}, false, "shown, enabled, checked"},
		"visible":              {domain.Check{Element: save, State: "visible"}, true, ""},
		"hidden":               {domain.Check{Element: save, State: "hidden"}, false, "shown, disabled, not checked"},
		"placeholder":          {domain.Check{Element: search, Attribute: "placeholder", Equals: "Search orders"}, true, ""},
		"wrong placeholder":    {domain.Check{Element: search, Attribute: "placeholder", Equals: "Find"}, false, `"Search orders"`},
		"href contains":        {domain.Check{Element: link, Attribute: "href", Contains: "/orders/"}, true, ""},
		"missing attribute":    {domain.Check{Element: link, Attribute: "title", Equals: "x"}, false, "no attribute title"},
		"path":                 {domain.Check{Page: "path", Equals: "/orders"}, true, ""},
		"wrong path":           {domain.Check{Page: "path", Equals: "/refund"}, false, `"/orders"`},
		"title":                {domain.Check{Page: "title", Contains: "dashboard"}, true, ""},
		"a row exists":         {domain.Check{Element: row, Exists: yes(true)}, true, ""},
		"a row does not exist": {domain.Check{Element: row, Exists: yes(false)}, false, "1 element: row one"},
		"three items":          {domain.Check{Element: row, Count: ptr(3)}, true, ""},
		"two items":            {domain.Check{Element: row, Count: ptr(2)}, false, "3 elements"},
	} {
		t.Run(name, func(t *testing.T) {
			prepare(tc.check)
			got, err := x.Test(t.Context(), tc.check)
			if err != nil {
				t.Fatal(err)
			}
			if got.Held != tc.held {
				t.Fatalf("held = %v, shown %q", got.Held, got.Shown)
			}
			if !tc.held && got.Shown != tc.shown {
				t.Errorf("shown = %q, want %q", got.Shown, tc.shown)
			}
		})
	}
	if len(r.rec.Events()) != 1 {
		t.Errorf("a test of a check recorded %d events, want only the open", len(r.rec.Events()))
	}
}

func TestACheckThatAnElementIsGoneNamesAnElementThatAnActionOfTheTurnRemoves(t *testing.T) {
	r, x, prepare := formsRig(t)
	l, err := x.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	remove := refOf(t, l, "button", "Remove first")
	row := refOf(t, l, "text", "row one")
	banner := refOf(t, l, "text", "Welcome back Close")
	closeBtn := refOf(t, l, "button", "Close")
	gone := domain.Check{Element: row, Exists: yes(false)}
	hidden := domain.Check{Element: banner, State: "hidden"}
	two := domain.Check{Element: row, Count: ptr(2)}
	prepare(gone, hidden, two)
	// The element is on the page, so the check does not hold yet, and nothing is recorded by a test of it.
	before, err := x.Test(t.Context(), gone)
	if err != nil || before.Held {
		t.Fatalf("before the action: %+v, %v", before, err)
	}
	if err := x.Click(t.Context(), remove); err != nil {
		t.Fatal(err)
	}
	if err := x.Click(t.Context(), closeBtn); err != nil {
		t.Fatal(err)
	}
	for _, c := range []domain.Check{gone, hidden} {
		if _, err := x.Assert(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	// A count of the rows reads the group that the element belonged to, not the element.
	if got, err := x.Test(t.Context(), two); err != nil || !got.Held {
		t.Errorf("two rows are left: %+v, %v", got, err)
	}
	evs := r.rec.Events()
	var probes []record.Probe
	for _, ev := range evs {
		if ev.Kind == record.KindCheck {
			probes = append(probes, *ev.Probe)
			if ev.Target == nil || ev.Page.Title != "Orders dashboard" {
				t.Errorf("event = %+v", ev)
			}
		}
	}
	if len(probes) != 2 || !probes[0].Held || !probes[1].Held {
		t.Errorf("probes = %+v, events:\n%s", probes, describeEvents(evs))
	}
}

func TestACheckOfAnElementWhoseLocatorMatchesSeveralIsRefusedWithTheReasonOfARun(t *testing.T) {
	r, x, prepare := formsRig(t)
	l, err := x.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	alpha := refOf(t, l, "text", "Alpha")
	for name, run := range map[string]func() error{
		"a state": func() error {
			c := domain.Check{Element: alpha, State: "enabled"}
			prepare(c)
			_, err := x.Test(t.Context(), c)
			return err
		},
		"an attribute": func() error {
			c := domain.Check{Element: alpha, Attribute: "title", Equals: "x"}
			prepare(c)
			_, err := x.Assert(t.Context(), c)
			return err
		},
		"a text": func() error { _, err := x.Check(t.Context(), alpha); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.Contains(err.Error(), "the locator matches 2 elements") || !strings.Contains(err.Error(), "name another element") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	for _, ev := range r.rec.Events() {
		if ev.Kind == record.KindCheck {
			t.Errorf("a refused check was recorded: %+v", ev)
		}
	}
	// A check that an element is not there, or how many there are, is an answer for several matches: the
	// three divs of the page are the group of the element.
	c := domain.Check{Element: alpha, Count: ptr(3)}
	prepare(c)
	if got, err := x.Test(t.Context(), c); err != nil || !got.Held {
		t.Errorf("a count of two: %+v, %v", got, err)
	}
}

func TestACheckNamesNoElementThatWasNotPrepared(t *testing.T) {
	_, x, _ := formsRig(t)
	if _, err := x.Test(t.Context(), domain.Check{Element: "e1", State: "hidden"}); err == nil || !strings.Contains(err.Error(), "was not prepared") {
		t.Errorf("err = %v", err)
	}
}
