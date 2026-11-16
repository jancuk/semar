package domain_test

import (
	"os"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/script"
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

// The value must change only on purpose: a change lapses every stored confirmation and script.
func TestExpectHashGolden(t *testing.T) {
	const want = "a4550ee759d58595b8bcfa7c062711787affe3c3c5065653cf0a574e9a183a26"
	if got := domain.ExpectHash(base()); string(got) != want {
		t.Fatalf("ExpectHash(base()) = %s, want %s", got, want)
	}
}

// A scenario without given must hash as it did before the field existed, or every stored
// confirmation and script of such a scenario would lapse.
func TestExpectHashUnchangedWithoutGiven(t *testing.T) {
	const dir = "../../testdata/e2e/"
	sc, err := scenario.Load(dir + ".semar/scenarios/SHOP-482/SHOP-482-01.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "scripts/SHOP-482-01.yaml"); err != nil {
		t.Fatal(err)
	}
	sr, err := script.Load(dir + "scripts/SHOP-482-01.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Given != nil {
		t.Fatalf("the scenario has no given, got %v", sc.Given)
	}
	if got := domain.ExpectHash(sc); got != sr.ExpectHash {
		t.Fatalf("ExpectHash = %s, the stored script says %s", got, sr.ExpectHash)
	}
	sc.Given = []domain.Given{}
	if got := domain.ExpectHash(sc); got != sr.ExpectHash {
		t.Fatalf("an empty given list must hash as no given, got %s", got)
	}
}

func TestExpectHashChangesWithGiven(t *testing.T) {
	given := func() []domain.Given {
		return []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "c1"}, Lease: domain.LeaseExclusive}}
	}
	withGiven := base()
	withGiven.Given = given()
	h := domain.ExpectHash(withGiven)
	if h == domain.ExpectHash(base()) {
		t.Fatal("adding a given entry must change the hash")
	}

	again := base()
	again.Given = given()
	if domain.ExpectHash(again) != h {
		t.Fatal("the hash must be stable")
	}

	for name, change := range map[string]func(*domain.Scenario){
		"fixture":    func(s *domain.Scenario) { s.Given[0].Fixture = "order.refunded" },
		"as":         func(s *domain.Scenario) { s.Given[0].As = "o" },
		"with value": func(s *domain.Scenario) { s.Given[0].With["customer"] = "c2" },
		"with key":   func(s *domain.Scenario) { s.Given[0].With["other"] = "x" },
		"with gone":  func(s *domain.Scenario) { s.Given[0].With = nil },
		"lease":      func(s *domain.Scenario) { s.Given[0].Lease = "" },
		"second":     func(s *domain.Scenario) { s.Given = append(s.Given, domain.Given{Fixture: "c.v", As: "c"}) },
		"removed":    func(s *domain.Scenario) { s.Given = nil },
	} {
		s := base()
		s.Given = given()
		change(&s)
		if domain.ExpectHash(s) == h {
			t.Errorf("%s must change the hash", name)
		}
	}
}

func TestExpectHashGivenOrderMatters(t *testing.T) {
	x := domain.Given{Fixture: "order.paid", As: "order"}
	y := domain.Given{Fixture: "customer.verified", As: "customer"}
	a, b := base(), base()
	a.Given = []domain.Given{x, y}
	b.Given = []domain.Given{y, x}
	if domain.ExpectHash(a) == domain.ExpectHash(b) {
		t.Fatal("the order of given entries must change the hash")
	}
}

func TestExpectHashCoversAsAndByOnlyWhenSet(t *testing.T) {
	h := domain.ExpectHash(base())
	withAs := base()
	withAs.Steps[1].As = "carol"
	hAs := domain.ExpectHash(withAs)
	if hAs == h {
		t.Error("as must change the hash when set")
	}
	other := base()
	other.Steps[1].As = "bob"
	if domain.ExpectHash(other) == hAs {
		t.Error("a different as must change the hash")
	}
	withBy := base()
	withBy.Steps[1].Expect[0].By = "model"
	if domain.ExpectHash(withBy) == h {
		t.Error("by must change the hash when set")
	}
	empty := base()
	empty.Steps[1].As = ""
	empty.Steps[1].Expect[0].By = ""
	if domain.ExpectHash(empty) != h {
		t.Error("empty as and by must hash as before")
	}
}
