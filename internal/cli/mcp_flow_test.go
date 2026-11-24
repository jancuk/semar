package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

const flowPassword = "pw-Zk93-mcp-flow-canary"

// savedLoginFlow records the flow login with the CLI on the record rig, and signs the app out again, so that
// a flow that runs through the tool server has something to do.
func savedLoginFlow(t *testing.T) *androidRig {
	t.Helper()
	t.Setenv("SHOP_PASSWORD", flowPassword)
	r, signed := recordRig(t)
	r.stdin("screen SignIn", "type 1 ana@example.com", "secret 2 SHOP_PASSWORD", flowPassword, "tap 3", "finish", "quit")
	r.must(t, cli.ExitOK, "record", "--flow", "login")
	if !fileIn(r, ".semar/flows/login.yaml") {
		t.Fatal("the flow login was not saved")
	}
	signed.Store(false)
	r.server.SetSource(signedOutScreen())
	r.taps.Store(0)
	return r
}

func TestMCPRunFlowRunsASavedFlowOnTheLiveDeviceWithNoModel(t *testing.T) {
	r := savedLoginFlow(t)
	typedBefore := len(r.server.Typed())
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local")
	p.initialize()
	if !hasTool(p.toolNames(), "semar_run_flow") {
		t.Fatalf("no semar_run_flow in %v", p.toolNames())
	}

	// The agent looked around first, so the device is open and the refs are those of the sign-in screen.
	listing, isErr := p.call("mobile_list_elements_on_screen", map[string]any{"format": "json"})
	if isErr {
		t.Fatal(listing)
	}
	mcpRef(t, listing, func(id, _, _ string) bool { return strings.HasSuffix(id, ":id/email") })

	text, isErr := p.call("semar_run_flow", map[string]any{"id": "login", "with": []map[string]string{}})
	if isErr {
		t.Fatalf("semar_run_flow: %s\nstderr:\n%s", text, p.stderr)
	}
	first := text[:strings.Index(text, "\n")]
	var out struct {
		Status string `json:"status"`
		Flow   struct {
			ID     string `json:"id"`
			Digest string `json:"digest"`
		} `json:"flow"`
		Steps []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(first), &out); err != nil {
		t.Fatalf("the first line is not the outcome: %v\n%s", err, text)
	}
	if out.Status != "completed" || out.Flow.ID != "login" || !strings.HasPrefix(out.Flow.Digest, "sha256:") || len(out.Steps) < 3 {
		t.Errorf("outcome = %+v", out)
	}
	for _, s := range out.Steps {
		if !s.OK {
			t.Errorf("step %s did not go well: %s", s.Name, text)
		}
	}
	// The next screen is listed, with refs that can be used at once.
	if !strings.Contains(text, "the screen now:") || !strings.Contains(text, "Hi Ana") {
		t.Errorf("no listing of the screen after the flow:\n%s", text)
	}
	if got := r.taps.Load(); got != 1 {
		t.Errorf("the fake server saw %d taps on the sign in button, want 1", got)
	}
	typed := r.server.Typed()
	if len(typed) != typedBefore+2 || typed[len(typed)-2] != "ana@example.com" || typed[len(typed)-1] != flowPassword {
		t.Errorf("typed = %q", typed)
	}
	if strings.Contains(text, flowPassword) {
		t.Errorf("the result holds the password:\n%s", text)
	}

	// The refs are those of the screen after the flow: e1 is the greeting, not the email field that e1 was.
	if !strings.Contains(text, `e1  text  "Hi Ana"`) {
		t.Errorf("the refs were not made again:\n%s", text)
	}
	var fresh string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Sign out") || strings.Contains(line, "Hi Ana") {
			fresh = strings.Fields(line)[0]
		}
	}
	if fresh == "" {
		t.Fatalf("no ref on the screen after the flow:\n%s", text)
	}
	if res, isErr := p.call("mobile_click_on_screen_at_coordinates", map[string]any{"ref": fresh}); isErr {
		t.Errorf("a fresh ref was refused: %s", res)
	}

	// The flow that does not exist is an error that says so, and the tool server goes on.
	if res, isErr := p.call("semar_run_flow", map[string]any{"id": "nope"}); !isErr || !strings.Contains(res, "nope") {
		t.Errorf("isError=%v, %s", isErr, res)
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
	if strings.Contains(p.stderr.String(), flowPassword) {
		t.Errorf("stderr holds the password:\n%s", p.stderr)
	}
	r.assertNoSecret(t, []string{flowPassword})
}

