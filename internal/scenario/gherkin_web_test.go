package scenario_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

// A feature file written before the web checks were added reads as it did, with the same hash of what a
// person confirmed. The hashes were taken from the code before the change.
func TestAFeatureFileOfTheEarlierGrammarMeansTheSame(t *testing.T) {
	for file, want := range map[string]domain.Hash{
		"testdata/full.feature":  "2c1b182ed4a410713db7261dbe760ee6f10a4479573d2dea90befe2e261e5bb4",
		"testdata/valid.feature": "8e44c6f1b96eebc0d6553cbdff28198a8fd28bbff9730ae2dcd094ac449ad121",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		sc, err := scenario.Parse(data, file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if got := domain.ExpectHash(sc); got != want {
			t.Errorf("%s: hash = %s, want %s", file, got, want)
		}
		for _, st := range sc.Steps {
			for _, e := range st.Expect {
				if e.Check.Page != "" || e.Check.Attribute != "" || e.Check.State != "" {
					t.Errorf("%s: a line of the earlier grammar read as a new check: %+v", file, e.Check)
				}
			}
		}
	}
}

func TestWebChecksAreOneGherkinLineEach(t *testing.T) {
	for line, want := range map[string]domain.Check{
		`the element total is hidden`:                         {Element: "total", State: "hidden"},
		`the element total is visible`:                        {Element: "total", State: "visible"},
		`the element save is enabled`:                         {Element: "save", State: "enabled"},
		`the element save is disabled`:                        {Element: "save", State: "disabled"},
		`the element agree is checked`:                        {Element: "agree", State: "checked"},
		`the element agree is not checked`:                    {Element: "agree", State: "unchecked"},
		`the element docs attribute href equals "/docs"`:      {Element: "docs", Attribute: "href", Equals: "/docs"},
		`the element docs attribute data-id contains "7"`:     {Element: "docs", Attribute: "data-id", Contains: "7"},
		`the element docs attribute href does not equal "/x"`: {Element: "docs", Attribute: "href", NotEquals: "/x"},
		`the page path equals "/orders"`:                      {Page: "path", Equals: "/orders"},
		`the page title contains "Orders"`:                    {Page: "title", Contains: "Orders"},
		`the page title does not equal "Sign in"`:             {Page: "title", NotEquals: "Sign in"},
		`the element row has 3 items`:                         {Element: "row", Count: ptr(3)},
		`the element toast does not exist`:                    {Element: "toast", Exists: ptr(false)},
	} {
		sc := domain.Scenario{
			APIVersion: "semar/v1", Kind: "Scenario", ID: "W-1", Title: "t", Ticket: "T-1", Surface: domain.SurfaceAPI, AuthProfile: "default",
			Steps: []domain.Step{{ID: "s", Action: "do it", Expect: []domain.Expectation{{Check: want, Source: domain.SourceDiff}}}},
		}
		data, err := scenario.Render(sc, "")
		if err != nil {
			t.Errorf("%s: Render: %v", line, err)
			continue
		}
		if !strings.Contains(string(data), "Then "+line+"\n") {
			t.Errorf("the line %q is not what Render writes:\n%s", line, data)
		}
		got, err := scenario.Parse(data, "w.feature")
		if err != nil {
			t.Errorf("%s: Parse: %v", line, err)
			continue
		}
		if c := got.Steps[0].Expect[0].Check; !checkEqual(c, want) {
			t.Errorf("%s: read %+v, want %+v", line, c, want)
		}
		if domain.ExpectHash(got) != domain.ExpectHash(sc) {
			t.Errorf("%s: the hash changed in the round trip", line)
		}
	}
}

func checkEqual(a, b domain.Check) bool {
	return domain.ExpectHash(domain.Scenario{Steps: []domain.Step{{Expect: []domain.Expectation{{Check: a}}}}}) ==
		domain.ExpectHash(domain.Scenario{Steps: []domain.Step{{Expect: []domain.Expectation{{Check: b}}}}})
}

func TestWebChecksThatAreNotWellFormedAreRefused(t *testing.T) {
	for check, want := range map[string]string{
		`{ page: title, exists: true }`:                    "a page check needs exactly one of equals, notEquals, or contains",
		`{ page: url, equals: x }`:                         "page must be path or title",
		`{ element: card, state: flying }`:                 "must be one of hidden, visible",
		`{ element: card, attribute: href, exists: true }`: "an attribute is read with equals, notEquals, or contains",
		`{ element: card, attribute: "a b", equals: x }`:   "is not the name of an attribute",
		`{ state: hidden, jsonpath: "$.a" }`:               "a state and an attribute belong to an element check",
		`{ page: path, element: card, equals: x }`:         "exactly one of status, jsonpath, header, element, page",
		`{ element: card, state: hidden, equals: x }`:      "needs exactly one operator",
	} {
		if got := problems(t, check); !strings.Contains(got, want) {
			t.Errorf("%s: problems = %q, want %q", check, got, want)
		}
	}
	for _, ok := range []string{
		`{ page: path, equals: /orders }`, `{ page: title, contains: Orders }`, `{ element: card, state: hidden }`,
		`{ element: card, attribute: href, contains: orders }`,
	} {
		if got := problems(t, ok); got != "" {
			t.Errorf("%s: %s", ok, got)
		}
	}
}

func TestAWebLineThatIsNotOneOfTheFormsIsRefusedWithTheList(t *testing.T) {
	feature := `Feature: T-1

  @id:W-1 @ticket:T-1 @surface:api
  Scenario: t
    When Do it
    # step: s
    Then the element card is purple
    # diff
`
	_, err := scenario.Parse([]byte(feature), "w.feature")
	if err == nil || !strings.Contains(err.Error(), "the element <id> is hidden") || !strings.Contains(err.Error(), "the page path equals") {
		t.Errorf("error = %v", err)
	}
}
