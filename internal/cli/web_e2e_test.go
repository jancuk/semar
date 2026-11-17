package cli_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/sampleapp"
)

const shopPage = `<!doctype html><html><head><title>Shop</title></head><body>
<h1>Cart</h1>
<button id="pay">Place order</button>
<p id="status">waiting</p>
<script>document.getElementById('pay').addEventListener('click', () => { document.getElementById('status').textContent = 'ordered'; });</script>
</body></html>`

const shopScreens = `apiVersion: semar/v1
kind: Screens
app: shop
screens:
  Cart:
    place-order: { role: button, name: Place order }
    status: { css: "#status" }
`

const webScenarioFile = `Feature: Placing an order

  @id:WEB-1 @ticket:SHOP-1 @surface:web @auth:support-agent
  Scenario: Placing an order shows that it was ordered
    The cart says that the order was placed

    Given Open the cart
    # step: open
    When Press Place order
    # step: pay
    Then the element status equals "ordered"
    # person
`

const webScriptFile = `apiVersion: semar/v1
kind: Script
scenario: WEB-1
authoredBy: { instance: person, model: "", harness: file }
steps:
  - step: open
    web: { service: orders, app: shop, do: open, path: "/cart" }
  - step: pay
    web: { service: orders, app: shop, do: click, screen: Cart, element: place-order }
`

func webRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := web.FindChrome(""); err != nil {
		t.Skip("skipped: " + err.Error())
	}
	r := newRepo(t, repoOptions{app: sampleapp.Options{}, wrap: func(next http.Handler) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("/cart", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, shopPage) })
		mux.Handle("/", next)
		return mux
	}})
	write := func(rel, text string) {
		p := r.path(filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".semar/screens/shop.yaml", shopScreens)
	write(".semar/scenarios/WEB-1.feature", webScenarioFile)
	write("scripts/WEB-1.yaml", webScriptFile)
	r.must(t, cli.ExitOK, "script", "add", r.path("scripts", "WEB-1.yaml"))
	return r
}

