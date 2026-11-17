//go:build unix

package codex_test

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
	"github.com/jancuk/semar/internal/provider/codex"
)

const schema = `{"type":"object","additionalProperties":false,"required":["method","path"],"properties":{"method":{"type":"string"},"path":{"type":"string"}}}`

var (
	_ provider.Instance = (*codex.Driver)(nil)
	_ provider.Account  = (*codex.Driver)(nil)
)

type fakeBinary struct {
	path, args, cwd, env, stdin, zero, schema, mode, schemaPath string
}

// newBinary writes a shell script that records how it was started, keeps a copy of the schema
// file with its mode, and then prints a fixture.
func newBinary(t *testing.T, fixture string, exit int, stderr string) fakeBinary {
	t.Helper()
	dir := t.TempDir()
	b := fakeBinary{
		path:       filepath.Join(dir, "codex-fake"),
		args:       filepath.Join(dir, "args"),
		cwd:        filepath.Join(dir, "cwd"),
		env:        filepath.Join(dir, "env"),
		stdin:      filepath.Join(dir, "stdin"),
		zero:       filepath.Join(dir, "zero"),
		schema:     filepath.Join(dir, "schema"),
		mode:       filepath.Join(dir, "mode"),
		schemaPath: filepath.Join(dir, "schema-path"),
	}
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\0' \"$@\" > %q\nprintf '%%s' \"$0\" > %q\npwd -P > %q\nenv > %q\ncat > %q\n", b.args, b.zero, b.cwd, b.env, b.stdin)
	body += fmt.Sprintf("prev=\nfor a in \"$@\"; do\n  if [ \"$prev\" = --output-schema ]; then cp \"$a\" %q; ls -l \"$a\" | cut -c1-10 > %q; printf '%%s' \"$a\" > %q; fi\n  prev=$a\ndone\n", b.schema, b.mode, b.schemaPath)
	if fixture != "" {
		abs := fixture
		if !filepath.IsAbs(abs) {
			var err error
			if abs, err = filepath.Abs(filepath.Join("testdata", fixture)); err != nil {
				t.Fatal(err)
			}
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

func argsOf(t *testing.T, b fakeBinary) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(read(t, b.args), "\x00"), "\x00")
}

// envWith is the environment of the test process with SEMAR_CODEX_BIN set to bin, or unset
// when bin is empty.
func envWith(bin string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if name == codex.BinaryEnv {
			return bin, bin != ""
		}
		return os.LookupEnv(name)
	}
}

func newDriver(t *testing.T, cfg config.Provider, bin string) *codex.Driver {
	t.Helper()
	d, err := codex.New(cfg, codex.Options{Lookup: envWith(bin)})
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

func envNames(t *testing.T, b fakeBinary) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(read(t, b.env)), "\n") {
		name, value, _ := strings.Cut(line, "=")
		got[name] = value
	}
	return got
}

