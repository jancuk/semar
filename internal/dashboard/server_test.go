package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/userconfig"
)

// fakeInstance is a provider without an account.
type fakeInstance struct {
	cfg    config.Provider
	health provider.Health
	delay  time.Duration
}

func (f *fakeInstance) ID() string   { return f.cfg.ID }
func (f *fakeInstance) Kind() string { return f.cfg.Kind }
func (f *fakeInstance) Health(ctx context.Context) provider.Health {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.health
}
func (f *fakeInstance) Run(context.Context, provider.Job) (provider.Result, error) {
	return provider.Result{}, &provider.Error{Class: provider.Unknown, Detail: "not in a test"}
}

// fakeAccount is a provider with an account and, for a harness, a sign-in command.
type fakeAccount struct {
	fakeInstance
	status provider.AccountStatus
	login  []string
}

func (f *fakeAccount) Status(context.Context) provider.AccountStatus { return f.status }
func (f *fakeAccount) LoginCommand() []string                        { return f.login }

type fixture struct {
	t       *testing.T
	s       *Server
	ts      *httptest.Server
	home    string
	keys    keychain.Store
	mu      sync.Mutex
	env     map[string]string
	environ []string
	insts   map[string]func(config.Provider) (provider.Instance, error)
}

// newFixture makes a server; each tune runs before it starts to answer.
func newFixture(t *testing.T, tune ...func(*Server)) *fixture {
	t.Helper()
	fx := &fixture{t: t, home: filepath.Join(t.TempDir(), "semar"), keys: keychain.Memory(), env: map[string]string{},
		insts: map[string]func(config.Provider) (provider.Instance, error){}}
	s, err := New(Deps{
		Home:        fx.home,
		NewInstance: fx.newInstance,
		Keys:        fx.keys,
		LookupEnv: func(name string) (string, bool) {
			fx.mu.Lock()
			defer fx.mu.Unlock()
			v, ok := fx.env[name]
			return v, ok
		},
		Environ: func() []string {
			fx.mu.Lock()
			defer fx.mu.Unlock()
			return append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + fx.home}, fx.environ...)
		},
		Now: func() time.Time { return time.Date(2026, 11, 14, 15, 0, 0, 0, time.Local) },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range tune {
		f(s)
	}
	fx.s = s
	fx.ts = httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		fx.ts.Close()
		s.logins.stop()
	})
	return fx
}

// instance makes the provider with the ID answer as inst does.
func (fx *fixture) instance(id string, inst func(config.Provider) (provider.Instance, error)) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.insts[id] = inst
}

func (fx *fixture) newInstance(cfg config.Provider) (provider.Instance, error) {
	fx.mu.Lock()
	f := fx.insts[cfg.ID]
	fx.mu.Unlock()
	if f != nil {
		return f(cfg)
	}
	if provider.IsAPI(cfg.Kind) {
		return &fakeAccount{fakeInstance: fakeInstance{cfg: cfg, health: provider.Health{Installed: true}}, status: provider.AccountStatus{Method: "api key"}}, nil
	}
	return &fakeAccount{
		fakeInstance: fakeInstance{cfg: cfg, health: provider.Health{Installed: true, Version: "1.0.0"}},
		status:       provider.AccountStatus{SignedIn: true, Method: "subscription", Plan: "max", Detail: "Signed in."},
		login:        []string{"/bin/true"},
	}, nil
}

func (fx *fixture) setEnv(name, value string) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.env[name] = value
	fx.environ = append(fx.environ, name+"="+value)
}

func (fx *fixture) host() string {
	u, _ := url.Parse(fx.ts.URL)
	return u.Host
}

type answer struct {
	code   int
	body   string
	header http.Header
}

func (a answer) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(a.body), v); err != nil {
		t.Fatalf("decode %q: %v", a.body, err)
	}
}

// call sends a request with the token and a JSON content type unless edit changes them.
func (fx *fixture) call(method, path, body string, edit ...func(*http.Request)) answer {
	fx.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, fx.ts.URL+path, rd)
	if err != nil {
		fx.t.Fatal(err)
	}
	req.Header.Set(TokenHeader, fx.s.Token())
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, e := range edit {
		e(req)
	}
	res, err := fx.ts.Client().Do(req)
	if err != nil {
		fx.t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		fx.t.Fatal(err)
	}
	return answer{code: res.StatusCode, body: string(data), header: res.Header}
}

