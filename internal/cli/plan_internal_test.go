package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

func TestRemoveAgentFiles(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		"CLAUDE.md", "sub/claude.local.md", "deep/x/AGENTS.md", ".mcp.json",
		".claude/settings.json", "a/.codex/config.toml", "b/.Cursor/rules",
		".git/config", "sub/.GIT/config", "deep/x/.git/HEAD",
		"main.go", "sub/keep.txt", ".gitignore", ".gitmodules",
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeAgentFiles(root); err != nil {
		t.Fatal(err)
	}
	var got []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{".gitignore", ".gitmodules", "main.go", "sub/keep.txt"}; !slices.Equal(got, want) {
		t.Fatalf("left %q, want %q", got, want)
	}
}

func TestNextRunNamesAProductionEnvironment(t *testing.T) {
	prod := config.Environment{Kind: config.KindProduction}
	if got := nextRun(prod, "production", "", "0123456789abcdef"); got != "semar run --all --env production" {
		t.Errorf("nextRun = %q", got)
	}
	if got := nextRun(config.Environment{Kind: config.KindTest}, "uat", "", "0123456789abcdef"); got != "semar run --all" {
		t.Errorf("nextRun = %q", got)
	}
}

func TestModelUseLine(t *testing.T) {
	cases := []struct {
		name     string
		r        provider.Result
		attempts int
		setup    bool
		want     string
	}{
		{"all of it", provider.Result{Turns: 21, InputTokens: 640_000, CacheReadTokens: 590_000, OutputTokens: 18_000, CostUSD: 1.1}, 2, false,
			"model use: 2 attempts, 21 turns, 640k input (590k cached), 18k output, $1.10"},
		{"one attempt and one turn", provider.Result{Turns: 1, InputTokens: 850, OutputTokens: 6_149}, 1, false,
			"model use: 1 attempt, 1 turn, 850 input, 6.1k output"},
		{"millions", provider.Result{Turns: 40, InputTokens: 2_700_000, CacheReadTokens: 2_449_999, OutputTokens: 999_600, CostUSD: 5.994}, 3, false,
			"model use: 3 attempts, 40 turns, 2.7M input (2.4M cached), 1.0M output, $5.99"},
		{"just under a million", provider.Result{Turns: 2, InputTokens: 999_499, OutputTokens: 9_999}, 1, false,
			"model use: 1 attempt, 2 turns, 999k input, 10k output"},
		{"with the setup", provider.Result{Turns: 3, InputTokens: 1000, OutputTokens: 1}, 1, true,
			"model use: 1 attempt, 3 turns, 1.0k input, 1 output\nyour MCP servers and skills were loaded, and they add tokens to every turn; plan without --with-setup to leave them out"},
		{"no job ran", provider.Result{}, 0, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelUseLine(tc.r, tc.attempts, tc.setup); got != tc.want {
				t.Errorf("modelUseLine = %q, want %q", got, tc.want)
			}
		})
	}
}
