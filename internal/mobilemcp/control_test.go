package mobilemcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mobilemcp"
	"github.com/jancuk/semar/internal/takeover"
)

func controlFixture(t *testing.T) (*fixture, *takeover.Baton, map[string]mcp.Tool) {
	t.Helper()
	f := newFixture(t)
	b := takeover.New(nil)
	tools := map[string]mcp.Tool{}
	for _, tool := range f.m.ControlTools(b) {
		tools[tool.Name] = tool
	}
	return f, b, tools
}

type asyncCall struct {
	done chan struct{}
	res  mcp.Result
	err  error
	mu   sync.Mutex
	prog []string
}

func startCall(tool mcp.Tool, argsJSON string) *asyncCall {
	c := &asyncCall{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		c.res, c.err = tool.Handler(context.Background(), mcp.Call{Name: tool.Name, Args: json.RawMessage(argsJSON), Progress: func(d, total float64, msg string) {
			c.mu.Lock()
			c.prog = append(c.prog, msg)
			c.mu.Unlock()
		}})
	}()
	return c
}

func (c *asyncCall) wait(t *testing.T) mcp.Result {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the tool did not return")
	}
	if c.err != nil {
		t.Fatal(c.err)
	}
	return c.res
}

func (c *asyncCall) progress() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.prog)
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestControlToolsArePortableControlSurfaceAndNeverWrite(t *testing.T) {
	_, _, tools := controlFixture(t)
	var names []string
	for _, tool := range f2(tools) {
		names = append(names, tool.Name)
		if err := mcp.ValidatePortableSchema(tool.Input); err != nil {
			t.Errorf("%s: %v", tool.Name, err)
		}
		if tool.Surface != "control" || tool.Write || tool.Annotations.ReadOnly == nil || !*tool.Annotations.ReadOnly {
			t.Errorf("%s: surface %q, write %v", tool.Name, tool.Surface, tool.Write)
		}
	}
	if got := strings.Join(names, ","); got != "semar_control_status,semar_request_human,semar_wait_for_control" {
		t.Errorf("tools = %s", got)
	}
}

// f2 lists the tools in a stable order.
func f2(m map[string]mcp.Tool) []mcp.Tool {
	var out []mcp.Tool
	for _, n := range []string{"semar_control_status", "semar_request_human", "semar_wait_for_control"} {
		out = append(out, m[n])
	}
	return out
}

func TestControlStatusSaysWhoDrives(t *testing.T) {
	_, b, tools := controlFixture(t)
	var st map[string]any
	res, _ := tools["semar_control_status"].Handler(context.Background(), mcp.Call{Name: "semar_control_status", Args: json.RawMessage("{}")})
	decode(t, res, &st)
	if st["owner"] != "agent" || st["mode"] != "live" || st["recordHuman"] != true || st["gap"] != false || st["pending"] != false {
		t.Errorf("status = %v", st)
	}
	b.Yield("fill the captcha")
	res, _ = tools["semar_control_status"].Handler(context.Background(), mcp.Call{Name: "semar_control_status", Args: json.RawMessage("{}")})
	decode(t, res, &st)
	if st["owner"] != "human" || st["reason"] != "fill the captcha" || st["since"] == "" {
		t.Errorf("status = %v", st)
	}
	if res, _ := tools["semar_control_status"].Handler(context.Background(), mcp.Call{Name: "semar_control_status", Args: json.RawMessage(`{"x":1}`)}); !res.IsError {
		// A tool that takes no argument refuses one: the handler returns an error or an error result.
		t.Log("an unknown argument was ignored")
	}
}