func (fx *fixture) state() stateBody {
	fx.t.Helper()
	a := fx.call("GET", "/api/state", "")
	if a.code != http.StatusOK {
		fx.t.Fatalf("state: %d %s", a.code, a.body)
	}
	var st stateBody
	a.json(fx.t, &st)
	return st
}

func (fx *fixture) writeFile(content string) {
	fx.t.Helper()
	if err := os.MkdirAll(fx.home, 0o700); err != nil {
		fx.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.home, userconfig.FileName), []byte(content), 0o600); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *fixture) readFile() string {
	fx.t.Helper()
	data, err := os.ReadFile(filepath.Join(fx.home, userconfig.FileName))
	if err != nil {
		fx.t.Fatal(err)
	}
	return string(data)
}

func want(t *testing.T, a answer, code int) {
	t.Helper()
	if a.code != code {
		t.Fatalf("status %d, want %d: %s", a.code, code, a.body)
	}
}

func withHeader(name, value string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(name, value) }
}

func withoutHeader(name string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Del(name) }
}

func withHost(host string) func(*http.Request) {
	return func(r *http.Request) { r.Host = host }
}

const twoProviders = `version: 1
default: claude
providers:
  - id: claude
    kind: claude
  - id: deepseek
    kind: openai
    baseUrl: https://api.deepseek.com
    model: deepseek-chat
    apiKey: env:DEEPSEEK_API_KEY
`

func TestRefusals(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	_, port, _ := strings.Cut(fx.host(), ":")
	big := `{"id":"x","kind":"claude","model":"` + strings.Repeat("a", maxBody) + `"}`
	cases := []struct {
		name         string
		method, path string
		body         string
		edit         []func(*http.Request)
		code         int
	}{
		{"no token", "GET", "/api/state", "", []func(*http.Request){withoutHeader(TokenHeader)}, 403},
		{"wrong token", "GET", "/api/state", "", []func(*http.Request){withHeader(TokenHeader, strings.Repeat("0", 64))}, 403},
		{"token of another length", "GET", "/api/state", "", []func(*http.Request){withHeader(TokenHeader, "abc")}, 403},
		{"foreign host on the API", "GET", "/api/state", "", []func(*http.Request){withHost("orders.example.com:" + port)}, 403},
		{"foreign host on the page", "GET", "/", "", []func(*http.Request){withHost("orders.example.com:" + port)}, 403},
		{"another port", "GET", "/api/state", "", []func(*http.Request){withHost("127.0.0.1:1")}, 403},
		{"no port", "GET", "/", "", []func(*http.Request){withHost("localhost")}, 403},
		{"foreign origin", "GET", "/api/state", "", []func(*http.Request){withHeader("Origin", "https://orders.example.com")}, 403},
		{"origin of another port", "PUT", "/api/default", `{"id":"claude"}`, []func(*http.Request){withHeader("Origin", "http://127.0.0.1:1")}, 403},
		{"origin null", "GET", "/api/state", "", []func(*http.Request){withHeader("Origin", "null")}, 403},
		{"text content type", "PUT", "/api/default", `{"id":"claude"}`, []func(*http.Request){withHeader("Content-Type", "text/plain")}, 415},
		{"form content type", "POST", "/api/providers/claude/login", "", []func(*http.Request){withHeader("Content-Type", "application/x-www-form-urlencoded")}, 415},
		{"no content type", "DELETE", "/api/providers/deepseek", "", []func(*http.Request){withoutHeader("Content-Type")}, 415},
		{"oversized body", "PUT", "/api/providers/x", big, nil, 413},
		{"unknown field", "PUT", "/api/default", `{"id":"claude","extra":1}`, nil, 400},
		{"two values", "PUT", "/api/default", `{"id":"claude"}{"id":"claude"}`, nil, 400},
		{"no body", "PUT", "/api/default", "", nil, 400},
		{"not JSON", "PUT", "/api/default", `id=claude`, nil, 400},
		{"wrong method on state", "POST", "/api/state", `{}`, nil, 405},
		{"head on state", "HEAD", "/api/state", "", nil, 405},
		{"wrong method on default", "DELETE", "/api/default", "", nil, 405},
		{"wrong method on a provider", "POST", "/api/providers/claude", `{}`, nil, 405},
		{"wrong method on the key", "GET", "/api/providers/deepseek/key", "", nil, 405},
		{"wrong method on usage", "PUT", "/api/usage", `{}`, nil, 405},
		{"wrong method on the page", "POST", "/", "", nil, 405},
		{"unknown API path", "GET", "/api/secrets", "", nil, 404},
		{"unknown page path", "GET", "/index.html", "", nil, 404},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := fx.call(c.method, c.path, c.body, c.edit...)
			want(t, a, c.code)
			if c.method != "HEAD" && (strings.HasPrefix(c.path, "/api/") || c.code != 404) {
				var e errorBody
				if a.json(t, &e); e.Error == "" {
					t.Fatalf("no error text: %s", a.body)
				}
			}
			if a.header.Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("a CORS header was sent")
			}
		})
	}
	if got := fx.readFile(); got != twoProviders {
		t.Fatalf("a refused request changed the file:\n%s", got)
	}
}

