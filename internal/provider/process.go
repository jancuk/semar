package provider

import (
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
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.over {
		return len(p), nil
	}
	if len(b.buf)+len(p) > b.limit {
		b.over = true
		b.buf = nil
		b.onFull()
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
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