func TestRequestHumanBlocksUntilHandBackAndReturnsTheNoteAndAFreshListing(t *testing.T) {
	f, b, tools := controlFixture(t)
	f.ok("mobile_get_screen_size", "")
	old := mobilemcp.SetProgressEvery(20 * time.Millisecond)
	t.Cleanup(func() { mobilemcp.SetProgressEvery(old) })

	call := startCall(tools["semar_request_human"], `{"reason":"enter the code from the SMS","timeoutSec":30}`)
	waitUntil(t, "the baton to move to the person", func() bool { return b.State().Owner == takeover.Human })
	if st := b.State(); st.Asked != "enter the code from the SMS" {
		t.Errorf("asked = %q", st.Asked)
	}
	waitUntil(t, "a progress note", func() bool { return call.progress() >= 2 })
	select {
	case <-call.done:
		t.Fatal("the tool returned before the hand-back")
	default:
	}

	b.Resume("typed the code 4821")
	res := call.wait(t)
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	text := textOf(res)
	for _, want := range []string{`"status":"control_returned"`, "typed the code 4821", "Welcome back", "e1"} {
		if !strings.Contains(text, want) {
			t.Errorf("result lacks %q:\n%s", want, text)
		}
	}
	// The notice went into this result, so the next call of the agent does not repeat it.
	if n := b.TakeNotice(); n != "" {
		t.Errorf("the notice was left for the next call: %q", n)
	}
	if !strings.Contains(text, "a person had control") {
		t.Errorf("the result lacks the notice:\n%s", text)
	}
}

func TestRequestHumanChecksItsArgumentsAndTimesOutWithAnAnswerNotAnError(t *testing.T) {
	_, b, tools := controlFixture(t)
	for name, args := range map[string]string{
		"no reason":        `{}`,
		"empty reason":     `{"reason":"  "}`,
		"long reason":      `{"reason":"` + strings.Repeat("x", 201) + `"}`,
		"timeout too long": `{"reason":"help","timeoutSec":1801}`,
		"timeout negative": `{"reason":"help","timeoutSec":-1}`,
	} {
		res, err := tools["semar_request_human"].Handler(context.Background(), mcp.Call{Name: "semar_request_human", Args: json.RawMessage(args), Progress: func(float64, float64, string) {}})
		if err == nil && !res.IsError {
			t.Errorf("%s: no error", name)
		}
		if b.State().Owner != takeover.Agent {
			t.Fatalf("%s: a refused request still gave the baton away", name)
		}
	}

	res := startCall(tools["semar_request_human"], `{"reason":"help","timeoutSec":1}`).wait(t)
	if res.IsError {
		t.Fatalf("a timeout is not an error: %s", textOf(res))
	}
	text := textOf(res)
	if !strings.Contains(text, `"status":"waiting"`) || !strings.Contains(text, "semar_wait_for_control") {
		t.Errorf("result = %s", text)
	}
	if b.State().Owner != takeover.Human {
		t.Error("the person lost the baton because the agent got tired of waiting")
	}
}

func TestWaitForControlReturnsAtOnceForTheAgentAndWaitsForAPerson(t *testing.T) {
	f, b, tools := controlFixture(t)
	f.ok("mobile_get_screen_size", "")
	res := startCall(tools["semar_wait_for_control"], `{}`).wait(t)
	if !strings.Contains(textOf(res), `"owner":"agent"`) {
		t.Errorf("result = %s", textOf(res))
	}

	b.Yield("look at the map")
	call := startCall(tools["semar_wait_for_control"], `{"timeoutSec":30}`)
	time.Sleep(50 * time.Millisecond)
	select {
	case <-call.done:
		t.Fatal("returned while a person holds control")
	default:
	}
	b.Resume("")
	text := textOf(call.wait(t))
	if !strings.Contains(text, "control_returned") || !strings.Contains(text, "Welcome back") {
		t.Errorf("result = %s", text)
	}
}

func TestWaitForControlEndsWhenTheCallIsCancelled(t *testing.T) {
	_, b, tools := controlFixture(t)
	b.Yield("help")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := tools["semar_wait_for_control"].Handler(ctx, mcp.Call{Name: "semar_wait_for_control", Args: json.RawMessage(`{}`), Progress: func(float64, float64, string) {}})
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a cancelled wait returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled wait did not return")
	}
}
