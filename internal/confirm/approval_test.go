package confirm_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
)

const fixtureHash = "9f2e0c4a7b1d3e5f60718293a4b5c6d7e8f9a0b1c2d3e4f5061728394a5b6c7d"

func approval(user string, at time.Time) confirm.FixtureApproval {
	return confirm.FixtureApproval{Fixture: "order.paid", Version: "1.0.0", Hash: fixtureHash, User: user, Identity: "local, unverified identity", At: at}
}

func approvalDir(root string) string {
	return filepath.Join(root, "store", "approvals", "fixtures", "order.paid", fixtureHash)
}

func TestFixtureApprovalRoundTrip(t *testing.T) {
	root := t.TempDir()
	l := confirm.NewLocal(root)
	if ok, err := l.FixtureApproved("order.paid", fixtureHash); ok || err != nil {
		t.Fatalf("FixtureApproved on an empty store = %v, %v", ok, err)
	}
	t0 := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	if err := l.PutFixture(approval("alice", t0)); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.FixtureApproved("order.paid", fixtureHash); !ok || err != nil {
		t.Fatalf("FixtureApproved = %v, %v", ok, err)
	}
	path := filepath.Join(approvalDir(root), "alice.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"schema": 1`, `"fixture": "order.paid"`, `"version": "1.0.0"`, `"user": "alice"`, `"identity": "local, unverified identity"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("file lacks %s:\n%s", want, data)
		}
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v", info, err)
	}
	// A confirmation of a scenario of the same name is a different record kind.
	if _, err := os.Stat(filepath.Join(root, "store", "confirmations")); !os.IsNotExist(err) {
		t.Errorf("an approval must not write under confirmations: %v", err)
	}
}

func TestFixtureApprovalKeepsTheFirstRecordOfAUser(t *testing.T) {
	root := t.TempDir()
	l := confirm.NewLocal(root)
	t0 := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	first := approval("alice", t0)
	later := approval("alice", t0.Add(time.Hour))
	later.Version = "9.9.9"
	for _, r := range []confirm.FixtureApproval{first, later} {
		if err := l.PutFixture(r); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(approvalDir(root), "alice.json"))
	if err != nil || strings.Contains(string(data), "9.9.9") || !strings.Contains(string(data), "2026-11-17T08:00:00Z") {
		t.Fatalf("the first record must stay: %s, %v", data, err)
	}
	if err := l.PutFixture(approval("bob", t0)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(approvalDir(root))
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %v, %v", entries, err)
	}
}

func TestFixtureApprovalIsPerHash(t *testing.T) {
	l := confirm.NewLocal(t.TempDir())
	old := approval("alice", time.Now())
	old.Hash = "aaaa"
	if err := l.PutFixture(old); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.FixtureApproved("order.paid", fixtureHash); ok || err != nil {
		t.Fatalf("a record for an old hash approved a new one: %v, %v", ok, err)
	}
	if ok, err := l.FixtureApproved("order.paid", "aaaa"); !ok || err != nil {
		t.Fatalf("the old hash is approved: %v, %v", ok, err)
	}
	if ok, err := l.FixtureApproved("order.refunded", "aaaa"); ok || err != nil {
		t.Fatalf("another fixture with the same hash: %v, %v", ok, err)
	}
}

