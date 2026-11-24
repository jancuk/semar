package mobilemcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mobilemcp"
)

// fakeFlows is a FlowBackend: it records what it was asked and gives a set answer, after changing the
// screen as a flow would.
type fakeFlows struct {
	mu   sync.Mutex
	reqs []mobilemcp.FlowRequest
	out  mobilemcp.FlowOutcome
	err  error
	// run is called with the device, to change the screen.
	run func(ctx context.Context, dev mobilemcp.Device)
}

func (b *fakeFlows) Run(ctx context.Context, dev mobilemcp.Device, r mobilemcp.FlowRequest) (mobilemcp.FlowOutcome, error) {
	b.mu.Lock()
	b.reqs = append(b.reqs, r)
	b.mu.Unlock()
	if b.run != nil {
		b.run(ctx, dev)
	}
	return b.out, b.err
}

func completed() mobilemcp.FlowOutcome {
	return mobilemcp.FlowOutcome{
		Status: "completed", Flow: domain.FlowRef{ID: "login", Digest: "sha256:abc"}, Checked: 1,
		Steps: []mobilemcp.FlowStep{{Name: "start", Op: "launch", OK: true, MS: 12}, {Name: "submit", Op: "tap", OK: true, MS: 30}},
	}
}

// flowFixture is the live fixture with semar_run_flow added.
func flowFixture(t *testing.T, b *fakeFlows, opts ...fixOpt) *fixture {
	t.Helper()
	f := newFixture(t, opts...)
	f.m.SetFlowBackend(b)
	for _, tool := range f.m.FlowTools() {
		f.tools[tool.Name] = tool
	}
	return f
}

func TestRunFlowToolIsRegisteredOnlyWithABackendAndPassesTheSchemaCheck(t *testing.T) {
	if got := newFixture(t).m.FlowTools(); len(got) != 0 {
		t.Errorf("no backend, but %d flow tools", len(got))
	}
	f := flowFixture(t, &fakeFlows{})
	tools := f.m.FlowTools()
	if len(tools) != 1 || tools[0].Name != "semar_run_flow" {
		t.Fatalf("tools = %+v", tools)
	}
	tool := tools[0]
	if err := mcp.ValidatePortableSchema(tool.Input); err != nil {
		t.Error(err)
	}
	if tool.Surface != "mobile" || !tool.Write || tool.Annotations.ReadOnly == nil || *tool.Annotations.ReadOnly || tool.Annotations.Destructive == nil || *tool.Annotations.Destructive {
		t.Errorf("surface %q, write %v, annotations %+v", tool.Surface, tool.Write, tool.Annotations)
	}
	props := tool.Input["properties"].(map[string]mcp.Schema)
	for _, k := range []string{"id", "with", "keepState", "list", "timeoutSec"} {
		if _, ok := props[k]; !ok {
			t.Errorf("no property %s", k)
		}
	}
	if props["keepState"]["default"] != true || props["list"]["default"] != true || props["timeoutSec"]["default"] != 300 {
		t.Errorf("defaults: %v %v %v", props["keepState"]["default"], props["list"]["default"], props["timeoutSec"]["default"])
	}
	// The regular tools do not include it.
	for _, tl := range f.m.Tools() {
		if tl.Name == "semar_run_flow" {
			t.Error("Tools() holds semar_run_flow")
		}
	}
}

func TestRunFlowRunsOnTheLiveDeviceAndListsTheScreenWithFreshRefs(t *testing.T) {
	b := &fakeFlows{out: completed()}
	f := flowFixture(t, b)
	b.run = func(_ context.Context, dev mobilemcp.Device) {
		if dev != mobilemcp.Device(f.dev) {
			t.Error("the flow did not get the live device")
		}
		// The flow signed in: the screen is another one.
		f.dev.setTree(screenTree("d2", mobile.Rect{Left: 100, Top: 1000, Right: 980, Bottom: 1120}, true))
	}
	f.ok("mobile_list_elements_on_screen", "")
	res := f.ok("semar_run_flow", `{"id":"login","with":[{"name":"email","value":"ana@example.com"}]}`)
	if f.opens.Load() != 1 || f.closes.Load() != 0 {
		t.Errorf("opens %d, closes %d: the flow must use the device that is open, with no handoff", f.opens.Load(), f.closes.Load())
	}
	if len(b.reqs) != 1 {
		t.Fatalf("%d requests", len(b.reqs))
	}
	if r := b.reqs[0]; r.ID != "login" || r.With["email"] != "ana@example.com" || !r.KeepState {
		t.Errorf("request = %+v: keepState is true by default", r)
	}
	text := textOf(res)
	var out mobilemcp.FlowOutcome
	if err := json.Unmarshal([]byte(text[:strings.Index(text, "\n")]), &out); err != nil {
		t.Fatalf("the first line is not the outcome: %v\n%s", err, text)
	}
	if out.Status != "completed" || out.Flow.ID != "login" || len(out.Steps) != 2 || out.Checked != 1 {
		t.Errorf("outcome = %+v", out)
	}
	// The listing is the screen after the flow, with refs that work.
	if !strings.Contains(text, "the screen now:") || !strings.Contains(text, "[100,1000][980,1120]") {
		t.Errorf("no fresh listing:\n%s", text)
	}
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+ref(t, text, `"Sign in"`)+`"}`)
	if g := lastTouched(f); g.From != (mobile.Point{X: 540, Y: 1060}) {
		t.Errorf("tapped %v", g)
	}
}

