package script_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/script"
)

func mobileScenario() domain.Scenario {
	return domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "sign-in", Ticket: "T-1", Intent: "i", Surface: domain.SurfaceAndroid,
		Steps: []domain.Step{
			{ID: "start", Action: "Start the app"},
			{ID: "email", Action: "Type the email"},
			{ID: "go", Action: "Tap sign in", Expect: []domain.Expectation{
				{Check: domain.Check{Element: "welcome", Equals: "Hi Ana"}, Source: domain.SourcePerson},
			}},
		},
	}
}

func mobileScript(sc domain.Scenario) domain.Script {
	return domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc),
		Steps: []domain.ScriptStep{
			{Step: "start", Mobile: &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileLaunch}},
			{Step: "email", Mobile: &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileType, Screen: "SignIn", Element: "email", Value: "ana@example.com"}},
			{Step: "go", Mobile: &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileTap, Screen: "SignIn", Element: "submit"},
				Captures: []domain.Capture{{Name: "shown", Element: "welcome"}}},
		},
	}
}

func TestMobileScriptFitsItsScenario(t *testing.T) {
	sc := mobileScenario()
	if ps := script.Validate(sc, mobileScript(sc)); len(ps) > 0 {
		t.Fatalf("problems:\n%s", problemText(ps))
	}
}

func TestMobileScriptProblems(t *testing.T) {
	sc := mobileScenario()
	cases := map[string]struct {
		edit func(*domain.Script)
		want string
	}{
		"request and mobile": {func(s *domain.Script) {
			s.Steps[0].Request = &domain.APIRequest{Service: "x", Method: "GET", Path: "/"}
		}, "a request, a web action, or a mobile action, and not more than one"},
		"web and mobile": {func(s *domain.Script) {
			s.Steps[0].Web = &domain.WebAction{Do: domain.WebWait}
		}, "not more than one"},
		"a web step in an android scenario": {func(s *domain.Script) {
			s.Steps[0].Mobile = nil
			s.Steps[0].Web = &domain.WebAction{Service: "s", App: "a", Do: domain.WebOpen, Path: "/"}
		}, "an android scenario has no web action"},
		"an action that is not valid": {func(s *domain.Script) { s.Steps[1].Mobile.Value = "" }, "mobile: type needs a value or a secret"},
		"an unknown do":               {func(s *domain.Script) { s.Steps[1].Mobile.Do = "shake" }, "mobile: do must be one of"},
		"an unknown variable":         {func(s *domain.Script) { s.Steps[1].Mobile.Value = "{{nobody}}" }, "nobody is used before any step captures it"},
		"the first step is not a launch": {func(s *domain.Script) {
			s.Steps[0].Mobile.Do = domain.MobileWait
		}, "the first mobile step of a service must be launch or open, and this one is wait"},
		"a jsonpath capture": {func(s *domain.Script) {
			s.Steps[2].Captures = []domain.Capture{{Name: "x", JSONPath: "$.a"}}
		}, "a mobile step has no response to read: use an element"},
		"a page check on a mobile step": {func(s *domain.Script) {
			sc.Steps[2].Expect = []domain.Expectation{{Check: domain.Check{Page: domain.PageTitle, Equals: "x"}, Source: domain.SourcePerson}}
			s.ExpectHash = domain.ExpectHash(sc)
		}, "a mobile step has no page"},
		"a status check on a mobile step": {func(s *domain.Script) {
			st := 200
			sc.Steps[2].Expect = []domain.Expectation{{Check: domain.Check{Status: &st}, Source: domain.SourcePerson}}
			s.ExpectHash = domain.ExpectHash(sc)
		}, "a mobile step has none: check an element"},
		"an element check on an api step": {func(s *domain.Script) {
			s.Steps[2].Mobile = nil
			s.Steps[2].Captures = nil
			s.Steps[2].Request = &domain.APIRequest{Service: "x", Method: "GET", Path: "/"}
			sc.Steps[2].Hint = &domain.RequestHint{Method: "GET", Path: "/"}
			s.ExpectHash = domain.ExpectHash(sc)
		}, "checks element welcome, so the step needs a mobile action"},
		"as another account on a mobile step": {func(s *domain.Script) {
			sc.AuthProfile = "default"
			sc.Steps[1].As = "bob"
			s.ExpectHash = domain.ExpectHash(sc)
		}, "a device has one signed-in account, so as cannot be bob"},
		"no action for a step with an expectation": {func(s *domain.Script) { s.Steps = s.Steps[:2] }, "has an expected result and no request or web action"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			sc = mobileScenario()
			s := mobileScript(sc)
			c.edit(&s)
			if got := problemText(script.Validate(sc, s)); !strings.Contains(got, c.want) {
				t.Errorf("problems:\n%s\nwant one with %q", got, c.want)
			}
		})
	}
}

