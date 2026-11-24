package mobilemcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/mobilemcp"
	"github.com/jancuk/semar/internal/takeover"
)

func fp(v float64) *float64 { return &v }
func sp(v string) *string   { return &v }

func TestHumanActInLiveModeGoesThroughTheControllerAndNeverTheFeedText(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_get_screen_size", "") // the device is open

	res, err := f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "tap", X: fp(540), Y: fp(1620)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Recorded || res.Summary != "tap (540,1620)" {
		t.Errorf("result = %+v", res)
	}
	if f.dev.called("Touch") != 1 {
		t.Errorf("touches = %d", f.dev.called("Touch"))
	}

	res, err = f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "type", Text: sp("hunter2-typed")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "[typed 13 chars]" || strings.Contains(res.Summary, "hunter2") {
		t.Errorf("summary = %q", res.Summary)
	}
	if got := f.dev.typed; len(got) != 1 || got[0] != "hunter2-typed" {
		t.Errorf("typed = %v", got)
	}

	// A secret is typed from the environment of the server and is in no answer.
	res, err = f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "type", SecretEnv: "SHOP_PASSWORD"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Summary, "s3cret") || !strings.Contains(res.Summary, "SHOP_PASSWORD") {
		t.Errorf("summary = %q", res.Summary)
	}
	if got := f.scrub.Text("x s3cret-Pa55word y"); strings.Contains(got, "s3cret") {
		t.Error("the scrubber did not learn the secret that the person typed")
	}

	if _, err := f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "type", SecretEnv: "NOPE"}, true); err == nil {
		t.Error("an unset secret was typed")
	}
	if _, err := f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "press", Key: "BACK"}, true); err != nil {
		t.Error(err)
	}
	if _, err := f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "swipe", Direction: "up"}, true); err != nil {
		t.Error(err)
	}
	if _, err := f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "fly"}, true); err == nil {
		t.Error("an operation that is not known went through")
	}
	if _, err := f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "tap"}, true); err == nil {
		t.Error("a tap with no place went through")
	}
}

func TestHumanActIsRefusedInObserveModeAndWhenTheBatonMovedOn(t *testing.T) {
	f := newFixture(t, func(o *mobilemcp.Options, _ *fixture) {
		o.Guard = mcpkit.Guard{Observe: true, Reason: "the environment is production"}
	})
	_, err := f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "tap", X: fp(1), Y: fp(1)}, true)
	var refused *mobilemcp.ErrHumanRefused
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "production") {
		t.Fatalf("err = %v, want a refusal that says why", err)
	}
	if f.dev.called("Touch") != 0 || f.opens.Load() != 0 {
		t.Error("a refused action reached the device")
	}
	if r := f.m.ObserveReason(); !strings.Contains(r, "production") {
		t.Errorf("ObserveReason = %q", r)
	}

	g := newFixture(t)
	stop := errors.New("the agent owns the device")
	_, err = g.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "tap", X: fp(1), Y: fp(1), Still: func() error { return stop }}, true)
	if !errors.Is(err, stop) || g.dev.called("Touch") != 0 {
		t.Errorf("err = %v, touches = %d", err, g.dev.called("Touch"))
	}
}

func TestLiveSnapshotGroupsByWindowAndKeepsTheRefsOfTheAgent(t *testing.T) {
	f := newFixture(t)
	listing := textOf(f.ok("mobile_list_elements_on_screen", ""))
	login := ref(t, listing, "Sign in")

	v, err := f.m.LiveSnapshot(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != mobilemcp.ModeLive || v.Width != 1080 || v.NoDevice {
		t.Errorf("view = %+v", v)
	}
	var kinds []string
	for _, w := range v.Windows {
		kinds = append(kinds, w.Kind+":"+w.Package)
	}
	if got := strings.Join(kinds, ","); got != "app:com.shop,keyboard:com.kb" {
		t.Errorf("windows = %s", got)
	}
	// The snapshot made no ref: the agent's e-number still taps what it named.
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+login+`"}`)
	if f.dev.called("Touch") != 1 {
		t.Error("the agent's ref no longer works after a studio snapshot")
	}

	// With no device open, the snapshot does not open one for the agent.
	g := newFixture(t)
	v, err = g.m.LiveSnapshot(context.Background(), false)
	if err != nil || !v.NoDevice || g.opens.Load() != 0 {
		t.Errorf("view = %+v, err = %v, opens = %d", v, err, g.opens.Load())
	}
	// A person who holds control may look, and that opens it.
	v, err = g.m.LiveSnapshot(context.Background(), true)
	if err != nil || v.NoDevice || len(v.Windows) == 0 || g.opens.Load() != 1 {
		t.Errorf("view = %+v, err = %v, opens = %d", v, err, g.opens.Load())
	}
}

