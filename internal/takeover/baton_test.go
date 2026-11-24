package takeover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/mcp"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func isHIC(err error) bool {
	var h *ErrHumanInControl
	return errors.As(err, &h)
}

func TestTransitions(t *testing.T) {
	ctx := context.Background()
	b := New(newClock().Now)
	if s := b.State(); s.Owner != Agent || s.Pending {
		t.Fatalf("start: %+v", s)
	}
	v0 := b.State().Version

	// Agent -> Pause -> Human -> Resume -> Agent
	if err := b.Pause(ctx, "fix login", time.Second); err != nil {
		t.Fatal(err)
	}
	s := b.State()
	if s.Owner != Human || s.Pending || s.Reason != "fix login" || s.Version <= v0 {
		t.Fatalf("after pause: %+v", s)
	}
	if err := b.Pause(ctx, "again", time.Second); err != nil { // idempotent
		t.Fatal(err)
	}
	if b.State().Reason != "fix login" {
		t.Fatal("idempotent pause changed the reason")
	}
	b.Resume("done")
	s = b.State()
	if s.Owner != Agent || s.Note != "done" || s.Asked != "" {
		t.Fatalf("after resume: %+v", s)
	}
	v := s.Version
	b.Resume("again") // idempotent
	if b.State().Version != v || b.State().Note != "done" {
		t.Fatal("idempotent resume changed state")
	}

	// Agent -> Yield -> Human -> Resume -> Agent
	b.Yield("need 2FA")
	s = b.State()
	if s.Owner != Human || s.Asked != "need 2FA" || s.Reason != "need 2FA" {
		t.Fatalf("after yield: %+v", s)
	}
	b.Resume("")
	if s = b.State(); s.Owner != Agent || s.Asked != "" {
		t.Fatalf("after resume 2: %+v", s)
	}
}

func TestEnterRules(t *testing.T) {
	ctx := context.Background()
	b := New(newClock().Now)

	// Human write under Agent is refused; human read is fine.
	if _, _, err := b.Enter(ctx, Human, true); !errors.Is(err, ErrAgentInControl) {
		t.Fatalf("human write under agent: %v", err)
	}
	if _, leave, err := b.Enter(ctx, Human, false); err != nil {
		t.Fatal(err)
	} else {
		leave(true, "")
	}
	// Agent write under Agent works.
	_, leave, err := b.Enter(ctx, Agent, true)
	if err != nil {
		t.Fatal(err)
	}
	leave(true, "")
	leave(true, "") // harmless

	b.Yield("help")
	if _, _, err := b.Enter(ctx, Agent, true); !isHIC(err) {
		t.Fatalf("agent write under human: %v", err)
	}
	// Read allowed under Human.
	if _, leave, err := b.Enter(ctx, Agent, false); err != nil {
		t.Fatal(err)
	} else {
		leave(true, "")
	}
	// Human write allowed under Human.
	if _, leave, err := b.Enter(ctx, Human, true); err != nil {
		t.Fatal(err)
	} else {
		leave(true, "")
	}
	b.Resume("")
	if _, _, err := b.Enter(ctx, Human, true); !errors.Is(err, ErrAgentInControl) {
		t.Fatal("human write after resume must be refused")
	}
}

