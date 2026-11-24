package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInitializeNegotiation(t *testing.T) {
	s := newTestServer(t)
	for _, c := range []struct{ ask, want string }{
		{"2025-11-25", "2025-11-25"},
		{"2025-06-18", "2025-06-18"},
		{"2025-03-26", "2025-03-26"},
		{"2024-11-05", "2024-11-05"},
		{"2099-01-01", "2025-11-25"},
		{"1999-01-01", "2025-11-25"},
	} {
		sess := NewSession("s")
		m := rpc(t, s, sess, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+c.ask+`","capabilities":{},"clientInfo":{"name":"cursor","version":"9"}}}`)
		r := resultOf(t, m)
		if r["protocolVersion"] != c.want {
			t.Errorf("ask %s: version = %v, want %s", c.ask, r["protocolVersion"], c.want)
		}
		if sess.ProtocolVersion() != c.want || sess.Client().Name != "cursor" {
			t.Errorf("ask %s: session = %q %+v", c.ask, sess.ProtocolVersion(), sess.Client())
		}
		caps := r["capabilities"].(map[string]any)
		tools := caps["tools"].(map[string]any)
		if tools["listChanged"] != false {
			t.Errorf("capabilities = %v", caps)
		}
		if _, ok := caps["resources"]; ok {
			t.Error("resources must not be advertised")
		}
		if _, ok := caps["prompts"]; ok {
			t.Error("prompts must not be advertised")
		}
		info := r["serverInfo"].(map[string]any)
		if info["name"] != "semar" || info["version"] != "test" || r["instructions"] != "Use the tools." {
			t.Errorf("info = %v", r)
		}
	}
}

func TestInitializeRejectsBadParams(t *testing.T) {
	s := newTestServer(t)
	for _, p := range []string{``, `,"params":{}`, `,"params":{"protocolVersion":5}`} {
		m := rpc(t, s, nil, `{"jsonrpc":"2.0","id":1,"method":"initialize"`+p+`}`)
		if errCode(t, m) != CodeInvalidParams {
			t.Errorf("params %q: %v", p, m)
		}
	}
	// initialize inside a batch is refused.
	out := s.Handle(context.Background(), nil, []byte(`[{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}]`))
	if !strings.Contains(string(out), "-32600") {
		t.Errorf("batched initialize: %s", out)
	}
}

func TestInitializedNotificationAndPing(t *testing.T) {
	s := newTestServer(t)
	sess := NewSession("s")
	if out := s.Handle(context.Background(), sess, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); out != nil {
		t.Errorf("a notification got an answer: %s", out)
	}
	if !sess.Initialized() {
		t.Error("the session is not initialized")
	}
	m := rpc(t, s, sess, `{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	if m["id"] != "p" || len(resultOf(t, m)) != 0 {
		t.Errorf("ping = %v", m)
	}
}

const wantToolsList = `{"jsonrpc":"2.0","id":7,"result":{"tools":[` +
	`{"name":"echo","title":"Echo","description":"Return the text.","inputSchema":{"properties":{"mode":{"description":"How.","enum":["plain","loud"],"type":"string"},"n":{"description":"A count.","minimum":0,"type":"integer"},"text":{"description":"What to echo.","type":"string"},"with":{"description":"Pairs.","items":{"properties":{"name":{"description":"The key.","type":"string"},"value":{"description":"The value.","type":"string"}},"required":["name","value"],"type":"object"},"type":"array"}},"required":["text"],"type":"object"},"annotations":{"readOnlyHint":true,"openWorldHint":false}},` +
	`{"name":"block","description":"Wait until cancelled.","inputSchema":{"properties":{},"type":"object"}},` +
	`{"name":"boom","description":"Panic.","inputSchema":{"properties":{},"type":"object"}},` +
	`{"name":"fail","description":"Fail.","inputSchema":{"properties":{},"type":"object"}},` +
	`{"name":"work","description":"Report progress.","inputSchema":{"properties":{},"type":"object"}}]}}`

func TestToolsListOrderAndGolden(t *testing.T) {
	s := newTestServer(t)
	out := s.Handle(context.Background(), nil, []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/list"}`))
	if string(out) != wantToolsList {
		t.Errorf("tools/list =\n%s\nwant\n%s", out, wantToolsList)
	}
	// Surface and Write are internal and never sent.
	if bytes.Contains(out, []byte("Surface")) || bytes.Contains(out, []byte(`"control"`)) {
		t.Error("tools/list leaked Surface or Write")
	}
}

