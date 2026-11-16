package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/sampleapp"
)

var initBase = []string{"init", "--base-url", "https://orders.example.com", "--service", "orders"}

func TestInitAccountsAddAuthProfiles(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	args := append(slicesClone(initBase), "--account", "bob=ORDERS_TOKEN_BOB", "--account", "carol=ORDERS_TOKEN_CAROL")
	res := p.must(t, cli.ExitOK, args...)
	contains(t, "stdout", res.stdout, "export ORDERS_TOKEN_BOB=", "export ORDERS_TOKEN_CAROL=")
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	def := cfg.AuthProfiles["default"]
	for name, env := range map[string]string{"bob": "ORDERS_TOKEN_BOB", "carol": "ORDERS_TOKEN_CAROL"} {
		got, ok := cfg.AuthProfiles[name]
		if !ok {
			t.Fatalf("no profile %s in %v", name, cfg.AuthProfiles)
		}
		if got.Session == nil || def.Session == nil || *got.Session != *def.Session {
			t.Errorf("%s session = %+v, want the default's %+v", name, got.Session, def.Session)
		}
		if len(got.Secrets) != 1 || got.Secrets["TOKEN"] != "env:"+env {
			t.Errorf("%s secrets = %v", name, got.Secrets)
		}
	}

	// The same flags again change nothing, and a different variable is a difference.
	res = p.must(t, cli.ExitOK, args...)
	contains(t, "stdout", res.stdout, "already has these settings")
	res = p.must(t, cli.ExitError, append(slicesClone(initBase), "--account", "bob=OTHER")...)
	contains(t, "stderr", res.stderr, "--account", "bob")
}

func TestInitRefusesABadAccount(t *testing.T) {
	for name, account := range map[string]string{
		"no equals sign":   "bob",
		"empty name":       "=ORDERS_TOKEN",
		"empty variable":   "bob=",
		"a bad name":       "bo b=ORDERS_TOKEN",
		"a path as a name": "../x=ORDERS_TOKEN",
		"a bad variable":   "bob=1X",
		"the default name": "default=ORDERS_TOKEN",
	} {
		t.Run(name, func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.opts.Providers = noProvider(t)
			res := p.must(t, cli.ExitError, append(slicesClone(initBase), "--account", account)...)
			contains(t, "stderr", res.stderr, "--account")
			if _, err := os.Stat(p.path(".semar", "semar.yaml")); err == nil {
				t.Error("a file was written")
			}
		})
	}
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	res := p.must(t, cli.ExitError, append(slicesClone(initBase), "--account", "bob=A", "--account", "bob=B")...)
	contains(t, "stderr", res.stderr, "bob", "more than once")
}

func TestInitWritesTheGitignoreOnce(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	p.must(t, cli.ExitOK, initBase...)
	data, err := os.ReadFile(p.path(".semar", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"runs/", "store/redact.key", "store/fixture-health/", "semar.local.yaml"} {
		if !strings.Contains("\n"+string(data), "\n"+line+"\n") {
			t.Errorf(".gitignore lacks the line %q:\n%s", line, data)
		}
	}
	for _, line := range []string{"store/confirmations", "store/approvals", "scenarios"} {
		if strings.Contains(string(data), line) {
			t.Errorf(".gitignore must not hide %q:\n%s", line, data)
		}
	}

	if err := os.WriteFile(p.path(".semar", ".gitignore"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.must(t, cli.ExitOK, initBase...)
	got, err := os.ReadFile(p.path(".semar", ".gitignore"))
	if err != nil || string(got) != "mine\n" {
		t.Errorf("an existing .gitignore was changed: %q, %v", got, err)
	}
}

func TestInitKeepsAnExistingGitignore(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	p.write(t, ".semar/.gitignore", "custom\n")
	p.must(t, cli.ExitOK, initBase...)
	got, err := os.ReadFile(p.path(".semar", ".gitignore"))
	if err != nil || string(got) != "custom\n" {
		t.Errorf(".gitignore = %q, %v", got, err)
	}
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }
