package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestJSONKeysAreLowerCamel(t *testing.T) {
	use := domain.UseCase{Pass: "p", Scenario: "s", Step: "t"}
	a, err := json.Marshal(domain.Action{Use: use})
	if err != nil {
		t.Fatal(err)
	}
	o, err := json.Marshal(domain.Activity{Use: use, Object: &domain.ObjectRef{Type: "order", ID: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(a) + string(o)
	for _, k := range []string{`"pass"`, `"scenario"`, `"step"`, `"type"`, `"id"`} {
		if !strings.Contains(got, k) {
			t.Errorf("missing key %s in %s", k, got)
		}
	}
	for _, k := range []string{`"Pass"`, `"Scenario"`, `"Step"`, `"Type"`, `"ID"`} {
		if strings.Contains(got, k) {
			t.Errorf("unexpected key %s in %s", k, got)
		}
	}
}

func TestUseCaseJSONOmitsEmptyFixture(t *testing.T) {
	a := domain.Action{Schema: 1, ID: "a-1", Run: "r-1", Use: domain.UseCase{Pass: domain.PassScenario, Scenario: "S-1", Step: "one"}}
	got, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"schema":1,"id":"a-1","seq":0,"run":"r-1","surface":"","op":"","use":{"pass":"scenario","scenario":"S-1","step":"one"},"by":"","traceId":"","at":"0001-01-01T00:00:00Z","durationMs":0}`
	if string(got) != want {
		t.Fatalf("an action without a fixture changed its encoding:\n got %s\nwant %s", got, want)
	}

	a.Use = domain.UseCase{Pass: domain.PassSetup, Scenario: "S-1", Fixture: &domain.FixtureUse{ID: "order.paid", As: "order", Phase: domain.PhaseCreate, Index: 2}, Attempt: 3}
	got, err = json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	const wantFixture = `"use":{"pass":"setup","scenario":"S-1","step":"","fixture":{"id":"order.paid","as":"order","phase":"create","index":2},"attempt":3}`
	if !strings.Contains(string(got), wantFixture) {
		t.Fatalf("got %s, want it to contain %s", got, wantFixture)
	}
}

func TestRunPurposeOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(domain.Run{Schema: 1, ID: "r-1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "purpose") {
		t.Fatalf("an empty purpose must not be encoded: %s", b)
	}
	b, err = json.Marshal(domain.Run{Schema: 1, ID: "r-1", Purpose: domain.PurposeFixtureCheck})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"purpose":"fixture-check"`) {
		t.Fatalf("got %s", b)
	}
	if domain.PurposeTrial != "trial" || domain.PassTrial != "trial" {
		t.Fatal("trial constants")
	}
}

func TestScenarioResultFixturesOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(domain.ScenarioResult{Scenario: "S-1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "fixtures") {
		t.Fatalf("no fixtures must not be encoded: %s", b)
	}
}

func TestScenarioResultOmitsAnEmptyExpectHash(t *testing.T) {
	got, err := json.Marshal(domain.ScenarioResult{Scenario: "S-1", Outcome: domain.OutcomePassed})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "expectHash") {
		t.Errorf("an old result gained a key: %s", got)
	}
	got, err = json.Marshal(domain.ScenarioResult{Scenario: "S-1", Outcome: domain.OutcomePassed, ExpectHash: "abc"})
	if err != nil || !strings.Contains(string(got), `"expectHash":"abc"`) {
		t.Errorf("%s, %v", got, err)
	}
}
