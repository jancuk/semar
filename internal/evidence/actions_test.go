package evidence_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
)

func TestWriteActionPaths(t *testing.T) {
	tests := []struct {
		name           string
		scenario, step string
		want           string
	}{
		{"scenario and step", "refund", "too-much", "exchanges/refund/0007-too-much.json"},
		{"scenario only", "refund", "", "exchanges/refund/0007.json"},
		{"neither", "", "", "exchanges/_run/0007.json"},
		{"step only", "", "login", "exchanges/_run/0007-login.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openStore(t)
			r, err := s.NewRun("r1")
			if err != nil {
				t.Fatal(err)
			}
			got, err := r.WriteAction(action(7, tt.scenario, tt.step))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("path = %q, want %q", got, tt.want)
			}
			if _, err := os.Stat(filepath.Join(s.Root(), "runs", "r1", filepath.FromSlash(got))); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestRunDirectoryNameCannotBeAScenarioID(t *testing.T) {
	if err := idcheck.Check("scenario ID", "_run"); err == nil {
		t.Fatal("_run is a valid ID, so it could collide with the directory for actions with no scenario")
	}
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteAction(action(1, "_run", "x")); err == nil {
		t.Error("WriteAction accepted the scenario ID _run")
	}
}

func TestRecorderToRunWithAndWithoutUseCase(t *testing.T) {
	s := openStore(t)
	run, err := s.NewRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	red, err := redact.New(config.Redact{}, func() []string { return nil }, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	classify := func(string, *url.URL) effects.Class { return effects.Read }
	rec := recorder.New(run, red, classify, "run-1")

	req := &domain.HTTPRequest{Method: "GET", URL: "https://t.example/v1/orders", PathTemplate: "/v1/orders"}
	full := domain.Action{Surface: domain.SurfaceAPI, Op: "request", Request: req, Response: &domain.HTTPResponse{Status: 200},
		Use: domain.UseCase{Pass: domain.PassScenario, Scenario: "refund", Step: "too-much"}}
	read := domain.Action{Surface: domain.SurfaceAPI, Op: "request", Request: req, Response: &domain.HTTPResponse{Status: 200},
		Use: domain.UseCase{Pass: domain.PassExplore}}
	refused := domain.Action{Surface: domain.SurfaceAPI, Op: "request", Request: req, Refused: "writes are off",
		Use: domain.UseCase{Pass: domain.PassExplore}}
	for _, a := range []domain.Action{full, read, refused} {
		if _, err := rec.Record(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := run.Close(domain.Run{ID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	got, acts, err := s.LoadRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exchanges/refund/0001-too-much.json", "exchanges/_run/0002.json", "exchanges/_run/0003.json"}
	if len(acts) != 3 {
		t.Fatalf("activity = %+v", acts)
	}
	for i, w := range want {
		if _, err := os.Stat(filepath.Join(s.Root(), "runs", "run-1", filepath.FromSlash(w))); err != nil {
			t.Error(err)
		}
		if len(got.Files[w]) != 64 {
			t.Errorf("run.json does not list %s: %v", w, got.Files)
		}
		if acts[i].Evidence != w {
			t.Errorf("activity %d evidence = %q, want %q", i+1, acts[i].Evidence, w)
		}
	}
}

func TestVerifyReportsATamperedActivityFile(t *testing.T) {
	s := openStore(t)
	closedRun(t, s, "r1")
	f, err := os.OpenFile(filepath.Join(s.Root(), "runs", "r1", "activity.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("this is not json\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Verify("r1")
	if err != nil {
		t.Fatalf("Verify = %v, want a listed change", err)
	}
	if strings.Join(got, "|") != "activity.jsonl: changed" {
		t.Errorf("verify = %v", got)
	}
}

func TestFailedCloseCanBeRetried(t *testing.T) {
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteAction(action(1, "refund", "a")); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendActivity(activity(1)); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Root(), "runs", "r1")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "activity.jsonl"), link); err != nil {
		t.Skip("symlinks are not available:", err)
	}
	if err := r.Close(domain.Run{ID: "r1"}); err == nil {
		t.Fatal("Close succeeded with a symlink in the run directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "run.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("run.json exists after a failed Close: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(domain.Run{ID: "r1"}); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if problems, err := s.Verify("r1"); err != nil || len(problems) != 0 {
		t.Errorf("verify = %v, %v", problems, err)
	}
	if _, _, err := s.LoadRun("r1"); err != nil {
		t.Error(err)
	}
	if err := r.Close(domain.Run{ID: "r1"}); err == nil {
		t.Error("third Close succeeded")
	}
}

func TestConcurrentWrites(t *testing.T) {
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := action(1, "refund", "s")
			a.Seq = i
			a.ID = domain.ActionID(fmt.Sprintf("a-%04d", i))
			if _, err := r.WriteAction(a); err != nil {
				errs <- err
			}
			if err := r.AppendActivity(activity(i)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for i := 1; i <= n; i++ {
		p := filepath.Join(s.Root(), "runs", "r1", "exchanges", "refund", fmt.Sprintf("%04d-s.json", i))
		if _, err := os.Stat(p); err != nil {
			t.Error(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(s.Root(), "runs", "r1", "activity.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("activity has %d lines, want %d", len(lines), n)
	}
	for _, l := range lines {
		var e domain.Activity
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Errorf("line %q: %v", l, err)
		}
	}
}

func TestLoadRunErrors(t *testing.T) {
	s := openStore(t)
	_, _, err := s.LoadRun("nope")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing run err = %v, want fs.ErrNotExist", err)
	}
	if _, err := os.Stat(s.Root()); err != nil {
		t.Fatal(err)
	}
	// a run directory that is a file is not "not found"
	runs := filepath.Join(s.Root(), "runs")
	if err := os.MkdirAll(runs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LoadRun("file"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
}

func TestOpenSetsModeOnExistingDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".semar")
	for _, d := range []string{"", "runs", "store"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := evidence.Open(root); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"", "runs", "store"} {
		info, err := os.Stat(filepath.Join(root, d))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%q mode = %o", d, info.Mode().Perm())
		}
	}
}
