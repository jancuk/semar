//go:build unix

package claude_test

import (
	"context"
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

const setupSettings = `{"disableAllHooks":true,"permissions":{"blockReadsOutsideWorkingDirectories":true}}`

// personEnv is the person's environment of the tests that run with the setup.
func personEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=/home/person", "ORDERS_REGION=eu", "CLAUDE_CONFIG_DIR=/home/person/.claude",
		"ANTHROPIC_API_KEY=sk-do-not-pass", "ANTHROPIC_BASE_URL=https://proxy.example.com", "OPENAI_API_KEY=sk-other",
		"SEMAR_RANDOM=x", "CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_SESSION_ID=s1",
		"ACME_DEPLOY_TOKEN=secret-value",
	}
}

func newSetupDriver(t *testing.T, cfg config.Provider, bin string) *claude.Driver {
	t.Helper()
	d, err := claude.NewWith(cfg, claude.Options{Lookup: envWith(bin), Environ: personEnv, DropEnv: []string{"ACME_DEPLOY_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func argsOf(t *testing.T, b fakeBinary) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(read(t, b.args), "\x00"), "\x00")
}

func envOf(t *testing.T, b fakeBinary) map[string]string {
	t.Helper()
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(read(t, b.env)), "\n") {
		name, value, _ := strings.Cut(line, "=")
		env[name] = value
	}
	return env
}

func TestSetupArgs(t *testing.T) {
	streamHead := []string{"-p", "--output-format", "stream-json", "--verbose", "--json-schema", schema}
	tail := []string{"--no-session-persistence", "--max-turns", "7"}
	cat := func(parts ...[]string) []string { return slices.Concat(parts...) }
	cases := []struct {
		name  string
		setup config.Setup
		model string
		want  []string
	}{
		{"MCP on", config.Setup{MCP: true}, "", cat(streamHead,
			[]string{"--tools", "Read,Grep,Glob", "--setting-sources", "user", "--settings", setupSettings}, tail)},
		{"skills on, MCP off", config.Setup{Skills: true}, "", cat(streamHead,
			[]string{"--tools", "Read,Grep,Glob,Skill", "--strict-mcp-config", "--setting-sources", "user", "--settings", setupSettings}, tail)},
		{"both on", config.Setup{MCP: true, Skills: true}, "", cat(streamHead,
			[]string{"--tools", "Read,Grep,Glob,Skill", "--setting-sources", "user", "--settings", setupSettings}, tail)},
		{"both on with deny and a model", config.Setup{MCP: true, Skills: true, Deny: []string{"mcp__orders__delete", "Bash(git push:*)"}}, "m1", cat(streamHead,
			[]string{"--tools", "Read,Grep,Glob,Skill", "--setting-sources", "user", "--settings", setupSettings,
				"--disallowedTools", "mcp__orders__delete", "Bash(git push:*)"}, tail, []string{"--model=m1"})},
		{"deny with both off", config.Setup{Deny: []string{"Grep"}}, "", cat(streamHead,
			[]string{"--tools", "Read,Grep,Glob", "--strict-mcp-config", "--setting-sources", "", "--disallowedTools", "Grep"}, tail)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, "stream_success.jsonl", 0, "")
			d := newSetupDriver(t, config.Provider{ID: "c", Kind: "claude", Model: c.model, Setup: c.setup}, b.path)
			if _, err := d.Run(context.Background(), job(t)); err != nil {
				t.Fatal(err)
			}
			if got := argsOf(t, b); !slices.Equal(got, c.want) {
				t.Fatalf("args = %q\nwant   %q", got, c.want)
			}
		})
	}
}