func TestAllowedHostsAndOrigin(t *testing.T) {
	fx := newFixture(t)
	_, port, _ := strings.Cut(fx.host(), ":")
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port} {
		want(t, fx.call("GET", "/api/state", "", withHost(host), withHeader("Origin", "http://"+host)), 200)
		want(t, fx.call("GET", "/", "", withHost(host), withoutHeader(TokenHeader)), 200)
	}
	want(t, fx.call("GET", "/api/state", "", withHost("localhost:"+port), withHeader("Origin", "http://127.0.0.1:"+port)), 403)
}

func TestHeaders(t *testing.T) {
	fx := newFixture(t)
	wantHeaders := map[string]string{
		"Cache-Control":           "no-store",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
	}
	answers := map[string]answer{
		"page":      fx.call("GET", "/", "", withoutHeader(TokenHeader)),
		"script":    fx.call("GET", "/app.js", "", withoutHeader(TokenHeader)),
		"style":     fx.call("GET", "/style.css", "", withoutHeader(TokenHeader)),
		"state":     fx.call("GET", "/api/state", ""),
		"refused":   fx.call("GET", "/api/state", "", withoutHeader(TokenHeader)),
		"not found": fx.call("GET", "/nothing", ""),
	}
	for name, a := range answers {
		for k, v := range wantHeaders {
			if got := a.header.Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", name, k, got, v)
			}
		}
		for k := range a.header {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s: CORS header %s", name, k)
			}
		}
	}
	for name, ct := range map[string]string{"page": "text/html", "script": "text/javascript", "style": "text/css", "state": "application/json"} {
		if got := answers[name].header.Get("Content-Type"); !strings.HasPrefix(got, ct) {
			t.Errorf("%s: Content-Type %q", name, got)
		}
	}
}

func TestStateWithoutFile(t *testing.T) {
	fx := newFixture(t)
	st := fx.state()
	if st.Exists || st.FileError != "" || st.Default != "claude" || len(st.Providers) != 1 {
		t.Fatalf("state %+v", st)
	}
	if st.Home != fx.home || st.File != filepath.Join(fx.home, "providers.yaml") {
		t.Fatalf("paths %q %q", st.Home, st.File)
	}
	if len(st.Kinds) != 4 || st.Kinds[0].Label != "Claude Code" || !st.Kinds[1].Harness || st.Kinds[3].Harness {
		t.Fatalf("kinds %+v", st.Kinds)
	}
	p := st.Providers[0]
	if p.ID != "claude" || p.Setup == nil || !*p.Setup.MCP || !*p.Setup.Skills {
		t.Fatalf("provider %+v", p)
	}
	if p.Health == nil || !p.Health.Installed || p.Account == nil || p.Account.Plan != "max" || len(p.LoginCommand) == 0 || p.KeyReady != nil {
		t.Fatalf("checks %+v", p)
	}
	if _, err := os.Stat(fx.home); err == nil {
		t.Fatal("a state call created the home directory")
	}
}

func TestHandEditShowsOnNextState(t *testing.T) {
	fx := newFixture(t)
	if st := fx.state(); len(st.Providers) != 1 {
		t.Fatalf("providers %+v", st.Providers)
	}
	fx.writeFile(twoProviders)
	st := fx.state()
	if !st.Exists || len(st.Providers) != 2 || st.Providers[1].ID != "deepseek" || st.Providers[1].BaseURL != "https://api.deepseek.com" {
		t.Fatalf("state %+v", st)
	}
	if p := st.Providers[1]; p.APIKey != "env:DEEPSEEK_API_KEY" || p.KeyReady == nil || *p.KeyReady || p.Setup != nil {
		t.Fatalf("deepseek %+v", p)
	}
	fx.setEnv("DEEPSEEK_API_KEY", "dk-0123456789")
	st = fx.state()
	if !*st.Providers[1].KeyReady {
		t.Fatal("the key does not resolve")
	}
	if a := fx.call("GET", "/api/state", ""); strings.Contains(a.body, "dk-0123456789") {
		t.Fatal("the state holds the key")
	}
}

