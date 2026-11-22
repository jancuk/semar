package edge_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/edge"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/script"
)

// refundBase is a scenario with a write that gives an ID and a read that uses it.
func refundBase() (domain.Scenario, domain.Script) {
	status := 200
	sc := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "PROJ-12-01", Title: "An order is read", Ticket: "PROJ-12",
		Surface: domain.SurfaceAPI, AuthProfile: "default",
		Steps: []domain.Step{
			{ID: "make", Setup: true, Action: "Create an order", Hint: &domain.RequestHint{Method: "POST", Path: "/v1/orders"}},
			{ID: "read", Action: "Read the order", Hint: &domain.RequestHint{Method: "GET", Path: "/v1/orders/{id}"},
				Expect: []domain.Expectation{{Check: domain.Check{Status: &status}, Source: domain.SourceTicket, Cite: "docs/t.md:3"}}},
		},
	}
	s := domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID,
		Steps: []domain.ScriptStep{
			{Step: "make", Request: &domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders", JSON: map[string]any{"amount": 1}},
				Captures: []domain.Capture{{Name: "order_id", JSONPath: "$.data.id"}}},
			{Step: "read", Request: &domain.APIRequest{Service: "orders", Method: "GET", Path: "/v1/orders/{{order_id}}"}},
		},
	}
	return sc, s
}

func TestAnUnknownIDCaseNeedsNoEarlierStep(t *testing.T) {
	sc, s := refundBase()
	op := operation(t, "GET", "/orders/{id}")
	cases, _ := edge.Generate(op, *s.Steps[1].Request)
	c := find(t, cases, edge.UnknownID, "id")
	got, gs, err := edge.Build(sc, s, "read", c, "PROJ-12-E01", "An edge case.")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Steps) != 1 || len(gs.Steps) != 1 {
		t.Fatalf("steps %d and %d, want only the changed step, because no variable is left", len(got.Steps), len(gs.Steps))
	}
	if got.Edge == nil || got.Edge.Kind != "unknown-id" || got.Edge.Base != "PROJ-12-01" || got.Edge.On != "id" {
		t.Errorf("edge mark = %+v", got.Edge)
	}
	ex := got.Steps[0].Expect
	if len(ex) != 1 || *ex[0].Check.Status != 404 || ex[0].Source != domain.SourceRule {
		t.Errorf("expect = %+v", ex)
	}
	if ps := scenario.Validate(got, scenario.FromFile); len(ps) > 0 {
		t.Errorf("scenario problems: %v", ps)
	}
	if ps := script.Validate(got, gs); len(ps) > 0 {
		t.Errorf("script problems: %v", ps)
	}
	if gs.AuthoredBy != edge.Rule {
		t.Errorf("authoredBy = %+v", gs.AuthoredBy)
	}
	if sc.Steps[1].Expect[0].Source != domain.SourceTicket {
		t.Error("the base scenario was changed")
	}
}

func TestANoSessionCaseKeepsTheStepThatGivesTheValueAndActsAsTheBaseAccount(t *testing.T) {
	sc, s := refundBase()
	op := operation(t, "GET", "/orders/{id}")
	op.Statuses = append(op.Statuses, "401")
	cases, _ := edge.Generate(op, *s.Steps[1].Request)
	c := find(t, cases, edge.NoSession, "")
	got, gs, err := edge.Build(sc, s, "read", c, "PROJ-12-E02", "x")
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthProfile != "" {
		t.Errorf("authProfile = %q, want none", got.AuthProfile)
	}
	if len(got.Steps) != 2 || got.Steps[0].As != "default" || !got.Steps[0].Setup || got.Steps[1].As != "" {
		t.Errorf("steps = %+v", got.Steps)
	}
	if len(gs.Steps[0].Captures) != 1 {
		t.Error("the setup step lost its capture")
	}
	if ps := script.Validate(got, gs); len(ps) > 0 {
		t.Errorf("script problems: %v", ps)
	}
	data, err := scenario.Render(got, "")
	if err != nil {
		t.Fatal(err)
	}
	back, err := scenario.Parse(data, "x")
	if err != nil {
		t.Fatal(err)
	}
	if back.Edge == nil || *back.Edge != *got.Edge || back.AuthProfile != "" {
		t.Errorf("the feature file does not keep the edge mark: %+v\n%s", back.Edge, data)
	}
}

