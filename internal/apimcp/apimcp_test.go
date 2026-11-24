package apimcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jancuk/semar/internal/apicheck"
	"github.com/jancuk/semar/internal/apimcp"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/openapi"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/takeover"
)

const seedToken = "tok-seed-9f3a7c"

var testKey = []byte("0123456789abcdef0123456789abcdef")

type sink struct {
	mu      sync.Mutex
	actions []domain.Action
}

func (s *sink) WriteAction(a domain.Action) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, a)
	return "actions/" + string(a.ID) + ".json", nil
}
func (s *sink) AppendActivity(domain.Activity) error { return nil }
func (s *sink) all() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.actions)
	return string(b)
}

type seen struct {
	Method, Path, Auth, Query string
	Header                    http.Header
	Body                      string
}

type rigOptions struct {
	mode     string
	kind     string
	observe  bool
	deny     []config.OpPattern
	once     []config.OpPattern
	reads    []config.OpPattern
	hosts    []string // replaces the host of the server
	methods  []string
	budget   int
	handler  http.HandlerFunc
	docs     string
	baton    *takeover.Baton
	checker  bool
	runID    string
	maxCheck bool
}

type rig struct {
	t     *testing.T
	m     *apimcp.Manager
	srv   *httptest.Server
	sink  *sink
	mu    sync.Mutex
	reqs  []seen
	hits  atomic.Int64
	guard mcpkit.Guard
	tools map[string]mcp.Tool
	scrub *mcpkit.Scrubber
}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	r := &rig{t: t, sink: &sink{}, tools: map[string]mcp.Tool{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		b := make([]byte, 1<<20)
		n, _ := req.Body.Read(b)
		r.mu.Lock()
		r.reqs = append(r.reqs, seen{req.Method, req.URL.Path, req.Header.Get("Authorization"), req.URL.RawQuery, req.Header.Clone(), string(b[:n])})
		r.mu.Unlock()
		if o.handler != nil {
			o.handler(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(r.srv.Close)
	u, _ := url.Parse(r.srv.URL)

	env := config.Environment{
		Name: "test", Kind: o.kind, Hosts: []string{u.Host},
		Methods: []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"},
		Effects: config.Effects{Mode: orDefault(o.mode, "test_data"), Deny: o.deny, Once: o.once, Reads: o.reads},
	}
	if o.hosts != nil {
		env.Hosts = o.hosts
	}
	if o.methods != nil {
		env.Methods = o.methods
	}
	pol, err := effects.New(env)
	if err != nil {
		t.Fatal(err)
	}
	red, err := redact.New(config.Redact{}, func() []string { return []string{"Bearer " + seedToken, seedToken} }, testKey)
	if err != nil {
		t.Fatal(err)
	}
	classify := func(method string, u *url.URL) effects.Class {
		return pol.Classify(effects.Request{Method: method, URL: u})
	}
	var doc openapi.Document
	var docs map[string][]openapi.Document
	var checker *apicheck.Checker
	if o.docs != "" {
		doc, err = openapi.Parse([]byte(o.docs), "orders.json")
		if err != nil {
			t.Fatal(err)
		}
		docs = map[string][]openapi.Document{"orders": {doc}}
		checker = apicheck.New(apicheck.Options{Env: "test", Schema: apicheck.Fail, Docs: []apicheck.Source{{Service: "orders", Doc: doc}}})
	}
	eng := api.New(api.Options{
		Policy: pol, Recorder: recorder.New(r.sink, red, classify, "r-1"),
		Services: map[string]string{"orders": r.srv.URL}, Run: "r-1", Version: "test", Budget: o.budget, Doc: checker,
		Session: func(_ context.Context, profile string) (string, string, error) {
			if profile != "support" {
				return "", "", fmt.Errorf("no session for %s", profile)
			}
			return "Authorization", "Bearer " + seedToken, nil
		},
	})
	r.guard = mcpkit.Guard{}
	if o.observe {
		r.guard = mcpkit.Guard{Observe: true, Reason: "the environment is production"}
	}
	r.scrub = mcpkit.NewScrubber(nil, seedToken)
	r.m = apimcp.New(apimcp.Options{
		Guard: r.guard, Scrub: r.scrub, Baton: o.baton, Policy: pol,
		Services: []apimcp.ServiceInfo{{
			Name: "orders", BaseURL: r.srv.URL, Hosts: env.Hosts, Profiles: []string{"support"},
			OpenAPI: len(doc.Operations), Mode: env.Effects.Mode, Kind: o.kind, Deny: len(o.deny), Once: len(o.once),
		}},
		Docs:     docs,
		Backend:  func(context.Context) (apimcp.RequestBackend, error) { return eng, nil },
		RunID:    func(context.Context) (string, error) { return orDefault(o.runID, "r-1"), nil },
		Profiles: []string{"support"},
	})
	for _, tool := range r.m.Tools() {
		tool.Handler = r.guard.Middleware()(tool, tool.Handler)
		r.tools[tool.Name] = tool
	}
	return r
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (r *rig) call(name string, args map[string]any) (string, bool) {
	r.t.Helper()
	res := r.callResult(name, args)
	var sb strings.Builder
	for _, c := range res.Content {
		sb.WriteString(c.Text)
	}
	return sb.String(), res.IsError
}

func (r *rig) callResult(name string, args map[string]any) mcp.Result {
	r.t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		r.t.Fatal(err)
	}
	res, err := r.tools[name].Handler(r.t.Context(), mcp.Call{Name: name, Args: raw, Progress: func(float64, float64, string) {}})
	if err != nil {
		r.t.Fatalf("%s: %v", name, err)
	}
	return res
}

type reply struct {
	Sent    bool              `json:"sent"`
	Status  int               `json:"status"`
	Refused string            `json:"refused"`
	Error   string            `json:"error"`
	Path    string            `json:"path"`
	Body    json.RawMessage   `json:"body"`
	BodyCut bool              `json:"bodyCut"`
	Cap     map[string]string `json:"captured"`
	Lifted  []string          `json:"lifted"`
	Doc     *struct {
		Operation string `json:"operation"`
		Misfits   []any  `json:"misfits"`
	} `json:"doc"`
	Expect []struct {
		State    string `json:"state"`
		Recorded bool   `json:"recorded"`
	} `json:"expect"`
}

func (r *rig) req(args map[string]any) (reply, string, bool) {
	r.t.Helper()
	text, isErr := r.call("api_request", args)
	var rp reply
	_ = json.Unmarshal([]byte(text), &rp)
	return rp, text, isErr
}

func (r *rig) seen() []seen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seen(nil), r.reqs...)
}

func get(path string) map[string]any {
	return map[string]any{"service": "orders", "method": "GET", "path": path}
}

func post(path, body, why string) map[string]any {
	return map[string]any{"service": "orders", "method": "POST", "path": path, "json": body, "why": why}
}

func TestMCPToolsArePortableAndNamed(t *testing.T) {
	m := apimcp.New(apimcp.Options{})
	var names []string
	for _, tool := range m.Tools() {
		names = append(names, tool.Name)
		if err := mcp.ValidatePortableSchema(tool.Input); err != nil {
			t.Errorf("%s: %v", tool.Name, err)
		}
		if tool.Surface != "api" {
			t.Errorf("%s surface = %q", tool.Name, tool.Surface)
		}
		// api_request judges each request itself, so the guard must not block its reads.
		if tool.Write {
			t.Errorf("%s is marked Write, which would block every GET in observe mode", tool.Name)
		}
		if tool.Handler == nil {
			t.Errorf("%s has no handler", tool.Name)
		}
	}
	if got, want := strings.Join(names, ","), "api_list_services,api_list_operations,api_request"; got != want {
		t.Errorf("tools = %s, want %s", got, want)
	}
	if _, err := mcp.NewServer(mcp.Info{Name: "t"}, m.Tools(), nil); err != nil {
		t.Errorf("the server rejects the tools: %v", err)
	}
}

func TestMCPListServicesShowsProfileNamesNeverTokens(t *testing.T) {
	r := newRig(t, rigOptions{observe: true, kind: "production", mode: "read_only", once: []config.OpPattern{{Method: "POST", Path: "/v1/x"}}})
	text, isErr := r.call("api_list_services", nil)
	if isErr {
		t.Fatal(text)
	}
	for _, want := range []string{`"name":"orders"`, `"authProfiles":["support"]`, `"policyMode":"read_only"`, `"onceRules":1`, `"observing":true`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
	if strings.Contains(text, seedToken) {
		t.Error("the listing shows a token")
	}
}

const ordersDoc = `{"openapi":"3.0.0","info":{"title":"o","version":"1"},"paths":{
 "/v1/orders":{"get":{"operationId":"listOrders","summary":"List the orders","responses":{"200":{"description":"ok"}}},
               "post":{"operationId":"createOrder","summary":"Create an order","responses":{"201":{"description":"ok"}}}},
 "/v1/orders/{id}":{"get":{"operationId":"getOrder","summary":"Read one order","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}}}}},
                    "delete":{"operationId":"deleteOrder","summary":"Delete","responses":{"204":{"description":"gone"}}}},
 "/admin/purge":{"post":{"operationId":"purge","summary":"Purge","responses":{"200":{"description":"ok"}}}}
}}`

func TestMCPListOperationsClassesAndRefusals(t *testing.T) {
	r := newRig(t, rigOptions{docs: ordersDoc, deny: []config.OpPattern{{Method: "POST", Path: "/admin/**"}}})
	text, isErr := r.call("api_list_operations", map[string]any{"service": "orders"})
	if isErr {
		t.Fatal(text)
	}
	var out struct {
		Operations []apimcp.OperationInfo `json:"operations"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	by := map[string]apimcp.OperationInfo{}
	for _, op := range out.Operations {
		by[op.Method+" "+op.Path] = op
	}
	if len(by) != 5 {
		t.Fatalf("operations = %d: %s", len(by), text)
	}
	if op := by["GET /v1/orders/{id}"]; op.Class != "read" || op.Refused != "" || op.OperationID != "getOrder" || op.Summary != "Read one order" {
		t.Errorf("get = %+v", op)
	}
	if op := by["POST /v1/orders"]; op.Class != "write" || op.Refused != "" {
		t.Errorf("post = %+v", op)
	}
	if op := by["POST /admin/purge"]; op.Class != "write" || !strings.Contains(op.Refused, "denied") {
		t.Errorf("purge = %+v", op)
	}

	text, _ = r.call("api_list_operations", map[string]any{"service": "orders", "match": "DELETE"})
	if !strings.Contains(text, "deleteOrder") || strings.Contains(text, "listOrders") {
		t.Errorf("match = %s", text)
	}
	text, _ = r.call("api_list_operations", map[string]any{"service": "orders", "limit": 2})
	if strings.Count(text, `"method"`) != 2 || !strings.Contains(text, `"more":3`) {
		t.Errorf("limit = %s", text)
	}
	if text, isErr = r.call("api_list_operations", map[string]any{"service": "orders", "limit": 201}); !isErr {
		t.Errorf("limit 201 was accepted: %s", text)
	}
	if text, isErr = r.call("api_list_operations", map[string]any{"service": "nope"}); !isErr || !strings.Contains(text, "orders") {
		t.Errorf("unknown service = %v %s", isErr, text)
	}
	bare := newRig(t, rigOptions{})
	if text, _ = bare.call("api_list_operations", map[string]any{"service": "orders"}); !strings.Contains(text, "no OpenAPI") {
		t.Errorf("no docs note = %s", text)
	}
}

func TestMCPPolicyMatrix(t *testing.T) {
	tests := []struct {
		name     string
		o        rigOptions
		args     map[string]any
		wantSent bool
		wantText string
	}{
		{"read works", rigOptions{}, get("/v1/orders"), true, ""},
		{"write works in a test environment", rigOptions{}, post("/v1/orders", `{"a":1}`, "create"), true, ""},
		{"host not allowed", rigOptions{hosts: []string{"example.test"}}, get("/v1/orders"), false, "host_not_allowed"},
		{"method not allowed", rigOptions{methods: []string{"GET"}}, post("/v1/orders", `{}`, "create"), false, "method_not_allowed"},
		{"deny list", rigOptions{deny: []config.OpPattern{{Method: "GET", Path: "/secret/**"}}}, get("/secret/x"), false, "denied"},
		{"read_only refuses a write", rigOptions{mode: "read_only"}, post("/v1/orders", `{}`, "create"), false, "read_only"},
		{"read_only allows a read", rigOptions{mode: "read_only"}, get("/v1/orders"), true, ""},
		{"observe allows GET", rigOptions{observe: true, kind: "production", mode: "read_only"}, get("/v1/orders"), true, ""},
		{"observe refuses POST before Do", rigOptions{observe: true, kind: "production"}, post("/v1/orders", `{}`, "create"), false, "only observes"},
		{"observe refuses DELETE before Do", rigOptions{observe: true}, map[string]any{"service": "orders", "method": "DELETE", "path": "/v1/orders/1", "why": "x"}, false, "only observes"},
		{"once is always refused", rigOptions{once: []config.OpPattern{{Method: "POST", Path: "/v1/orders"}}}, post("/v1/orders", `{}`, "create"), false, "effects.once"},
		{"a write needs why", rigOptions{}, map[string]any{"service": "orders", "method": "POST", "path": "/v1/orders"}, false, "needs why"},
		{"a dot segment is refused", rigOptions{}, get("/v1/../admin"), false, "dot segment"},
		{"a query in the path is refused", rigOptions{}, get("/v1/orders?x=1"), false, "?"},
		{"a reads pattern makes a POST a read", rigOptions{observe: true, reads: []config.OpPattern{{Method: "POST", Path: "/v1/search"}}}, post("/v1/search", `{"q":"a"}`, ""), true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.o)
			rp, text, isErr := r.req(tc.args)
			if tc.wantSent {
				if !rp.Sent || isErr || r.hits.Load() != 1 {
					t.Fatalf("sent=%v isErr=%v hits=%d: %s", rp.Sent, isErr, r.hits.Load(), text)
				}
				return
			}
			if rp.Sent || !isErr || r.hits.Load() != 0 {
				t.Fatalf("sent=%v isErr=%v hits=%d, want a refusal before the server: %s", rp.Sent, isErr, r.hits.Load(), text)
			}
			if !strings.Contains(text, tc.wantText) {
				t.Errorf("the refusal should say %q: %s", tc.wantText, text)
			}
			if r.m.Seq() != 0 {
				t.Errorf("a refused request was logged")
			}
		})
	}
}

func TestMCPObserveModeKeepsReadsAndStopsEveryWrite(t *testing.T) {
	r := newRig(t, rigOptions{observe: true, kind: "production", handler: func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			t.Errorf("the server got a %s", req.Method)
		}
		_, _ = w.Write([]byte(`{}`))
	}})
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		_, text, isErr := r.req(map[string]any{"service": "orders", "method": m, "path": "/v1/orders/1", "why": "try"})
		if !isErr || !strings.Contains(text, "only observes") {
			t.Errorf("%s: %v %s", m, isErr, text)
		}
	}
	if rp, text, _ := r.req(get("/v1/orders/1")); !rp.Sent {
		t.Errorf("GET: %s", text)
	}
	if rp, text, _ := r.req(map[string]any{"service": "orders", "method": "HEAD", "path": "/v1/orders/1"}); !rp.Sent {
		t.Errorf("HEAD: %s", text)
	}
}

func TestMCPBudgetStopsTheEngine(t *testing.T) {
	r := newRig(t, rigOptions{budget: 2})
	for i := 0; i < 2; i++ {
		if rp, text, _ := r.req(get("/v1/orders")); !rp.Sent {
			t.Fatalf("request %d: %s", i, text)
		}
	}
	rp, text, isErr := r.req(get("/v1/orders"))
	if rp.Sent || !isErr || !strings.Contains(rp.Refused, "budget") || r.hits.Load() != 2 {
		t.Errorf("third request: hits=%d %s", r.hits.Load(), text)
	}
}

func TestMCPHeaderAllowlist(t *testing.T) {
	good := []string{"Accept", "content-type", "If-Match", "If-None-Match", "Idempotency-Key", "X-Request-Id", "X-Trace-Hint", "X-A"}
	for _, h := range good {
		if err := apimcp.CheckHeaders(map[string]string{h: "v"}); err != nil {
			t.Errorf("%s: %v", h, err)
		}
	}
	bad := []string{"Authorization", "Proxy-Authorization", "X-Api-Key", "X-Auth-Token", "Cookie", "Set-Cookie", "X-Session-Id", "X-Secret", "Api-Key", "User-Agent", "Host", "X-" + strings.Repeat("a", 65), "X-bad header"}
	for _, h := range bad {
		if err := apimcp.CheckHeaders(map[string]string{h: "v"}); err == nil {
			t.Errorf("%s was accepted", h)
		}
	}
	if err := apimcp.CheckHeaders(map[string]string{"Accept": "a\r\nX-Evil: 1"}); err == nil {
		t.Error("a header value with CRLF was accepted")
	}

	r := newRig(t, rigOptions{})
	args := get("/v1/orders")
	args["headers"] = []map[string]string{{"name": "Authorization", "value": "Bearer stolen"}}
	if rp, text, isErr := r.req(args); rp.Sent || !isErr || !strings.Contains(text, "as parameter") || r.hits.Load() != 0 {
		t.Errorf("credential header: %v %s", isErr, text)
	}
	args["headers"] = map[string]string{"X-Request-Id": "abc", "Accept": "application/json"}
	if rp, text, _ := r.req(args); !rp.Sent {
		t.Fatal(text)
	}
	if got := r.seen()[0].Header.Get("X-Request-Id"); got != "abc" {
		t.Errorf("X-Request-Id = %q", got)
	}
}

func TestMCPAuthProfileTokenNeverLeaks(t *testing.T) {
	r := newRig(t, rigOptions{handler: func(w http.ResponseWriter, req *http.Request) {
		// The service echoes the header and the bare token, in the body and in a header.
		auth := req.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", strings.TrimPrefix(auth, "Bearer "))
		_ = json.NewEncoder(w).Encode(map[string]any{"echo": auth, "token": strings.TrimPrefix(auth, "Bearer "), "id": "o-1"})
	}})
	args := get("/v1/orders/1")
	args["as"] = "support"
	args["captures"] = []map[string]string{{"name": "tok", "jsonpath": "$.token"}}
	rp, text, isErr := r.req(args)
	if !rp.Sent || isErr {
		t.Fatalf("%s", text)
	}
	if got := r.seen()[0].Auth; got != "Bearer "+seedToken {
		t.Fatalf("the server got Authorization %q", got)
	}
	var texts []string
	texts = append(texts, text)
	for _, tool := range []string{"api_list_services"} {
		s, _ := r.call(tool, nil)
		texts = append(texts, s)
	}
	for _, step := range r.m.Since(0) {
		b, _ := json.Marshal(step)
		texts = append(texts, string(b))
	}
	texts = append(texts, r.sink.all())
	for i, s := range texts {
		if strings.Contains(s, seedToken) {
			t.Errorf("text %d holds the token:\n%s", i, s)
		}
	}
	if strings.Contains(r.sink.all(), "Bearer") && !strings.Contains(r.sink.all(), "[session of support]") {
		t.Errorf("the action should record [session of support]:\n%s", r.sink.all())
	}

	args["as"] = "ghost"
	if _, text, isErr := r.req(args); !isErr || !strings.Contains(text, "support") {
		t.Errorf("an unknown profile = %v %s", isErr, text)
	}
}

func TestMCPTemplatingCapturesAndLiteralLifting(t *testing.T) {
	r := newRig(t, rigOptions{handler: func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"ord-12345","amount":100}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"ord-12345"}}`))
	}})
	args := post("/v1/orders", `{"amount":100}`, "create an order")
	args["captures"] = []map[string]string{{"name": "order_id", "jsonpath": "$.data.id"}}
	rp, text, _ := r.req(args)
	if !rp.Sent || rp.Status != 201 || rp.Cap["order_id"] != "ord-12345" {
		t.Fatalf("create: %s", text)
	}
	if v, _ := r.m.Var("order_id"); v != "ord-12345" {
		t.Errorf("var = %q", v)
	}

	// A template renders from the variables, and the log keeps the template.
	rp, text, _ = r.req(get("/v1/orders/{{order_id}}"))
	if !rp.Sent || r.seen()[1].Path != "/v1/orders/ord-12345" || rp.Path != "/v1/orders/{{order_id}}" {
		t.Fatalf("read: %s (%v)", text, r.seen())
	}
	// A literal that equals a capture is lifted, and the result says so.
	rp, text, _ = r.req(get("/v1/orders/ord-12345"))
	if !rp.Sent || rp.Path != "/v1/orders/{{order_id}}" || len(rp.Lifted) != 1 || !strings.Contains(rp.Lifted[0], "{{order_id}}") {
		t.Fatalf("lift: %s", text)
	}
	// A JSON string leaf is lifted too, and a short value is not.
	r.m.SetVar("short", "ab")
	_, text, _ = r.req(post("/v1/orders/{{order_id}}/refunds", `{"order":"ord-12345","note":"ab","n":5}`, "refund"))
	steps := r.m.Since(0)
	if len(steps) != 4 {
		t.Fatalf("steps = %d: %s", len(steps), text)
	}
	if !strings.Contains(text, `"lifted"`) {
		t.Errorf("no lift note: %s", text)
	}
	st := steps[3]
	js := st.Request.JSON.(map[string]any)
	if js["order"] != "{{order_id}}" || js["note"] != "ab" {
		t.Errorf("logged json = %v", js)
	}
	if st.Request.Path != "/v1/orders/{{order_id}}/refunds" || st.PathTemplate != st.Request.Path || !st.Write || st.Action != "refund" || st.Service != "orders" || st.Status != 201 {
		t.Errorf("step = %+v", st)
	}
	for i, s := range steps {
		if strings.Contains(s.Request.Path, "ord-12345") {
			t.Errorf("step %d logs the rendered path %q", i, s.Request.Path)
		}
	}
	if steps[0].Captures[0].Name != "order_id" {
		t.Errorf("captures = %+v", steps[0].Captures)
	}

	// An unknown variable is a refusal that names the variable and sends nothing.
	before := r.hits.Load()
	if _, text, isErr := r.req(get("/v1/orders/{{nothing}}")); !isErr || !strings.Contains(text, "nothing") || r.hits.Load() != before {
		t.Errorf("unknown variable: %v %s", isErr, text)
	}
	// A path variable cannot change the endpoint.
	r.m.SetVar("evil", "../admin")
	if _, text, isErr := r.req(get("/v1/orders/{{evil}}")); !isErr || r.hits.Load() != before {
		t.Errorf("a dotted value was sent: %s", text)
	}
	// A journey variable and run.id render.
	r.m.SetVar("leg.name", "from-leg")
	args = get("/v1/x")
	args["query"] = map[string]string{"leg": "{{leg.name}}", "run": "{{run.id}}"}
	if rp, text, _ := r.req(args); !rp.Sent {
		t.Fatal(text)
	}
	if q := r.seen()[len(r.seen())-1].Query; q != "leg=from-leg&run=r-1" {
		t.Errorf("query = %q", q)
	}
}

