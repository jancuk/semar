//go:build unix

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
)

const helperEnv = "SEMAR_MAIN_SIGNAL_HELPER"

// TestHelperProcess waits for a stop signal the way main does, and answers each line of its
// input, so that a test can tell that a signal sent before the line was handled. It does
// nothing in a normal run.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		return
	}
	ignored := signal.Ignored(syscall.SIGHUP)
	ctx, stop := signal.NotifyContext(context.Background(), cli.StopSignals()...)
	defer stop()
	lines := make(chan string)
	go func() {
		in := bufio.NewScanner(os.Stdin)
		for in.Scan() {
			lines <- in.Text()
		}
	}()
	fmt.Printf("ready, hangup ignored: %v\n", ignored)
	for {
		select {
		case <-ctx.Done():
			fmt.Println("stopped:", context.Cause(ctx))
			os.Exit(0)
		case l := <-lines:
			fmt.Println("pong", l)
		case <-time.After(30 * time.Second):
			os.Exit(3)
		}
	}
}

type helper struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	lines *bufio.Scanner
}

func startHelper(t *testing.T, ignoreHangup bool) *helper {
	t.Helper()
	args := []string{os.Args[0], "-test.run=^TestHelperProcess$"}
	if ignoreHangup {
		args = append([]string{"/bin/sh", "-c", `trap "" HUP; exec "$0" "$@"`}, args...)
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &helper{cmd: cmd, in: in, lines: bufio.NewScanner(out)}
	want := fmt.Sprintf("ready, hangup ignored: %v", ignoreHangup)
	if !h.lines.Scan() || h.lines.Text() != want {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("the helper started with %q, want %q", h.lines.Text(), want)
	}
	return h
}

// signal sends sig, then waits until the helper has answered a line.
func (h *helper) signal(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := h.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(h.in, sig); err != nil {
		t.Fatal(err)
	}
}

// end returns the rest of the output and the exit error.
func (h *helper) end() (string, error) {
	_ = h.in.Close()
	var rest []string
	for h.lines.Scan() {
		rest = append(rest, h.lines.Text())
	}
	return strings.Join(rest, "\n"), h.cmd.Wait()
}

func TestStopSignalsCancelTheCommand(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			h := startHelper(t, false)
			h.signal(t, sig)
			out, err := h.end()
			if err != nil || !strings.Contains(out, "stopped: "+sig.String()) {
				t.Fatalf("after %v: %v, output %q", sig, err, out)
			}
		})
	}
}

func TestAnIgnoredHangupStaysIgnored(t *testing.T) {
	h := startHelper(t, true)
	h.signal(t, syscall.SIGHUP)
	if !h.lines.Scan() || h.lines.Text() != "pong hangup" {
		_ = h.cmd.Process.Kill()
		_, err := h.end()
		t.Fatalf("after a hangup the helper wrote %q (%v), want an answer", h.lines.Text(), err)
	}
	h.signal(t, syscall.SIGTERM)
	out, err := h.end()
	if err != nil || strings.Contains(out, "hangup signal") || !strings.Contains(out, "stopped: terminated") {
		t.Fatalf("%v, output %q", err, out)
	}
}
