package userconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func ptr(b bool) *bool { return &b }

func valid() File {
	return File{Version: 1, Default: "claude", Providers: []Provider{
		{ID: "claude", Kind: "claude"},
		{ID: "acme", Kind: "openai", BaseURL: "https://api.example.com", Model: "acme-chat", APIKey: "keychain:acme"},
	}}
}

func TestHome(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"SEMAR_HOME": "/srv/semar", "XDG_CONFIG_HOME": "/x"}, "/srv/semar"},
		{map[string]string{"XDG_CONFIG_HOME": "/x"}, "/x/semar"},
	}
	for _, c := range cases {
		got, err := Home(env(c.env))
		if err != nil || got != c.want {
			t.Errorf("Home(%v) = %q, %v; want %q", c.env, got, err, c.want)
		}
	}
	got, err := Home(env(nil))
	if err != nil || !strings.HasSuffix(got, filepath.Join(".config", "semar")) {
		t.Errorf("default Home = %q, %v", got, err)
	}
	if _, err := Home(env(map[string]string{"SEMAR_HOME": "relative/dir"})); err == nil {
		t.Error("relative SEMAR_HOME accepted")
	}
}

func TestF1MissingFileIsBuiltin(t *testing.T) {
	home := t.TempDir()
	f, exists, err := Load(home)
	if err != nil || exists {
		t.Fatalf("Load = %v, %v", exists, err)
	}
	if !reflect.DeepEqual(f, Builtin()) || f.Default != "claude" || len(f.Providers) != 1 {
		t.Errorf("not the built-in list: %+v", f)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("Load wrote something: %v", entries)
	}
	if err := Builtin().Validate(); err != nil {
		t.Errorf("Builtin invalid: %v", err)
	}
}

func TestValidateRules(t *testing.T) {
	mut := func(fn func(*File)) File {
		f := valid()
		fn(&f)
		return f
	}
	cases := []struct {
		name string
		f    File
		want string // substring of the error; empty means valid
	}{
		{"valid", valid(), ""},
		{"version", mut(func(f *File) { f.Version = 2 }), "version must be 1"},
		// F2
		{"bad id", mut(func(f *File) { f.Providers[0].ID = "../x" }), "invalid provider id"},
		{"empty id", mut(func(f *File) { f.Providers[0].ID = "" }), "invalid provider id"},
		{"duplicate id", mut(func(f *File) {
			f.Providers[1].ID = "claude"
			f.Providers[1].Kind = "claude"
			f.Providers[1].BaseURL = ""
			f.Providers[1].APIKey = ""
			f.Providers[1].Model = ""
		}), "used twice"},
		{"ids that differ only in case", mut(func(f *File) { f.Providers[1].ID = "CLAUDE" }), `"CLAUDE": the id differs from "claude" only in case`},
		{"default unknown", mut(func(f *File) { f.Default = "nope" }), `default "nope"`},
		{"default empty", mut(func(f *File) { f.Default = "" }), "default must name"},
		// F3
		{"kind fake", mut(func(f *File) { f.Providers[0].Kind = "fake" }), "for tests"},
		{"kind unknown", mut(func(f *File) { f.Providers[0].Kind = "gemini" }), "not one of"},
		{"kind empty", mut(func(f *File) { f.Providers[0].Kind = "" }), "not one of"},
		// F4
		{"setup on api", mut(func(f *File) { f.Providers[1].Setup = &Setup{} }), "setup is for"},
		{"baseUrl on harness", mut(func(f *File) { f.Providers[0].BaseURL = "https://api.example.com" }), "baseUrl is for"},
		{"apiKey on harness", mut(func(f *File) { f.Providers[0].APIKey = "env:X" }), "apiKey is for"},
		{"empty deny", mut(func(f *File) { f.Providers[0].Setup = &Setup{Deny: []string{" "}} }), "setup.deny"},
		{"deny with a dash", mut(func(f *File) { f.Providers[0].Setup = &Setup{Deny: []string{"--dangerously-skip-permissions"}} }), "starts with a dash"},
		{"deny with a comma", mut(func(f *File) { f.Providers[0].Setup = &Setup{Deny: []string{"Bash,Edit"}} }), "holds a comma"},
		{"deny with a control character", mut(func(f *File) { f.Providers[0].Setup = &Setup{Deny: []string{"Bash\nEdit"}} }), "control character"},
		{"deny of an MCP tool", mut(func(f *File) { f.Providers[0].Setup = &Setup{Deny: []string{"mcp__orders__delete", "Bash(rm:*)"}} }), ""},
		// F5
		{"http remote", mut(func(f *File) { f.Providers[1].BaseURL = "http://api.example.com" }), "http is allowed only"},
		{"http localhost", mut(func(f *File) { f.Providers[1].BaseURL = "http://localhost:8080/v1" }), ""},
		{"http 127", mut(func(f *File) { f.Providers[1].BaseURL = "http://127.0.0.1:8080" }), ""},
		{"http ipv6 loopback", mut(func(f *File) { f.Providers[1].BaseURL = "http://[::1]:8080" }), ""},
		{"http 127 lookalike", mut(func(f *File) { f.Providers[1].BaseURL = "http://127.0.0.1.example.com" }), "http is allowed only"},
		{"user info", mut(func(f *File) { f.Providers[1].BaseURL = "https://user:pw@api.example.com" }), "user info"},
		{"query", mut(func(f *File) { f.Providers[1].BaseURL = "https://api.example.com/v1?x=1" }), "query"},
		{"fragment", mut(func(f *File) { f.Providers[1].BaseURL = "https://api.example.com/v1#x" }), "fragment"},
		{"empty fragment", mut(func(f *File) { f.Providers[1].BaseURL = "https://api.example.com/v1#" }), "fragment"},
		{"ftp", mut(func(f *File) { f.Providers[1].BaseURL = "ftp://api.example.com" }), "https://"},
		{"no host", mut(func(f *File) { f.Providers[1].BaseURL = "https://" }), "not a URL"},
		{"missing baseUrl is vendor default", mut(func(f *File) { f.Providers[1].BaseURL = "" }), ""},
		// F6
		{"apiKey env", mut(func(f *File) { f.Providers[1].APIKey = "env:ACME_API_KEY" }), ""},
		{"apiKey missing", mut(func(f *File) { f.Providers[1].APIKey = "" }), "apiKey is required"},
		{"apiKey missing loopback", mut(func(f *File) { f.Providers[1].BaseURL = "http://localhost:1234"; f.Providers[1].APIKey = "" }), ""},
		{"apiKey raw", mut(func(f *File) { f.Providers[1].APIKey = "sk-live-abc123" }), "must be env:NAME or keychain:NAME"},
		{"apiKey bad scheme", mut(func(f *File) { f.Providers[1].APIKey = "file:/etc/x" }), "must be env:NAME"},
		{"apiKey bad name", mut(func(f *File) { f.Providers[1].APIKey = "env:a b" }), "not valid"},
		// F7
		{"model bad", mut(func(f *File) { f.Providers[1].Model = "bad model!" }), "not a model name"},
		{"model empty harness", mut(func(f *File) { f.Providers[0].Model = "" }), ""},
	}
	for _, c := range cases {
		err := c.f.Validate()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected error: %v", c.name, err)
		case c.want != "" && err == nil:
			t.Errorf("%s: accepted, want error with %q", c.name, c.want)
		case c.want != "" && !strings.Contains(err.Error(), c.want):
			t.Errorf("%s: error %q lacks %q", c.name, err, c.want)
		}
	}
}

