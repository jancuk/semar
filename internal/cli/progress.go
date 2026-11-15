package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/stage/plan"
)

const (
	statusEvery   = 120 * time.Millisecond
	plainEvery    = 30 * time.Second
	statusWidth   = 78
	toolNameWidth = 24
	eraseLine     = "\r\x1b[K"
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// ticker returns a channel that receives about every d, and a function that stops it.
type ticker func(d time.Duration) (<-chan time.Time, func())

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// stderrIsTerminal tells a terminal from a pipe or a file. /dev/null is a character device
// too, so it is left out by name.
func stderrIsTerminal(lookupEnv func(string) (string, bool)) bool {
	info, err := os.Stderr.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	term, ok := lookupEnv("TERM")
	return ok && term != "" && term != "dumb"
}

// progressReporter shows how a plan is going: one status line on a terminal, or one line every
// 30 seconds elsewhere. It owns one goroutine, which Stop ends.
type progressReporter struct {
	w        io.Writer
	clock    func() time.Time
	terminal bool
	start    time.Time

	mu      sync.Mutex
	stopped bool
	stopAt  time.Time
	last    plan.Progress
	frame   int

	quit chan struct{}
	done chan struct{}
	once sync.Once
}

func startProgress(w io.Writer, clock func() time.Time, terminal bool, tick ticker) *progressReporter {
	r := &progressReporter{w: w, clock: clock, terminal: terminal, start: clock(),
		quit: make(chan struct{}), done: make(chan struct{})}
	every := plainEvery
	if terminal {
		every = statusEvery
	}
	ticks, stopTicks := tick(every)
	go func() {
		defer close(r.done)
		defer stopTicks()
		for {
			select {
			case <-ticks:
				r.tick()
			case <-r.quit:
				return
			}
		}
	}()
	return r
}

// Report takes the progress of the plan. It is safe to call from any goroutine.
func (r *progressReporter) Report(p plan.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if r.last.Attempt > 0 && p.Attempt > r.last.Attempt {
		if r.terminal {
			io.WriteString(r.w, eraseLine)
		}
		fmt.Fprintf(r.w, "semar: draft %d had %s; the planner is fixing them\n", r.last.Attempt, problemCount(p.Problems))
	}
	r.last = p
}

func problemCount(n int) string {
	if n == 1 {
		return "1 problem"
	}
	return fmt.Sprintf("%d problems", n)
}

func (r *progressReporter) tick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	elapsed := r.clock().Sub(r.start)
	if !r.terminal {
		fmt.Fprintln(r.w, plainLine(elapsed, r.last))
		return
	}
	io.WriteString(r.w, "\r"+statusLine(spinner[r.frame%len(spinner)], elapsed, r.last)+"\x1b[K")
	r.frame++
}

// Stop erases the status line, ends the goroutine, and waits for it. Nothing is written after
// it returns. It can be called more than once.
func (r *progressReporter) Stop() {
	r.once.Do(func() {
		r.mu.Lock()
		r.stopped = true
		r.stopAt = r.clock()
		if r.terminal {
			io.WriteString(r.w, eraseLine)
		}
		r.mu.Unlock()
		close(r.quit)
	})
	<-r.done
}

// Elapsed is the time from the start to Stop, or to now while the reporter runs.
func (r *progressReporter) Elapsed() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return r.stopAt.Sub(r.start)
	}
	return r.clock().Sub(r.start)
}

// statusLine drops parts from the end until the line fits.
func statusLine(frame string, elapsed time.Duration, p plan.Progress) string {
	parts := []string{frame + " planning", formatElapsed(elapsed)}
	if p.Attempt > 1 {
		parts = append(parts, fmt.Sprintf("draft %d of %d", p.Attempt, plan.MaxAttempts))
	}
	if p.Turns > 0 {
		parts = append(parts, plural(p.Turns, "response"))
	}
	if p.ToolCalls > 0 {
		parts = append(parts, plural(p.ToolCalls, "tool call"))
	}
	if p.Tool != "" {
		parts = append(parts, "last: "+toolLabel(p.Tool))
	}
	for len(parts) > 2 {
		line := strings.Join(parts, " · ")
		if len([]rune(line)) <= statusWidth {
			return line
		}
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, " · ")
}

// plainLine has no tool name and always names the draft.
func plainLine(elapsed time.Duration, p plan.Progress) string {
	parts := []string{formatElapsed(elapsed), fmt.Sprintf("draft %d of %d", max(p.Attempt, 1), plan.MaxAttempts)}
	if p.Turns > 0 {
		parts = append(parts, plural(p.Turns, "response"))
	}
	if p.ToolCalls > 0 {
		parts = append(parts, plural(p.ToolCalls, "tool call"))
	}
	return "semar: still planning: " + strings.Join(parts, ", ")
}

// toolLabel hides the name of an MCP tool, because it names a server of the person's setup.
func toolLabel(name string) string {
	if strings.HasPrefix(name, "mcp__") {
		return "an MCP tool"
	}
	safe := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, terminalSafe(name))
	if rs := []rune(safe); len(rs) > toolNameWidth {
		return string(rs[:toolNameWidth])
	}
	return safe
}

// formatElapsed is 45s, 1m24s, or 1h02m03s.
func formatElapsed(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	s = max(s, 0)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh%02dm%02ds", s/3600, s%3600/60, s%60)
}
