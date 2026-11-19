package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

// blockedShop is the cart page with a picture from a host that the environment does not list.
func blockedShop(t *testing.T) (r *repo, host string) {
	t.Helper()
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "x") }))
	t.Cleanup(cdn.Close)
	page := strings.Replace(shopPage, "</body>", `<img src="`+cdn.URL+`/pixel.png?token=abc123"></body>`, 1)
	r = webRepoPage(t, page)
	return r, strings.TrimPrefix(cdn.URL, "http://")
}

func TestRunNamesTheHostsTheBrowserBlockedAndWritesThemInTheReport(t *testing.T) {
	r, host := blockedShop(t)
	res := r.must(t, cli.ExitOK, "run", "--scenario", "WEB-1")
	contains(t, "stdout", res.stdout,
		"WEB-1  passed",
		"blocked hosts: the browser did not reach 1 host, because environments.local.hosts does not list them\n",
		"  "+host+": WEB-1\n",
		"can pass or fail for the wrong reason",
		"add the host under environments.local.hosts in .semar/semar.yaml, or run semar login, which offers to add them",
	)
	if strings.Count(res.stdout, host) != 1 {
		t.Errorf("the host is printed more than once:\n%s", res.stdout)
	}
	report := r.lastReport(t)
	contains(t, "the report", report, "## Blocked hosts", "- "+host+": WEB-1", "`environments.local.hosts`")
	if strings.Contains(report+res.stdout, "abc123") {
		t.Error("an output holds the query of a blocked request")
	}
}

func TestRunOfAPageWithNoBlockedHostSaysNothingOfThem(t *testing.T) {
	r := webRepo(t)
	res := r.must(t, cli.ExitOK, "run", "--scenario", "WEB-1")
	if strings.Contains(res.stdout, "blocked hosts") || strings.Contains(r.lastReport(t), "Blocked hosts") {
		t.Errorf("a run with no blocked host mentions them:\n%s", res.stdout)
	}
}
