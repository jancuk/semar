package api_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
)

var key = []byte("0123456789abcdef0123456789abcdef")

const (
	traceID = "0af7651916cd43dd8448eb211c80319c"
	spanID  = "b7ad6b7169203331"
)

var step = domain.UseCase{Pass: domain.PassScenario, Scenario: "sc-1", Step: "s-1"}

type fakeSink struct {
	mu      sync.Mutex
	actions []domain.Action
	err     error
}

func (s *fakeSink) WriteAction(a domain.Action) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	s.actions = append(s.actions, a)
	return "actions/" + string(a.ID) + ".json", nil
}

func (s *fakeSink) AppendActivity(domain.Activity) error { return nil }

func (s *fakeSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.actions)
}

func (s *fakeSink) last(t *testing.T) domain.Action {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.actions) == 0 {
		t.Fatal("no action was recorded")
	}
	return s.actions[len(s.actions)-1]
}

// server counts every request that reaches it.
type server struct {
	*httptest.Server
	hits atomic.Int64
}

func newServer(t *testing.T, h http.HandlerFunc) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		h(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) host(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

type setup struct {
	mode    string
	secrets []string
	options func(*api.Options)
}

func newEngine(t *testing.T, srv *server, st setup) (*api.Engine, *fakeSink) {
	t.Helper()
	mode := st.mode
	if mode == "" {
		mode = "test_data"
	}
	pol, err := effects.New(config.Environment{
		Hosts:   []string{srv.host(t)},
		Methods: []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"},
		Effects: config.Effects{Mode: mode},
	})
	if err != nil {
		t.Fatal(err)
	}
	red, err := redact.New(config.Redact{}, func() []string { return st.secrets }, key)
	if err != nil {
		t.Fatal(err)
	}
	sink := &fakeSink{}
	classify := func(method string, u *url.URL) effects.Class {
		return pol.Classify(effects.Request{Method: method, URL: u})
	}
	o := api.Options{
		Policy:   pol,
		Recorder: recorder.New(sink, red, classify, "r-1"),
		Services: map[string]string{"orders": srv.URL},
		Session: func(context.Context, string) (string, string, error) {
			return "Authorization", "Bearer tok", nil
		},
		Run:     "r-1",
		Version: "test",
		TraceID: func() string { return traceID },
		SpanID:  func() string { return spanID },
	}
	if st.options != nil {
		st.options(&o)
	}
	return api.New(o), sink
}

func get(path string) api.Call {
	return api.Call{Use: step, By: domain.ActorReplay, Service: "orders", Method: "GET", Path: path, PathTemplate: path}
}

func post(path, body string) api.Call {
	c := get(path)
	c.Method = "POST"
	c.JSON = []byte(body)
	return c
}

func do(t *testing.T, e *api.Engine, c api.Call) api.Result {
	t.Helper()
	res, err := e.Do(t.Context(), c)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return res
}

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

// hang waits until the client goes away. The server only notices that once the
// request body has been read.
func hang(r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	<-r.Context().Done()
}

func TestHeaders(t *testing.T) {
	var seen http.Header
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
	})
	e, sink := newEngine(t, srv, setup{})
	c := post("/v1/orders", `{"a":1}`)
	c.AuthProfile = "staff"
	c.Headers = map[string]string{"X-Semar-Run": "forged", "x-semar-run": "forged", "X-Extra": "kept"}
	res := do(t, e, c)

	want := map[string]string{
		"Traceparent":   "00-" + traceID + "-" + spanID + "-01",
		"Baggage":       "semar.run=r-1",
		"User-Agent":    "Semar/test",
		"X-Semar-Run":   "r-1",
		"Authorization": "Bearer tok",
		"Content-Type":  "application/json",
		"X-Extra":       "kept",
	}
	for name, v := range want {
		if got := seen.Values(name); len(got) != 1 || got[0] != v {
			t.Errorf("server saw %s = %q, want [%q]", name, got, v)
		}
	}
	stored := sink.last(t)
	if auth := stored.Request.Headers["Authorization"]; len(auth) != 1 || !redact.IsMarker(auth[0]) {
		t.Errorf("stored Authorization = %q, want one marker", auth)
	}
	if stored.TraceID != traceID || res.Action.TraceID != traceID {
		t.Errorf("trace id stored %q, returned %q, want %q", stored.TraceID, res.Action.TraceID, traceID)
	}
	if stored.Surface != domain.SurfaceAPI || stored.Use != step || stored.By != domain.ActorReplay {
		t.Errorf("stored surface %q use %+v by %q", stored.Surface, stored.Use, stored.By)
	}
}

