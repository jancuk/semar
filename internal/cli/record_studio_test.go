package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/localhttp"
)

type studioRun struct {
	url    string
	token  string
	host   string
	stderr *lockedBuffer
	cancel context.CancelFunc
	done   chan error
}

// startStudio runs semar record --ui as a command, and waits for the address that it prints.
func startStudio(t *testing.T, dir string, open func(string) error, args ...string) *studioRun {
	t.Helper()
	o := options{
		LookupEnv:   func(string) (string, bool) { return "", false },
		OpenBrowser: open,
	}.withDefaults()
	run := &studioRun{stderr: &lockedBuffer{}, done: make(chan error, 1)}
	stdout := &lockedBuffer{}
	cmd := newRecordCommand(o)
	cmd.SetArgs(append([]string{"--ui", "--port", "0", "--dir", dir}, args...))
	cmd.SetOut(stdout)
	cmd.SetErr(run.stderr)
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	go func() { run.done <- cmd.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-run.done:
		case <-time.After(10 * time.Second):
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if line, _, ok := strings.Cut(stdout.String(), "\n"); ok {
			run.url = line
			break
		}
		select {
		case err := <-run.done:
			t.Fatalf("the command ended: %v, stderr %q", err, run.stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no address was printed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	u, err := url.Parse(run.url)
	if err != nil {
		t.Fatal(err)
	}
	run.host, run.token = u.Host, strings.TrimPrefix(u.Fragment, "token=")
	return run
}

func (r *studioRun) call(t *testing.T, method, path, token, body string) (int, string) {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	var req *http.Request
	var err error
	if rd != nil {
		req, err = http.NewRequest(method, "http://"+r.host+path, rd)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, "http://"+r.host+path, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(localhttp.TokenHeader, token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return res.StatusCode, b.String()
}

func TestRecordUIServesTheStudioAndStopsOnInterrupt(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var opened []string
	run := startStudio(t, dir, func(u string) error {
		mu.Lock()
		opened = append(opened, u)
		mu.Unlock()
		return nil
	}, "--no-open")

	if !strings.HasPrefix(run.url, "http://127.0.0.1:") || run.token == "" || !strings.Contains(run.url, "/#token=") {
		t.Fatalf("address = %q", run.url)
	}
	if !strings.Contains(run.stderr.String(), "Ctrl+C") {
		t.Errorf("stderr = %q", run.stderr.String())
	}
	if code, _ := run.call(t, "GET", "/", "", ""); code != 200 {
		t.Errorf("the page = %d", code)
	}
	if code, _ := run.call(t, "GET", "/api/state", "", ""); code != 403 {
		t.Errorf("no token = %d, want 403", code)
	}
	// A repository with no configuration gets a studio, and the guided setup makes the configuration.
	code, body := run.call(t, "GET", "/api/state", run.token, "")
	var st struct {
		HasConfig bool `json:"hasConfig"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &st) != nil || st.HasConfig {
		t.Fatalf("state = %d %s", code, body)
	}
	code, body = run.call(t, "POST", "/api/scaffold", run.token, `{"name":"sinau","package":"com.example.sinau"}`)
	if code != 200 || !strings.Contains(body, `"hasConfig":true`) || !strings.Contains(body, `"service":"sinau"`) {
		t.Fatalf("scaffold = %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, ".semar", "semar.yaml")); err != nil {
		t.Error(err)
	}
	mu.Lock()
	if len(opened) != 0 {
		t.Errorf("--no-open opened %v", opened)
	}
	mu.Unlock()

	// Ctrl+C ends the command cleanly.
	run.cancel()
	select {
	case err := <-run.done:
		if err != nil {
			t.Errorf("the studio ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the studio did not stop")
	}
}

func TestRecordUIOpensTheBrowserWithTheAddress(t *testing.T) {
	var mu sync.Mutex
	var opened []string
	run := startStudio(t, t.TempDir(), func(u string) error {
		mu.Lock()
		opened = append(opened, u)
		mu.Unlock()
		return nil
	})
	if code, _ := run.call(t, "GET", "/", "", ""); code != 200 {
		t.Fatalf("the page = %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(opened) != 1 || opened[0] != run.url {
		t.Errorf("opened %v, want [%s]", opened, run.url)
	}
}

func TestRecordUIRefusesFlowAndUse(t *testing.T) {
	o := options{LookupEnv: func(string) (string, bool) { return "", false }}.withDefaults()
	for _, args := range [][]string{{"--ui", "--flow", "login"}, {"--ui", "--use", "login"}} {
		cmd := newRecordCommand(o)
		cmd.SetArgs(append(args, "--dir", t.TempDir(), "--no-open"))
		cmd.SetOut(&lockedBuffer{})
		cmd.SetErr(&lockedBuffer{})
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "leave out --flow and --use") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}
