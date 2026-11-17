package journey

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/screens"
)

const webJourney = `apiVersion: semar/v1
kind: Journey
id: card-flow
title: A card moved by Bob shows for Alice
owner: team-board
criticality: critical
authProfile: alice
legs:
  - id: move
    surface: api
    service: board
    steps:
      - id: move
        action: Bob moves the card
        hint: { method: POST, path: "/v1/cards/{id}/move" }
        request: { method: POST, path: "/v1/cards/c1/move" }
        expect:
          - check: { status: 200 }
            source: person
        captures:
          - { name: title, jsonpath: "$.data.title" }
    outputs: [title]
  - id: web_read
    surface: web
    service: board
    app: kanban
    as: alice
    needs: [move]
    steps:
      - id: open
        action: Alice opens the board
        web: { do: open, path: "/" }
      - id: read
        action: Read the card in Doing
        web: { do: wait, screen: Board, element: doing-card }
        expect:
          - check: { element: doing-card, equals: "{{move.title}}" }
            source: person
        captures:
          - { name: shown, element: doing-card }
    outputs: [shown]
invariants:
  - equal: ["{{move.title}}", "{{web_read.shown}}"]
`

const kanbanScreens = `apiVersion: semar/v1
kind: Screens
app: kanban
screens:
  Board:
    doing-card: { role: heading, name: Buy milk }
`

func parseWeb(t *testing.T, text string) domain.Journey {
	t.Helper()
	j, err := Parse([]byte(text), "card-flow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func screensOf(text string) ScreensFunc {
	return func(app string) (domain.Screens, error) {
		if text == "" {
			return domain.Screens{}, fmt.Errorf("%s: %w", screens.Path(app), screens.ErrNotFound)
		}
		return screens.Parse([]byte(text), screens.Path(app), app)
	}
}

func problemsOf(ps []Problem) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Path+": "+p.Message)
	}
	return strings.Join(out, "\n")
}

func TestAWebLegIsValid(t *testing.T) {
	j := parseWeb(t, webJourney)
	if ps := ValidateWithScreens(j, nil, screensOf(kanbanScreens)); len(ps) != 0 {
		t.Fatalf("problems:\n%s", problemsOf(ps))
	}
	if ps := Validate(j, nil); len(ps) != 0 {
		t.Fatalf("problems without the screens:\n%s", problemsOf(ps))
	}
}

func TestAWebLegBecomesAWebScenario(t *testing.T) {
	j := parseWeb(t, webJourney)
	sc, sr := LegScenario(j, "web_read", "alice")
	if sc.Surface != domain.SurfaceWeb || sc.AuthProfile != "alice" || sc.ID != "card-flow.web_read" {
		t.Fatalf("scenario = %+v", sc)
	}
	if sc.Steps[0].Hint != nil || sc.Steps[1].As != "alice" {
		t.Errorf("steps = %+v", sc.Steps)
	}
	if sc.Steps[1].Expect[0].Check.Element != "doing-card" {
		t.Errorf("the check lost its element: %+v", sc.Steps[1].Expect)
	}
	w := sr.Steps[1].Web
	if sr.Steps[1].Request != nil || w == nil || w.Service != "board" || w.App != "kanban" || w.Element != "doing-card" {
		t.Errorf("script step = %+v", sr.Steps[1])
	}
	if sr.Steps[1].Captures[0].Element != "doing-card" {
		t.Errorf("captures = %+v", sr.Steps[1].Captures)
	}
	if sr.ExpectHash != domain.ExpectHash(sc) {
		t.Error("the script is not bound to the scenario")
	}
	if api, _ := LegScenario(j, "move", "alice"); api.Surface != domain.SurfaceAPI {
		t.Errorf("api leg surface = %s", api.Surface)
	}
}