func TestRunSetupOff(t *testing.T) {
	b := newBinary(t, "success.jsonl", 0, "")
	t.Setenv("OPENAI_API_KEY", "sk-do-not-pass")
	t.Setenv("CODEX_HOME", "/home/acme/.codex")
	t.Setenv("SEMAR_RANDOM", "x")
	t.Setenv("SOMETHING_ELSE", "x")
	d := newDriver(t, config.Provider{ID: "c", Kind: "codex"}, b.path)
	j := job(t)
	res, err := d.Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID() != "c" || d.Kind() != "codex" {
		t.Fatal("identity")
	}
	if string(res.Output) != `{"method":"GET","path":"/v1/orders"}` || res.Model != "" || res.Turns != 1 ||
		res.InputTokens != 1200 || res.CacheReadTokens != 800 || res.CacheWriteTokens != 5 || res.OutputTokens != 90 ||
		res.Denied != 1 || res.CostUSD != 0 || len(res.Limits) != 0 {
		t.Fatalf("got %+v", res)
	}
	wantTools := []provider.ToolUse{{Name: "mcp__orders__list_orders", Calls: 2}, {Name: "shell", Calls: 2}}
	if !slices.Equal(res.Tools, wantTools) {
		t.Fatalf("tools = %+v", res.Tools)
	}

	wd, _ := filepath.EvalSymlinks(j.Workdir)
	schemaFile := read(t, b.schemaPath)
	q := `"` + wd + `"`
	want := []string{
		"exec", "--json", "--ephemeral", "--skip-git-repo-check", "--ignore-rules", "--cd", wd,
		"--output-schema", schemaFile,
		"--ignore-user-config",
		"--disable", "hooks", "--disable", "memories", "--disable", "computer_use", "--disable", "browser_use",
		"--disable", "browser_use_external", "--disable", "in_app_browser", "--disable", "image_generation",
		"--disable", "multi_agent", "--disable", "tool_suggest", "--disable", "skill_mcp_dependency_install",
		"--disable", "plugins", "--disable", "apps",
		"--config", `approval_policy="never"`,
		"--config", `web_search="disabled"`,
		"--config", `allow_login_shell=false`,
		"--config", `shell_environment_policy={inherit="core", experimental_use_profile=false}`,
		"--config", `project_doc_max_bytes=0`,
		"--config", `project_root_markers=[]`,
		"--config", `projects={` + q + `={trust_level="untrusted"}}`,
		"--config", `default_permissions="semar"`,
		"--config", `permissions.semar={filesystem={":minimal"="read", ":tmpdir"="deny", ":slash_tmp"="deny", ` + q + `="read"}}`,
		"--config", "skills.include_instructions=false",
		"--config", "skills.bundled.enabled=false",
		"-",
	}
	if got := argsOf(t, b); !slices.Equal(got, want) {
		t.Fatalf("args = %q\nwant   %q", got, want)
	}
	if read(t, b.stdin) != "plan this change" {
		t.Fatalf("stdin = %q", read(t, b.stdin))
	}
	if got := strings.TrimSpace(read(t, b.cwd)); got != wd {
		t.Fatalf("cwd = %q, want %q", got, wd)
	}
	env := envNames(t, b)
	for name := range env {
		switch name {
		case "PATH", "HOME", "USER", "LOGNAME", "LANG", "TERM", "TMPDIR", "CODEX_HOME", "PWD", "SHLVL", "_", "OLDPWD":
		default:
			t.Errorf("the child inherited %s", name)
		}
	}
	if env["CODEX_HOME"] != "/home/acme/.codex" {
		t.Errorf("CODEX_HOME = %q", env["CODEX_HOME"])
	}

	t.Run("the schema file", func(t *testing.T) {
		if read(t, b.schema) != schema {
			t.Fatalf("schema = %q", read(t, b.schema))
		}
		if got := strings.TrimSpace(read(t, b.mode)); got != "-rw-------" {
			t.Fatalf("mode = %q", got)
		}
		realFile, _ := filepath.EvalSymlinks(filepath.Dir(schemaFile))
		if rel, err := filepath.Rel(wd, realFile); err == nil && filepath.IsLocal(rel) {
			t.Fatalf("the schema file %q lies inside the working directory", schemaFile)
		}
		if _, err := os.Stat(schemaFile); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the schema file is left after the run: %v", err)
		}
	})
}

