// Package gittest builds git repositories for tests, with fixed identities and dates.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Repo is a work tree in a temporary directory.
type Repo struct {
	t     testing.TB
	Dir   string
	clock time.Time
}

// New creates an empty repository whose first branch is main.
func New(t testing.TB) *Repo {
	t.Helper()
	r := &Repo{t: t, Dir: t.TempDir(), clock: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)}
	r.Git("init", "--quiet", "--initial-branch=main")
	return r
}

// Run runs git in dir and returns its trimmed output.
func Run(t testing.TB, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main", "-c", "protocol.file.allow=always"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(cleanEnv(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Ada Author", "GIT_AUTHOR_EMAIL=ada@example.com",
		"GIT_COMMITTER_NAME=Ada Author", "GIT_COMMITTER_EMAIL=ada@example.com",
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// Git runs git in the work tree.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	return Run(r.t, r.Dir, r.dates(), args...)
}

func (r *Repo) dates() []string {
	d := r.clock.Format(time.RFC3339)
	return []string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}
}

// Write writes a file under the work tree.
func (r *Repo) Write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// Commit writes the files (a nil value removes one), commits everything, and returns the commit ID.
func (r *Repo) Commit(msg string, files map[string]*string) string {
	r.t.Helper()
	for p, c := range files {
		if c == nil {
			r.Git("rm", "--quiet", "--force", "--", p)
			continue
		}
		r.Write(p, *c)
	}
	r.Git("add", "--all")
	r.clock = r.clock.Add(time.Minute)
	r.Git("commit", "--quiet", "--allow-empty", "-m", msg)
	return r.Git("rev-parse", "HEAD")
}

// S returns a pointer to s, for the files map of Commit.
func S(s string) *string { return &s }

// Tick advances the clock by a minute.
func (r *Repo) Tick() { r.clock = r.clock.Add(time.Minute) }

// MergeNoFF merges a branch with a merge commit and the given message.
func (r *Repo) MergeNoFF(branch, msg string) string {
	r.t.Helper()
	r.Tick()
	r.Git("merge", "--quiet", "--no-ff", "-m", msg, branch)
	return r.Git("rev-parse", "HEAD")
}

// Bare creates a bare repository, pushes every branch of r to it, and returns its path.
func (r *Repo) Bare() string {
	r.t.Helper()
	dir := filepath.Join(r.t.TempDir(), "remote.git")
	Run(r.t, filepath.Dir(dir), nil, "init", "--quiet", "--bare", dir)
	r.Git("push", "--quiet", dir, "--all")
	return dir
}
