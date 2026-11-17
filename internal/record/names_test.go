package record

import (
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestSlug(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Place order", "place-order"},
		{"save-order", "save-order"},
		{"  Close   dialog! ", "close-dialog"},
		{"Total (USD)", "total-usd"},
		{"", ""},
		{"???", ""},
		{"Résumé", "r-sum"},
		{"A very long name that goes on and on and on past the limit of an element name", "a-very-long-name-that-goes-on-and-on-and"},
	} {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNamerNumbersTwoElementsThatWouldBeCalledTheSame(t *testing.T) {
	n := NewNamer(nil)
	cancel1 := domain.Locator{CSS: "#c1"}
	cancel2 := domain.Locator{CSS: "#c2"}
	if got := n.Name("Cancel", cancel1); got != "cancel" {
		t.Errorf("first = %q", got)
	}
	if got := n.Name("Cancel", cancel2); got != "cancel-2" {
		t.Errorf("second = %q", got)
	}
	if got := n.Name("Cancel", cancel1); got != "cancel" {
		t.Errorf("the first again = %q: the same element keeps its name", got)
	}
	if got := n.Name("Cancel", domain.Locator{CSS: "#c3"}); got != "cancel-3" {
		t.Errorf("third = %q", got)
	}
	if got := n.Name("", domain.Locator{CSS: "div"}); got != "element" {
		t.Errorf("no hint = %q", got)
	}
}

func TestNamerKeepsTheElementsOfTheFile(t *testing.T) {
	n := NewNamer(map[string]domain.Locator{
		"pay":    {Role: "button", Name: "Place order"},
		"cancel": {Role: "button", Name: "Cancel order"},
	})
	if got := n.Name("Place order", domain.Locator{Role: "button", Name: "Place order"}); got != "pay" {
		t.Errorf("an element that the file has under another name = %q", got)
	}
	if got := n.Name("Cancel", domain.Locator{CSS: "#other"}); got != "cancel-2" {
		t.Errorf("the name of an element that has another locator = %q", got)
	}
}

func TestScreenNameIsTheTitleOrThePath(t *testing.T) {
	for _, c := range []struct {
		title, path string
		drop        []string
		want        string
	}{
		{"Refund", "/orders/o_1/refund", nil, "Refund"},
		{"Sign in", "/login", nil, "Sign-in"},
		{"Order o_4471 | Shop", "/orders/o_4471", []string{"o_4471"}, "Order-Shop"},
		{"", "/orders/o_4471/refund", []string{"o_4471"}, "orders-refund"},
		{"", "/", nil, "Home"},
		{"", "/#/boards", nil, "boards"},
		{"???", "/x", nil, "x"},
	} {
		if got := ScreenName(c.title, c.path, c.drop); got != c.want {
			t.Errorf("ScreenName(%q, %q) = %q, want %q", c.title, c.path, got, c.want)
		}
	}
}
