package confirm_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
)

func TestNeeds(t *testing.T) {
	tests := []struct {
		name  string
		trust []domain.Source
		src   domain.Source
		want  bool
	}{
		{"diff", []domain.Source{"diff", "baseline"}, "diff", true},
		{"baseline", []domain.Source{"diff", "baseline"}, "baseline", true},
		{"ticket", []domain.Source{"diff", "baseline"}, "ticket", false},
		{"testcase", []domain.Source{"diff", "baseline"}, "testcase", false},
		{"person", []domain.Source{"diff", "baseline"}, "person", false},
		{"ticket when trusted", []domain.Source{"diff", "baseline", "ticket"}, "ticket", true},
		{"person when listed", []domain.Source{"person", "diff"}, "person", false},
		{"person with no list", nil, "person", false},
		{"diff with no list", nil, "diff", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := confirm.Needs(tt.src, tt.trust); got != tt.want {
				t.Errorf("Needs = %v, want %v", got, tt.want)
			}
		})
	}
}

var _ confirm.Store = (*confirm.Local)(nil)

func rec(user string, at time.Time) confirm.Record {
	return confirm.Record{Scenario: "refund", ExpectHash: "abc123", User: user, Identity: "local, unverified identity", At: at, Sources: []domain.Source{domain.SourceDiff}}
}