func (r *repo) edit(t *testing.T, rel string, change func(string) string) {
	t.Helper()
	p := r.path(filepath.FromSlash(rel))
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(change(string(data))), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWebScenarioNeedsApprovedScreensThenPasses(t *testing.T) {
	r := webRepo(t)
	res := r.must(t, cli.ExitUndecided, "run", "--scenario", "WEB-1")
	contains(t, "stdout", res.stdout, "needs review")
	report := r.lastReport(t)
	contains(t, "the report", report, "the screens of shop are not approved: semar review shop")

	shown := r.must(t, cli.ExitOK, "confirm", "--screens", "shop", "--yes")
	contains(t, "the screens", shown.stdout, "Cart", "place-order: { role: button, name: \"Place order\" }", "never approved before", "approved the screens of shop")
	again := r.must(t, cli.ExitOK, "confirm", "--screens", "shop", "--yes")
	contains(t, "second approval", again.stdout, "already approved")

	res = r.must(t, cli.ExitOK, "run", "--scenario", "WEB-1")
	contains(t, "stdout", res.stdout, "WEB-1  passed")
	report = r.lastReport(t)
	contains(t, "the report", report, "## Browser steps", "Press Place order (click Cart)", "[Screenshot](screenshots/WEB-1/")
	runDir := r.latestRun(t)
	if _, err := os.Stat(filepath.Join(runDir, "screenshots", "WEB-1")); err != nil {
		t.Errorf("no screenshots in the run: %v", err)
	}
}

func TestChangedScreensNeedApprovalAgain(t *testing.T) {
	r := webRepo(t)
	r.must(t, cli.ExitOK, "confirm", "--screens", "shop", "--yes")
	r.must(t, cli.ExitOK, "run", "--scenario", "WEB-1")
	r.edit(t, ".semar/screens/shop.yaml", func(s string) string { return strings.Replace(s, "name: Place order", "name: Pay", 1) })
	r.must(t, cli.ExitUndecided, "run", "--scenario", "WEB-1")
	contains(t, "the report", r.lastReport(t), "the screens of shop are not approved")
	shown := r.must(t, cli.ExitOK, "confirm", "--screens", "shop", "--yes")
	contains(t, "the change", shown.stdout, "changed Cart.place-order: the element changed: check it is the same thing")
	// The element is not on the page any more, so the leg is inconclusive and never failed.
	r.must(t, cli.ExitUndecided, "run", "--scenario", "WEB-1")
	contains(t, "the report", r.lastReport(t), "an element was not found: either the product changed or the script is out of date")
}

func TestScreensRepairProposesAndApplies(t *testing.T) {
	r := webRepo(t)
	r.edit(t, ".semar/screens/shop.yaml", func(s string) string {
		return "# the shop screens\n" + strings.Replace(s, "role: button, name: Place order", "role: link, name: Place order", 1)
	})
	r.must(t, cli.ExitOK, "confirm", "--screens", "shop", "--yes")
	r.must(t, cli.ExitUndecided, "run", "--scenario", "WEB-1")

	res := r.must(t, cli.ExitOK, "screens", "repair", "latest")
	contains(t, "the proposal", res.stdout,
		"shop Cart.place-order was not found",
		`old: { role: link, name: "Place order" }`,
		`1. { role: button, name: "Place order" }`,
		"the same name",
	)
	if strings.Contains(res.stdout, "Cart.status") {
		t.Errorf("an element that was not looked for is listed:\n%s", res.stdout)
	}

	r.must(t, cli.ExitError, "screens", "repair", "latest", "--apply", "Cart.place-order=2")
	r.must(t, cli.ExitError, "screens", "repair", "latest", "--apply", "Cart.status=1")
	r.must(t, cli.ExitError, "screens", "repair", "latest", "--apply", "nonsense")

	res = r.must(t, cli.ExitOK, "screens", "repair", "latest", "--apply", "Cart.place-order=1")
	contains(t, "the repair", res.stdout, "now: { role: button, name: \"Place order\" }", "semar review shop")
	data, err := os.ReadFile(r.path(".semar", "screens", "shop.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# the shop screens\n" + shopScreens
	if string(data) != want {
		t.Errorf("the file after the repair:\n%s\nwant\n%s", data, want)
	}

	// The repair never touches a scenario or a script.
	scenario, _ := os.ReadFile(r.path(".semar", "scenarios", "WEB-1.feature"))
	if string(scenario) != webScenarioFile {
		t.Error("a repair changed the scenario")
	}

	// Its hash is the one of the file that the first run was refused for, and the file is new to the approvals.
	r.must(t, cli.ExitUndecided, "run", "--scenario", "WEB-1")
	shown := r.must(t, cli.ExitOK, "confirm", "--screens", "shop", "--yes")
	contains(t, "the change", shown.stdout, "changed Cart.place-order: the element changed: check it is the same thing")
	r.must(t, cli.ExitOK, "run", "--scenario", "WEB-1")
}

func TestScreensRepairOfARunWithNoMiss(t *testing.T) {
	r := webRepo(t)
	r.must(t, cli.ExitOK, "confirm", "--screens", "shop", "--yes")
	r.must(t, cli.ExitOK, "run", "--scenario", "WEB-1")
	res := r.must(t, cli.ExitOK, "screens", "repair", "latest")
	contains(t, "the answer", res.stdout, "nothing to repair")
}

func TestConfirmScreensRefusesAMissingFile(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	res := r.must(t, cli.ExitError, "confirm", "--screens", "nope", "--yes")
	contains(t, "the error", res.stderr, ".semar/screens/nope.yaml")
}

func TestLoginWithoutChromeSaysSo(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "    effects:", "    web: {chrome: /does/not/exist}\n    effects:", 1)
	})
	res := r.must(t, cli.ExitError, "login", "orders", "--env", "local")
	contains(t, "the error", res.stderr, "Chrome was not found at the path of web.chrome, /does/not/exist")
	res = r.must(t, cli.ExitError, "login", "shop", "--env", "local")
	contains(t, "the error", res.stderr, "has no service shop")
}

func TestNoChromeBlocksAWebScenario(t *testing.T) {
	r := newRepo(t, repoOptions{})
	for rel, text := range map[string]string{
		".semar/screens/shop.yaml":       shopScreens,
		".semar/scenarios/WEB-1.feature": webScenarioFile,
		"scripts/WEB-1.yaml":             webScriptFile,
	} {
		p := r.path(filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "    effects:", "    web: {chrome: /does/not/exist}\n    effects:", 1)
	})
	r.must(t, cli.ExitOK, "script", "add", r.path("scripts", "WEB-1.yaml"))
	r.must(t, cli.ExitOK, "confirm", "--screens", "shop", "--yes")
	r.must(t, cli.ExitUndecided, "run", "--scenario", "WEB-1")
	contains(t, "the report", r.lastReport(t), "blocked", "Chrome was not found")
}
