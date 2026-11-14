package domain_test

import (
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func base() domain.Scenario {
	zero := 0
	status := 422
	return domain.Scenario{
		APIVersion:  "semar/v1",
		Kind:        "Scenario",
		ID:          "SHOP-482-01",
		Title:       "Refund above the order total is rejected",
		Ticket:      "SHOP-482",
		Intent:      "Before: a refund larger than the order total was accepted. Now: the API rejects it with 422 and leaves the order unchanged.",
		Surface:     domain.SurfaceAPI,
		AuthProfile: "support-agent",
		Steps: []domain.Step{
			{
				ID:     "create-order",
				Setup:  true,
				Action: "Create a paid order for 100000",
				Hint:   &domain.RequestHint{Method: "POST", Path: "/v1/orders"},
			},
			{
				ID:     "refund-too-much",
				Action: "Request a refund of 150000 for that order",
				Hint:   &domain.RequestHint{Method: "POST", Path: "/v1/orders/{id}/refunds"},
				Expect: []domain.Expectation{
					{
						Check:  domain.Check{Status: &status},
						Source: domain.SourceTicket,
						Cite:   "SHOP-482: a refund above the order total must be rejected",
					},
					{
						Check:  domain.Check{JSONPath: "$.error.code", Equals: "REFUND_EXCEEDS_TOTAL"},
						Source: domain.SourceDiff,
					},
				},
			},
			{
				ID:     "order-unchanged",
				Action: "Read the order again",
				Hint:   &domain.RequestHint{Method: "GET", Path: "/v1/orders/{id}"},
				Expect: []domain.Expectation{
					{
						Check:  domain.Check{JSONPath: "$.data.refunded_total", Equals: zero},
						Source: domain.SourceTicket,
						Cite:   "SHOP-482: the order must stay unchanged",
					},
				},
			},
		},
	}
}

func TestExpectHash(t *testing.T) {
	h := domain.ExpectHash(base())
	if len(h) != 64 {
		t.Fatalf("want a SHA-256 hex string, got %q", h)
	}

	same := base()
	same.Title = "another title"
	same.Steps[0].Action = "reworded"
	if domain.ExpectHash(same) != h {
		t.Error("title and action text must not change the hash")
	}

	for name, change := range map[string]func(*domain.Scenario){
		"intent":      func(s *domain.Scenario) { s.Intent += " and more" },
		"authProfile": func(s *domain.Scenario) { s.AuthProfile = "customer" },
		"hint":        func(s *domain.Scenario) { s.Steps[1].Hint.Method = "PUT" },
		"value":       func(s *domain.Scenario) { v := 409; s.Steps[1].Expect[0].Check.Status = &v },
		"source":      func(s *domain.Scenario) { s.Steps[1].Expect[0].Source = "diff" },
		"setup":       func(s *domain.Scenario) { s.Steps[0].Setup = false },
		"step id":     func(s *domain.Scenario) { s.Steps[2].ID = "renamed" },
	} {
		s := base()
		change(&s)
		if domain.ExpectHash(s) == h {
			t.Errorf("%s must change the hash", name)
		}
	}
}

func TestExpectHashEncodingFailureGivesEmptyHash(t *testing.T) {
	if got := domain.ExpectHash(base()); len(got) != 64 {
		t.Fatalf("base hash length = %d, want 64", len(got))
	}
	s := base()
	s.Steps[1].Expect[1].Check.Equals = make(chan int)
	if got := domain.ExpectHash(s); got != "" {
		t.Fatalf("ExpectHash with unencodable check = %q, want empty", got)
	}
}