func lastTouched(f *fixture) mobile.Gesture {
	f.dev.mu.Lock()
	defer f.dev.mu.Unlock()
	return f.dev.touches[len(f.dev.touches)-1]
}

func TestRunFlowForgetsTheOldRefsEvenWithoutAListing(t *testing.T) {
	f := flowFixture(t, &fakeFlows{out: completed()})
	f.ok("mobile_list_elements_on_screen", "")
	res := f.ok("semar_run_flow", `{"id":"login","list":false}`)
	if strings.Contains(textOf(res), "the screen now") {
		t.Errorf("list: false still listed:\n%s", textOf(res))
	}
	f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"e1"}`, "no refs yet")
}

func TestRunFlowScrubsWhatTheFlowCaptured(t *testing.T) {
	out := completed()
	out.Captured = map[string]string{"token": "s3cret-Pa55word", "shown": "Hi Ana", "long": strings.Repeat("x", 500)}
	out.Steps[1].Note = "typed s3cret-Pa55word"
	b := &fakeFlows{out: out}
	f := flowFixture(t, b)
	f.scrub.Add("s3cret-Pa55word")
	text := textOf(f.ok("semar_run_flow", `{"id":"login"}`))
	if strings.Contains(text, "s3cret-Pa55word") {
		t.Errorf("a secret is in the result:\n%s", text)
	}
	if !strings.Contains(text, `"shown":"Hi Ana"`) {
		t.Errorf("a harmless capture is gone:\n%s", text)
	}
	if strings.Contains(text, strings.Repeat("x", 300)) {
		t.Error("a long capture was not cut")
	}
}

func TestRunFlowOnAFlowThatDidNotCompleteIsAnErrorWithTheScreen(t *testing.T) {
	for _, status := range []string{"failed", "refused"} {
		out := mobilemcp.FlowOutcome{Status: status, Flow: domain.FlowRef{ID: "login"}, Error: "step submit " + status}
		f := flowFixture(t, &fakeFlows{out: out})
		got := f.fails("semar_run_flow", `{"id":"login"}`, "step submit "+status)
		if !strings.Contains(got, "the screen now:") {
			t.Errorf("%s: no listing of the screen where it stopped:\n%s", status, got)
		}
		if n := len(f.m.Uses(0)); n != 0 {
			t.Errorf("%s: %d uses recorded for a flow that did not complete", status, n)
		}
	}
}

func TestRunFlowBackendErrorIsReportedAndRefsAreForgotten(t *testing.T) {
	f := flowFixture(t, &fakeFlows{err: errors.New("the screens of shop-app were not found")})
	f.ok("mobile_list_elements_on_screen", "")
	f.fails("semar_run_flow", `{"id":"login"}`, "the screens of shop-app were not found")
	f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"e1"}`, "no refs yet")
}

func TestRunFlowKeepStateCanBeTurnedOff(t *testing.T) {
	b := &fakeFlows{out: completed()}
	f := flowFixture(t, b)
	f.ok("semar_run_flow", `{"id":"login","keepState":false}`)
	if b.reqs[0].KeepState {
		t.Error("keepState: false was not passed on")
	}
}

func TestRunFlowChecksItsArguments(t *testing.T) {
	b := &fakeFlows{out: completed()}
	f := flowFixture(t, b)
	var many []string
	for i := 0; i < 33; i++ {
		many = append(many, `{"name":"p`+string(rune('a'+i%26))+string(rune('a'+i/26))+`","value":"v"}`)
	}
	cases := map[string]string{
		`{}`:                               "id",
		`{"id":"../etc"}`:                  "invalid flow ID",
		`{"id":"login","timeoutSec":0}`:    "timeoutSec is 1 to 1800",
		`{"id":"login","timeoutSec":1801}`: "timeoutSec is 1 to 1800",
		`{"id":"login","with":[` + strings.Join(many, ",") + `]}`:                          "at most 32",
		`{"id":"login","with":[{"name":"a","value":"` + strings.Repeat("v", 1025) + `"}]}`: "longer than 1024",
		`{"id":"login","nope":1}`: "nope",
	}
	for args, want := range cases {
		short := args
		if len(short) > 60 {
			short = short[:60]
		}
		t.Run(short, func(t *testing.T) { f.fails("semar_run_flow", args, want) })
	}
	if len(b.reqs) != 0 {
		t.Errorf("%d runs started for bad arguments", len(b.reqs))
	}
	// The object form of with is accepted too, and a value of 1 KiB is.
	f.ok("semar_run_flow", `{"id":"login","with":{"email":"`+strings.Repeat("v", 1024)+`"},"timeoutSec":1800}`)
	if len(b.reqs) != 1 {
		t.Errorf("%d runs", len(b.reqs))
	}
}

