//go:build unix

package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/claude"
)

const schema = `{"type":"object","properties":{"method":{"type":"string"}}}`

type fakeBinary struct {
	path, args, cwd, env, stdin string
}

// newBinary writes a shell script that records how it was started and then prints a fixture.
func newBinary(t *testing.T, fixture string, exit int, stderr string) fakeBinary {
	t.Helper()
	dir := t.TempDir()
	b := fakeBinary{
		path:  filepath.Join(dir, "claude-fake"),
		args:  filepath.Join(dir, "args"),
		cwd:   filepath.Join(dir, "cwd"),
		env:   filepath.Join(dir, "env"),
		stdin: filepath.Join(dir, "stdin"),
	}
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\0' \"$@\" > %q\npwd -P > %q\nenv > %q\ncat > %q\n", b.args, b.cwd, b.env, b.stdin)
	if fixture != "" {
		abs, err := filepath.Abs(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		body += fmt.Sprintf("cat %q\n", abs)
	}
	if stderr != "" {
		body += fmt.Sprintf("printf '%%s\\n' %q >&2\n", stderr)
	}
	body += fmt.Sprintf("exit %d\n", exit)
	if err := os.WriteFile(b.path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return b
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func newDriver(t *testing.T, cfg config.Provider) *claude.Driver {
	t.Helper()
	d, err := claude.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func job(t *testing.T) provider.Job {
	return provider.Job{Prompt: "plan this change", Workdir: t.TempDir(), Schema: json.RawMessage(schema), MaxTurns: 7}
}

func classOf(t *testing.T, err error) provider.FailureClass {
	t.Helper()
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("want a *provider.Error, got %T %v", err, err)
	}
	return pe.Class
}

func TestRunSuccess(t *testing.T) {
	b := newBinary(t, "success.json", 0, "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-do-not-pass")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("SEMAR_RANDOM", "x")
	d := newDriver(t, config.Provider{ID: "c", Kind: "claude", BinaryPath: b.path})
	j := job(t)
	res, err := d.Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"method":"GET","path":"/v1/ping"}` || res.Model != "claude-sonnet-5-5" || res.Turns != 4 ||
		res.InputTokens != 6+6017+11639 || res.OutputTokens != 275 {
		t.Fatalf("got %+v", res)
	}
	if d.ID() != "c" || d.Kind() != "claude" {
		t.Fatal("identity")
	}

	wantArgs := []string{
		"-p", "--output-format", "json", "--json-schema", schema, "--tools", "Read,Grep,Glob",
		"--strict-mcp-config", "--setting-sources", "", "--no-session-persistence", "--max-turns", "7",
	}
	got := strings.Split(strings.TrimSuffix(read(t, b.args), "\x00"), "\x00")
	if !slices.Equal(got, wantArgs) {
		t.Fatalf("args = %q\nwant   %q", got, wantArgs)
	}
	if read(t, b.stdin) != "plan this change" {
		t.Fatalf("stdin = %q", read(t, b.stdin))
	}
	wantCwd, _ := filepath.EvalSymlinks(j.Workdir)
	if strings.TrimSpace(read(t, b.cwd)) != wantCwd {
		t.Fatalf("cwd = %q, want %q", read(t, b.cwd), wantCwd)
	}
	for _, line := range strings.Split(strings.TrimSpace(read(t, b.env)), "\n") {
		name, _, _ := strings.Cut(line, "=")
		switch name {
		case "PATH", "HOME", "USER", "LOGNAME", "LANG", "TERM", "TMPDIR", "PWD", "SHLVL", "_", "OLDPWD":
		default:
			t.Errorf("the child inherited %s", name)
		}
	}
}

func TestRunModelFlag(t *testing.T) {
	cases := []struct {
		name, cfgModel, jobModel string
		want                     []string
	}{
		{"none", "", "", nil},
		{"configured", "cfg-model", "", []string{"--model", "cfg-model"}},
		{"job wins", "cfg-model", "job-model", []string{"--model", "job-model"}},
		{"job only", "", "job-model", []string{"--model", "job-model"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, "success.json", 0, "")
			d := newDriver(t, config.Provider{BinaryPath: b.path, Model: c.cfgModel})
			j := job(t)
			j.Model = c.jobModel
			if _, err := d.Run(context.Background(), j); err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSuffix(read(t, b.args), "\x00"), "\x00")
			if slices.Contains(args, "--bare") {
				t.Fatal("--bare must never be passed")
			}
			tail := args[len(args)-len(c.want):]
			if len(c.want) == 0 {
				if slices.Contains(args, "--model") {
					t.Fatalf("args = %q", args)
				}
				return
			}
			if !slices.Equal(tail, c.want) {
				t.Fatalf("args = %q", args)
			}
		})
	}
}

func TestRunModelTie(t *testing.T) {
	b := newBinary(t, "tie.json", 0, "")
	res, err := newDriver(t, config.Provider{BinaryPath: b.path}).Run(context.Background(), job(t))
	if err != nil || res.Model != "aa-model" {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestRunFailureClasses(t *testing.T) {
	cases := []struct {
		name, fixture string
		exit          int
		stderr        string
		class         provider.FailureClass
		detail        string
	}{
		{"turn limit", "error_max_turns.json", 1, "", provider.ValidationError, "the harness reached the turn limit"},
		{"usage limit by status", "error_usage_limit.json", 1, "", provider.UsageLimit, "usage or rate limit"},
		{"rate limit by subtype", "error_rate_limit_subtype.json", 1, "", provider.UsageLimit, "rate limit reached"},
		{"unauthorized", "error_unauthorized.json", 1, "", provider.PermissionError, "401"},
		{"other error", "error_other.json", 1, "", provider.ProviderError, "error_during_execution"},
		{"no object", "no_object.json", 0, "", provider.ValidationError, "the output holds no object"},
		{"output is not an object", "output_not_object.json", 0, "", provider.ValidationError, "the output holds no object"},
		{"crash with no JSON", "", 3, "boom: out of memory", provider.ProviderError, "exit code 3: boom: out of memory"},
		{"not JSON on success", "", 0, "", provider.ProviderError, "not JSON"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, c.fixture, c.exit, c.stderr)
			_, err := newDriver(t, config.Provider{BinaryPath: b.path}).Run(context.Background(), job(t))
			if got := classOf(t, err); got != c.class {
				t.Fatalf("class = %s, err = %v", got, err)
			}
			if !strings.Contains(err.Error(), c.detail) {
				t.Fatalf("error %q lacks %q", err, c.detail)
			}
			if strings.Contains(err.Error(), "plan this change") {
				t.Fatal("the error holds the prompt")
			}
		})
	}
}

func TestRunStderrIsRedactedAndCut(t *testing.T) {
	stderr := "token=abcdef0123 Bearer sk-ant-abcdefghijklmnopqrstuvwxyz0123456789 " + strings.Repeat("x ", 400)
	b := newBinary(t, "", 1, stderr)
	_, err := newDriver(t, config.Provider{BinaryPath: b.path}).Run(context.Background(), job(t))
	text := err.Error()
	if strings.Contains(text, "abcdef0123") || strings.Contains(text, "sk-ant") {
		t.Fatalf("a secret leaked: %s", text)
	}
	if len(text) > 500 {
		t.Fatalf("detail has %d bytes", len(text))
	}
}

func TestRunNotInstalled(t *testing.T) {
	t.Run("configured path", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope")
		_, err := newDriver(t, config.Provider{BinaryPath: missing}).Run(context.Background(), job(t))
		if classOf(t, err) != provider.NotInstalled || !strings.Contains(err.Error(), missing) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("on PATH at run time", func(t *testing.T) {
		d := newDriver(t, config.Provider{})
		t.Setenv("PATH", t.TempDir())
		_, err := d.Run(context.Background(), job(t))
		if classOf(t, err) != provider.NotInstalled || !strings.Contains(err.Error(), `"claude"`) || !strings.Contains(err.Error(), "PATH") {
			t.Fatalf("got %v", err)
		}
		if h := d.Health(context.Background()); h.Installed || h.Detail == "" {
			t.Fatalf("health = %+v", h)
		}
	})
}

func TestRunTimeoutAndCancel(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hang")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > /dev/null\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := newDriver(t, config.Provider{BinaryPath: script})
	t.Run("job timeout", func(t *testing.T) {
		j := job(t)
		j.Timeout = 300 * time.Millisecond
		start := time.Now()
		_, err := d.Run(context.Background(), j)
		if classOf(t, err) != provider.TransportError || !strings.Contains(err.Error(), "did not finish in 300ms") {
			t.Fatalf("got %v", err)
		}
		if time.Since(start) > 15*time.Second {
			t.Fatal("the child was not stopped")
		}
	})
	t.Run("context deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		_, err := d.Run(ctx, job(t))
		if classOf(t, err) != provider.TransportError {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("parent cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(300*time.Millisecond, cancel)
		_, err := d.Run(ctx, job(t))
		var pe *provider.Error
		if !errors.Is(err, context.Canceled) || errors.As(err, &pe) {
			t.Fatalf("got %T %v", err, err)
		}
	})
}

func TestRunRefusesBadJobs(t *testing.T) {
	b := newBinary(t, "success.json", 0, "")
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*provider.Job)
	}{
		{"no turns", func(j *provider.Job) { j.MaxTurns = 0 }},
		{"no workdir", func(j *provider.Job) { j.Workdir = "" }},
		{"workdir is a file", func(j *provider.Job) { j.Workdir = file }},
		{"workdir missing", func(j *provider.Job) { j.Workdir = filepath.Join(j.Workdir, "x") }},
		{"no prompt", func(j *provider.Job) { j.Prompt = "  " }},
		{"schema not an object", func(j *provider.Job) { j.Schema = json.RawMessage(`[1]`) }},
		{"schema empty", func(j *provider.Job) { j.Schema = nil }},
		{"schema broken", func(j *provider.Job) { j.Schema = json.RawMessage(`{`) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := job(t)
			c.mutate(&j)
			_, err := newDriver(t, config.Provider{BinaryPath: b.path}).Run(context.Background(), j)
			if classOf(t, err) != provider.ValidationError {
				t.Fatalf("got %v", err)
			}
			if _, statErr := os.Stat(b.args); statErr == nil {
				t.Fatal("the child started")
			}
		})
	}
}

func TestHealth(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	script := filepath.Join(dir, "claude-v")
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\necho '2.1.325 (Claude Code)'\necho second line\n", record)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	h := newDriver(t, config.Provider{BinaryPath: script}).Health(context.Background())
	if !h.Installed || h.Version != "2.1.325 (Claude Code)" || h.Detail != "" {
		t.Fatalf("health = %+v", h)
	}
	if read(t, record) != "--version\n" {
		t.Fatalf("args = %q", read(t, record))
	}
	t.Run("a failing binary", func(t *testing.T) {
		b := newBinary(t, "", 2, "broken install")
		h := newDriver(t, config.Provider{BinaryPath: b.path}).Health(context.Background())
		if h.Installed || !strings.Contains(h.Detail, "broken install") {
			t.Fatalf("health = %+v", h)
		}
	})
}

func TestFactory(t *testing.T) {
	in, err := claude.Factory()(config.Provider{ID: "x", Kind: "claude"})
	if err != nil || in.Kind() != "claude" || in.ID() != "x" {
		t.Fatalf("got %v %v", in, err)
	}
}