func TestAMobileStepOutsideAnAndroidScenarioIsRefused(t *testing.T) {
	sc := mobileScenario()
	sc.Surface = domain.SurfaceWeb
	s := mobileScript(sc)
	s.ExpectHash = domain.ExpectHash(sc)
	if got := problemText(script.Validate(sc, s)); !strings.Contains(got, "a mobile action needs a scenario with the surface android") {
		t.Errorf("problems:\n%s", got)
	}
}

func TestEachServiceStartsItsOwnApp(t *testing.T) {
	sc := mobileScenario()
	s := mobileScript(sc)
	s.Steps[1].Mobile.Service = "other-app"
	if got := problemText(script.Validate(sc, s)); !strings.Contains(got, "the first mobile step of a service must be launch or open, and this one is type") {
		t.Errorf("problems:\n%s", got)
	}
	s.Steps[1].Mobile.Do = domain.MobileOpenLink
	s.Steps[1].Mobile.Link = "shop://home"
	s.Steps[1].Mobile.Screen, s.Steps[1].Mobile.Element, s.Steps[1].Mobile.Value = "", "", ""
	if got := problemText(script.Validate(sc, s)); got != "" {
		t.Errorf("problems:\n%s", got)
	}
}

func TestAnAPIStepMayPrepareAnAndroidScenario(t *testing.T) {
	sc := mobileScenario()
	sc.Steps[0].Hint = &domain.RequestHint{Method: "POST", Path: "/seed"}
	s := mobileScript(sc)
	s.Steps[0].Mobile = nil
	s.Steps[0].Request = &domain.APIRequest{Service: "api", Method: "POST", Path: "/seed"}
	s.Steps[1].Mobile.Do = domain.MobileLaunch
	s.Steps[1].Mobile.Screen, s.Steps[1].Mobile.Element, s.Steps[1].Mobile.Value = "", "", ""
	s.ExpectHash = domain.ExpectHash(sc)
	if got := problemText(script.Validate(sc, s)); got != "" {
		t.Errorf("problems:\n%s", got)
	}
}

func TestMobileScriptRoundTrips(t *testing.T) {
	sc := mobileScenario()
	s := mobileScript(sc)
	s.Steps[1].Mobile.Value = ""
	s.Steps[1].Mobile.Secret = "env:SHOP_PASSWORD"
	s.Steps[2].Mobile = &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileScroll, Screen: "Home", Element: "row", Within: "list", Value: "up"}
	data, err := script.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := script.Parse(data, "sign-in.yaml")
	if err != nil {
		t.Fatalf("parse:\n%s\n%v", data, err)
	}
	if *back.Steps[1].Mobile != *s.Steps[1].Mobile || *back.Steps[2].Mobile != *s.Steps[2].Mobile || back.Steps[2].Captures[0].Element != "welcome" {
		t.Errorf("round trip lost the mobile step:\n%s", data)
	}
	if !strings.Contains(string(data), "mobile:") || strings.Contains(string(data), "web:") {
		t.Errorf("the key is mobile:\n%s", data)
	}
}