func TestAnotherAccountIsRefusedWhenTheBaseUsesAFixture(t *testing.T) {
	sc, s := refundBase()
	sc.Given = []domain.Given{{Fixture: "order.open", As: "o"}}
	s.Steps[1].Request.Path = "/v1/orders/{{o.id}}"
	c := edge.OtherAccountCase(*s.Steps[1].Request, "bob")
	c.Status = 403
	if _, _, err := edge.Build(sc, s, "read", c, "PROJ-12-E03", ""); err == nil || err.Error() != edge.ReasonFixture {
		t.Errorf("err = %v", err)
	}
}

func TestKeyNamesTheTicketTheOperationTheKindAndTheTarget(t *testing.T) {
	sc, s := refundBase()
	op := operation(t, "GET", "/orders/{id}")
	cases, _ := edge.Generate(op, *s.Steps[1].Request)
	got, _, err := edge.Build(sc, s, "read", find(t, cases, edge.UnknownID, "id"), "PROJ-12-E01", "")
	if err != nil {
		t.Fatal(err)
	}
	if k := edge.KeyOf(got); k != "PROJ-12|GET /v1/orders/{id}|unknown-id|id" {
		t.Errorf("key = %q", k)
	}
	if edge.KeyOf(sc) != "" {
		t.Error("a scenario that is no edge case has a key")
	}
}

func TestDroppedCasesAreRemembered(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".semar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if list, err := edge.ReadDropped(dir); err != nil || len(list) != 0 {
		t.Fatalf("empty = %v, %v", list, err)
	}
	d := edge.Dropped{Key: "PROJ-12|GET /v1/x|no-session|", Scenario: "PROJ-12-E01", Reason: "dropped in the review", User: "tester", Day: "2026-11-14"}
	for range 2 {
		if err := edge.AddDropped(dir, d); err != nil {
			t.Fatal(err)
		}
	}
	list, err := edge.ReadDropped(dir)
	if err != nil || len(list) != 1 || list[0] != d {
		t.Fatalf("list = %v, %v", list, err)
	}
	if !edge.Keys(list)[d.Key] {
		t.Error("the key is not in the set")
	}
	if err := os.WriteFile(filepath.Join(dir, edge.DroppedFile), []byte("nonsense: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := edge.ReadDropped(dir); err == nil {
		t.Error("a file that is not an edge-dropped file was read")
	}
}

func TestAValueReadOnADeviceCannotFeedAnEdgeCase(t *testing.T) {
	sc, s := refundBase()
	// The step that gives the ID is a step on a device: an edge case runs only API steps.
	s.Steps[0] = domain.ScriptStep{Step: "make", Mobile: &domain.MobileAction{Service: "app", Do: domain.MobileLaunch},
		Captures: []domain.Capture{{Name: "order_id", Element: "id"}}}
	op := operation(t, "GET", "/orders/{id}")
	cases, _ := edge.Generate(op, *s.Steps[1].Request)
	c := find(t, cases, edge.UnknownID, "id")
	// An unknown id needs no earlier step, so it is built.
	if _, _, err := edge.Build(sc, s, "read", c, "PROJ-12-E01", "x"); err != nil {
		t.Errorf("a case that needs no earlier value: %v", err)
	}
	op.Statuses = append(op.Statuses, "401")
	cases, _ = edge.Generate(op, *s.Steps[1].Request)
	c = find(t, cases, edge.NoSession, "")
	if _, _, err := edge.Build(sc, s, "read", c, "PROJ-12-E02", "x"); err == nil || !strings.Contains(err.Error(), "reads a value on a device") {
		t.Errorf("error = %v, want a refusal that names the device step", err)
	}
}
