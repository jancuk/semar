package replay_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

// withCite gives the scenario another cite on the first expected result of the refund step.
func withCite(sc domain.Scenario, cite string) domain.Scenario {
	sc.Steps = slices.Clone(sc.Steps)
	for i, st := range sc.Steps {
		if st.ID != "refund-too-much" {
			continue
		}
		sc.Steps[i].Expect = slices.Clone(st.Expect)
		sc.Steps[i].Expect[0].Cite = cite
	}
	return sc
}

func shapedScript(t *testing.T, sc domain.Scenario) domain.Script {
	t.Helper()
	s := loadScript(t)
	s.ExpectHash = domain.ExpectHash(sc)
	s.ShapeHash = domain.ShapeHash(sc)
	return s
}

func TestAnEditedExpectedValueRunsWithTheScriptOfTheSameShape(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	s := shapedScript(t, sc)
	r.putScript(t, s)
	edited := withCite(sc, "SHOP-482: reworded")
	r.confirmAll(t, edited)
	res, _, _ := r.run(t, edited)
	expect(t, res[0], domain.OutcomePassed, "")
	if res[0].ExpectHash != domain.ExpectHash(edited) || res[0].ScriptHash != s.ExpectHash {
		t.Errorf("ExpectHash %s ScriptHash %s, want %s and %s", res[0].ExpectHash, res[0].ScriptHash, domain.ExpectHash(edited), s.ExpectHash)
	}
}

func TestAChangedRequestHintIsBlockedAndSendsNothing(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	r.putScript(t, shapedScript(t, sc))
	edited := withCite(sc, "SHOP-482: reworded")
	edited.Steps[0].Hint = &domain.RequestHint{Method: "POST", Path: "/v1/other"}
	r.confirmAll(t, edited)
	res, _, _ := r.run(t, edited)
	expect(t, res[0], domain.OutcomeBlocked, fmt.Sprintf("no script fits the steps of %s: the steps, the account, or the given changed since its script was stored", edited.ID))
	if n := r.app.Requests("", "/v1/"); n != 0 {
		t.Fatalf("%d requests reached the service", n)
	}
}

func TestAScriptWithoutAShapeHashIsUsedOnlyOnAnExactHash(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	s := loadScript(t)
	s.ShapeHash = ""
	r.putScript(t, s)
	r.confirmAll(t, sc)
	res, _, _ := r.run(t, sc)
	expect(t, res[0], domain.OutcomePassed, "")

	edited := withCite(sc, "SHOP-482: reworded")
	r.confirmAll(t, edited)
	before := r.app.Requests("", "/v1/")
	res, _, _ = r.run(t, edited)
	expect(t, res[0], domain.OutcomeBlocked, fmt.Sprintf("no script fits the steps of %s", edited.ID))
	if n := r.app.Requests("", "/v1/"); n != before {
		t.Fatalf("%d requests reached the service for the edited scenario", n-before)
	}
}

func TestTheNewestScriptOfAShapeIsUsed(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	old, newer := withCite(sc, "SHOP-482: first"), withCite(sc, "SHOP-482: second")
	now := time.Now().UTC().Truncate(time.Second)
	so, sn := shapedScript(t, old), shapedScript(t, newer)
	so.StoredAt, sn.StoredAt = now.Add(-time.Hour), now
	// the newer script has the smaller hash, so only the time can pick it
	if so.ExpectHash < sn.ExpectHash {
		so.StoredAt, sn.StoredAt = sn.StoredAt, so.StoredAt
		so, sn = sn, so
	}
	r.putScript(t, so)
	r.putScript(t, sn)
	third := withCite(sc, "SHOP-482: third")
	r.confirmAll(t, third)
	res, _, _ := r.run(t, third)
	expect(t, res[0], domain.OutcomePassed, "")
	if res[0].ScriptHash != sn.ExpectHash {
		t.Errorf("ScriptHash = %s, want the newer script %s", res[0].ScriptHash, sn.ExpectHash)
	}
}

func TestAScenarioWithNoScriptKeepsItsReason(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc := loadScenario(t)
	r.confirmAll(t, sc)
	res, _, _ := r.run(t, sc)
	expect(t, res[0], domain.OutcomeBlocked, "no script was accepted")
}
