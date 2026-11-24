package screens_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/screens"
)

const withCommon = `apiVersion: semar/v1
kind: Screens
app: shop
common:
  back: { desc: Navigate up }
  tab-orders: { id: nav_orders }
  nav: { id: nav_bar }
  tab: { role: tab, within: nav }
screens:
  Cart:
    milk-card: { class: android.view.View, hasText: Milk }
    delete: { role: button, name: Delete, within: milk-card }
    third-delete: { role: button, name: Delete, index: 3 }
    back: { desc: Close cart }
    to-nav: { role: tab, within: nav }
`

func parse(t *testing.T, src string) domain.Screens {
	t.Helper()
	s, err := screens.Parse([]byte(src), "shop.yaml", "shop")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCommonLoadsAndAScreenElementShadowsIt(t *testing.T) {
	s := parse(t, withCommon)
	if l, ok := s.Locator("Cart", "tab-orders"); !ok || l.ID != "nav_orders" {
		t.Errorf("common element from a screen = %+v, %v", l, ok)
	}
	if l, ok := s.Locator("Nowhere", "tab-orders"); !ok || l.ID != "nav_orders" {
		t.Errorf("common element from an unknown screen = %+v, %v", l, ok)
	}
	if l, _ := s.Locator("Cart", "back"); l.Desc != "Close cart" {
		t.Errorf("the screen did not win: %+v", l)
	}
	if l, _ := s.Locator("Other", "back"); l.Desc != "Navigate up" {
		t.Errorf("common back = %+v", l)
	}
	if l, _ := s.Locator("Cart", "milk-card"); l.HasText != "Milk" {
		t.Errorf("hasText = %+v", l)
	}
	if l, _ := s.Locator("Cart", "third-delete"); l.Index != 3 {
		t.Errorf("index = %+v", l)
	}
}

func TestAFileWithOnlyCommonLoads(t *testing.T) {
	src := "apiVersion: semar/v1\nkind: Screens\napp: shop\ncommon:\n  back: { desc: Up }\n"
	parse(t, src)
}

func TestCommonProblemsArePlain(t *testing.T) {
	for name, c := range map[string]struct{ yaml, want string }{
		"common within a screen element": {strings.Replace(withCommon, "tab: { role: tab, within: nav }", "tab: { role: tab, within: milk-card }", 1), "common.tab.within: within names milk-card, which is not an element of common elements"},
		"common cycle":                   {strings.Replace(withCommon, "nav: { id: nav_bar }", "nav: { id: nav_bar, within: tab }", 1), "common.nav.within: within leads back to the element itself"},
		"bad common name":                {strings.Replace(withCommon, "  back: { desc: Navigate up }", "  \"bad name\": { desc: Navigate up }", 1), "the element name \"bad name\""},
		"bad common locator":             {strings.Replace(withCommon, "tab-orders: { id: nav_orders }", "tab-orders: { within: nav }", 1), "common.tab-orders: the element has no way to be found"},
		"index too big":                  {strings.Replace(withCommon, "index: 3", "index: 201", 1), "screens.Cart.third-delete.index: index must be a number from 1 to 200"},
		"negative index":                 {strings.Replace(withCommon, "index: 3", "index: -1", 1), "index must be a number from 1 to 200"},
		"hasText alone":                  {strings.Replace(withCommon, "class: android.view.View, hasText: Milk", "hasText: Milk", 1), "hasText only filters the matches of another field"},
		"hasText malformed":              {strings.Replace(withCommon, "hasText: Milk", `hasText: "Mi{{lk"`, 1), "hasText holds a malformed variable"},
		"within unknown":                 {strings.Replace(withCommon, "within: milk-card", "within: nowhere", 1), "which is not an element of this screen or the common elements"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := screens.Parse([]byte(c.yaml), "shop.yaml", "shop")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want it to hold %q", err, c.want)
			}
		})
	}
}

func TestAScreenElementMayBeWithinACommonOne(t *testing.T) {
	parse(t, withCommon) // Cart.to-nav is within the common element nav.
}

func TestAWithinCycleThroughAShadowIsRefused(t *testing.T) {
	// Cart.nav shadows the common nav, and is within nav: that is itself.
	src := strings.Replace(withCommon, "    back: { desc: Close cart }", "    nav: { id: other_nav, within: nav }", 1)
	_, err := screens.Parse([]byte(src), "shop.yaml", "shop")
	if err == nil || !strings.Contains(err.Error(), "within leads back to the element itself") {
		t.Errorf("error = %v", err)
	}
}

func TestAScreenElementMayNotShadowACommonElementThatAnotherIsInside(t *testing.T) {
	// common.tab is within common.nav, and a common within is looked up in common only.
	src := strings.Replace(withCommon, "    back: { desc: Close cart }", "    nav: { id: other_nav }", 1)
	_, err := screens.Parse([]byte(src), "shop.yaml", "shop")
	if err == nil || !strings.Contains(err.Error(), "common.tab is inside") {
		t.Errorf("error = %v", err)
	}
	// A shadow of a common element that nothing is inside stays allowed: Cart.back is one.
	parse(t, withCommon)
}

func TestCommonCountsTowardTheElementLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("apiVersion: semar/v1\nkind: Screens\napp: shop\ncommon:\n")
	for i := 0; i < 501; i++ {
		b.WriteString("  e" + strconv.Itoa(i) + ": { id: a }\n")
	}
	_, err := screens.Parse([]byte(b.String()), "shop.yaml", "shop")
	if err == nil || !strings.Contains(err.Error(), "501 elements; at most 500") {
		t.Errorf("error = %v", err)
	}
}