// TestEveryRegisteredSchemaIsPortable is the A2 check: it walks every tool and fails on a keyword
// outside the E8 subset. Other packages repeat it over their own tool sets with ValidatePortableSchema.
func TestEveryRegisteredSchemaIsPortable(t *testing.T) {
	for _, tool := range newTestServer(t).Tools() {
		if err := ValidatePortableSchema(tool.Input); err != nil {
			t.Errorf("tool %s: %v", tool.Name, err)
		}
	}
}

func TestNewServerRejectsBadTools(t *testing.T) {
	ok := func(context.Context, Call) (Result, error) { return Result{}, nil }
	for name, tools := range map[string][]Tool{
		"duplicate":   {{Name: "a", Handler: ok}, {Name: "a", Handler: ok}},
		"no name":     {{Handler: ok}},
		"no handler":  {{Name: "a"}},
		"anyOf":       {{Name: "a", Handler: ok, Input: Schema{"type": "object", "properties": map[string]Schema{}, "anyOf": []any{}}}},
		"not object":  {{Name: "a", Handler: ok, Input: String("x")}},
		"nested $ref": {{Name: "a", Handler: ok, Input: Object(map[string]Schema{"x": {"$ref": "#/x"}})}},
	} {
		if _, err := NewServer(Info{}, tools, nil); err == nil {
			t.Errorf("%s: NewServer accepted the tools", name)
		}
	}
}

