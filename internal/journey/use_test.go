package journey

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

const useJourney = `apiVersion: semar/v1
kind: Journey
id: phone-flow
title: A reference is used by the API
owner: team-shop
criticality: standard
authProfile: alice
legs:
  - id: phone
    surface: android
    service: shop-app
    app: shop-app
    steps:
      - id: sign-in
        action: Sign in
        use: login
        with: { email: ana@example.com }
      - id: seen
        action: See the order
        mobile: { do: wait, screen: SignIn, element: welcome }
        expect:
          - check: { element: welcome, contains: Order }
            source: person
    outputs: [orderRef]
  - id: echo
    surface: api
    service: orders
    needs: [phone]
    steps:
      - id: create
        action: Create an order
        hint: { method: POST, path: /v1/orders }
        request: { method: POST, path: /v1/orders, json: { ref: "{{phone.orderRef}}" } }
        expect:
          - check: { status: 201 }
            source: person
`

func loginUsing() domain.Flow {
	return domain.Flow{
		APIVersion: "semar/v1", Kind: "Flow", ID: "login", Surface: domain.SurfaceAndroid, Service: "shop-app", App: "shop-app",
		Params: []domain.FlowParam{{Name: "email"}},
		Steps: []domain.ScriptStep{
			{Step: "start", Mobile: &domain.MobileAction{Do: domain.MobileLaunch}},
			{Step: "go", Mobile: &domain.MobileAction{Do: domain.MobileTap, Screen: "SignIn", Element: "submit"},
				Captures: []domain.Capture{{Name: "orderRef", Element: "welcome", Pattern: `#(\w+)`}}},
		},
	}
}

func flowResolver(fs ...domain.Flow) func(string) (domain.Flow, error) {
	return func(id string) (domain.Flow, error) {
		for _, f := range fs {
			if f.ID == id {
				return f, nil
			}
		}
		return domain.Flow{}, fmt.Errorf("the flow file was not found")
	}
}

func useProblems(t *testing.T, edit func(string) string, fs ...domain.Flow) string {
	t.Helper()
	text := useJourney
	if edit != nil {
		if text = edit(text); text == useJourney {
			t.Fatal("the edit did not apply")
		}
	}
	j, err := Parse([]byte(text), "phone-flow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return problemsOf(ValidateWithFlows(j, nil, screensOf(shopAppScreens), flowResolver(fs...)))
}

func TestAnAndroidLegMayUseAFlowAndOutputItsCapture(t *testing.T) {
	if got := useProblems(t, nil, loginUsing()); got != "" {
		t.Fatalf("problems:\n%s", got)
	}
	// Without the flows, a use step is not looked into, and the output is not judged.
	j, err := Parse([]byte(useJourney), "phone-flow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ps := ValidateWithScreens(j, nil, screensOf(shopAppScreens)); len(ps) != 1 || !strings.Contains(ps[0].Message, "orderRef is not captured by any step") {
		t.Errorf("problems without flows:\n%s", problemsOf(ps))
	}
}

func TestALegStepThatUsesAFlowBecomesAUseStep(t *testing.T) {
	j, err := Parse([]byte(useJourney), "phone-flow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sc, sr := LegScenario(j, "phone", "alice")
	st := sr.Steps[0]
	if st.Use != "login" || st.With["email"] != "ana@example.com" || st.Mobile != nil || st.Request != nil || sc.Steps[0].ID != "sign-in" {
		t.Errorf("script step = %+v", st)
	}
	if sr.Steps[1].Mobile == nil {
		t.Errorf("the next step lost its action: %+v", sr.Steps[1])
	}
}

func TestAUseStepProblemsArePlain(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(string) string
		want string
	}{
		{"an unknown param", func(s string) string {
			return strings.Replace(s, "with: { email: ana@example.com }", "with: { email: a, color: red }", 1)
		}, "legs[0].steps[0].with: flow login has no param color"},
		{"a missing param", func(s string) string { return strings.Replace(s, "        with: { email: ana@example.com }\n", "", 1) }, "flow login needs the param email"},
		{"an action as well", func(s string) string {
			return strings.Replace(s, "        use: login\n", "        use: login\n        mobile: { do: launch }\n", 1)
		}, "legs[0].steps[0].mobile: a step that uses a flow has no mobile action"},
		{"captures", func(s string) string {
			return strings.Replace(s, "        use: login\n", "        use: login\n        captures: [{ name: x, element: welcome }]\n", 1)
		}, "takes no captures"},
		{"a check", func(s string) string {
			return strings.Replace(s, "        use: login\n", "        use: login\n        expect: [{ check: { element: welcome, equals: x }, source: person }]\n", 1)
		}, "takes no expected result"},
		{"a flow that is not there", func(s string) string { return strings.Replace(s, "use: login", "use: gone", 1) }, "legs[0].steps[0].use: flow gone: the flow file was not found"},
		{"an unknown leg output in with", func(s string) string {
			return strings.Replace(s, "email: ana@example.com", `email: "{{other.id}}"`, 1)
		}, "legs[0].steps[0].with: {{other.id}}"},
		{"use in an api leg", func(s string) string {
			return strings.Replace(s, "        hint: { method: POST, path: /v1/orders }\n", "        hint: { method: POST, path: /v1/orders }\n        use: login\n", 1)
		}, "legs[1].steps[0].use: use is for a step of an android leg"},
		{"with without use", func(s string) string {
			return strings.Replace(s, "        hint: { method: POST, path: /v1/orders }\n", "        hint: { method: POST, path: /v1/orders }\n        with: { a: b }\n", 1)
		}, "legs[1].steps[0].with: with belongs to a step that has use"},
		{"an output nobody captures", func(s string) string { return strings.Replace(s, "outputs: [orderRef]", "outputs: [nothing]", 1) }, "nothing is not captured by any step of leg phone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := useProblems(t, tc.edit, loginUsing())
			if !strings.Contains(got, tc.want) {
				t.Errorf("problems:\n%s\nwant one holding %q", got, tc.want)
			}
		})
	}
}

func TestACaptureThroughAUsedFlowInsideAFlowCountsAsAnOutput(t *testing.T) {
	outer := domain.Flow{
		APIVersion: "semar/v1", Kind: "Flow", ID: "outer", Surface: domain.SurfaceAndroid, Service: "shop-app", App: "shop-app",
		Params: []domain.FlowParam{{Name: "email"}},
		Steps:  []domain.ScriptStep{{Step: "in", Use: "login", With: map[string]string{"email": "{{email}}"}}},
	}
	got := useProblems(t, func(s string) string { return strings.Replace(s, "use: login", "use: outer", 1) }, loginUsing(), outer)
	if got != "" {
		t.Errorf("problems:\n%s", got)
	}
}

func TestAnInvalidCapturePatternInALegIsRefused(t *testing.T) {
	got := androidProblems(t, func(s string) string {
		return strings.Replace(s, "- { name: greeting, element: welcome }", "- { name: greeting, element: welcome, pattern: '([a-z' }", 1)
	}, shopAppScreens)
	if !strings.Contains(got, "not a valid regular expression") {
		t.Errorf("problems:\n%s", got)
	}
}