func TestLiveSnapshotAnswersBusyWhileTheAgentRunsACall(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_get_screen_size", "")
	f.dev.delay = 1500 * time.Millisecond
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		f.call("mobile_list_elements_on_screen", "")
	}()
	deadline := time.Now().Add(5 * time.Second)
	for f.dev.inflight.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	v, err := f.m.LiveSnapshot(context.Background(), false)
	if err != nil || !v.Busy {
		t.Errorf("view = %+v, err = %v", v, err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("the snapshot waited %v for the agent", time.Since(start))
	}
	wg.Wait()
}

func TestLiveScreenGivesAJPEGAndRefusesASecretScreen(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_get_screen_size", "")
	data, err := f.m.LiveScreen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) {
		t.Errorf("not a JPEG: % x", data[:8])
	}
	f.scrub.Add("Welcome back")
	if _, err := f.m.LiveScreen(context.Background()); !errors.Is(err, mobilemcp.ErrSecretScreen) {
		t.Errorf("err = %v, want ErrSecretScreen", err)
	}
	v, err := f.m.LiveSnapshot(context.Background(), false)
	if err != nil || !v.Secret {
		t.Errorf("secret = %v, err = %v", v.Secret, err)
	}
	if b, _ := json.Marshal(v); strings.Contains(string(b), "Welcome back") {
		t.Error("the snapshot shows the secret text")
	}
}