func TestRunFlowIsRefusedInObserveMode(t *testing.T) {
	b := &fakeFlows{out: completed()}
	f := flowFixture(t, b, observeOpt)
	got := f.fails("semar_run_flow", `{"id":"login"}`, "production")
	if !strings.Contains(got, "mobile") {
		t.Errorf("refusal = %q", got)
	}
	if len(b.reqs) != 0 || f.opens.Load() != 0 {
		t.Errorf("a refused flow ran: %d runs, %d opens", len(b.reqs), f.opens.Load())
	}
}

func TestRunFlowStopsWhenTheCallIsCancelled(t *testing.T) {
	started := make(chan struct{})
	b := &fakeFlows{}
	b.run = func(ctx context.Context, _ mobilemcp.Device) {
		close(started)
		<-ctx.Done()
		b.err = ctx.Err()
	}
	f := flowFixture(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan mcp.Result, 1)
	go func() {
		res, _ := f.tools["semar_run_flow"].Handler(ctx, callOf("semar_run_flow", `{"id":"login"}`))
		done <- res
	}()
	<-started
	cancel()
	select {
	case res := <-done:
		if !res.IsError || !strings.Contains(textOf(res), "could not run") {
			t.Errorf("result = %q", textOf(res))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the flow did not stop when the call was cancelled")
	}
}

func TestRunFlowTimeoutEndsTheRunContext(t *testing.T) {
	b := &fakeFlows{}
	var deadline time.Time
	b.run = func(ctx context.Context, _ mobilemcp.Device) { deadline, _ = ctx.Deadline() }
	b.out = completed()
	f := flowFixture(t, b)
	before := time.Now()
	f.ok("semar_run_flow", `{"id":"login","timeoutSec":7}`)
	if d := deadline.Sub(before); d < 6*time.Second || d > 8*time.Second {
		t.Errorf("the deadline is %v away, want about 7 s", d)
	}
}

func TestUsesGivesTheFlowsThatCompletedForAJourneyToTap(t *testing.T) {
	b := &fakeFlows{out: completed()}
	f := flowFixture(t, b)
	f.scrub.Add("hunter2-secret")
	if len(f.m.Uses(0)) != 0 {
		t.Fatal("uses before any run")
	}
	f.ok("semar_run_flow", `{"id":"login","with":[{"name":"email","value":"ana@example.com"}]}`)
	mark := len(f.m.Uses(0))
	f.ok("semar_run_flow", `{"id":"login","with":[{"name":"email","value":"hunter2-secret"}]}`)
	all, since := f.m.Uses(0), f.m.Uses(mark)
	if len(all) != 2 || all[0].Flow != "login" || all[0].With["email"] != "ana@example.com" {
		t.Errorf("uses = %+v", all)
	}
	if len(since) != 1 || strings.Contains(since[0].With["email"], "hunter2") {
		t.Errorf("uses since %d = %+v: a known secret must not be kept", mark, since)
	}
	// What a caller gets is its own copy.
	all[0].With["email"] = "changed"
	if f.m.Uses(0)[0].With["email"] != "ana@example.com" {
		t.Error("Uses gives the map that the Manager keeps")
	}
	if f.m.Uses(-3) == nil || f.m.Uses(99) != nil {
		t.Error("a since out of range is not clamped")
	}
}

func TestRunFlowWhileRecordingRefusesKeepStateFalseAndNeedsARecording(t *testing.T) {
	f, _ := recFixture(t)
	fb := &fakeFlows{out: completed()}
	f.m.SetFlowBackend(fb)
	for _, tool := range f.m.FlowTools() {
		f.tools[tool.Name] = tool
	}
	f.ok("semar_record_start", `{"kind":"flow","id":"rec-1"}`)
	var steps struct {
		Steps []json.RawMessage `json:"steps"`
	}
	decode(t, f.ok("semar_record_steps", ""), &steps)
	startSteps := len(steps.Steps)
	f.fails("semar_run_flow", `{"id":"login","keepState":false}`, "refused while recording")
	if len(fb.reqs) != 0 {
		t.Error("the backend was used while a recording goes on: the session runs the flow")
	}
	// A flow that does not exist in the repository is reported by the session, and nothing is recorded.
	f.fails("semar_run_flow", `{"id":"nope"}`, "")
	decode(t, f.ok("semar_record_steps", ""), &steps)
	if len(steps.Steps) != startSteps || len(f.m.Uses(0)) != 0 {
		t.Errorf("a failed flow left %d steps (was %d) and %d uses", len(steps.Steps), startSteps, len(f.m.Uses(0)))
	}
}
