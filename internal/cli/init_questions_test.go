package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

// askRepo is a repository with a terminal that answers with the lines.
func askRepo(t *testing.T, answers ...string) *planRepo {
	t.Helper()
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	p.opts.Terminal = func() bool { return true }
	p.opts.Stdin = strings.NewReader(strings.Join(answers, "\n") + "\n")
	return p
}

func TestInitAsksForWhatNoFlagGave(t *testing.T) {
	p := askRepo(t,
		"https://orders.example.com",
		"ORDERS_TOKEN",
		"a customer with paid orders",
		"support",
		"ORDERS_TOKEN_SUPPORT",
		"a support agent who can refund any order",
		"",
	)
	t.Setenv("ORDERS_TOKEN", "x")
	t.Setenv("ORDERS_TOKEN_SUPPORT", "y")
	res := p.must(t, cli.ExitOK, "init", "--service", "orders")
	contains(t, "stdout", res.stdout,
		"Address of the service in the test environment",
		"Environment variable that holds the token [SEMAR_TOKEN]",
		"Who is this account, in one line",
		"Another account? Name, or Enter for none",
		"Environment variable of its token",
		"Wrote .semar/semar.yaml. Next: semar run",
	)
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.AuthProfiles["default"]; got.Description != "a customer with paid orders" || got.Secrets["TOKEN"] != "env:ORDERS_TOKEN" {
		t.Errorf("default account = %+v", got)
	}
	if got := cfg.AuthProfiles["support"]; got.Description != "a support agent who can refund any order" || got.Secrets["TOKEN"] != "env:ORDERS_TOKEN_SUPPORT" {
		t.Errorf("support account = %+v", got)
	}
	if got := cfg.Environments["uat"].Services[0].BaseURL; got != "https://orders.example.com" {
		t.Errorf("address = %q", got)
	}
}

func TestInitTakesTheDefaultTokenVariableOnEnter(t *testing.T) {
	p := askRepo(t, "https://orders.example.com", "", "", "")
	p.must(t, cli.ExitOK, "init", "--service", "orders")
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.AuthProfiles["default"]; got.Secrets["TOKEN"] != "env:SEMAR_TOKEN" || got.Description != "" {
		t.Errorf("default account = %+v", got)
	}
}

func TestInitPrintsWhatItFoundBeforeItAsks(t *testing.T) {
	p := askRepo(t, "https://orders.example.com", "", "", "")
	p.git.Commit("SHOP-481 write the documents", map[string]*string{
		"docs/openapi.yaml":        gittest.S("openapi: 3.0.0\ninfo: {title: t, version: '1'}\npaths: {}\n"),
		"docs/guide.md":            gittest.S("# guide\n"),
		"docs/tickets/SHOP-482.md": gittest.S("# SHOP-482 a\n"),
		"docs/tickets/SHOP-483.md": gittest.S("# SHOP-483 b\n"),
	})
	res := p.must(t, cli.ExitOK, "init", "--service", "orders")
	found := strings.Index(res.stdout, "Found")
	asked := strings.Index(res.stdout, "Address of the service")
	if found < 0 || asked < 0 || found > asked {
		t.Fatalf("Found must come before the first question:\n%s", res.stdout)
	}
	contains(t, "stdout", res.stdout, "docs/openapi.yaml (API document)", "docs/ (2 documents)", "docs/tickets/ (2 tickets)")
}

func TestInitDescribeFlagGivesAnAccountItsDescription(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	args := append(slicesClone(initBase), "--account", "bob=ORDERS_TOKEN_BOB",
		"--describe", "default=a customer with paid orders", "--describe", "bob=a member of the shared board")
	p.must(t, cli.ExitOK, args...)
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.AuthProfiles["default"].Description; got != "a customer with paid orders" {
		t.Errorf("default description = %q", got)
	}
	if got := cfg.AuthProfiles["bob"].Description; got != "a member of the shared board" {
		t.Errorf("bob description = %q", got)
	}
}

func TestInitRefusesABadDescribe(t *testing.T) {
	for name, describe := range map[string]string{
		"no equals sign":  "bob",
		"an empty text":   "default=",
		"an unknown name": "carol=a person",
		"too long":        "default=" + strings.Repeat("x", 201),
	} {
		t.Run(name, func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.opts.Providers = noProvider(t)
			res := p.must(t, cli.ExitError, append(slicesClone(initBase), "--describe", describe)...)
			contains(t, "stderr", res.stderr, "--describe")
			if _, err := os.Stat(p.path(".semar", "semar.yaml")); err == nil {
				t.Error("a file was written")
			}
		})
	}
}

func TestInitWithNoTerminalAsksNothingAndNamesTheMissingFlag(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	p.opts.Terminal = func() bool { return false }
	res := p.must(t, cli.ExitError, "init")
	contains(t, "stderr", res.stderr, "--base-url")
	if strings.Contains(res.stdout, "Address of the service") {
		t.Errorf("a question was asked with no terminal:\n%s", res.stdout)
	}
	if _, err := os.Stat(p.path(".semar", "semar.yaml")); err == nil {
		t.Error("a file was written")
	}
}

func TestInitWithBaseURLAsksNothingOnATerminal(t *testing.T) {
	p := askRepo(t)
	res := p.must(t, cli.ExitOK, initBase...)
	if strings.Contains(res.stdout, "Who is this account") {
		t.Errorf("a question was asked although --base-url was given:\n%s", res.stdout)
	}
}