func TestPauseWaitsForInflight(t *testing.T) {
	ctx := context.Background()
	b := New(nil)
	ctx2, leave, err := b.Enter(ctx, Agent, true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- b.Pause(ctx, "x", 5*time.Second) }()

	waitFor(t, func() bool { return b.State().Pending })
	if _, _, err := b.Enter(ctx, Agent, true); !isHIC(err) {
		t.Fatalf("new write during pending: %v", err)
	}
	if !b.Pending() {
		t.Fatal("Pending() false")
	}
	select {
	case <-done:
		t.Fatal("pause returned while a write was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	if ctx2.Err() != nil {
		t.Fatal("write was cancelled before the grace ended")
	}
	leave(true, "")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if b.State().Owner != Human {
		t.Fatal("not human")
	}
}

func TestPauseForceCancelsAfterGrace(t *testing.T) {
	ctx := context.Background()
	b := New(nil)
	ctx2, leave, err := b.Enter(ctx, Agent, true)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { // a stuck write that exits when cancelled
		<-ctx2.Done()
		leave(false, "cancelled")
		close(stopped)
	}()
	if err := b.Pause(ctx, "stuck", 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("write was not cancelled")
	}
	if b.State().Owner != Human {
		t.Fatal("not human")
	}
}

func TestPauseContextCancelRollsBack(t *testing.T) {
	b := New(nil)
	_, leave, _ := b.Enter(context.Background(), Agent, true)
	defer leave(true, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Pause(ctx, "x", time.Minute) }()
	waitFor(t, func() bool { return b.State().Pending })
	cancel()
	if err := <-done; err == nil {
		t.Fatal("want an error")
	}
	if s := b.State(); s.Pending || s.Owner != Agent {
		t.Fatalf("not rolled back: %+v", s)
	}
}

func TestResumeDuringPauseAborts(t *testing.T) {
	b := New(nil)
	_, leave, _ := b.Enter(context.Background(), Agent, true)
	done := make(chan error, 1)
	go func() { done <- b.Pause(context.Background(), "x", time.Minute) }()
	waitFor(t, func() bool { return b.State().Pending })
	b.Resume("never mind")
	leave(true, "")
	if err := <-done; !errors.Is(err, ErrPauseAborted) {
		t.Fatalf("got %v", err)
	}
	if b.State().Owner != Agent {
		t.Fatal("should still be agent")
	}
}

func TestHeartbeatExpiry(t *testing.T) {
	clk := newClock()
	b := New(clk.Now)
	var handed int32
	b.OnHandBack(func() { atomic.AddInt32(&handed, 1) })
	b.Yield("help")

	clk.Add(100 * time.Second)
	if b.State().Owner != Human {
		t.Fatal("handed back too early")
	}
	// A watch refreshes the heartbeat.
	b.Watch(context.Background(), 0, 0)
	clk.Add(100 * time.Second)
	if b.State().Owner != Human {
		t.Fatal("heartbeat did not refresh")
	}
	clk.Add(21 * time.Second)
	s := b.State()
	if s.Owner != Agent || s.Note != "the studio went away" {
		t.Fatalf("after expiry: %+v", s)
	}
	if atomic.LoadInt32(&handed) != 1 {
		t.Fatal("OnHandBack not run")
	}
	if n := b.TakeNotice(); !strings.Contains(n, "the studio went away") {
		t.Fatalf("notice %q", n)
	}

	// A custom timeout, checked by Enter.
	b.SetHeartbeatTimeout(10 * time.Second)
	b.Yield("again")
	clk.Add(11 * time.Second)
	if _, _, err := b.Enter(context.Background(), Agent, true); err != nil {
		t.Fatalf("enter after expiry: %v", err)
	}
}

func TestWaitAgent(t *testing.T) {
	b := New(nil)
	if _, err := b.WaitAgent(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.Yield("x")
	done := make(chan State, 1)
	go func() { s, _ := b.WaitAgent(context.Background()); done <- s }()
	time.Sleep(20 * time.Millisecond)
	b.Resume("ok")
	select {
	case s := <-done:
		if s.Owner != Agent {
			t.Fatal("not agent")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitAgent did not wake")
	}
	b.Yield("y")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := b.WaitAgent(ctx); err == nil {
		t.Fatal("want ctx error")
	}
}

func TestWatchAndEvents(t *testing.T) {
	b := New(nil)
	s0 := b.State()
	got := make(chan State, 1)
	go func() { s, _ := b.Watch(context.Background(), s0.Version, 5*time.Second); got <- s }()
	time.Sleep(20 * time.Millisecond)
	b.RecordEvent(Event{Who: Agent, Tool: "t", OK: true})
	select {
	case s := <-got:
		if s.Version <= s0.Version {
			t.Fatal("version did not grow")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not wake on event")
	}

	st, evs := b.Watch(context.Background(), 0, 0)
	if len(evs) != 1 || evs[0].Tool != "t" || evs[0].Seq == 0 || evs[0].At.IsZero() {
		t.Fatalf("events: %+v", evs)
	}
	_, evs = b.Watch(context.Background(), st.Version, 10*time.Millisecond)
	if len(evs) != 0 {
		t.Fatalf("stale events: %+v", evs)
	}

	for i := 0; i < MaxEvents+50; i++ {
		b.RecordEvent(Event{Tool: "x"})
	}
	_, evs = b.Watch(context.Background(), 0, 0)
	if len(evs) != MaxEvents {
		t.Fatalf("ring size %d", len(evs))
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq <= evs[i-1].Seq {
			t.Fatal("events out of order")
		}
	}

	// A watch that times out returns the state.
	start := time.Now()
	b.Watch(context.Background(), 1<<60, 30*time.Millisecond)
	if time.Since(start) > time.Second {
		t.Fatal("watch overran its max")
	}
}

func TestSettersBumpVersion(t *testing.T) {
	b := New(nil)
	v := b.State().Version
	b.SetMode("record")
	b.SetRecordHuman(false)
	b.SetGap(true)
	s := b.State()
	if s.Mode != "record" || s.RecordHuman || !s.Gap || s.Version < v+3 {
		t.Fatalf("%+v", s)
	}
}

// TestStressNoAgentWriteUnderHuman runs agent writes against pause and resume. The fake device
// records the owner at every write.
func TestStressNoAgentWriteUnderHuman(t *testing.T) {
	b := New(nil)
	ctx := context.Background()
	var (
		violations int64
		writes     int64
		stop       = make(chan struct{})
	)
	device := func() {
		if b.State().Owner == Human {
			atomic.AddInt64(&violations, 1)
		}
		atomic.AddInt64(&writes, 1)
		time.Sleep(50 * time.Microsecond)
		if b.State().Owner == Human {
			atomic.AddInt64(&violations, 1)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c, leave, err := b.Enter(ctx, Agent, true)
				if err != nil {
					time.Sleep(100 * time.Microsecond)
					continue
				}
				if c.Err() == nil {
					device()
				}
				leave(true, "")
			}
		}()
	}
	for i := 0; i < 40; i++ {
		if err := b.Pause(ctx, "p", time.Second); err != nil {
			t.Error(err)
		}
		time.Sleep(200 * time.Microsecond)
		if i%2 == 0 {
			b.Resume("")
		} else {
			b.Yield("y")
			b.Resume("")
		}
		time.Sleep(200 * time.Microsecond)
	}
	close(stop)
	wg.Wait()
	if v := atomic.LoadInt64(&violations); v != 0 {
		t.Fatalf("%d agent writes ran under human control", v)
	}
	if atomic.LoadInt64(&writes) == 0 {
		t.Fatal("no writes ran")
	}
}

// --- middleware ---

func tool(name, surface string, write bool, ran *int32) (mcp.Tool, mcp.Handler) {
	h := func(ctx context.Context, c mcp.Call) (mcp.Result, error) {
		if ran != nil {
			atomic.AddInt32(ran, 1)
		}
		return mcp.TextResult("ran " + name), nil
	}
	return mcp.Tool{Name: name, Surface: surface, Write: write}, h
}

func TestMiddleware(t *testing.T) {
	b := New(nil)
	mw := Middleware(b)
	ctx := context.Background()
	var wrote, read, ctl int32
	wt, wh := tool("mobile_tap", "mobile", true, &wrote)
	rt, rh := tool("mobile_list", "mobile", false, &read)
	ct, ch := tool("semar_control_status", "control", true, &ctl)
	w, r, c := mw(wt, wh), mw(rt, rh), mw(ct, ch)

	res, err := w(ctx, mcp.Call{Name: "mobile_tap", Args: json.RawMessage(`{"secret":"hunter2"}`)})
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}

	b.Yield("2FA code")
	res, err = w(ctx, mcp.Call{Name: "mobile_tap"})
	if err != nil || !res.IsError {
		t.Fatalf("want isError: %v %+v", err, res)
	}
	m, ok := res.Structured.(map[string]any)
	if !ok || m["status"] != "human_in_control" || m["reason"] != "2FA code" || m["next"] != "semar_wait_for_control" || m["since"] == "" {
		t.Fatalf("structured: %+v", res.Structured)
	}
	if wrote != 1 {
		t.Fatalf("write ran under human: %d", wrote)
	}
	if res, _ = r(ctx, mcp.Call{}); res.IsError || read != 1 {
		t.Fatal("read must pass through")
	}
	if res, _ = c(ctx, mcp.Call{}); res.IsError || ctl != 1 {
		t.Fatal("control tool must bypass")
	}

	b.Resume("typed the code")
	res, _ = r(ctx, mcp.Call{})
	if len(res.Content) != 2 || !strings.Contains(res.Content[0].Text, "a person had control") || !strings.Contains(res.Content[0].Text, "typed the code") {
		t.Fatalf("notice missing: %+v", res.Content)
	}
	res, _ = w(ctx, mcp.Call{})
	if len(res.Content) != 1 {
		t.Fatalf("notice delivered twice: %+v", res.Content)
	}

	// The feed holds names and outcomes only.
	_, evs := b.Watch(ctx, 0, 0)
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	for _, e := range evs {
		if strings.Contains(e.Summary, "hunter2") || e.Tool == "" || e.Who != Agent {
			t.Fatalf("event %+v", e)
		}
	}
}

func TestMiddlewareCancelsInflightOnPause(t *testing.T) {
	b := New(nil)
	started := make(chan struct{})
	h := func(ctx context.Context, c mcp.Call) (mcp.Result, error) {
		close(started)
		<-ctx.Done()
		return mcp.ErrorResult("cancelled"), nil
	}
	call := Middleware(b)(mcp.Tool{Name: "mobile_wait", Surface: "mobile", Write: true}, h)
	done := make(chan mcp.Result, 1)
	go func() { r, _ := call(context.Background(), mcp.Call{}); done <- r }()
	<-started
	if err := b.Pause(context.Background(), "x", 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if r := <-done; !r.IsError {
		t.Fatal("want cancelled result")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestYieldDrainsInflightWrites(t *testing.T) {
	ctx := context.Background()
	b := New(nil)
	b.yieldGrace = time.Hour // only a cancel can end it
	b.cancelWait = time.Second
	ctx2, leave, err := b.Enter(ctx, Agent, true)
	if err != nil {
		t.Fatal(err)
	}
	// A write that finishes on its own inside the grace: the person gets control only after it.
	done := make(chan struct{})
	go func() { b.Yield("captcha"); close(done) }()
	waitFor(t, func() bool { return b.State().Pending })
	if _, _, err := b.Enter(ctx, Agent, true); !isHIC(err) {
		t.Fatalf("a new write while yielding: %v", err)
	}
	select {
	case <-done:
		t.Fatal("yield returned with a write in flight")
	case <-time.After(30 * time.Millisecond):
	}
	if b.State().Owner != Agent {
		t.Fatal("the person owns the baton while a write runs")
	}
	if ctx2.Err() != nil {
		t.Fatal("the write was cancelled inside the grace")
	}
	leave(true, "")
	<-done
	if s := b.State(); s.Owner != Human || s.Pending || s.Asked != "captcha" {
		t.Fatalf("after yield: %+v", s)
	}
}

func TestYieldCancelsAWriteThatOutlastsTheGrace(t *testing.T) {
	b := New(nil)
	b.yieldGrace = 20 * time.Millisecond
	ctx2, leave, _ := b.Enter(context.Background(), Agent, true)
	stopped := make(chan struct{})
	go func() { <-ctx2.Done(); leave(false, "cancelled"); close(stopped) }()
	b.Yield("help")
	select {
	case <-stopped:
	default:
		t.Fatal("the write kept running after Yield")
	}
	if b.State().Owner != Human {
		t.Fatal("not human")
	}
}

func TestYieldEndedByResumeKeepsTheAgent(t *testing.T) {
	b := New(nil)
	b.yieldGrace = time.Hour
	_, leave, _ := b.Enter(context.Background(), Agent, true)
	done := make(chan struct{})
	go func() { b.Yield("x"); close(done) }()
	waitFor(t, func() bool { return b.State().Pending })
	b.Resume("never mind")
	leave(true, "")
	<-done
	if s := b.State(); s.Owner != Agent || s.Pending {
		t.Fatalf("after resume: %+v", s)
	}
}

func TestPauseCancelsRunningReadsAfterAShortGraceButLaterReadsRun(t *testing.T) {
	ctx := context.Background()
	b := New(nil)
	b.readGrace = 20 * time.Millisecond
	rctx, leave, err := b.Enter(ctx, Agent, false) // a read
	if err != nil {
		t.Fatal(err)
	}
	defer leave(true, "")
	start := time.Now()
	if err := b.Pause(ctx, "mine", time.Minute); err != nil {
		t.Fatal(err)
	}
	// No write was in flight, so Pause did not wait for the read: it returned at once.
	if time.Since(start) > 5*time.Second {
		t.Fatal("pause waited for a read")
	}
	select {
	case <-rctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the running read was not cancelled")
	}
	// Reads that start while a person drives are admitted and are not cancelled.
	c2, leave2, err := b.Enter(ctx, Agent, false)
	if err != nil {
		t.Fatalf("a read while a person drives: %v", err)
	}
	defer leave2(true, "")
	time.Sleep(60 * time.Millisecond)
	if c2.Err() != nil {
		t.Fatal("a read that started after the Pause was cancelled")
	}
}

func TestPauseKeepsAReadThatFinishesInsideTheGrace(t *testing.T) {
	b := New(nil)
	b.readGrace = time.Hour
	rctx, leave, _ := b.Enter(context.Background(), Agent, false)
	if err := b.Pause(context.Background(), "x", 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if rctx.Err() != nil {
		t.Fatal("the read was cancelled before its grace")
	}
	leave(true, "")
}

func TestMiddlewareKeepsTheNoticeWhenTheHandlerFails(t *testing.T) {
	b := New(nil)
	b.Yield("x")
	b.Resume("done")
	fail := Middleware(b)(mcp.Tool{Name: "mobile_tap", Surface: "mobile", Write: true}, func(context.Context, mcp.Call) (mcp.Result, error) {
		return mcp.Result{}, errors.New("boom")
	})
	if _, err := fail(context.Background(), mcp.Call{}); err == nil {
		t.Fatal("want the error")
	}
	isErrRes := Middleware(b)(mcp.Tool{Name: "mobile_tap", Surface: "mobile", Write: true}, func(context.Context, mcp.Call) (mcp.Result, error) {
		return mcp.ErrorResult("no"), nil
	})
	if res, _ := isErrRes(context.Background(), mcp.Call{}); len(res.Content) != 1 {
		t.Fatalf("an error result carries the notice: %+v", res.Content)
	}
	var ran int32
	ok := Middleware(b)(mcp.Tool{Name: "mobile_list", Surface: "mobile"}, func(context.Context, mcp.Call) (mcp.Result, error) {
		atomic.AddInt32(&ran, 1)
		return mcp.TextResult("fine"), nil
	})
	res, _ := ok(context.Background(), mcp.Call{})
	if len(res.Content) != 2 || !strings.Contains(res.Content[0].Text, "a person had control") {
		t.Fatalf("the notice was dropped by the failed calls: %+v", res.Content)
	}
}
