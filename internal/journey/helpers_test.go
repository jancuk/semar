package journey

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
)

const exampleName = "checkout.yaml"

func exampleText(t testing.TB) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", exampleName))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// example parses the journey of the plan. Each call returns a journey that no other test shares.
func example(t testing.TB) domain.Journey {
	t.Helper()
	j, err := Parse([]byte(exampleText(t)), exampleName)
	if err != nil {
		t.Fatal(err)
	}
	return j
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

// orderPaid is a catalogue of one fixture, as Load builds it from the files.
func orderPaid(id domain.FixtureID) ([]string, bool) {
	if id == "order.paid" {
		return []string{"id"}, true
	}
	return nil, false
}

// repoWith writes files under .semar/journeys of a new directory and returns it.
func repoWith(t testing.TB, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".semar", "journeys")
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

func mustNode(t testing.TB, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return &doc
}
