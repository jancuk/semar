package scenario_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/scenario"
)

func webScenario(check string) string {
	return `apiVersion: semar/v1
kind: Scenario
id: board-web
title: The board shows the card
ticket: BOARD-1
intent: The board shows the card
surface: web
authProfile: alice
steps:
  - id: read
    action: Read the card on the board
    expect:
      - check: ` + check + `
        source: person
`
}

func problems(t *testing.T, check string) string {
	t.Helper()
	sc, err := scenario.Parse([]byte(webScenario(check)), "board-web.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range scenario.Validate(sc, scenario.FromFile) {
		out = append(out, p.Path+": "+p.Message)
	}
	return strings.Join(out, "\n")
}

func TestElementChecksValidate(t *testing.T) {
	for _, ok := range []string{
		`{ element: card, equals: Buy milk }`,
		`{ element: card, notEquals: Buy eggs }`,
		`{ element: card, contains: milk }`,
		`{ element: card, exists: false }`,
		`{ element: card, count: 3 }`,
	} {
		if got := problems(t, ok); got != "" {
			t.Errorf("%s: %s", ok, got)
		}
	}
	for check, want := range map[string]string{
		`{ element: card }`:                             "needs exactly one operator",
		`{ element: card, equals: a, exists: true }`:    "needs exactly one operator",
		`{ element: card, greater: 1 }`:                 "the operator greater is not for an element",
		`{ element: card, jsonpath: "$.a", equals: 1 }`: "exactly one of status, jsonpath, header, element",
		`{ element: card, status: 200 }`:                "exactly one of status, jsonpath, header, element",
		`{ element: "a b", exists: true }`:              "must start with a letter or a digit",
		`{ element: card, count: -1 }`:                  "count must not be negative",
	} {
		if got := problems(t, check); !strings.Contains(got, want) {
			t.Errorf("%s: problems %q, want one with %q", check, got, want)
		}
	}
}

func TestElementCheckRoundTrips(t *testing.T) {
	sc, err := scenario.Parse([]byte(webScenario(`{ element: card, equals: Buy milk }`)), "board-web.yaml")
	if err != nil {
		t.Fatal(err)
	}
	data, err := scenario.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "element: card") {
		t.Errorf("the element was not written:\n%s", data)
	}
	back, err := scenario.Parse(data, "x.yaml")
	if err != nil || back.Steps[0].Expect[0].Check.Element != "card" {
		t.Errorf("round trip: %v, %+v", err, back)
	}
}