func TestRefusalSendsNothing(t *testing.T) {
	srv := newServer(t, ok)
	e, sink := newEngine(t, srv, setup{mode: "read_only"})
	res := do(t, e, post("/v1/orders", `{}`))
	if !strings.Contains(res.Action.Refused, "read_only") {
		t.Errorf("Refused = %q, want it to name read_only", res.Action.Refused)
	}
	if res.Action.Response != nil {
		t.Errorf("Response = %+v, want nil", res.Action.Response)
	}
	if n := srv.hits.Load(); n != 0 {
		t.Errorf("server hits = %d, want 0", n)
	}
	if sink.count() != 1 {
		t.Errorf("recorded %d actions, want 1", sink.count())
	}
}

func TestWriteNeedsAUseCase(t *testing.T) {
	cases := []struct {
		name        string
		call        api.Call
		wantRefused string
		wantHits    int64
	}{
		{"write", post("/v1/orders", `{}`), "a write must belong to a scenario step, an exploration, or a probe", 0},
		{"read", get("/v1/orders"), "", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, ok)
			e, sink := newEngine(t, srv, setup{})
			tc.call.Use = domain.UseCase{}
			res := do(t, e, tc.call)
			if !strings.Contains(res.Action.Refused, tc.wantRefused) || (tc.wantRefused == "") != (res.Action.Refused == "") {
				t.Errorf("Refused = %q, want %q", res.Action.Refused, tc.wantRefused)
			}
			if n := srv.hits.Load(); n != tc.wantHits {
				t.Errorf("server hits = %d, want %d", n, tc.wantHits)
			}
			if sink.count() != 1 {
				t.Errorf("recorded %d actions, want 1", sink.count())
			}
		})
	}
}

func TestRedirects(t *testing.T) {
	other := newServer(t, ok)
	cases := []struct {
		name        string
		budget      int
		target      func(self *server) string
		wantHits    int64
		wantRefused string
		wantErr     string
	}{
		{
			name:        "to a host that is not on the list",
			target:      func(*server) string { return other.URL + "/elsewhere" },
			wantHits:    1,
			wantRefused: "redirect to a host that is not allowed",
		},
		{
			name:        "past the budget",
			budget:      1,
			target:      func(self *server) string { return self.URL + "/again" },
			wantHits:    1,
			wantRefused: "request budget reached",
		},
		{
			name:     "more than five",
			target:   func(self *server) string { return self.URL + "/again" },
			wantHits: 6,
			wantErr:  "stopped after 5 redirects",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := other.hits.Load()
			var srv *server
			srv = newServer(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.target(srv), http.StatusFound)
			})
			e, sink := newEngine(t, srv, setup{options: func(o *api.Options) { o.Budget = tc.budget }})
			res := do(t, e, get("/v1/orders"))
			if n := srv.hits.Load(); n != tc.wantHits {
				t.Errorf("server hits = %d, want %d", n, tc.wantHits)
			}
			if n := other.hits.Load() - before; n != 0 {
				t.Errorf("the other server got %d requests, want 0", n)
			}
			if tc.wantRefused == "" && res.Action.Refused != "" || !strings.Contains(res.Action.Refused, tc.wantRefused) {
				t.Errorf("Refused = %q, want %q", res.Action.Refused, tc.wantRefused)
			}
			if tc.wantErr == "" && res.Action.Err != "" || !strings.Contains(res.Action.Err, tc.wantErr) {
				t.Errorf("Err = %q, want %q", res.Action.Err, tc.wantErr)
			}
			if res.Action.Response == nil || res.Action.Response.Status != http.StatusFound {
				t.Errorf("Response = %+v, want the 302", res.Action.Response)
			}
			if sink.count() != 1 {
				t.Errorf("recorded %d actions, want 1", sink.count())
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	cases := []struct {
		name              string
		call              api.Call
		wantEffectUnknown bool
	}{
		{"write", post("/v1/orders", `{}`), true},
		{"read", get("/v1/orders"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, func(w http.ResponseWriter, r *http.Request) { hang(r) })
			e, sink := newEngine(t, srv, setup{options: func(o *api.Options) { o.Timeout = 50 * time.Millisecond }})
			res := do(t, e, tc.call)
			if res.Action.EffectUnknown != tc.wantEffectUnknown {
				t.Errorf("EffectUnknown = %v, want %v", res.Action.EffectUnknown, tc.wantEffectUnknown)
			}
			if res.Action.Err == "" {
				t.Error("Err is empty, want the timeout")
			}
			if n := srv.hits.Load(); n != 1 {
				t.Errorf("server hits = %d, want exactly 1", n)
			}
			if sink.count() != 1 {
				t.Errorf("recorded %d actions, want 1", sink.count())
			}
		})
	}
}

