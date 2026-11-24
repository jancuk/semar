package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/mcp"
)

// syncBuffer is a buffer that a test reads while the command still writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// mcpProc is one semar mcp that runs in the test, on pipes.
type mcpProc struct {
	t      *testing.T
	in     *io.PipeWriter
	lines  chan string
	stderr *syncBuffer
	done   chan int
	cancel context.CancelFunc
	next   int
}

// startMCP runs semar mcp with the options and arguments. Its standard input is a pipe, so that a test
// ends the server the way a client does, with end of file.
func startMCP(t *testing.T, opts cli.Options, args ...string) *mcpProc {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	opts.Stdin = inR
	ctx, cancel := context.WithCancel(t.Context())
	p := &mcpProc{t: t, in: inW, lines: make(chan string, 64), stderr: &syncBuffer{}, done: make(chan int, 1), cancel: cancel}
	go func() {
		p.done <- cli.RunWith(ctx, append([]string{"mcp"}, args...), outW, p.stderr, opts)
		_ = outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 32<<20)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
		close(p.lines)
	}()
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
	})
	return p
}

// request sends a JSON-RPC request and gives the result of its response. Every line of standard output
// must be one JSON message, which is how a client reads it.
func (p *mcpProc) request(method string, params any) map[string]any {
	p.t.Helper()
	p.next++
	id := p.next
	msg, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		p.t.Fatal(err)
	}
	if _, err := p.in.Write(append(msg, '\n')); err != nil {
		p.t.Fatalf("send %s: %v", method, err)
	}
	timeout := time.After(60 * time.Second)
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				p.t.Fatalf("standard output ended before the answer to %s\nstderr:\n%s", method, p.stderr)
			}
			var m struct {
				ID     *int           `json:"id"`
				Result map[string]any `json:"result"`
				Error  map[string]any `json:"error"`
			}
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				p.t.Fatalf("standard output holds a line that is not JSON: %q", line)
			}
			if m.ID == nil || *m.ID != id {
				continue
			}
			if m.Error != nil {
				p.t.Fatalf("%s failed: %v", method, m.Error)
			}
			return m.Result
		case <-timeout:
			p.t.Fatalf("no answer to %s\nstderr:\n%s", method, p.stderr)
		}
	}
}

func (p *mcpProc) initialize() {
	p.t.Helper()
	res := p.request("initialize", map[string]any{
		"protocolVersion": mcp.SupportedVersions()[0], "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "1"},
	})
	if info, _ := res["serverInfo"].(map[string]any); info["name"] != "semar" {
		p.t.Fatalf("serverInfo = %v", res["serverInfo"])
	}
	note, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if _, err := p.in.Write(append(note, '\n')); err != nil {
		p.t.Fatal(err)
	}
}