func TestMCPRunFlowWhileRecordingAScenarioWritesAUseStep(t *testing.T) {
	r := savedLoginFlow(t)
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local")
	p.initialize()
	say := func(name string, kv ...any) string {
		t.Helper()
		args := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i].(string)] = kv[i+1]
		}
		text, isErr := p.call(name, args)
		if isErr {
			t.Fatalf("%s %v: %s\nstderr:\n%s", name, args, text, p.stderr)
		}
		return text
	}
	say("semar_record_start", "kind", "scenario", "id", "TASK-9", "title", "Greets Ana after sign in", "reset", "clear")
	if res, isErr := p.call("semar_run_flow", map[string]any{"id": "login", "keepState": false}); !isErr || !strings.Contains(res, "refused while recording") {
		t.Errorf("keepState false while recording: isError=%v %s", isErr, res)
	}
	text := say("semar_run_flow", "id", "login")
	contains(t, "the result", text, "recorded step", "Hi Ana")
	if strings.Contains(text, flowPassword) {
		t.Errorf("the result holds the password:\n%s", text)
	}
	say("semar_record_screen", "name", "Home")
	listing := say("mobile_list_elements_on_screen", "format", "json")
	say("semar_record_assert", "ref", mcpRef(t, listing, func(id, _, _ string) bool { return strings.HasSuffix(id, ":id/welcome") }), "kind", "equals", "value", "Hi Ana")
	var saved struct {
		Verify struct {
			Status string `json:"status"`
		} `json:"verify"`
		Checks int `json:"checks"`
	}
	saveText := say("semar_record_save")
	if err := json.Unmarshal([]byte(saveText), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Verify.Status != "passed" {
		stored, _ := filepath.Glob(r.path(".semar", "store", "scripts", "TASK-9", "*.yaml"))
		for _, f := range stored {
			b, _ := os.ReadFile(f)
			t.Logf("script:\n%s", b)
		}
		reps, _ := filepath.Glob(r.path(".semar", "runs", "*", "report.md"))
		for _, f := range reps {
			b, _ := os.ReadFile(f)
			t.Logf("%s:\n%s", f, b)
		}
	}
	if saved.Verify.Status != "passed" || saved.Checks != 1 {
		t.Fatalf("save = %+v", saved)
	}
	say("semar_record_stop")
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}

	stored, _ := filepath.Glob(r.path(".semar", "store", "scripts", "TASK-9", "*.yaml"))
	if len(stored) != 1 {
		t.Fatalf("stored scripts = %v", stored)
	}
	script, err := os.ReadFile(stored[0])
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the script", string(script), "use: login")
	if strings.Contains(string(script), flowPassword) || strings.Contains(string(script), "ana@example.com") {
		t.Errorf("the script holds what the flow typed:\n%s", script)
	}
	res := r.must(t, cli.ExitOK, "run", "--scenario", "TASK-9")
	contains(t, "stdout", res.stdout, "TASK-9  passed")
}

func TestMCPRunFlowIsRefusedInObserveModeAndLeftOutOfStandalone(t *testing.T) {
	r := savedLoginFlow(t)
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--read-only")
	p.initialize()
	if !hasTool(p.toolNames(), "semar_run_flow") {
		t.Fatal("repository mode lacks semar_run_flow")
	}
	if text, isErr := p.call("semar_run_flow", map[string]any{"id": "login"}); !isErr || !strings.Contains(text, "--read-only") {
		t.Errorf("isError=%v, text %q", isErr, text)
	}
	if got := r.taps.Load(); got != 0 {
		t.Errorf("a refused flow tapped %d times", got)
	}
	p.finish()

	r2 := localRun(t, androidOptions{})
	p2 := startMCP(t, r2.opts, "--dir", t.TempDir())
	p2.initialize()
	if hasTool(p2.toolNames(), "semar_run_flow") {
		t.Error("standalone mode lists semar_run_flow")
	}
	p2.finish()
}
