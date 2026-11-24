package mobilemcp_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/mobilemcp"
)

func observeOpt(o *mobilemcp.Options, _ *fixture) {
	o.Guard = mcpkit.Guard{Observe: true, Reason: "the environment is production"}
}

func TestObserveModeRefusesEveryToolThatChangesSomething(t *testing.T) {
	f := newFixture(t, observeOpt)
	writes, reads := 0, 0
	for _, tool := range f.m.Tools() {
		res := f.call(tool.Name, `{"packageName":"com.example.shop","confirm":true}`)
		text := textOf(res)
		if tool.Write {
			writes++
			if !res.IsError || !strings.Contains(text, "production") || !strings.Contains(text, "mobile") {
				t.Errorf("%s: %q", tool.Name, text)
			}
		} else {
			reads++
			if strings.Contains(text, "production") {
				t.Errorf("%s was refused in observe mode: %q", tool.Name, text)
			}
		}
	}
	if writes != 20 || reads != 12 {
		t.Errorf("%d writes, %d reads", writes, reads)
	}
	if f.opens.Load() != 0 || len(f.dev.calls) > 40 {
		// A refused tool never opens or touches the device; only the read tools do.
		t.Logf("opens %d, calls %d", f.opens.Load(), len(f.dev.calls))
	}
	for _, c := range f.dev.calls {
		for _, w := range []string{"Touch", "Key", "TypeFocused", "LaunchApp", "TerminateApp", "InstallAPK", "UninstallApp", "ClearAppData", "OpenURL", "StartRecording", "StopRecording", "SetOrientation", "Fold", "SetLocation", "ClearLocation", "SetClipboard"} {
			if strings.HasPrefix(c, w) {
				t.Errorf("a write reached the device in observe mode: %s", c)
			}
		}
	}
}

func TestObserveModeStillReads(t *testing.T) {
	f := newFixture(t, observeOpt)
	f.ok("mobile_list_elements_on_screen", "")
	f.ok("mobile_get_screen_size", "")
	f.ok("mobile_take_screenshot", "")
	f.ok("mobile_get_device_logs", `{"recent":true}`)
	f.fails("mobile_click_on_screen_at_coordinates", `{"x":1,"y":2}`, "observe")
	f.fails("mobile_batch_commands", `{"steps":[{"name":"mobile_get_screen_size"}]}`, "observe")
}

func TestGuardOffAllowsWrites(t *testing.T) {
	f := newFixture(t, func(o *mobilemcp.Options, _ *fixture) { o.Guard = mcpkit.Guard{} })
	f.ok("mobile_click_on_screen_at_coordinates", `{"x":1,"y":2}`)
}

func TestURLGateMatrix(t *testing.T) {
	policy, err := effects.New(config.Environment{
		Hosts: []string{"shop.example.com"}, Methods: []string{"GET", "POST"}, Effects: config.Effects{Mode: "test_data"},
	})
	if err != nil {
		t.Fatal(err)
	}
	always := []string{
		"file:///sdcard/a.txt", "FILE:///x", "content://com.android.contacts/contacts", "intent://scan/#Intent;scheme=zxing;end",
		"javascript:alert(1)", "data:text/html;base64,PGgxPg==", "about:blank", "vbscript:msgbox", "Data:text/plain,hi",
	}
	broken := []string{
		"", "example.com/path", "://x", "https://exa mple.com", "https://example.com/a\nb", "https://example.com/\x00",
		"https://example.com/\x7f", "https://example.com/" + strings.Repeat("a", 2049),
	}
	custom := []string{"myapp://open/item/3", "market://details?id=com.x", "tel:+15551234", "MyApp://x"}

	type gate struct {
		name string
		g    mobilemcp.URLGate
		// httpOK is how the gate treats https://shop.example.com/a, evil is https://evil.example.org/.
		httpOK, evilOK, customOK bool
	}
	for _, c := range []gate{
		{"standalone", mobilemcp.URLGate{}, true, true, false},
		{"opt in", mobilemcp.URLGate{AllowUnsafe: true}, true, true, true},
		{"policy", mobilemcp.URLGate{Policy: policy}, true, false, false},
		{"policy and opt in", mobilemcp.URLGate{Policy: policy, AllowUnsafe: true}, true, false, true},
		{"service schemes", mobilemcp.URLGate{Schemes: []string{"myapp", "market", "tel"}}, true, true, true},
		{"one service scheme", mobilemcp.URLGate{Schemes: []string{"myapp"}}, true, true, false},
	} {
		for _, u := range always {
			g := c.g
			g.AllowUnsafe = true
			g.Schemes = []string{"file", "content", "intent", "javascript", "data", "about", "vbscript"}
			if err := g.Check(u); err == nil || !strings.Contains(err.Error(), "never") {
				t.Errorf("%s: %q: %v", c.name, u, err)
			}
			if err := c.g.Check(u); err == nil {
				t.Errorf("%s: %q was allowed", c.name, u)
			}
		}
		for _, u := range broken {
			if err := c.g.Check(u); err == nil {
				t.Errorf("%s: %q was allowed", c.name, truncate(u))
			}
		}
		check := func(u string, want bool) {
			t.Helper()
			err := c.g.Check(u)
			if (err == nil) != want {
				t.Errorf("%s: %q allowed=%v, err=%v", c.name, u, err == nil, err)
			}
		}
		check("https://shop.example.com/a", c.httpOK)
		check("HTTPS://shop.example.com/a", c.httpOK)
		check("http://shop.example.com/a", c.httpOK)
		check("https://evil.example.org/", c.evilOK)
		for _, u := range custom {
			want := c.customOK
			if c.name == "one service scheme" {
				want = strings.HasPrefix(strings.ToLower(u), "myapp:")
			}
			check(u, want)
		}
	}
}

