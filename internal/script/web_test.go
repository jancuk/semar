package script_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/script"
)

func webScenario() domain.Scenario {
	return domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "board", Ticket: "T-1", Intent: "i", Surface: domain.SurfaceWeb,
		Steps: []domain.Step{
			{ID: "open", Action: "Open the board"},
			{ID: "read", Action: "Read the card", Expect: []domain.Expectation{
				{Check: domain.Check{Element: "card", Equals: "Buy milk"}, Source: domain.SourcePerson},
			}},
		},
	}
}

func webScript(sc domain.Scenario) domain.Script {
	return domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc),
		Steps: []domain.ScriptStep{
			{Step: "open", Web: &domain.WebAction{Service: "board", App: "lanes", Do: domain.WebOpen, Path: "/"}},
			{Step: "read", Web: &domain.WebAction{Service: "board", App: "lanes", Do: domain.WebWait, Screen: "Board", Element: "card"},
				Captures: []domain.Capture{{Name: "shown", Element: "card"}}},
		},
	}
}

func problemText(ps []script.Problem) string {
	var out []string
	for _, p := range ps {
		out = append(out, string(p.Step)+": "+p.Message)
	}
	return strings.Join(out, "\n")
}

func TestWebScriptFitsItsScenario(t *testing.T) {
	sc := webScenario()
	if ps := script.Validate(sc, webScript(sc)); len(ps) > 0 {
		t.Fatalf("problems:\n%s", problemText(ps))
	}
}

func TestWebScriptProblems(t *testing.T) {
	sc := webScenario()
	cases := map[string]struct {
		edit func(*domain.Script)
		want string
	}{
		"request and web": {func(s *domain.Script) {
			s.Steps[0].Request = &domain.APIRequest{Service: "x", Method: "GET", Path: "/"}
		}, "a request or a web action, and not both"},
		"an action that is not valid": {func(s *domain.Script) { s.Steps[0].Web.Path = "" }, "web: open needs the path"},
		"an unknown variable":         {func(s *domain.Script) { s.Steps[0].Web.Path = "/{{nobody}}" }, "nobody is used before any step captures it"},
		"a jsonpath capture on a web step": {func(s *domain.Script) {
			s.Steps[1].Captures = []domain.Capture{{Name: "x", JSONPath: "$.a"}}
		}, "a web step has no response to read: use an element"},
		"an element capture on an api step": {func(s *domain.Script) {
			s.Steps[0].Web = nil
			s.Steps[0].Request = &domain.APIRequest{Service: "x", Method: "GET", Path: "/"}
			s.Steps[0].Captures = []domain.Capture{{Name: "x", Element: "card"}}
		}, "names an element, so the step needs a web action"},
		"both subjects": {func(s *domain.Script) {
			s.Steps[1].Captures = []domain.Capture{{Name: "x", JSONPath: "$.a", Element: "card"}}
		}, "takes a jsonpath or an element, and not both"},
		"neither subject": {func(s *domain.Script) {
			s.Steps[1].Captures = []domain.Capture{{Name: "x"}}
		}, "needs a jsonpath or an element"},
		"no action for a step with an expectation": {func(s *domain.Script) { s.Steps = s.Steps[:1] }, "has an expected result and no request or web action"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := webScript(sc)
			c.edit(&s)
			if got := problemText(script.Validate(sc, s)); !strings.Contains(got, c.want) {
				t.Errorf("problems:\n%s\nwant one with %q", got, c.want)
			}
		})
	}
}

func TestElementCheckNeedsAWebStep(t *testing.T) {
	sc := webScenario()
	s := webScript(sc)
	s.Steps[1].Web = nil
	s.Steps[1].Captures = nil
	s.Steps[1].Request = &domain.APIRequest{Service: "x", Method: "GET", Path: "/"}
	sc.Steps[1].Hint = &domain.RequestHint{Method: "GET", Path: "/"}
	s.ExpectHash = domain.ExpectHash(sc)
	if got := problemText(script.Validate(sc, s)); !strings.Contains(got, "checks element card, so the step needs a web action") {
		t.Errorf("problems:\n%s", got)
	}
}

func TestWebScriptRoundTrips(t *testing.T) {
	sc := webScenario()
	s := webScript(sc)
	s.Steps[1].Web.Secret = ""
	data, err := script.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := script.Parse(data, "board.yaml")
	if err != nil {
		t.Fatalf("parse:\n%s\n%v", data, err)
	}
	if back.Steps[1].Web == nil || back.Steps[1].Web.Element != "card" || back.Steps[1].Captures[0].Element != "card" {
		t.Errorf("round trip lost the web step:\n%s", data)
	}
}

func TestRenderWebPathMayHoldARouteOrAQuery(t *testing.T) {
	vars := map[string]string{"id": "b-1", "bad": "a b"}
	for in, want := range map[string]string{
		"/":                     "/",
		"/#/boards/{{id}}":      "/#/boards/b-1",
		"/boards/{{id}}?tab=x":  "/boards/b-1?tab=x",
		"/b/{{id}}?q={{id}}#/a": "/b/b-1?q=b-1#/a",
	} {
		got, err := script.RenderWeb(domain.WebAction{Do: domain.WebOpen, Path: in}, vars)
		if err != nil || got.Path != want {
			t.Errorf("RenderWeb(%q) = %q, %v, want %q", in, got.Path, err, want)
		}
	}
	for _, in := range []string{"/#/boards/{{bad}}", "/x/{{bad}}", "/#/{{nobody}}", "/a/../b#x"} {
		if _, err := script.RenderWeb(domain.WebAction{Do: domain.WebOpen, Path: in}, vars); err == nil {
			t.Errorf("RenderWeb(%q) accepted it", in)
		}
	}
}