func TestFixtureApprovalCountsEveryUser(t *testing.T) {
	l := confirm.NewLocal(t.TempDir())
	for _, u := range []string{"alice", "bob"} {
		if err := l.PutFixture(approval(u, time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := l.FixtureApproved("order.paid", fixtureHash); !ok || err != nil {
		t.Fatalf("got %v, %v", ok, err)
	}
}

func TestFixtureApprovalUnsafeKeys(t *testing.T) {
	l := confirm.NewLocal(t.TempDir())
	for _, bad := range []string{"", "../x", "a/b", "/abs", ".", "..", "_x", "a b"} {
		r := approval("u", time.Now())
		r.Fixture = domain.FixtureID(bad)
		if err := l.PutFixture(r); err == nil {
			t.Errorf("PutFixture accepted fixture %q", bad)
		}
		if _, err := l.FixtureApproved(domain.FixtureID(bad), "abc"); err == nil {
			t.Errorf("FixtureApproved accepted fixture %q", bad)
		}
		r = approval("u", time.Now())
		r.Hash = domain.Hash(bad)
		if err := l.PutFixture(r); err == nil {
			t.Errorf("PutFixture accepted hash %q", bad)
		}
		if _, err := l.FixtureApproved("order.paid", domain.Hash(bad)); err == nil {
			t.Errorf("FixtureApproved accepted hash %q", bad)
		}
	}
	// An empty hash is what FixtureHash gives on an encoding failure: it must match nothing.
	if ok, err := l.FixtureApproved("order.paid", ""); ok || err == nil {
		t.Errorf("the empty hash: %v, %v", ok, err)
	}
}

func TestFixtureApprovalUserNameIsCleaned(t *testing.T) {
	root := t.TempDir()
	l := confirm.NewLocal(root)
	if err := l.PutFixture(approval("../../etc/passwd", time.Now())); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(approvalDir(root))
	if err != nil || len(entries) != 1 || entries[0].Name() != ".._.._etc_passwd.json" {
		t.Fatalf("entries = %v, %v", entries, err)
	}
}

func TestFixtureApprovalRefusesLinkAndSwap(t *testing.T) {
	components := []string{"store", "store/approvals", "store/approvals/fixtures", "store/approvals/fixtures/order.paid", "store/approvals/fixtures/order.paid/" + fixtureHash}
	hashDir := components[len(components)-1]
	record := `{"schema": 1, "fixture": "order.paid", "version": "1.0.0", "hash": "` + fixtureHash + `", "user": "mallory", "identity": "x", "at": "2026-01-01T00:00:00Z"}`
	for _, c := range components {
		for _, inside := range []bool{true, false} {
			name := c + " to a directory outside the store"
			if inside {
				name = c + " to a directory inside the store"
			}
			t.Run(name, func(t *testing.T) {
				root := filepath.Join(t.TempDir(), ".semar")
				target := filepath.Join(t.TempDir(), "elsewhere")
				if inside {
					target = filepath.Join(root, "elsewhere")
				}
				below, _ := filepath.Rel(c, hashDir)
				if err := os.MkdirAll(filepath.Join(target, below), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, below, "mallory.json"), []byte(record), 0o600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(root, filepath.FromSlash(c))
				if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Skip("symlinks are not available:", err)
				}
				l := confirm.NewLocal(root)
				ok, err := l.FixtureApproved("order.paid", fixtureHash)
				if ok || err == nil || !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), "symbolic link") {
					t.Errorf("FixtureApproved: %v, %v, want an error that names %s", ok, err, link)
				}
				if err := l.PutFixture(approval("alice", time.Now())); err == nil || !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), "symbolic link") {
					t.Errorf("PutFixture: %v, want an error that names %s", err, link)
				}
				entries, err := os.ReadDir(filepath.Join(target, below))
				if err != nil || len(entries) != 1 {
					t.Errorf("the link's target holds %v (%v)", entries, err)
				}
			})
		}
	}

	t.Run("a record that is a link", func(t *testing.T) {
		root := t.TempDir()
		dir := approvalDir(root)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(root, "other.json")
		if err := os.WriteFile(other, []byte(record), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(other, filepath.Join(dir, "mallory.json")); err != nil {
			t.Skip("symlinks are not available:", err)
		}
		ok, err := confirm.NewLocal(root).FixtureApproved("order.paid", fixtureHash)
		if ok || err == nil || !strings.Contains(err.Error(), "mallory.json is a symbolic link") {
			t.Errorf("FixtureApproved: %v, %v, want a refusal of the link", ok, err)
		}
	})

	t.Run("a record swapped for a link after it was stored", func(t *testing.T) {
		root := t.TempDir()
		l := confirm.NewLocal(root)
		if err := l.PutFixture(approval("alice", time.Now())); err != nil {
			t.Fatal(err)
		}
		if ok, err := l.FixtureApproved("order.paid", fixtureHash); !ok || err != nil {
			t.Fatalf("before the swap: %v, %v", ok, err)
		}
		path := filepath.Join(approvalDir(root), "alice.json")
		other := filepath.Join(root, "other.json")
		if err := os.Rename(path, other); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(other, path); err != nil {
			t.Skip("symlinks are not available:", err)
		}
		if ok, err := l.FixtureApproved("order.paid", fixtureHash); ok || err == nil {
			t.Errorf("after the swap: %v, %v, want a refusal", ok, err)
		}
	})
}