func TestOnHandBackDropsTheRefsAndReadsARecordingAgain(t *testing.T) {
	f := newFixture(t)
	listing := textOf(f.ok("mobile_list_elements_on_screen", ""))
	login := ref(t, listing, "Sign in")
	if err := f.m.OnHandBack(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"`+login+`"}`, "no refs yet")

	// FreshListing makes refs that work.
	text := f.m.FreshListing(context.Background())
	login = ref(t, text, "Sign in")
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+login+`"}`)

	g, _ := recFixture(t)
	g.ok("semar_record_start", `{"kind":"flow","id":"hand"}`)
	if err := g.m.OnHandBack(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m := g.m.Mode(); m != mobilemcp.ModeRecord {
		t.Errorf("mode = %s", m)
	}
}

func TestHumanActInARecordingIsAStepOrAGap(t *testing.T) {
	f, b := recFixture(t)
	f.ok("semar_record_start", `{"kind":"flow","id":"hand"}`)
	steps := func() int {
		var out struct {
			Steps []json.RawMessage `json:"steps"`
		}
		decode(t, f.ok("semar_record_steps", ""), &out)
		return len(out.Steps)
	}
	base := steps()

	// Recorded: the tap on the button is a step, and it is the person's.
	res, err := f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "tap", X: fp(300), Y: fp(950)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Recorded || res.Gap || steps() != base+1 {
		t.Errorf("result = %+v, steps = %d (was %d)", res, steps(), base)
	}
	v, err := f.m.LiveSnapshot(context.Background(), false)
	if err != nil || v.Mode != mobilemcp.ModeRecord || len(v.Steps) != base+1 {
		t.Fatalf("view = %+v, err = %v", v, err)
	}

	// Not recorded: the device moves, no step is added, and a save is refused.
	b.d.Show(signIn(false))
	if _, err := f.m.LiveSnapshot(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	touches := len(b.d.Touches())
	res, err = f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "tap", X: fp(300), Y: fp(950)}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Recorded || !res.Gap || steps() != base+1 || len(b.d.Touches()) != touches+1 {
		t.Errorf("result = %+v, steps = %d, touches %d -> %d", res, steps(), touches, len(b.d.Touches()))
	}
	if gap, at := f.m.Gap(); !gap || at != base+1 {
		t.Errorf("Gap = %v, %d", gap, at)
	}
	f.fails("semar_record_save", `{"verify":false}`, "were not recorded")
	if b.exists(".semar/flows/hand.yaml") {
		t.Error("a save with a gap wrote the flow")
	}
	var st map[string]any
	decode(t, mustTool(t, f.m.ControlTools(takeover.New(nil)), "semar_control_status").call(t), &st)
	if st["gap"] != true || st["mode"] != "record" {
		t.Errorf("status = %v", st)
	}
}

type toolCaller struct {
	tool mcp.Tool
	args string
}

func mustTool(t *testing.T, tools []mcp.Tool, name string) toolCaller {
	t.Helper()
	for _, tl := range tools {
		if tl.Name == name {
			return toolCaller{tool: tl, args: "{}"}
		}
	}
	t.Fatalf("no tool %s", name)
	return toolCaller{}
}

func (c toolCaller) call(t *testing.T) mcp.Result {
	t.Helper()
	res, err := c.tool.Handler(context.Background(), mcp.Call{Name: c.tool.Name, Args: json.RawMessage(c.args), Progress: func(float64, float64, string) {}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

var _ = mobile.Gesture{}

func TestHumanActGivesUpWithABusyErrorWhileAnAgentCallHoldsTheDevice(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_get_screen_size", "")
	f.dev.delay = 2600 * time.Millisecond
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		f.call("mobile_list_elements_on_screen", "")
	}()
	deadline := time.Now().Add(5 * time.Second)
	for f.dev.inflight.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	_, err := f.m.HumanAct(context.Background(), mobilemcp.HumanActRequest{Do: "tap", X: fp(1), Y: fp(1)}, true)
	took := time.Since(start)
	if !errors.Is(err, mobilemcp.ErrDeviceBusy) || err.Error() != "device is busy, try again" {
		t.Errorf("err = %v, want the busy error", err)
	}
	if took > 2500*time.Millisecond {
		t.Errorf("HumanAct waited %v for the agent", took)
	}
	if f.dev.called("Touch") != 0 {
		t.Error("the tap reached the device")
	}
	wg.Wait()
}

func TestControlStatusDoesNotHangBehindALongAgentCall(t *testing.T) {
	f := newFixture(t)
	b := takeover.New(nil)
	var status mcp.Tool
	for _, tool := range f.m.ControlTools(b) {
		if tool.Name == "semar_control_status" {
			status = tool
		}
	}
	f.ok("mobile_get_screen_size", "")
	f.dev.delay = 1200 * time.Millisecond
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		f.call("mobile_list_elements_on_screen", "")
	}()
	deadline := time.Now().Add(5 * time.Second)
	for f.dev.inflight.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	res, err := status.Handler(context.Background(), mcp.Call{Name: status.Name, Args: json.RawMessage(`{}`)})
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	if took := time.Since(start); took > 1100*time.Millisecond {
		t.Errorf("the status waited %v for the agent", took)
	}
	if text := res.Content[0].Text; !strings.Contains(text, `"busy":true`) {
		t.Errorf("status = %s", text)
	}
	wg.Wait()
}

func TestClipboardReadsPassTheBatonWhileAPersonDrivesButSetsAreRefused(t *testing.T) {
	b := takeover.New(nil)
	f := newFixture(t, func(o *mobilemcp.Options, _ *fixture) { o.Baton = b })
	f.dev.clip = "on the clipboard"
	wrap := takeover.Middleware(b)
	tool := f.tools["mobile_clipboard"]
	h := wrap(tool, tool.Handler)
	call := func(args string) mcp.Result {
		res, err := h(context.Background(), mcp.Call{Name: tool.Name, Args: json.RawMessage(args), Progress: func(float64, float64, string) {}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	if res := call(`{"text":"mine"}`); res.IsError {
		t.Fatalf("a set while the agent drives: %+v", res)
	}
	b.Yield("a captcha")
	res := call(`{"text":"agent write"}`)
	st, _ := res.Structured.(map[string]any)
	if !res.IsError || st["status"] != "human_in_control" {
		t.Fatalf("a set while a person drives: %+v", res)
	}
	if f.dev.clip != "mine" {
		t.Errorf("the clipboard was set while a person drives: %q", f.dev.clip)
	}
	if res := call(`{}`); res.IsError || !strings.Contains(textOf(res), "mine") {
		t.Errorf("a read while a person drives: %+v", res)
	}
}
