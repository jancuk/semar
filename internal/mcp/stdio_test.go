package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func shortGrace(t *testing.T) {
	t.Helper()
	old := stdioGrace
	stdioGrace = 100 * time.Millisecond
	t.Cleanup(func() { stdioGrace = old })
}

type stdioHarness struct {
	in    *io.PipeWriter
	out   *bufio.Reader
	done  chan error
	ended chan struct{}
}

func startStdio(t *testing.T, ctx context.Context, s *Server) *stdioHarness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &stdioHarness{in: inW, out: bufio.NewReader(outR), done: make(chan error, 1), ended: make(chan struct{})}
	go func() {
		err := ServeStdio(ctx, s, inR, outW)
		outW.Close()
		h.done <- err
		close(h.ended)
	}()
	t.Cleanup(func() {
		inW.Close()
		outR.Close()
		select {
		case <-h.ended:
		case <-time.After(5 * time.Second):
			t.Error("ServeStdio did not return at cleanup")
		}
	})
	return h
}

func (h *stdioHarness) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(h.in, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (h *stdioHarness) frame(t *testing.T) map[string]any {
	t.Helper()
	type res struct {
		line []byte
		err  error
	}
	c := make(chan res, 1)
	go func() { l, err := h.out.ReadBytes('\n'); c <- res{l, err} }()
	select {
	case r := <-c:
		if r.err != nil {
			t.Fatalf("read a frame: %v", r.err)
		}
		var m map[string]any
		if err := json.Unmarshal(r.line, &m); err != nil {
			t.Fatalf("frame %q: %v", r.line, err)
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no frame arrived")
		return nil
	}
}

func TestStdioRoundTripAndEOFCleanup(t *testing.T) {
	shortGrace(t)
	s := newTestServer(t)
	h := startStdio(t, context.Background(), s)

	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"gemini"}}}`)
	if m := h.frame(t); resultOf(t, m)["protocolVersion"] != "2025-06-18" {
		t.Errorf("initialize = %v", m)
	}
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	h.send(t, ``)
	h.send(t, `   `)
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	if text, _ := textOf(t, h.frame(t)); text != "hi|0|0|gemini|stdio" {
		t.Errorf("echo = %q", text)
	}
	h.send(t, `not json`)
	if m := h.frame(t); errCode(t, m) != CodeParseError || m["id"] != nil {
		t.Errorf("parse error = %v", m)
	}

	// A call that blocks is cancelled at EOF, and ServeStdio returns by itself.
	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"block"}}`)
	start := time.Now()
	h.in.Close()
	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("ServeStdio = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeStdio did not return after EOF")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("EOF cleanup took %v", time.Since(start))
	}
	if rest, _ := io.ReadAll(h.out); len(rest) != 0 {
		t.Errorf("a cancelled call wrote %q", rest)
	}
}

func TestStdioFinishesFastCallsAtEOF(t *testing.T) {
	// A one-shot client that writes a request and closes stdin still gets its answer.
	shortGrace(t)
	s := newTestServer(t)
	var out bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`) // no final newline
	if err := ServeStdio(context.Background(), s, in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("frames = %q", out.String())
	}
	for _, l := range lines {
		if !json.Valid([]byte(l)) {
			t.Errorf("not one JSON frame: %q", l)
		}
	}
}

func TestStdioContextCancelStopsCalls(t *testing.T) {
	shortGrace(t)
	ctx, cancel := context.WithCancel(context.Background())
	h := startStdio(t, ctx, newTestServer(t))
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block"}}`)
	cancel()
	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("ServeStdio = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeStdio ignored the context")
	}
}

func TestStdioCancelNotification(t *testing.T) {
	shortGrace(t)
	h := startStdio(t, context.Background(), newTestServer(t))
	h.send(t, `{"jsonrpc":"2.0","id":"x","method":"tools/call","params":{"name":"block"}}`)
	// The next line is read only after the call is registered, so the cancel always finds it.
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"x"}}`)
	h.send(t, `{"jsonrpc":"2.0","id":9,"method":"ping"}`)
	if m := h.frame(t); m["id"].(float64) != 9 {
		t.Errorf("the first frame was %v, the cancelled call must stay silent", m)
	}
}

func TestStdioProgressNotifications(t *testing.T) {
	h := startStdio(t, context.Background(), newTestServer(t))
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"work","_meta":{"progressToken":"tok-1"}}}`)
	p1, p2, last := h.frame(t), h.frame(t), h.frame(t)
	for i, p := range []map[string]any{p1, p2} {
		if p["method"] != "notifications/progress" || p["id"] != nil {
			t.Fatalf("frame %d = %v", i, p)
		}
	}
	params := p1["params"].(map[string]any)
	if params["progressToken"] != "tok-1" || params["progress"].(float64) != 1 || params["total"].(float64) != 3 || params["message"] != "one" {
		t.Errorf("first progress = %v", params)
	}
	if params := p2["params"].(map[string]any); params["progress"].(float64) != 2 || params["message"] != nil {
		t.Errorf("second progress = %v", params)
	}
	if text, _ := textOf(t, last); text != "done" || last["id"].(float64) != 1 {
		t.Errorf("answer = %v", last)
	}

	// Without a token there is no notification.
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"work"}}`)
	if m := h.frame(t); m["id"] == nil {
		t.Errorf("unexpected notification %v", m)
	}
	// A numeric token is echoed as a number.
	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"work","_meta":{"progressToken":42}}}`)
	if m := h.frame(t); m["params"].(map[string]any)["progressToken"].(float64) != 42 {
		t.Errorf("numeric token = %v", m)
	}
	h.frame(t)
	h.frame(t)
}

func TestStdioOversizedLineIsSkipped(t *testing.T) {
	var out bytes.Buffer
	big := strings.Repeat("a", MaxMessage+10)
	in := big + "\n" + `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	if err := ServeStdio(context.Background(), newTestServer(t), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("frames = %d: %.200s", len(lines), out.String())
	}
	var first, second map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if errCode(t, first) != CodeParseError || first["id"] != nil || second["id"].(float64) != 1 {
		t.Errorf("frames = %v / %v", first, second)
	}
}

func TestStdioConcurrentWritesStayWholeLines(t *testing.T) {
	var out syncBuffer
	var in strings.Builder
	for i := 0; i < 200; i++ {
		in.WriteString(`{"jsonrpc":"2.0","id":` + string(rune('0'+i%10)) + `,"method":"tools/call","params":{"name":"work","_meta":{"progressToken":1}}}` + "\n")
	}
	if err := ServeStdio(context.Background(), newTestServer(t), strings.NewReader(in.String()), &out); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !json.Valid([]byte(l)) {
			t.Fatalf("interleaved frame: %q", l)
		}
		n++
	}
	if n != 600 {
		t.Errorf("frames = %d, want 600", n)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
