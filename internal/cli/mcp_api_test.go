package cli_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

type apiReply struct {
	Sent    bool              `json:"sent"`
	Status  int               `json:"status"`
	Refused string            `json:"refused"`
	Path    string            `json:"path"`
	Body    json.RawMessage   `json:"body"`
	Cap     map[string]string `json:"captured"`
	Expect  []struct {
		State string `json:"state"`
	} `json:"expect"`
}

func apiCall(t *testing.T, p *mcpProc, args map[string]any) (apiReply, string, bool) {
	t.Helper()
	text, isErr := p.call("api_request", args)
	var rp apiReply
	_ = json.Unmarshal([]byte(text), &rp)
	return rp, text, isErr
}

func startAPIMCP(t *testing.T, r *repo) *mcpProc {
	t.Helper()
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--surfaces", "api")
	p.initialize()
	return p
}

func TestMCPAPIRequestsInATestEnvironment(t *testing.T) {
	var posts, gets atomic.Int64
	r := newRepo(t, repoOptions{wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			switch req.Method {
			case http.MethodPost:
				posts.Add(1)
			case http.MethodGet:
				gets.Add(1)
			}
			next.ServeHTTP(w, req)
		})
	}})
	p := startAPIMCP(t, r)

	names := p.toolNames()
	for _, want := range []string{"api_list_services", "api_list_operations", "api_request"} {
		if !hasTool(names, want) {
			t.Fatalf("%s is not listed: %v", want, names)
		}
	}
	for _, n := range names {
		if strings.HasPrefix(n, "mobile_") {
			t.Errorf("--surfaces api lists %s", n)
		}
	}
	// Decision E1: the run is made on the first engine action, not before.
	if dirs := r.runDirsOrNone(); len(dirs) != 0 {
		t.Fatalf("a run exists before any request: %v", dirs)
	}

	text, isErr := p.call("api_list_services", nil)
	if isErr || !strings.Contains(text, `"name":"orders"`) || !strings.Contains(text, `"support-agent"`) {
		t.Fatalf("api_list_services: %v %s", isErr, text)
	}
	if strings.Contains(text, token) {
		t.Fatal("the service list shows the token")
	}
	if dirs := r.runDirsOrNone(); len(dirs) != 0 {
		t.Fatalf("a listing made a run: %v", dirs)
	}

	// A write with a capture, signed in through the profile.
	rp, text, isErr := apiCall(t, p, map[string]any{
		"service": "orders", "method": "POST", "path": "/v1/orders", "as": "support-agent",
		"json": `{"amount": 1200, "customer": "c-1"}`, "why": "create an order to read back",
		"captures": []map[string]string{{"name": "order_id", "jsonpath": "$.data.id"}},
		"expect":   []map[string]any{{"status": 201}},
	})
	if isErr || !rp.Sent || rp.Status != 201 || rp.Cap["order_id"] == "" {
		t.Fatalf("create: %v %s", isErr, text)
	}
	if len(rp.Expect) != 1 || rp.Expect[0].State != "true" {
		t.Errorf("expect: %s", text)
	}

	// A read through the captured variable.
	rp, text, isErr = apiCall(t, p, map[string]any{
		"service": "orders", "method": "GET", "path": "/v1/orders/{{order_id}}", "as": "support-agent",
		"expect": []map[string]any{{"jsonpath": "$.data.id", "equals": "{{order_id}}"}},
	})
	if isErr || !rp.Sent || rp.Status != 200 || rp.Path != "/v1/orders/{{order_id}}" || len(rp.Expect) != 1 || rp.Expect[0].State != "true" {
		t.Fatalf("read: %v %s", isErr, text)
	}
	if posts.Load() != 1 {
		t.Errorf("the server got %d POSTs, want 1", posts.Load())
	}

	// A write that is not allowed by the tool's own rules never reaches the server.
	if _, text, isErr := apiCall(t, p, map[string]any{"service": "orders", "method": "POST", "path": "/v1/orders", "json": `{"amount":1}`}); !isErr || !strings.Contains(text, "why") {
		t.Errorf("a write without why: %s", text)
	}
	if _, text, isErr := apiCall(t, p, map[string]any{"service": "orders", "method": "GET", "path": "/v1/orders", "headers": []map[string]string{{"name": "Authorization", "value": "Bearer x"}}}); !isErr || !strings.Contains(text, "as parameter") {
		t.Errorf("a credential header: %s", text)
	}
	if posts.Load() != 1 {
		t.Errorf("a refused write reached the server: %d POSTs", posts.Load())
	}

	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
	// The run holds what the agent sent, and no file holds the token.
	if dirs := r.runDirsOrNone(); len(dirs) != 1 {
		t.Fatalf("runs = %v, want one", dirs)
	}
	r.assertNoSecret(t, []string{token})
	if strings.Contains(p.stderr.String(), token) {
		t.Errorf("stderr holds the token:\n%s", p.stderr)
	}
	for _, line := range strings.Split(p.stderr.String(), "\n") {
		if strings.Contains(line, "api_request") && (strings.Contains(line, "order_id") || strings.Contains(line, "/v1/")) {
			t.Errorf("the audit log shows arguments: %s", line)
		}
	}
	if !strings.Contains(p.stderr.String(), "api_request ") {
		t.Errorf("no audit line for api_request:\n%s", p.stderr)
	}
}

