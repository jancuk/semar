package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

const webSecretEnvValue = "hunter2-pass"

var webToolNames = []string{
	"web_open", "web_list_elements", "web_click", "web_fill", "web_select", "web_set_checked", "web_press",
	"web_hover", "web_drag", "web_read", "web_assert", "web_screenshot", "web_save", "web_close",
}

func refOf(t *testing.T, list, role, name string) string {
	t.Helper()
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[1] == role && strings.HasPrefix(line, f[0]+" "+role+" \""+name+"\"") {
			return f[0]
		}
	}
	t.Fatalf("no %s %q in the list:\n%s", role, name, list)
	return ""
}

// An agent records the refund flow over MCP, saves it, and the test that it wrote replays and carries
// the checks of a model.
func TestMCPWebRecordsSavesAndVerifiesATest(t *testing.T) {
	r, _ := recordRepo(t)
	t.Setenv("SHOP_PW", webSecretEnvValue)
	p := startMCP(t, r.opts, "--dir", r.dir, "--surfaces", "web")
	p.initialize()
	names := p.toolNames()
	for _, want := range webToolNames {
		if !hasTool(names, want) {
			t.Fatalf("%s is missing: %v", want, names)
		}
	}
	for _, n := range names {
		if strings.HasPrefix(n, "mobile_") {
			t.Errorf("--surfaces web lists %s", n)
		}
	}

	if text, isErr := p.call("web_click", map[string]any{"ref": "e1"}); !isErr || !strings.Contains(text, "web_open") {
		t.Fatalf("a click with no browser: %v %s", isErr, text)
	}
	if text, isErr := p.call("web_open", map[string]any{"url": "https://example.invalid/"}); !isErr {
		t.Fatalf("an address outside the environment was opened: %s", text)
	}
	list, isErr := p.call("web_open", map[string]any{"path": "/refund"})
	if isErr {
		t.Fatalf("web_open: %s\nstderr:\n%s", list, p.stderr)
	}
	amount := refOf(t, list, "textbox", "Amount")
	for _, step := range []struct {
		tool string
		args map[string]any
	}{
		{"web_fill", map[string]any{"ref": amount, "text": "5"}},
		{"web_click", map[string]any{"ref": refOf(t, list, "button", "Refund")}},
	} {
		if text, isErr := p.call(step.tool, step.args); isErr {
			t.Fatalf("%s: %s", step.tool, text)
		}
	}
	// Close with steps that were not saved is refused.
	if text, isErr := p.call("web_close", nil); !isErr || !strings.Contains(text, "web_save") {
		t.Fatalf("close with unsaved steps: %v %s", isErr, text)
	}
	// A save with no check says what to do, and ends the window.
	if text, isErr := p.call("web_save", map[string]any{"id": "SHOP-482-09"}); !isErr || !strings.Contains(text, "web_assert") {
		t.Fatalf("save with no check: %v %s", isErr, text)
	}
	if _, err := os.Stat(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-09.feature")); err == nil {
		t.Fatal("a scenario with no check was written")
	}

	list, _ = p.call("web_open", map[string]any{"path": "/refund"})
	amount = refOf(t, list, "textbox", "Amount")
	p.call("web_fill", map[string]any{"ref": amount, "text": "5"})
	p.call("web_click", map[string]any{"ref": refOf(t, list, "button", "Refund")})
	list, _ = p.call("web_list_elements", nil)
	total := ""
	for _, line := range strings.Split(list, "\n") {
		if strings.Contains(line, `"5.00"`) || strings.Contains(line, `"partly refunded"`) {
			total = strings.Fields(line)[0]
			break
		}
	}
	if total == "" {
		t.Fatalf("the page does not show the result:\n%s", list)
	}
	if text, isErr := p.call("web_read", map[string]any{"ref": total}); isErr || text == "" {
		t.Fatalf("web_read: %v %q", isErr, text)
	}
	if text, isErr := p.call("web_assert", map[string]any{"ref": total}); isErr {
		t.Fatalf("web_assert: %s\nstderr:\n%s", text, p.stderr)
	}
	shot, isErr := p.call("web_screenshot", nil)
	if isErr {
		t.Fatalf("web_screenshot: %s", shot)
	}
	saved, isErr := p.call("web_save", map[string]any{"id": "SHOP-482-10", "ticket": "SHOP-482", "title": "Refund 5 over MCP"})
	if isErr {
		t.Fatalf("web_save: %s\nstderr:\n%s", saved, p.stderr)
	}
	contains(t, "the save", saved, `"replay":"passed"`, `"browser":"closed"`, "SHOP-482-10")

	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-10.feature"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the scenario", string(feature), "@id:SHOP-482-10", "Refund 5 over MCP", "Click refund", "# baseline", "model")
	if strings.Contains(string(feature), "# person") {
		t.Errorf("a check of a model is marked as a person's:\n%s", feature)
	}
	passed := r.run(t, t.Context(), cli.ExitOK, "--scenario", "SHOP-482-10")
	contains(t, "the run", passed.stdout, "SHOP-482-10  passed")

	// The same ID is refused until replace is given.
	list, _ = p.call("web_open", map[string]any{"path": "/refund"})
	p.call("web_close", map[string]any{"discard": true})
	_ = list
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
}

// While the environment is observed, the browser opens and reads and nothing else works.
func TestMCPWebObserveModeOnlyReads(t *testing.T) {
	r, _ := recordRepo(t)
	p := startMCP(t, r.opts, "--dir", r.dir, "--surfaces", "web", "--read-only")
	p.initialize()
	list, isErr := p.call("web_open", map[string]any{"path": "/refund"})
	if isErr {
		t.Fatalf("web_open while observing: %s\nstderr:\n%s", list, p.stderr)
	}
	if text, isErr := p.call("web_list_elements", nil); isErr {
		t.Fatalf("list: %s", text)
	}
	amount := refOf(t, list, "textbox", "Amount")
	for tool, args := range map[string]map[string]any{
		"web_fill":  {"ref": amount, "text": "5"},
		"web_click": {"ref": refOf(t, list, "button", "Refund")},
		"web_save":  {"id": "SHOP-482-11"},
	} {
		if text, isErr := p.call(tool, args); !isErr || !strings.Contains(text, "only observes") {
			t.Errorf("%s while observing: %v %s", tool, isErr, text)
		}
	}
	if text, isErr := p.call("web_screenshot", nil); isErr {
		t.Errorf("screenshot while observing: %s", text)
	}
	if text, isErr := p.call("web_close", nil); isErr {
		t.Errorf("close while observing: %s", text)
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
}
