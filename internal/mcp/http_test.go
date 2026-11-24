package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const httpTok = "0123456789abcdef0123456789abcdef"

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"c"}}}`

// httpReq builds a request that arrived on the given local address.
func httpReq(method, host, local, body string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(method, "http://"+host+"/mcp", strings.NewReader(body))
	r.Host = host
	r.Header.Set("Authorization", "Bearer "+httpTok)
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	a, _ := net.ResolveTCPAddr("tcp", local)
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, net.Addr(a)))
}

func doHTTP(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func newHTTP(t *testing.T) http.Handler {
	t.Helper()
	return NewHTTPHandler(newTestServer(t), HTTPOptions{Token: httpTok})
}

func initSession(t *testing.T, h http.Handler) string {
	t.Helper()
	w := doHTTP(h, httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", initBody, nil))
	if w.Code != 200 {
		t.Fatalf("initialize = %d %s", w.Code, w.Body)
	}
	id := w.Header().Get(SessionHeader)
	if id == "" {
		t.Fatal("no session id")
	}
	return id
}

func TestHTTPGuardMatrix(t *testing.T) {
	h := newHTTP(t)
	sid := initSession(t, h)
	ping := `{"jsonrpc":"2.0","id":2,"method":"ping"}`
	bad := strings.Repeat("a", MaxMessage+1)
	cases := []struct {
		name               string
		method, host, loc  string
		body               string
		hdr                map[string]string
		want               int
		wantHeader, wantIs string
	}{
		{"ok 127", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid}, 200, "", ""},
		{"ok localhost", "POST", "localhost:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid}, 200, "", ""},
		{"ok ipv6", "POST", "[::1]:7000", "[::1]:7000", ping, map[string]string{SessionHeader: sid}, 200, "", ""},
		{"bad host", "POST", "evil.example:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid}, 403, "", ""},
		{"wrong port", "POST", "127.0.0.1:1", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid}, 403, "", ""},
		{"host without port", "POST", "127.0.0.1", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid}, 403, "", ""},
		{"origin absent", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid}, 200, "", ""},
		{"origin 127", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Origin": "http://127.0.0.1:7000"}, 200, "", ""},
		{"origin localhost", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Origin": "http://localhost:7000"}, 200, "", ""},
		{"origin ipv6", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Origin": "http://[::1]:7000"}, 200, "", ""},
		{"origin other", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Origin": "http://evil.example"}, 403, "", ""},
		{"origin wrong port", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Origin": "http://localhost:80"}, 403, "", ""},
		{"origin https", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Origin": "https://127.0.0.1:7000"}, 403, "", ""},
		{"origin null", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Origin": "null"}, 403, "", ""},
		{"no token", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Authorization": ""}, 401, "WWW-Authenticate", "Bearer"},
		{"wrong token", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Authorization": "Bearer nope"}, 401, "WWW-Authenticate", "Bearer"},
		{"short token", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Authorization": "Bearer " + httpTok[:5]}, 401, "", ""},
		{"basic auth", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Authorization": "Basic " + httpTok}, 401, "", ""},
		{"x-semar-token", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Authorization": "", TokenHeader: httpTok}, 200, "", ""},
		{"x-semar-token wrong", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Authorization": "", TokenHeader: "x"}, 401, "", ""},
		{"content type missing", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Content-Type": ""}, 415, "", ""},
		{"content type text", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Content-Type": "text/plain"}, 415, "", ""},
		{"content type charset", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, "Content-Type": "application/json; charset=utf-8"}, 200, "", ""},
		{"too large", "POST", "127.0.0.1:7000", "127.0.0.1:7000", bad, map[string]string{SessionHeader: sid}, 413, "", ""},
		{"protocol ok", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, ProtocolHeader: "2025-06-18"}, 200, "", ""},
		{"protocol bad", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: sid, ProtocolHeader: "2000-01-01"}, 400, "", ""},
		{"get", "GET", "127.0.0.1:7000", "127.0.0.1:7000", "", map[string]string{SessionHeader: sid}, 405, "Allow", "POST, DELETE"},
		{"put", "PUT", "127.0.0.1:7000", "127.0.0.1:7000", ping, nil, 405, "", ""},
		{"get no token", "GET", "127.0.0.1:7000", "127.0.0.1:7000", "", map[string]string{"Authorization": ""}, 401, "", ""},
		{"no session", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, nil, 400, "", ""},
		{"unknown session", "POST", "127.0.0.1:7000", "127.0.0.1:7000", ping, map[string]string{SessionHeader: "ghost"}, 404, "", ""},
		{"empty body", "POST", "127.0.0.1:7000", "127.0.0.1:7000", "", map[string]string{SessionHeader: sid}, 400, "", ""},
		{"parse error", "POST", "127.0.0.1:7000", "127.0.0.1:7000", "{", map[string]string{SessionHeader: sid}, 400, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := doHTTP(h, httpReq(c.method, c.host, c.loc, c.body, c.hdr))
			if w.Code != c.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, c.want, w.Body)
			}
			if c.wantHeader != "" && w.Header().Get(c.wantHeader) != c.wantIs {
				t.Errorf("%s = %q, want %q", c.wantHeader, w.Header().Get(c.wantHeader), c.wantIs)
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("headers = %v", w.Header())
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Error("a CORS header was sent")
			}
			if c.want == 200 && w.Header().Get("Content-Type") != "application/json" {
				t.Errorf("content type = %q", w.Header().Get("Content-Type"))
			}
		})
	}
}

func TestHTTPRefusesWithoutLocalAddrOrToken(t *testing.T) {
	r := httptest.NewRequest("POST", "http://127.0.0.1:7000/mcp", strings.NewReader(initBody))
	r.Host = "127.0.0.1:7000"
	r.Header.Set("Authorization", "Bearer "+httpTok)
	r.Header.Set("Content-Type", "application/json")
	if w := doHTTP(newHTTP(t), r); w.Code != 403 {
		t.Errorf("no local address = %d", w.Code)
	}
	open := NewHTTPHandler(newTestServer(t), HTTPOptions{})
	for _, auth := range []string{"", "Bearer ", "Bearer x"} {
		if w := doHTTP(open, httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", initBody, map[string]string{"Authorization": auth})); w.Code != 401 {
			t.Errorf("empty server token, auth %q = %d", auth, w.Code)
		}
	}
}

func TestHTTPPath(t *testing.T) {
	h := NewHTTPHandler(newTestServer(t), HTTPOptions{Token: httpTok, Path: "/rpc"})
	r := httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", initBody, nil)
	if w := doHTTP(h, r); w.Code != 404 {
		t.Errorf("/mcp on a /rpc server = %d", w.Code)
	}
	r.URL.Path = "/rpc"
	if w := doHTTP(h, r); w.Code != 200 {
		t.Errorf("/rpc = %d", w.Code)
	}
}

func TestHTTPSessionLifecycle(t *testing.T) {
	h := newHTTP(t)
	sid := initSession(t, h)
	hdr := map[string]string{SessionHeader: sid}

	// The answer to initialize is JSON with the negotiated version.
	w := doHTTP(h, httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", initBody, nil))
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if resultOf(t, m)["protocolVersion"] != "2025-11-25" || w.Header().Get(SessionHeader) == sid {
		t.Errorf("second initialize = %s (session %q)", w.Body, w.Header().Get(SessionHeader))
	}

	// A notification-only POST is 202 with no body.
	w = doHTTP(h, httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, hdr))
	if w.Code != 202 || w.Body.Len() != 0 {
		t.Errorf("notification = %d %q", w.Code, w.Body)
	}
	// So is a client response.
	w = doHTTP(h, httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", `{"jsonrpc":"2.0","id":1,"result":{}}`, hdr))
	if w.Code != 202 {
		t.Errorf("response = %d", w.Code)
	}

	// tools/call sees the client and session of the HTTP session.
	w = doHTTP(h, httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"z"}}}`, hdr))
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if text, _ := textOf(t, m); text != "z|0|0|c|"+sid {
		t.Errorf("echo = %q", text)
	}

	// A batch works over HTTP.
	w = doHTTP(h, httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`, hdr))
	var arr []any
	if err := json.Unmarshal(w.Body.Bytes(), &arr); err != nil || len(arr) != 2 {
		t.Errorf("batch = %s", w.Body)
	}

	// A failed initialize issues no session.
	w = doHTTP(h, httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, nil))
	if w.Header().Get(SessionHeader) != "" {
		t.Error("a failed initialize issued a session")
	}

	// DELETE ends the session. Then it is unknown, and deleting again is 404.
	if w := doHTTP(h, httpReq("DELETE", "127.0.0.1:7000", "127.0.0.1:7000", "", hdr)); w.Code != 204 {
		t.Errorf("delete = %d", w.Code)
	}
	if w := doHTTP(h, httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", `{"jsonrpc":"2.0","id":4,"method":"ping"}`, hdr)); w.Code != 404 {
		t.Errorf("after delete = %d", w.Code)
	}
	if w := doHTTP(h, httpReq("DELETE", "127.0.0.1:7000", "127.0.0.1:7000", "", hdr)); w.Code != 404 {
		t.Errorf("second delete = %d", w.Code)
	}
	if w := doHTTP(h, httpReq("DELETE", "127.0.0.1:7000", "127.0.0.1:7000", "", nil)); w.Code != 400 {
		t.Errorf("delete without id = %d", w.Code)
	}
}

func TestHTTPDeleteCancelsCallsInFlight(t *testing.T) {
	h := newHTTP(t)
	sid := initSession(t, h)
	done := make(chan int, 1)
	go func() {
		w := doHTTP(h, httpReq("POST", "127.0.0.1:7000", "127.0.0.1:7000", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"block"}}`, map[string]string{SessionHeader: sid}))
		done <- w.Code
	}()
	time.Sleep(50 * time.Millisecond)
	doHTTP(h, httpReq("DELETE", "127.0.0.1:7000", "127.0.0.1:7000", "", map[string]string{SessionHeader: sid}))
	select {
	case code := <-done:
		if code != 202 {
			t.Errorf("the cancelled call answered %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("DELETE did not cancel the call")
	}
}

func TestHTTPOverARealListener(t *testing.T) {
	srv := httptest.NewServer(newHTTP(t))
	defer srv.Close()
	post := func(body string, hdr map[string]string) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(TokenHeader, httpTok)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	res := post(initBody, nil)
	if res.StatusCode != 200 || res.Header.Get(SessionHeader) == "" {
		t.Fatalf("initialize = %d", res.StatusCode)
	}
	res = post(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, map[string]string{SessionHeader: res.Header.Get(SessionHeader)})
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	if len(resultOf(t, m)["tools"].([]any)) != 5 {
		t.Errorf("tools/list = %v", m)
	}
	res = post(initBody, map[string]string{"Origin": "http://evil.example"})
	if res.StatusCode != 403 {
		t.Errorf("foreign origin = %d", res.StatusCode)
	}
}
