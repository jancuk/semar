//go:build unix

package provider_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/provider"
)

const helperEnv = "SEMAR_PROVIDER_HELPER"

// TestHelperProcess is the child that the tests below start. It does nothing in a normal run.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		return
	}
	switch mode {
	case "stubborn":
		signal.Ignore(syscall.SIGTERM)
		gc := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
		gc.Env = append(os.Environ(), helperEnv+"=sleep")
		gc.Stdout = os.Stdout
		if err := gc.Start(); err != nil {
			os.Exit(2)
		}
		pids := strconv.Itoa(os.Getpid()) + "\n" + strconv.Itoa(gc.Process.Pid) + "\n"
		if err := os.WriteFile(os.Getenv("SEMAR_PIDFILE"), []byte(pids), 0o600); err != nil {
			os.Exit(2)
		}
		select {}
	case "sleep":
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Minute)
	case "echo":
		b := make([]byte, 64)
		n, _ := os.Stdin.Read(b)
		os.Stdout.WriteString("in:" + string(b[:n]))
		os.Stderr.WriteString("line1\nline2\n")
		os.Exit(7)
	case "flood":
		buf := make([]byte, 1<<20)
		for {
			if _, err := os.Stdout.Write(buf); err != nil {
				os.Exit(0)
			}
		}
	case "stderr":
		os.Stderr.WriteString(strings.Repeat("a", 6000) + "END")
	}
	os.Exit(0)
}

func helperSpec(mode string, extra ...string) provider.ProcessSpec {
	return provider.ProcessSpec{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestHelperProcess$"},
		Env:  append([]string{helperEnv + "=" + mode}, extra...),
	}
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestRunProcessStopsTheWholeGroup(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pids")
	spec := helperSpec("stubborn", "SEMAR_PIDFILE="+pidfile)
	spec.Grace = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := provider.RunProcess(ctx, spec); done <- err }()

	var pids []int
	deadline := time.Now().Add(10 * time.Second)
	for len(pids) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("the helper did not start")
		}
		time.Sleep(20 * time.Millisecond)
		data, _ := os.ReadFile(pidfile)
		pids = pids[:0]
		for f := range strings.FieldsSeq(string(data)) {
			if n, err := strconv.Atoi(f); err == nil {
				pids = append(pids, n)
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunProcess did not return")
	}
	for _, pid := range pids {
		for i := 0; alive(pid); i++ {
			if i > 200 {
				t.Fatalf("process %d is still alive", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestRunProcessTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	spec := helperSpec("sleep")
	spec.Grace = 100 * time.Millisecond
	_, err := provider.RunProcess(ctx, spec)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestRunProcessOutput(t *testing.T) {
	spec := helperSpec("echo")
	spec.Stdin = strings.NewReader("hello")
	res, err := provider.RunProcess(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "in:hello" || res.ExitCode != 7 || string(res.StderrTail) != "line1\nline2\n" {
		t.Fatalf("got %q %d %q", res.Stdout, res.ExitCode, res.StderrTail)
	}
}

func TestRunProcessStderrKeepsTheTail(t *testing.T) {
	res, err := provider.RunProcess(context.Background(), helperSpec("stderr"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.StderrTail) != 4096 || !strings.HasSuffix(string(res.StderrTail), "aaaEND") {
		t.Fatalf("tail has %d bytes", len(res.StderrTail))
	}
}

func TestRunProcessOutputCap(t *testing.T) {
	spec := helperSpec("flood")
	spec.StdoutCap = 3 << 20
	spec.Grace = time.Second
	_, err := provider.RunProcess(context.Background(), spec)
	if !errors.Is(err, provider.ErrOutputTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func TestRunProcessMissingBinary(t *testing.T) {
	_, err := provider.RunProcess(context.Background(), provider.ProcessSpec{Path: filepath.Join(t.TempDir(), "nope")})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}
