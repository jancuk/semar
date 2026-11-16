package journey

import (
	"reflect"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/script"
)

func TestLegScenarioFields(t *testing.T) {
	j := example(t)
	sc, sr := LegScenario(j, "pay", "payments-user")
	if sc.APIVersion != "semar/v1" || sc.Kind != "Scenario" || sc.ID != "checkout.pay" || sc.Ticket != "checkout" {
		t.Errorf("scenario header = %+v", sc)
	}
	if sc.Title != "An order is placed, paid, and shown as paid: pay" {
		t.Errorf("title = %q", sc.Title)
	}
	if want := "Leg pay of journey checkout: An order is placed, paid, and shown as paid"; sc.Intent != want {
		t.Errorf("intent = %q", sc.Intent)
	}
	if sc.Surface != domain.SurfaceAPI || sc.AuthProfile != "payments-user" || len(sc.Given) != 0 {
		t.Errorf("surface, profile, given = %q, %q, %v", sc.Surface, sc.AuthProfile, sc.Given)
	}
	if len(sc.Steps) != 1 || sc.Steps[0].ID != "pay" || sc.Steps[0].Hint.Path != "/v1/payments" || len(sc.Steps[0].Expect) != 1 {
		t.Errorf("steps = %+v", sc.Steps)
	}

	if sr.APIVersion != "semar/v1" || sr.Kind != "Script" || sr.Scenario != sc.ID {
		t.Errorf("script header = %+v", sr)
	}
	if want := (domain.ProviderRef{Instance: "journey", Harness: "file"}); sr.AuthoredBy != want {
		t.Errorf("authoredBy = %+v", sr.AuthoredBy)
	}
	if sr.ExpectHash != domain.ExpectHash(sc) || len(sr.ExpectHash) != 64 {
		t.Errorf("expectHash = %q", sr.ExpectHash)
	}
	if len(sr.Steps) != 1 || sr.Steps[0].Step != "pay" || sr.Steps[0].Request.Service != "payments" ||
		len(sr.Steps[0].Captures) != 1 || sr.Steps[0].Captures[0].Name != "amount" {
		t.Errorf("script steps = %+v", sr.Steps)
	}
}

func TestLegScenarioSetsTheLegService(t *testing.T) {
	j := example(t)
	j.Legs[1].Steps[0].Request.Service = "" // the file may leave it out (J8)
	_, sr := LegScenario(j, "pay", "")
	if got := sr.Steps[0].Request.Service; got != "payments" {
		t.Errorf("service = %q", got)
	}
	if j.Legs[1].Steps[0].Request.Service != "" {
		t.Error("LegScenario changed the journey")
	}
}

func TestLegScenarioBindsToItsScript(t *testing.T) {
	// leg place has no variable of another leg, so the script rules alone can judge it
	sc, sr := LegScenario(example(t), "place", "")
	if ps := script.Validate(sc, sr); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
}

// TestLegScenarioHashStable: the hash that a confirmation stores does not change between two loads
// of the same file, and it changes with what a person confirmed.
func TestLegScenarioHashStable(t *testing.T) {
	hashes := func(j domain.Journey) map[domain.LegID]domain.Hash {
		out := map[domain.LegID]domain.Hash{}
		for _, l := range j.Legs {
			_, sr := LegScenario(j, l.ID, "")
			out[l.ID] = sr.ExpectHash
		}
		return out
	}
	a, b := hashes(example(t)), hashes(example(t))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("hashes differ between two loads: %v and %v", a, b)
	}
	if a["place"] == a["pay"] || a["pay"] == a["settled"] {
		t.Errorf("two legs have the same hash: %v", a)
	}

	changed := example(t)
	changed.Legs[1].Steps[0].Expect[0].Check = domain.Check{Status: new(201)}
	if hashes(changed)["pay"] == a["pay"] {
		t.Error("the hash of leg pay does not follow its expected result")
	}
	if hashes(changed)["place"] != a["place"] {
		t.Error("the hash of leg place follows an edit of leg pay")
	}

	reworded := example(t)
	reworded.Legs[1].Steps[0].Action = "Settle the order"
	if hashes(reworded)["pay"] != a["pay"] {
		t.Error("rewording an action changes a hash")
	}
	// The title is part of the intent that a person confirmed, as the plan says.
	reworded.Title = "A new title"
	if hashes(reworded)["pay"] == a["pay"] {
		t.Error("the hash does not follow the title, which is in the intent")
	}

	_, withProfile := LegScenario(example(t), "pay", "payments-user")
	if withProfile.ExpectHash == a["pay"] {
		t.Error("the hash does not follow the auth profile")
	}
}

