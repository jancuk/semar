package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

func loadText(t *testing.T, text string) (config.Config, error) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".semar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".semar", "semar.yaml"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.Load(root)
}

func loadFixture(t *testing.T, name string) (config.Config, error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return loadText(t, string(data))
}

func TestLoad(t *testing.T) {
	cases := []struct{ file, want string }{
		{"valid.yaml", ""},
		{"unknown-key.yaml", "semar.yaml:4"},
		{"kind-prod.yaml", "kind must be test"},
		{"no-effects-mode.yaml", "effects.mode is required: test_data or read_only"},
		{"service-off-list.yaml", "baseUrl host api.other.example.com is not in hosts"},
		{"later-plan-keys.yaml", ""},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			_, err := loadFixture(t, c.file)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error with %q, got %v", c.want, err)
			}
		})
	}
}

func TestLoadUnknownKeys(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{"top level", "version: 1\nenviroments: {}\n", []string{"semar.yaml:2", "enviroments"}},
		{"inside a known block", "version: 1\nreplay:\n  worker: 2\n", []string{"semar.yaml:3", "worker"}},
		{"inside an environment", "version: 1\nenvironments:\n  uat:\n    kindd: test\n", []string{"semar.yaml:4", "kindd"}},
		{"two documents", "version: 1\n---\nversion: 1\n", []string{"semar.yaml", "more than one"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadText(t, c.text)
			if err == nil {
				t.Fatal("want an error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := config.Load(t.TempDir()); err == nil || !strings.Contains(err.Error(), "semar.yaml") {
		t.Fatalf("want an error that names the file, got %v", err)
	}
}

const envHead = "version: 1\nenvironments:\n  uat:\n    kind: test\n"

func TestLoadValidation(t *testing.T) {
	ok := "    hosts: [api.uat.example.com]\n    methods: [GET]\n    effects: {mode: read_only}\n"
	svc := func(url string) string {
		return envHead + ok + "    services:\n      - {name: api, baseUrl: \"" + url + "\"}\n"
	}
	cases := []struct{ name, text, want string }{
		{"version missing", "environments: {}\n", "version must be 1"},
		{"version 2", "version: 2\n", "version must be 1"},
		{"no hosts", envHead + "    methods: [GET]\n    effects: {mode: read_only}\n", "hosts needs at least one entry"},
		{"no methods", envHead + "    hosts: [h]\n    effects: {mode: read_only}\n", "methods needs at least one entry"},
		{"bad mode", envHead + "    hosts: [h]\n    methods: [GET]\n    effects: {mode: write}\n", "effects.mode is required: test_data or read_only"},
		{"service no name", envHead + ok + "    services:\n      - {baseUrl: \"https://api.uat.example.com\"}\n", "name is required"},
		{"relative url", svc("/v1"), "baseUrl must be an absolute http or https URL"},
		{"ftp url", svc("ftp://api.uat.example.com"), "baseUrl must be an absolute http or https URL"},
		{"https default port", svc("https://api.uat.example.com:443"), ""},
		{"host case", svc("https://API.uat.example.com"), ""},
		{"port not in hosts", svc("http://api.uat.example.com:8080"), "baseUrl host api.uat.example.com:8080 is not in hosts"},
		{"loopback needs port entry", strings.Replace(svc("http://127.0.0.1:8080"), "api.uat.example.com", "127.0.0.1", 1), "baseUrl host 127.0.0.1:8080 is not in hosts"},
		{"loopback with port entry", strings.Replace(svc("http://127.0.0.1:8080"), "api.uat.example.com", "127.0.0.1:8080", 1), ""},
		{"bad trust source", "version: 1\npolicy: {trust: {confirm: [diff, invariant]}}\n", "trust.confirm: invariant is not a source"},
		{"negative workers", "version: 1\nreplay: {workers: -1}\n", "replay.workers must not be negative"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadText(t, c.text)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error with %q, got %v", c.want, err)
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	cases := []struct {
		name, text string
		confirm    []domain.Source
		workers    int
	}{
		{"nothing set", "version: 1\n", []domain.Source{domain.SourceDiff, domain.SourceBaseline}, 4},
		{"zero workers", "version: 1\nreplay: {workers: 0}\n", []domain.Source{domain.SourceDiff, domain.SourceBaseline}, 4},
		{"explicit", "version: 1\nreplay: {workers: 2}\npolicy: {trust: {confirm: [ticket]}}\n", []domain.Source{domain.SourceTicket}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadText(t, c.text)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(c.confirm, cfg.Policy.Trust.Confirm) {
				t.Fatalf("confirm = %v, want %v", cfg.Policy.Trust.Confirm, c.confirm)
			}
			if cfg.Replay.Workers != c.workers {
				t.Fatalf("workers = %d, want %d", cfg.Replay.Workers, c.workers)
			}
		})
	}
}

func TestEnvironment(t *testing.T) {
	one := "version: 1\nenvironments:\n  uat: &e\n    kind: test\n    hosts: [h]\n    methods: [GET]\n    effects: {mode: read_only}\n"
	two := one + "  qa: *e\n"
	cases := []struct {
		name, text, ask string
		wantName        string
		wantErr         []string
	}{
		{"only one by empty name", one, "", "uat", nil},
		{"by name", two, "qa", "qa", nil},
		{"several and no name", two, "", "", []string{"qa, uat"}},
		{"unknown name", two, "prod", "", []string{"prod", "qa, uat"}},
		{"unknown name with one", one, "prod", "", []string{"prod", "uat"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadText(t, c.text)
			if err != nil {
				t.Fatal(err)
			}
			name, env, err := cfg.Environment(c.ask)
			if len(c.wantErr) > 0 {
				if err == nil {
					t.Fatal("want an error")
				}
				for _, w := range c.wantErr {
					if !strings.Contains(err.Error(), w) {
						t.Fatalf("error %q lacks %q", err, w)
					}
				}
				return
			}
			if err != nil || name != c.wantName || !slices.Equal(env.Hosts, []string{"h"}) {
				t.Fatalf("got %q %+v %v", name, env, err)
			}
		})
	}
	t.Run("no environments", func(t *testing.T) {
		cfg, err := loadText(t, "version: 1\n")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := cfg.Environment(""); err == nil {
			t.Fatal("want an error")
		}
	})
}
