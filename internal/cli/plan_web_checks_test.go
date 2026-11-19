package cli_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

// formsExtra is the end of the refund page: a list whose first row goes, a notice that a button closes, a
// button that a box turns on, a field with a placeholder, a link, two elements that share a test ID, and a
// password field that holds a value which must never leave the page.
const formsExtra = `
<label>Search <input id="q" type="search" placeholder="Search orders"></label>
<a href="/refund/history">History</a>
<button id="submit" disabled>Submit</button>
<label><input type="checkbox" id="agree"> I agree</label>
<ul id="rows"><li>row one</li><li>row two</li><li>row three</li></ul>
<button id="drop">Remove first row</button>
<div data-testid="notice">Refund pending <button id="dismiss">Dismiss</button></div>
<div data-testid="twin">Alpha</div><div data-testid="twin" style="display:none">Beta</div>
<input type="password" aria-label="Pin" autocomplete="current-password" value="hunter2-secret">
<script>
document.getElementById('drop').addEventListener('click', () => document.querySelector('#rows li').remove());
document.getElementById('dismiss').addEventListener('click', () => { document.querySelector('[data-testid=notice]').style.display = 'none'; });
document.getElementById('agree').addEventListener('change', e => { document.getElementById('submit').disabled = !e.target.checked; });
</script>`

// formsTurns is a model that proves the criterion with every form of a check, and that names a locator
// which matches two elements once, and repairs that in the next turn.
func formsTurns(t *testing.T, seen *[]string) func(n int, prompt string) string {
	return func(n int, prompt string) string {
		*seen = append(*seen, prompt)
		refs := map[string]string{}
		for _, r := range refPattern.FindAllStringSubmatch(prompt, -1) {
			refs[r[2]+" "+r[3]] = r[1]
		}
		ref := func(key string) string {
			if refs[key] == "" {
				t.Fatalf("turn %d: the page list has no %s:\n%s", n, key, prompt)
			}
			return refs[key]
		}
		switch n {
		case 1:
			return fmt.Sprintf(`{"turn":{"kind":"act","actions":[
			  {"kind":"fill","ref":%q,"text":"5"},{"kind":"click","ref":%q},{"kind":"click","ref":%q},
			  {"kind":"absent","ref":%q},{"kind":"click","ref":%q}]}}`,
				ref("textbox Amount"), ref("button Refund"), ref("button Remove first row"), ref("text row one"), ref("button Dismiss"))
		case 2:
			return fmt.Sprintf(`{"turn":{"kind":"act","actions":[
			  {"kind":"check","ref":%q,"expect":"5.00"},{"kind":"state","ref":%q,"state":"hidden"},{"kind":"count","ref":%q,"count":2},
			  {"kind":"state","ref":%q,"state":"disabled"},
			  {"kind":"attribute","ref":%q,"attribute":"placeholder","match":"equals","expect":"Search orders"}]}}`,
				ref("text 5.00"), ref("generic Refund pending Dismiss"), ref("text row two"), ref("button Submit"), ref("textbox Search"))
		case 3:
			return fmt.Sprintf(`{"turn":{"kind":"act","actions":[{"kind":"state","ref":%q,"state":"enabled"}]}}`, ref("text Alpha"))
		case 4:
			return fmt.Sprintf(`{"turn":{"kind":"act","actions":[
			  {"kind":"tick","ref":%q},{"kind":"state","ref":%q,"state":"checked"},{"kind":"state","ref":%q,"state":"enabled"},
			  {"kind":"page","page":"path","match":"equals","expect":"/refund"},{"kind":"page","page":"title","match":"equals","expect":"Refund"}]}}`,
				ref("checkbox I agree"), ref("checkbox I agree"), ref("button Submit"))
		}
		return `{"turn":{"kind":"done","title":"A refund page shows its state","intent":"Before: no state. Now: the page shows its state."}}`
	}
}

