package screens

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func additions() []Addition {
	return []Addition{
		{Screen: "Order", Elements: []Element{
			{Name: "refund", Locator: domain.Locator{Role: "button", Name: "Refund"}},
			{Name: "status", Locator: domain.Locator{CSS: "#status"}},
		}},
	}
}

func TestNewWritesAFileThatParsesToTheSameElements(t *testing.T) {
	data, err := New("shop", additions())
	if err != nil {
		t.Fatal(err)
	}
	want := `apiVersion: semar/v1
kind: Screens
app: shop
screens:
  Order:
    refund: { role: button, name: Refund }
    status: { css: "#status" }
`
	if string(data) != want {
		t.Errorf("file:\n%s\nwant\n%s", data, want)
	}
	s, err := Parse(data, Path("shop"), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := s.Locator("Order", "status"); l.CSS != "#status" {
		t.Errorf("status = %+v", l)
	}
}

const existing = `# the screens of the shop
apiVersion: semar/v1
kind: Screens
app: shop
screens:
  Cart:
    place-order: { role: button, name: Place order }   # the pay button
    # a note between two elements
    status: { css: "#status" }

  Order:
    pay: { role: button, name: Refund }
`

func TestAddKeepsEveryByteOfTheFileAndAddsBelowTheLastElement(t *testing.T) {
	out, err := Add([]byte(existing), "shop", []Addition{
		{Screen: "Order", Elements: []Element{{Name: "amount", Locator: domain.Locator{Role: "textbox", Name: "Amount"}}}},
		{Screen: "Receipt", Elements: []Element{{Name: "total", Locator: domain.Locator{CSS: "#total"}}, {Name: "paid", Locator: domain.Locator{Text: "Paid"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(existing, "    pay: { role: button, name: Refund }\n",
		"    pay: { role: button, name: Refund }\n    amount: { role: textbox, name: Amount }\n", 1) +
		"  Receipt:\n    total: { css: \"#total\" }\n    paid: { text: Paid }\n"
	if string(out) != want {
		t.Errorf("file:\n%s\nwant\n%s", out, want)
	}
	s, err := Parse(out, Path("shop"), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := s.Locator("Order", "pay"); !ok || l.Name != "Refund" {
		t.Errorf("an element that was there is changed: %+v", l)
	}
}

func TestAddToAScreenInTheMiddleOfTheFile(t *testing.T) {
	out, err := Add([]byte(existing), "shop", []Addition{
		{Screen: "Cart", Elements: []Element{{Name: "coupon", Locator: domain.Locator{TestID: "coupon"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(existing, "    status: { css: \"#status\" }\n", "    status: { css: \"#status\" }\n    coupon: { testId: coupon }\n", 1)
	if string(out) != want {
		t.Errorf("file:\n%s\nwant\n%s", out, want)
	}
}

func TestAddToAFileWithBlockElementsAndNothingAfterTheLastLine(t *testing.T) {
	file := "apiVersion: semar/v1\nkind: Screens\napp: shop\nscreens:\n    Cart:\n        pay:\n            role: button\n            name: Pay"
	out, err := Add([]byte(file), "shop", []Addition{
		{Screen: "Cart", Elements: []Element{{Name: "cancel", Locator: domain.Locator{Role: "button", Name: "Cancel"}}}},
		{Screen: "Done", Elements: []Element{{Name: "ok", Locator: domain.Locator{CSS: "#ok"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := file + "\n        cancel: { role: button, name: Cancel }\n    Done:\n        ok: { css: \"#ok\" }"
	if string(out) != want {
		t.Errorf("file:\n%s\nwant\n%s", out, want)
	}
}

func TestAddRefusesAnElementTheFileHas(t *testing.T) {
	_, err := Add([]byte(existing), "shop", []Addition{{Screen: "Cart", Elements: []Element{{Name: "status", Locator: domain.Locator{CSS: "#x"}}}}})
	if err == nil || !strings.Contains(err.Error(), "Cart already has the element status") {
		t.Errorf("error = %v", err)
	}
}

func TestAddRefusesAFileThatDoesNotParse(t *testing.T) {
	if _, err := Add([]byte("screens: [\n"), "shop", additions()); err == nil {
		t.Error("a broken file was extended")
	}
}
