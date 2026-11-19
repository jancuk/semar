package scenario_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

const suiteFile = `Feature: SHOP-1

  @id:SHOP-1-01 @ticket:SHOP-1 @surface:api @auth:default %s
  Scenario: Read an order
    When Read the order
    # step: read; GET /v1/orders/o-1
    Then the status is 200
    # ticket SHOP-1: reading works
`

func parseSuites(t *testing.T, tags string) (domain.Scenario, error) {
	t.Helper()
	return scenario.Parse([]byte(strings.Replace(suiteFile, "%s", tags, 1)), "m.feature")
}

func TestASuiteTagNamesSeveralSuites(t *testing.T) {
	sc, err := parseSuites(t, "@suite:smoke,checkout-2")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sc.Suites, "|"); got != "smoke|checkout-2" {
		t.Errorf("suites = %q", got)
	}
	none, err := parseSuites(t, "")
	if err != nil || none.Suites != nil {
		t.Errorf("no tag: suites %v, err %v", none.Suites, err)
	}
}

func TestASuiteTagIsNotPartOfWhatAPersonApproved(t *testing.T) {
	with, err := parseSuites(t, "@suite:smoke")
	if err != nil {
		t.Fatal(err)
	}
	without, err := parseSuites(t, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := parseSuites(t, "@suite:regression,smoke")
	if err != nil {
		t.Fatal(err)
	}
	if domain.ExpectHash(with) != domain.ExpectHash(without) || domain.ExpectHash(with) != domain.ExpectHash(other) {
		t.Error("adding or changing @suite: changes the expectation hash, so an approved scenario would ask for a new review")
	}
}

func TestASuiteTagIsRefusedWhenItsNamesAreNotValid(t *testing.T) {
	for tags, want := range map[string]string{
		"@suite:":            "has no value",
		"@suite":             "has no value",
		"@suite:Smoke":       "lower-case",
		"@suite:smoke,":      "lower-case",
		"@suite:smoke,smoke": "named twice",
		"@suite:a @suite:b":  "appears twice",
		"@suite:smoke_test":  "lower-case",
	} {
		if _, err := parseSuites(t, tags); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want one with %q", tags, err, want)
		}
	}
}

func TestSuitesSurviveRenderAndParse(t *testing.T) {
	in := gOne(domain.Check{Status: ptr(200)})
	in.Suites = []string{"smoke", "checkout"}
	want := in
	text := gRoundTrip(t, in, want)
	if !strings.Contains(text, "@suite:smoke,checkout") {
		t.Errorf("the file has no suite tag:\n%s", text)
	}
	in.Suites = []string{"Smoke"}
	if _, err := scenario.Render(in, ""); err == nil {
		t.Error("Render wrote a suite name that Parse refuses")
	}
}

func TestValidateRefusesASuiteNameThatIsNotValid(t *testing.T) {
	sc := gOne(domain.Check{Status: ptr(200)})
	sc.Suites = []string{"smoke", "Not Valid"}
	ps := scenario.Validate(sc, scenario.FromFile)
	if len(ps) != 1 || ps[0].Path != "suites[1]" {
		t.Errorf("problems = %+v", ps)
	}
}