func TestMCPLogSeqSinceAndDropLast(t *testing.T) {
	r := newRig(t, rigOptions{handler: func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"abcd1234"}`))
	}})
	if r.m.DropLast() || r.m.Seq() != 0 {
		t.Fatal("an empty log dropped a step")
	}
	r.m.SetVar("id", "old-value")
	a := get("/v1/a")
	a["captures"] = []map[string]string{{"name": "id", "jsonpath": "$.id"}, {"name": "other", "jsonpath": "$.id"}}
	r.req(a)
	r.req(get("/v1/b"))
	if r.m.Seq() != 2 || len(r.m.Since(1)) != 1 || len(r.m.Since(0)) != 2 || r.m.Since(5) != nil {
		t.Fatalf("seq=%d", r.m.Seq())
	}
	if !r.m.DropLast() || r.m.Seq() != 1 {
		t.Fatal("DropLast failed")
	}
	if v, _ := r.m.Var("id"); v != "abcd1234" {
		t.Errorf("id = %q", v)
	}
	if !r.m.DropLast() {
		t.Fatal("second DropLast failed")
	}
	if v, _ := r.m.Var("id"); v != "old-value" {
		t.Errorf("id was not restored: %q", v)
	}
	if _, ok := r.m.Var("other"); ok {
		t.Error("a captured variable survived the undo")
	}
	// The log hands out copies.
	r.req(get("/v1/c"))
	s := r.m.Since(0)
	s[0].Request.Path = "/changed"
	if r.m.Since(0)[0].Request.Path == "/changed" {
		t.Error("Since gave the log itself")
	}
}

func TestMCPExpectIsJudgedByCode(t *testing.T) {
	r := newRig(t, rigOptions{handler: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"ord-9999","status":"created","amount":100,"tags":["a","b"]}}`))
	}})
	r.m.SetVar("known", "ord-9999")
	args := get("/v1/orders/1")
	args["captures"] = []map[string]string{{"name": "oid", "jsonpath": "$.data.id"}}
	args["expect"] = []map[string]any{
		{"status": 200},
		{"jsonpath": "$.data.status", "equals": "created"},
		{"jsonpath": "$.data.amount", "equals": "100"},
		{"jsonpath": "$.data.amount", "equals": 100},
		{"jsonpath": "$.data.id", "equals": "{{known}}"},
		{"jsonpath": "$.data.id", "equals": "{{oid}}"},
		{"jsonpath": "$.data.tags", "exists": true},
		{"jsonpath": "$.data.status", "contains": "reat"},
		{"header": "Content-Type", "contains": "json"},
		{"status": 404},
		{"jsonpath": "$.data.status", "equals": "paid"},
		{"jsonpath": "$.data.amount", "equals": "\"100\""},
	}
	rp, text, isErr := r.req(args)
	if !rp.Sent || isErr {
		t.Fatal(text)
	}
	want := []string{"true", "true", "true", "true", "true", "true", "true", "true", "true", "false", "false", "false"}
	if len(rp.Expect) != len(want) {
		t.Fatalf("expect = %d: %s", len(rp.Expect), text)
	}
	for i, w := range want {
		if rp.Expect[i].State != w || rp.Expect[i].Recorded != (w == "true") {
			t.Errorf("expect[%d] = %+v, want %s\n%s", i, rp.Expect[i], w, text)
		}
	}
	step := r.m.Since(0)[0]
	if len(step.Expect) != 9 {
		t.Fatalf("recorded %d, want the 9 that held", len(step.Expect))
	}
	for _, e := range step.Expect {
		if e.By != "model" || e.Source != domain.SourceBaseline {
			t.Errorf("expectation = %+v", e)
		}
	}
	// The equals that was a variable stays a template in the log.
	if got := step.Expect[4].Check.Equals; got != "{{known}}" {
		t.Errorf("logged equals = %v", got)
	}

	bad := []map[string]any{
		{}, {"status": 200, "jsonpath": "$.a", "exists": true}, {"jsonpath": "$.a"}, {"jsonpath": "$.a", "equals": "x", "exists": true},
		{"status": 200, "equals": "1"},
	}
	for i, e := range bad {
		args["expect"] = []map[string]any{e}
		if _, text, isErr := r.req(args); !isErr || !strings.Contains(text, "expect[0]") {
			t.Errorf("bad expect %d accepted: %s", i, text)
		}
	}
}

