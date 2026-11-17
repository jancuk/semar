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

func TestInitAsksForTheWebSignInOfEachAccount(t *testing.T) {
	p := askRepo(t,
		"https://orders.example.com",
		"ORDERS_TOKEN",
		"a customer with paid orders",
		"ana@example.com",
		"ORDERS_WEB_PASSWORD",
		"support",
		"ORDERS_TOKEN_SUPPORT",
		"a support agent",
		"",
		"",
	)
	t.Setenv("ORDERS_TOKEN", "x")
	t.Setenv("ORDERS_TOKEN_SUPPORT", "y")
	res := p.must(t, cli.ExitOK, "init", "--service", "orders")
	contains(t, "stdout", res.stdout,
		"User that this account signs in with on the web, or Enter for none",
		"Environment variable of its web password",
		"export ORDERS_WEB_PASSWORD=<the web password of the account default>",
	)
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if w := cfg.AuthProfiles["default"].Web; w == nil || w.User != "ana@example.com" || w.Password != "env:ORDERS_WEB_PASSWORD" {
		t.Errorf("default web = %+v", w)
	}
	if w := cfg.AuthProfiles["support"].Web; w != nil {
		t.Errorf("support web = %+v, want none", w)
	}
}

func TestInitDoesNotAskForAWebSignInThatAFlagGave(t *testing.T) {
	p := askRepo(t, "https://orders.example.com", "", "", "")
	res := p.must(t, cli.ExitOK, "init", "--service", "orders", "--web-user", "default=ana@example.com", "--web-password-env", "default=ORDERS_WEB_PASSWORD")
	if strings.Contains(res.stdout, "signs in with on the web") {
		t.Errorf("the web sign-in was asked although the flags gave it:\n%s", res.stdout)
	}
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if w := cfg.AuthProfiles["default"].Web; w == nil || w.User != "ana@example.com" {
		t.Errorf("default web = %+v", w)
	}
}

func TestInitWebFlagsWriteTheBlockAndCompareWithTheFile(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	args := append(slicesClone(initBase), "--account", "bob=ORDERS_TOKEN_BOB",
		"--web-user", "default=ana@example.com", "--web-password-env", "default=ORDERS_WEB_PASSWORD",
		"--web-user", "bob=ben@example.com", "--web-password-env", "bob=ORDERS_WEB_PASSWORD_BOB")
	res := p.must(t, cli.ExitOK, args...)
	contains(t, "stdout", res.stdout, "export ORDERS_WEB_PASSWORD=", "export ORDERS_WEB_PASSWORD_BOB=")
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]config.WebSignIn{
		"default": {User: "ana@example.com", Password: "env:ORDERS_WEB_PASSWORD"},
		"bob":     {User: "ben@example.com", Password: "env:ORDERS_WEB_PASSWORD_BOB"},
	} {
		if w := cfg.AuthProfiles[name].Web; w == nil || *w != want {
			t.Errorf("%s web = %+v, want %+v", name, w, want)
		}
	}
	data, err := os.ReadFile(p.path(".semar", "semar.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the file", string(data), `web: {user: ana@example.com, password: "env:ORDERS_WEB_PASSWORD"}`)

	res = p.must(t, cli.ExitOK, args...)
	contains(t, "stdout", res.stdout, "already has these settings")
	res = p.must(t, cli.ExitError, append(slicesClone(initBase), "--web-user", "default=other@example.com", "--web-password-env", "default=ORDERS_WEB_PASSWORD")...)
	contains(t, "stderr", res.stderr, "--web-user", "default")
	res = p.must(t, cli.ExitError, append(slicesClone(initBase), "--web-user", "default=ana@example.com", "--web-password-env", "default=OTHER")...)
	contains(t, "stderr", res.stderr, "--web-password-env", "default")
}

func TestInitRefusesABadWebSignIn(t *testing.T) {
	for name, c := range map[string]struct {
		flags []string
		want  string
	}{
		"a user alone":           {[]string{"--web-user", "default=ana@example.com"}, "--web-password-env"},
		"a variable alone":       {[]string{"--web-password-env", "default=ORDERS_WEB_PASSWORD"}, "--web-user"},
		"no equals sign":         {[]string{"--web-user", "ana", "--web-password-env", "default=P"}, "--web-user"},
		"an unknown account":     {[]string{"--web-user", "carol=c", "--web-password-env", "carol=P"}, "carol"},
		"a bad variable":         {[]string{"--web-user", "default=ana", "--web-password-env", "default=1X"}, "--web-password-env"},
		"a user with a newline":  {[]string{"--web-user", "default=a\nb", "--web-password-env", "default=P"}, "--web-user"},
		"the same account twice": {[]string{"--web-user", "default=a", "--web-user", "default=b", "--web-password-env", "default=P"}, "more than once"},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.opts.Providers = noProvider(t)
			res := p.must(t, cli.ExitError, append(slicesClone(initBase), c.flags...)...)
			contains(t, "stderr", res.stderr, c.want)
			if _, err := os.Stat(p.path(".semar", "semar.yaml")); err == nil {
				t.Error("a file was written")
			}
		})
	}
}

func TestInitCheckMissesAWebPasswordThatIsNotSet(t *testing.T) {
	srv, _ := versionServer(t, 200, `{"commit":"4f2a91c0d3"}`)
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.HTTP = srv.Client()
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(claude.Kind, func(cfg config.Provider) (provider.Instance, error) { return fake.New(cfg, root) })
		return r
	}
	os.Unsetenv("ORDERS_WEB_PASSWORD")
	p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders",
		"--web-user", "default=ana@example.com", "--web-password-env", "default=ORDERS_WEB_PASSWORD")
	res := p.must(t, cli.ExitUndecided, "init")
	contains(t, "stdout", res.stdout, `  missing  ORDERS_WEB_PASSWORD is not set: export it with the web password of the account "default"`+"\n")

	t.Setenv("ORDERS_WEB_PASSWORD", "x")
	res = p.must(t, cli.ExitOK, "init")
	contains(t, "stdout", res.stdout, "  ok       ORDERS_WEB_PASSWORD is set\n")
}