func TestMCPAPIProductionAllowsReadsAndRefusesWrites(t *testing.T) {
	var nonGet, gets atomic.Int64
	var echoed atomic.Value
	r := newRepo(t, repoOptions{wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodGet {
				gets.Add(1)
				echoed.Store(req.Header.Get("Authorization"))
			} else {
				nonGet.Add(1)
			}
			next.ServeHTTP(w, req)
		})
	}})
	r.rewriteConfig(t, func(s string) string { return strings.Replace(s, "kind: test", "kind: production", 1) })
	p := startAPIMCP(t, r)

	rp, text, isErr := apiCall(t, p, map[string]any{"service": "orders", "method": "GET", "path": "/v1/orders", "as": "support-agent"})
	if isErr || !rp.Sent || rp.Status != 200 || gets.Load() != 1 {
		t.Fatalf("a GET in production: %v %s", isErr, text)
	}
	if got, _ := echoed.Load().(string); got != "Bearer "+token {
		t.Errorf("the server got Authorization %q", got)
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		_, text, isErr := apiCall(t, p, map[string]any{"service": "orders", "method": method, "path": "/v1/orders", "json": `{"amount": 5}`, "why": "try a write"})
		if !isErr || !strings.Contains(text, "only observes") {
			t.Errorf("%s in production: %v %s", method, isErr, text)
		}
	}
	if nonGet.Load() != 0 {
		t.Fatalf("%d writes reached the server", nonGet.Load())
	}
	if strings.Contains(text, token) {
		t.Error("a result shows the token")
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
	r.assertNoSecret(t, []string{token})
}

func TestMCPAPIWithNoServiceIsLeftOut(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.rewriteConfig(t, func(s string) string {
		from, to := strings.Index(s, "    services:"), strings.Index(s, "authProfiles:")
		return s[:from] + "    services: []\n" + s[to:]
	})
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--surfaces", "api")
	p.initialize()
	for _, n := range p.toolNames() {
		if strings.HasPrefix(n, "api_") {
			t.Errorf("%s is listed without a service", n)
		}
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
	if !strings.Contains(p.stderr.String(), "the api tools are left out") {
		t.Errorf("stderr does not say why:\n%s", p.stderr)
	}
}

func TestMCPStartLineNamesOnlyTheGroupsThatAreServed(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.rewriteConfig(t, func(s string) string {
		from, to := strings.Index(s, "    services:"), strings.Index(s, "authProfiles:")
		return s[:from] + "    services: []\n" + s[to:]
	})
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--surfaces", "mobile,api")
	p.initialize()
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
	out := p.stderr.String()
	if !strings.Contains(out, "tool groups mobile") {
		t.Errorf("the start line does not name the mobile group:\n%s", out)
	}
	if strings.Contains(out, "tool groups mobile,api") {
		t.Errorf("the start line names a group that was left out:\n%s", out)
	}
}
