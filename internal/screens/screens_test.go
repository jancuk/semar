package screens_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/screens"
)

const shop = `apiVersion: semar/v1
kind: Screens
app: shop
screens:
  Cart:
    place-order: { role: button, name: Place order }
    cart-list: { testId: cart }
    line: { css: "li.line", within: cart-list }
  Login:
    email: { role: textbox, name: Email }
`

func write(t *testing.T, app, content string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".semar", "screens")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, app+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestLoad(t *testing.T) {
	s, err := screens.Load(write(t, "shop", shop), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Locator("Cart", "line"); !ok || got.Within != "cart-list" || got.CSS != "li.line" {
		t.Errorf("Cart.line = %+v, %v", got, ok)
	}
	if got := s.ScreenNames(); len(got) != 2 || got[0] != "Cart" {
		t.Errorf("screens = %v", got)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := screens.Load(t.TempDir(), "shop")
	if !errors.Is(err, screens.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), ".semar/screens/shop.yaml") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

func TestParseProblemsArePlain(t *testing.T) {
	for name, c := range map[string]struct{ yaml, want string }{
		"wrong app":          {strings.Replace(shop, "app: shop", "app: other", 1), "app other does not match the file name shop.yaml"},
		"no locator":         {strings.Replace(shop, "{ testId: cart }", "{ within: line }", 1), "the element has no way to be found"},
		"within unknown":     {strings.Replace(shop, "within: cart-list", "within: nowhere", 1), "within names nowhere, which is not an element of this screen"},
		"within cycle":       {strings.Replace(shop, "{ testId: cart }", "{ testId: cart, within: line }", 1), "within leads back to the element itself"},
		"malformed variable": {strings.Replace(shop, "name: Place order", `name: "Place {{order"`, 1), "name holds a malformed variable"},
		"unknown key":        {strings.Replace(shop, "role: button,", "roll: button,", 1), "unknown key roll"},
		"no screens":         {"apiVersion: semar/v1\nkind: Screens\napp: shop\nscreens: {}\n", "needs at least one screen"},
		"bad kind":           {strings.Replace(shop, "kind: Screens", "kind: Page", 1), "kind must be Screens"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := screens.Parse([]byte(c.yaml), ".semar/screens/shop.yaml", "shop")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want it to hold %q", err, c.want)
			}
			if strings.Contains(err.Error(), "domain.") || strings.Contains(err.Error(), "struct") {
				t.Errorf("the error names a Go type: %v", err)
			}
		})
	}
}

func TestLoadRefusesALink(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.yaml")
	if err := os.WriteFile(outside, []byte(shop), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, ".semar", "screens")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "shop.yaml")); err != nil {
		t.Skip("symbolic links are not available")
	}
	if _, err := screens.Load(repo, "shop"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("error = %v, want a refusal of the link", err)
	}
}

