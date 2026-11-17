package cli_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/sampleapp"
)

type refuseTransport struct{}

func (refuseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("this test has no network")
}

// offlineHTTP is the client of a test that does not look for a build probe: every request fails.
func offlineHTTP() *http.Client { return &http.Client{Transport: refuseTransport{}} }

// versionServer answers /version with body and records every path it was asked for.
func versionServer(t *testing.T, status int, body string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func initWith(t *testing.T, baseURL string, extra ...string) (*planRepo, result) {
	t.Helper()
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	p.opts.HTTP = http.DefaultClient
	res := p.must(t, cli.ExitOK, append([]string{"init", "--base-url", baseURL, "--service", "orders"}, extra...)...)
	return p, res
}

func TestInitFindsTheBuildProbeAtVersion(t *testing.T) {
	srv, asked := versionServer(t, 200, `{"commit":"4f2a91c0d3","built":"today"}`)
	p, res := initWith(t, srv.URL)
	contains(t, "stdout", res.stdout, "Found   the build probe at "+srv.URL+"/version")
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Environments["uat"].Services[0].Build
	if b == nil || b.URL != srv.URL+"/version" || b.Commit != "$.commit" {
		t.Errorf("build = %+v", b)
	}
	if got := strings.Join(asked(), ","); got != "GET /version" {
		t.Errorf("init sent %q, want one GET /version", got)
	}
}

func TestInitWritesNoProbeWhenVersionIsNotTheCommit(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"a missing path":     {404, `{"error":"no"}`},
		"not JSON":           {200, `<html>hi</html>`},
		"no commit":          {200, `{"version":"1.2.3"}`},
		"a commit that is 1": {200, `{"commit":1}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv, asked := versionServer(t, tc.status, tc.body)
			p, res := initWith(t, srv.URL)
			contains(t, "stdout", res.stdout, "No build probe was found at "+srv.URL+"/version", "build: {probe: http")
			cfg, err := config.Load(p.dir)
			if err != nil {
				t.Fatal(err)
			}
			if b := cfg.Environments["uat"].Services[0].Build; b != nil {
				t.Errorf("build = %+v, want none", b)
			}
			if got := strings.Join(asked(), ","); got != "GET /version" {
				t.Errorf("init sent %q", got)
			}
		})
	}
}

func TestInitSendsNothingWhenTheProbeIsNamed(t *testing.T) {
	srv, asked := versionServer(t, 200, `{"commit":"abc"}`)
	p, _ := initWith(t, srv.URL, "--build-url", srv.URL+"/version", "--build-commit", "$.commit")
	if got := asked(); len(got) != 0 {
		t.Errorf("init sent %v although --build-url was given", got)
	}
	if cfg, err := config.Load(p.dir); err != nil || cfg.Environments["uat"].Services[0].Build == nil {
		t.Errorf("the named probe is missing: %v", err)
	}
}

func TestInitFollowsNoRedirectWhenItLooksForTheProbe(t *testing.T) {
	other, otherAsked := versionServer(t, 200, `{"commit":"abc"}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/version", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	p, res := initWith(t, srv.URL)
	contains(t, "stdout", res.stdout, "No build probe was found")
	if got := otherAsked(); len(got) != 0 {
		t.Errorf("init followed a redirect to another host: %v", got)
	}
	if _, err := os.Stat(p.path(".semar", "semar.yaml")); err != nil {
		t.Error(err)
	}
}

func TestInitDoesNotProbeAConfiguredRepository(t *testing.T) {
	srv, asked := versionServer(t, 200, `{"commit":"abc"}`)
	p, _ := initWith(t, srv.URL)
	n := len(asked())
	p.must(t, cli.ExitOK, "init", "--base-url", srv.URL, "--service", "orders", "--build-url", srv.URL+"/version")
	if got := len(asked()); got != n {
		t.Errorf("a second init sent %d more requests", got-n)
	}
}
