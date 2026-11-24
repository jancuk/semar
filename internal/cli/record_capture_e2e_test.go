package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

// A flow records a capture of the greeting, a scenario starts with that flow and checks {{who}}, and the
// scenario replays as semar run does: the value comes from the screen of each run, and no file holds it.
func TestRecordAFlowWithACaptureThenReplayAScenarioThatChecksIt(t *testing.T) {
	const password = "pw-Zk93-capture-canary"
	r, signed := recordRig(t)

	// Signed out: 1 email, 2 password, 3 the button. Signed in: 1 the greeting "Hi Ana", 2 the button.
	r.stdin("screen SignIn", "type 1 ana@example.com", "secret 2 SHOP_PASSWORD", password, "tap 3",
		"screen Home", `capture 1 who Hi (\w+)`, "finish", "quit")
	res := r.must(t, cli.ExitOK, "record", "--flow", "login-who")
	contains(t, "stdout", res.stdout, `Captured who = "Ana"`, "Saved:", ".semar/flows/login-who.yaml", "the flow login-who completed")
	flowText, err := os.ReadFile(r.path(".semar", "flows", "login-who.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the flow", string(flowText), "captures:", "name: who", `pattern: Hi (\w+)`)

	// The scenario runs the flow live and then checks that the screen still greets the one that the flow
	// captured. The greeting is in the file as {{who}}.
	t.Setenv("SHOP_PASSWORD", password)
	signed.Store(true)
	r.server.SetSource(signedInScreen("Hi Ana"))
	r.stdin("screen Home", "assert 1 equals Hi {{who}}", "finish", "quit")
	res = r.must(t, cli.ExitOK, "record", "TASK-9", "--use", "login-who")
	contains(t, "stdout", res.stdout, "Saved:", "TASK-9.feature", "Replayed once: passed.")
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "TASK", "TASK-9.feature"))
	if err != nil {
		t.Fatalf("no feature: %v\n%s", err, res.stdout)
	}
	contains(t, "the scenario", string(feature), `equals "Hi {{who}}"`)
	stored, _ := filepath.Glob(r.path(".semar", "store", "scripts", "TASK-9", "*.yaml"))
	if len(stored) != 1 {
		t.Fatalf("stored scripts = %v", stored)
	}
	script, _ := os.ReadFile(stored[0])
	contains(t, "the script", string(script), "use: login-who")
	for name, text := range map[string]string{"feature": string(feature), "script": string(script), "flow": string(flowText)} {
		if strings.Contains(text, "Hi Ana") || strings.Contains(text, password) {
			t.Errorf("the %s holds a captured value or the password:\n%s", name, text)
		}
	}

	// The stored scenario replays with plain semar run: the flow captures the value again on this run.
	res = r.must(t, cli.ExitOK, "run", "--scenario", "TASK-9")
	contains(t, "stdout", res.stdout, "TASK-9  passed")
}
