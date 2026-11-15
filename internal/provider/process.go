package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

const (
	DefaultStdoutCap = 16 << 20
	stderrTailSize   = 4 << 10
	DefaultGrace     = 5 * time.Second
	// MaxStdoutLine is the longest line that OnStdoutLine gets. A longer one is skipped, so
	// that a huge tool result in a stream is not held a second time.
	MaxStdoutLine = 1 << 20
)

var ErrOutputTooLarge = errors.New("the child wrote more output than the cap")

type ProcessSpec struct {
	Path      string
	Args      []string
	Dir       string
	Env       []string
	Stdin     io.Reader
	StdoutCap int           // 0 means DefaultStdoutCap
	Grace     time.Duration // 0 means DefaultGrace
	// OnStdoutLine, when set, gets each complete line of standard output as it arrives, without
	// its line ending, in order, one call at a time, and never after RunProcess returns. The
	// slice is valid only during the call. A line longer than MaxStdoutLine, and a last line
	// with no line ending, are not passed. The calls stop when the output reaches the cap. The
	// child waits while the function runs, so it must return quickly.
	OnStdoutLine func(line []byte)
}

type ProcessResult struct {
	Stdout     []byte
	StderrTail []byte
	ExitCode   int
}

// RunProcess starts a child in its own process group and waits for it. When ctx ends it
// sends the group a stop signal, waits the grace period, then kills the group. It always
// waits for the child, and kills what is left of the group after it, so that no grandchild
// outlives the run. A child that ends with a status other than 0 is not an error. When ctx
// ends first, the error wraps ctx.Err().
func RunProcess(ctx context.Context, spec ProcessSpec) (ProcessResult, error) {
	limit := spec.StdoutCap
	if limit <= 0 {
		limit = DefaultStdoutCap
	}
	grace := spec.Grace
	if grace <= 0 {
		grace = DefaultGrace
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	out := &cappedBuffer{limit: limit, onFull: cancel}
	errTail := &tailBuffer{size: stderrTailSize}
	cmd := exec.CommandContext(runCtx, spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdin = spec.Stdin
	cmd.Stdout = out
	if spec.OnStdoutLine != nil {
		cmd.Stdout = &lineWriter{out: out, lines: lineSplitter{fn: spec.OnStdoutLine}}
	}
	cmd.Stderr = errTail
	cmd.WaitDelay = grace
	inOwnGroup(cmd)

	if err := cmd.Start(); err != nil {
		return ProcessResult{}, fmt.Errorf("start %s: %w", spec.Path, err)
	}
	waitErr := cmd.Wait()
	killGroup(cmd)
	res := ProcessResult{Stdout: out.bytes(), StderrTail: errTail.bytes(), ExitCode: -1}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	switch {
	case out.full():
		return res, ErrOutputTooLarge
	case ctx.Err() != nil:
		return res, fmt.Errorf("stop %s: %w", spec.Path, ctx.Err())
	}
	var exitErr *exec.ExitError
	switch {
	case waitErr == nil, errors.As(waitErr, &exitErr):
		return res, nil
	case errors.Is(waitErr, exec.ErrWaitDelay) && res.ExitCode == 0:
		// The child finished, and a grandchild kept standard output open past the grace
		// period. What the child wrote is complete.
		return res, nil
	}
	return res, fmt.Errorf("run %s: %w", spec.Path, waitErr)
}

type cappedBuffer struct {
	mu     sync.Mutex
	buf    []byte
	limit  int
	over   bool
	onFull func()
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.keep(p)
	return len(p), nil
}

// keep appends p and says whether it was kept, which it is not once the cap is reached.
func (b *cappedBuffer) keep(p []byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.over {
		return false
	}
	if len(b.buf)+len(p) > b.limit {
		b.over = true
		b.buf = nil
		b.onFull()
		return false
	}
	b.buf = append(b.buf, p...)
	return true
}

// lineWriter keeps standard output for the final read and passes its lines on. exec.Cmd calls
// Write from one goroutine and waits for it before Wait returns, so the splitter needs no lock,
// and the line function runs outside the lock of the buffer.
type lineWriter struct {
	out   *cappedBuffer
	lines lineSplitter
}

func (w *lineWriter) Write(p []byte) (int, error) {
	if w.out.keep(p) {
		w.lines.feed(p)
	}
	return len(p), nil
}

// lineSplitter holds at most MaxStdoutLine bytes of an unfinished line.
type lineSplitter struct {
	fn   func([]byte)
	buf  []byte
	skip bool // the current line is longer than MaxStdoutLine
}

func (s *lineSplitter) feed(p []byte) {
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			s.add(p)
			return
		}
		if !s.skip && len(s.buf) == 0 && i <= MaxStdoutLine {
			s.fn(bytes.TrimSuffix(p[:i], []byte("\r")))
		} else {
			s.add(p[:i])
			if !s.skip {
				s.fn(bytes.TrimSuffix(s.buf, []byte("\r")))
			}
		}
		s.buf, s.skip = s.buf[:0], false
		p = p[i+1:]
	}
}

func (s *lineSplitter) add(part []byte) {
	switch {
	case s.skip:
	case len(s.buf)+len(part) > MaxStdoutLine:
		s.skip, s.buf = true, nil
	default:
		s.buf = append(s.buf, part...)
	}
}

func (b *cappedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf
}

func (b *cappedBuffer) full() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.over
}

type tailBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if extra := len(b.buf) - b.size; extra > 0 {
		b.buf = append(b.buf[:0], b.buf[extra:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf...)
}