func TestInvalidFileRefusesChanges(t *testing.T) {
	fx := newFixture(t)
	bad := "version: 1\ndefault: claude\nproviders:\n  - id: claude\n    kind: claude\n  - id: x\n    kind: openai\n    apiKey: sk-pasted-by-mistake-0123456789\n"
	fx.writeFile(bad)
	st := fx.state()
	if st.FileError == "" || !st.Exists || len(st.Providers) != 1 || st.Providers[0].ID != "claude" {
		t.Fatalf("state %+v", st)
	}
	a := fx.call("GET", "/api/state", "")
	if strings.Contains(a.body, "sk-pasted") {
		t.Fatalf("the state quotes the key: %s", a.body)
	}
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/api/providers/codex", `{"id":"codex","kind":"codex"}`},
		{"DELETE", "/api/providers/x", ""},
		{"PUT", "/api/default", `{"id":"claude"}`},
		{"PUT", "/api/providers/x/key", `{"value":"sk-new"}`},
		{"POST", "/api/providers/claude/login", ""},
	} {
		want(t, fx.call(c.method, c.path, c.body), http.StatusConflict)
	}
	if fx.readFile() != bad {
		t.Fatal("the file was changed")
	}
	if _, err := fx.keys.Get("x"); err == nil {
		t.Fatal("a key was stored")
	}
}

func TestStateChecks(t *testing.T) {
	fx := newFixture(t, func(s *Server) { s.checkTimeout = 300 * time.Millisecond })
	fx.writeFile(`version: 1
default: claude
providers:
  - id: claude
    kind: claude
  - id: codex
    kind: codex
    setup: {mcp: false, skills: true, deny: [Bash]}
  - id: slow
    kind: claude
  - id: broken
    kind: claude
  - id: anthropic
    kind: anthropic
    model: claude-opus-5-5
    apiKey: keychain:anthropic
  - id: local
    kind: openai
    baseUrl: http://localhost:8080/v1
    model: orders-model
`)
	fx.instance("claude", func(cfg config.Provider) (provider.Instance, error) {
		return &fakeAccount{
			fakeInstance: fakeInstance{cfg: cfg, health: provider.Health{Installed: true, Version: "2.0"}},
			status:       provider.AccountStatus{SignedIn: true, Plan: "max", Detail: "Signed in as person@example.com with token=abc123."},
			login:        []string{"/usr/bin/claude", "auth", "login"},
		}, nil
	})
	fx.instance("codex", func(cfg config.Provider) (provider.Instance, error) {
		return &fakeInstance{cfg: cfg, health: provider.Health{Installed: false, Detail: "codex is not on PATH"}}, nil
	})
	fx.instance("slow", func(cfg config.Provider) (provider.Instance, error) {
		return &fakeInstance{cfg: cfg, delay: 3 * time.Second, health: provider.Health{Installed: true}}, nil
	})
	fx.instance("broken", func(cfg config.Provider) (provider.Instance, error) {
		return nil, &provider.Error{Class: provider.ValidationError, Detail: "it cannot start"}
	})
	if err := fx.keys.Set("anthropic", "sk-ant-secret-value"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	a := fx.call("GET", "/api/state", "")
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("a slow check held the page for %s", took)
	}
	want(t, a, 200)
	for _, private := range []string{"person@example.com", "abc123", "sk-ant-secret-value"} {
		if strings.Contains(a.body, private) {
			t.Fatalf("the state holds %q: %s", private, a.body)
		}
	}
	var st stateBody
	a.json(t, &st)
	byID := map[string]providerState{}
	for _, p := range st.Providers {
		byID[p.ID] = p
	}
	if p := byID["claude"]; p.Account == nil || !p.Account.SignedIn || strings.Join(p.LoginCommand, " ") != "/usr/bin/claude auth login" || p.Health.Version != "2.0" {
		t.Fatalf("claude %+v", p)
	}
	if p := byID["codex"]; p.Health == nil || p.Health.Installed || p.Account != nil || *p.Setup.MCP || !*p.Setup.Skills || p.Setup.Deny[0] != "Bash" {
		t.Fatalf("codex %+v", p)
	}
	if p := byID["slow"]; p.Health != nil || !strings.Contains(p.Error, "did not answer") {
		t.Fatalf("slow %+v", p)
	}
	if p := byID["broken"]; !strings.Contains(p.Error, "cannot be checked") {
		t.Fatalf("broken %+v", p)
	}
	if p := byID["anthropic"]; p.KeyReady == nil || !*p.KeyReady || p.APIKey != "keychain:anthropic" {
		t.Fatalf("anthropic %+v", p)
	}
	if p := byID["local"]; p.KeyReady == nil || !*p.KeyReady || p.APIKey != "" {
		t.Fatalf("local %+v", p)
	}
}