func TestMCPBodyIsCapped(t *testing.T) {
	big := strings.Repeat("a", 300<<10)
	r := newRig(t, rigOptions{handler: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(big))
	}})
	args := get("/v1/big")
	rp, text, _ := r.req(args)
	if !rp.Sent || !rp.BodyCut || len(text) > apimcp.DefaultBody+4096 {
		t.Errorf("default cap: cut=%v len=%d", rp.BodyCut, len(text))
	}
	args["maxBody"] = apimcp.MaxBody
	rp, text, _ = r.req(args)
	if !rp.BodyCut || len(text) < apimcp.MaxBody || len(text) > apimcp.MaxBody+4096 {
		t.Errorf("max cap: cut=%v len=%d", rp.BodyCut, len(text))
	}
	args["maxBody"] = apimcp.MaxBody + 1
	if _, text, isErr := r.req(args); !isErr || !strings.Contains(text, "maxBody") {
		t.Errorf("maxBody over the limit: %s", text)
	}
	args["maxBody"] = 10
	rp, _, _ = r.req(args)
	var s string
	_ = json.Unmarshal(rp.Body, &s)
	if len(s) != 10 {
		t.Errorf("a 10 byte cap gave %d bytes", len(s))
	}
}

func TestMCPDocVerdictFromOpenAPI(t *testing.T) {
	r := newRig(t, rigOptions{docs: ordersDoc, handler: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":5}`))
	}})
	rp, text, _ := r.req(get("/v1/orders/o-1"))
	if !rp.Sent || rp.Doc == nil || rp.Doc.Operation != "GET /v1/orders/{id}" || len(rp.Doc.Misfits) == 0 {
		t.Errorf("doc verdict missing: %s", text)
	}
}