func TestRunSetupSwitches(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".codex/skills", ".codex/plugins", ".agents/skills"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	realHome, _ := filepath.EvalSymlinks(home)
	cases := []struct {
		name          string
		setup         config.Setup
		want, notWant []string
	}{
		{"mcp on", config.Setup{MCP: true},
			[]string{"skills.include_instructions=false", "skills.bundled.enabled=false"},
			[]string{"--ignore-user-config", "plugins", "apps", "mcp_servers={}", realHome}},
		{"skills on", config.Setup{Skills: true},
			[]string{"plugins", "apps", "mcp_servers={}", `"` + filepath.Join(realHome, ".codex", "skills") + `"="read"`,
				`"` + filepath.Join(realHome, ".codex", "plugins") + `"="read"`, `"` + filepath.Join(realHome, ".agents", "skills") + `"="read"`},
			[]string{"--ignore-user-config", "skills.include_instructions=false"}},
		{"both on", config.Setup{MCP: true, Skills: true},
			[]string{filepath.Join(realHome, ".codex", "skills")},
			[]string{"--ignore-user-config", "plugins", "apps", "mcp_servers={}", "skills.include_instructions=false"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, "success.jsonl", 0, "")
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", "")
			os.Unsetenv("CODEX_HOME")
			d := newDriver(t, config.Provider{Setup: c.setup}, b.path)
			j := job(t)
			skill := filepath.Join(j.Workdir, ".agents", "skills", "acme", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(skill), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(skill, []byte("---\nname: acme\n---\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Run(context.Background(), j); err != nil {
				t.Fatal(err)
			}
			args := argsOf(t, b)
			joined := strings.Join(args, "\x00")
			for _, w := range c.want {
				if !strings.Contains(joined, w) {
					t.Errorf("args lack %q: %q", w, args)
				}
			}
			for _, w := range c.notWant {
				if slices.Contains(args, w) || (strings.Contains(w, "/") && strings.Contains(joined, w)) {
					t.Errorf("args hold %q: %q", w, args)
				}
			}
			for _, always := range []string{"hooks", "memories", "--ignore-rules", `approval_policy="never"`, `project_doc_max_bytes=0`, `default_permissions="semar"`} {
				if !slices.Contains(args, always) {
					t.Errorf("args lack %q", always)
				}
			}
			wd, _ := filepath.EvalSymlinks(j.Workdir)
			disabled := `skills.config=[{path="` + filepath.Join(wd, ".agents", "skills", "acme", "SKILL.md") + `", enabled=false}]`
			if c.setup.Skills != slices.Contains(args, disabled) {
				t.Errorf("the skill of the working directory: args = %q", args)
			}
		})
	}
}

func TestEnvWithSetupOn(t *testing.T) {
	b := newBinary(t, "success.jsonl", 0, "")
	environ := []string{
		"PATH=/usr/bin:/bin", "HOME=/home/acme", "EDITOR=vi", "CODEX_HOME=/home/acme/.codex", "HTTPS_PROXY=http://proxy.example.com",
		"ANTHROPIC_API_KEY=a", "ANTHROPIC_BASE_URL=b", "OPENAI_API_KEY=c", "DEEPSEEK_API_KEY=d", "GEMINI_API_KEY=e",
		"GOOGLE_API_KEY=f", "SEMAR_CODEX_BIN=" + b.path, "ORDERS_TOKEN=g", "MALFORMED",
	}
	d, err := codex.New(config.Provider{Setup: config.Setup{MCP: true}}, codex.Options{
		Lookup:  envWith(b.path),
		Environ: func() []string { return environ },
		DropEnv: []string{"ORDERS_TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Run(context.Background(), job(t)); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(argsOf(t, b), `shell_environment_policy={inherit="core", experimental_use_profile=false}`) {
		t.Errorf("the commands of the model would inherit the environment of Codex: args = %q", argsOf(t, b))
	}
	env := envNames(t, b)
	for _, name := range []string{"PATH", "HOME", "EDITOR", "CODEX_HOME", "HTTPS_PROXY"} {
		if _, ok := env[name]; !ok {
			t.Errorf("the child lacks %s", name)
		}
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "SEMAR_CODEX_BIN", "ORDERS_TOKEN", "MALFORMED"} {
		if _, ok := env[name]; ok {
			t.Errorf("the child inherited %s", name)
		}
	}
}

func TestRunModel(t *testing.T) {
	cases := []struct {
		name, cfgModel, jobModel string
		want                     string
	}{
		{"none", "", "", ""},
		{"configured", "cfg-model", "", "--model=cfg-model"},
		{"job wins", "cfg-model", "job-model", "--model=job-model"},
		{"job only", "", "job-model", "--model=job-model"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, "success.jsonl", 0, "")
			d := newDriver(t, config.Provider{Model: c.cfgModel}, b.path)
			j := job(t)
			j.Model = c.jobModel
			res, err := d.Run(context.Background(), j)
			if err != nil {
				t.Fatal(err)
			}
			args := argsOf(t, b)
			models := slices.DeleteFunc(slices.Clone(args), func(a string) bool { return !strings.HasPrefix(a, "--model") && a != "-m" })
			switch {
			case c.want == "" && len(models) != 0, c.want != "" && !slices.Equal(models, []string{c.want}):
				t.Fatalf("args = %q", args)
			}
			if res.Model != strings.TrimPrefix(c.want, "--model=") {
				t.Fatalf("model = %q", res.Model)
			}
		})
	}
	t.Run("a value that looks like a flag", func(t *testing.T) {
		b := newBinary(t, "success.jsonl", 0, "")
		j := job(t)
		j.Model = "--dangerously-bypass-approvals-and-sandbox"
		_, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), j)
		if classOf(t, err) != provider.ValidationError {
			t.Fatalf("got %v", err)
		}
		if _, statErr := os.Stat(b.args); statErr == nil {
			t.Fatal("the child started")
		}
	})
	t.Run("a configured value that looks like a flag", func(t *testing.T) {
		if _, err := codex.New(config.Provider{ID: "x", Model: "-c"}, codex.Options{Lookup: os.LookupEnv}); err == nil {
			t.Fatal("New accepted the model")
		}
	})
}

