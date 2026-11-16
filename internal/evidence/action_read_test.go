package evidence_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/evidence"
)

func TestReadActionRoundTrip(t *testing.T) {
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	want := action(3, "refund", "too-much")
	want.DurationMS = 412
	rel, err := r.WriteAction(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadAction("r1", rel)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Seq != 3 || got.DurationMS != 412 || got.Use.Scenario != "refund" || got.Response == nil || got.Response.Status != 200 {
		t.Errorf("ReadAction = %+v, want the action that was written", got)
	}
}

func TestReadActionRefusesPath(t *testing.T) {
	s := openStore(t)
	closedRun(t, s, "r1")
	outside := filepath.Join(s.Root(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"schema":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, rel string }{
		{"parent", "../run.json"},
		{"deep parent", "exchanges/../../r2/run.json"},
		{"absolute", outside},
		{"empty", ""},
		{"dot", "."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.ReadAction("r1", tt.rel); err == nil {
				t.Fatalf("ReadAction(%q) = nil error, want a refusal", tt.rel)
			}
		})
	}
	if _, err := s.ReadAction("../r1", "exchanges/refund/0001-too-much.json"); err == nil {
		t.Error("ReadAction accepted a run ID that leaves the runs directory")
	}
}

func TestReadActionRefusesLink(t *testing.T) {
	s := openStore(t)
	closedRun(t, s, "r1")
	dir := filepath.Join(s.Root(), "runs", "r1", "exchanges", "refund")
	target := filepath.Join(s.Root(), "runs", "r1", "exchanges", "refund", "0001-too-much.json")

	if err := os.Symlink(target, filepath.Join(dir, "link.json")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if _, err := s.ReadAction("r1", "exchanges/refund/link.json"); err == nil || !strings.Contains(err.Error(), "link") {
		t.Errorf("a link to a file of the run: error = %v, want one that names a link", err)
	}

	outside := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(outside, []byte(`{"schema":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "out.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadAction("r1", "exchanges/refund/out.json"); err == nil {
		t.Error("a link to a file outside the run was read")
	}

	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "0001.json"), []byte(`{"schema":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(s.Root(), "runs", "r1", "exchanges", "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadAction("r1", "exchanges/linked/0001.json"); err == nil {
		t.Error("a file below a linked directory was read")
	}
}

func TestReadActionRefusesNotRegular(t *testing.T) {
	s := openStore(t)
	closedRun(t, s, "r1")
	if err := os.Mkdir(filepath.Join(s.Root(), "runs", "r1", "exchanges", "dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadAction("r1", "exchanges/dir.json"); err == nil {
		t.Error("a directory was read as an action")
	}
}

func TestReadActionRefusesLarge(t *testing.T) {
	s := openStore(t)
	closedRun(t, s, "r1")
	p := filepath.Join(s.Root(), "runs", "r1", "exchanges", "refund", "big.json")
	pad := strings.Repeat(" ", evidence.MaxActionBytes)
	if err := os.WriteFile(p, []byte(`{"schema":1}`+pad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.ReadAction("r1", "exchanges/refund/big.json")
	if !errors.Is(err, evidence.ErrActionTooLarge) {
		t.Fatalf("error = %v, want ErrActionTooLarge", err)
	}

	ok := filepath.Join(s.Root(), "runs", "r1", "exchanges", "refund", "edge.json")
	body := `{"schema":1}`
	if err := os.WriteFile(ok, []byte(body+strings.Repeat(" ", evidence.MaxActionBytes-len(body))), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadAction("r1", "exchanges/refund/edge.json"); err != nil {
		t.Errorf("a file of exactly %d bytes was refused: %v", evidence.MaxActionBytes, err)
	}
}

func TestReadActionRefusesUnknownField(t *testing.T) {
	s := openStore(t)
	closedRun(t, s, "r1")
	p := filepath.Join(s.Root(), "runs", "r1", "exchanges", "refund", "odd.json")
	if err := os.WriteFile(p, []byte(`{"schema":1,"surprise":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.ReadAction("r1", "exchanges/refund/odd.json")
	if err == nil || !strings.Contains(err.Error(), "surprise") {
		t.Errorf("error = %v, want one that names the unknown field", err)
	}
}
