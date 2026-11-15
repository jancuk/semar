package cli

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/stage/plan"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// quietTicker never ticks, so a test calls tick itself.
func quietTicker(time.Duration) (<-chan time.Time, func()) { return make(chan time.Time), func() {} }

func TestFormatElapsed(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"}, {-time.Second, "0s"}, {45 * time.Second, "45s"}, {59*time.Second + 600*time.Millisecond, "1m00s"},
		{time.Minute, "1m00s"}, {84 * time.Second, "1m24s"}, {59*time.Minute + 59*time.Second, "59m59s"},
		{time.Hour, "1h00m00s"}, {time.Hour + 2*time.Minute + 3*time.Second, "1h02m03s"},
	} {
		if got := formatElapsed(tc.d); got != tc.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestTerminalStatusLine(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	var out lockedBuffer
	r := startProgress(&out, clock.Now, true, quietTicker)

	clock.Advance(120 * time.Millisecond)
	r.tick()
	want := "\r⠋ planning · 0s\x1b[K"
	if got := out.String(); got != want {
		t.Fatalf("first tick wrote %q, want %q", got, want)
	}

	r.Report(plan.Progress{Attempt: 1})
	r.Report(plan.Progress{Attempt: 1, Progress: provider.Progress{Turns: 12, ToolCalls: 31, Tool: "Read"}})
	clock.Advance(84 * time.Second)
	r.tick()
	want += "\r⠙ planning · 1m24s · 12 responses · 31 tool calls · last: Read\x1b[K"

	r.Report(plan.Progress{Attempt: 1, Progress: provider.Progress{Turns: 1, ToolCalls: 1, Tool: "a\x1b[31mb\nc"}})
	r.tick()
	want += "\r⠹ planning · 1m24s · 1 response · 1 tool call · last: a\\x1b[31mb c\x1b[K"

	r.Report(plan.Progress{Attempt: 1, Progress: provider.Progress{Turns: 1, ToolCalls: 1, Tool: "mcp__secret_server__lookup"}})
	r.tick()
	want += "\r⠸ planning · 1m24s · 1 response · 1 tool call · last: an MCP tool\x1b[K"

	r.Report(plan.Progress{Attempt: 1, Progress: provider.Progress{Turns: 100000, ToolCalls: 100000, Tool: "AVeryLongToolNameThatGoesOnAndOn"}})
	r.tick()
	// The tool part would give 79 characters, so it is dropped.
	want += "\r⠼ planning · 1m24s · 100000 responses · 100000 tool calls\x1b[K"

	r.Report(plan.Progress{Attempt: 2, Problems: 1})
	r.flush() // the goroutine may have written the line already
	want += "\r\x1b[Ksemar: draft 1 had 1 problem; the planner is fixing them\n"
	r.Report(plan.Progress{Attempt: 2, Problems: 1, Progress: provider.Progress{Turns: 2}})
	r.tick()
	want += "\r⠴ planning · 1m24s · draft 2 of 3 · 2 responses\x1b[K"

	r.Report(plan.Progress{Attempt: 3, Problems: 4})
	r.flush() // the goroutine may have written the line already
	want += "\r\x1b[Ksemar: draft 2 had 4 problems; the planner is fixing them\n"

	if got := out.String(); got != want {
		t.Fatalf("output:\n%q\nwant:\n%q", got, want)
	}

	r.Stop()
	want += "\r\x1b[K"
	r.Stop()
	r.Report(plan.Progress{Attempt: 3, Problems: 4, Progress: provider.Progress{Turns: 5}})
	r.tick()
	if got := out.String(); got != want {
		t.Errorf("after Stop:\n%q\nwant:\n%q", got, want)
	}
	if got := r.Elapsed(); got != 84*time.Second+120*time.Millisecond {
		t.Errorf("Elapsed = %v", got)
	}
}

func TestStatusLineIsAtMost78Characters(t *testing.T) {
	long := plan.Progress{Attempt: 2, Progress: provider.Progress{Turns: 123456, ToolCalls: 123456, Tool: strings.Repeat("é", 40)}}
	for _, p := range []plan.Progress{{}, long, {Attempt: 3, Progress: provider.Progress{Turns: 9, Tool: "Read"}}} {
		line := statusLine("⠋", 3*time.Hour+time.Second, p)
		if n := utf8.RuneCountInString(line); n > statusWidth {
			t.Errorf("%q has %d characters", line, n)
		}
	}
	if got := statusLine("⠋", time.Second, long); !strings.HasSuffix(got, "123456 tool calls") {
		t.Errorf("long line = %q", got)
	}
	if got := toolLabel(strings.Repeat("é", 40)); utf8.RuneCountInString(got) != toolNameWidth {
		t.Errorf("toolLabel = %q", got)
	}
}

func TestPlainReporter(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	var out lockedBuffer
	r := startProgress(&out, clock.Now, false, quietTicker)
	r.Report(plan.Progress{Attempt: 1})
	r.Report(plan.Progress{Attempt: 1, Progress: provider.Progress{Turns: 12, ToolCalls: 31, Tool: "Read"}})
	if out.String() != "" {
		t.Fatalf("wrote %q before the first line", out.String())
	}
	clock.Advance(90 * time.Second)
	r.tick()
	want := "semar: still planning: 1m30s, draft 1 of 3, 12 responses, 31 tool calls\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
	r.Report(plan.Progress{Attempt: 2, Problems: 4})
	r.flush() // the goroutine may have written the line already
	want += "semar: draft 1 had 4 problems; the planner is fixing them\n"
	clock.Advance(30 * time.Second)
	r.tick()
	want += "semar: still planning: 2m00s, draft 2 of 3\n"
	r.Stop()
	r.Stop()
	r.tick()
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestPlainReporterBeforeAnyReportNamesDraftOne(t *testing.T) {
	if got := plainLine(30*time.Second, plan.Progress{}); got != "semar: still planning: 30s, draft 1 of 3" {
		t.Errorf("got %q", got)
	}
}

func TestReporterTicksFromItsGoroutine(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	var out lockedBuffer
	ticks := make(chan time.Time)
	stopped := false
	r := startProgress(&out, clock.Now, true, func(d time.Duration) (<-chan time.Time, func()) {
		if d != 120*time.Millisecond {
			t.Errorf("tick interval = %v", d)
		}
		return ticks, func() { stopped = true }
	})
	// An unbuffered send returns once the goroutine has taken the tick, so after the second
	// send the first tick has been written.
	ticks <- time.Time{}
	ticks <- time.Time{}
	r.Stop()
	got := out.String()
	if !strings.HasPrefix(got, "\r⠋ planning · 0s\x1b[K") || !strings.HasSuffix(got, "\r\x1b[K") {
		t.Errorf("got %q", got)
	}
	if !stopped {
		t.Error("the ticker was not stopped")
	}
}

func TestReporterRace(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	var out lockedBuffer
	ticks := make(chan time.Time)
	r := startProgress(&out, clock.Now, true, func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} })
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				r.Report(plan.Progress{Attempt: 1 + i/100, Problems: g, Progress: provider.Progress{Turns: i, ToolCalls: i, Tool: "Read"}})
				_ = r.Elapsed()
			}
		}()
	}
	stopped := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case ticks <- time.Time{}:
				clock.Advance(time.Second)
			case <-stopped:
				return
			}
		}
	}()
	var stoppers sync.WaitGroup
	stoppers.Add(2)
	for range 2 {
		go func() { defer stoppers.Done(); r.Stop() }()
	}
	stoppers.Wait()
	close(stopped)
	wg.Wait()
	r.Stop()
	end := out.String()
	r.Report(plan.Progress{Attempt: 9})
	if out.String() != end {
		t.Error("something was written after Stop")
	}
}

