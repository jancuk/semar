package domain_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestMobileProblems(t *testing.T) {
	ok := func(m domain.MobileAction) {
		t.Helper()
		if ps := m.Problems(); len(ps) > 0 {
			t.Errorf("%+v: unexpected problems %v", m, ps)
		}
	}
	bad := func(name string, m domain.MobileAction, want string) {
		t.Helper()
		ps := m.Problems()
		if len(ps) == 0 || !strings.Contains(strings.Join(ps, "\n"), want) {
			t.Errorf("%s: problems %v, want one with %q", name, ps, want)
		}
	}
	ok(domain.MobileAction{Do: domain.MobileLaunch})
	ok(domain.MobileAction{Do: domain.MobileLaunch, Screen: "Home"})
	ok(domain.MobileAction{Do: domain.MobileOpenLink, Link: "shop://cart/{{id}}"})
	ok(domain.MobileAction{Do: domain.MobileTap, Screen: "S", Element: "e"})
	ok(domain.MobileAction{Do: domain.MobileLongTap, Screen: "S", Element: "e"})
	ok(domain.MobileAction{Do: domain.MobileType, Screen: "S", Element: "e", Value: "x"})
	ok(domain.MobileAction{Do: domain.MobileType, Screen: "S", Element: "e", Secret: "env:P"})
	ok(domain.MobileAction{Do: domain.MobileSwipe, Value: "up"})
	ok(domain.MobileAction{Do: domain.MobileSwipe, Value: "left", Screen: "S", Element: "card"})
	ok(domain.MobileAction{Do: domain.MobileScroll, Screen: "S", Element: "row"})
	ok(domain.MobileAction{Do: domain.MobileScroll, Screen: "S", Element: "row", Value: "up", Within: "list"})
	ok(domain.MobileAction{Do: domain.MobilePress, Value: "hide-keyboard"})
	ok(domain.MobileAction{Do: domain.MobileWait})
	ok(domain.MobileAction{Do: domain.MobileWait, Screen: "S", Element: "e"})

	bad("no do", domain.MobileAction{}, "do must be one of")
	bad("an unknown do", domain.MobileAction{Do: "shake"}, "do must be one of")
	bad("a web op", domain.MobileAction{Do: "click"}, "do must be one of")
	bad("launch with element", domain.MobileAction{Do: domain.MobileLaunch, Element: "e"}, "launch takes no element")
	bad("launch with link", domain.MobileAction{Do: domain.MobileLaunch, Link: "a://b"}, "launch takes no link")
	bad("open without link", domain.MobileAction{Do: domain.MobileOpenLink}, "open needs the link")
	bad("open with value", domain.MobileAction{Do: domain.MobileOpenLink, Link: "a://b", Value: "x"}, "open takes no value")
	bad("tap without screen", domain.MobileAction{Do: domain.MobileTap, Element: "e"}, "tap needs the screen")
	bad("tap without element", domain.MobileAction{Do: domain.MobileTap, Screen: "S"}, "tap needs the element")
	bad("tap with value", domain.MobileAction{Do: domain.MobileTap, Screen: "S", Element: "e", Value: "x"}, "tap takes no value")
	bad("longPress with within", domain.MobileAction{Do: domain.MobileLongTap, Screen: "S", Element: "e", Within: "w"}, "longPress takes no within")
	bad("type without text", domain.MobileAction{Do: domain.MobileType, Screen: "S", Element: "e"}, "type needs a value or a secret")
	bad("type with both", domain.MobileAction{Do: domain.MobileType, Screen: "S", Element: "e", Value: "x", Secret: "env:P"}, "not both")
	bad("type with a bad secret", domain.MobileAction{Do: domain.MobileType, Screen: "S", Element: "e", Secret: "hunter2"}, "secret must be a secret reference")
	bad("swipe without direction", domain.MobileAction{Do: domain.MobileSwipe}, "swipe needs the value")
	bad("swipe sideways", domain.MobileAction{Do: domain.MobileSwipe, Value: "diagonal"}, "must be one of up, down, left, right")
	bad("swipe element without screen", domain.MobileAction{Do: domain.MobileSwipe, Value: "up", Element: "e"}, "swipe needs the screen")
	bad("scroll without element", domain.MobileAction{Do: domain.MobileScroll, Screen: "S"}, "scroll needs the element")
	bad("scroll bad direction", domain.MobileAction{Do: domain.MobileScroll, Screen: "S", Element: "e", Value: "in"}, "must be one of")
	bad("press without key", domain.MobileAction{Do: domain.MobilePress}, "press needs the value")
	bad("press unknown key", domain.MobileAction{Do: domain.MobilePress, Value: "power"}, "press takes one of")
	bad("press with element", domain.MobileAction{Do: domain.MobilePress, Value: "back", Element: "e"}, "press takes no element")
	bad("wait with value", domain.MobileAction{Do: domain.MobileWait, Value: "x"}, "wait takes no value")
	bad("wait element without screen", domain.MobileAction{Do: domain.MobileWait, Element: "e"}, "wait needs the screen")
	bad("a malformed variable", domain.MobileAction{Do: domain.MobileOpenLink, Link: "a://{{b"}, "link holds a malformed variable")
	bad("a bad element name", domain.MobileAction{Do: domain.MobileTap, Screen: "S", Element: "a b"}, "element")
}

