//go:build unix

package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/userconfig"
)

// A variable that semar.yaml names as a secret never reaches a harness that runs with the
// person's setup, and the rest of the person's environment does.
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
	p.must(t, cli.ExitError, "plan", "--diff", "main..release")

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
	p.must(t, cli.ExitError, "plan", "--diff", "main..release")

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