func TestCheckNamesWhatTheFileHas(t *testing.T) {
	s, err := screens.Parse([]byte(shop), "shop.yaml", "shop")
	if err != nil {
		t.Fatal(err)
	}
	uses := []screens.Use{
		{Where: "step a", Action: domain.WebAction{Do: domain.WebClick, Screen: "Cart", Element: "place-order"}},
		{Where: "step b", Action: domain.WebAction{Do: domain.WebClick, Screen: "Nope", Element: "x"}},
		{Where: "step c", Action: domain.WebAction{Do: domain.WebDrag, Screen: "Cart", Element: "line", To: "bin"}, Elements: []string{"gone"}},
		{Where: "step d", Action: domain.WebAction{Do: domain.WebOpen, Path: "/"}, Elements: []string{"x"}},
	}
	got := screens.Check(s, uses)
	want := []string{
		"step b: screen Nope is not in the screens of shop; its screens are Cart, Login",
		"step c: element bin is not on screen Cart of shop; its elements are cart-list, line, place-order",
		"step c: element gone is not on screen Cart of shop; its elements are cart-list, line, place-order",
		"step d names element x but has no screen",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Check =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestNeededFollowsWithin(t *testing.T) {
	s, _ := screens.Parse([]byte(shop), "shop.yaml", "shop")
	got := screens.Needed(s, "Cart", []string{"line", "place-order"})
	if strings.Join(got, ",") != "line,cart-list,place-order" {
		t.Errorf("Needed = %v", got)
	}
}

func TestReplaceKeepsTheRestOfTheFile(t *testing.T) {
	src := "# the shop screens\n" + shop
	out, err := screens.Replace([]byte(src), "shop", "Cart", "place-order", domain.Locator{Role: "button", Name: "Pay now"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "# the shop screens") {
		t.Errorf("the comment was lost:\n%s", text)
	}
	if !strings.Contains(text, "Pay now") || strings.Contains(text, "Place order") {
		t.Errorf("the locator was not replaced:\n%s", text)
	}
	s, err := screens.Parse(out, "shop.yaml", "shop")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Locator("Cart", "line"); got.CSS != "li.line" || got.Within != "cart-list" {
		t.Errorf("another element changed: %+v", got)
	}
	if _, err := screens.Replace([]byte(src), "shop", "Cart", "missing", domain.Locator{Role: "button"}); err == nil {
		t.Error("an element that is not in the file was replaced")
	}
}

func TestRepairedKeepsAVariable(t *testing.T) {
	file := domain.Locator{Role: "heading", Name: "{{title}}", Within: "column"}
	resolved := domain.Locator{Role: "heading", Name: "Buy milk", Within: "column"}
	got := screens.Repaired(file, resolved, domain.Locator{Role: "link", Name: "Buy milk"})
	if got.Role != "link" || got.Name != "{{title}}" || got.Within != "column" {
		t.Errorf("Repaired = %+v", got)
	}
}

func TestChangesSaysWhetherRoleAndNameStayed(t *testing.T) {
	old, _ := screens.Parse([]byte(shop), "shop.yaml", "shop")
	moved, _ := screens.Parse([]byte(strings.Replace(shop, "{ role: button, name: Place order }", "{ role: button, name: Place order, css: .pay }", 1)), "shop.yaml", "shop")
	changed, _ := screens.Parse([]byte(strings.Replace(shop, "name: Place order", "name: Cancel order", 1)), "shop.yaml", "shop")
	if got := screens.Changes(old, moved); len(got) != 1 || !got[0].SameRoleAndName || got[0].Kind != "changed" {
		t.Errorf("moved = %+v", got)
	}
	if got := screens.Changes(old, changed); len(got) != 1 || got[0].SameRoleAndName {
		t.Errorf("changed = %+v", got)
	}
	if got := screens.Changes(old, old); len(got) != 0 {
		t.Errorf("same = %+v", got)
	}
}

func TestReplaceChangesOnlyTheTextOfTheElement(t *testing.T) {
	src := "# the shop screens\n" + shop
	out, err := screens.Replace([]byte(src), "shop", "Cart", "place-order", domain.Locator{Role: "button", Name: "Pay now"})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(src, "place-order: { role: button, name: Place order }", `place-order: { role: button, name: Pay now }`, 1)
	if string(out) != want {
		t.Errorf("the file changed beyond the element:\n%s\nwant\n%s", out, want)
	}
}

func TestReplaceQuotesWhatNeedsIt(t *testing.T) {
	out, err := screens.Replace([]byte(shop), "shop", "Cart", "line", domain.Locator{CSS: `li[data-x="1"]: first`, Within: "cart-list"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := screens.Parse(out, "shop.yaml", "shop")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := s.Locator("Cart", "line"); got.CSS != `li[data-x="1"]: first` || got.Within != "cart-list" {
		t.Errorf("locator = %+v", got)
	}
}

func TestReplaceAnElementWrittenAsABlock(t *testing.T) {
	src := "apiVersion: semar/v1\nkind: Screens\napp: shop\nscreens:\n  Cart:\n    pay:\n      role: button\n      name: Pay\n    other: { testId: o }\n"
	out, err := screens.Replace([]byte(src), "shop", "Cart", "pay", domain.Locator{Role: "link", Name: "Pay now"})
	if err != nil {
		t.Fatal(err)
	}
	want := "apiVersion: semar/v1\nkind: Screens\napp: shop\nscreens:\n  Cart:\n    pay:\n      role: link\n      name: Pay now\n    other: { testId: o }\n"
	if string(out) != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}
