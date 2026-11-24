package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

// mcpRef gives the ref of the first element of a JSON listing that matches.
func mcpRef(t *testing.T, text string, match func(id, desc, text string) bool) string {
	t.Helper()
	var list struct {
		Elements []struct {
			Ref  string `json:"ref"`
			ID   string `json:"id"`
			Desc string `json:"desc"`
			Text string `json:"text"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		t.Fatalf("the listing is not JSON: %v\n%s", err, text)
	}
	for _, e := range list.Elements {
		if match(e.ID, e.Desc, e.Text) {
			return e.Ref
		}
	}
	t.Fatalf("no such element in %s", text)
	return ""
}

func TestMCPRecordAScenarioSaveVerifyRunAndUndo(t *testing.T) {
	const password = "pw-Zk93-mcp-canary"
	t.Setenv("SHOP_PASSWORD", password)
	r, _ := recordRig(t)
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local")
	p.initialize()

	names := p.toolNames()
	for _, want := range []string{"semar_list", "semar_record_start", "semar_record_assert", "semar_record_capture", "semar_record_screen",
		"semar_record_steps", "semar_record_delete_step", "semar_record_save", "semar_record_undo", "semar_record_stop", "semar_run_scenario"} {
		if !hasTool(names, want) {
			t.Errorf("the tool %s is missing: %v", want, names)
		}
	}
	if hasTool(names, "semar_record_use_flow") {
		t.Error("semar_record_use_flow is dropped: semar_run_flow takes its place")
	}

	var transcript []string
	say := func(name string, kv ...any) string {
		t.Helper()
		args := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i].(string)] = kv[i+1]
		}
		text, isErr := p.call(name, args)
		transcript = append(transcript, text)
		if isErr {
			t.Fatalf("%s %v: %s\nstderr:\n%s", name, args, text, p.stderr)
		}
		return text
	}
	byID := func(suffix string) func(id, desc, text string) bool {
		return func(id, _, _ string) bool { return strings.HasSuffix(id, ":id/"+suffix) }
	}
	byDesc := func(want string) func(id, desc, text string) bool {
		return func(_, desc, _ string) bool { return desc == want }
	}
	// refOf lists the screen of the recording and finds the element, so that no ref is stale.
	refOf := func(match func(id, desc, text string) bool) string {
		t.Helper()
		return mcpRef(t, say("mobile_list_elements_on_screen", "format", "json"), match)
	}

	if cat := say("semar_list"); !strings.Contains(cat, "shop-app") {
		t.Errorf("semar_list: %s", cat)
	}

	say("semar_record_start", "kind", "scenario", "id", "TASK-2", "title", "Sign in as Ana")
	say("mobile_type_keys", "text", "ana@example.com", "ref", refOf(byID("email")))
	say("mobile_type_keys", "secretEnv", "SHOP_PASSWORD", "ref", refOf(byID("password")))
	say("mobile_click_on_screen_at_coordinates", "ref", refOf(byDesc("Sign in")))
	say("semar_record_screen", "name", "Home")
	say("semar_record_assert", "ref", refOf(byID("welcome")), "kind", "equals", "value", "Hi Ana")

	var saved struct {
		Files  []string `json:"files"`
		Checks int      `json:"checks"`
		Verify struct {
			Status string `json:"status"`
		} `json:"verify"`
	}
	if err := json.Unmarshal([]byte(say("semar_record_save")), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Verify.Status != "passed" || saved.Checks != 1 {
		t.Fatalf("save = %+v", saved)
	}

	featureText, err := os.ReadFile(r.path(".semar", "scenarios", "TASK", "TASK-2.feature"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the feature", string(featureText), "@surface:android", `equals "Hi Ana"`, "# baseline, by model")
	if !fileIn(r, ".semar/screens/shop-app.yaml") {
		t.Error("no screens file")
	}
	stored, _ := filepath.Glob(r.path(".semar", "store", "scripts", "TASK-2", "*.yaml"))
	if len(stored) != 1 {
		t.Fatalf("stored scripts = %v", stored)
	}
	script, _ := os.ReadFile(stored[0])
	contains(t, "the script", string(script), "secret: env:SHOP_PASSWORD", "reset: clear", "harness: mcp")

	say("semar_record_stop")
	var verdict struct {
		Outcome string `json:"outcome"`
		Report  string `json:"reportPath"`
	}
	if err := json.Unmarshal([]byte(say("semar_run_scenario", "id", "TASK-2")), &verdict); err != nil {
		t.Fatal(err)
	}
	if verdict.Outcome != "passed" || verdict.Report == "" {
		t.Errorf("verdict = %+v", verdict)
	}
	res := r.must(t, cli.ExitOK, "run", "--scenario", "TASK-2")
	contains(t, "stdout", res.stdout, "TASK-2  passed")

	// Undo removes what a save wrote, and a stop needs no discard after a save and an undo of nothing else.
	say("semar_record_start", "kind", "flow", "id", "tap-only")
	say("mobile_click_on_screen_at_coordinates", "ref", refOf(byDesc("Sign in")))
	say("semar_record_save", "verify", false)
	if !fileIn(r, ".semar/flows/tap-only.yaml") {
		t.Fatal("the flow was not written")
	}
	say("semar_record_undo")
	if fileIn(r, ".semar/flows/tap-only.yaml") {
		t.Error("undo left the flow")
	}
	say("semar_record_stop", "discard", true)

	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}

	// The password reached the device, and is in no file, no tool result, and no log.
	typed := r.server.Typed()
	if len(typed) < 2 || typed[1] != password {
		t.Errorf("typed = %q", typed)
	}
	for i, text := range transcript {
		if strings.Contains(text, password) {
			t.Fatalf("tool result %d holds the password:\n%s", i, text)
		}
	}
	if strings.Contains(p.stderr.String(), password) {
		t.Errorf("stderr holds the password:\n%s", p.stderr)
	}
	r.assertNoSecret(t, []string{password})
}

func fileIn(r *androidRig, rel string) bool {
	_, err := os.Stat(r.path(strings.Split(rel, "/")...))
	return err == nil
}

func TestMCPRecordToolsAreLeftOutOfStandaloneAndObserveRefusesThem(t *testing.T) {
	r := localRun(t, androidOptions{})
	p := startMCP(t, r.opts, "--dir", t.TempDir())
	p.initialize()
	for _, n := range p.toolNames() {
		if strings.HasPrefix(n, "semar_") {
			t.Errorf("standalone mode lists %s", n)
		}
	}
	p.finish()

	r2 := localRun(t, androidOptions{})
	p2 := startMCP(t, r2.opts, "--dir", r2.dir, "--env", "local", "--read-only")
	p2.initialize()
	if !hasTool(p2.toolNames(), "semar_record_start") {
		t.Fatal("repository mode lacks the record tools")
	}
	if text, isErr := p2.call("semar_record_start", map[string]any{"kind": "flow", "id": "x"}); !isErr || !strings.Contains(text, "--read-only") {
		t.Errorf("isError=%v, text %q", isErr, text)
	}
	if text, isErr := p2.call("semar_list", map[string]any{}); isErr {
		t.Errorf("semar_list is a read: %s", text)
	}
	p2.finish()
}