func TestMobileWritesAndVariables(t *testing.T) {
	writes := map[domain.MobileOp]bool{
		domain.MobileLaunch: false, domain.MobileOpenLink: false, domain.MobileTap: true, domain.MobileLongTap: true,
		domain.MobileType: true, domain.MobileSwipe: true, domain.MobileScroll: false, domain.MobilePress: true, domain.MobileWait: false,
	}
	for op, want := range writes {
		if op.Writes() != want {
			t.Errorf("%s.Writes() = %v", op, !want)
		}
	}
	m := domain.MobileAction{Do: domain.MobileOpenLink, Link: "shop://{{a.b}}/{{c}}", Value: "{{c}} {{d}}"}
	if got := m.Variables(); !reflect.DeepEqual(got, []string{"a.b", "c", "d"}) {
		t.Errorf("variables = %v", got)
	}
	if got := (domain.MobileAction{Element: "e", Within: "w"}).Elements(); !reflect.DeepEqual(got, []string{"e", "w"}) {
		t.Errorf("elements = %v", got)
	}
}

func TestUIReadsBothEngines(t *testing.T) {
	st := map[string]domain.ElementState{"x": {Count: 1}}
	web := domain.Action{Web: &domain.WebRecord{Op: "click", App: "shop", Screen: "Cart", URL: "https://x/y", Title: "T", Elements: st, Screenshot: "s.jpg"}}
	if u := web.UI(); u == nil || u.App != "shop" || u.URL != "https://x/y" || u.Title != "T" || u.Device || u.Elements["x"].Count != 1 || u.Screenshot != "s.jpg" {
		t.Errorf("web UI = %+v", u)
	}
	mob := domain.Action{Mobile: &domain.MobileRecord{Op: "tap", App: "shop", Screen: "Cart", Elements: st, Repair: []domain.Candidate{{Element: "x"}}}}
	if u := mob.UI(); u == nil || u.Op != "tap" || !u.Device || u.URL != "" || len(u.Repair) != 1 {
		t.Errorf("mobile UI = %+v", u)
	}
	if (domain.Action{}).UI() != nil {
		t.Error("an API action has no UI")
	}
}

func TestOnScreen(t *testing.T) {
	for s, want := range map[domain.Surface]bool{domain.SurfaceAPI: false, domain.SurfaceWeb: true, domain.SurfaceAndroid: true, "ios": false} {
		if s.OnScreen() != want {
			t.Errorf("%s.OnScreen() = %v", s, !want)
		}
	}
}

func TestLocatorStrategiesForTheWebStayAsTheyWere(t *testing.T) {
	l := domain.Locator{TestID: "a", Role: "button", Text: "t", CSS: "c", ID: "i", Desc: "d", Class: "k", XPath: "//x"}
	if got := l.Strategies(); !reflect.DeepEqual(got, []string{"testId", "role", "text", "css"}) {
		t.Errorf("web strategies = %v", got)
	}
	if got := l.MobileStrategies(); !reflect.DeepEqual(got, []string{"id", "desc", "role", "text", "xpath", "class"}) {
		t.Errorf("mobile strategies = %v", got)
	}
	if got := (domain.Locator{TestID: "a"}).MobileStrategies(); !reflect.DeepEqual(got, []string{"id"}) {
		t.Errorf("testId on a device = %v", got)
	}
	for l, want := range map[domain.Locator][2]bool{
		{CSS: "c"}:          {true, false},
		{Desc: "d"}:         {false, true},
		{TestID: "a"}:       {true, true},
		{Role: "button"}:    {true, true},
		{XPath: "//a"}:      {false, true},
		{Class: "k"}:        {false, true},
		{Within: "w"}:       {false, false},
		{ID: "i", CSS: "c"}: {true, true},
	} {
		if l.ForWeb() != want[0] || l.ForMobile() != want[1] {
			t.Errorf("%v: web %v mobile %v, want %v", l, l.ForWeb(), l.ForMobile(), want)
		}
	}
	if (domain.Locator{Desc: "d"}).Empty() || (domain.Locator{XPath: "//a"}).Empty() || !(domain.Locator{Within: "w"}).Empty() {
		t.Error("Empty ignores a new field, or counts within")
	}
	if got := (domain.Locator{ID: "x", Desc: "Back", Class: "k", XPath: "//a[1]"}).String(); got != `{ id: x, desc: Back, class: k, xpath: "//a[1]" }` {
		t.Errorf("String = %s", got)
	}
}

func TestAnAPKBuildDoesNotChangeTheManifestHash(t *testing.T) {
	a := []domain.BuildRef{{Service: "app", Commit: "ab", ConfigHash: "c", Declared: true}}
	b := []domain.BuildRef{{Service: "app", Commit: "ab", ConfigHash: "c", Declared: true, APK: true}}
	if domain.ManifestHash(a) != domain.ManifestHash(b) {
		t.Error("APK is in the manifest hash")
	}
	data, _ := json.Marshal(b[0])
	if !strings.Contains(string(data), `"apk":true`) {
		t.Errorf("json = %s", data)
	}
	data, _ = json.Marshal(a[0])
	if strings.Contains(string(data), "apk") {
		t.Errorf("a build with no APK encodes apk: %s", data)
	}
}

func TestAMobileActionRecordRoundTripsThroughJSON(t *testing.T) {
	in := domain.Action{Surface: domain.SurfaceAndroid, Op: "tap", Mobile: &domain.MobileRecord{Op: "tap", App: "shop", Screen: "S", Package: "com.a.b",
		Target: &domain.Locator{Desc: "Buy"}, Elements: map[string]domain.ElementState{"e": {Found: true, Count: 1, Text: "x"}}}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out domain.Action
	if err := json.Unmarshal(data, &out); err != nil || !reflect.DeepEqual(in, out) {
		t.Errorf("round trip: %v\n%+v\n%+v", err, in, out)
	}
}
