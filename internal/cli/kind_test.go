package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/sampleapp"
)

func TestRunStartLineNamesTheEnvironment(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScript(t)
	res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	contains(t, "stderr", res.stderr, "semar: running against local (test, test data)\n")
	if strings.Contains(res.stdout, "running against") {
		t.Errorf("the start line is on stdout:\n%s", res.stdout)
	}
}

func TestRunOfAProductionEnvironmentNeedsItsName(t *testing.T) {
	asProduction := func(s string) string { return strings.Replace(s, "kind: test", "kind: production", 1) }

	t.Run("refused without --env", func(t *testing.T) {
		r := newRepo(t, repoOptions{})
		r.addScript(t)
		r.rewriteConfig(t, asProduction)
		res := r.must(t, cli.ExitError, "run", "--all")
		contains(t, "stderr", res.stderr, `the environment "local" is a production environment; name it with --env local to run against it`)
		if strings.Contains(res.stderr, "running against") {
			t.Errorf("the start line was printed for a refused run:\n%s", res.stderr)
		}
		if n := r.app.Requests("", "/"); n != 0 {
			t.Errorf("%d requests reached the service", n)
		}
		if dirs := r.runDirs(t); len(dirs) != 0 {
			t.Errorf("a refused run created %v", dirs)
		}
	})

	t.Run("accepted with --env", func(t *testing.T) {
		r := newRepo(t, repoOptions{})
		r.addScript(t)
		r.rewriteConfig(t, asProduction)
		res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID, "--env", "local")
		contains(t, "stderr", res.stderr, "semar: running against local (production, test data)\n")
		run, err := os.ReadFile(filepath.Join(r.latestRun(t), "run.json"))
		if err != nil {
			t.Fatal(err)
		}
		contains(t, "run.json", string(run), `"environmentKind": "production"`)
		report, err := os.ReadFile(filepath.Join(r.latestRun(t), "report.md"))
		if err != nil {
			t.Fatal(err)
		}
		contains(t, "report.md", string(report), "- Environment: local (production)\n")
	})

	t.Run("read only mode is named in the start line", func(t *testing.T) {
		r := newRepo(t, repoOptions{})
		r.addScript(t)
		r.rewriteConfig(t, func(s string) string {
			return strings.Replace(asProduction(s), "mode: test_data", "mode: read_only", 1)
		})
		res := r.semar(t, t.Context(), "run", "--scenario", scenarioID, "--env", "local")
		contains(t, "stderr", res.stderr, "semar: running against local (production, read only)\n")
	})

	t.Run("a test environment needs no --env", func(t *testing.T) {
		r := newRepo(t, repoOptions{})
		r.addScript(t)
		r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	})
}

func TestInitKinds(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		env     string
		kind    string
		mode    string
		methods []string
		stdout  []string
	}{
		{"default kind", nil, "uat", "test", "test_data",
			[]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, []string{"the token of the test account"}},
		{"test", []string{"--kind", "test"}, "uat", "test", "test_data",
			[]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, nil},
		{"production", []string{"--kind", "production"}, "production", "production", "read_only",
			[]string{"GET", "HEAD"}, []string{"the production environment is read only; edit the file to allow more"}},
		{"production with a name", []string{"--kind", "production", "--env", "live"}, "live", "production", "read_only",
			[]string{"GET", "HEAD"}, []string{"edit the file to allow more"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.opts.Providers = noProvider(t)
			res := p.must(t, cli.ExitOK, append([]string{"init", "--base-url", "https://orders.example.com"}, tc.args...)...)
			contains(t, "stdout", res.stdout, tc.stdout...)
			cfg, err := config.Load(p.dir)
			if err != nil {
				t.Fatal(err)
			}
			env, ok := cfg.Environments[tc.env]
			if !ok || len(cfg.Environments) != 1 {
				t.Fatalf("environments = %+v, want only %q", cfg.Environments, tc.env)
			}
			if env.Kind != tc.kind || env.Effects.Mode != tc.mode || strings.Join(env.Methods, ",") != strings.Join(tc.methods, ",") {
				t.Errorf("environment = kind %q, mode %q, methods %v", env.Kind, env.Effects.Mode, env.Methods)
			}
		})
	}

	t.Run("an unknown kind", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.opts.Providers = noProvider(t)
		res := p.must(t, cli.ExitError, "init", "--base-url", "https://orders.example.com", "--kind", "staging")
		contains(t, "stderr", res.stderr, `--kind "staging" must be test or production`)
		if exists(t, p.path(".semar", "semar.yaml")) {
			t.Error("a refused init wrote semar.yaml")
		}
	})
}
