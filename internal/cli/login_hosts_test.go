package cli_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/sampleapp"
)

// closeTheWindow stands where the person stands: it waits for the page to load and closes every page.
func closeTheWindow(tab context.Context) {
	time.Sleep(2 * time.Second)
	res, err := chromedp.CallBrowser(tab, target.GetTargets, target.GetTargetsParams{})
	if err != nil {
		return
	}
	for _, info := range res.TargetInfos {
		if info.Type == "page" {
			_, _ = chromedp.CallBrowser(tab, target.CloseTarget, target.CloseTargetParams{TargetID: info.TargetID})
		}
	}
}

// loginShop is a repository whose service answers with a page that loads a picture from each of two
// other hosts, which the environment does not list. The hosts come back sorted.
func loginShop(t *testing.T) (*repo, []string) {
	t.Helper()
	if _, err := web.FindChrome(""); err != nil {
		t.Skip("skipped: " + err.Error())
	}
	var others []string
	var page strings.Builder
	page.WriteString("<!doctype html><title>Sign in</title><p>sign in here</p>")
	for range 2 {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "GIF89a") }))
		t.Cleanup(srv.Close)
		others = append(others, strings.TrimPrefix(srv.URL, "http://"))
		fmt.Fprintf(&page, `<img src="%s/pixel.gif?token=abc123">`, srv.URL)
	}
	slices.Sort(others)
	r := newRepo(t, repoOptions{app: sampleapp.Options{}, wrap: func(next http.Handler) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, page.String()) })
		mux.Handle("/", next)
		return mux
	}})
	return r, others
}

func (r *repo) login(t *testing.T, want int, args ...string) result {
	t.Helper()
	res := r.semar(t, cli.WithLogin(t.Context(), closeTheWindow), append([]string{"login"}, args...)...)
	if res.code != want {
		t.Fatalf("semar login %v exited %d, want %d\nstdout:\n%s\nstderr:\n%s", args, res.code, want, res.stdout, res.stderr)
	}
	return res
}

func (r *repo) configText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(r.path(".semar", "semar.yaml"))
	must(t, err)
	return string(data)
}

func TestLoginWithNoServiceUsesTheOnlyServiceOfTheEnvironment(t *testing.T) {
	r, _ := loginShop(t)
	res := r.login(t, cli.ExitOK)
	contains(t, "stdout", res.stdout, "opening Chrome on the profile of support-agent in local at http://127.0.0.1:", "the window was closed: the profile of support-agent in local is saved")
}

func TestLoginWithNoServiceAndSeveralNamesThemInOneLine(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.rewriteConfig(t, func(s string) string {
		s = strings.Replace(s, `hosts: ["127.0.0.1:9"]`, `hosts: ["127.0.0.1:9", "127.0.0.1:7"]`, 1)
		return strings.Replace(s, "authProfiles:", "      - name: payments\n        baseUrl: http://127.0.0.1:7\nauthProfiles:", 1)
	})
	res := r.must(t, cli.ExitError, "login")
	if strings.Count(strings.TrimSpace(res.stderr), "\n") != 0 {
		t.Errorf("the error is more than one line:\n%s", res.stderr)
	}
	contains(t, "the error", res.stderr, "environment local has several services, orders, payments: name one, as in semar login orders")
	r.must(t, cli.ExitError, "login", "nope")
	if res := r.must(t, cli.ExitError, "login", "nope"); !strings.Contains(res.stderr, "has no service nope") {
		t.Errorf("a name that is not a service:\n%s", res.stderr)
	}
}

func TestLoginWithNoTerminalPrintsTheUnlistedHostsAndTheLinesToAddAndChangesNothing(t *testing.T) {
	r, others := loginShop(t)
	r.opts.Terminal = func() bool { return false }
	before := r.configText(t)
	res := r.login(t, cli.ExitOK, "orders")
	contains(t, "stdout", res.stdout,
		"the window asked for 2 hosts that environments.local.hosts does not list, so a run blocks them and a page that needs one can miss data:\n",
		"  1. "+others[0]+"\n  2. "+others[1]+"\n",
		".semar/semar.yaml was not changed. To allow them, add these lines to the list under environments.local.hosts in .semar/semar.yaml:\n",
		"      - "+others[0]+"\n      - "+others[1]+"\n",
	)
	if strings.Contains(res.stdout, "abc123") {
		t.Errorf("the output holds the query of a request:\n%s", res.stdout)
	}
	if after := r.configText(t); after != before {
		t.Errorf("the file changed with no terminal:\n%s", after)
	}
}

func TestLoginInATerminalAsksWhichHostsToAdd(t *testing.T) {
	for name, tc := range map[string]struct {
		answer string
		added  []int // the indexes of the hosts that land in the file
		said   string
	}{
		"all":                  {"all\n", []int{0, 1}, "added 2 hosts to environments.local.hosts in .semar/semar.yaml"},
		"none":                 {"none\n", nil, "nothing was added"},
		"an empty answer":      {"\n", nil, "nothing was added"},
		"the stream ends":      {"", nil, "nothing was added"},
		"the second":           {"2\n", []int{1}, "added 1 host to environments.local.hosts"},
		"both by number":       {"2, 1\n", []int{0, 1}, "added 2 hosts"},
		"a wrong answer first": {"7\nall\n", []int{0, 1}, "\"7\" is not all, none, or a number from 1 to 2"},
		"three wrong answers":  {"x\nq\nz\n", nil, "nothing was added"},
	} {
		t.Run(name, func(t *testing.T) {
			r, others := loginShop(t)
			r.opts.Terminal = func() bool { return true }
			r.opts.Stdin = strings.NewReader(tc.answer)
			before := r.configText(t)
			listed, err := loadConfig(t, r)
			must(t, err)
			res := r.login(t, cli.ExitOK)
			contains(t, "stdout", res.stdout, "Add them to environments.local.hosts in .semar/semar.yaml? Type all, none, or numbers such as 1,3 [none]: ", tc.said)
			if strings.Contains(res.stdout, "To allow them, add these lines") {
				t.Errorf("a terminal is told to add the lines by hand:\n%s", res.stdout)
			}
			after := r.configText(t)
			cfg, err := loadConfig(t, r)
			must(t, err)
			hosts := cfg.Environments["local"].Hosts
			want := slices.Clone(listed.Environments["local"].Hosts)
			for _, i := range tc.added {
				want = append(want, others[i])
			}
			if !slices.Equal(hosts, want) {
				t.Errorf("hosts = %v, want %v", hosts, want)
			}
			if len(tc.added) == 0 && after != before {
				t.Errorf("the file changed:\n%s", after)
			}
			rest := after
			for _, h := range selected(others, tc.added) {
				rest = strings.Replace(rest, `, "`+h+`"`, "", 1)
			}
			if rest != before {
				t.Errorf("the file changed by more than the hosts:\n%s", after)
			}
		})
	}
}

func selected(hosts []string, idx []int) []string {
	var out []string
	for _, i := range idx {
		out = append(out, hosts[i])
	}
	return out
}

func loadConfig(t *testing.T, r *repo) (config.Config, error) {
	t.Helper()
	return config.Load(r.dir)
}
