//go:build unix

package claude_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

var private = []string{"person@example.com", "example.com", "Acme Orders", "abcd", "/home/person"}

func checkPrivate(t *testing.T, st provider.AccountStatus) {
	t.Helper()
	text := fmt.Sprintf("%+v", st)
	for _, p := range private {
		if strings.Contains(text, p) {
			t.Fatalf("the status holds %q: %s", p, text)
		}
	}
}

func TestStatus(t *testing.T) {
	cases := []struct {
		name, fixture string
		exit          int
		stderr        string
		want          provider.AccountStatus
	}{
		{"signed in", "status_signed_in.json", 0, "", provider.AccountStatus{SignedIn: true, Method: "subscription", Plan: "max", Detail: "Signed in to Claude Code with the plan max."}},
		{"signed out", "status_signed_out.json", 1, "", provider.AccountStatus{Method: "none", Detail: "Claude Code is not signed in; sign in with claude auth login."}},
		{"api key", "status_api_key.json", 0, "", provider.AccountStatus{SignedIn: true, Method: "api key", Detail: "Signed in to Claude Code."}},
		{"values that are no plain words", "status_odd_values.json", 0, "", provider.AccountStatus{SignedIn: true, Detail: "Signed in to Claude Code."}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, c.fixture, c.exit, c.stderr)
			st := newDriver(t, config.Provider{}, b.path).Status(context.Background())
			if st != c.want {
				t.Fatalf("status = %+v\nwant     %+v", st, c.want)
			}
			checkPrivate(t, st)
			if got := argsOf(t, b); !slices.Equal(got, []string{"auth", "status", "--json"}) {
				t.Fatalf("args = %q", got)
			}
			here, _ := os.Getwd()
			cwd := strings.TrimSpace(read(t, b.cwd))
			if cwd == here || !strings.Contains(filepath.Base(cwd), "semar-claude-status-") {
				t.Fatalf("cwd = %q", cwd)
			}
			if _, err := os.Stat(cwd); err == nil {
				t.Fatal("the empty directory was left behind")
			}
		})
	}
}

func TestStatusFailures(t *testing.T) {
	t.Run("text instead of JSON", func(t *testing.T) {
		dir := t.TempDir()
		script := filepath.Join(dir, "claude")
		body := "#!/bin/sh\necho 'Logged in as person@example.com'\necho 'refused for person@example.com, token=abcdef0123456789' >&2\nexit 1\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		st := newDriver(t, config.Provider{}, script).Status(context.Background())
		if st.SignedIn || !strings.Contains(st.Detail, "no answer that Semar can read, exit code 1") {
			t.Fatalf("status = %+v", st)
		}
		checkPrivate(t, st)
		if strings.Contains(st.Detail, "abcdef0123456789") {
			t.Fatalf("a token leaked: %s", st.Detail)
		}
	})
	t.Run("no answer in time", func(t *testing.T) {
		dir := t.TempDir()
		script := filepath.Join(dir, "claude")
		if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		st := newDriver(t, config.Provider{}, script).Status(ctx)
		if st.SignedIn || !strings.Contains(st.Detail, "did not answer") || time.Since(start) > 15*time.Second {
			t.Fatalf("status = %+v", st)
		}
	})
	t.Run("not installed", func(t *testing.T) {
		d := newDriver(t, config.Provider{}, "")
		t.Setenv("PATH", t.TempDir())
		if st := d.Status(context.Background()); st.SignedIn || !strings.Contains(st.Detail, "PATH") {
			t.Fatalf("status = %+v", st)
		}
		if cmd := d.LoginCommand(); cmd != nil {
			t.Fatalf("login = %q", cmd)
		}
	})
	t.Run("with the setup the person's environment is used", func(t *testing.T) {
		b := newBinary(t, "status_signed_in.json", 0, "")
		st := newSetupDriver(t, config.Provider{Setup: config.Setup{MCP: true}}, b.path).Status(context.Background())
		if !st.SignedIn {
			t.Fatalf("status = %+v", st)
		}
		env := envOf(t, b)
		if env["CLAUDE_CONFIG_DIR"] != "/home/person/.claude" {
			t.Fatalf("env = %v", env)
		}
		if _, ok := env["ANTHROPIC_API_KEY"]; ok {
			t.Fatal("the status command inherited ANTHROPIC_API_KEY")
		}
	})
}

func TestLoginCommand(t *testing.T) {
	b := newBinary(t, "", 0, "")
	var account provider.Account = newDriver(t, config.Provider{}, b.path)
	got := account.LoginCommand()
	if !slices.Equal(got, []string{b.path, "auth", "login"}) {
		t.Fatalf("login = %q", got)
	}
	if _, err := os.Stat(b.args); err == nil {
		t.Fatal("the login command was started")
	}
}