func TestWebLegProblemsArePlain(t *testing.T) {
	cases := map[string]struct {
		edit       func(string) string
		screens    string
		path, want string
	}{
		"no app": {func(s string) string { return strings.Replace(s, "    app: kanban\n", "", 1) }, kanbanScreens,
			"legs[1].app", "app is required for a web leg"},
		"an app on an api leg": {func(s string) string {
			return strings.Replace(s, "    service: board\n    steps:\n      - id: move", "    service: board\n    app: kanban\n    steps:\n      - id: move", 1)
		}, kanbanScreens,
			"legs[0].app", "app is for a web leg"},
		"a request on a web step": {func(s string) string {
			return strings.Replace(s, "        web: { do: open, path: \"/\" }", "        web: { do: open, path: \"/\" }\n        request: { method: GET, path: \"/\" }", 1)
		}, kanbanScreens, "legs[1].steps[0].request", "a web action, and not a request"},
		"a hint on a web step": {func(s string) string {
			return strings.Replace(s, "        web: { do: open, path: \"/\" }", "        web: { do: open, path: \"/\" }\n        hint: { method: GET, path: \"/\" }", 1)
		}, kanbanScreens, "legs[1].steps[0].hint", "a step of a web leg has no hint"},
		"a web action on an api step": {func(s string) string {
			return strings.Replace(s, "        request: { method: POST, path: \"/v1/cards/c1/move\" }", "        request: { method: POST, path: \"/v1/cards/c1/move\" }\n        web: { do: open, path: \"/\" }", 1)
		}, kanbanScreens, "legs[0].steps[0].web", "a step of an api leg has a request"},
		"a web step with no action": {func(s string) string { return strings.Replace(s, "        web: { do: open, path: \"/\" }\n", "", 1) }, kanbanScreens,
			"legs[1].steps[0].web", "needs web: its one browser action"},
		"an unknown action": {func(s string) string { return strings.Replace(s, "do: open", "do: tap", 1) }, kanbanScreens,
			"legs[1].steps[0].web", "do must be one of open, click, fill, press, drag, wait"},
		"a wait": {func(s string) string {
			return strings.Replace(s, "    needs: [move]\n", "    needs: [move]\n    wait: { until: { jsonpath: \"$.a\", equals: 1 }, timeout: 10s }\n", 1)
		}, kanbanScreens, "legs[1].wait", "a web leg cannot wait"},
		"a screens file that is not there": {func(s string) string { return s }, "",
			"legs[1].app", "the screens of kanban were not found: .semar/screens/kanban.yaml"},
		"a screen that the file lacks": {func(s string) string { return strings.Replace(s, "screen: Board", "screen: Basket", 1) }, kanbanScreens,
			"legs[1].steps[1].web", "screen Basket is not in the screens of kanban; its screens are Board"},
		"an element that the file lacks": {func(s string) string {
			return strings.Replace(s, "element: doing-card }\n        expect", "element: nothing }\n        expect", 1)
		}, kanbanScreens,
			"legs[1].steps[1].web", "element nothing is not on screen Board of kanban; its elements are doing-card"},
		"a check on an element that the file lacks": {func(s string) string {
			return strings.Replace(s, "check: { element: doing-card,", "check: { element: ghost,", 1)
		}, kanbanScreens,
			"legs[1].steps[1].web", "element ghost is not on screen Board"},
		"an output of a leg that is not needed": {func(s string) string {
			return strings.Replace(strings.Replace(s, "    needs: [move]\n", "", 1), `web: { do: open, path: "/" }`, `web: { do: open, path: "/{{move.title}}" }`, 1)
		}, kanbanScreens, "legs[1].steps[0].web", "is an output of leg move, which leg web_read does not need"},
		"a variable that is not an output": {func(s string) string {
			return strings.Replace(s, `web: { do: open, path: "/" }`, `web: { do: open, path: "/{{move.nope}}" }`, 1)
		}, kanbanScreens, "legs[1].steps[0].web", "{{move.nope}} is not an output of leg move; its outputs are title"},
		"an element capture that is not an output": {func(s string) string { return strings.Replace(s, "outputs: [shown]", "outputs: [other]", 1) }, kanbanScreens,
			"legs[1].outputs", "other is not captured by any step of leg web_read"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			text := c.edit(webJourney)
			if text == webJourney && name != "a screens file that is not there" {
				t.Fatal("the edit changed nothing")
			}
			j, err := Parse([]byte(text), "card-flow.yaml")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			ps := ValidateWithScreens(j, nil, screensOf(c.screens))
			if !hasProblem(ps, c.path, c.want) {
				t.Fatalf("no problem at %q holding %q:\n%s", c.path, c.want, problemsOf(ps))
			}
			for _, p := range ps {
				for _, bad := range []string{"domain.", "struct", "*", "yaml:"} {
					if strings.Contains(p.Message, bad) && !strings.Contains(p.Message, "*)") {
						t.Errorf("the problem names a Go type or a library: %q", p.Message)
					}
				}
			}
		})
	}
}

func TestAScreensFileThatCannotBeUsedIsAProblemOfTheLeg(t *testing.T) {
	j := parseWeb(t, webJourney)
	ps := ValidateWithScreens(j, nil, func(string) (domain.Screens, error) { return domain.Screens{}, errors.New("line 3: unknown key roll") })
	if !hasProblem(ps, "legs[1].app", "the screens of kanban cannot be used: line 3: unknown key roll") {
		t.Errorf("problems:\n%s", problemsOf(ps))
	}
}

func TestUnknownKeyOfAWebStepIsPlain(t *testing.T) {
	_, err := Parse([]byte(strings.Replace(webJourney, "web: { do: open, path: \"/\" }", "web: { do: open, path: \"/\", url: x }", 1)), "card-flow.yaml")
	if err == nil || !strings.Contains(err.Error(), "unknown key url") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "domain.") || strings.Contains(err.Error(), "WebAction") {
		t.Errorf("the error names a Go type: %v", err)
	}
}
