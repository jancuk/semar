//go:build unix

package dashboard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

// script writes a shell script into a new directory and makes the claude provider use it
// as its sign-in command.
func (fx *fixture) script(body string) string {
	fx.t.Helper()
	path := filepath.Join(fx.t.TempDir(), "sign-in.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		fx.t.Fatal(err)
	}
	fx.instance("claude", func(cfg config.Provider) (provider.Instance, error) {
		return &fakeAccount{fakeInstance: fakeInstance{cfg: cfg, health: provider.Health{Installed: true}}, login: []string{path, "auth", "login"}}, nil
	})
	return path
}

func (fx *fixture) startLogin() jobBody {
	fx.t.Helper()
	a := fx.call("POST", "/api/providers/claude/login", "")
	want(fx.t, a, 200)
	var j jobBody
	a.json(fx.t, &j)
	if j.ID == "" || j.Provider != "claude" {
		fx.t.Fatalf("job %+v", j)
	}
	return j
}

func (fx *fixture) waitJob(id string, until func(jobBody) bool) jobBody {
	fx.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var j jobBody
		a := fx.call("GET", "/api/logins/"+id, "")
		want(fx.t, a, 200)
		a.json(fx.t, &j)
		if until(j) {
			return j
		}
		if time.Now().After(deadline) {
			fx.t.Fatalf("the job did not get there: %+v", j)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func finished(j jobBody) bool { return j.State != stateRunning }

func TestLoginRunsToTheEnd(t *testing.T) {
	fx := newFixture(t)
	marker := filepath.Join(t.TempDir(), "ran")
	fx.script(`echo "first line"
echo "args: $*"
echo "to stderr" >&2
echo "vendor: [$ANTHROPIC_API_KEY] semar: [$SEMAR_HOME] other: [$ORDERS_KEEP]"
echo "files: $(ls -A | wc -l | tr -d ' ')"
if read line; then echo "read: $line"; else echo "no input"; fi
touch ` + marker + `
`)
	fx.setEnv("ANTHROPIC_API_KEY", "sk-should-not-pass")
	fx.setEnv("SEMAR_HOME", "/x")
	fx.setEnv("ORDERS_KEEP", "kept")
	j := fx.waitJob(fx.startLogin().ID, finished)
	if j.State != stateDone || j.Detail == "" {
		t.Fatalf("job %+v", j)
	}
	got := strings.Join(j.Lines, "\n")
	for _, line := range []string{"first line", "args: auth login", "to stderr", "vendor: [] semar: [] other: [kept]", "files: 0", "no input"} {
		if !strings.Contains(got, line) {
			t.Errorf("no line %q in\n%s", line, got)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the command did not run")
	}
}

func TestLoginFails(t *testing.T) {
	fx := newFixture(t)
	fx.script("echo nope\nexit 3\n")
	j := fx.waitJob(fx.startLogin().ID, finished)
	if j.State != stateFailed || !strings.Contains(j.Detail, "exit code 3") {
		t.Fatalf("job %+v", j)
	}
}

func TestLoginCleansLines(t *testing.T) {
	fx := newFixture(t)
	fx.script(`printf 'a\033[31mred\033[0m\007b\tc\r\n'
printf '\033]0;title\007visible\n'
printf 'Signed in as person@example.com\n'
printf 'Open https://login.example.com/oauth?code_challenge=AbCdEfGhIjKlMnOpQrStUvWxYz0123456789&state=xyz\n'
printf 'progress 1\rprogress 2\n'
head -c 5000 /dev/zero | tr '\0' 'x'; echo
i=0; while [ $i -lt 250 ]; do echo "line $i"; i=$((i+1)); done
`)
	j := fx.waitJob(fx.startLogin().ID, finished)
	if j.State != stateDone {
		t.Fatalf("job %+v", j)
	}
	if len(j.Lines) != keptLines || j.Lines[len(j.Lines)-1] != "line 249" || j.Lines[0] != "line 50" {
		t.Fatalf("kept %d lines, from %q to %q", len(j.Lines), j.Lines[0], j.Lines[len(j.Lines)-1])
	}

	fx.script(`printf 'a\033[31mred\033[0m\007b\tc\r\n'
printf '\033]0;title\007visible\n'
printf 'Signed in as person@example.com\n'
printf 'Open https://login.example.com/oauth?code_challenge=AbCdEfGhIjKlMnOpQrStUvWxYz0123456789&state=xyz\n'
head -c 5000 /dev/zero | tr '\0' 'x'; echo
`)
	j = fx.waitJob(fx.startLogin().ID, finished)
	want := []string{
		"aredb c",
		"visible",
		"Signed in as [e-mail address]",
		"Open https://login.example.com/oauth?code_challenge=AbCdEfGhIjKlMnOpQrStUvWxYz0123456789&state=xyz",
		strings.Repeat("x", maxLineRune),
	}
	if strings.Join(j.Lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines\n%q\nwant\n%q", j.Lines, want)
	}
	for _, line := range j.Lines {
		for _, r := range line {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("a control character is left in %q", line)
			}
		}
	}
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func readPIDs(t *testing.T, path string) []int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(path)
		fields := strings.Fields(string(data))
		if len(fields) == 2 {
			var pids []int
			for _, f := range fields {
				n, err := strconv.Atoi(f)
				if err != nil {
					t.Fatal(err)
				}
				pids = append(pids, n)
			}
			return pids
		}
		if time.Now().After(deadline) {
			t.Fatal("the script wrote no process IDs")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitGone(t *testing.T, pids []int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for _, pid := range pids {
		for alive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("process %d is still running", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// sleeper is a script that starts a child, writes both process IDs, and waits.
func sleeper(pidFile string) string {
	return `trap '' TERM
sleep 60 &
echo "$$ $!" > ` + pidFile + `.tmp && mv ` + pidFile + `.tmp ` + pidFile + `
echo waiting
wait
`
}

func TestLoginCancelAndSecondStart(t *testing.T) {
	fx := newFixture(t)
	pidFile := filepath.Join(t.TempDir(), "pids")
	fx.script(sleeper(pidFile))
	first := fx.startLogin()
	pids := readPIDs(t, pidFile)
	if second := fx.startLogin(); second.ID != first.ID {
		t.Fatalf("a second start made job %s besides %s", second.ID, first.ID)
	}
	fx.waitJob(first.ID, func(j jobBody) bool { return len(j.Lines) > 0 })

	a := fx.call("DELETE", "/api/logins/"+first.ID, "")
	want(t, a, 200)
	var j jobBody
	a.json(t, &j)
	if j.State != stateCancelled || j.Lines[0] != "waiting" {
		t.Fatalf("job %+v", j)
	}
	waitGone(t, pids)
	want(t, fx.call("DELETE", "/api/logins/"+first.ID, ""), 200)
	want(t, fx.call("GET", "/api/logins/nothing", ""), 404)

	if third := fx.startLogin(); third.ID == first.ID {
		t.Fatal("a start after the end returned the old job")
	}
}

func TestLoginTimeLimit(t *testing.T) {
	fx := newFixture(t, func(s *Server) { s.loginLimit = 2 * time.Second })
	pidFile := filepath.Join(t.TempDir(), "pids")
	fx.script(sleeper(pidFile))
	j := fx.startLogin()
	pids := readPIDs(t, pidFile)
	j = fx.waitJob(j.ID, finished)
	if j.State != stateFailed || !strings.Contains(j.Detail, "did not finish") {
		t.Fatalf("job %+v", j)
	}
	waitGone(t, pids)
}

func TestLoginRefusals(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	fx.instance("claude", func(cfg config.Provider) (provider.Instance, error) {
		return &fakeAccount{fakeInstance: fakeInstance{cfg: cfg}}, nil
	})
	want(t, fx.call("POST", "/api/providers/claude/login", ""), 400)
	want(t, fx.call("POST", "/api/providers/deepseek/login", "{}"), 400)
	want(t, fx.call("POST", "/api/providers/nobody/login", ""), 404)
	fx.instance("claude", func(cfg config.Provider) (provider.Instance, error) {
		return &fakeInstance{cfg: cfg}, nil
	})
	want(t, fx.call("POST", "/api/providers/claude/login", ""), 400)
	fx.instance("claude", func(cfg config.Provider) (provider.Instance, error) {
		return &fakeAccount{fakeInstance: fakeInstance{cfg: cfg}, login: []string{filepath.Join(t.TempDir(), "missing")}}, nil
	})
	want(t, fx.call("POST", "/api/providers/claude/login", ""), 500)
}

func TestServeStopsJobs(t *testing.T) {
	fx := newFixture(t)
	pidFile := filepath.Join(t.TempDir(), "pids")
	fx.script(sleeper(pidFile))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fx.s.Serve(ctx, ln) }()

	base := "http://" + ln.Addr().String()
	req, _ := http.NewRequest("POST", base+"/api/providers/claude/login", nil)
	req.Header.Set(TokenHeader, fx.s.Token())
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	pids := readPIDs(t, pidFile)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not stop")
	}
	waitGone(t, pids)
	for _, j := range fx.s.logins.jobs {
		if b := j.body(); b.State != stateCancelled {
			t.Fatalf("job %+v", b)
		}
	}
}
