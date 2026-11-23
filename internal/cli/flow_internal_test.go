package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jancuk/semar/internal/config"
)

func TestMobileBudgetGrowsWithTheRunAndNeverShrinksUnderTheDefault(t *testing.T) {
	tests := []struct {
		name                    string
		steps, retries, perStep int
		want                    int
	}{
		{"a small run keeps the default", 5, 0, 0, 200},
		{"twenty scenarios of two steps", 40, 0, 0, 320},
		{"retries replay everything", 40, 2, 0, 960},
		{"a long flow counts for every step", 40, 0, 50, 2000},
		{"a negative retry count is none", 40, -1, 0, 320},
		{"the budget has a limit", 100000, 5, 50, maxMobileBudget},
	}
	for _, tt := range tests {
		if got := mobileBudget(tt.steps, tt.retries, tt.perStep); got != tt.want {
			t.Errorf("%s: mobileBudget(%d, %d, %d) = %d, want %d", tt.name, tt.steps, tt.retries, tt.perStep, got, tt.want)
		}
	}
}

func TestLongestFlowReadsTheFlowsOfTheRepository(t *testing.T) {
	repo := t.TempDir()
	if n := longestFlow(repo); n != 0 {
		t.Errorf("no flows: %d", n)
	}
	dir := filepath.Join(repo, ".semar", "flows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "apiVersion: semar/v1\nkind: Flow\nid: login\nsurface: android\nservice: s\napp: a\nsteps:\n  - step: one\n    mobile: { do: launch }\n  - step: two\n    mobile: { do: wait }\n"
	if err := os.WriteFile(filepath.Join(dir, "login.yaml"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := longestFlow(repo); n != 2 {
		t.Errorf("longestFlow = %d, want 2", n)
	}
}

func TestSecretEnvNamesInAddsTheSecretsOfTheFlows(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".semar", "flows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "apiVersion: semar/v1\nkind: Flow\nid: login\nsurface: android\nservice: s\napp: a\nsteps:\n  - step: pw\n    mobile: { do: type, screen: S, element: e, secret: \"env:FLOW_PW\" }\n"
	if err := os.WriteFile(filepath.Join(dir, "login.yaml"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthProfiles: map[string]config.AuthProfile{"default": {Secrets: map[string]string{"TOKEN": "env:ORDERS_TOKEN"}}}}
	got := secretEnvNamesIn(cfg, repo)
	slices.Sort(got)
	if want := []string{"FLOW_PW", "ORDERS_TOKEN"}; !slices.Equal(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
	// The one that takes no repository still lists the configuration only.
	if got := secretEnvNames(cfg); !slices.Equal(got, []string{"ORDERS_TOKEN"}) {
		t.Errorf("secretEnvNames = %v", got)
	}
}
