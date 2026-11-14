package evidence_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/recorder"
)

var _ recorder.Sink = (*evidence.Run)(nil)

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func action(seq int, scenario, step string) domain.Action {
	return domain.Action{
		Schema: 1, ID: domain.ActionID("a-000" + string(rune('0'+seq))), Seq: seq, Run: "r1",
		Surface: domain.SurfaceAPI, Op: "request",
		Use: domain.UseCase{Pass: domain.PassScenario, Scenario: domain.ScenarioID(scenario), Step: domain.StepID(step)},
		By:  domain.ActorAgent, At: t0,
		Response: &domain.HTTPResponse{Status: 200, Body: domain.Body(`{"ok":true}`)},
	}
}

func activity(seq int) domain.Activity {
	return domain.Activity{Seq: seq, At: t0, Actor: domain.ActorAgent, Verb: domain.VerbRead, Target: "GET /x", Result: "200"}
}

func openStore(t *testing.T) *evidence.Store {
	t.Helper()
	s, err := evidence.Open(filepath.Join(t.TempDir(), ".semar"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func closedRun(t *testing.T, s *evidence.Store, id domain.RunID) {
	t.Helper()
	r, err := s.NewRun(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteAction(action(1, "refund", "too-much")); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendActivity(activity(1)); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(domain.Run{ID: id, Environment: "staging", StartedAt: t0, EndedAt: t0}); err != nil {
		t.Fatal(err)
	}
}

func TestRunRoundTrip(t *testing.T) {
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i := 1; i <= 3; i++ {
		p, err := r.WriteAction(action(i, "refund", "step-a"))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	if paths[2] != "exchanges/refund/0003-step-a.json" {
		t.Errorf("path = %q", paths[2])
	}
	for _, i := range []int{3, 1, 2} {
		if err := r.AppendActivity(activity(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.WriteFile("report.md", []byte("# report\n")); err != nil {
		t.Fatal(err)
	}
	want := domain.Run{ID: "r1", Environment: "staging", StartedAt: t0, EndedAt: t0.Add(time.Minute),
		BuildsStart: []domain.BuildRef{{Service: "orders", Commit: "abc"}}}
	if err := r.Close(want); err != nil {
		t.Fatal(err)
	}

	got, acts, err := s.LoadRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Environment != "staging" || !got.EndedAt.Equal(want.EndedAt) || got.Schema != 1 ||
		len(got.BuildsStart) != 1 || got.BuildsStart[0].Commit != "abc" {
		t.Errorf("run = %+v", got)
	}
	if len(acts) != 3 || acts[0].Seq != 1 || acts[1].Seq != 2 || acts[2].Seq != 3 {
		t.Errorf("activity = %+v", acts)
	}
	wantFiles := []string{
		"activity.jsonl", "report.md",
		"exchanges/refund/0001-step-a.json", "exchanges/refund/0002-step-a.json", "exchanges/refund/0003-step-a.json",
	}
	if len(got.Files) != len(wantFiles) {
		t.Errorf("files = %v", got.Files)
	}
	for _, f := range wantFiles {
		if len(got.Files[f]) != 64 {
			t.Errorf("hash of %s = %q", f, got.Files[f])
		}
	}
	if _, ok := got.Files["run.json"]; ok {
		t.Error("run.json lists itself")
	}

	root := filepath.Join(s.Root(), "runs", "r1")
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %o, want %o", p, info.Mode().Perm(), want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if problems, err := s.Verify("r1"); err != nil || len(problems) != 0 {
		t.Errorf("verify = %v, %v", problems, err)
	}
}

func TestStoredJSONIsReadable(t *testing.T) {
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	a := action(1, "refund", "too-much")
	a.Response.Body = domain.Body(`<a href="/x?a=1&b=2">`)
	p, err := r.WriteAction(a)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.Root(), "runs", "r1", filepath.FromSlash(p)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`<a href=\"/x?a=1&b=2\">`)) || bytes.Contains(data, []byte(`\u00`)) {
		t.Errorf("file escaped its body:\n%s", data)
	}
	if !bytes.Contains(data, []byte("\n  \"schema\": 1")) {
		t.Errorf("file is not indented with two spaces:\n%s", data)
	}

	b := action(2, "refund", "x")
	b.Why = "a<b&c>d"
	if _, err := r.WriteAction(b); err != nil {
		t.Fatal(err)
	}
	e := activity(1)
	e.Target = "GET /x?a=1&b=<2>"
	if err := r.AppendActivity(e); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(domain.Run{ID: "r1"}); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(s.Root(), "runs", "r1", "activity.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log, []byte("GET /x?a=1&b=<2>")) || bytes.Count(log, []byte("\n")) != 1 {
		t.Errorf("activity.jsonl = %s", log)
	}
}

func TestInterruptedRun(t *testing.T) {
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteAction(action(1, "refund", "a")); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.LoadRun("r1")
	if !errors.Is(err, evidence.ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", err)
	}
	_, _, err = s.LoadRun("nope")
	if err == nil || errors.Is(err, evidence.ErrInterrupted) || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing run err = %v", err)
	}
}

func TestActivityOfAnInterruptedRun(t *testing.T) {
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{2, 1} {
		if err := r.AppendActivity(activity(i)); err != nil {
			t.Fatal(err)
		}
	}
	acts, err := s.Activity("r1")
	if err != nil || len(acts) != 2 || acts[0].Seq != 1 || acts[1].Seq != 2 {
		t.Fatalf("activity = %+v, %v", acts, err)
	}

	if _, err := s.NewRun("r2"); err != nil {
		t.Fatal(err)
	}
	if acts, err := s.Activity("r2"); err != nil || len(acts) != 0 {
		t.Errorf("a run with no action: activity = %+v, %v", acts, err)
	}
	if _, err := s.Activity("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing run err = %v, want fs.ErrNotExist", err)
	}
	if _, err := s.Activity("../r1"); err == nil {
		t.Error("an ID that leaves the store is accepted")
	}
}

func TestVerify(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, dir string)
		want   []string
	}{
		{"intact", func(*testing.T, string) {}, nil},
		{"changed", func(t *testing.T, dir string) {
			f, err := os.OpenFile(filepath.Join(dir, "exchanges", "refund", "0001-too-much.json"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString(" "); err != nil {
				t.Fatal(err)
			}
		}, []string{"exchanges/refund/0001-too-much.json: changed"}},
		{"missing", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "activity.jsonl")); err != nil {
				t.Fatal(err)
			}
		}, []string{"activity.jsonl: missing"}},
		{"unlisted", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, []string{"extra.txt: not listed in run.json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openStore(t)
			closedRun(t, s, "r1")
			tt.mutate(t, filepath.Join(s.Root(), "runs", "r1"))
			got, err := s.Verify("r1")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("verify = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVerifyInterruptedRunIsAnError(t *testing.T) {
	s := openStore(t)
	if _, err := s.NewRun("r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify("r1"); !errors.Is(err, evidence.ErrInterrupted) {
		t.Errorf("err = %v", err)
	}
}

func TestNoHalfWrittenFiles(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop this user")
	}
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile("sub/a.txt", []byte("a")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Root(), "runs", "r1", "sub")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := r.WriteFile("sub/b.txt", []byte("b")); err == nil {
		t.Fatal("write into a read-only directory succeeded")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.txt" {
		t.Errorf("directory holds %v", entries)
	}
}

func TestWritesAfterClose(t *testing.T) {
	s := openStore(t)
	r, err := s.NewRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(domain.Run{ID: "r1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteAction(action(1, "a", "b")); err == nil {
		t.Error("WriteAction after Close succeeded")
	}
	if err := r.AppendActivity(activity(1)); err == nil {
		t.Error("AppendActivity after Close succeeded")
	}
	if err := r.WriteFile("x.txt", nil); err == nil {
		t.Error("WriteFile after Close succeeded")
	}
	if err := r.Close(domain.Run{ID: "r1"}); err == nil {
		t.Error("second Close succeeded")
	}
}

func TestNewRunRefusesExistingDirectory(t *testing.T) {
	s := openStore(t)
	if _, err := s.NewRun("r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NewRun("r1"); err == nil {
		t.Error("second NewRun with the same ID succeeded")
	}
}

func TestRunsNewestFirst(t *testing.T) {
	s := openStore(t)
	closedRun(t, s, "run-2026-11-14-a")
	closedRun(t, s, "run-2026-11-16-a")
	if _, err := s.NewRun("run-2026-11-15-a"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root(), "runs", "stray.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.Root(), "runs", ".hidden"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := s.Runs()
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.RunID{"run-2026-11-16-a", "run-2026-11-15-a", "run-2026-11-14-a"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("runs = %v, want %v", got, want)
	}
}

func TestRunsOnAnEmptyStore(t *testing.T) {
	got, err := openStore(t).Runs()
	if err != nil || len(got) != 0 {
		t.Errorf("runs = %v, %v", got, err)
	}
}

func TestUnsafeIDs(t *testing.T) {
	bad := map[string]string{
		"parent":   "../x",
		"absolute": "/etc/passwd",
		"empty":    "",
		"slash":    "a/b",
		"dot":      ".",
		"dotdot":   "..",
		"leading":  ".hidden",
		"space":    "a b",
	}
	for name, id := range bad {
		t.Run(name, func(t *testing.T) {
			s := openStore(t)
			parent := filepath.Dir(s.Root())
			if _, err := s.NewRun(domain.RunID(id)); err == nil {
				t.Error("NewRun accepted it")
			}
			if _, _, err := s.LoadRun(domain.RunID(id)); err == nil {
				t.Error("LoadRun accepted it")
			}
			if _, err := s.Verify(domain.RunID(id)); err == nil {
				t.Error("Verify accepted it")
			}
			if err := s.PutScript(domain.Script{Scenario: domain.ScenarioID(id), ExpectHash: "h"}); err == nil {
				t.Error("PutScript accepted the scenario ID")
			}
			if err := s.PutScript(domain.Script{Scenario: "s", ExpectHash: domain.Hash(id)}); err == nil {
				t.Error("PutScript accepted the hash")
			}
			if _, _, err := s.Script(domain.ScenarioID(id), "h"); err == nil {
				t.Error("Script accepted the scenario ID")
			}
			if _, _, err := s.Script("s", domain.Hash(id)); err == nil {
				t.Error("Script accepted the hash")
			}
			r, err := s.NewRun("ok")
			if err != nil {
				t.Fatal(err)
			}
			if id != "" { // an empty scenario or step is a valid action
				if _, err := r.WriteAction(action(1, id, "step")); err == nil {
					t.Error("WriteAction accepted the scenario ID")
				}
				if _, err := r.WriteAction(action(1, "scenario", id)); err == nil {
					t.Error("WriteAction accepted the step ID")
				}
			}
			if err := r.WriteFile(id, []byte("x")); err == nil && name != "slash" {
				t.Error("WriteFile accepted it")
			}
			entries, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Errorf("files appeared outside the store: %v", entries)
			}
		})
	}
}

func TestWriteFileRefusesReservedAndEscapingPaths(t *testing.T) {
	for _, rel := range []string{"run.json", "../x", "a/../../x", "a//b", `a\b`, "/abs"} {
		t.Run(rel, func(t *testing.T) {
			s := openStore(t)
			r, err := s.NewRun("r1")
			if err != nil {
				t.Fatal(err)
			}
			if err := r.WriteFile(rel, []byte("x")); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func scriptFixture(id, hash string) domain.Script {
	return domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: domain.ScenarioID(id), ExpectHash: domain.Hash(hash),
		AuthoredBy: domain.ProviderRef{Instance: "i", Model: "m", Harness: "h"},
		Steps: []domain.ScriptStep{{
			Step: "too-much",
			Request: &domain.APIRequest{
				Service: "orders", Method: "POST", Path: "/v1/orders/{{id}}/refunds",
				JSON: map[string]any{"amount": 5, "note": "a<b&c"},
			},
			Captures: []domain.Capture{{Name: "id", JSONPath: "$.id"}},
		}},
	}
}

func TestScriptStoreIsKeyedByHash(t *testing.T) {
	s := openStore(t)
	sc := scriptFixture("refund", "aaa")
	if err := s.PutScript(sc); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Script("refund", "aaa")
	if err != nil || !ok {
		t.Fatalf("Script = %v, %v", ok, err)
	}
	if got.Scenario != "refund" || got.ExpectHash != "aaa" || len(got.Steps) != 1 ||
		got.Steps[0].Request.Path != "/v1/orders/{{id}}/refunds" || got.Steps[0].Captures[0].JSONPath != "$.id" {
		t.Errorf("script = %+v", got)
	}
	if _, ok, err := s.Script("refund", "bbb"); ok || err != nil {
		t.Errorf("Script(bbb) = %v, %v", ok, err)
	}
	data, err := os.ReadFile(filepath.Join(s.Root(), "store", "scripts", "refund", "aaa.yaml"))
	if err != nil || !bytes.Contains(data, []byte("apiVersion: semar/v1")) {
		t.Errorf("stored file = %s, %v", data, err)
	}
}

func TestPutScriptRefusesAnUnkeyedScript(t *testing.T) {
	s := openStore(t)
	if err := s.PutScript(scriptFixture("", "aaa")); err == nil {
		t.Error("empty scenario accepted")
	}
	if err := s.PutScript(scriptFixture("refund", "")); err == nil {
		t.Error("empty hash accepted")
	}
}

func TestScriptChangedByHandIsAnError(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"unknown field", "apiVersion: semar/v1\nkind: Script\nscenario: refund\nexpectHash: aaa\nbogus: 1\nsteps: []\n"},
		{"not yaml", "a: [\n"},
		{"other scenario", "apiVersion: semar/v1\nkind: Script\nscenario: other\nexpectHash: aaa\nsteps: []\n"},
		{"other hash", "apiVersion: semar/v1\nkind: Script\nscenario: refund\nexpectHash: zzz\nsteps: []\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openStore(t)
			if err := s.PutScript(scriptFixture("refund", "aaa")); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.Root(), "store", "scripts", "refund", "aaa.yaml")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := s.Script("refund", "aaa"); err == nil || ok {
				t.Errorf("Script = %v, %v, want an error", ok, err)
			}
		})
	}
}

func TestRedactKey(t *testing.T) {
	s := openStore(t)
	first, err := s.RedactKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 || bytes.Equal(first, make([]byte, 32)) {
		t.Fatalf("key = %x", first)
	}
	second, err := s.RedactKey()
	if err != nil || !bytes.Equal(first, second) {
		t.Errorf("second call = %x, %v", second, err)
	}
	reopened, err := evidence.Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	third, err := reopened.RedactKey()
	if err != nil || !bytes.Equal(first, third) {
		t.Errorf("reopened = %x, %v", third, err)
	}
	path := filepath.Join(s.Root(), "store", "redact.key")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("key file = %v, %v", info, err)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Errorf("key directory = %v, %v", dir, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("store directory holds %v", entries)
	}
}

func TestRedactKeyWrongLength(t *testing.T) {
	s := openStore(t)
	path := filepath.Join(s.Root(), "store", "redact.key")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("short-secret-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.RedactKey()
	if err == nil || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "short-secret-value") {
		t.Errorf("err = %v", err)
	}
}