func TestPutProvider(t *testing.T) {
	fx := newFixture(t)
	a := fx.call("PUT", "/api/providers/codex", `{"id":"codex","kind":"codex","model":"","setup":{"mcp":false,"skills":true,"deny":[" Bash ",""]}}`)
	want(t, a, http.StatusCreated)
	f, exists, err := userconfig.Load(fx.home)
	if err != nil || !exists || len(f.Providers) != 2 || f.Default != "claude" {
		t.Fatalf("file %+v %v %v", f, exists, err)
	}
	if s := f.Providers[1].Setup; *s.MCP || !*s.Skills || len(s.Deny) != 1 || s.Deny[0] != "Bash" {
		t.Fatalf("setup %+v", s)
	}
	info, err := os.Stat(filepath.Join(fx.home, userconfig.FileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", info, err)
	}

	want(t, fx.call("PUT", "/api/providers/codex", `{"id":"codex","kind":"codex","model":"gpt-5-codex"}`), http.StatusOK)
	if f, _, _ := userconfig.Load(fx.home); f.Providers[1].Model != "gpt-5-codex" || len(f.Providers) != 2 {
		t.Fatalf("changed %+v", f.Providers)
	}
	want(t, fx.call("PUT", "/api/providers/codex", `{"id":"codex","kind":"codex"}`, withHeader("If-None-Match", "*")), http.StatusPreconditionFailed)
	want(t, fx.call("PUT", "/api/providers/openai", `{"id":"openai","kind":"openai","model":"orders-model","apiKey":"env:OPENAI_API_KEY"}`, withHeader("If-None-Match", "*")), http.StatusCreated)

	for name, c := range map[string]struct {
		path, body string
		code       int
	}{
		"ids disagree":     {"/api/providers/codex", `{"id":"other","kind":"codex"}`, 400},
		"invalid path id":  {"/api/providers/.hidden", `{"id":".hidden","kind":"codex"}`, 400},
		"fake kind":        {"/api/providers/f", `{"id":"f","kind":"fake"}`, 400},
		"unknown kind":     {"/api/providers/f", `{"id":"f","kind":"gemini"}`, 400},
		"setup on api":     {"/api/providers/f", `{"id":"f","kind":"openai","model":"m","apiKey":"env:K","setup":{"mcp":true}}`, 400},
		"http address":     {"/api/providers/f", `{"id":"f","kind":"openai","model":"m","baseUrl":"http://llm.example.com","apiKey":"env:K"}`, 400},
		"missing key":      {"/api/providers/f", `{"id":"f","kind":"openai","model":"m"}`, 400},
		"wrong field type": {"/api/providers/f", `{"id":"f","kind":"openai","model":7}`, 400},
	} {
		t.Run(name, func(t *testing.T) { want(t, fx.call("PUT", c.path, c.body), c.code) })
	}

	pasted := "sk-live-0123456789abcdefghij"
	a = fx.call("PUT", "/api/providers/f", `{"id":"f","kind":"openai","model":"m","apiKey":"`+pasted+`"}`)
	want(t, a, 400)
	if strings.Contains(a.body, pasted) || strings.Contains(fx.readFile(), pasted) {
		t.Fatalf("the pasted key was echoed or saved: %s", a.body)
	}
}

func TestDeleteProvider(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(`version: 1
default: claude
providers:
  - id: claude
    kind: claude
  - id: deepseek
    kind: openai
    baseUrl: https://api.deepseek.com
    model: deepseek-chat
    apiKey: keychain:deepseek
  - id: one
    kind: openai
    model: m
    apiKey: keychain:shared
  - id: two
    kind: openai
    model: m
    apiKey: keychain:shared
`)
	for _, name := range []string{"deepseek", "shared"} {
		if err := fx.keys.Set(name, "value-"+name); err != nil {
			t.Fatal(err)
		}
	}
	want(t, fx.call("DELETE", "/api/providers/claude", ""), http.StatusConflict)
	want(t, fx.call("DELETE", "/api/providers/nobody", ""), http.StatusNotFound)
	want(t, fx.call("DELETE", "/api/providers/deepseek", ""), http.StatusOK)
	if _, err := fx.keys.Get("deepseek"); err == nil {
		t.Fatal("the keychain entry of the removed provider is still there")
	}
	want(t, fx.call("DELETE", "/api/providers/one", "{}"), http.StatusOK)
	if _, err := fx.keys.Get("shared"); err != nil {
		t.Fatal("an entry that another provider still uses was deleted")
	}
	want(t, fx.call("DELETE", "/api/providers/two", ""), http.StatusOK)
	if _, err := fx.keys.Get("shared"); err == nil {
		t.Fatal("the entry of the last provider that used it is still there")
	}
	st := fx.state()
	if len(st.Providers) != 1 || st.Providers[0].ID != "claude" {
		t.Fatalf("providers %+v", st.Providers)
	}
}

func TestPutDefault(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	want(t, fx.call("PUT", "/api/default", `{"id":"nobody"}`), http.StatusNotFound)
	want(t, fx.call("PUT", "/api/default", `{"id":"deepseek"}`), http.StatusOK)
	if st := fx.state(); st.Default != "deepseek" {
		t.Fatalf("default %q", st.Default)
	}
	want(t, fx.call("DELETE", "/api/providers/deepseek", ""), http.StatusConflict)
	want(t, fx.call("DELETE", "/api/providers/claude", ""), http.StatusOK)
}

func TestPutKey(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	secret := "sk-ds-SECRET-0123456789abcdef"
	want(t, fx.call("PUT", "/api/providers/deepseek/key", `{"value":""}`), 400)
	want(t, fx.call("PUT", "/api/providers/deepseek/key", `{"value":"   "}`), 400)
	want(t, fx.call("PUT", "/api/providers/claude/key", `{"value":"`+secret+`"}`), 400)
	want(t, fx.call("PUT", "/api/providers/nobody/key", `{"value":"`+secret+`"}`), 404)
	var bodies []string
	a := fx.call("PUT", "/api/providers/deepseek/key", `{"value":"`+secret+`\n"}`)
	want(t, a, 200)
	bodies = append(bodies, a.body)
	if got, err := fx.keys.Get("deepseek"); err != nil || got != secret {
		t.Fatalf("keychain %q %v", got, err)
	}
	st := fx.state()
	if p := st.Providers[1]; p.APIKey != "keychain:deepseek" || !*p.KeyReady {
		t.Fatalf("provider %+v", p)
	}
	bodies = append(bodies, fx.call("GET", "/api/state", "").body, fx.call("GET", "/api/usage", "").body, fx.readFile())
	for _, b := range bodies {
		if strings.Contains(b, secret) || strings.Contains(b, "SECRET") {
			t.Fatalf("the key appears in %s", b)
		}
	}
}

func TestEmbeddedPage(t *testing.T) {
	fx := newFixture(t)
	page := fx.call("GET", "/", "", withoutHeader(TokenHeader))
	want(t, page, 200)
	checkPage(t, page.body)
	for _, name := range []string{"/app.js", "/style.css"} {
		want(t, fx.call("GET", name, "", withoutHeader(TokenHeader)), 200)
	}
	script := fx.call("GET", "/app.js", "").body
	for _, banned := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "http://"} {
		if strings.Contains(script, banned) {
			t.Errorf("app.js uses %s", banned)
		}
	}
}