func TestValidateNeverQuotesKey(t *testing.T) {
	f := valid()
	f.Providers[1].APIKey = "sk-live-secret-value"
	err := f.Validate()
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("error = %v", err)
	}
	if err := Save(t.TempDir(), f); err == nil {
		t.Error("Save accepted a raw key")
	}
}

func TestF8SaveRoundTrip(t *testing.T) {
	home := filepath.Join(t.TempDir(), "semar")
	f := File{Version: 1, Default: "codex", Providers: []Provider{
		{ID: "claude", Kind: "claude", Setup: &Setup{MCP: ptr(false), Deny: []string{"Bash(rm:*)"}}},
		{ID: "codex", Kind: "codex"},
		{ID: "anthropic", Kind: "anthropic", Model: "claude-opus-5-5", APIKey: "env:ANTHROPIC_API_KEY"},
		{ID: "local", Kind: "openai", BaseURL: "http://localhost:11434/v1", Model: "llama3"},
	}}
	if err := Save(home, f); err != nil {
		t.Fatal(err)
	}
	di, _ := os.Stat(home)
	fi, _ := os.Stat(filepath.Join(home, FileName))
	if di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
		t.Errorf("modes: dir %v file %v", di.Mode().Perm(), fi.Mode().Perm())
	}
	data, _ := os.ReadFile(filepath.Join(home, FileName))
	text := string(data)
	// F9
	if !strings.HasPrefix(text, "#") || !strings.Contains(text, "rewrites this file") || !strings.Contains(text, "comments are not kept") {
		t.Errorf("header missing:\n%s", text)
	}
	for _, want := range []string{"baseUrl: http://localhost:11434/v1", "apiKey: env:ANTHROPIC_API_KEY", "mcp: false", "skills: true"} {
		if !strings.Contains(text, want) {
			t.Errorf("file lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "skills:") != 2 || strings.Count(text, "mcp:") != 2 {
		t.Errorf("both switches must be written for each harness kind:\n%s", text)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}

	got, exists, err := Load(home)
	if err != nil || !exists {
		t.Fatalf("Load = %v, %v", exists, err)
	}
	if !reflect.DeepEqual(got.All(), f.All()) {
		t.Errorf("round trip differs:\n got %+v\nwant %+v", got.All(), f.All())
	}
	if got.Default != "codex" {
		t.Errorf("default = %q", got.Default)
	}
}

func TestSaveKeepsExistingDirectoryMode(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Save(home, Builtin()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(home); fi.Mode().Perm() != 0o755 {
		t.Errorf("directory mode changed to %v", fi.Mode().Perm())
	}
}

func TestSaveRefusesSymlinks(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere.yaml")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, FileName)); err != nil {
		t.Skip("no symlinks:", err)
	}
	err := Save(home, Builtin())
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Save = %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Errorf("the link target was written: %q", b)
	}
}