func TestPlanDraftsEveryFormOfAWebCheckAndRepairsAnElementThatTheLocatorDoesNotPickOut(t *testing.T) {
	var prompts []string
	m := &webModel{}
	m.script = formsTurns(t, &prompts)
	p, res := planWithWebPage(t, m, formsExtra)
	if res.code != cli.ExitOK {
		t.Fatalf("plan exited %d\nstdout:\n%s\nstderr:\n%s", res.code, res.stdout, res.stderr)
	}
	contains(t, "stdout", res.stdout, "web: 1 criterion explored, 1 test written, 0 left for a person, 5 turns")
	if strings.Contains(res.stdout, "fails on this build") || strings.Contains(res.stdout, "fails now") {
		t.Errorf("a check of the test fails now:\n%s", res.stdout)
	}

	// The locator of a twin matches two elements, so the first answer that names it is refused before
	// anything is written, and the next turn is a turn like any other.
	if len(prompts) != 5 {
		t.Fatalf("the model played %d turns, want 5", len(prompts))
	}
	contains(t, "the prompt of turn 4", prompts[3], "the locator matches 2 elements", "does not name one element", "could not be done")

	feature, err := os.ReadFile(p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-01.feature"))
	must(t, err)
	text := string(feature)
	forms := []string{
		"does not exist",
		"is hidden",
		"has 2 items",
		"is disabled",
		`attribute placeholder equals "Search orders"`,
		"is checked",
		"is enabled",
		`the page path equals "/refund"`,
		`the page title equals "Refund"`,
		`equals "5.00"`,
	}
	contains(t, "the web scenario", text, forms...)
	if strings.Contains(text, "# person") || strings.Contains(text, "Alpha") {
		t.Errorf("the scenario:\n%s", text)
	}

	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	must(t, err)
	contains(t, "plan.md", string(md), "SHOP-482-01", "Replay: passed")

	// The checks count as soon as they are written, and they pass.
	run := p.must(t, cli.ExitOK, "run", "--scenario", "SHOP-482-01", "--build", p.head(t))
	contains(t, "the run", run.stdout, "SHOP-482-01  passed")
}

// The value of a password field is on the page and in the attribute of a field. It reaches no list, no
// prompt, and no file that Semar keeps.
func TestThePasswordOfAPageNeverReachesAPromptOrAFile(t *testing.T) {
	const secret = "hunter2-secret"
	var prompts []string
	m := &webModel{}
	m.script = formsTurns(t, &prompts)
	p, res := planWithWebPage(t, m, formsExtra)
	if res.code != cli.ExitOK {
		t.Fatalf("plan exited %d\nstderr:\n%s", res.code, res.stderr)
	}
	for i, prompt := range prompts {
		if strings.Contains(prompt, secret) {
			t.Errorf("the prompt of turn %d holds the password", i+1)
		}
		if !strings.Contains(prompt, `textbox "Pin"`) && i == 0 {
			t.Errorf("the password field is not in the list:\n%s", prompt)
		}
	}
	for _, job := range m.jobs {
		if strings.Contains(job.Prompt, secret) {
			t.Error("a job of the model holds the password")
		}
	}
	for _, out := range []string{res.stdout, res.stderr} {
		if strings.Contains(out, secret) {
			t.Errorf("the output holds the password:\n%s", out)
		}
	}
	for _, root := range []string{p.path(), p.opts.Home} {
		must(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			// The profile of the browser caches the page as the application sent it, and that is the
			// application's own text and not something that Semar keeps.
			if err != nil || d.IsDir() || strings.Contains(path, string(filepath.Separator)+".git"+string(filepath.Separator)) || strings.HasPrefix(path, filepath.Join(p.opts.Home, "browser")) {
				return nil
			}
			if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), secret) {
				t.Errorf("%s holds the password", path)
			}
			return nil
		}))
	}
}

// A check of another form that the page does not meet is doubted once, and then written as a test that
// fails now, with the words of its form.
func TestPlanWritesACheckOfAnotherFormThatFailsNowAndSaysSo(t *testing.T) {
	var prompts []string
	m := &webModel{}
	m.script = func(n int, prompt string) string {
		prompts = append(prompts, prompt)
		submit := ""
		for _, r := range refPattern.FindAllStringSubmatch(prompt, -1) {
			if r[2] == "button" && r[3] == "Submit" {
				submit = r[1]
			}
		}
		if n <= 2 {
			return fmt.Sprintf(`{"turn":{"kind":"act","actions":[{"kind":"state","ref":%q,"state":"enabled"}]}}`, submit)
		}
		return `{"turn":{"kind":"done","title":"Submit is on","intent":"Before: none. Now: submit is on."}}`
	}
	p, res := planWithWebPage(t, m, formsExtra)
	if res.code != cli.ExitOK {
		t.Fatalf("plan exited %d\nstdout:\n%s\nstderr:\n%s", res.code, res.stdout, res.stderr)
	}
	contains(t, "stdout", res.stdout,
		"web: 1 criterion explored, 1 test written (1 fails on this build), 0 left for a person, 3 turns",
		"web: SHOP-482-01 fails now: the test expects the element submit is enabled, and the page shows shown, disabled, not checked",
	)
	contains(t, "the prompt of turn 2", prompts[1], "does not hold", "shown, disabled, not checked", "Nothing was recorded")
	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	must(t, err)
	contains(t, "plan.md", string(md), "Fails now: the test expects the element submit is enabled, and the page shows shown, disabled, not checked.")
}
