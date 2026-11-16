package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

const exampleName = "order.paid.yaml"

func exampleText(t testing.TB) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", exampleName))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// example parses the fixture of the plan. Each call returns a fixture that no other test shares.
func example(t testing.TB) domain.Fixture {
	t.Helper()
	f, err := Parse([]byte(exampleText(t)), exampleName)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// edited is the example with one replacement of text, which must be there.
func edited(t testing.TB, old, replacement string) string {
	t.Helper()
	s := exampleText(t)
	if !strings.Contains(s, old) {
		t.Fatalf("the example has no %q", old)
	}
	return strings.Replace(s, old, replacement, 1)
}

// repoWith writes files under .semar/fixtures of a new directory and returns it.
func repoWith(t testing.TB, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".semar", "fixtures")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func hasProblem(ps []Problem, path, msg string) bool {
	for _, p := range ps {
		if p.Path == path && strings.Contains(p.Message, msg) {
			return true
		}
	}
	return false
}