func TestSaveInvalidWritesNothing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "semar")
	f := valid()
	f.Default = "nope"
	if err := Save(home, f); err == nil {
		t.Fatal("invalid file saved")
	}
	if _, err := os.Stat(home); err == nil {
		t.Error("directory created for an invalid file")
	}
}

func TestLoadHandWrittenFile(t *testing.T) {
	home := t.TempDir()
	text := `# my providers
version: 1
default: deepseek
providers:
  - id: claude
    kind: claude
    model: ""
    setup:
      mcp: true
      deny: [mcp__orders__delete]
  - id: codex
    kind: codex
    setup: {skills: false}
  - id: anthropic
    kind: anthropic
    model: claude-opus-5-5
    apiKey: env:ANTHROPIC_API_KEY
  - id: deepseek
    kind: openai
    baseUrl: https://api.deepseek.example.com
    model: deepseek-chat
    apiKey: keychain:deepseek
`
	if err := os.WriteFile(filepath.Join(home, FileName), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	f, exists, err := Load(home)
	if err != nil || !exists {
		t.Fatalf("Load = %v, %v", exists, err)
	}
	claude, _ := f.Find("claude")
	if !claude.User || !claude.Setup.MCP || !claude.Setup.Skills || !reflect.DeepEqual(claude.Setup.Deny, []string{"mcp__orders__delete"}) {
		t.Errorf("claude = %+v", claude)
	}
	codex, _ := f.Find("codex")
	if !codex.Setup.MCP || codex.Setup.Skills {
		t.Errorf("codex setup = %+v", codex.Setup)
	}
	ds, ok := f.Find("deepseek")
	if !ok || ds.BaseURL != "https://api.deepseek.example.com" || ds.APIKey != "keychain:deepseek" || !ds.User {
		t.Errorf("deepseek = %+v", ds)
	}
	if !reflect.DeepEqual(ds.Setup, config.Setup{}) {
		t.Errorf("an API kind has no setup: %+v", ds.Setup)
	}
	if _, ok := f.Find("missing"); ok {
		t.Error("Find invented a provider")
	}
	if got := f.All(); len(got) != 4 || got[0].ID != "claude" || got[3].ID != "deepseek" {
		t.Errorf("All = %+v", got)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"unknown top key":      "version: 1\ndefault: a\nproviders:\n  - {id: a, kind: claude}\nextra: 1\n",
		"unknown provider key": "version: 1\ndefault: a\nproviders:\n  - {id: a, kind: claude, base_url: x}\n",
		"unknown setup key":    "version: 1\ndefault: a\nproviders:\n  - {id: a, kind: claude, setup: {hooks: true}}\n",
		"snake case key":       "version: 1\ndefault: a\nproviders:\n  - {id: a, kind: openai, base_url: https://x.example.com, apiKey: env:K}\n",
		"empty file":           "",
		"rule broken":          "version: 1\ndefault: b\nproviders:\n  - {id: a, kind: claude}\n",
		"raw key":              "version: 1\ndefault: a\nproviders:\n  - {id: a, kind: anthropic, apiKey: sk-abc}\n",
	}
	for name, text := range cases {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, FileName), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		_, exists, err := Load(home)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !exists || !strings.Contains(err.Error(), FileName) {
			t.Errorf("%s: exists=%v, error %q does not name the file", name, exists, err)
		}
		if strings.Contains(err.Error(), "sk-abc") {
			t.Errorf("%s: error quotes the key: %v", name, err)
		}
	}
}

func TestFindDefaultsAreFreshCopies(t *testing.T) {
	f := File{Version: 1, Default: "a", Providers: []Provider{{ID: "a", Kind: "claude", Setup: &Setup{Deny: []string{"x"}}}}}
	p, _ := f.Find("a")
	p.Setup.Deny[0] = "changed"
	if f.Providers[0].Setup.Deny[0] != "x" {
		t.Error("Find shares the deny slice with the file")
	}
}