func TestNewRefusesDeny(t *testing.T) {
	_, err := codex.New(config.Provider{ID: "x", Setup: config.Setup{MCP: true, Deny: []string{"mcp__orders__delete"}}}, codex.Options{Lookup: os.LookupEnv})
	if err == nil || !strings.Contains(err.Error(), "setup.deny is not supported") {
		t.Fatalf("got %v", err)
	}
	if _, err := codex.New(config.Provider{}, codex.Options{}); err == nil {
		t.Fatal("New accepted no lookup")
	}
}

func TestRunSchemaThatStrictModeRefuses(t *testing.T) {
	b := newBinary(t, "answer_fenced.jsonl", 0, "")
	j := job(t)
	j.Schema = json.RawMessage(`{"type":"object","properties":{"method":{"type":"string"},"json":{}}}`)
	res, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"method":"GET"}` {
		t.Fatalf("output = %s", res.Output)
	}
	if slices.Contains(argsOf(t, b), "--output-schema") {
		t.Fatal("a schema that strict mode refuses was passed")
	}
	stdin := read(t, b.stdin)
	if !strings.HasPrefix(stdin, "plan this change\n\n") || !strings.Contains(stdin, `{"type":"object","properties":{"method":{"type":"string"},"json":{}}}`) {
		t.Fatalf("stdin = %q", stdin)
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
		{"schema refused", "failed_schema.jsonl", 1, "", provider.ValidationError, "Missing 'extra'"},
		{"usage limit", "failed_usage.jsonl", 1, "", provider.UsageLimit, "usage limit"},
		{"rate limit by status", "failed_rate.jsonl", 1, "", provider.UsageLimit, "Slow down."},
		{"not signed in", "failed_unauthorized.jsonl", 1, "", provider.PermissionError, "Your session has expired."},
		{"other error", "failed_other.jsonl", 1, "", provider.ProviderError, "is not supported"},
		{"error without a finished turn", "error_only.jsonl", 1, "", provider.ProviderError, "stream disconnected"},
		{"no answer", "no_answer.jsonl", 0, "", provider.ValidationError, "the answer is not one JSON object"},
		{"prose answer", "answer_prose.jsonl", 0, "", provider.ValidationError, "the answer is not one JSON object"},
		{"two objects", "answer_two_objects.jsonl", 0, "", provider.ValidationError, "the answer is not one JSON object"},
		{"text after the object", "answer_trailing_text.jsonl", 0, "", provider.ValidationError, "the answer is not one JSON object"},
		{"crash with no JSON", "", 3, "boom: out of memory", provider.ProviderError, "exit code 3: boom: out of memory"},
		{"not JSON on success", "", 0, "", provider.ProviderError, "not JSON"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, c.fixture, c.exit, c.stderr)
			_, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), job(t))
			if got := classOf(t, err); got != c.class {
				t.Fatalf("class = %s, err = %v", got, err)
			}
			if !strings.Contains(err.Error(), c.detail) {
				t.Fatalf("error %q lacks %q", err, c.detail)
			}
			if strings.Contains(err.Error(), "plan this change") || strings.Contains(err.Error(), "/v1/orders") {
				t.Fatal("the error holds the prompt or the answer")
			}
		})
	}
}

func TestRunOverLongLine(t *testing.T) {
	dir := t.TempDir()
	long := `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"pad\":\"` + strings.Repeat("x", 5<<20) + `\"}"}}`
	turn := `{"type":"turn.completed","usage":{"input_tokens":3,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":1}}`
	short := `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"{\"ok\":true}"}}`
	write := func(name string, lines ...string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("the long line is skipped", func(t *testing.T) {
		b := newBinary(t, write("long.jsonl", long, turn), 0, "")
		_, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), job(t))
		if classOf(t, err) != provider.ValidationError {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("the lines after it count", func(t *testing.T) {
		b := newBinary(t, write("long-then-short.jsonl", long, short, turn), 0, "")
		res, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), job(t))
		if err != nil || string(res.Output) != `{"ok":true}` || res.InputTokens != 3 {
			t.Fatalf("got %+v %v", res, err)
		}
	})
}

func TestRunStderrIsRedactedAndCut(t *testing.T) {
	stderr := "token=abcdef0123 Bearer sk-proj-abcdefghijklmnopqrstuvwxyz0123456789 " + strings.Repeat("x ", 400)
	b := newBinary(t, "", 1, stderr)
	_, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), job(t))
	text := err.Error()
	if strings.Contains(text, "abcdef0123") || strings.Contains(text, "sk-proj") {
		t.Fatalf("a secret leaked: %s", text)
	}
	if len(text) > 500 {
		t.Fatalf("detail has %d bytes", len(text))
	}
}

