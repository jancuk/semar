//go:build unix

package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/userconfig"
)

// A variable that semar.yaml names as a secret never reaches a harness that runs with the
// person's setup (--with-setup), and the rest of the person's environment does.
func TestSecretOfSemarYamlStaysOutOfTheHarness(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env")
	bin := filepath.Join(dir, "claude-fake")
	script := fmt.Sprintf("#!/bin/sh\nenv > %q\n", envFile)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.useProviders(t, "  - id: planner\n    kind: claude\n", "")
	p.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "secrets: {TOKEN:", "secrets: {DEPLOY: \"env:ACME_DEPLOY_TOKEN\", TOKEN:", 1)
	})
	p.opts.LookupEnv = func(k string) (string, bool) {
		if k == "SEMAR_CLAUDE_BIN" {
			return bin, true
		}
		return os.LookupEnv(k)
	}
	p.opts.Environ = func() []string {
		return []string{"PATH=/usr/bin:/bin", "ACME_DEPLOY_TOKEN=s3cret-deploy", "ACME_REGION=eu", "ANTHROPIC_API_KEY=sk-vendor"}
	}
	p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--with-setup")

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("the harness did not start: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "ACME_REGION=eu") {
		t.Errorf("the person's environment did not reach the harness:\n%s", got)
	}
	for _, secret := range []string{"ACME_DEPLOY_TOKEN", "s3cret-deploy", "ANTHROPIC_API_KEY"} {
		if strings.Contains(got, secret) {
			t.Errorf("the harness inherited %s", secret)
		}
	}
}

// A variable that the person's own providers.yaml names as a key never reaches a harness
// either, whichever provider of the file names it.
func TestKeyOfTheProvidersFileStaysOutOfTheHarness(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env")
	bin := filepath.Join(dir, "claude-fake")
	script := fmt.Sprintf("#!/bin/sh\nenv > %q\n", envFile)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.yourFile(t, userconfig.File{Version: 1, Default: "planner", Providers: []userconfig.Provider{
		{ID: "planner", Kind: "claude"},
		{ID: "acme-chat", Kind: "openai", BaseURL: "https://llm.example.com/v1", APIKey: "env:ACME_LLM_KEY"},
	}})
	p.opts.LookupEnv = func(k string) (string, bool) {
		if k == "SEMAR_CLAUDE_BIN" {
			return bin, true
		}
		return os.LookupEnv(k)
	}
	p.opts.Environ = func() []string {
		return []string{"PATH=/usr/bin:/bin", "ACME_LLM_KEY=sk-acme-llm", "ACME_REGION=eu", "HF_TOKEN=hf-x", "TOGETHER_API_KEY=t-x"}
	}
	p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--with-setup")

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("the harness did not start: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "ACME_REGION=eu") {
		t.Errorf("the person's environment did not reach the harness:\n%s", got)
	}
	for _, secret := range []string{"ACME_LLM_KEY", "sk-acme-llm", "HF_TOKEN", "TOGETHER_API_KEY"} {
		if strings.Contains(got, secret) {
			t.Errorf("the harness inherited %s", secret)
		}
	}
}

// A plan loads neither MCP servers nor skills unless the person asks, in the flag or in the
// person's own providers file, and the arguments of the real driver show it.
func TestPlanJobFlagsFollowTheOptIn(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "claude-fake")
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do printf '%%s\\n' \"$a\"; done > %q\n", argsFile)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	yours := func(withSetup bool) userconfig.File {
		f := userconfig.File{Version: 1, Default: "planner", Providers: []userconfig.Provider{{ID: "planner", Kind: "claude"}}}
		f.Plan.WithSetup = withSetup
		return f
	}
	cases := []struct {
		name  string
		file  userconfig.File
		args  []string
		setup bool
	}{
		{"nothing asks for the setup", yours(false), nil, false},
		{"the flag asks", yours(false), []string{"--with-setup"}, true},
		{"your file asks", yours(true), nil, true},
		{"the flag turns your file off", yours(true), []string{"--with-setup=false"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			os.Remove(argsFile)
			p := newPlanRepo(t, sampleapp.Options{})
			p.configure(t, goodDraft)
			p.yourFile(t, tc.file)
			p.opts.LookupEnv = func(k string) (string, bool) {
				if k == "SEMAR_CLAUDE_BIN" {
					return bin, true
				}
				return os.LookupEnv(k)
			}
			p.opts.Environ = func() []string { return []string{"PATH=/usr/bin:/bin"} }
			p.must(t, cli.ExitError, append([]string{"plan", "--diff", "main..release"}, tc.args...)...)
			data, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatalf("the harness did not start: %v", err)
			}
			args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			has := func(a string) bool { return slices.Contains(args, a) }
			i := slices.Index(args, "--setting-sources")
			if i < 0 || i+1 >= len(args) {
				t.Fatalf("no --setting-sources in %q", args)
			}
			if tc.setup {
				if has("--strict-mcp-config") || args[i+1] != "user" || !slices.Contains(args, "Read,Grep,Glob,Skill") {
					t.Errorf("the setup is not loaded: %q", args)
				}
				return
			}
			if !has("--strict-mcp-config") || args[i+1] != "" || !slices.Contains(args, "Read,Grep,Glob") || slices.Contains(args, "Read,Grep,Glob,Skill") {
				t.Errorf("the setup is loaded: %q", args)
			}
		})
	}
}
