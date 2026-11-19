package web_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/engine/web"
)

const listPage = `<!doctype html><html><head><title>Board</title></head><body>
<h1>Orders</h1>
<label>Amount <input id="a" type="text" value="5"></label>
<input type="password" value="s3cret-pass" aria-label="Password">
<button disabled>Refund</button>
<button>Save <span>now</span></button>
<a href="/boards">Boards</a>
<p>Total: <b>5.00</b></p>
<div style="display:none"><button>Hidden</button></div>
<button style="visibility:hidden">Invisible</button>
<div draggable="true">Card A</div>
<input type="checkbox" checked aria-label="Paid">
<textarea placeholder="Notes"></textarea>
<div class="badge">Overdue</div>
<div style="height:3000px"></div>
<button>Far away</button>
</body></html>`

func TestThePageListNamesRolesNamesValuesAndStates(t *testing.T) {
	r := newRecRig(t, map[string]string{"/board": listPage})
	r.open("/board")
	l, err := web.NewExplorer(r.rec).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := l.String()
	for _, want := range []string{
		"Title: Board\n",
		`heading "Orders"`,
		`textbox "Amount" value="5"`,
		`textbox "Password" type="password"` + "\n",
		`button "Refund" disabled`,
		`button "Save now"`,
		`link "Boards"`,
		`text "Total: 5.00"`,
		`text "5.00"`,
		`draggable "Card A"`,
		`checkbox "Paid" checked`,
		`textbox "Notes"`,
		`text "Overdue"`,
		`button "Far away"`, // out of the view: listed, and the browser scrolls to it
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the list lacks %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"Hidden", "Invisible", "s3cret-pass"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the list holds %q:\n%s", unwanted, got)
		}
	}
	if strings.Contains(got, `text "Save`) || strings.Contains(got, `text "now"`) {
		t.Errorf("a button is listed twice, once as its text:\n%s", got)
	}
}

func TestThePageListHolds150LinesAndCutsTextsAt80Characters(t *testing.T) {
	var b strings.Builder
	b.WriteString("<!doctype html><title>Many</title><body>")
	for i := range 200 {
		fmt.Fprintf(&b, "<p>row %d</p>", i)
	}
	for i := range 40 {
		fmt.Fprintf(&b, "<button>button %d %s</button>", i, strings.Repeat("long ", 30))
	}
	b.WriteString("</body>")
	r := newRecRig(t, map[string]string{"/many": b.String()})
	r.open("/many")
	l, err := web.NewExplorer(r.rec).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Elements) != 150 || l.Omitted != 90 {
		t.Errorf("%d elements and %d omitted, want 150 and 90", len(l.Elements), l.Omitted)
	}
	buttons := 0
	for _, e := range l.Elements {
		if e.Role == "button" {
			buttons++
			if n := len([]rune(e.Name)); n != 80 {
				t.Fatalf("a button name has %d characters: %q", n, e.Name)
			}
		}
	}
	if buttons != 40 {
		t.Errorf("%d buttons are listed, want all 40", buttons)
	}
}

const attrPage = `<!doctype html><html><head><title>Account</title></head><body>
<label>Email <input id="e" type="email" name="email" placeholder="you@shop.example"></label>
<label>Password <input id="p" type="password" name="pw" placeholder="Your password" value="hunter2-secret" autocomplete="current-password"></label>
<input type="text" aria-label="Pass" autocomplete="current-password" value="also-secret">
<a href="/orders?id=1" title="All orders">Orders</a>
<img alt="Logo" src="/x.png">
<div data-testid="drawer" style="display:none">Drawer</div>
<div role="dialog" style="display:none">Dialog text</div>
<div style="display:none"><button>Buried</button></div>
</body></html>`

func TestThePageListCarriesTheAttributesOfACheckAndTheHiddenElementsOfAuthors(t *testing.T) {
	r := newRecRig(t, map[string]string{"/account": attrPage})
	r.open("/account")
	l, err := web.NewExplorer(r.rec).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := l.String()
	for _, want := range []string{
		`textbox "Email" placeholder="you@shop.example" name="email" type="email"`,
		`textbox "Password" placeholder="Your password" name="pw" type="password"` + "\n",
		`link "Orders" href="/orders?id=1" title="All orders"`,
		`img "Logo"` + "\n",
		`generic "Drawer" hidden`,
		`dialog "Dialog text" hidden`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the list lacks %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"hunter2", "also-secret", "Buried"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the list holds %q:\n%s", unwanted, got)
		}
	}
}