// lockWatch records whether the server held its lock at each change of the keychain.
type lockWatch struct {
	keychain.Store
	s      *Server
	mu     sync.Mutex
	unheld []string
}

func (l *lockWatch) note(op string) {
	if l.s.mu.TryLock() {
		l.s.mu.Unlock()
		l.mu.Lock()
		l.unheld = append(l.unheld, op)
		l.mu.Unlock()
	}
}

func (l *lockWatch) Set(name, value string) error {
	l.note("set " + name)
	return l.Store.Set(name, value)
}
func (l *lockWatch) Delete(name string) error { l.note("delete " + name); return l.Store.Delete(name) }

func TestKeychainChangesHoldTheLock(t *testing.T) {
	var watch *lockWatch
	fx := newFixture(t, func(s *Server) {
		watch = &lockWatch{Store: s.d.Keys, s: s}
		s.d.Keys = watch
	})
	fx.writeFile(twoProviders)
	want(t, fx.call("PUT", "/api/providers/deepseek/key", `{"value":"sk-ds-0123456789"}`), http.StatusOK)
	want(t, fx.call("DELETE", "/api/providers/deepseek", ""), http.StatusOK)
	if _, err := fx.keys.Get("deepseek"); err == nil {
		t.Fatal("the keychain entry of the removed provider is still there")
	}
	if len(watch.unheld) > 0 {
		t.Fatalf("the keychain was changed without the lock: %q", watch.unheld)
	}
}

