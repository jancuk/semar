package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

func healthRecord(h domain.Hash, healthy bool, at time.Time) HealthRecord {
	r := HealthRecord{
		Fixture: "order.paid", Version: "1.0.0", Hash: h, Environment: "uat", Run: "r-check",
		At: at, Builds: []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab"}},
		Find: "nothing", Create: "made", Cleanup: "done", Healthy: healthy,
	}
	if !healthy {
		r.Create, r.Reason = "failed", "fixture order.paid: create step 2 (POST /v1/payments) answered 500"
	}
	return r
}

func TestHealthLatestPerHash(t *testing.T) {
	root := t.TempDir()
	h := NewHealthStore(root)
	if _, found, err := h.Latest("uat", "order.paid"); err != nil || found {
		t.Fatalf("empty store: found %v, err %v", found, err)
	}
	at := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	if err := h.Put(healthRecord("aaaa", true, at)); err != nil {
		t.Fatal(err)
	}
	if err := h.Put(healthRecord("bbbb", false, at.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	got, found, err := h.Latest("uat", "order.paid")
	if err != nil || !found {
		t.Fatalf("found %v, err %v", found, err)
	}
	if got.Hash != "bbbb" || got.Healthy || got.Schema != 1 {
		t.Errorf("latest = %+v", got)
	}
	if _, found, _ := h.Latest("local", "order.paid"); found {
		t.Error("a record of uat counts for local")
	}

	// The resolver reads the record only for the hash it names.
	f := example(t)
	if blocked := unhealthy(got, f); blocked != "" {
		t.Errorf("a record of another hash blocks: %s", blocked)
	}
	rec := healthRecord(domain.FixtureHash(f), false, at)
	want := "fixture unhealthy: order.paid failed its last check on uat at 2026-11-17T08:00:00Z: fixture order.paid: create step 2 (POST /v1/payments) answered 500"
	if blocked := unhealthy(rec, f); blocked != want {
		t.Errorf("blocked = %q, want %q", blocked, want)
	}
	if blocked := unhealthy(healthRecord(domain.FixtureHash(f), true, at), f); blocked != "" {
		t.Errorf("a healthy record blocks: %s", blocked)
	}

	data, err := os.ReadFile(filepath.Join(root, "store", "fixture-health", "uat", "order.paid.json"))
	if err != nil || !strings.Contains(string(data), `"schema": 1`) {
		t.Fatalf("stored file: %s, %v", data, err)
	}
}

func TestHealthRefusesBadRecords(t *testing.T) {
	cases := []struct {
		name  string
		write func(t *testing.T, file string)
		want  string
	}{
		{"link", func(t *testing.T, file string) {
			other := filepath.Join(t.TempDir(), "x.json")
			if err := os.WriteFile(other, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, file); err != nil {
				t.Skip("no symbolic links:", err)
			}
		}, "symbolic link"},
		{"unknown field", func(t *testing.T, file string) {
			write(t, file, `{"schema":1,"fixture":"order.paid","environment":"uat","extra":1}`)
		}, "unknown field"},
		{"schema", func(t *testing.T, file string) {
			write(t, file, `{"schema":2,"fixture":"order.paid","environment":"uat"}`)
		}, "schema 2"},
		{"moved", func(t *testing.T, file string) {
			write(t, file, `{"schema":1,"fixture":"customer.verified","environment":"uat"}`)
		}, "is for fixture customer.verified on uat"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "store", "fixture-health", "uat")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			c.write(t, filepath.Join(dir, "order.paid.json"))
			_, _, err := NewHealthStore(root).Latest("uat", "order.paid")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestHealthRefusesBadNames(t *testing.T) {
	h := NewHealthStore(t.TempDir())
	if _, _, err := h.Latest("../uat", "order.paid"); err == nil {
		t.Error("an environment name with a path is accepted")
	}
	if err := h.Put(HealthRecord{Fixture: "../x", Environment: "uat"}); err == nil {
		t.Error("a fixture ID with a path is accepted")
	}
}

func write(t *testing.T, file, text string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
