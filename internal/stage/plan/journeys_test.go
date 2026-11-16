package plan

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func legOf(id, service, method, path string) domain.Leg {
	return domain.Leg{
		ID: domain.LegID(id), Surface: domain.SurfaceAPI, Service: service,
		Steps: []domain.LegStep{{ID: "step", Hint: &domain.RequestHint{Method: method, Path: path}}},
	}
}

func journeyOf(id, criticality string, legs ...domain.Leg) domain.Journey {
	return domain.Journey{APIVersion: "semar/v1", Kind: "Journey", ID: domain.JourneyID(id), Title: "t", Owner: "team-commerce", Criticality: criticality, Legs: legs}
}

func checkoutJourney() domain.Journey {
	return journeyOf("checkout", "critical",
		legOf("place", "orders", "POST", "/v1/orders"),
		legOf("pay", "payments", "POST", "/v1/payments"),
		legOf("settled", "orders", "GET", "/v1/orders/{id}"))
}

// planWithJourneys renders the plan of the golden draft with these journey files.
func planWithJourneys(t *testing.T, env string, js ...domain.Journey) string {
	t.Helper()
	in := goldenInput(t)
	in.Journeys, in.Environment = js, env
	return render(t, in, goldenOutput(t, in, draft(t)))
}

func TestJourneysToRerunMatchesHint(t *testing.T) {
	got := planWithJourneys(t, "local", checkoutJourney())
	want := "- checkout (critical, owner team-commerce): leg place calls POST /v1/orders, which SHOP-482-01 and SHOP-483-01 test; " +
		"leg settled calls GET /v1/orders/{id}, which SHOP-483-01 tests. Run: semar journey run checkout --env local\n"
	if !strings.Contains(got, "## Journeys to rerun\n\n"+want) {
		t.Errorf("plan.md lacks the line:\n%s", got)
	}
	// The section follows "Fixtures needed" and comes before the files that no scenario covers.
	fixtures, journeys, uncovered := strings.Index(got, "## Fixtures needed"), strings.Index(got, "## Journeys to rerun"), strings.Index(got, "## Files that no scenario covers")
	if fixtures < 0 || journeys < fixtures || uncovered < journeys {
		t.Errorf("section order: fixtures %d, journeys %d, uncovered %d", fixtures, journeys, uncovered)
	}
	golden(t, "plan-journeys.golden.md", got)
}

func TestJourneysToRerunMatchesATemplateInBothDirections(t *testing.T) {
	// A hint with another parameter name, and a hint with a literal segment, both fit the drafted {id}.
	got := planWithJourneys(t, "",
		journeyOf("by-name", "standard", legOf("read", "orders", "GET", "/v1/orders/{order}")),
		journeyOf("literal", "standard", legOf("read", "orders", "GET", "/v1/orders/o-1")))
	for _, want := range []string{
		"- by-name (standard, owner team-commerce): leg read calls GET /v1/orders/{order}, which SHOP-483-01 tests. Run: semar journey run by-name\n",
		"- literal (standard, owner team-commerce): leg read calls GET /v1/orders/o-1, which SHOP-483-01 tests. Run: semar journey run literal\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan.md lacks %q:\n%s", want, got)
		}
	}
}

func TestJourneysToRerunOtherServiceDoesNotMatch(t *testing.T) {
	got := planWithJourneys(t, "local", journeyOf("billing", "critical", legOf("read", "payments", "GET", "/v1/orders/{id}")))
	if strings.Contains(got, "leg read calls") {
		t.Errorf("a leg of another service matched:\n%s", got)
	}
	if !strings.Contains(got, "## Journeys to rerun\n\nNo journey calls a route that this plan tests.\n") {
		t.Errorf("plan.md lacks the sentence for no match:\n%s", got)
	}
}

func TestJourneysToRerunOtherMethodOrPathDoesNotMatch(t *testing.T) {
	got := planWithJourneys(t, "local", journeyOf("other", "standard",
		legOf("delete", "orders", "DELETE", "/v1/orders/{id}"),
		legOf("list", "orders", "GET", "/v1/orders"),
		legOf("deep", "orders", "GET", "/v1/orders/{id}/lines")))
	if strings.Contains(got, "- other (") {
		t.Errorf("a leg that calls another route matched:\n%s", got)
	}
}

func TestNoJourneyFilesNoSection(t *testing.T) {
	in := goldenInput(t)
	got := render(t, in, goldenOutput(t, in, draft(t)))
	if strings.Contains(got, "Journeys to rerun") {
		t.Errorf("plan.md has a section for no journey file:\n%s", got)
	}
	golden(t, "plan.golden.md", got)
}

func TestJourneysToRerunEscapesTheText(t *testing.T) {
	j := journeyOf("checkout", "critical", legOf("place", "orders", "POST", "/v1/orders"))
	j.Owner = "team-<b>[x](http://evil.example)"
	j.Legs[0].ID = "pl|ace"
	got := planWithJourneys(t, "lo`cal", j)
	for _, bad := range []string{"<b>", "](http://", "pl|ace", "lo`cal"} {
		if strings.Contains(got, bad) {
			t.Errorf("plan.md holds %q:\n%s", bad, got)
		}
	}
}

func TestJourneysToRerunUsesTheTemplateOfTheStepHint(t *testing.T) {
	// A step with no hint calls nothing that a plan could match.
	l := legOf("place", "orders", "POST", "/v1/orders")
	l.Steps = append(l.Steps, domain.LegStep{ID: "bare"})
	got := planWithJourneys(t, "local", journeyOf("checkout", "critical", l))
	if !strings.Contains(got, "leg place calls POST /v1/orders, which SHOP-482-01 and SHOP-483-01 test") {
		t.Errorf("plan.md:\n%s", got)
	}
}
