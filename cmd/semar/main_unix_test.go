//go:build unix

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

const helperEnv = "SEMAR_MAIN_SIGNAL_HELPER"

// TestHelperProcess waits for a stop signal the way main does. It does nothing in a normal run.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals()...)
	defer stop()
	fmt.Println("ready")
	select {
	case <-ctx.Done():
		fmt.Println("stopped")
		os.Exit(0)
	case <-time.After(30 * time.Second):
		os.Exit(3)
	}
}

func TestStopSignalsCancelTheCommand(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
			cmd.Env = append(os.Environ(), helperEnv+"=1")
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			lines := bufio.NewScanner(out)
			if !lines.Scan() || lines.Text() != "ready" {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("the helper did not start: %q", lines.Text())
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			var rest []string
			for lines.Scan() {
				rest = append(rest, lines.Text())
			}
			err = cmd.Wait()
			if err != nil || !strings.Contains(strings.Join(rest, "\n"), "stopped") {
				t.Fatalf("after %v: %v, output %q", sig, err, rest)
			}
		})
	}
}
