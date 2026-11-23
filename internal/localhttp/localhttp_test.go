package localhttp

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const tok = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type echo struct {
	Name string `json:"name"`
}

func handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/page", Allow(map[string]http.HandlerFunc{"GET": func(w http.ResponseWriter, r *http.Request) { Reply(w, 200, echo{"page"}) }}))
	api := http.NewServeMux()
	api.Handle("/api/echo", Allow(map[string]http.HandlerFunc{
		"GET": func(w http.ResponseWriter, r *http.Request) { Reply(w, 200, echo{"get"}) },
		"PUT": func(w http.ResponseWriter, r *http.Request) {
			var e echo
			if !Decode(w, r, &e, false, 64) {
				return
			}
			Reply(w, 200, e)
		},
	}))
	mux.Handle("/api/", GuardAPI("studio", "semar record", tok, 64, api))
	return GuardAll("studio", "", mux)
}

// serve starts a real listener so that the Host check sees the port.
func serve(t *testing.T) (base string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener: " + err.Error())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() { done <- Serve(ctx, ln, handler(t), func() { close(stopped) }) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
		select {
		case <-stopped:
		default:
			t.Error("the stop function did not run")
		}
	})
	return ln.Addr().String()
}

func call(t *testing.T, addr, method, path, body string, edit ...func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(TokenHeader, tok)
	if method != "GET" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, e := range edit {
		e(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestGuards(t *testing.T) {
	addr := serve(t)
	_, port, _ := net.SplitHostPort(addr)
	hdr := func(k, v string) func(*http.Request) { return func(r *http.Request) { r.Header.Set(k, v) } }
	cases := []struct {
		name         string
		method, path string
		body         string
		edit         []func(*http.Request)
		want         int
	}{
		{"get", "GET", "/api/echo", "", nil, 200},
		{"page needs no token", "GET", "/page", "", []func(*http.Request){func(r *http.Request) { r.Header.Del(TokenHeader) }}, 200},
		{"no token", "GET", "/api/echo", "", []func(*http.Request){func(r *http.Request) { r.Header.Del(TokenHeader) }}, 403},
		{"wrong token", "GET", "/api/echo", "", []func(*http.Request){hdr(TokenHeader, strings.Repeat("0", 64))}, 403},
		{"short token", "GET", "/api/echo", "", []func(*http.Request){hdr(TokenHeader, "abc")}, 403},
		{"localhost host", "GET", "/api/echo", "", []func(*http.Request){func(r *http.Request) { r.Host = "localhost:" + port }}, 200},
		{"other host", "GET", "/api/echo", "", []func(*http.Request){func(r *http.Request) { r.Host = "evil.example:" + port }}, 403},
		{"wrong port", "GET", "/api/echo", "", []func(*http.Request){func(r *http.Request) { r.Host = "127.0.0.1:1" }}, 403},
		{"same origin", "GET", "/api/echo", "", []func(*http.Request){hdr("Origin", "http://"+addr)}, 200},
		{"empty origin", "GET", "/api/echo", "", nil, 200},
		{"other origin", "GET", "/api/echo", "", []func(*http.Request){hdr("Origin", "http://evil.example")}, 403},
		{"change", "PUT", "/api/echo", `{"name":"x"}`, nil, 200},
		{"not json", "PUT", "/api/echo", `{"name":"x"}`, []func(*http.Request){hdr("Content-Type", "text/plain")}, 415},
		{"unknown field", "PUT", "/api/echo", `{"name":"x","more":1}`, nil, 400},
		{"two values", "PUT", "/api/echo", `{"name":"x"}{"name":"y"}`, nil, 400},
		{"no body", "PUT", "/api/echo", ``, nil, 400},
		{"too large", "PUT", "/api/echo", `{"name":"` + strings.Repeat("a", 100) + `"}`, nil, 413},
		{"wrong method", "DELETE", "/api/echo", "", nil, 405},
		{"head refused", "HEAD", "/api/echo", "", nil, 405},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := call(t, addr, c.method, c.path, c.body, c.edit...)
			if res.StatusCode != c.want {
				t.Fatalf("status = %d, want %d", res.StatusCode, c.want)
			}
			h := res.Header
			if h.Get("Content-Security-Policy") != CSP || h.Get("Cache-Control") != "no-store" || h.Get("X-Frame-Options") != "DENY" ||
				h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
				t.Errorf("security headers = %v", h)
			}
			if h.Get("Access-Control-Allow-Origin") != "" {
				t.Error("a CORS header was sent")
			}
		})
	}
}

func TestHostAllowedWithoutLocalAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "http://127.0.0.1:80/", nil)
	if HostAllowed(r) {
		t.Error("a request with no local address must be refused")
	}
}

func TestNewTokenIsRandomHex(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if len(a) != 64 || a == b {
		t.Errorf("tokens %q %q", a, b)
	}
}

func TestServeEndsWhenTheListenerFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	ln.Close()
	ran := make(chan struct{})
	err = Serve(context.Background(), ln, http.NotFoundHandler(), func() { close(ran) })
	if err == nil {
		t.Error("want the error of the closed listener")
	}
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Error("stop did not run")
	}
}