func truncate(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

func TestURLGateMessages(t *testing.T) {
	policy, _ := effects.New(config.Environment{Hosts: []string{"shop.example.com"}, Methods: []string{"GET"}, Effects: config.Effects{Mode: "test_data"}})
	g := mobilemcp.URLGate{Policy: policy}
	err := g.Check("https://evil.example.org/reset?token=abc123")
	if err == nil || !strings.Contains(err.Error(), "host_not_allowed") || strings.Contains(err.Error(), "abc123") {
		t.Errorf("policy refusal = %v", err)
	}
	err = (mobilemcp.URLGate{}).Check("myapp://x")
	if err == nil || !strings.Contains(err.Error(), "android.schemes") || !strings.Contains(err.Error(), "--allow-unsafe-urls") || !strings.Contains(err.Error(), "SEMAR_MCP_ALLOW_UNSAFE_URLS") {
		t.Errorf("custom scheme refusal = %v", err)
	}
}

func TestAllowUnsafeFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	for _, c := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"SEMAR_MCP_ALLOW_UNSAFE_URLS": "1"}, true},
		{map[string]string{"SEMAR_MCP_ALLOW_UNSAFE_URLS": " 1 "}, true},
		{map[string]string{"SEMAR_MCP_ALLOW_UNSAFE_URLS": "0"}, false},
		{map[string]string{"SEMAR_MCP_ALLOW_UNSAFE_URLS": "true"}, false},
		{map[string]string{"SEMAR_MCP_ALLOW_UNSAFE_URLS": ""}, false},
		{map[string]string{}, false},
	} {
		if got := mobilemcp.AllowUnsafeFromEnv(env(c.env)); got != c.want {
			t.Errorf("%v = %v", c.env, got)
		}
	}
}

func TestOpenURLToolUsesTheGate(t *testing.T) {
	f := newFixture(t)
	f.fails("mobile_open_url", `{"url":"myapp://x"}`, "android.schemes")
	g := newFixture(t, func(o *mobilemcp.Options, _ *fixture) { o.URLs = mobilemcp.URLGate{Schemes: []string{"myapp"}} })
	g.ok("mobile_open_url", `{"url":"myapp://x"}`)
	if g.dev.called("OpenURL myapp://x") != 1 {
		t.Errorf("calls = %v", g.dev.calls)
	}
	g.fails("mobile_open_url", `{"url":"intent://x#Intent;end"}`, "never")
	g.fails("mobile_open_url", `{}`, "empty")
}

func TestObserveModeReadsTheClipboardButNeverSetsIt(t *testing.T) {
	f := newFixture(t, observeOpt)
	f.dev.clip = "copied text"
	if got := textOf(f.ok("mobile_clipboard", "")); !strings.Contains(got, "copied text") {
		t.Errorf("read = %s", got)
	}
	f.fails("mobile_clipboard", `{"text":"x"}`, "production")
	f.fails("mobile_clipboard", `{"text":""}`, "production")
	if f.dev.clip != "copied text" || f.dev.called("SetClipboard") != 0 {
		t.Errorf("a write reached the device in observe mode: %v", f.dev.calls)
	}
	if tool := f.tools["mobile_clipboard"]; tool.Write {
		t.Error("mobile_clipboard is a write: the guard would refuse a read")
	}
}