func TestLargeBodyIsCut(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"`+strings.Repeat("x", 92)+`"}`)
	})
	e, _ := newEngine(t, srv, setup{options: func(o *api.Options) { o.MaxBody = 16 }})
	c := get("/v1/orders")
	c.Captures = []domain.Capture{{Name: "id", JSONPath: "$.id"}}
	res := do(t, e, c)
	resp := res.Action.Response
	if resp == nil || !resp.Truncated || len(resp.Body) != 16 {
		t.Fatalf("Response = %+v, want Truncated and 16 bytes", resp)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "id" || len(res.Captured) != 0 {
		t.Errorf("Captured %v Missing %v, want id missing on a cut body", res.Captured, res.Missing)
	}
}

func TestBudget(t *testing.T) {
	srv := newServer(t, ok)
	e, sink := newEngine(t, srv, setup{options: func(o *api.Options) { o.Budget = 2 }})
	for range 2 {
		if res := do(t, e, get("/v1/orders")); res.Action.Refused != "" {
			t.Fatalf("Refused = %q within the budget", res.Action.Refused)
		}
	}
	res := do(t, e, get("/v1/orders"))
	if res.Action.Refused != "request budget reached" {
		t.Errorf("Refused = %q, want %q", res.Action.Refused, "request budget reached")
	}
	if n := srv.hits.Load(); n != 2 {
		t.Errorf("server hits = %d, want 2", n)
	}
	if sink.count() != 3 {
		t.Errorf("recorded %d actions, want 3", sink.count())
	}
}

func TestBudgetUnderConcurrency(t *testing.T) {
	srv := newServer(t, ok)
	e, sink := newEngine(t, srv, setup{options: func(o *api.Options) { o.Budget = 10 }})
	var wg sync.WaitGroup
	var refused atomic.Int64
	for range 20 {
		wg.Go(func() {
			res, err := e.Do(t.Context(), get("/v1/orders"))
			if err != nil {
				t.Error(err)
				return
			}
			if res.Action.Refused == "request budget reached" {
				refused.Add(1)
			}
		})
	}
	wg.Wait()
	if n := srv.hits.Load(); n != 10 {
		t.Errorf("server hits = %d, want 10", n)
	}
	if refused.Load() != 10 || sink.count() != 20 {
		t.Errorf("refused %d, recorded %d, want 10 and 20", refused.Load(), sink.count())
	}
}

func TestCancelledContext(t *testing.T) {
	srv := newServer(t, ok)
	e, sink := newEngine(t, srv, setup{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := e.Do(ctx, get("/v1/orders"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if n := srv.hits.Load(); n != 0 {
		t.Errorf("server hits = %d, want 0", n)
	}
	if sink.count() != 0 {
		t.Errorf("recorded %d actions, want 0", sink.count())
	}
}

func TestCancelledInFlight(t *testing.T) {
	arrived := make(chan struct{})
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		hang(r)
	})
	e, sink := newEngine(t, srv, setup{})
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-arrived
		cancel()
	}()
	res, err := e.Do(ctx, post("/v1/orders", `{}`))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if !res.Action.EffectUnknown || res.Action.Err == "" {
		t.Errorf("EffectUnknown %v Err %q, want true and the error", res.Action.EffectUnknown, res.Action.Err)
	}
	if sink.count() != 1 {
		t.Errorf("recorded %d actions, want 1", sink.count())
	}
}

func TestCaptures(t *testing.T) {
	const secret = "tok-0a1b2c3d4e"
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"data":{"id":"o-1","n":12.50,"ok":true,"token":%q,"tags":["a","b"],"obj":{},"nil":null}}`, secret)
	})
	e, sink := newEngine(t, srv, setup{secrets: []string{secret}})
	c := post("/v1/orders", `{}`)
	c.Captures = []domain.Capture{
		{Name: "order_id", JSONPath: "$.data.id"},
		{Name: "amount", JSONPath: "$.data.n"},
		{Name: "ok", JSONPath: "$.data.ok"},
		{Name: "token", JSONPath: "$.data.token"},
		{Name: "absent", JSONPath: "$.data.nope"},
		{Name: "many", JSONPath: "$.data.tags[*]"},
		{Name: "object", JSONPath: "$.data.obj"},
		{Name: "null", JSONPath: "$.data.nil"},
	}
	res := do(t, e, c)

	wantCaptured := map[string]string{"order_id": "o-1", "amount": "12.50", "ok": "true", "token": secret}
	if fmt.Sprint(res.Captured) != fmt.Sprint(wantCaptured) {
		t.Errorf("Captured = %v, want %v", res.Captured, wantCaptured)
	}
	if want := "[absent many object null]"; fmt.Sprint(res.Missing) != want {
		t.Errorf("Missing = %v, want %v", res.Missing, want)
	}
	stored := sink.last(t)
	if stored.Captures["order_id"] != "o-1" {
		t.Errorf("stored order_id = %q, want o-1", stored.Captures["order_id"])
	}
	if !redact.IsMarker(stored.Captures["token"]) {
		t.Errorf("stored token = %q, want a marker", stored.Captures["token"])
	}
	if _, ok := stored.Captures["absent"]; ok {
		t.Error("a missing capture was stored")
	}
}