func TestMCPWhyIsBounded(t *testing.T) {
	r := newRig(t, rigOptions{})
	if _, text, isErr := r.req(post("/v1/orders", `{}`, strings.Repeat("x", apimcp.MaxWhy+1))); !isErr || !strings.Contains(text, "why") {
		t.Errorf("long why: %s", text)
	}
	if rp, text, _ := r.req(post("/v1/orders", `{}`, strings.Repeat("x", apimcp.MaxWhy))); !rp.Sent {
		t.Errorf("200 characters: %s", text)
	}
}

func TestMCPJSONArgumentForms(t *testing.T) {
	r := newRig(t, rigOptions{})
	args := post("/v1/orders", `{"n":12345678901234567890}`, "big number")
	if rp, text, _ := r.req(args); !rp.Sent || r.seen()[0].Body != `{"n":12345678901234567890}` {
		t.Errorf("text form: %s %q", text, r.seen()[0].Body)
	}
	args["json"] = map[string]any{"a": "b"}
	if rp, text, _ := r.req(args); !rp.Sent || r.seen()[1].Body != `{"a":"b"}` {
		t.Errorf("object form: %s %q", text, r.seen()[1].Body)
	}
	args["json"] = `{"a":`
	if _, text, isErr := r.req(args); !isErr || !strings.Contains(text, "json") {
		t.Errorf("bad json: %s", text)
	}
}