func TestCheckFindsCommonElementsOnAnyScreen(t *testing.T) {
	s := parse(t, withCommon)
	use := func(screen string, els ...string) []screens.Use {
		return []screens.Use{{Where: "step x", Surface: domain.SurfaceAndroid, Screen: screen, Elements: els}}
	}
	if got := screens.Check(s, use("Unlisted", "back", "tab-orders")); len(got) != 0 {
		t.Errorf("a screen with only common elements: %v", got)
	}
	if got := screens.Check(s, use("Cart", "tab-orders", "delete")); len(got) != 0 {
		t.Errorf("common and own: %v", got)
	}
	got := screens.Check(s, use("Unlisted", "back", "nope"))
	if len(got) != 1 || !strings.Contains(got[0], "screen Unlisted is not in the screens of shop") {
		t.Errorf("a screen with a non-common element: %v", got)
	}
	got = screens.Check(s, use("Cart", "nope"))
	if len(got) != 1 || !strings.Contains(got[0], "element nope is not on screen Cart") || !strings.Contains(got[0], "tab-orders") {
		t.Errorf("the list of elements holds the common ones: %v", got)
	}
	// A within chain through common is followed.
	if got := screens.Needed(s, "Cart", []string{"to-nav"}); strings.Join(got, ",") != "to-nav,nav" {
		t.Errorf("needed = %v", got)
	}
	if got := screens.Needed(s, "Unlisted", []string{"tab"}); strings.Join(got, ",") != "tab,nav" {
		t.Errorf("needed = %v", got)
	}
}

func TestCheckRefusesHasTextAndIndexOnTheWeb(t *testing.T) {
	src := strings.Replace(withCommon, "role: button, name: Delete, index: 3", "role: button, name: Delete, index: 3, testId: d", 1)
	s := parse(t, src)
	web := func(els ...string) []screens.Use {
		return []screens.Use{{Where: "step x", Surface: domain.SurfaceWeb, Screen: "Cart", Elements: els}}
	}
	for _, el := range []string{"third-delete", "milk-card"} {
		got := screens.Check(s, web(el))
		if len(got) != 1 || !strings.Contains(got[0], "uses hasText or index, which are for a device only") {
			t.Errorf("%s: %v", el, got)
		}
	}
	// The device accepts both.
	dev := []screens.Use{{Where: "step x", Surface: domain.SurfaceAndroid, Screen: "Cart", Elements: []string{"third-delete", "delete"}}}
	if got := screens.Check(s, dev); len(got) != 0 {
		t.Errorf("device: %v", got)
	}
}

func TestIndexAndHasTextRoundTripThroughReplace(t *testing.T) {
	for _, loc := range []domain.Locator{
		{Role: "button", Name: "Delete", Index: 2},
		{Class: "android.view.View", HasText: "Milk: 2 l"},
		{Class: "k", HasText: "Milk", Index: 2, Within: "nav"},
	} {
		out, err := screens.Replace([]byte(withCommon), "shop", "Cart", "third-delete", loc)
		if err != nil {
			t.Fatalf("%+v: %v", loc, err)
		}
		if strings.Contains(string(out), `index: "`) {
			t.Errorf("the index is quoted:\n%s", out)
		}
		if got, _ := parse(t, string(out)).Locator("Cart", "third-delete"); got != loc {
			t.Errorf("got %+v, want %+v", got, loc)
		}
	}
	// A block element too.
	block := "apiVersion: semar/v1\nkind: Screens\napp: shop\nscreens:\n  Cart:\n    d:\n      role: button\n"
	out, err := screens.Replace([]byte(block), "shop", "Cart", "d", domain.Locator{Role: "button", Index: 4})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := parse(t, string(out)).Locator("Cart", "d"); got.Index != 4 {
		t.Errorf("block: %+v\n%s", got, out)
	}
}

func TestReplaceFallsBackToACommonElement(t *testing.T) {
	out, err := screens.Replace([]byte(withCommon), "shop", "Cart", "tab-orders", domain.Locator{ID: "orders_tab"})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(withCommon, "tab-orders: { id: nav_orders }", "tab-orders: { id: orders_tab }", 1)
	if string(out) != want {
		t.Errorf("file:\n%s", out)
	}
	// An element of the screen with the same name is the one that is replaced.
	out, err = screens.Replace([]byte(withCommon), "shop", "Cart", "back", domain.Locator{Desc: "Close"})
	if err != nil || !strings.Contains(string(out), "back: { desc: Navigate up }") || !strings.Contains(string(out), "back: { desc: Close }") {
		t.Errorf("shadow: %v\n%s", err, out)
	}
	if _, err := screens.Replace([]byte(withCommon), "shop", "Cart", "nobody", domain.Locator{ID: "x"}); err == nil {
		t.Error("an element that is nowhere was replaced")
	}
}

func TestRepairedKeepsHasTextLikeTheOtherFields(t *testing.T) {
	file := domain.Locator{Class: "k", HasText: "{{item}}"}
	resolved := domain.Locator{Class: "k", HasText: "Milk"}
	got := screens.Repaired(file, resolved, domain.Locator{Class: "k2", HasText: "Milk", Index: 2})
	if got.HasText != "{{item}}" || got.Class != "k2" || got.Index != 2 {
		t.Errorf("Repaired = %+v", got)
	}
	got = screens.Repaired(domain.Locator{Role: "button", Index: 3}, domain.Locator{Role: "button", Index: 3}, domain.Locator{Role: "button"})
	if got.Index != 0 {
		t.Errorf("the index comes from the candidate: %+v", got)
	}
}
