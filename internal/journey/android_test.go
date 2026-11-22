package journey

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

const androidJourney = `apiVersion: semar/v1
kind: Journey
id: phone-flow
title: An order ID is used in the app
owner: team-shop
criticality: standard
authProfile: alice
legs:
  - id: place
    surface: api
    service: orders
    steps:
      - id: create
        action: Create an order
        hint: { method: POST, path: /v1/orders }
        request: { method: POST, path: /v1/orders }
        captures:
          - { name: id, jsonpath: "$.data.id" }
    outputs: [id]
  - id: phone
    surface: android
    service: shop-app
    app: shop-app
    needs: [place]
    steps:
      - id: start
        action: Start the app
        mobile: { do: launch }
      - id: email
        action: Type the order ID
        mobile: { do: type, screen: SignIn, element: email, value: "{{place.id}}" }
      - id: sign-in
        action: Tap sign in
        mobile: { do: tap, screen: SignIn, element: submit }
        expect:
          - check: { element: welcome, equals: "Hi Ana" }
            source: person
        captures:
          - { name: greeting, element: welcome }
    outputs: [greeting]
`

const shopAppScreens = `apiVersion: semar/v1
kind: Screens
app: shop-app
screens:
  SignIn:
    email: { id: email }
    submit: { desc: Sign in }
    welcome: { id: welcome }
`

func androidProblems(t *testing.T, edit func(string) string, screens string) string {
	t.Helper()
	text := androidJourney
	if edit != nil {
		text = edit(text)
		if text == androidJourney {
			t.Fatal("the edit did not apply")
		}
	}
	j, err := Parse([]byte(text), "phone-flow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return problemsOf(ValidateWithScreens(j, nil, screensOf(screens)))
}

func TestAnAndroidLegIsValid(t *testing.T) {
	if got := androidProblems(t, nil, shopAppScreens); got != "" {
		t.Fatalf("problems:\n%s", got)
	}
	if got := androidProblems(t, nil, ""); !strings.Contains(got, "the screens of shop-app were not found") {
		t.Errorf("a missing screens file: %q", got)
	}
}

func TestAnAndroidLegBecomesAnAndroidScenario(t *testing.T) {
	j, err := Parse([]byte(androidJourney), "phone-flow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sc, sr := LegScenario(j, "phone", "alice")
	if sc.Surface != domain.SurfaceAndroid || sc.ID != "phone-flow.phone" || sc.AuthProfile != "alice" {
		t.Fatalf("scenario = %+v", sc)
	}
	m := sr.Steps[1].Mobile
	if sr.Steps[1].Request != nil || sr.Steps[1].Web != nil || m == nil || m.Service != "shop-app" || m.App != "shop-app" || m.Do != domain.MobileType || m.Value != "{{place.id}}" {
		t.Errorf("script step = %+v", sr.Steps[1])
	}
	if sr.Steps[2].Captures[0].Element != "welcome" || sc.Steps[2].Expect[0].Check.Element != "welcome" {
		t.Errorf("captures = %+v, expect = %+v", sr.Steps[2].Captures, sc.Steps[2].Expect)
	}
	if sc.Steps[0].Hint != nil {
		t.Errorf("a step of an android leg has a hint: %+v", sc.Steps[0].Hint)
	}
}

func TestAnAndroidLegProblemsArePlain(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(string) string
		want string
	}{
		{"no app", func(s string) string { return strings.Replace(s, "    app: shop-app\n", "", 1) }, "legs[1].app: app is required for an android leg"},
		{"a wait", func(s string) string {
			return strings.Replace(s, "    needs: [place]\n", "    needs: [place]\n    wait: { timeout: 30s, until: { jsonpath: \"$.a\", equals: x } }\n", 1)
		}, "legs[1].wait: an android leg cannot wait"},
		{"a hint", func(s string) string {
			return strings.Replace(s, "        action: Start the app\n", "        action: Start the app\n        hint: { method: GET, path: / }\n", 1)
		}, "legs[1].steps[0].hint: a step of an android leg has no hint"},
		{"a request", func(s string) string {
			return strings.Replace(s, "        mobile: { do: launch }\n", "        mobile: { do: launch }\n        request: { method: GET, path: / }\n", 1)
		}, "legs[1].steps[0].request: a step of an android leg has a mobile action, and not a request"},
		{"a web action", func(s string) string {
			return strings.Replace(s, "        mobile: { do: launch }\n", "        mobile: { do: launch }\n        web: { do: open, path: / }\n", 1)
		}, "legs[1].steps[0].web: a step of an android leg has a mobile action, and not a web action"},
		{"no action", func(s string) string { return strings.Replace(s, "        mobile: { do: launch }\n", "", 1) }, "legs[1].steps[0].mobile: a step of an android leg needs mobile"},
		{"another service", func(s string) string {
			return strings.Replace(s, "mobile: { do: launch }", "mobile: { service: other, do: launch }", 1)
		}, "legs[1].steps[0].mobile.service: other differs from the leg's service shop-app"},
		{"another app", func(s string) string {
			return strings.Replace(s, "mobile: { do: launch }", "mobile: { app: other, do: launch }", 1)
		}, "legs[1].steps[0].mobile.app: other differs from the leg's app shop-app"},
		{"a first step that is not a launch", func(s string) string {
			return strings.Replace(s, "mobile: { do: launch }", "mobile: { do: tap, screen: SignIn, element: submit }", 1)
		}, "the first mobile step of a service must be launch or open"},
		{"a missing element", func(s string) string { return strings.Replace(s, "element: submit", "element: gone", 1) }, "legs[1].steps[2].mobile: element gone is not on screen SignIn of shop-app"},
		{"an unknown variable", func(s string) string { return strings.Replace(s, "{{place.id}}", "{{other.id}}", 1) }, "legs[1].steps[1].mobile: {{other.id}} is not a capture"},
		{"mobile in an api leg", func(s string) string {
			return strings.Replace(s, "        request: { method: POST, path: /v1/orders }\n", "        request: { method: POST, path: /v1/orders }\n        mobile: { do: launch }\n", 1)
		}, "legs[0].steps[0].mobile: a step of an api leg has a request, and not a mobile action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := androidProblems(t, tc.edit, shopAppScreens)
			if !strings.Contains(got, tc.want) {
				t.Errorf("problems:\n%s\nwant one holding %q", got, tc.want)
			}
		})
	}
}

func TestAnAPILegHasNoApp(t *testing.T) {
	got := androidProblems(t, func(s string) string {
		return strings.Replace(s, "    service: orders\n", "    service: orders\n    app: shop-app\n", 1)
	}, shopAppScreens)
	if !strings.Contains(got, "legs[0].app: app is for a web leg or an android leg") {
		t.Errorf("problems:\n%s", got)
	}
}
