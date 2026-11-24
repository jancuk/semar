package screens

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

var commonAdd = Addition{Common: true, Elements: []Element{
	{Name: "back", Locator: domain.Locator{Desc: "Navigate up"}},
	{Name: "third", Locator: domain.Locator{Role: "button", Name: "Delete", Index: 3}},
}}

func TestNewWritesACommonBlock(t *testing.T) {
	data, err := New("shop", append([]Addition{commonAdd}, additions()...))
	if err != nil {
		t.Fatal(err)
	}
	want := "apiVersion: semar/v1\nkind: Screens\napp: shop\ncommon:\n  back: { desc: Navigate up }\n  third: { role: button, name: Delete, index: 3 }\n" +
		"screens:\n  Order:\n    refund: { role: button, name: Refund }\n    status: { css: \"#status\" }\n"
	if string(data) != want {
		t.Errorf("file:\n%s\nwant\n%s", data, want)
	}
	only, err := New("shop", []Addition{commonAdd})
	if err != nil || strings.Contains(string(only), "screens:") {
		t.Errorf("only common: %v\n%s", err, only)
	}
}

func TestAddCommonWithoutABlockAppendsOneAndKeepsTheRestOfTheFile(t *testing.T) {
	out, err := Add([]byte(existing), "shop", []Addition{commonAdd})
	if err != nil {
		t.Fatal(err)
	}
	want := existing + "common:\n  back: { desc: Navigate up }\n  third: { role: button, name: Delete, index: 3 }\n"
	if !strings.HasSuffix(existing, "\n") {
		want = existing + "\ncommon:\n  back: { desc: Navigate up }\n  third: { role: button, name: Delete, index: 3 }"
	}
	if string(out) != want {
		t.Errorf("file:\n%s\nwant\n%s", out, want)
	}
	s, err := Parse(out, Path("shop"), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := s.Locator("Anywhere", "third"); !ok || l.Index != 3 {
		t.Errorf("third = %+v, %v", l, ok)
	}
}

const withBlock = `apiVersion: semar/v1
kind: Screens
app: shop
common:
  # shared
  nav: { id: nav }
screens:
  Order:
    pay: { role: button, name: Refund }
`

func TestAddCommonToAnExistingBlock(t *testing.T) {
	out, err := Add([]byte(withBlock), "shop", []Addition{commonAdd})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(withBlock, "  nav: { id: nav }\n",
		"  nav: { id: nav }\n  back: { desc: Navigate up }\n  third: { role: button, name: Delete, index: 3 }\n", 1)
	if string(out) != want {
		t.Errorf("file:\n%s\nwant\n%s", out, want)
	}
	// Adding a screen element after a common one still goes below the last screen.
	out, err = Add(out, "shop", []Addition{{Screen: "Order", Elements: []Element{{Name: "x", Locator: domain.Locator{ID: "x"}}}}})
	if err != nil || !strings.Contains(string(out), "    pay: { role: button, name: Refund }\n    x: { id: x }\n") {
		t.Errorf("%v\n%s", err, out)
	}
	if _, err := Add([]byte(withBlock), "shop", []Addition{{Common: true, Elements: []Element{{Name: "nav", Locator: domain.Locator{ID: "n"}}}}}); err == nil ||
		!strings.Contains(err.Error(), "already have the element nav") {
		t.Errorf("a duplicate: %v", err)
	}
}

func TestAddCommonKeepsCRLF(t *testing.T) {
	crlf := strings.ReplaceAll(withBlock, "\n", "\r\n")
	out, err := Add([]byte(crlf), "shop", []Addition{commonAdd})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(crlf, "  nav: { id: nav }\r\n",
		"  nav: { id: nav }\r\n  back: { desc: Navigate up }\r\n  third: { role: button, name: Delete, index: 3 }\r\n", 1)
	if string(out) != want {
		t.Errorf("file:\n%q\nwant\n%q", out, want)
	}
	noBlock := strings.ReplaceAll(existing, "\n", "\r\n")
	out, err = Add([]byte(noBlock), "shop", []Addition{commonAdd})
	if err != nil || strings.Contains(strings.ReplaceAll(string(out), "\r\n", ""), "\n") {
		t.Errorf("a bare line feed was written: %v\n%q", err, out)
	}
}

func TestAddAScreenToAFileThatHoldsOnlyCommon(t *testing.T) {
	only, err := New("shop", []Addition{commonAdd})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Add(only, "shop", []Addition{{Screen: "Order", Elements: []Element{{Name: "pay", Locator: domain.Locator{Role: "button", Name: "Pay"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := string(only) + "screens:\n  Order:\n    pay: { role: button, name: Pay }\n"
	if string(out) != want {
		t.Errorf("file:\n%s\nwant\n%s", out, want)
	}
	s, err := Parse(out, Path("shop"), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := s.Locator("Order", "pay"); !ok || l.Name != "Pay" {
		t.Errorf("pay = %+v, %v", l, ok)
	}
	if _, ok := s.Locator("Order", "back"); !ok {
		t.Error("the common element is gone")
	}
}