func TestADragAndADoubleTapRoundTripWithTheirDropElement(t *testing.T) {
	sc := mobileScenario()
	s := mobileScript(sc)
	s.Steps[1].Mobile = &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileDrag, Screen: "Home", Element: "card", To: "slot", Value: "800ms"}
	s.Steps[2].Mobile = &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileDoubleTap, Screen: "Home", Element: "card"}
	data, err := script.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "to: slot") || !strings.Contains(string(data), "do: drag") || !strings.Contains(string(data), "do: doubleTap") {
		t.Errorf("the script lacks the drag:\n%s", data)
	}
	back, err := script.Parse(data, "sign-in.yaml")
	if err != nil {
		t.Fatalf("parse:\n%s\n%v", data, err)
	}
	if *back.Steps[1].Mobile != *s.Steps[1].Mobile || *back.Steps[2].Mobile != *s.Steps[2].Mobile {
		t.Errorf("round trip lost the drag:\n%s", data)
	}
}

func TestRenderMobileLinkAndValue(t *testing.T) {
	vars := map[string]string{"id": "b-1", "bad": "a b", "slash": "x/y", "text": "any thing; 'quoted'"}
	got, err := script.RenderMobile(domain.MobileAction{Do: domain.MobileOpenLink, Link: "shop://order/{{id}}?v={{id}}"}, vars)
	if err != nil || got.Link != "shop://order/b-1?v=b-1" {
		t.Errorf("link = %q, %v", got.Link, err)
	}
	got, err = script.RenderMobile(domain.MobileAction{Do: domain.MobileType, Value: "{{text}}"}, vars)
	if err != nil || got.Value != "any thing; 'quoted'" {
		t.Errorf("value = %q, %v", got.Value, err)
	}
	for _, in := range []string{"shop://{{bad}}", "shop://{{slash}}", "shop://{{nobody}}", "shop://{{text}}"} {
		if _, err := script.RenderMobile(domain.MobileAction{Do: domain.MobileOpenLink, Link: in}, vars); err == nil {
			t.Errorf("RenderMobile(%q) accepted it", in)
		}
	}
}

func TestRenderLocatorKnowsTheMobileFields(t *testing.T) {
	vars := map[string]string{"n": "Ana"}
	got, err := script.RenderLocator(domain.Locator{ID: "id_{{n}}", Desc: "Hi {{n}}", Class: "k", XPath: "//*[@text='{{n}}']"}, vars)
	if err != nil || got.ID != "id_Ana" || got.Desc != "Hi Ana" || got.XPath != "//*[@text='Ana']" || got.Class != "k" {
		t.Errorf("locator = %+v, %v", got, err)
	}
	if _, err := script.RenderLocator(domain.Locator{Desc: "{{nobody}}"}, vars); err == nil || !strings.Contains(err.Error(), "desc") {
		t.Errorf("error = %v", err)
	}
	vs := script.LocatorVariables(domain.Locator{ID: "{{a}}", Desc: "{{b}}", Class: "{{a}}", XPath: "{{c}}"})
	if strings.Join(vs, ",") != "a,b,c" {
		t.Errorf("variables = %v", vs)
	}
}

func TestRenderLocatorFillsHasText(t *testing.T) {
	got, err := script.RenderLocator(domain.Locator{Class: "k", HasText: "Order {{ref}}", Index: 2}, map[string]string{"ref": "A1"})
	if err != nil || got.HasText != "Order A1" || got.Index != 2 {
		t.Errorf("locator = %+v, %v", got, err)
	}
	if _, err := script.RenderLocator(domain.Locator{Class: "k", HasText: "{{nobody}}"}, nil); err == nil || !strings.Contains(err.Error(), "hasText") {
		t.Errorf("error = %v", err)
	}
	if vs := script.LocatorVariables(domain.Locator{Class: "{{a}}", HasText: "{{b}} {{a}}"}); strings.Join(vs, ",") != "a,b" {
		t.Errorf("variables = %v", vs)
	}
}