func TestRunNotInstalled(t *testing.T) {
	t.Run("on PATH at run time", func(t *testing.T) {
		d := newDriver(t, config.Provider{}, "")
		t.Setenv("PATH", t.TempDir())
		_, err := d.Run(context.Background(), job(t))
		if classOf(t, err) != provider.NotInstalled || !strings.Contains(err.Error(), `"codex"`) || !strings.Contains(err.Error(), "PATH") {
			t.Fatalf("got %v", err)
		}
		if h := d.Health(context.Background()); h.Installed || h.Detail == "" {
			t.Fatalf("health = %+v", h)
		}
		if s := d.Status(context.Background()); s.SignedIn || s.Detail == "" {
			t.Fatalf("status = %+v", s)
		}
		if d.LoginCommand() != nil {
			t.Fatal("a login command without a binary")
		}
	})
	t.Run("relative PATH entry", func(t *testing.T) {
		b := newBinary(t, "success.jsonl", 0, "")
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "bin"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(b.path, filepath.Join(dir, "bin", "codex")); err != nil {
			t.Fatal(err)
		}
		t.Chdir(dir)
		t.Setenv("PATH", "bin")
		_, err := newDriver(t, config.Provider{}, "").Run(context.Background(), job(t))
		if classOf(t, err) != provider.NotInstalled || !strings.Contains(err.Error(), "relative") {
			t.Fatalf("got %v", err)
		}
		if _, statErr := os.Stat(b.args); statErr == nil {
			t.Fatal("the child started")
		}
	})
}

