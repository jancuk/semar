package screens_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/screens"
)

const shopApp = `apiVersion: semar/v1
kind: Screens
app: shop-app
screens:
  SignIn:
    email: { id: email_field, hint: x }
    submit: { desc: Sign in }
    legal: { xpath: "//android.widget.TextView[@text='Terms']" }
    row: { class: android.widget.TextView, within: list }
    list: { xpath: "//androidx.recyclerview.widget.RecyclerView[1]" }
    web-only: { css: "#x" }
    templated: { xpath: "//*[@text='{{name}}'" }
    both: { testId: pay, css: ".pay" }
`

func TestMobileFieldsLoad(t *testing.T) {
	src := strings.Replace(shopApp, "email: { id: email_field, hint: x }", "email: { id: email_field }", 1)
	s, err := screens.Parse([]byte(src), "shop-app.yaml", "shop-app")
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := s.Locator("SignIn", "email"); l.ID != "email_field" {
		t.Errorf("email = %+v", l)
	}
	if l, _ := s.Locator("SignIn", "submit"); l.Desc != "Sign in" {
		t.Errorf("submit = %+v", l)
	}
	if l, _ := s.Locator("SignIn", "row"); l.Class != "android.widget.TextView" || l.Within != "list" {
		t.Errorf("row = %+v", l)
	}
}

func TestAnXPathIsCheckedWhenTheFileLoads(t *testing.T) {
	bad := strings.Replace(shopApp, "email: { id: email_field, hint: x }", "email: { xpath: \"//a[@b!='c']\" }", 1)
	_, err := screens.Parse([]byte(bad), "shop-app.yaml", "shop-app")
	if err == nil || !strings.Contains(err.Error(), "screens.SignIn.email.xpath") || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error = %v", err)
	}
	// An xpath that holds a variable is checked after the variable is filled in.
	if strings.Contains(err.Error(), "templated") {
		t.Errorf("a templated xpath was refused at load: %v", err)
	}
}

func TestOldFilesLoadTheSame(t *testing.T) {
	s, err := screens.Parse([]byte(shop), "shop.yaml", "shop")
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := s.Locator("Cart", "place-order"); l != (domain.Locator{Role: "button", Name: "Place order"}) {
		t.Errorf("locator = %+v", l)
	}
}

func TestCheckBlocksALocatorThatItsSurfaceCannotUse(t *testing.T) {
	src := strings.Replace(shopApp, "email: { id: email_field, hint: x }", "email: { id: email_field }", 1)
	src = strings.Replace(src, "    templated: { xpath: \"//*[@text='{{name}}'\" }\n", "", 1)
	s, err := screens.Parse([]byte(src), "shop-app.yaml", "shop-app")
	if err != nil {
		t.Fatal(err)
	}
	dev := screens.MobileUse("step a", domain.MobileAction{Do: domain.MobileTap, Screen: "SignIn", Element: "web-only"}, nil)
	got := screens.Check(s, []screens.Use{dev})
	if len(got) != 1 || !strings.Contains(got[0], "step a: element web-only on screen SignIn of shop-app cannot be found on a device") {
		t.Errorf("device: %v", got)
	}
	br := screens.WebUse("step b", domain.WebAction{Do: domain.WebClick, Screen: "SignIn", Element: "submit"}, nil)
	got = screens.Check(s, []screens.Use{br})
	if len(got) != 1 || !strings.Contains(got[0], "cannot be found in a browser") {
		t.Errorf("browser: %v", got)
	}
	// One file may serve both: testId works on both, and css is only for a browser.
	for _, el := range []string{"both"} {
		for _, u := range []screens.Use{
			screens.MobileUse("a", domain.MobileAction{Do: domain.MobileTap, Screen: "SignIn", Element: el}, nil),
			screens.WebUse("b", domain.WebAction{Do: domain.WebClick, Screen: "SignIn", Element: el}, nil),
		} {
			if got := screens.Check(s, []screens.Use{u}); len(got) != 0 {
				t.Errorf("%s: %v", el, got)
			}
		}
	}
	// An element reached through within must be usable too.
	s.Screens["SignIn"]["row"] = domain.Locator{Class: "x", Within: "web-only"}
	got = screens.Check(s, []screens.Use{screens.MobileUse("c", domain.MobileAction{Do: domain.MobileTap, Screen: "SignIn", Element: "row"}, nil)})
	if len(got) != 1 || !strings.Contains(got[0], "element web-only") {
		t.Errorf("within: %v", got)
	}
	// A missing element is named once, and not also reported as unusable.
	got = screens.Check(s, []screens.Use{screens.MobileUse("d", domain.MobileAction{Do: domain.MobileTap, Screen: "SignIn", Element: "nope"}, nil)})
	if len(got) != 1 || !strings.Contains(got[0], "element nope is not on screen SignIn") {
		t.Errorf("missing: %v", got)
	}
}

func TestReplaceAndRepairedKeepTheNewFields(t *testing.T) {
	src := strings.Replace(shopApp, "email: { id: email_field, hint: x }", "email: { id: email_field }", 1)
	src = strings.Replace(src, "    templated: { xpath: \"//*[@text='{{name}}'\" }\n", "", 1)
	for _, loc := range []domain.Locator{
		{ID: "new_id"},
		{Desc: "Log in", Class: "android.widget.Button"},
		{XPath: "//android.widget.Button[@text='Go: now']", Within: "list"},
		{Role: "button", Name: "Pay", ID: "pay", Desc: "Pay now", Class: "k", XPath: "//k[1]", Within: "list"},
	} {
		out, err := screens.Replace([]byte(src), "shop-app", "SignIn", "submit", loc)
		if err != nil {
			t.Fatalf("%+v: %v", loc, err)
		}
		s, err := screens.Parse(out, "shop-app.yaml", "shop-app")
		if err != nil {
			t.Fatalf("%+v: %v", loc, err)
		}
		if got, _ := s.Locator("SignIn", "submit"); got != loc {
			t.Errorf("round trip: got %+v, want %+v", got, loc)
		}
		// Everything else in the file is as it was.
		if !strings.Contains(string(out), "email: { id: email_field }") {
			t.Errorf("another element changed:\n%s", out)
		}
	}
	file := domain.Locator{Desc: "Hi {{name}}", XPath: "//k[@text='{{name}}']", Within: "w"}
	resolved := domain.Locator{Desc: "Hi Ana", XPath: "//k[@text='Ana']", Within: "w"}
	got := screens.Repaired(file, resolved, domain.Locator{Desc: "Hi Ana", XPath: "//k[2]"})
	if got.Desc != "Hi {{name}}" || got.XPath != "//k[2]" || got.Within != "w" {
		t.Errorf("Repaired = %+v", got)
	}
}

func TestAddWritesTheNewFields(t *testing.T) {
	out, err := screens.New("shop-app", []screens.Addition{{Screen: "Home", Elements: []screens.Element{
		{Name: "menu", Locator: domain.Locator{Desc: "Menu", Class: "android.widget.ImageButton"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "menu: { desc: Menu, class: android.widget.ImageButton }") {
		t.Errorf("out =\n%s", out)
	}
}