func TestSetupEnvironment(t *testing.T) {
	t.Run("with the setup", func(t *testing.T) {
		b := newBinary(t, "stream_success.jsonl", 0, "")
		d := newSetupDriver(t, config.Provider{Setup: config.Setup{MCP: true}}, b.path)
		if _, err := d.Run(context.Background(), job(t)); err != nil {
			t.Fatal(err)
		}
		env := envOf(t, b)
		for name, want := range map[string]string{
			"HOME": "/home/person", "ORDERS_REGION": "eu", "CLAUDE_CONFIG_DIR": "/home/person/.claude",
			"CLAUDE_CODE_DISABLE_CLAUDE_MDS": "1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1",
		} {
			if env[name] != want {
				t.Errorf("%s = %q, want %q", name, env[name], want)
			}
		}
		for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "OPENAI_API_KEY", "SEMAR_RANDOM", claude.BinaryEnv,
			"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "ACME_DEPLOY_TOKEN"} {
			if _, ok := env[name]; ok {
				t.Errorf("the child inherited %s", name)
			}
		}
	})
	t.Run("without the setup the short list stays", func(t *testing.T) {
		b := newBinary(t, "success.jsonl", 0, "")
		d := newSetupDriver(t, config.Provider{Setup: config.Setup{Deny: []string{"Grep"}}}, b.path)
		if _, err := d.Run(context.Background(), job(t)); err != nil {
			t.Fatal(err)
		}
		for name := range envOf(t, b) {
			switch name {
			case "PATH", "HOME", "USER", "LOGNAME", "LANG", "TERM", "TMPDIR", "PWD", "SHLVL", "_", "OLDPWD":
			default:
				t.Errorf("the child inherited %s", name)
			}
		}
	})
}

func TestNewWithRefuses(t *testing.T) {
	cases := []struct {
		name   string
		cfg    config.Provider
		opts   claude.Options
		detail string
	}{
		{"no lookup", config.Provider{}, claude.Options{}, "no environment lookup"},
		{"setup without an environment", config.Provider{ID: "c", Setup: config.Setup{Skills: true}}, claude.Options{Lookup: os.LookupEnv}, "no environment"},
		{"empty deny entry", config.Provider{Setup: config.Setup{Deny: []string{"Grep", " "}}}, claude.Options{Lookup: os.LookupEnv}, "entry 2 of setup.deny"},
		{"deny entry that is a flag", config.Provider{Setup: config.Setup{Deny: []string{"--dangerously-skip-permissions"}}}, claude.Options{Lookup: os.LookupEnv}, "starts with a dash"},
		{"deny entry with a comma", config.Provider{Setup: config.Setup{Deny: []string{"Read,Grep"}}}, claude.Options{Lookup: os.LookupEnv}, "holds a comma"},
		{"deny entry with a newline", config.Provider{Setup: config.Setup{Deny: []string{"Grep\n--verbose"}}}, claude.Options{Lookup: os.LookupEnv}, "control character"},
		{"deny entry with a tab", config.Provider{Setup: config.Setup{Deny: []string{"Gr\tep"}}}, claude.Options{Lookup: os.LookupEnv}, "control character"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := claude.NewWith(c.cfg, c.opts); err == nil || !strings.Contains(err.Error(), c.detail) {
				t.Fatalf("got %v, want %q", err, c.detail)
			}
			if _, err := claude.FactoryWith(c.opts)(c.cfg); err == nil {
				t.Fatal("the factory accepted it")
			}
		})
	}
	t.Run("the old constructor cannot start the setup", func(t *testing.T) {
		if _, err := claude.New(config.Provider{Setup: config.Setup{MCP: true}}, os.LookupEnv, ""); err == nil {
			t.Fatal("accepted")
		}
	})
}

func TestFactoryWith(t *testing.T) {
	in, err := claude.FactoryWith(claude.Options{Lookup: os.LookupEnv, Environ: os.Environ})(config.Provider{ID: "x", Kind: "claude", Setup: config.Setup{MCP: true}})
	if err != nil || in.Kind() != "claude" || in.ID() != "x" {
		t.Fatalf("got %v %v", in, err)
	}
}