func TestToolsCall(t *testing.T) {
	s := newTestServer(t)
	sess := NewSession("sid")
	rpc(t, s, sess, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"codex"}}}`)

	text, isErr := textOf(t, rpc(t, s, sess, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi","n":2,"with":[{"name":"a","value":"b"}]}}}`))
	if isErr || text != "hi|2|1|codex|sid" {
		t.Errorf("good call = %q err=%v", text, isErr)
	}

	// Bad arguments are an isError result, not a protocol error.
	for _, args := range []string{`{"text":"hi","bogus":1}`, `{"text":5}`, `[1]`, `"x"`} {
		text, isErr := textOf(t, rpc(t, s, sess, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":`+args+`}}`))
		if !isErr || text == "" {
			t.Errorf("args %s: text=%q isError=%v", args, text, isErr)
		}
	}
	// A secret in a wrong-typed value is never echoed.
	text, _ = textOf(t, rpc(t, s, sess, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi","n":"hunter2"}}}`))
	if strings.Contains(text, "hunter2") {
		t.Errorf("the message quotes a value: %q", text)
	}

	// No arguments at all decode as {} and the handler reports the problem itself.
	if _, isErr := textOf(t, rpc(t, s, sess, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo"}}`)); isErr {
		t.Error("missing optional arguments must be fine")
	}

	// A handler error is an isError result.
	text, isErr = textOf(t, rpc(t, s, sess, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fail"}}`))
	if !isErr || text != "the device is gone" {
		t.Errorf("fail = %q %v", text, isErr)
	}

	// An unknown tool and a missing name are protocol faults.
	if c := errCode(t, rpc(t, s, sess, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope"}}`)); c != CodeInvalidParams {
		t.Errorf("unknown tool code = %d", c)
	}
	if c := errCode(t, rpc(t, s, sess, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{}}`)); c != CodeInvalidParams {
		t.Errorf("no name code = %d", c)
	}
}

func TestPanicIsRecoveredWithoutAStack(t *testing.T) {
	var logs bytes.Buffer
	s, err := NewServer(Info{Name: "semar"}, testTools(), slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	out := s.Handle(context.Background(), nil, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom"}}`))
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if errCode(t, m) != CodeInternal {
		t.Errorf("answer = %s", out)
	}
	if strings.Contains(string(out), "secret stack detail") || strings.Contains(string(out), "goroutine") {
		t.Errorf("the answer leaks the panic: %s", out)
	}
	if !strings.Contains(logs.String(), "secret stack detail") {
		t.Error("the panic was not logged")
	}
	// The server still works.
	rpc(t, s, nil, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
}

func TestProtocolErrors(t *testing.T) {
	s := newTestServer(t)
	for _, c := range []struct {
		name, in string
		code     int
		nullID   bool
	}{
		{"parse error", `{"jsonrpc":"2.0",`, CodeParseError, true},
		{"empty", ``, CodeParseError, true},
		{"not an object", `42`, CodeInvalidRequest, true},
		{"empty batch", `[]`, CodeInvalidRequest, true},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, CodeInvalidRequest, false},
		{"no method", `{"jsonrpc":"2.0","id":1}`, CodeInvalidRequest, false},
		{"bad id", `{"jsonrpc":"2.0","id":{},"method":"ping"}`, CodeInvalidRequest, true},
		{"bad params", `{"jsonrpc":"2.0","id":1,"method":"ping","params":5}`, CodeInvalidRequest, false},
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, CodeMethodNotFound, false},
		{"prompts", `{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`, CodeMethodNotFound, false},
		{"bad level", `{"jsonrpc":"2.0","id":1,"method":"logging/setLevel","params":{"level":"loud"}}`, CodeInvalidParams, false},
	} {
		m := rpc(t, s, nil, c.in)
		if got := errCode(t, m); got != c.code {
			t.Errorf("%s: code = %d, want %d (%v)", c.name, got, c.code, m)
		}
		if id, present := m["id"]; !present || (c.nullID && id != nil) {
			t.Errorf("%s: id = %v present=%v", c.name, id, present)
		}
	}
}

func TestIDsAreEchoedVerbatim(t *testing.T) {
	s := newTestServer(t)
	for _, id := range []string{`7`, `"abc"`, `0`, `-3`, `1.5`, `null`} {
		out := s.Handle(context.Background(), nil, []byte(`{"jsonrpc":"2.0","id":`+id+`,"method":"ping"}`))
		if !strings.Contains(string(out), `"id":`+id+`,`) {
			t.Errorf("id %s: %s", id, out)
		}
	}
}

func TestLoggingSetLevel(t *testing.T) {
	s := newTestServer(t)
	sess := NewSession("s")
	m := rpc(t, s, sess, `{"jsonrpc":"2.0","id":1,"method":"logging/setLevel","params":{"level":"warning"}}`)
	if len(resultOf(t, m)) != 0 || sess.LogLevel() != "warning" {
		t.Errorf("setLevel = %v level=%q", m, sess.LogLevel())
	}
}

func TestBatch(t *testing.T) {
	s := newTestServer(t)
	in := `[{"jsonrpc":"2.0","id":1,"method":"ping"},
		{"jsonrpc":"2.0","method":"notifications/initialized"},
		{"jsonrpc":"2.0","id":"two","method":"tools/call","params":{"name":"echo","arguments":{"text":"x"}}},
		{"jsonrpc":"2.0","id":3,"method":"nope"},
		5]`
	out := s.Handle(context.Background(), nil, []byte(in))
	var got []map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d answers: %s", len(got), out)
	}
	if got[0]["id"].(float64) != 1 || got[1]["id"] != "two" || got[2]["id"].(float64) != 3 || got[3]["id"] != nil {
		t.Errorf("answers out of order: %s", out)
	}
	if errCode(t, got[2]) != CodeMethodNotFound || errCode(t, got[3]) != CodeInvalidRequest {
		t.Errorf("errors: %s", out)
	}
	// A batch of only notifications gets no answer.
	if out := s.Handle(context.Background(), nil, []byte(`[{"jsonrpc":"2.0","method":"notifications/initialized"}]`)); out != nil {
		t.Errorf("notification batch answered: %s", out)
	}
}

func TestNotificationsGetNoAnswer(t *testing.T) {
	s := newTestServer(t)
	for _, in := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"unknown/thing"}`,
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"fail"}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}`,
		`{"jsonrpc":"2.0","id":1,"result":{}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"x"}}`,
	} {
		if out := s.Handle(context.Background(), nil, []byte(in)); out != nil {
			t.Errorf("%s answered: %s", in, out)
		}
	}
}

func TestCancellationStopsABlockingCall(t *testing.T) {
	s := newTestServer(t)
	sess := NewSession("s")
	done := make(chan []byte, 1)
	started := make(chan struct{})
	go func() {
		done <- s.handle(context.Background(), sess, []byte(`{"jsonrpc":"2.0","id":"c1","method":"tools/call","params":{"name":"block"}}`), func() { close(started) })
	}()
	<-started
	select {
	case <-done:
		t.Fatal("the call ended before it was cancelled")
	case <-time.After(20 * time.Millisecond):
	}
	// A cancel for another session's id does nothing.
	s.Handle(context.Background(), NewSession("other"), []byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"c1"}}`))
	select {
	case <-done:
		t.Fatal("another session cancelled the call")
	case <-time.After(20 * time.Millisecond):
	}
	s.Handle(context.Background(), sess, []byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"c1","reason":"user"}}`))
	select {
	case out := <-done:
		if out != nil {
			t.Errorf("a cancelled call must not be answered: %s", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the call kept running after the cancel")
	}
}

func TestCloseSessionCancelsCalls(t *testing.T) {
	s := newTestServer(t)
	sess := NewSession("gone")
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		s.handle(context.Background(), sess, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block"}}`), func() { close(started) })
		close(done)
	}()
	<-started
	s.CloseSession("gone")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CloseSession did not cancel the call")
	}
}