func TestMCPHumanInControlRefusesWritesNotReads(t *testing.T) {
	baton := takeover.New(nil)
	r := newRig(t, rigOptions{baton: baton})
	baton.Yield("looking at the screen")

	res := r.callResult("api_request", post("/v1/orders", `{}`, "create"))
	st, _ := res.Structured.(map[string]any)
	if !res.IsError || st["status"] != "human_in_control" || st["next"] != "semar_wait_for_control" || r.hits.Load() != 0 {
		t.Errorf("write under human control: %+v hits=%d", res, r.hits.Load())
	}
	if rp, text, _ := r.req(get("/v1/orders")); !rp.Sent {
		t.Errorf("a read under human control: %s", text)
	}
	baton.Resume("done")
	if rp, text, _ := r.req(post("/v1/orders", `{}`, "create")); !rp.Sent {
		t.Errorf("a write after the hand-back: %s", text)
	}
}

func TestMCPConcurrentRequests(t *testing.T) {
	r := newRig(t, rigOptions{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.req(get("/v1/orders"))
			r.m.Seq()
			r.m.Since(0)
			r.m.SetVar("k", "vvvv")
		}()
	}
	wg.Wait()
	if r.m.Seq() != 8 {
		t.Errorf("seq = %d", r.m.Seq())
	}
}

func TestMCPBackendErrorIsReported(t *testing.T) {
	m := apimcp.New(apimcp.Options{
		Policy:   mustPolicy(t),
		Services: []apimcp.ServiceInfo{{Name: "orders", BaseURL: "http://127.0.0.1:1"}},
		Backend: func(context.Context) (apimcp.RequestBackend, error) {
			return nil, fmt.Errorf("no environment is bound")
		},
	})
	var tool mcp.Tool
	for _, x := range m.Tools() {
		if x.Name == "api_request" {
			tool = x
		}
	}
	raw, _ := json.Marshal(get("/v1/orders"))
	res, err := tool.Handler(t.Context(), mcp.Call{Args: raw})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].Text, "no environment is bound") {
		t.Errorf("%+v %v", res, err)
	}
}

func mustPolicy(t *testing.T) *effects.Policy {
	t.Helper()
	p, err := effects.New(config.Environment{Hosts: []string{"127.0.0.1:1"}, Methods: []string{"GET"}, Effects: config.Effects{Mode: "test_data"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