func TestStreamResult(t *testing.T) {
	b := newBinary(t, "stream_success.jsonl", 0, "")
	res, err := newSetupDriver(t, config.Provider{Setup: config.Setup{MCP: true, Skills: true}}, b.path).Run(context.Background(), job(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"method":"GET","path":"/v1/orders"}` || res.Model != "claude-sonnet-5-5" || res.Turns != 4 {
		t.Fatalf("got %+v", res)
	}
	if res.InputTokens != 4+66396+62997 || res.OutputTokens != 531 || res.CacheReadTokens != 62997 || res.CacheWriteTokens != 66396 || res.CostUSD != 0.2772 {
		t.Fatalf("usage = %+v", res)
	}
	wantTools := []provider.ToolUse{{Name: "Grep", Calls: 1}, {Name: "Read", Calls: 2}, {Name: "mcp__orders__delete", Calls: 1}, {Name: "mcp__orders__list", Calls: 1}}
	if !slices.Equal(res.Tools, wantTools) {
		t.Fatalf("tools = %+v", res.Tools)
	}
	if res.Denied != 1 {
		t.Fatalf("denied = %d", res.Denied)
	}
	wantLimits := []provider.Limit{
		{Window: "five_hour", Utilization: 0.09, ResetsAt: time.Unix(1794746400, 0).UTC(), Status: "allowed_warning"},
		{Window: "seven_day", Utilization: 0.14, ResetsAt: time.Unix(1795284000, 0).UTC(), Status: "allowed"},
	}
	if !slices.Equal(res.Limits, wantLimits) {
		t.Fatalf("limits = %+v", res.Limits)
	}
	if l := res.Loaded; !slices.Equal(l.MCPServers, []string{"acme-docs", "orders"}) || l.Skills != 2 || l.PermissionMode != "default" {
		t.Fatalf("loaded = %+v", l)
	}
}

func TestStreamWithoutTools(t *testing.T) {
	b := newBinary(t, "stream_no_tools.jsonl", 0, "")
	res, err := newSetupDriver(t, config.Provider{Setup: config.Setup{MCP: true}}, b.path).Run(context.Background(), job(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Tools != nil || res.Limits != nil || res.Denied != 0 || res.CostUSD != 0 || res.InputTokens != 3 {
		t.Fatalf("got %+v", res)
	}
	// Without the Skill tool the listed skills cannot be called.
	if l := res.Loaded; !slices.Equal(l.MCPServers, []string{"orders"}) || l.Skills != 0 || l.PermissionMode != "auto" {
		t.Fatalf("loaded = %+v", l)
	}
}

func TestStreamFailures(t *testing.T) {
	long := filepath.Join(t.TempDir(), "long.jsonl")
	resultLine := `{"type":"result","subtype":"success","is_error":false,"structured_output":{"method":"GET","pad":"` + strings.Repeat("x", 9<<20) + `"}}` + "\n"
	if err := os.WriteFile(long, []byte(`{"type":"system","subtype":"init"}`+"\n"+resultLine), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, fixture string
		exit          int
		stderr        string
		class         provider.FailureClass
		detail        string
	}{
		{"usage limit", "stream_usage_limit.jsonl", 1, "", provider.UsageLimit, "usage or rate limit"},
		{"no result event", "stream_no_result.jsonl", 1, "stopped early", provider.ProviderError, "no result event, exit code 1: stopped early"},
		{"a broken result line", "stream_broken_result.jsonl", 0, "", provider.ProviderError, "no result event"},
		{"an over-long result line", long, 0, "", provider.ProviderError, "longer than 8 MiB"},
		{"no output", "", 2, "boom", provider.ProviderError, "not JSON, exit code 2: boom"},
		{"output that is no object", "output_not_object.jsonl", 0, "", provider.ValidationError, "the output holds no object"},
		{"the turn limit", "error_max_turns.jsonl", 1, "", provider.ValidationError, "the turn limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, c.fixture, c.exit, c.stderr)
			_, err := newSetupDriver(t, config.Provider{Setup: config.Setup{MCP: true}}, b.path).Run(context.Background(), job(t))
			if got := classOf(t, err); got != c.class {
				t.Fatalf("class = %s, err = %v", got, err)
			}
			if !strings.Contains(err.Error(), c.detail) {
				t.Fatalf("error %q lacks %q", err, c.detail)
			}
		})
	}
}

func TestNoToolsJobPassesNoTool(t *testing.T) {
	b := newBinary(t, "stream_success.jsonl", 0, "")
	d := newSetupDriver(t, config.Provider{ID: "c", Kind: "claude"}, b.path)
	j := job(t)
	j.NoTools = true
	if _, err := d.Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	args := argsOf(t, b)
	i := slices.Index(args, "--tools")
	if i < 0 || i+1 >= len(args) || args[i+1] != "" {
		t.Fatalf("--tools must take one empty argument, args = %q", args)
	}
}