func TestDuplicateInFlightIDIsRefused(t *testing.T) {
	s := newTestServer(t)
	sess := NewSession("s")
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		s.handle(context.Background(), sess, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block"}}`), func() { close(started) })
		close(done)
	}()
	<-started
	m := rpc(t, s, sess, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block"}}`)
	if errCode(t, m) != CodeInvalidRequest {
		t.Errorf("duplicate id = %v", m)
	}
	s.CloseSession("s")
	<-done
}

func TestMiddlewareOrder(t *testing.T) {
	var mu sync.Mutex
	var trace []string
	mk := func(name string) Middleware {
		return func(tool Tool, next Handler) Handler {
			return func(ctx context.Context, c Call) (Result, error) {
				mu.Lock()
				trace = append(trace, name+">"+tool.Name)
				mu.Unlock()
				res, err := next(ctx, c)
				mu.Lock()
				trace = append(trace, "<"+name)
				mu.Unlock()
				return res, err
			}
		}
	}
	s := newTestServer(t)
	s.Use(mk("a"), mk("b"))
	s.Use(mk("c"))
	rpc(t, s, nil, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"x"}}}`)
	if got := strings.Join(trace, " "); got != "a>echo b>echo c>echo <c <b <a" {
		t.Errorf("trace = %s", got)
	}
}

func TestMiddlewareSeesToolMetadataAndCanRefuse(t *testing.T) {
	s := newTestServer(t)
	s.Use(func(tool Tool, next Handler) Handler {
		if !tool.Write {
			return next
		}
		return func(ctx context.Context, c Call) (Result, error) {
			return ErrorResult("refused " + tool.Surface), nil
		}
	})
	text, isErr := textOf(t, rpc(t, s, nil, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block"}}`))
	if !isErr || text != "refused control" {
		t.Errorf("write tool = %q %v", text, isErr)
	}
	if _, isErr := textOf(t, rpc(t, s, nil, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"x"}}}`)); isErr {
		t.Error("a read-only tool was refused")
	}
}

func TestProgressIsNoOpWithoutANotifier(t *testing.T) {
	s := newTestServer(t)
	text, _ := textOf(t, rpc(t, s, nil, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"work","_meta":{"progressToken":"t"}}}`))
	if text != "done" {
		t.Errorf("work = %q", text)
	}
}

func TestStructuredResultAndImage(t *testing.T) {
	r, err := StructuredResult(map[string]int{"a": 1})
	if err != nil || r.Content[0].Text != `{"a":1}` {
		t.Fatalf("%+v %v", r, err)
	}
	s, _ := NewServer(Info{Name: "x"}, []Tool{{Name: "img", Handler: func(context.Context, Call) (Result, error) {
		return ImageResult("QUJD", "image/png", "shot"), nil
	}}, {Name: "none", Handler: func(context.Context, Call) (Result, error) { return Result{}, nil }}}, nil)
	out := string(s.Handle(context.Background(), nil, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"img"}}`)))
	if !strings.Contains(out, `{"type":"image","data":"QUJD","mimeType":"image/png"},{"type":"text","text":"shot"}`) {
		t.Errorf("image = %s", out)
	}
	out = string(s.Handle(context.Background(), nil, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"none"}}`)))
	if !strings.Contains(out, `"content":[]`) {
		t.Errorf("empty result = %s", out)
	}
}

func TestDecodeLimits(t *testing.T) {
	if _, _, err := Decode(bytes.Repeat([]byte(" "), MaxMessage+1)); err == nil || err.Code != CodeInvalidRequest {
		t.Errorf("oversized = %v", err)
	}
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i <= maxBatch; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"jsonrpc":"2.0","method":"x"}`)
	}
	b.WriteString("]")
	if _, _, err := Decode([]byte(b.String())); err == nil || err.Code != CodeInvalidRequest {
		t.Errorf("huge batch = %v", err)
	}
	if _, _, err := Decode([]byte(strings.Repeat("[", 20000))); err == nil {
		t.Error("deeply nested input was accepted")
	}
}
