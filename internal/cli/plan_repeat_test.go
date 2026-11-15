package cli_test

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/claude"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/sampleapp"
)

func TestPlanRepeatedWithTheSameFlags(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	outputs := p.drafts(t, goodDraft)
	// The second plan must not repeat the scenarios of the first.
	p.write(t, "drafts/renamed.json", strings.NewReplacer("Refund above the order total is rejected", "A refund of the whole total still works",
		"An order is read by its ID", "An order is read twice").Replace(goodDraft))
	var jobs, builds atomic.Int64
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(claude.Kind, func(cfg config.Provider) (provider.Instance, error) {
			cfg.Outputs = outputs
			if builds.Add(1) > 1 {
				cfg.Outputs = []string{"drafts/renamed.json"}
			}
			f, err := fake.New(cfg, root)
			return counted{f, &jobs}, err
		})
		return r
	}
	// A trailing slash on the address still agrees with the file.
	first := []string{"plan", "--diff", "main..release", "--base-url", "http://" + p.host, "--service", "orders", "--token-env", planTokenEnv}
	p.must(t, cli.ExitOK, first...)
	if jobs.Load() == 0 {
		t.Fatal("the first run did not plan")
	}
	before := jobs.Load()
	second := append(append([]string{}, first...), "--kind", "test", "--auth-header", "Authorization", "--auth-value", "Bearer {{secret.TOKEN}}")
	second[4] = "http://" + p.host + "/"
	res := p.must(t, cli.ExitOK, second...)
	contains(t, "stderr", res.stderr, "semar: .semar/semar.yaml already has these settings, so the setup flags are not needed")
	if jobs.Load() == before {
		t.Error("the repeated command did not plan")
	}
}

func TestPlanNamesEachFlagThatDiffers(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: http://"+p.host, "baseUrl: http://"+p.host+"\n        build: {probe: http, url: \"http://"+p.host+"/version\", commit: \"$.commit\"}", 1)
	})
	p.opts.Providers = noProvider(t)
	base := "http://" + p.host
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--base-url", "https://orders.staging.example.com"}, "  --base-url is https://orders.staging.example.com, and the file has " + base + " for the environment uat"},
		{[]string{"--service", "billing"}, "  --service is billing, and the file has orders for the environment uat"},
		{[]string{"--kind", "production"}, "  --kind is production, and the file has test for the environment uat"},
		{[]string{"--token-env", "OTHER"}, "  --token-env is OTHER, and the file has " + planTokenEnv + " for the auth profile default"},
		{[]string{"--auth-header", "X-Session"}, "  --auth-header is X-Session, and the file has Authorization for the auth profile default"},
		{[]string{"--auth-value", "Token {{secret.TOKEN}}"}, "  --auth-value differs from the file for the auth profile default"},
		{[]string{"--build-url", base + "/other"}, "  --build-url is " + base + "/other, and the file has " + base + "/version for the environment uat"},
		{[]string{"--build-commit", "$.sha"}, "  --build-commit is $.sha, and the file has $.commit for the environment uat"},
	} {
		res := p.must(t, cli.ExitError, append([]string{"plan", "--diff", "main..release"}, tc.args...)...)
		contains(t, "stderr", res.stderr, tc.want, "exists and differs from the flags:", "run the command without the setup flags")
		if strings.Contains(res.stderr, "{{secret.TOKEN}}") {
			t.Errorf("a secret template was printed:\n%s", res.stderr)
		}
		p.assertNothingPlanned(t)
		p.assertNoTempCopy(t)
	}
	data, err := os.ReadFile(p.path(".semar", "semar.yaml"))
	if err != nil || !strings.Contains(string(data), "baseUrl: "+base) {
		t.Errorf("the file changed: %v", err)
	}
}

func TestPlanComparesWithTheEnvironmentThatEnvNames(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "authProfiles:", "  prod:\n    kind: production\n    hosts: [\"orders.example.com\"]\n    methods: [GET]\n    effects: {mode: read_only}\n    services:\n      - name: orders\n        baseUrl: https://orders.example.com\nauthProfiles:", 1)
	})
	p.opts.Providers = noProvider(t)
	res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--env", "prod", "--base-url", "http://"+p.host)
	contains(t, "stderr", res.stderr, "  --base-url is http://"+p.host+", and the file has https://orders.example.com for the environment prod")
	res = p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--base-url", "http://"+p.host)
	contains(t, "stderr", res.stderr, "choose an environment: prod, uat")
	p.assertNothingPlanned(t)
}

func TestInitTwice(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	args := []string{"init", "--base-url", "https://orders.uat.example.com", "--service", "orders"}
	p.must(t, cli.ExitOK, args...)
	before, err := os.ReadFile(p.path(".semar", "semar.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	res := p.must(t, cli.ExitOK, append(args, "--kind", "test", "--token-env", "SEMAR_TOKEN")...)
	contains(t, "stdout", res.stdout, ".semar/semar.yaml already has these settings; nothing was written")
	res = p.must(t, cli.ExitError, "init", "--base-url", "https://orders.staging.example.com")
	contains(t, "stderr", res.stderr, ".semar/semar.yaml exists and differs from the flags:",
		"  --base-url is https://orders.staging.example.com, and the file has https://orders.uat.example.com for the environment uat",
		"To change the environment, edit .semar/semar.yaml.")
	after, _ := os.ReadFile(p.path(".semar", "semar.yaml"))
	if string(before) != string(after) {
		t.Error("init changed the file")
	}
}
