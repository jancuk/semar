package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
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