func TestPutKeyRefusesAnEntryOfAnotherProvider(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders + `  - id: acme
    kind: anthropic
    apiKey: keychain:Deepseek
`)
	if err := fx.keys.Set("Deepseek", "sk-acme-0123456789"); err != nil {
		t.Fatal(err)
	}
	a := fx.call("PUT", "/api/providers/deepseek/key", `{"value":"sk-ds-0123456789"}`)
	want(t, a, http.StatusConflict)
	if !strings.Contains(a.body, "acme") || !strings.Contains(a.body, "keychain:Deepseek") {
		t.Errorf("answer %s does not name the provider and its reference", a.body)
	}
	if got, _ := fx.keys.Get("Deepseek"); got != "sk-acme-0123456789" {
		t.Fatal("the key of another provider was replaced")
	}
	if p := fx.state().Providers[1]; p.APIKey != "env:DEEPSEEK_API_KEY" {
		t.Fatalf("provider %+v", p)
	}
}

func TestStateShowsReasoningAndTheFileWithATilde(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile("version: 1\ndefault: claude\nproviders:\n  - id: claude\n    kind: claude\n    reasoning: high\n  - id: codex\n    kind: codex\n")
	fx.setEnv("HOME", filepath.Dir(fx.home))
	a := fx.call("GET", "/api/state", "")
	want(t, a, 200)
	var raw struct {
		File        string `json:"file"`
		FileDisplay string `json:"fileDisplay"`
		Providers   []map[string]any
	}
	a.json(t, &raw)
	if raw.File != filepath.Join(fx.home, "providers.yaml") || raw.FileDisplay != filepath.Join("~", "semar", "providers.yaml") {
		t.Fatalf("file %q, shown as %q", raw.File, raw.FileDisplay)
	}
	if raw.Providers[0]["reasoning"] != "high" || raw.Providers[1]["reasoning"] != "" {
		t.Fatalf("providers %v", raw.Providers)
	}

	fx.setEnv("HOME", "/srv/elsewhere")
	st := fx.state()
	if st.FileDisplay != st.File {
		t.Fatalf("a file outside the home directory is shown as %q", st.FileDisplay)
	}
}

func TestPutProviderTakesReasoning(t *testing.T) {
	fx := newFixture(t)
	want(t, fx.call("PUT", "/api/providers/codex", `{"id":"codex","kind":"codex","reasoning":" high "}`), http.StatusCreated)
	if f, _, _ := userconfig.Load(fx.home); f.Providers[1].Reasoning != "high" {
		t.Fatalf("saved %+v", f.Providers)
	}
	if !strings.Contains(fx.readFile(), "reasoning: high") {
		t.Fatalf("file:\n%s", fx.readFile())
	}
	a := fx.call("PUT", "/api/providers/codex", `{"id":"codex","kind":"codex","reasoning":"High"}`)
	want(t, a, 400)
	if !strings.Contains(a.body, "lower-case letters") {
		t.Fatalf("body = %s", a.body)
	}
	want(t, fx.call("PUT", "/api/providers/codex", `{"id":"codex","kind":"codex"}`), http.StatusOK)
	if strings.Contains(fx.readFile(), "reasoning") {
		t.Fatalf("the key stayed after it was cleared:\n%s", fx.readFile())
	}
}
