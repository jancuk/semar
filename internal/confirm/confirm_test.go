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
	return confirm.Record{Scenario: "refund", ExpectHash: "abc123", User: user, Identity: "local, unverified identity", At: at}
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
