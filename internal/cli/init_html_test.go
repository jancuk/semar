package cli_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/claude"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/sampleapp"
)

// pageServer answers GET / with contentType, and every other path with 404. It records each request.
func pageServer(t *testing.T, contentType string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte("<html><body>Dashboard</body></html>"))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// noTokenRepo is a repository whose environment has no SEMAR_TOKEN, with a client that reaches the
// servers of the test.
func noTokenRepo(t *testing.T) *planRepo {
	t.Helper()
	p := newPlanRepo(t, sampleapp.Options{})
	t.Setenv("SEMAR_TOKEN", "")
	os.Unsetenv("SEMAR_TOKEN")
	p.opts.Providers = noProvider(t)
	p.opts.HTTP = http.DefaultClient
	return p
}

func TestInitWritesAnAccountWithNoTokenForAWebAddress(t *testing.T) {
	srv, seen := pageServer(t, "text/html; charset=utf-8")
	p := noTokenRepo(t)
	res := p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders")
	contains(t, "stdout", res.stdout, "wrote ", "The address answers with a web page, so the account default has no token. To add one later, add session and secrets to authProfiles.default in .semar/semar.yaml.\n")
	if !strings.HasSuffix(res.stdout, "\nNext: semar login\n") {
		t.Errorf("init of a web address does not end with semar login as the next step:\n%s", res.stdout)
	}
	if strings.Contains(res.stdout, "export SEMAR_TOKEN") {
		t.Errorf("init asks for a token variable:\n%s", res.stdout)
	}
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.AuthProfiles["default"]; got.Session != nil || len(got.Secrets) != 0 {
		t.Errorf("default account = %+v, want no session and no secrets", got)
	}
	if got := strings.Join(seen(), ","); got != "GET /,GET /version" {
		t.Errorf("init sent %q, want one GET of the address and one of /version", got)
	}
}

func TestInitKeepsTheDescriptionAndTheWebSignInOfAnAccountWithNoToken(t *testing.T) {
	srv, _ := pageServer(t, "text/html")
	p := noTokenRepo(t)
	p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders",
		"--describe", "default=a team lead", "--web-user", "default=lead", "--web-password-env", "default=DASH_PASSWORD")
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.AuthProfiles["default"]
	if got.Session != nil || len(got.Secrets) != 0 || got.Description != "a team lead" || got.Web == nil || got.Web.User != "lead" || got.Web.PasswordEnv() != "DASH_PASSWORD" {
		t.Errorf("default account = %+v", got)
	}
}

func TestInitWritesATokenAccountWhenTheAddressIsNotAWebPage(t *testing.T) {
	srv, _ := pageServer(t, "application/json")
	p := noTokenRepo(t)
	res := p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders")
	contains(t, "stdout", res.stdout, "export SEMAR_TOKEN=")
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.AuthProfiles["default"]; got.Session == nil || got.Secrets["TOKEN"] != "env:SEMAR_TOKEN" {
		t.Errorf("default account = %+v", got)
	}
}

func TestInitAsksNothingOfAWebAddressWhenAFlagNamesTheToken(t *testing.T) {
	for name, flags := range map[string][]string{
		"token-env":   {"--token-env", "DASH_TOKEN"},
		"auth-header": {"--auth-header", "X-Token"},
		"auth-value":  {"--auth-value", "Token {{secret.TOKEN}}"},
		"account":     {"--account", "bob=DASH_TOKEN_BOB"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, seen := pageServer(t, "text/html")
			p := noTokenRepo(t)
			p.must(t, cli.ExitOK, append([]string{"init", "--base-url", srv.URL, "--service", "orders"}, flags...)...)
			cfg, err := config.Load(p.dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.AuthProfiles["default"]; got.Session == nil || len(got.Secrets) == 0 {
				t.Errorf("default account = %+v, want a token account", got)
			}
			for _, s := range seen() {
				if s == "GET /" {
					t.Errorf("init looked at the address although a flag named the token: %v", seen())
				}
			}
		})
	}
}