func TestBinaryFromEnvironmentIsChecked(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, bin, detail string }{
		{"relative", "tools/codex", "is \"tools/codex\", which is not an absolute path"},
		{"directory", dir, "which is not a regular file"},
		{"not executable", plain, "which is not executable"},
		{"missing", filepath.Join(dir, "nope"), "which cannot be read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newDriver(t, config.Provider{}, c.bin)
			_, err := d.Run(context.Background(), job(t))
			if classOf(t, err) != provider.NotInstalled || !strings.Contains(err.Error(), codex.BinaryEnv) || !strings.Contains(err.Error(), c.detail) {
				t.Fatalf("got %v", err)
			}
			h := d.Health(context.Background())
			if h.Installed || !strings.Contains(h.Detail, codex.BinaryEnv) || !strings.Contains(h.Detail, c.detail) {
				t.Fatalf("health = %+v", h)
			}
		})
	}
}

func TestRunRefusesABinaryInsideTheWorkdir(t *testing.T) {
	b := newBinary(t, "success.jsonl", 0, "")
	body, err := os.ReadFile(b.path)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		place func(t *testing.T, workdir string) string
	}{
		{"a file of the copy", func(t *testing.T, workdir string) string {
			p := filepath.Join(workdir, "tools", "codex")
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, body, 0o755); err != nil {
				t.Fatal(err)
			}
			return p
		}},
		{"a link outside that points into the copy", func(t *testing.T, workdir string) string {
			p := filepath.Join(workdir, "codex")
			if err := os.WriteFile(p, body, 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(t.TempDir(), "codex")
			if err := os.Symlink(p, link); err != nil {
				t.Fatal(err)
			}
			return link
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := job(t)
			bin := c.place(t, j.Workdir)
			_, err := newDriver(t, config.Provider{}, bin).Run(context.Background(), j)
			if classOf(t, err) != provider.PermissionError || !strings.Contains(err.Error(), "inside the working directory") {
				t.Fatalf("got %v", err)
			}
			if _, statErr := os.Stat(b.args); statErr == nil {
				t.Fatal("the child started")
			}
		})
	}
}

func TestAHarnessInsideTheRepositoryIsNotStarted(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	body := fmt.Sprintf("#!/bin/sh\ntouch %q\necho 'codex-cli 0.169.0'\n", marker)
	place := func(t *testing.T, dir string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "codex")
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	started := func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}
	const refusal = "a harness inside the repository under test is not started"
	cases := []struct {
		name string
		env  func(t *testing.T, repo string) func(string) (string, bool)
	}{
		{"the first entry of PATH", func(t *testing.T, repo string) func(string) (string, bool) {
			place(t, filepath.Join(repo, "bin"))
			t.Setenv("PATH", filepath.Join(repo, "bin")+string(os.PathListSeparator)+"/usr/bin:/bin")
			return envWith("")
		}},
		{"SEMAR_CODEX_BIN", func(t *testing.T, repo string) func(string) (string, bool) {
			return envWith(place(t, filepath.Join(repo, "tools")))
		}},
		{"a link outside that points into the repository", func(t *testing.T, repo string) func(string) (string, bool) {
			link := filepath.Join(t.TempDir(), "codex")
			if err := os.Symlink(place(t, filepath.Join(repo, "bin")), link); err != nil {
				t.Fatal(err)
			}
			return envWith(link)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := t.TempDir()
			d, err := codex.New(config.Provider{}, codex.Options{Lookup: c.env(t, repo), RepoRoot: repo})
			if err != nil {
				t.Fatal(err)
			}
			if h := d.Health(context.Background()); h.Installed || !strings.Contains(h.Detail, refusal) {
				t.Errorf("health = %+v", h)
			}
			if s := d.Status(context.Background()); s.SignedIn || !strings.Contains(s.Detail, refusal) {
				t.Errorf("status = %+v", s)
			}
			if d.LoginCommand() != nil {
				t.Error("a login command for a harness inside the repository")
			}
			_, err = d.Run(context.Background(), job(t))
			if classOf(t, err) != provider.PermissionError || !strings.Contains(err.Error(), refusal) {
				t.Errorf("run: %v", err)
			}
			if started() {
				t.Fatal("the harness inside the repository was started")
			}
		})
	}
}