func TestLegScenarioOfAnUnknownLeg(t *testing.T) {
	sc, sr := LegScenario(example(t), "nope", "")
	if sc.ID != "" || sr.Scenario != "" || len(sc.Steps) != 0 {
		t.Fatalf("scenario = %+v, script = %+v", sc, sr)
	}
}

func TestLegScenarioDoesNotShareMaps(t *testing.T) {
	j := example(t)
	j.Legs[1].Steps[0].Request.Query = map[string]string{"a": "b"}
	_, sr := LegScenario(j, "pay", "")
	sr.Steps[0].Request.Query["a"] = "changed"
	if j.Legs[1].Steps[0].Request.Query["a"] != "b" {
		t.Error("a change to the script changed the journey")
	}
}

// The hashes of the example legs before steps could act as another account. A journey that does
// not use `as` keeps them, so no confirmation is lost.
func TestLegScenarioHashIsUnchangedWithoutAs(t *testing.T) {
	want := map[domain.LegID]domain.Hash{
		"place":   "a620a057f45f5305f5d8e048ede11526f141702d98aeadcb99858ee46f5f830c",
		"pay":     "08f39e3b64ed6c15d058c91331b22c04bd12aa866ccfbacf04950b5e3885610b",
		"settled": "c69a46f8aa6b5e76d936265d5b864ebd5664c4d3d80b5bf32564228645255507",
	}
	j := example(t)
	for id, h := range want {
		if _, sr := LegScenario(j, id, "payments-user"); sr.ExpectHash != h {
			t.Errorf("hash of leg %s = %s, want %s", id, sr.ExpectHash, h)
		}
	}
}

func TestLegAsAppliesToEveryStepAndAStepOverridesIt(t *testing.T) {
	j := example(t)
	pay := &j.Legs[1]
	pay.As = "bob"
	pay.Steps = append(pay.Steps, domain.LegStep{ID: "again", Hint: pay.Steps[0].Hint, Request: pay.Steps[0].Request, As: "carol"},
		domain.LegStep{ID: "third", Hint: pay.Steps[0].Hint, Request: pay.Steps[0].Request})
	sc, _ := LegScenario(j, "pay", "alice")
	got := []string{sc.Steps[0].As, sc.Steps[1].As, sc.Steps[2].As}
	if !reflect.DeepEqual(got, []string{"bob", "carol", "bob"}) {
		t.Errorf("as of the steps = %v", got)
	}
	if other, _ := LegScenario(j, "place", "alice"); other.Steps[0].As != "" {
		t.Errorf("a step of another leg acts as %q", other.Steps[0].As)
	}
}

func TestLegAsChangesTheHashOnlyOfItsLeg(t *testing.T) {
	j := example(t)
	_, before := LegScenario(j, "pay", "alice")
	_, otherBefore := LegScenario(j, "place", "alice")
	j.Legs[1].As = "bob"
	_, after := LegScenario(j, "pay", "alice")
	_, otherAfter := LegScenario(j, "place", "alice")
	if before.ExpectHash == after.ExpectHash {
		t.Error("the hash of the leg does not cover as")
	}
	if otherBefore.ExpectHash != otherAfter.ExpectHash {
		t.Error("the hash of another leg follows as")
	}
}