func TestFixtureApprovalRefusesWrongDirectory(t *testing.T) {
	root := t.TempDir()
	l := confirm.NewLocal(root)
	if err := l.PutFixture(approval("alice", time.Now())); err != nil {
		t.Fatal(err)
	}
	// An approval of order.paid moved under the directory of another fixture.
	other := filepath.Join(root, "store", "approvals", "fixtures", "order.refunded", fixtureHash)
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(approvalDir(root), "alice.json"), filepath.Join(other, "alice.json")); err != nil {
		t.Fatal(err)
	}
	ok, err := l.FixtureApproved("order.refunded", fixtureHash)
	if ok || err == nil || !strings.Contains(err.Error(), `is for fixture "order.paid"`) {
		t.Fatalf("FixtureApproved = %v, %v, want an error that names the fixture", ok, err)
	}

	// The same record moved under another hash.
	root = t.TempDir()
	l = confirm.NewLocal(root)
	if err := l.PutFixture(approval("alice", time.Now())); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "store", "approvals", "fixtures", "order.paid", "bbbb")
	if err := os.MkdirAll(moved, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(approvalDir(root), "alice.json"), filepath.Join(moved, "alice.json")); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.FixtureApproved("order.paid", "bbbb"); ok || err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("FixtureApproved = %v, %v", ok, err)
	}
}

func TestFixtureApprovalRefusesABadRecord(t *testing.T) {
	good := `"fixture": "order.paid", "version": "1.0.0", "hash": "` + fixtureHash + `", "user": "u", "identity": "x", "at": "2026-01-01T00:00:00Z"`
	tests := []struct {
		name, body, want string
	}{
		{"not JSON", `{`, "parse fixture approval"},
		{"an unknown field", `{"schema": 1, "extra": 1, ` + good + `}`, "unknown field"},
		{"schema 2", `{"schema": 2, ` + good + `}`, "has schema 2, want 1"},
		{"no schema", `{` + good + `}`, "has schema 0, want 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(approvalDir(root), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(approvalDir(root), "u.json"), []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			ok, err := confirm.NewLocal(root).FixtureApproved("order.paid", fixtureHash)
			if ok || err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, %v, want an error with %q", ok, err, tt.want)
			}
		})
	}
	t.Run("a directory named like a record is not a record", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(approvalDir(root), "u.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		if ok, err := confirm.NewLocal(root).FixtureApproved("order.paid", fixtureHash); ok || err != nil {
			t.Fatalf("got %v, %v", ok, err)
		}
	})
	t.Run("other files are ignored", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(approvalDir(root), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(approvalDir(root), "notes.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if ok, err := confirm.NewLocal(root).FixtureApproved("order.paid", fixtureHash); ok || err != nil {
			t.Fatalf("got %v, %v", ok, err)
		}
	})
}

func TestConcurrentFixtureApprovalsKeepOneRecord(t *testing.T) {
	root := t.TempDir()
	l := confirm.NewLocal(root)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- l.PutFixture(approval("alice", time.Date(2026, 11, 17, 8, i, 0, 0, time.UTC)))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	entries, err := os.ReadDir(approvalDir(root))
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v, %v", entries, err)
	}
	if ok, err := l.FixtureApproved("order.paid", fixtureHash); !ok || err != nil {
		t.Fatalf("got %v, %v", ok, err)
	}
}
