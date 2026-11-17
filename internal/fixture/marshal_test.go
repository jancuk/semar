package fixture

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func drafted() domain.Fixture {
	return domain.Fixture{
		APIVersion: "semar/v1", Kind: "Fixture", ID: "order.cancelled", Description: "A cancelled order of the default account",
		Version: "1.0.0", Owner: "semar",
		Create: []domain.FixtureStep{
			{Request: domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders", JSON: map[string]any{"amount": int64(1000), "ref": "semar-{{run.id}}"}},
				Captures: []domain.Capture{{Name: "id", JSONPath: "$.data.id"}}},
			{Request: domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders/{{id}}/cancel"}},
		},
		Outputs: []string{"id"},
		Cleanup: []domain.FixtureStep{{Request: domain.APIRequest{Service: "orders", Method: "GET", Path: "/v1/orders/{{id}}", Query: map[string]string{"x": "1"}}}},
	}
}

func TestMarshalWritesAFileThatParsesToTheSameFixture(t *testing.T) {
	fx := drafted()
	data, err := Marshal(fx)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "# Drafted by semar plan.") || !strings.Contains(text, "semar review order.cancelled") {
		t.Errorf("the file has no header:\n%s", text)
	}
	back, err := Parse(data, "order.cancelled.yaml")
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, text)
	}
	if got, want := domain.FixtureHash(back), domain.FixtureHash(fx); got != want {
		t.Errorf("hash after a round trip = %s, want %s\n%s", got, want, text)
	}
	if got := validate(back, "order.cancelled.yaml", []string{"orders"}); len(got) > 0 {
		t.Errorf("the loader refuses the file: %v\n%s", got, text)
	}
	if strings.Contains(text, "find:") || strings.Contains(text, "lease") || strings.Contains(text, "until") || strings.Contains(text, "params:") {
		t.Errorf("the file holds keys that a drafted fixture has no value for:\n%s", text)
	}
}