func (p *mcpProc) toolNames() []string {
	p.t.Helper()
	res := p.request("tools/list", map[string]any{})
	var names []string
	for _, tool := range res["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	return names
}

// call runs a tool and gives its text and whether it is an error.
func (p *mcpProc) call(name string, args map[string]any) (string, bool) {
	p.t.Helper()
	res := p.request("tools/call", map[string]any{"name": name, "arguments": args})
	var text strings.Builder
	for _, c := range res["content"].([]any) {
		if m := c.(map[string]any); m["type"] == "text" {
			text.WriteString(m["text"].(string))
		}
	}
	isErr, _ := res["isError"].(bool)
	return text.String(), isErr
}

// finish closes the standard input and waits for the exit code.
func (p *mcpProc) finish() int {
	p.t.Helper()
	_ = p.in.Close()
	select {
	case code := <-p.done:
		return code
	case <-time.After(60 * time.Second):
		p.t.Fatalf("semar mcp did not end\nstderr:\n%s", p.stderr)
		return -1
	}
}

func hasTool(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestMCPStandaloneFlow(t *testing.T) {
	r := localRun(t, androidOptions{})
	bare := t.TempDir()
	p := startMCP(t, r.opts, "--dir", bare)
	p.initialize()

	names := p.toolNames()
	if !hasTool(names, "mobile_list_elements_on_screen") || !hasTool(names, "mobile_click_on_screen_at_coordinates") {
		t.Fatalf("the mobile tools are missing: %v", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "semar_") || strings.HasPrefix(n, "web_") || strings.HasPrefix(n, "api_") {
			t.Errorf("standalone mode lists %s", n)
		}
	}

	text, isErr := p.call("mobile_list_elements_on_screen", map[string]any{"format": "json"})
	if isErr {
		t.Fatalf("list elements: %s\nstderr:\n%s", text, p.stderr)
	}
	var list struct {
		Elements []struct {
			Ref  string `json:"ref"`
			Desc string `json:"desc"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		t.Fatalf("the listing is not JSON: %v\n%s", err, text)
	}
	ref := ""
	for _, e := range list.Elements {
		if e.Desc == "Sign in" {
			ref = e.Ref
		}
	}
	if ref == "" {
		t.Fatalf("no Sign in button in %s", text)
	}
	if text, isErr := p.call("mobile_click_on_screen_at_coordinates", map[string]any{"ref": ref}); isErr {
		t.Fatalf("click: %s", text)
	}
	if r.taps.Load() != 1 {
		t.Errorf("the button was tapped %d times, want 1", r.taps.Load())
	}
	if text, isErr := p.call("mobile_take_screenshot", map[string]any{}); isErr {
		t.Fatalf("screenshot: %s", text)
	}

	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d, want %d\nstderr:\n%s", code, cli.ExitOK, p.stderr)
	}
	if r.server.Deleted() != 1 {
		t.Errorf("the session was deleted %d times, want 1", r.server.Deleted())
	}
	assertDeviceLeaseFree(t, r.opts.Home)

	stderr := p.stderr.String()
	contains(t, "stderr", stderr, "adb: ", "(SEMAR_ADB_BIN)", "standalone mode", "mobile_click_on_screen_at_coordinates ", " ok")
	// The log names the tool, never its arguments.
	if strings.Contains(stderr, ref+`"`) || strings.Contains(stderr, `"ref"`) {
		t.Errorf("the audit log shows arguments:\n%s", stderr)
	}
}

func TestMCPRepoModeWithAProductionEnvironmentOnlyObserves(t *testing.T) {
	r := localRun(t, androidOptions{production: true})
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local")
	p.initialize()

	names := p.toolNames()
	if !hasTool(names, "mobile_click_on_screen_at_coordinates") {
		t.Fatalf("the write tools are not listed: %v", names)
	}
	// Reading still works.
	if text, isErr := p.call("mobile_list_elements_on_screen", map[string]any{}); isErr {
		t.Fatalf("a read tool failed: %s\nstderr:\n%s", text, p.stderr)
	}
	writes := map[string]map[string]any{
		"mobile_click_on_screen_at_coordinates": {"x": 100, "y": 950},
		"mobile_type_keys":                      {"text": "hello"},
		"mobile_press_button":                   {"button": "HOME"},
		"mobile_launch_app":                     {"packageName": androidPackage},
		"mobile_open_url":                       {"url": "https://example.com/"},
		"mobile_clear_app_data":                 {"packageName": androidPackage, "confirm": true},
	}
	for name, args := range writes {
		text, isErr := p.call(name, args)
		if !isErr || !strings.Contains(text, "production") {
			t.Errorf("%s: isError=%v, text %q, want an error that says production", name, isErr, text)
		}
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
	if r.taps.Load() != 0 || len(r.server.Touches()) != 0 {
		t.Errorf("a tap reached the device: taps %d, touches %v", r.taps.Load(), r.server.Touches())
	}
	for _, line := range r.adb.Log() {
		for _, write := range []string{"input", "install", "'am' 'start'", "'pm' 'clear'", "monkey"} {
			if strings.Contains(line, write) {
				t.Errorf("adb saw a write call: %s", line)
			}
		}
	}
	contains(t, "stderr", p.stderr.String(), "repository mode (environment local", "observing only")
}

func TestMCPProductionNeedsAnExplicitEnvironment(t *testing.T) {
	r := localRun(t, androidOptions{production: true})
	var out bytes.Buffer
	errw := &syncBuffer{}
	opts := r.opts
	opts.Stdin = strings.NewReader("")
	code := cli.RunWith(t.Context(), []string{"mcp", "--dir", r.dir}, &out, errw, opts)
	if code != cli.ExitError {
		t.Fatalf("exit %d, want %d", code, cli.ExitError)
	}
	contains(t, "stderr", errw.String(), "production", "--env local")
	if out.Len() != 0 {
		t.Errorf("standard output holds %q on a failed start", out.String())
	}
}

func TestMCPReadOnlyFlagRefusesWritesInAnyMode(t *testing.T) {
	r := localRun(t, androidOptions{})
	p := startMCP(t, r.opts, "--dir", t.TempDir(), "--read-only")
	p.initialize()
	text, isErr := p.call("mobile_press_button", map[string]any{"button": "HOME"})
	if !isErr || !strings.Contains(text, "--read-only") {
		t.Errorf("isError=%v, text %q, want a refusal that names --read-only", isErr, text)
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
}

var (
	httpAddrLine  = regexp.MustCompile(`address: (http://127\.0\.0\.1:\d+/mcp)`)
	httpTokenLine = regexp.MustCompile(`token: ([0-9a-f]{64})`)
)

func TestMCPHTTP(t *testing.T) {
	r := localRun(t, androidOptions{})
	opts := r.opts
	opts.Stdin = strings.NewReader("")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errw := &syncBuffer{}
	var out bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- cli.RunWith(ctx, []string{"mcp", "--dir", t.TempDir(), "--http", "127.0.0.1:0"}, &out, errw, opts)
	}()

	var url, token string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s := errw.String()
		a, k := httpAddrLine.FindStringSubmatch(s), httpTokenLine.FindStringSubmatch(s)
		if a != nil && k != nil {
			url, token = a[1], k[1]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if url == "" {
		t.Fatalf("no address and token on standard error:\n%s", errw)
	}
	contains(t, "stderr", errw.String(), `"mcpServers"`, "Bearer "+token)

	post := func(token, host, session, body string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if host != "" {
			req.Host = host
		}
		if session != "" {
			req.Header.Set(mcp.SessionHeader, session)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp, string(data)
	}
	initBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, mcp.SupportedVersions()[0])

	if resp, _ := post("wrong-token", "", "", initBody); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a wrong token gave %d, want 401", resp.StatusCode)
	}
	if resp, _ := post("", "", "", initBody); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token gave %d, want 401", resp.StatusCode)
	}
	if resp, _ := post(token, "evil.example", "", initBody); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a foreign Host gave %d, want 403", resp.StatusCode)
	}
	resp, body := post(token, "", "", initBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize gave %d: %s", resp.StatusCode, body)
	}
	session := resp.Header.Get(mcp.SessionHeader)
	if session == "" {
		t.Fatal("initialize gave no session")
	}
	post(token, "", session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp, body = post(token, "", session, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "mobile_list_elements_on_screen") {
		t.Fatalf("tools/list gave %d: %s", resp.StatusCode, body)
	}
	resp, body = post(token, "", session, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mobile_list_elements_on_screen","arguments":{}}}`)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, `"isError":true`) || !strings.Contains(body, "Sign in") {
		t.Fatalf("a tool call gave %d: %s\nstderr:\n%s", resp.StatusCode, body, errw)
	}
	if strings.Contains(errw.String(), token+"\n") && strings.Count(errw.String(), token) > 2 {
		t.Errorf("the token is repeated on standard error beyond the banner:\n%s", errw)
	}

	cancel()
	select {
	case code := <-done:
		if code != cli.ExitOK {
			t.Fatalf("exit %d\nstderr:\n%s", code, errw)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the HTTP server did not end")
	}
	if out.Len() != 0 {
		t.Errorf("standard output holds %q in HTTP mode", out.String())
	}
	if r.server.Deleted() != 1 {
		t.Errorf("the session was deleted %d times, want 1", r.server.Deleted())
	}
	assertDeviceLeaseFree(t, r.opts.Home)
}

func TestMCPRefusesAddressesOffThisMachine(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	for _, addr := range []string{"0.0.0.0:8765", "192.168.1.5:8765", "example.com:80", "8765"} {
		var out bytes.Buffer
		errw := &syncBuffer{}
		opts := r.opts
		opts.Stdin = strings.NewReader("")
		code := cli.RunWith(t.Context(), []string{"mcp", "--dir", t.TempDir(), "--http", addr}, &out, errw, opts)
		if code != cli.ExitError {
			t.Errorf("--http %s: exit %d, want %d", addr, code, cli.ExitError)
		}
		if !strings.Contains(errw.String(), "--http") {
			t.Errorf("--http %s: stderr %q does not name the flag", addr, errw)
		}
	}
	// A studio address is held to the same rule.
	var out bytes.Buffer
	errw := &syncBuffer{}
	opts := r.opts
	opts.Stdin = strings.NewReader("")
	if code := cli.RunWith(t.Context(), []string{"mcp", "--dir", t.TempDir(), "--studio=0.0.0.0:9000"}, &out, errw, opts); code != cli.ExitError {
		t.Errorf("--studio on 0.0.0.0: exit %d, want %d", code, cli.ExitError)
	}
	contains(t, "stderr", errw.String(), "--studio", "must be on this machine")
}

func TestMCPShortTokenIsRefused(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	opts := r.opts
	opts.Stdin = strings.NewReader("")
	opts.LookupEnv = func(name string) (string, bool) {
		if name == "SEMAR_MCP_TOKEN" {
			return "short", true
		}
		return "", false
	}
	var out bytes.Buffer
	errw := &syncBuffer{}
	code := cli.RunWith(t.Context(), []string{"mcp", "--dir", t.TempDir(), "--http", ":0"}, &out, errw, opts)
	if code != cli.ExitError {
		t.Fatalf("exit %d, want %d", code, cli.ExitError)
	}
	contains(t, "stderr", errw.String(), "SEMAR_MCP_TOKEN", "at least 32")
}

func TestMCPSurfacesAreChecked(t *testing.T) {
	r := localRun(t, androidOptions{})
	run := func(dir string, args ...string) (int, string) {
		var out bytes.Buffer
		errw := &syncBuffer{}
		opts := r.opts
		opts.Stdin = strings.NewReader("")
		code := cli.RunWith(t.Context(), append([]string{"mcp", "--dir", dir}, args...), &out, errw, opts)
		return code, errw.String()
	}
	bare := t.TempDir()
	cases := []struct {
		name string
		dir  string
		args []string
		want string
	}{
		{"an unknown group", bare, []string{"--surfaces", "mobile,bogus"}, `"bogus" is not a tool group`},
		{"an empty list", bare, []string{"--surfaces", ","}, "--surfaces is empty"},
		{"web without a repository", bare, []string{"--surfaces", "web"}, "needs a repository"},
		{"journey without a repository", bare, []string{"--surfaces", "mobile,journey"}, "needs a repository"},
		{"an environment without a repository", bare, []string{"--env", "local"}, "--env needs a repository"},
		{"an unknown environment", r.dir, []string{"--env", "nowhere"}, "nowhere"},
	}
	for _, c := range cases {
		code, stderr := run(c.dir, c.args...)
		if code != cli.ExitError {
			t.Errorf("%s: exit %d, want %d\nstderr:\n%s", c.name, code, cli.ExitError, stderr)
			continue
		}
		if !strings.Contains(stderr, c.want) {
			t.Errorf("%s: stderr does not hold %q:\n%s", c.name, c.want, stderr)
		}
	}
	// mobile alone is valid with and without a repository, and --surfaces mobile in a repository leaves
	// the other groups out. The semar_ record tools are extras of the mobile group, so they are served too.
	for _, dir := range []string{bare, r.dir} {
		p := startMCP(t, r.opts, "--dir", dir, "--surfaces", "mobile")
		p.initialize()
		for _, n := range p.toolNames() {
			if !strings.HasPrefix(n, "mobile_") && !strings.HasPrefix(n, "semar_") {
				t.Errorf("--surfaces mobile lists %s", n)
			}
		}
		if code := p.finish(); code != cli.ExitOK {
			t.Errorf("exit %d\nstderr:\n%s", code, p.stderr)
		}
	}
}

func TestMCPDoesNotChangeTheHelp(t *testing.T) {
	var out, errw bytes.Buffer
	if code := cli.Run(t.Context(), []string{"--help"}, &out, &errw); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out.String(), "\n  mcp ") {
		t.Errorf("the help lists mcp, which is a hidden command:\n%s", out.String())
	}
	var mcpHelp bytes.Buffer
	if code := cli.Run(t.Context(), []string{"mcp", "--help"}, &mcpHelp, &errw); code != cli.ExitOK {
		t.Fatalf("mcp --help: exit %d", code)
	}
	contains(t, "mcp --help", mcpHelp.String(), "--env", "--http", "--device", "--out", "--read-only", "--allow-unsafe-urls", "--studio", "--surfaces", "--headed", "--dir")
}

// assertDeviceLeaseFree fails when the lease of the fake device is still held, which a server that ended
// cleanly never leaves.
func assertDeviceLeaseFree(t *testing.T, home string) {
	t.Helper()
	leases, err := fixture.OpenLeases(home)
	if err != nil {
		t.Fatal(err)
	}
	release, ok, err := leases.TryLock("android", "device", "emulator-5554")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the lease of the device is still held after the server ended")
	}
	release()
}
