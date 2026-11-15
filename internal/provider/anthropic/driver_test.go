package anthropic

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
	d, err := New(config.Provider{ID: "acme", Kind: Kind, Model: "claude-opus-5-5", BaseURL: base, APIKey: keyRef}, Options{
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

const firstContent = `[{"type":"thinking","thinking":"","signature":"sig-abc=="},` +
	`{"type":"text","text":"I will read the handler."},` +
	`{"type":"tool_use","id":"toolu_01","name":"read_file","input":{"path":"orders/handler.go"}}]`

func checkHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
		t.Errorf("%s %s", r.Method, r.URL.Path)
	}
	if r.Header.Get("x-api-key") != testKey || r.Header.Get("anthropic-version") != "2026-10-21" ||
		r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "" {
		t.Errorf("headers = %v", r.Header)
	}
}

func TestTwoStepToolConversation(t *testing.T) {
	job := newJob(t)
	srv, n := server(t,
		func(t *testing.T, r *http.Request, body map[string]any) (int, string) {
			checkHeaders(t, r)
			if body["model"] != "claude-opus-5-5" || body["max_tokens"] != float64(maxTokens) {
				t.Errorf("model %v, max_tokens %v", body["model"], body["max_tokens"])
			}
			if _, ok := body["cache_control"]; ok {
				t.Error("automatic caching was sent to a server that is not the vendor's")
			}
			if get(body, "system", 0, "cache_control", "type") != "ephemeral" || get(body, "system", 0, "text") == "" {
				t.Errorf("system = %v", body["system"])
			}
			var names []string
			for _, tl := range body["tools"].([]any) {
				names = append(names, get(tl, "name").(string))
				if get(tl, "description") == "" || get(tl, "input_schema", "type") != "object" {
					t.Errorf("tool = %v", tl)
				}
			}
			if strings.Join(names, ",") != "read_file,search,list_files,submit" {
				t.Errorf("tools = %v", names)
			}
			if !sameJSON(t, get(body, "tools", 3, "input_schema"), plan.Schema()) {
				t.Error("the submit tool does not carry the plan schema")
			}
			if get(body, "messages", 0, "role") != "user" || get(body, "messages", 0, "content", 0, "text") != job.Prompt ||
				get(body, "messages", 0, "content", 0, "cache_control", "type") != "ephemeral" {
				t.Errorf("messages = %v", body["messages"])
			}
			return 200, `{"type":"message","model":"claude-opus-5-5","stop_reason":"tool_use","content":` + firstContent +
				`,"usage":{"input_tokens":50,"output_tokens":20,"cache_creation_input_tokens":900,"cache_read_input_tokens":0}}`
		},
		func(t *testing.T, r *http.Request, body map[string]any) (int, string) {
			checkHeaders(t, r)
			msgs := body["messages"].([]any)
			if len(msgs) != 3 {
				t.Fatalf("%d messages", len(msgs))
			}
			if get(msgs, 1, "role") != "assistant" || !sameJSON(t, get(msgs, 1, "content"), []byte(firstContent)) {
				t.Errorf("the assistant message was not sent back unchanged: %v", msgs[1])
			}
			result := get(msgs, 2, "content", 0)
			if get(msgs, 2, "role") != "user" || get(result, "type") != "tool_result" || get(result, "tool_use_id") != "toolu_01" ||
				!strings.Contains(get(result, "content").(string), "3\tfunc Refund() {}") || get(result, "is_error") != nil {
				t.Errorf("tool result = %v", msgs[2])
			}
			return 200, `{"type":"message","model":"claude-opus-5-5","stop_reason":"tool_use","content":[` +
				`{"type":"tool_use","id":"toolu_02","name":"submit","input":` + draft + `}],` +
				`"usage":{"input_tokens":30,"output_tokens":40,"cache_creation_input_tokens":100,"cache_read_input_tokens":900}}`
		},
	)
	var reports []provider.Progress
	job.Progress = func(p provider.Progress) { reports = append(reports, p) }
	d := newDriver(t, srv.URL, "env:ACME_KEY")
	res, err := d.Run(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 || res.Turns != 2 {
		t.Errorf("%d requests, %d turns", n.Load(), res.Turns)
	}
	if _, err := plan.DecodeDraft(res.Output); err != nil || compact(t, res.Output) != draft {
		t.Errorf("output %s: %v", res.Output, err)
	}
	if res.InputTokens != 1980 || res.OutputTokens != 60 || res.CacheReadTokens != 900 || res.CacheWriteTokens != 1000 || res.Model != "claude-opus-5-5" {
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

func submitStep(t *testing.T, r *http.Request, _ map[string]any) (int, string) {
	return 200, `{"type":"message","model":"m","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"submit","input":` + draft + `}],"usage":{}}`
}

func TestErrorClasses(t *testing.T) {
	echo := `{"type":"error","error":{"type":"some_error","message":"the key ` + testKey + ` is wrong"}}`
	cases := []struct {
		status   int
		body     string
		class    provider.FailureClass
		requests int32
	}{
		{401, echo, provider.PermissionError, 1},
		{403, echo, provider.PermissionError, 1},
		{429, echo, provider.UsageLimit, 2},
		{402, echo, provider.UsageLimit, 1},
		{400, echo, provider.ProviderError, 1},
		{500, echo, provider.ProviderError, 2},
		{529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, provider.ProviderError, 2},
		{200, `<html>not json</html>`, provider.ProviderError, 1},
		{200, `{"type":"message"}`, provider.ProviderError, 1},
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
		func(*testing.T, *http.Request, map[string]any) (int, string) { return 503, `{}` },
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
		http.Redirect(w, r, other.URL+"/v1/messages", http.StatusTemporaryRedirect)
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
		if _, ok := r.Header["X-Api-Key"]; ok {
			t.Error("a key was sent")
		}
		return submitStep(t, r, body)
	})
	if _, err := newDriver(t, srv.URL, "").Run(context.Background(), newJob(t)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ base, key string }{
		{"http://api.example.com", "env:ACME_KEY"},
		{"https://api.example.com", ""},
		{"", ""},
		{"https://api.example.com", testKey},
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
	job.Model = "claude-opus-5-5"
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

	broken := newDriver(t, srv.URL, "env:OTHER")
	if h := broken.Health(context.Background()); h.Installed || !strings.Contains(h.Detail, "env:OTHER") {
		t.Errorf("health = %+v", h)
	}
	if st := broken.Status(context.Background()); st.SignedIn {
		t.Errorf("status = %+v", st)
	}
	_, err = broken.Run(context.Background(), job)
	if !errors.As(err, &perr) || perr.Class != provider.PermissionError {
		t.Errorf("unresolved key: %v", err)
	}
}

func TestAutomaticCacheOnTheVendorAddressOnly(t *testing.T) {
	req := agentloop.Request{System: "s", Messages: []agentloop.Message{{Role: agentloop.RoleUser, Text: "p"}}}
	if buildRequest("m", req, true).CacheControl == nil || buildRequest("m", req, false).CacheControl != nil {
		t.Error("automatic caching is not set as asked")
	}
	d, err := New(config.Provider{ID: "acme", APIKey: "env:ACME_KEY"}, Options{ResolveKey: func(string) (string, error) { return testKey, nil }})
	if err != nil || d.endpoint.Base.String() != DefaultBase {
		t.Fatalf("default address %v: %v", d.endpoint.Base, err)
	}
}

func TestEmptyAssistantMessageIsLeftOut(t *testing.T) {
	req := agentloop.Request{Messages: []agentloop.Message{
		{Role: agentloop.RoleUser, Text: "p"},
		{Role: agentloop.RoleAssistant, Native: json.RawMessage(`[]`)},
		{Role: agentloop.RoleUser, Text: "reminder"},
	}}
	out := buildRequest("m", req, false)
	if len(out.Messages) != 2 || out.Messages[1].Role != "user" {
		t.Errorf("messages = %+v", out.Messages)
	}
}
