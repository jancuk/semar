package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/secret"
)

func TestMCPRunIsMadeOnFirstUseAndClosedOnce(t *testing.T) {
	dir := t.TempDir()
	env := config.Environment{Kind: "test", Hosts: []string{"127.0.0.1:9"}, Methods: []string{"GET"}, Effects: config.Effects{Mode: "test_data"}}
	pol, err := effects.New(env)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	w := &mcpWiring{
		Dir: dir, RepoMode: true, EnvName: "local", Env: env, Policy: pol,
		Opts:    options{RunID: func() domain.RunID { return "r-mcp-1" }, Clock: func() time.Time { return now }},
		Secrets: secret.NewEnv(func(string) (string, bool) { return "", false }), Scrubber: mcpkit.NewScrubber(nil),
	}
	if _, err := os.Stat(filepath.Join(dir, ".semar", "runs")); err == nil {
		t.Fatal("a run folder exists before the first action")
	}
	id, rec, err := w.EnsureRun()
	if err != nil || rec == nil || id != "r-mcp-1" {
		t.Fatalf("EnsureRun = %q, %v, %v", id, rec, err)
	}
	id2, rec2, err := w.EnsureRun()
	if err != nil || id2 != id || rec2 != rec {
		t.Fatalf("a second EnsureRun made another run: %q %v", id2, err)
	}
	if err := w.closeRun(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".semar", "runs", "r-mcp-1", "run.json")); err != nil {
		t.Errorf("the run has no run.json: %v", err)
	}
	if err := w.closeRun(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
}

func TestMCPRunNeedsARepositoryAndAnEnvironment(t *testing.T) {
	for name, w := range map[string]*mcpWiring{
		"standalone":     {Dir: t.TempDir()},
		"no environment": {Dir: t.TempDir(), RepoMode: true},
	} {
		if _, _, err := w.EnsureRun(); err == nil {
			t.Errorf("%s: EnsureRun made a run", name)
		}
	}
}

func TestMCPLoopbackAddr(t *testing.T) {
	for in, want := range map[string]string{
		":8765": "127.0.0.1:8765", "127.0.0.1:0": "127.0.0.1:0", "localhost:80": "127.0.0.1:80", "[::1]:9000": "[::1]:9000",
	} {
		if got, err := loopbackAddr("--http", in); err != nil || got != want {
			t.Errorf("loopbackAddr(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"0.0.0.0:80", "[::]:80", "10.0.0.1:80", "example.com:80", "8765", ":99999", ":x", ""} {
		if got, err := loopbackAddr("--http", in); err == nil {
			t.Errorf("loopbackAddr(%q) = %q, want an error", in, got)
		}
	}
}