func TestPutAndGet(t *testing.T) {
	root := t.TempDir()
	l := confirm.NewLocal(root)
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	if _, ok, err := l.Get("refund", "abc123"); ok || err != nil {
		t.Fatalf("Get on empty = %v, %v", ok, err)
	}
	if err := l.Put(rec("bob", t0.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := l.Put(rec("alice", t0)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := l.Get("refund", "abc123")
	if err != nil || !ok {
		t.Fatalf("Get = %v, %v", ok, err)
	}
	if got.User != "alice" || got.Schema != 1 || !got.At.Equal(t0) || got.Identity != "local, unverified identity" {
		t.Errorf("record = %+v", got)
	}
	if _, ok, _ := l.Get("refund", "other"); ok {
		t.Error("another hash found a record")
	}
	path := filepath.Join(root, "store", "confirmations", "refund", "abc123", "bob.json")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"schema": 1`) {
		t.Errorf("file = %s, %v", data, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v", info, err)
	}
}

func TestPutKeepsTheFirstConfirmationOfAUser(t *testing.T) {
	l := confirm.NewLocal(t.TempDir())
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	if err := l.Put(rec("alice", t0)); err != nil {
		t.Fatal(err)
	}
	if err := l.Put(rec("alice", t0.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	got, _, err := l.Get("refund", "abc123")
	if err != nil || !got.At.Equal(t0) {
		t.Errorf("record = %+v, %v", got, err)
	}
}

func TestUserNameIsCleaned(t *testing.T) {
	tests := []struct {
		user string
		file string
	}{
		{"alice", "alice.json"},
		{"Alice.B-c_d", "Alice.B-c_d.json"},
		{"../../etc/passwd", ".._.._etc_passwd.json"},
		{"a/b", "a_b.json"},
		{"", "unknown.json"},
		{"..", "unknown.json"},
		{".", "unknown.json"},
		{"ali ce", "ali_ce.json"},
	}
	for _, tt := range tests {
		t.Run(tt.user, func(t *testing.T) {
			root := t.TempDir()
			l := confirm.NewLocal(root)
			if err := l.Put(rec(tt.user, time.Now())); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "store", "confirmations", "refund", "abc123")
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != tt.file {
				t.Errorf("entries = %v, %v, want %s", entries, err, tt.file)
			}
		})
	}
}

func TestUnsafeKeys(t *testing.T) {
	l := confirm.NewLocal(t.TempDir())
	for _, bad := range []string{"", "../x", "a/b", "/abs", ".", ".."} {
		r := rec("u", time.Now())
		r.Scenario = domain.ScenarioID(bad)
		if err := l.Put(r); err == nil {
			t.Errorf("Put accepted scenario %q", bad)
		}
		if _, _, err := l.Get(domain.ScenarioID(bad), "abc"); err == nil {
			t.Errorf("Get accepted scenario %q", bad)
		}
		r = rec("u", time.Now())
		r.ExpectHash = domain.Hash(bad)
		if err := l.Put(r); err == nil {
			t.Errorf("Put accepted hash %q", bad)
		}
		if _, _, err := l.Get("refund", domain.Hash(bad)); err == nil {
			t.Errorf("Get accepted hash %q", bad)
		}
	}
}

func TestGetRejectsACorruptRecord(t *testing.T) {
	root := t.TempDir()
	l := confirm.NewLocal(root)
	dir := filepath.Join(root, "store", "confirmations", "refund", "abc123")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.Get("refund", "abc123"); err == nil {
		t.Error("corrupt record accepted")
	}
}

func TestConcurrentPutsKeepOneRecord(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	for range 50 {
		l := confirm.NewLocal(t.TempDir())
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, at := range []time.Time{t0, t0.Add(time.Hour)} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- l.Put(rec("alice", at))
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		first, _, err := l.Get("refund", "abc123")
		if err != nil {
			t.Fatal(err)
		}
		// whichever put won, a later Put must not replace it
		if err := l.Put(rec("alice", t0.Add(2*time.Hour))); err != nil {
			t.Fatal(err)
		}
		again, _, err := l.Get("refund", "abc123")
		if err != nil || !again.At.Equal(first.At) {
			t.Fatalf("record changed from %v to %v (%v)", first.At, again.At, err)
		}
	}
}

func TestPutRefusesARecordWithNoSource(t *testing.T) {
	l := confirm.NewLocal(t.TempDir())
	r := rec("alice", time.Now())
	r.Sources = nil
	if err := l.Put(r); err == nil {
		t.Fatal("Put accepted a record with no source")
	}
	if _, ok, _ := l.Get("refund", "abc123"); ok {
		t.Error("a record without a source was stored")
	}
}

func TestPutSortsAndDeduplicatesSources(t *testing.T) {
	root := t.TempDir()
	l := confirm.NewLocal(root)
	r := rec("alice", time.Now())
	r.Sources = []domain.Source{domain.SourceDiff, domain.SourceBaseline, domain.SourceDiff}
	if err := l.Put(r); err != nil {
		t.Fatal(err)
	}
	got, _, err := l.Get("refund", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Source{domain.SourceBaseline, domain.SourceDiff}
	if !slices.Equal(got.Sources, want) {
		t.Errorf("Sources = %v, want %v", got.Sources, want)
	}
}

func TestGetUnitesTheSourcesOfEveryUser(t *testing.T) {
	l := confirm.NewLocal(t.TempDir())
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	a := rec("alice", t0)
	b := rec("bob", t0.Add(time.Hour))
	b.Sources = []domain.Source{domain.SourceBaseline}
	for _, r := range []confirm.Record{a, b} {
		if err := l.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := l.Get("refund", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if want := []domain.Source{domain.SourceBaseline, domain.SourceDiff}; !slices.Equal(got.Sources, want) || got.User != "alice" {
		t.Errorf("record = %+v, want the sources %v of alice and bob", got, want)
	}
}

func TestNoLinkIsFollowed(t *testing.T) {
	components := []string{"store", "store/confirmations", "store/confirmations/refund", "store/confirmations/refund/abc123"}
	const hashDir = "store/confirmations/refund/abc123"
	record := `{"schema": 1, "scenario": "refund", "expectHash": "abc123", "user": "mallory", "identity": "x", "at": "2026-01-01T00:00:00Z", "sources": ["diff"]}`
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
				if _, _, err := l.Get("refund", "abc123"); err == nil || !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), "symbolic link") {
					t.Errorf("Get: %v, want an error that names %s", err, link)
				}
				if err := l.Put(rec("alice", time.Now())); err == nil || !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), "symbolic link") {
					t.Errorf("Put: %v, want an error that names %s", err, link)
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
		dir := filepath.Join(root, filepath.FromSlash(hashDir))
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
		_, _, err := confirm.NewLocal(root).Get("refund", "abc123")
		if err == nil || !strings.Contains(err.Error(), "mallory.json is a symbolic link") {
			t.Errorf("Get: %v, want a refusal of the link", err)
		}
	})
}

func TestGetReadsARecordThatAnEarlierVersionWrote(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "store", "confirmations", "refund", "abc123")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	written := `{
  "schema": 1,
  "scenario": "refund",
  "expectHash": "abc123",
  "user": "alice",
  "identity": "local, unverified identity",
  "at": "2026-11-14T10:00:00Z",
  "sources": [
    "diff"
  ]
}
`
	if err := os.WriteFile(filepath.Join(dir, "alice.json"), []byte(written), 0o600); err != nil {
		t.Fatal(err)
	}
	r, ok, err := confirm.NewLocal(root).Get("refund", "abc123")
	if err != nil || !ok || r.User != "alice" || !slices.Equal(r.Sources, []domain.Source{domain.SourceDiff}) {
		t.Fatalf("Get = %+v, %v, %v", r, ok, err)
	}
}

func TestNeedsExpectation(t *testing.T) {
	trust := []domain.Source{domain.SourceDiff}
	tests := []struct {
		name string
		e    domain.Expectation
		want bool
	}{
		{"a trusted source", domain.Expectation{Source: domain.SourceDiff}, true},
		{"an untrusted source", domain.Expectation{Source: domain.SourceTicket}, false},
		{"a model's ticket expectation", domain.Expectation{Source: domain.SourceTicket, By: "model"}, true},
		{"a model's testcase expectation", domain.Expectation{Source: domain.SourceTestcase, By: "model"}, true},
		{"a person's own expectation", domain.Expectation{Source: domain.SourcePerson, By: "model"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := confirm.NeedsExpectation(tt.e, trust); got != tt.want {
				t.Errorf("NeedsExpectation = %v, want %v", got, tt.want)
			}
			if got := confirm.NeedsExpectation(tt.e, nil); got != (tt.e.By == "model" && tt.e.Source != domain.SourcePerson) {
				t.Errorf("with no trust, NeedsExpectation = %v", got)
			}
		})
	}
}
