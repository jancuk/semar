package cli_test

import (
	"context"
	"net/http/httptest"
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

const unapprovedFixture = `apiVersion: semar/v1
kind: Fixture
id: order.paid
description: A paid order
version: 1.0.0
owner: semar
create:
  - request: { service: orders, method: POST, path: /v1/orders, json: { amount: 1000 } }
    captures: [{ name: id, jsonpath: "$.data.id" }]
outputs: [id]
cleanup:
  - request: { service: orders, method: POST, path: "/v1/orders/{{id}}/cancel" }
`

const unapprovedWebScenario = `Feature: Placing an order

  @id:WEB-1 @ticket:SHOP-482 @surface:web @auth:default
  Scenario: Placing an order shows that it was ordered
    The cart says that the order was placed

    Given Open the cart
    # step: open
    When Press Place order
    # step: pay
    Then the element status equals "ordered"
    # diff internal/orders/read.go:5: the cart says that it was ordered
`

type unhealthy struct{ provider.Instance }

func (unhealthy) Health(context.Context) provider.Health {
	return provider.Health{Detail: "the claude program is not installed"}
}

// setUpRepo is a repository that init wrote for a service that answers /version, with a second
// account whose token variable is not set.
func setUpRepo(t *testing.T) (*planRepo, *httptest.Server) {
	t.Helper()
	srv, _ := versionServer(t, 200, `{"commit":"4f2a91c0d3"}`)
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.HTTP = srv.Client()
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(claude.Kind, func(cfg config.Provider) (provider.Instance, error) { return fake.New(cfg, root) })
		return r
	}
	t.Setenv("ORDERS_TOKEN_SUPPORT", "")
	os.Unsetenv("ORDERS_TOKEN_SUPPORT")
	p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders", "--account", "support=ORDERS_TOKEN_SUPPORT")
	return p, srv
}

func TestInitInAConfiguredRepositoryChecksTheMachineAndWritesNothing(t *testing.T) {
	p, srv := setUpRepo(t)
	before, err := os.ReadFile(p.path(".semar", "semar.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	res := p.must(t, cli.ExitUndecided, "init")
	contains(t, "stdout", res.stdout,
		".semar/semar.yaml exists: checking this machine.",
		"  ok       SEMAR_TOKEN is set\n",
		`  missing  ORDERS_TOKEN_SUPPORT is not set: export it with the token of the account "support"`+"\n",
		"  ok       uat: orders answers, build 4f2a91c\n",
		"  ok       the provider claude would plan\n",
		"1 missing. Next: fix them, then semar run",
	)
	after, err := os.ReadFile(p.path(".semar", "semar.yaml"))
	if err != nil || string(after) != string(before) {
		t.Errorf("init changed the configuration: %v", err)
	}

	t.Setenv("ORDERS_TOKEN_SUPPORT", "tok-support")
	res = p.must(t, cli.ExitOK, "init")
	contains(t, "stdout", res.stdout, "  ok       ORDERS_TOKEN_SUPPORT is set\n", "Nothing is missing. Next: semar run")
	srv.Close()
	res = p.must(t, cli.ExitUndecided, "init")
	contains(t, "stdout", res.stdout, "  missing  uat: orders did not answer its build probe at "+srv.URL+"/version")
}

func TestInitCheckSaysWhichProviderCannotPlan(t *testing.T) {
	p, _ := setUpRepo(t)
	t.Setenv("ORDERS_TOKEN_SUPPORT", "tok-support")
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(claude.Kind, func(cfg config.Provider) (provider.Instance, error) {
			f, err := fake.New(cfg, root)
			return unhealthy{f}, err
		})
		return r
	}
	res := p.must(t, cli.ExitUndecided, "init")
	contains(t, "stdout", res.stdout, "  missing  the provider claude cannot plan: the claude program is not installed\n")
}

func TestInitCheckNamesChromeWhenAScenarioHasWebSteps(t *testing.T) {
	p, _ := setUpRepo(t)
	t.Setenv("ORDERS_TOKEN_SUPPORT", "tok-support")
	cfg, err := os.ReadFile(p.path(".semar", "semar.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p.write(t, ".semar/semar.yaml", strings.Replace(string(cfg), "    kind: test\n", "    kind: test\n    web:\n      chrome: /no/such/chrome\n", 1))
	res := p.must(t, cli.ExitOK, "init")
	if strings.Contains(res.stdout, "Chrome") {
		t.Errorf("Chrome is named although no scenario has a web step:\n%s", res.stdout)
	}
	p.write(t, ".semar/scenarios/SHOP-482/WEB-1.feature", unapprovedWebScenario)
	res = p.must(t, cli.ExitUndecided, "init")
	contains(t, "stdout", res.stdout, "  missing  Chrome was not found: 1 scenario has web steps\n")
}

func TestTheMachineCheckSaysNothingOfApprovals(t *testing.T) {
	p, _ := setUpRepo(t)
	t.Setenv("ORDERS_TOKEN_SUPPORT", "tok-support")
	p.write(t, ".semar/fixtures/order.paid.yaml", unapprovedFixture)
	p.write(t, ".semar/scenarios/SHOP-482/WEB-1.feature", strings.Replace(unapprovedWebScenario, "@surface:web", "@surface:api", 1))
	res := p.must(t, cli.ExitOK, "init")
	for _, word := range []string{"approved", "review"} {
		if strings.Contains(res.stdout, word) {
			t.Errorf("the machine check says %q:\n%s", word, res.stdout)
		}
	}
}

func TestInitWithFlagsStillComparesWithTheFile(t *testing.T) {
	p, srv := setUpRepo(t)
	res := p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders", "--account", "support=ORDERS_TOKEN_SUPPORT")
	contains(t, "stdout", res.stdout, "already has these settings")
}