func TestInitWritesATokenAccountWhenTheDefaultVariableIsSet(t *testing.T) {
	srv, seen := pageServer(t, "text/html")
	p := noTokenRepo(t)
	t.Setenv("SEMAR_TOKEN", "tok")
	p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders")
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.AuthProfiles["default"]; got.Secrets["TOKEN"] != "env:SEMAR_TOKEN" {
		t.Errorf("default account = %+v", got)
	}
	if got := strings.Join(seen(), ","); got != "GET /version" {
		t.Errorf("init sent %q", got)
	}
}

func TestInitDoesNotFollowARedirectWhenItLooksAtTheAddress(t *testing.T) {
	other, otherSeen := pageServer(t, "text/html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	p := noTokenRepo(t)
	p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders")
	if got := otherSeen(); len(got) != 0 {
		t.Errorf("init followed a redirect to another host: %v", got)
	}
}

func TestInitQuestionAcceptsNoTokenForAWebAddress(t *testing.T) {
	srv, _ := pageServer(t, "text/html")
	p := askRepo(t, srv.URL, "", "a team lead", "", "")
	t.Setenv("SEMAR_TOKEN", "")
	os.Unsetenv("SEMAR_TOKEN")
	p.opts.HTTP = http.DefaultClient
	res := p.must(t, cli.ExitOK, "init", "--service", "orders")
	contains(t, "stdout", res.stdout, "Environment variable that holds the token, or Enter for none: ", "the account default has no token")
	if !strings.HasSuffix(res.stdout, "\nNext: semar login\n") || strings.Contains(res.stdout, "semar run") {
		t.Errorf("init of a web address does not end with semar login as the next step:\n%s", res.stdout)
	}
	if strings.Contains(res.stdout, "export SEMAR_TOKEN") {
		t.Errorf("init asks for a token variable:\n%s", res.stdout)
	}
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.AuthProfiles["default"]; got.Session != nil || len(got.Secrets) != 0 || got.Description != "a team lead" {
		t.Errorf("default account = %+v", got)
	}
}

func TestInitQuestionTakesTheTokenVariableWhenTheAddressIsNotAWebPage(t *testing.T) {
	srv, _ := pageServer(t, "application/json")
	p := askRepo(t, srv.URL, "", "", "", "")
	t.Setenv("SEMAR_TOKEN", "")
	os.Unsetenv("SEMAR_TOKEN")
	p.opts.HTTP = http.DefaultClient
	res := p.must(t, cli.ExitOK, "init", "--service", "orders")
	contains(t, "stdout", res.stdout, "Environment variable that holds the token [SEMAR_TOKEN]: ")
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.AuthProfiles["default"]; got.Secrets["TOKEN"] != "env:SEMAR_TOKEN" {
		t.Errorf("default account = %+v", got)
	}
}

func TestInitSaysTheHeadCommitIsTheBuildOfALocalService(t *testing.T) {
	srv, _ := pageServer(t, "text/html")
	p := noTokenRepo(t)
	res := p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders")
	contains(t, "stdout", res.stdout, "No build probe was found at "+srv.URL+"/version. The service is on this machine, so the head commit of the repository is its build.")
	if strings.Contains(res.stdout, "semar run --build") {
		t.Errorf("init asks a local service for --build:\n%s", res.stdout)
	}
}

func TestInitCheckSaysTheHeadCommitIsTheBuildOfALocalService(t *testing.T) {
	srv, _ := pageServer(t, "text/html")
	p := noTokenRepo(t)
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(claude.Kind, func(cfg config.Provider) (provider.Instance, error) { return fake.New(cfg, root) })
		return r
	}
	p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders")
	res := p.must(t, cli.ExitOK, "init")
	contains(t, "stdout", res.stdout, "  ok       uat: orders answers; it has no build probe, so its build is the head commit of the repository\n", "Nothing is missing.")
	if strings.Contains(res.stdout, "--build") {
		t.Errorf("the check asks a local service for --build:\n%s", res.stdout)
	}
}
