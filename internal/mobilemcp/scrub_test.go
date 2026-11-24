package mobilemcp_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mcp"
)

const leakyValue = "tok_live_9f8e7d6c5b4a"

// anyLeak says whether a value shows in any block of text of any result.
func anyLeak(results ...mcp.Result) (string, bool) {
	for _, r := range results {
		for _, c := range r.Content {
			if strings.Contains(c.Text, leakyValue) {
				return c.Text, true
			}
		}
	}
	return "", false
}

func TestASecretNeverReachesAResult(t *testing.T) {
	f := newFixture(t)
	f.scrub.Add(leakyValue)

	// In the text of an element, its description, its hint, and its id.
	tree := screenTree("leak1", loginBox, true)
	login := tree.Root.Children[0].Children[2]
	login.Text, login.Desc, login.Hint, login.ID = "Token "+leakyValue, "desc "+leakyValue, "hint "+leakyValue, "com.shop:id/"+leakyValue
	login.Attrs["text"] = login.Text
	f.dev.setTree(tree)

	var results []mcp.Result
	results = append(results,
		f.call("mobile_list_elements_on_screen", ""),
		f.call("mobile_list_elements_on_screen", `{"format":"json"}`),
		f.call("mobile_list_elements_on_screen", `{"raw":true}`),
	)

	// In a log line, the clipboard, a crash list, a crash report, and the foreground app.
	f.dev.logs = []android.LogEntry{{Time: "t", PID: 1, TID: 2, Level: "E", Tag: "Auth " + leakyValue, Message: "bearer " + leakyValue}}
	f.dev.clip = "paste " + leakyValue
	f.dev.crashes = []android.Crash{{ID: "data_app_crash@1", Tag: "data_app_crash", Time: time.Now(), Process: "p", Package: "com.shop", Summary: "boom " + leakyValue, Body: "at x " + leakyValue}}
	results = append(results,
		f.call("mobile_get_device_logs", `{"recent":true}`),
		f.call("mobile_clipboard", ""),
		f.call("mobile_list_crashes", ""),
		f.call("mobile_get_crash", `{"id":"data_app_crash@1"}`),
	)

	// In the error message of every tool that can fail with the value in it.
	leakErr := errors.New("the device said " + leakyValue)
	for _, m := range []string{"Source", "Touch", "Key", "OpenURL", "LaunchApp", "TerminateApp", "Orientation", "SetClipboard", "Logs", "GetCrash", "ScreenSize", "StartRecording", "TypeFocused"} {
		f.dev.err[m] = leakErr
	}
	f.dev.setTree(screenTree("leak2", loginBox, true))
	for name, args := range map[string]string{
		"mobile_list_elements_on_screen":             `{}`,
		"mobile_click_on_screen_at_coordinates":      `{"x":1,"y":1}`,
		"mobile_press_button":                        `{"button":"BACK"}`,
		"mobile_open_url":                            `{"url":"https://example.com"}`,
		"mobile_launch_app":                          `{"packageName":"com.shop"}`,
		"mobile_terminate_app":                       `{"packageName":"com.shop"}`,
		"mobile_get_orientation":                     `{}`,
		"mobile_clipboard":                           `{"text":"x"}`,
		"mobile_get_device_logs":                     `{"recent":true}`,
		"mobile_get_crash":                           `{"id":"data_app_crash@1"}`,
		"mobile_get_screen_size":                     `{}`,
		"mobile_start_screen_recording":              `{}`,
		"mobile_take_screenshot":                     `{}`,
		"mobile_swipe_on_screen":                     `{"direction":"up"}`,
		"mobile_type_keys":                           `{"text":"a"}`,
		"mobile_batch_commands":                      `{"steps":[{"name":"mobile_press_button","arguments":{"button":"BACK"}}]}`,
		"mobile_long_press_on_screen_at_coordinates": `{"x":1,"y":1}`,
	} {
		res := f.call(name, args)
		if !res.IsError {
			t.Errorf("%s did not fail", name)
		}
		results = append(results, res)
	}
	if text, leaked := anyLeak(results...); leaked {
		t.Errorf("a secret reached a result:\n%s", text)
	}
	// The markers are there instead.
	if !strings.Contains(textOf(results[0]), "[redacted:") {
		t.Errorf("no marker in the listing:\n%s", textOf(results[0]))
	}
}

func TestAKnownSecretFromTheRedactorIsScrubbedToo(t *testing.T) {
	f := newFixture(t)
	f.dev.setTree(textTree("k1", "bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2lnbmF0dXJl is shown"))
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(text, "eyJhbGci") {
		t.Errorf("a token pattern was not scrubbed: %s", text)
	}
	f.fails("mobile_take_screenshot", "", "secret value")
}

func TestPasswordFieldsHaveNoText(t *testing.T) {
	f := newFixture(t)
	tree := screenTree("pw", loginBox, true)
	field := tree.Root.Children[0].Children[1]
	field.Password, field.Text = true, "hunter2-typed"
	field.Attrs["text"] = "hunter2-typed"
	tree.Finish()
	f.dev.setTree(tree)
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(text, "hunter2-typed") || !strings.Contains(text, "password") {
		t.Errorf("listing:\n%s", text)
	}
}

func TestToolCallsAreSerialized(t *testing.T) {
	f := newFixture(t)
	f.dev.delay = 5 * time.Millisecond
	var wg sync.WaitGroup
	calls := []struct{ name, args string }{
		{"mobile_get_screen_size", ""}, {"mobile_get_orientation", ""}, {"mobile_list_elements_on_screen", ""},
		{"mobile_click_on_screen_at_coordinates", `{"x":1,"y":2}`}, {"mobile_press_button", `{"button":"BACK"}`},
		{"mobile_take_screenshot", `{"maxSize":64}`}, {"mobile_swipe_on_screen", `{"direction":"up"}`},
		{"mobile_batch_commands", `{"steps":[{"name":"mobile_get_orientation"},{"name":"delay","arguments":{"milliseconds":1}},{"name":"mobile_get_orientation"}]}`},
	}
	for round := 0; round < 3; round++ {
		for _, c := range calls {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.call(c.name, c.args)
			}()
		}
	}
	wg.Wait()
	if peak := f.dev.peak.Load(); peak != 1 {
		t.Errorf("%d device calls ran at once", peak)
	}
	if f.opens.Load() != 1 {
		t.Errorf("the device was opened %d times", f.opens.Load())
	}
	if len(f.dev.calls) < 24 {
		t.Errorf("only %d device calls", len(f.dev.calls))
	}
}

func TestServerEndToEnd(t *testing.T) {
	f := newFixture(t)
	srv, err := mcp.NewServer(mcp.Info{Name: "semar-test"}, f.m.Tools(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	do := func(msg string) string { return string(srv.Handle(ctx, nil, []byte(msg))) }
	list := do(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if strings.Count(list, `"name":"mobile_`) != 32 || !strings.Contains(list, `"readOnlyHint":true`) || strings.Contains(list, "oneOf") || strings.Contains(list, "additionalProperties") {
		t.Errorf("tools/list = %.300s", list)
	}
	got := do(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"mobile_get_screen_size","arguments":{}}}`)
	if !strings.Contains(got, `\"width\":1080`) || strings.Contains(got, `"isError":true`) {
		t.Errorf("call = %s", got)
	}
	got = do(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mobile_press_button","arguments":{"button":"NOPE"}}}`)
	if !strings.Contains(got, `"isError":true`) {
		t.Errorf("call = %s", got)
	}
}
