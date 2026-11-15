package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/agentloop"
	"github.com/jancuk/semar/internal/stage/plan"
)

const testKey = "acme-test-key"

var draft = `{"summary":"refunds","tickets":[],"scenarios":[],"notDrafted":[],"uncovered":[]}`

type step func(t *testing.T, r *http.Request, body map[string]any) (int, string)

// server answers request n with steps[n]; a request past the script fails the test.
func server(t *testing.T, steps ...step) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(steps) {
			t.Errorf("unexpected request %d", i+1)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("request %d is not JSON: %v", i+1, err)
		}
		status, out := steps[i](t, r, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func newDriver(t *testing.T, base, keyRef string) *Driver {
	t.Helper()
	d, err := New(config.Provider{ID: "acme", Kind: Kind, Model: "acme-chat", BaseURL: base, APIKey: keyRef}, Options{
		ResolveKey: func(ref string) (string, error) {
			if ref != "env:ACME_KEY" {
				return "", fmt.Errorf("unknown reference %s", ref)
			}
			return testKey, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	d.retryWait = time.Millisecond
	return d
}

func newJob(t *testing.T) provider.Job {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "orders"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "orders", "handler.go"), []byte("package orders\n\nfunc Refund() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return provider.Job{Prompt: "Plan the tests for orders.", Workdir: dir, Schema: plan.Schema(), MaxTurns: 4, Timeout: time.Minute}
}

func compact(t *testing.T, v any) string {
	t.Helper()
	var data []byte
	switch x := v.(type) {
	case string:
		data = []byte(x)
	case json.RawMessage:
		data = x
	default:
		var err error
		if data, err = json.Marshal(x); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		t.Fatalf("compact %s: %v", data, err)
	}
	return buf.String()
}

// sameJSON reports whether decoded holds the same JSON value as raw.
func sameJSON(t *testing.T, decoded any, raw []byte) bool {
	t.Helper()
	var want any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(decoded, want)
}

func get(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			a, _ := v.([]any)
			if k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func checkHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
		t.Errorf("%s %s", r.Method, r.URL.Path)
	}
	if r.Header.Get("Authorization") != "Bearer "+testKey || r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", r.Header)
	}
}

func TestTwoStepToolConversation(t *testing.T) {
	job := newJob(t)
	srv, n := server(t,
		func(t *testing.T, r *http.Request, body map[string]any) (int, string) {
			checkHeaders(t, r)
			if body["model"] != "acme-chat" {
				t.Errorf("model = %v", body["model"])
			}
			for _, k := range []string{"tool_choice", "max_tokens", "max_completion_tokens", "parallel_tool_calls", "response_format"} {
				if _, ok := body[k]; ok {
					t.Errorf("the request sends %s", k)
				}
			}
			var names []string
			for _, tl := range body["tools"].([]any) {
				if get(tl, "type") != "function" || get(tl, "function", "description") == "" || get(tl, "function", "strict") != nil {
					t.Errorf("tool = %v", tl)
				}
				names = append(names, get(tl, "function", "name").(string))
			}
			if strings.Join(names, ",") != "read_file,search,list_files,submit" {
				t.Errorf("tools = %v", names)
			}
			if !sameJSON(t, get(body, "tools", 3, "function", "parameters"), plan.Schema()) {
				t.Error("the submit tool does not carry the plan schema")
			}
			if get(body, "messages", 0, "role") != "system" || get(body, "messages", 1, "role") != "user" || get(body, "messages", 1, "content") != job.Prompt {
				t.Errorf("messages = %v", body["messages"])
			}
			return 200, `{"model":"acme-chat-0301","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,` +
				`"reasoning_content":"The handler is in orders.",` +
				`"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"orders/handler.go\"}"}}]}}],` +
				`"usage":{"prompt_tokens":1000,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":800}}}`
		},
		func(t *testing.T, r *http.Request, body map[string]any) (int, string) {
			checkHeaders(t, r)
			msgs := body["messages"].([]any)
			if len(msgs) != 4 {
				t.Fatalf("%d messages: %v", len(msgs), msgs)
			}
			want := `{"role":"assistant","content":null,"reasoning_content":"The handler is in orders.",` +
				`"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"orders/handler.go\"}"}}]}`
			if !sameJSON(t, msgs[2], []byte(want)) {
				t.Errorf("assistant message = %v", msgs[2])
			}
			if get(msgs, 3, "role") != "tool" || get(msgs, 3, "tool_call_id") != "call_1" || !strings.Contains(get(msgs, 3, "content").(string), "3\tfunc Refund() {}") {
				t.Errorf("tool message = %v", msgs[3])
			}
			// The arguments arrive as an object, which some servers send instead of a string.
			return 200, `{"model":"acme-chat-0301","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"",` +
				`"tool_calls":[{"id":"call_2","type":"function","function":{"name":"submit","arguments":` + draft + `}}]}}],` +
				`"usage":{"prompt_tokens":1200,"completion_tokens":50,"prompt_cache_hit_tokens":900}}`
		},
	)
	var reports []provider.Progress
	job.Progress = func(p provider.Progress) { reports = append(reports, p) }
	res, err := newDriver(t, srv.URL+"/v1/", "env:ACME_KEY").Run(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 || res.Turns != 2 {
		t.Errorf("%d requests, %d turns", n.Load(), res.Turns)
	}
	if _, err := plan.DecodeDraft(res.Output); err != nil || compact(t, res.Output) != draft {
		t.Errorf("output %s: %v", res.Output, err)
	}
	if res.InputTokens != 2200 || res.OutputTokens != 80 || res.CacheReadTokens != 900 || res.CacheWriteTokens != 800 || res.Model != "acme-chat-0301" {
		t.Errorf("result = %+v", res)
	}
	if fmt.Sprint(res.Tools) != "[{read_file 1} {submit 1}]" {
		t.Errorf("tools = %v", res.Tools)
	}
	// The driver passes the job's progress function on to the loop.
	if fmt.Sprint(reports) != "[{1 1 read_file} {2 2 submit}]" {
		t.Errorf("reports = %+v", reports)
	}
}

func TestArgumentsAsAString(t *testing.T) {
	quoted, _ := json.Marshal(draft)
	twice, _ := json.Marshal(string(quoted))
	for name, args := range map[string]string{"string": string(quoted), "string twice": string(twice), "object": draft} {
		t.Run(name, func(t *testing.T) {
			srv, _ := server(t, func(*testing.T, *http.Request, map[string]any) (int, string) {
				// No call ID: some local servers send none.
				return 200, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"function":{"name":"submit","arguments":` + args + `}}]}}]}`
			})
			res, err := newDriver(t, srv.URL, "env:ACME_KEY").Run(context.Background(), newJob(t))
			if err != nil || compact(t, res.Output) != draft {
				t.Fatalf("output %s: %v", res.Output, err)
			}
		})
	}
	got, wire := arguments(json.RawMessage(`""`))
	if string(got) != "{}" || wire != "" {
		t.Errorf("empty arguments = %s, %q", got, wire)
	}
}

func TestReasoningKeptWithoutToolCalls(t *testing.T) {
	resp, err := parseResponse([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":null,"reasoning_content":"thinking"}}]}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"assistant","content":"","reasoning_content":"thinking"}`
	if compact(t, resp.Message.Native) != want {
		t.Errorf("native = %s", resp.Message.Native)
	}
	empty, err := parseResponse([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":null}}]}`), 2)
	if err != nil || empty.Message.Native != nil {
		t.Fatalf("empty message: %s, %v", empty.Message.Native, err)
	}
	out := buildRequest("m", agentloop.Request{Messages: []agentloop.Message{{Role: agentloop.RoleUser, Text: "p"}, empty.Message, {Role: agentloop.RoleUser, Text: "r"}}})
	if len(out.Messages) != 2 {
		t.Errorf("an empty assistant message was sent: %+v", out.Messages)
	}
}

func submitStep(*testing.T, *http.Request, map[string]any) (int, string) {
	return 200, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"c","type":"function","function":{"name":"submit","arguments":` + strconvQuote(draft) + `}}]}}]}`
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestErrorClasses(t *testing.T) {
	echo := `{"error":{"message":"Incorrect API key provided: ` + testKey + `","type":"invalid_request_error","code":"invalid_api_key"}}`
	cases := []struct {
		status   int
		body     string
		class    provider.FailureClass
		requests int32
	}{
		{401, echo, provider.PermissionError, 1},
		{403, echo, provider.PermissionError, 1},
		{429, echo, provider.UsageLimit, 2},
		{402, `{"error":{"message":"Insufficient Balance"}}`, provider.UsageLimit, 1},
		{400, echo, provider.ProviderError, 1},
		{500, echo, provider.ProviderError, 2},
		{503, `upstream unavailable`, provider.ProviderError, 2},
		{200, `not json`, provider.ProviderError, 1},
		{200, `{"choices":[]}`, provider.ProviderError, 1},
		{200, `{"error":{"message":"bad key ` + testKey + `"}}`, provider.ProviderError, 1},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.status), func(t *testing.T) {
			answer := func(*testing.T, *http.Request, map[string]any) (int, string) { return c.status, c.body }
			srv, n := server(t, answer, answer)
			_, err := newDriver(t, srv.URL, "env:ACME_KEY").Run(context.Background(), newJob(t))
			var perr *provider.Error
			if !errors.As(err, &perr) || perr.Class != c.class || n.Load() != c.requests {
				t.Fatalf("%d requests, error %v", n.Load(), err)
			}
			if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "Plan the tests") {
				t.Errorf("the error holds the key or the prompt: %v", err)
			}
		})
	}
}

func TestTransportError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := newDriver(t, url, "env:ACME_KEY").Run(context.Background(), newJob(t))
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.TransportError {
		t.Fatalf("error = %v", err)
	}
}

func TestRetryThenSuccess(t *testing.T) {
	srv, n := server(t,
		func(*testing.T, *http.Request, map[string]any) (int, string) { return 429, `{}` },
		submitStep,
	)
	res, err := newDriver(t, srv.URL, "env:ACME_KEY").Run(context.Background(), newJob(t))
	if err != nil || n.Load() != 2 || res.Turns != 1 {
		t.Fatalf("%d requests, %d turns, error %v", n.Load(), res.Turns, err)
	}
}

func TestRedirectRefused(t *testing.T) {
	other, hits := server(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/chat/completions", http.StatusFound)
	}))
	defer srv.Close()
	_, err := newDriver(t, srv.URL, "env:ACME_KEY").Run(context.Background(), newJob(t))
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.ProviderError || !strings.Contains(perr.Detail, "redirect") || hits.Load() != 0 {
		t.Fatalf("error %v, the other host got %d requests", err, hits.Load())
	}
}

func TestLoopbackNeedsNoKey(t *testing.T) {
	srv, _ := server(t, func(t *testing.T, r *http.Request, body map[string]any) (int, string) {
		if _, ok := r.Header["Authorization"]; ok {
			t.Error("a key was sent")
		}
		return submitStep(t, r, body)
	})
	if _, err := newDriver(t, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), "").Run(context.Background(), newJob(t)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ base, key string }{
		{"http://api.example.com/v1", "env:ACME_KEY"},
		{"https://api.example.com/v1", ""},
		{"https://api.example.com/v1?key=x", "env:ACME_KEY"},
		{"https://api.example.com/v1", testKey},
	} {
		if _, err := New(config.Provider{ID: "acme", BaseURL: c.base, APIKey: c.key}, Options{ResolveKey: func(string) (string, error) { return testKey, nil }}); err == nil {
			t.Errorf("%q with key %q was accepted", c.base, c.key)
		} else if strings.Contains(err.Error(), testKey) {
			t.Errorf("the error holds the key: %v", err)
		}
	}
}

func TestModelKeyHealthAccount(t *testing.T) {
	var resolved atomic.Int32
	srv, _ := server(t, submitStep, submitStep)
	d, err := New(config.Provider{ID: "acme", BaseURL: srv.URL, APIKey: "env:ACME_KEY"}, Options{ResolveKey: func(string) (string, error) {
		resolved.Add(1)
		return testKey, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	job := newJob(t)
	_, err = d.Run(context.Background(), job)
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.ValidationError || !strings.Contains(perr.Detail, "needs a model") {
		t.Fatalf("no model: %v", err)
	}
	job.Model = "deepseek-chat"
	for range 2 {
		if _, err := d.Run(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	if resolved.Load() != 2 {
		t.Errorf("the key was resolved %d times for two jobs", resolved.Load())
	}
	if h := d.Health(context.Background()); !h.Installed || !strings.HasPrefix(h.Version, "127.0.0.1:") {
		t.Errorf("health = %+v", h)
	}
	if st := d.Status(context.Background()); !st.SignedIn || st.Method != "api key" || d.LoginCommand() != nil {
		t.Errorf("status = %+v", st)
	}
	var _ provider.Account = d

	broken := newDriver(t, srv.URL, "keychain:other")
	if h := broken.Health(context.Background()); h.Installed || !strings.Contains(h.Detail, "keychain:other") {
		t.Errorf("health = %+v", h)
	}
	_, err = broken.Run(context.Background(), job)
	if !errors.As(err, &perr) || perr.Class != provider.PermissionError {
		t.Errorf("unresolved key: %v", err)
	}
	d2, err := New(config.Provider{ID: "acme", APIKey: "env:ACME_KEY"}, Options{ResolveKey: func(string) (string, error) { return testKey, nil }})
	if err != nil || d2.endpoint.URL("/chat/completions") != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("default address: %v", err)
	}
}
