package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/claude"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/sampleapp"
)

func withoutToken(s string) string {
	i := strings.Index(s, "authProfiles:")
	return s[:i] + "authProfiles:\n  support-agent: {}\n"
}

func TestAnAccountWithNoTokenIsValid(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.rewriteConfig(t, withoutToken)
	cfg, err := config.Load(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if p := cfg.AuthProfiles["support-agent"]; p.Session != nil || len(p.Secrets) != 0 {
		t.Errorf("profile = %+v", p)
	}
}

func TestAnApiStepOfAnAccountWithNoTokenIsBlocked(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScript(t)
	r.rewriteConfig(t, withoutToken)
	t.Setenv(tokenVar, "")

	res := r.must(t, cli.ExitUndecided, "run", "--scenario", scenarioID)
	contains(t, "stdout", res.stdout, "blocked", "the account support-agent has no token")
	if strings.Contains(res.stdout+res.stderr, "failed") {
		t.Errorf("a step was reported as failed:\n%s\n%s", res.stdout, res.stderr)
	}
	if n := r.app.Requests("", "/v1"); n != 0 {
		t.Errorf("%d requests reached the service", n)
	}
}

func TestInitChecksNoVariableForAnAccountWithNoToken(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(claude.Kind, func(cfg config.Provider) (provider.Instance, error) { return fake.New(cfg, root) })
		return r
	}
	p.write(t, ".semar/semar.yaml", `version: 1
environments:
  uat:
    kind: test
    hosts: ["`+p.host+`"]
    methods: [GET]
    effects: {mode: test_data}
    services:
      - name: orders
        baseUrl: http://`+p.host+`
authProfiles:
  default: {}
`)
	t.Setenv("SEMAR_TOKEN", "")
	os.Unsetenv("SEMAR_TOKEN")
	res := p.semar(t, t.Context(), "init")
	if strings.Contains(res.stdout, "SEMAR_TOKEN") || strings.Contains(res.stdout, "is not set") {
		t.Errorf("the machine check names a variable:\n%s", res.stdout)
	}
}

func TestPlanTrialSendsNothingAsAnAccountWithNoToken(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, listDraft)
	p.rewriteConfig(t, func(s string) string {
		i, j := strings.Index(s, "authProfiles:"), strings.Index(s, "providers:")
		return s[:i] + "authProfiles:\n  default: {}\n" + s[j:]
	})
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	contains(t, "stderr", res.stderr, "semar: trial run skipped: the account default has no token\n")
	if n := p.app.Requests("", "/v1"); n != 0 {
		t.Errorf("the environment received %d requests, want none", n)
	}
}