// blockingWriter holds every Write until release is closed.
type blockingWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	out     lockedBuffer
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.out.Write(p)
}

func TestReportDoesNotWaitForAStalledWriter(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	w := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(w.release) }) }
	defer release()
	ticks := make(chan time.Time)
	r := startProgress(w, clock.Now, true, func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} })

	ticks <- time.Time{}
	select {
	case <-w.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the tick did not start a write")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= 50; i++ {
			r.Report(plan.Progress{Attempt: 1 + i/10, Problems: i, Progress: provider.Progress{Turns: i}})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Report waited for a write that was stalled")
	}

	release()
	r.Stop()
	got := w.out.String()
	for attempt := 1; attempt <= 5; attempt++ {
		line := "semar: draft " + strconv.Itoa(attempt) + " had "
		if n := strings.Count(got, line); n != 1 {
			t.Errorf("%q written %d times in %q", line, n, got)
		}
	}
	if !strings.HasSuffix(got, "\r\x1b[K") {
		t.Errorf("the status line was not erased: %q", got)
	}
}

func TestToolLabelHasOnlyPrintableASCII(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{strings.Repeat("世", 30), strings.Repeat("?", toolNameWidth)},
		{"Read日本Tool é", "Read??Tool ?"},
		{"a\x1b[31mb\nc", "a\\x1b[31mb c"},
	} {
		if got := toolLabel(tc.name); got != tc.want {
			t.Errorf("toolLabel(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
	line := statusLine("⠋", time.Second, plan.Progress{Attempt: 1, Progress: provider.Progress{Turns: 1, Tool: strings.Repeat("世", 30)}})
	if !strings.HasSuffix(line, "last: "+strings.Repeat("?", toolNameWidth)) {
		t.Errorf("line = %q", line)
	}
}
