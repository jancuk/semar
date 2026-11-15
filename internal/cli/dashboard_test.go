package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/dashboard"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/provider"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type dashboardInstance struct{ cfg config.Provider }

func (d dashboardInstance) ID() string   { return d.cfg.ID }
func (d dashboardInstance) Kind() string { return d.cfg.Kind }
func (d dashboardInstance) Health(context.Context) provider.Health {
	return provider.Health{Installed: true, Version: "test"}
}
func (d dashboardInstance) Run(context.Context, provider.Job) (provider.Result, error) {
	return provider.Result{}, errors.New("no runs in this test")
}

type dashboardRun struct {
	url    string
	opened []string
	stderr *lockedBuffer
	cancel context.CancelFunc
	done   chan error
}

func startDashboard(t *testing.T, open func(string) error, args ...string) *dashboardRun {
	t.Helper()
	home := t.TempDir()
	reg := provider.NewRegistry()
	reg.Register(provider.KindClaude, func(cfg config.Provider) (provider.Instance, error) { return dashboardInstance{cfg}, nil })
	o := options{
		LookupEnv: func(name string) (string, bool) {
			if name == "SEMAR_HOME" {
				return home, true
			}
			return "", false
		},
		Providers: func(string) *provider.Registry { return reg },
	}.withDefaults()
	run := &dashboardRun{stderr: &lockedBuffer{}, done: make(chan error, 1)}
	var mu sync.Mutex
	sys := dashboardSystem{keys: keychain.Memory(), environ: func() []string { return nil }, open: func(u string) error {
		mu.Lock()
		run.opened = append(run.opened, u)
		mu.Unlock()
		return open(u)
	}}
	stdout := &lockedBuffer{}
	cmd := dashboardCommand(o, sys)
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(run.stderr)
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	go func() { run.done <- cmd.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-run.done
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
	// The command opens the browser before it serves, so an answer means open has returned.
	u, err := url.Parse(run.url)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := dashboardGet(t, "http://"+u.Host+"/", "", nil); code != 200 {
		t.Fatalf("page status %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	run.opened = append([]string(nil), run.opened...)
	return run
}

func dashboardGet(t *testing.T, address, token string, edit func(*http.Request)) (int, string) {
	t.Helper()
	req, err := http.NewRequest("GET", address, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(dashboard.TokenHeader, token)
	}
	if edit != nil {
		edit(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	return res.StatusCode, buf.String()
}

func TestDashboardServesUntilCancelled(t *testing.T) {
	run := startDashboard(t, func(string) error { return nil })
	u, err := url.Parse(run.url)
	if err != nil || u.Scheme != "http" || !strings.HasPrefix(u.Host, "127.0.0.1:") || u.Path != "/" || !strings.HasPrefix(u.Fragment, "token=") {
		t.Fatalf("address %q", run.url)
	}
	if len(run.opened) != 1 || run.opened[0] != run.url {
		t.Fatalf("opened %q", run.opened)
	}
	token := strings.TrimPrefix(u.Fragment, "token=")
	base := "http://" + u.Host

	code, body := dashboardGet(t, base+"/api/state", token, nil)
	var st struct {
		Default   string `json:"default"`
		Exists    bool   `json:"exists"`
		Providers []struct {
			ID     string `json:"id"`
			Health struct {
				Version string `json:"version"`
			} `json:"health"`
		} `json:"providers"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &st) != nil || st.Default != "claude" || st.Exists || st.Providers[0].Health.Version != "test" {
		t.Fatalf("state %d %s", code, body)
	}
	for name, c := range map[string]struct {
		token string
		edit  func(*http.Request)
	}{
		"no token":       {"", nil},
		"wrong token":    {"0" + token[1:], nil},
		"foreign host":   {token, func(r *http.Request) { r.Host = "orders.example.com" }},
		"foreign origin": {token, func(r *http.Request) { r.Header.Set("Origin", "https://orders.example.com") }},
	} {
		if code, _ := dashboardGet(t, base+"/api/state", c.token, c.edit); code != 403 {
			t.Errorf("%s: status %d", name, code)
		}
	}
	if code, body := dashboardGet(t, base+"/", "", nil); code != 200 || !strings.Contains(body, "app.js") {
		t.Fatalf("page %d", code)
	}

	run.cancel()
	select {
	case err := <-run.done:
		if err != nil {
			t.Fatal(err)
		}
		run.done <- nil
	case <-time.After(10 * time.Second):
		t.Fatal("the command did not stop")
	}
	if _, err := net.DialTimeout("tcp", u.Host, time.Second); err == nil {
		t.Fatal("the port still answers")
	}
}

func TestDashboardNoOpen(t *testing.T) {
	run := startDashboard(t, func(string) error { t.Error("a browser was opened"); return nil }, "--no-open")
	if len(run.opened) != 0 {
		t.Fatalf("opened %q", run.opened)
	}
}

func TestDashboardOpenFailureIsANotice(t *testing.T) {
	run := startDashboard(t, func(string) error { return errors.New("no opener") })
	if !strings.Contains(run.stderr.String(), "no browser was opened") {
		t.Fatalf("stderr %q", run.stderr.String())
	}
	u, _ := url.Parse(run.url)
	if code, _ := dashboardGet(t, "http://"+u.Host+"/api/state", strings.TrimPrefix(u.Fragment, "token="), nil); code != 200 {
		t.Fatalf("status %d", code)
	}
}

func TestDashboardPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	busy := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	run := startDashboard(t, func(string) error { return nil }, "--port", strconv.Itoa(busy), "--no-open")
	if !strings.HasPrefix(run.url, "http://127.0.0.1:"+strconv.Itoa(busy)+"/#token=") {
		t.Fatalf("address %q", run.url)
	}

	o := options{LookupEnv: func(string) (string, bool) { return t.TempDir(), true }, Providers: func(string) *provider.Registry { return provider.NewRegistry() }}.withDefaults()
	sys := dashboardSystem{keys: keychain.Memory(), environ: func() []string { return nil }, open: func(string) error { return nil }}
	for _, args := range [][]string{{"--port", "-1"}, {"--port", "70000"}, {"--port", strconv.Itoa(busy), "--no-open"}} {
		cmd := dashboardCommand(o, sys)
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := cmd.ExecuteContext(ctx)
		cancel()
		if err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}