func TestRunTimeoutAndCancel(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hang")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > /dev/null\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := newDriver(t, config.Provider{}, script)
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
	b := newBinary(t, "success.jsonl", 0, "")
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
			_, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), j)
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
	script := filepath.Join(dir, "codex-v")
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\necho 'codex-cli 0.169.0'\necho second line\n", record)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	h := newDriver(t, config.Provider{}, script).Health(context.Background())
	if !h.Installed || h.Version != "codex-cli 0.169.0" || h.Detail != "" {
		t.Fatalf("health = %+v", h)
	}
	if read(t, record) != "--version\n" {
		t.Fatalf("args = %q", read(t, record))
	}
	t.Run("a failing binary", func(t *testing.T) {
		b := newBinary(t, "", 2, "broken install")
		h := newDriver(t, config.Provider{}, b.path).Health(context.Background())
		if h.Installed || !strings.Contains(h.Detail, "broken install") {
			t.Fatalf("health = %+v", h)
		}
	})
}

func TestAccount(t *testing.T) {
	cases := []struct {
		name, stdout, stderr string
		exit                 int
		want                 provider.AccountStatus
	}{
		{"chatgpt", "", "Logged in using ChatGPT\nAccount: someone@example.com", 0,
			provider.AccountStatus{SignedIn: true, Method: "subscription", Detail: "Signed in to Codex with ChatGPT."}},
		{"api key", "", "Logged in using an API key - sk-proj-***ABCD (someone@example.com)", 0,
			provider.AccountStatus{SignedIn: true, Method: "api key", Detail: "Signed in to Codex with an API key."}},
		{"access token", "Logged in using access token\n", "", 0,
			provider.AccountStatus{SignedIn: true, Method: "access token", Detail: "Signed in to Codex with an access token."}},
		{"not signed in", "", "Not logged in", 1,
			provider.AccountStatus{Detail: "Not signed in. Run codex login."}},
		{"failure", "", "someone@example.com: keychain locked", 2,
			provider.AccountStatus{Detail: "codex login status ended with exit code 2"}},
		{"unknown answer", "someone@example.com\n", "", 0,
			provider.AccountStatus{Detail: "codex login status gave an answer that is not known"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			record := filepath.Join(dir, "args")
			script := filepath.Join(dir, "codex")
			body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nprintf '%%s' %q\nprintf '%%s' %q >&2\nexit %d\n", record, c.stdout, c.stderr, c.exit)
			if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			d := newDriver(t, config.Provider{}, script)
			got := d.Status(context.Background())
			if got != c.want {
				t.Fatalf("status = %+v, want %+v", got, c.want)
			}
			if text := fmt.Sprintf("%+v", got); strings.Contains(text, "@") || strings.Contains(text, "someone") || strings.Contains(text, "sk-") {
				t.Fatalf("the status holds account data: %s", text)
			}
			if read(t, record) != "login\nstatus\n" {
				t.Fatalf("args = %q", read(t, record))
			}
			if lc := d.LoginCommand(); !slices.Equal(lc, []string{script, "login"}) {
				t.Fatalf("login command = %q", lc)
			}
		})
	}
	t.Run("a status command that hangs", func(t *testing.T) {
		dir := t.TempDir()
		script := filepath.Join(dir, "codex")
		if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if s := newDriver(t, config.Provider{}, script).Status(ctx); s.SignedIn || s.Detail != "codex login status did not finish" {
			t.Fatalf("status = %+v", s)
		}
	})
}

func TestFactory(t *testing.T) {
	in, err := codex.Factory(codex.Options{Lookup: os.LookupEnv})(config.Provider{ID: "x", Kind: "codex"})
	if err != nil || in.Kind() != "codex" || in.ID() != "x" {
		t.Fatalf("got %v %v", in, err)
	}
	if _, err := codex.Factory(codex.Options{Lookup: os.LookupEnv})(config.Provider{ID: "x", Setup: config.Setup{Deny: []string{"x"}}}); err == nil {
		t.Fatal("the factory accepted setup.deny")
	}
}

