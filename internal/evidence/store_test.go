package evidence_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScriptsOfListsEveryScriptOfTheScenario(t *testing.T) {
	s := openStore(t)
	for _, sc := range []struct{ id, hash string }{{"SHOP-482-01", "aaa"}, {"SHOP-482-01", "bbb"}, {"SHOP-482-02", "ccc"}} {
		if err := s.PutScript(scriptFixture(sc.id, sc.hash)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.Root(), "store", "scripts", "SHOP-482-01", "zzz.yaml"), []byte("a: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.ScriptsOf("SHOP-482-01")
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string]bool{}
	for _, g := range got {
		if g.Scenario != "SHOP-482-01" {
			t.Errorf("script of %s listed", g.Scenario)
		}
		hashes[string(g.ExpectHash)] = true
	}
	if len(got) != 2 || !hashes["aaa"] || !hashes["bbb"] {
		t.Errorf("ScriptsOf = %d scripts %v, want aaa and bbb", len(got), hashes)
	}
	none, err := s.ScriptsOf("SHOP-482-09")
	if none != nil || err != nil {
		t.Errorf("ScriptsOf(SHOP-482-09) = %v, %v; want nil, nil", none, err)
	}
}

func TestPutScriptStampsTheTimeItWasStored(t *testing.T) {
	s := openStore(t)
	before := time.Now().UTC().Add(-time.Minute)
	if err := s.PutScript(scriptFixture("refund", "aaa")); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Script("refund", "aaa")
	if err != nil {
		t.Fatal(err)
	}
	if got.StoredAt.IsZero() || got.StoredAt.Before(before) || got.StoredAt.Location() != time.UTC {
		t.Errorf("StoredAt = %v, want a recent UTC time", got.StoredAt)
	}

	set := scriptFixture("refund", "bbb")
	set.StoredAt = time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC)
	if err := s.PutScript(set); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.Script("refund", "bbb")
	if err != nil {
		t.Fatal(err)
	}
	if !got.StoredAt.Equal(set.StoredAt) {
		t.Errorf("StoredAt = %v, want %v", got.StoredAt, set.StoredAt)
	}
}
