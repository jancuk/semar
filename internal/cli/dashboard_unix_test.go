//go:build unix

package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/dashboard"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/provider"
)

type signInInstance struct {
	dashboardInstance
	argv []string
}

func (s signInInstance) Status(context.Context) provider.AccountStatus {
	return provider.AccountStatus{}
}
func (s signInInstance) LoginCommand() []string { return s.argv }

// A closed terminal or a kill stops the dashboard as an interrupt does, and the running
// sign-in ends with it.
func TestDashboardStopsOnSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			if sig == syscall.SIGHUP && signal.Ignored(syscall.SIGHUP) {
				t.Skip("the test runs with the hangup ignored")
			}
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "pids")
			script := filepath.Join(dir, "sign-in")
			body := "#!/bin/sh\ntrap '' TERM HUP\nsleep 60 &\necho \"$$ $!\" > " + pidFile + ".tmp && mv " + pidFile + ".tmp " + pidFile + "\nwait\n"
			if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			reg := provider.NewRegistry()
			reg.Register(provider.KindClaude, func(cfg config.Provider) (provider.Instance, error) {
				return signInInstance{dashboardInstance{cfg}, []string{script}}, nil
			})
			home := filepath.Join(dir, "home")
			o := options{
				LookupEnv: func(name string) (string, bool) {
					if name == "SEMAR_HOME" {
						return home, true
					}
					return "", false
				},
				Providers: func(string) *provider.Registry { return reg },
			}.withDefaults()
			sys := dashboardSystem{keys: keychain.Memory(), environ: func() []string { return []string{"PATH=" + os.Getenv("PATH")} }, open: func(string) error { return nil }}
			stdout := &lockedBuffer{}
			cmd := dashboardCommand(o, sys)
			cmd.SetArgs([]string{"--no-open"})
			cmd.SetOut(stdout)
			cmd.SetErr(&lockedBuffer{})
			done := make(chan error, 1)
			go func() { done <- cmd.ExecuteContext(context.Background()) }()

			var address string
			for deadline := time.Now().Add(5 * time.Second); address == ""; time.Sleep(10 * time.Millisecond) {
				if line, _, ok := strings.Cut(stdout.String(), "\n"); ok {
					address = line
				} else if time.Now().After(deadline) {
					t.Fatal("no address was printed")
				}
			}
			u, err := url.Parse(address)
			if err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest("POST", "http://"+u.Host+"/api/providers/claude/login", bytes.NewReader(nil))
			req.Header.Set(dashboard.TokenHeader, strings.TrimPrefix(u.Fragment, "token="))
			req.Header.Set("Content-Type", "application/json")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Fatalf("sign-in status %d", res.StatusCode)
			}
			var pids []int
			for deadline := time.Now().Add(5 * time.Second); len(pids) < 2; time.Sleep(20 * time.Millisecond) {
				data, _ := os.ReadFile(pidFile)
				pids = pids[:0]
				for _, f := range strings.Fields(string(data)) {
					if n, err := strconv.Atoi(f); err == nil {
						pids = append(pids, n)
					}
				}
				if len(pids) < 2 && time.Now().After(deadline) {
					t.Fatal("the sign-in wrote no process IDs")
				}
			}

			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the dashboard did not stop")
			}
			for _, pid := range pids {
				for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
					if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("process %d of the sign-in is still running", pid)
					}
				}
			}
		})
	}
}