func TestRunReasoning(t *testing.T) {
	cases := []struct {
		name, cfgLevel, jobLevel, want string
	}{
		{"none", "", "", ""},
		{"configured", "medium", "", `model_reasoning_effort="medium"`},
		{"job wins", "medium", "xhigh", `model_reasoning_effort="xhigh"`},
		{"a level that only the catalog knows", "", "acme", `model_reasoning_effort="acme"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, "success.jsonl", 0, "")
			d := newDriver(t, config.Provider{Reasoning: c.cfgLevel}, b.path)
			j := job(t)
			j.Reasoning = c.jobLevel
			res, err := d.Run(context.Background(), j)
			if err != nil {
				t.Fatal(err)
			}
			args := argsOf(t, b)
			var got []string
			for i, a := range args {
				if strings.Contains(a, "model_reasoning_effort") {
					if args[i-1] != "--config" {
						t.Fatalf("the override is not the value of --config: %q", args)
					}
					got = append(got, a)
				}
			}
			if c.want == "" && len(got) != 0 || c.want != "" && !slices.Equal(got, []string{c.want}) {
				t.Fatalf("args = %q", args)
			}
			if want := strings.TrimSuffix(strings.TrimPrefix(c.want, `model_reasoning_effort="`), `"`); res.Reasoning != want {
				t.Fatalf("result level = %q, want %q", res.Reasoning, want)
			}
		})
	}
	t.Run("a level that looks like a flag", func(t *testing.T) {
		b := newBinary(t, "success.jsonl", 0, "")
		j := job(t)
		j.Reasoning = `high"; sandbox_mode="danger-full-access`
		_, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), j)
		if classOf(t, err) != provider.ValidationError {
			t.Fatalf("got %v", err)
		}
		if _, statErr := os.Stat(b.args); statErr == nil {
			t.Fatal("the child started")
		}
		if _, err := codex.New(config.Provider{ID: "x", Reasoning: "-c"}, codex.Options{Lookup: os.LookupEnv}); err == nil {
			t.Fatal("New accepted the level")
		}
	})
}

func TestNoToolsJobTurnsTheCommandToolsOff(t *testing.T) {
	for _, noTools := range []bool{false, true} {
		b := newBinary(t, "success.jsonl", 0, "")
		d := newDriver(t, config.Provider{ID: "c", Kind: "codex"}, b.path)
		j := job(t)
		j.NoTools = noTools
		if _, err := d.Run(context.Background(), j); err != nil {
			t.Fatal(err)
		}
		args := argsOf(t, b)
		var off []string
		for i, a := range args {
			if a == "--disable" {
				off = append(off, args[i+1])
			}
		}
		for _, f := range []string{"shell_tool", "unified_exec"} {
			if slices.Contains(off, f) != noTools {
				t.Errorf("noTools=%v: --disable %s present = %v", noTools, f, !noTools)
			}
		}
	}
}

func TestRunSchemaInPromptPassesNoSchemaFile(t *testing.T) {
	b := newBinary(t, "success.jsonl", 0, "")
	j := job(t)
	j.SchemaInPrompt = true
	if _, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(argsOf(t, b), "--output-schema") {
		t.Fatal("a schema that the caller wants in the prompt was enforced")
	}
	if stdin := read(t, b.stdin); !strings.HasPrefix(stdin, "plan this change\n\n") || !strings.Contains(stdin, schema) {
		t.Fatalf("stdin = %q", stdin)
	}
}

func TestRunDropsAClosingBraceTooMany(t *testing.T) {
	b := newBinary(t, "answer_extra_brace.jsonl", 0, "")
	res, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), job(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"method":"GET","steps":[{"id":"a"}]}` {
		t.Fatalf("output = %s", res.Output)
	}
}
