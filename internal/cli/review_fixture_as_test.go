package cli

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestFixtureRequestsShowTheAccountOfAStep(t *testing.T) {
	fx := domain.Fixture{
		ID: "order.cancelled", Version: "1.0.0", Owner: "semar", Description: "A cancelled order of bob", Outputs: []string{"id"},
		Create: []domain.FixtureStep{
			{As: "bob", Request: domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders"}},
			{Request: domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders/{{id}}/cancel"}},
		},
		Cleanup: []domain.FixtureStep{{As: "bob", Request: domain.APIRequest{Service: "orders", Method: "DELETE", Path: "/v1/orders/{{id}}"}}},
	}
	sends, removes := fixtureRequests(fx)
	if len(sends) != 2 || !strings.HasPrefix(sends[0], "create 1 as bob: POST orders /v1/orders") || !strings.HasPrefix(sends[1], "create 2: POST orders") {
		t.Errorf("sends = %q", sends)
	}
	if len(removes) != 1 || !strings.HasPrefix(removes[0], "cleanup 1 as bob: DELETE orders") {
		t.Errorf("removes = %q", removes)
	}
	details := strings.Join(fixtureLines(fx, domain.FixtureHash(fx)), "\n")
	if !strings.Contains(details, "create 1 as bob: POST orders /v1/orders") {
		t.Errorf("the details do not show the account:\n%s", details)
	}
}