func TestURL(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		path   string
		query  map[string]string
		want   string
	}{
		{"no prefix", "", "/v1/orders", nil, "/v1/orders"},
		{"path prefix", "/api", "/v1/orders", nil, "/api/v1/orders"},
		{"prefix with slash", "/api/", "/v1/orders", nil, "/api/v1/orders"},
		{"query", "", "/v1/orders", map[string]string{"b": "2", "a": "x y"}, "/v1/orders?a=x+y&b=2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			srv := newServer(t, func(w http.ResponseWriter, r *http.Request) { seen = r.URL.RequestURI() })
			e, sink := newEngine(t, srv, setup{options: func(o *api.Options) {
				o.Services = map[string]string{"orders": srv.URL + tc.prefix}
			}})
			c := get(tc.path)
			c.PathTemplate = "/v1/{kind}"
			c.Query = tc.query
			do(t, e, c)
			if seen != tc.want {
				t.Errorf("server saw %q, want %q", seen, tc.want)
			}
			stored := sink.last(t)
			if stored.Request.URL != srv.URL+tc.want || stored.Request.PathTemplate != "/v1/{kind}" {
				t.Errorf("stored URL %q template %q", stored.Request.URL, stored.Request.PathTemplate)
			}
		})
	}
}

func TestSignInFailure(t *testing.T) {
	srv := newServer(t, ok)
	e, sink := newEngine(t, srv, setup{options: func(o *api.Options) {
		o.Session = func(context.Context, string) (string, string, error) { return "", "", errors.New("otp expired") }
	}})
	c := get("/v1/orders")
	c.AuthProfile = "staff"
	res := do(t, e, c)
	if res.Action.Err != "sign-in failed: otp expired" {
		t.Errorf("Err = %q", res.Action.Err)
	}
	if n := srv.hits.Load(); n != 0 {
		t.Errorf("server hits = %d, want 0", n)
	}
	if sink.count() != 1 {
		t.Errorf("recorded %d actions, want 1", sink.count())
	}
}

func TestUnknownService(t *testing.T) {
	srv := newServer(t, ok)
	e, sink := newEngine(t, srv, setup{})
	c := get("/v1/orders")
	c.Service = "billing"
	if _, err := e.Do(t.Context(), c); err == nil {
		t.Error("err = nil, want an unknown service error")
	}
	if srv.hits.Load() != 0 || sink.count() != 0 {
		t.Errorf("hits %d, recorded %d, want 0 and 0", srv.hits.Load(), sink.count())
	}
}

func TestRecorderFailure(t *testing.T) {
	boom := errors.New("disk full")
	srv := newServer(t, ok)
	e, sink := newEngine(t, srv, setup{})
	sink.err = boom
	if _, err := e.Do(t.Context(), get("/v1/orders")); !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestDefaultIDs(t *testing.T) {
	var seen string
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) { seen = r.Header.Get("traceparent") })
	e, _ := newEngine(t, srv, setup{options: func(o *api.Options) { o.TraceID, o.SpanID = nil, nil }})
	res := do(t, e, get("/v1/orders"))
	if !regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`).MatchString(seen) {
		t.Errorf("traceparent = %q", seen)
	}
	if !strings.HasPrefix(seen, "00-"+res.Action.TraceID+"-") {
		t.Errorf("TraceID %q is not the one sent in %q", res.Action.TraceID, seen)
	}
}
