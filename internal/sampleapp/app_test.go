package sampleapp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/sampleapp"
)

type reply struct {
	status   int
	location string
	body     string
}

func (r reply) field(t *testing.T, path ...string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		t.Fatalf("body %q is not JSON: %v", r.body, err)
	}
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("body %q has no %v", r.body, path)
		}
		v = m[p]
	}
	return v
}

type rig struct {
	t      *testing.T
	app    *sampleapp.App
	server *httptest.Server
	client *http.Client
}

func newRig(t *testing.T, o sampleapp.Options) *rig {
	t.Helper()
	app := sampleapp.New(o)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &rig{t: t, app: app, server: srv, client: client}
}

func (g *rig) do(method, path, token, body string) reply {
	g.t.Helper()
	return g.doCtx(context.Background(), method, path, token, body)
}

func (g *rig) doCtx(ctx context.Context, method, path, token, body string) reply {
	g.t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, g.server.URL+path, strings.NewReader(body))
	if err != nil {
		g.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		g.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		g.t.Fatal(err)
	}
	return reply{status: resp.StatusCode, location: resp.Header.Get("Location"), body: string(b)}
}

func (g *rig) createOrder(token string, amount int) reply {
	g.t.Helper()
	return g.do("POST", "/v1/orders", token, `{"amount":`+itoa(amount)+`}`)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

const (
	alice = "tok-alice"
	bob   = "tok-bob"
)

func TestRoutes(t *testing.T) {
	g := newRig(t, sampleapp.Options{})

	tests := []struct {
		name       string
		method     string
		path       string
		token      string
		body       string
		wantStatus int
		wantBody   string
	}{
		{"version needs no token", "GET", "/version", "", "", 200, `{"commit":"9f3c2ab"}`},
		{"create without token", "POST", "/v1/orders", "", `{"amount":100000}`, 401, `{"error":{"code":"UNAUTHENTICATED"}}`},
		{"create with unknown token", "POST", "/v1/orders", "nope", `{"amount":100000}`, 401, `{"error":{"code":"UNAUTHENTICATED"}}`},
		{"create", "POST", "/v1/orders", alice, `{"amount":100000}`, 201, `{"data":{"id":"o-1","amount":100000,"refunded_total":0,"owner":"alice"}}`},
		{"read", "GET", "/v1/orders/o-1", alice, "", 200, `{"data":{"id":"o-1","amount":100000,"refunded_total":0,"owner":"alice"}}`},
		{"read without token", "GET", "/v1/orders/o-1", "", "", 401, `{"error":{"code":"UNAUTHENTICATED"}}`},
		{"read unknown order", "GET", "/v1/orders/o-99", alice, "", 404, `{"error":{"code":"NOT_FOUND"}}`},
		{"read by another user", "GET", "/v1/orders/o-1", bob, "", 403, `{"error":{"code":"FORBIDDEN"}}`},
		{"refund above total", "POST", "/v1/orders/o-1/refunds", alice, `{"amount":150000}`, 422, `{"error":{"code":"REFUND_EXCEEDS_TOTAL"}}`},
		{"refund by another user", "POST", "/v1/orders/o-1/refunds", bob, `{"amount":1}`, 403, `{"error":{"code":"FORBIDDEN"}}`},
		{"refund on unknown order", "POST", "/v1/orders/o-99/refunds", alice, `{"amount":1}`, 404, `{"error":{"code":"NOT_FOUND"}}`},
		{"refund without token", "POST", "/v1/orders/o-1/refunds", "", `{"amount":1}`, 401, `{"error":{"code":"UNAUTHENTICATED"}}`},
		{"refund body not JSON", "POST", "/v1/orders/o-1/refunds", alice, `not json`, 400, `{"error":{"code":"INVALID_REQUEST"}}`},
		{"refund amount zero", "POST", "/v1/orders/o-1/refunds", alice, `{"amount":0}`, 400, `{"error":{"code":"INVALID_REQUEST"}}`},
		{"refund amount negative", "POST", "/v1/orders/o-1/refunds", alice, `{"amount":-5}`, 400, `{"error":{"code":"INVALID_REQUEST"}}`},
		{"refund amount fractional", "POST", "/v1/orders/o-1/refunds", alice, `{"amount":1.5}`, 400, `{"error":{"code":"INVALID_REQUEST"}}`},
		{"refund amount missing", "POST", "/v1/orders/o-1/refunds", alice, `{}`, 400, `{"error":{"code":"INVALID_REQUEST"}}`},
		{"create body not JSON", "POST", "/v1/orders", alice, `{`, 400, `{"error":{"code":"INVALID_REQUEST"}}`},
		{"create amount zero", "POST", "/v1/orders", alice, `{"amount":0}`, 400, `{"error":{"code":"INVALID_REQUEST"}}`},
		{"unknown route with token", "GET", "/v2/nothing", alice, "", 404, `{"error":{"code":"NOT_FOUND"}}`},
		{"unknown route without token", "GET", "/v2/nothing", "", "", 401, `{"error":{"code":"UNAUTHENTICATED"}}`},
		{"refund of the right size", "POST", "/v1/orders/o-1/refunds", alice, `{"amount":40000}`, 201, `{"data":{"id":"r-1","amount":40000,"order_id":"o-1"}}`},
		{"read after refund", "GET", "/v1/orders/o-1", alice, "", 200, `{"data":{"id":"o-1","amount":100000,"refunded_total":40000,"owner":"alice"}}`},
		{"second refund over the rest", "POST", "/v1/orders/o-1/refunds", alice, `{"amount":60001}`, 422, `{"error":{"code":"REFUND_EXCEEDS_TOTAL"}}`},
		{"second refund of the rest", "POST", "/v1/orders/o-1/refunds", alice, `{"amount":60000}`, 201, `{"data":{"id":"r-2","amount":60000,"order_id":"o-1"}}`},
		{"order ids count up", "POST", "/v1/orders", bob, `{"amount":5}`, 201, `{"data":{"id":"o-2","amount":5,"refunded_total":0,"owner":"bob"}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := g.do(tc.method, tc.path, tc.token, tc.body)
			if got.status != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", got.status, tc.wantStatus, got.body)
			}
			if strings.TrimSpace(got.body) != tc.wantBody {
				t.Fatalf("body = %s, want %s", got.body, tc.wantBody)
			}
		})
	}
}

func TestContentTypeIsJSON(t *testing.T) {
	g := newRig(t, sampleapp.Options{})
	for _, p := range []string{"/version", "/v1/orders/o-1"} {
		resp, err := g.client.Get(g.server.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type = %q", p, ct)
		}
	}
}

func TestDefects(t *testing.T) {
	t.Run("RefundOverTotal accepts a refund above the total", func(t *testing.T) {
		g := newRig(t, sampleapp.Options{Defects: map[sampleapp.Defect]bool{sampleapp.RefundOverTotal: true}})
		g.createOrder(alice, 100000)
		if got := g.do("POST", "/v1/orders/o-1/refunds", alice, `{"amount":150000}`); got.status != 201 {
			t.Fatalf("refund status = %d, body %s", got.status, got.body)
		}
		read := g.do("GET", "/v1/orders/o-1", alice, "")
		if v := read.field(t, "data", "refunded_total"); v != float64(150000) {
			t.Fatalf("refunded_total = %v", v)
		}
	})

	t.Run("RefundServerError answers 500 and changes nothing", func(t *testing.T) {
		g := newRig(t, sampleapp.Options{Defects: map[sampleapp.Defect]bool{sampleapp.RefundServerError: true}})
		g.createOrder(alice, 100000)
		got := g.do("POST", "/v1/orders/o-1/refunds", alice, `{"amount":10}`)
		if got.status != 500 || got.field(t, "error", "code") != "INTERNAL" {
			t.Fatalf("refund = %d %s", got.status, got.body)
		}
		if v := g.do("GET", "/v1/orders/o-1", alice, "").field(t, "data", "refunded_total"); v != float64(0) {
			t.Fatalf("refunded_total = %v", v)
		}
	})

	t.Run("BuildChanges gives another commit from the second call", func(t *testing.T) {
		g := newRig(t, sampleapp.Options{Defects: map[sampleapp.Defect]bool{sampleapp.BuildChanges: true}})
		for i, want := range []string{"9f3c2ab", "c0ffee1", "c0ffee1"} {
			if got := g.do("GET", "/version", "", "").field(t, "commit"); got != want {
				t.Fatalf("call %d: commit = %v, want %s", i+1, got, want)
			}
		}
	})

	t.Run("Commit option replaces the default", func(t *testing.T) {
		g := newRig(t, sampleapp.Options{Commit: "abc1234"})
		if got := g.do("GET", "/version", "", "").field(t, "commit"); got != "abc1234" {
			t.Fatalf("commit = %v", got)
		}
	})

	t.Run("RedirectAway redirects a read only", func(t *testing.T) {
		g := newRig(t, sampleapp.Options{
			Defects:    map[sampleapp.Defect]bool{sampleapp.RedirectAway: true},
			RedirectTo: "http://127.0.0.1:1/elsewhere",
		})
		g.createOrder(alice, 100)
		got := g.do("GET", "/v1/orders/o-1", alice, "")
		if got.status != 302 || got.location != "http://127.0.0.1:1/elsewhere" {
			t.Fatalf("read = %d, Location %q", got.status, got.location)
		}
		if got := g.do("GET", "/v1/orders/o-1", "", ""); got.status != 401 {
			t.Fatalf("read without token = %d, want 401", got.status)
		}
		if got := g.do("POST", "/v1/orders/o-1/refunds", alice, `{"amount":1}`); got.status != 201 {
			t.Fatalf("refund = %d, want 201", got.status)
		}
	})

	t.Run("OtherUserReads lets a non-owner read but not refund", func(t *testing.T) {
		g := newRig(t, sampleapp.Options{Defects: map[sampleapp.Defect]bool{sampleapp.OtherUserReads: true}})
		g.createOrder(alice, 100)
		if got := g.do("GET", "/v1/orders/o-1", bob, ""); got.status != 200 {
			t.Fatalf("bob read = %d", got.status)
		}
		if got := g.do("POST", "/v1/orders/o-1/refunds", bob, `{"amount":1}`); got.status != 403 {
			t.Fatalf("bob refund = %d, want 403", got.status)
		}
	})
}

func TestSlowRefundIsAppliedBeforeTheAnswer(t *testing.T) {
	g := newRig(t, sampleapp.Options{
		Defects: map[sampleapp.Defect]bool{sampleapp.SlowRefund: true},
		Delay:   300 * time.Millisecond,
	})
	g.createOrder(alice, 100000)

	answered := make(chan reply, 1)
	go func() { answered <- g.do("POST", "/v1/orders/o-1/refunds", alice, `{"amount":25000}`) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		v := g.do("GET", "/v1/orders/o-1", alice, "").field(t, "data", "refunded_total")
		if v == float64(25000) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("refund never applied, refunded_total = %v", v)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case got := <-answered:
		t.Fatalf("the refund answered (%d) before its delay passed", got.status)
	default:
	}
	select {
	case got := <-answered:
		if got.status != 201 {
			t.Fatalf("refund = %d %s", got.status, got.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refund never answered")
	}
}

func TestSlowRefundStopsWhenTheRequestIsCancelled(t *testing.T) {
	g := newRig(t, sampleapp.Options{
		Defects: map[sampleapp.Defect]bool{sampleapp.SlowRefund: true},
		Delay:   time.Hour,
	})
	g.createOrder(alice, 100)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "POST", g.server.URL+"/v1/orders/o-1/refunds", strings.NewReader(`{"amount":1}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+alice)
	done := make(chan error, 1)
	go func() {
		resp, err := g.client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for g.do("GET", "/v1/orders/o-1", alice, "").field(t, "data", "refunded_total") != float64(1) {
		if time.Now().After(deadline) {
			t.Fatal("refund never applied")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("client call succeeded after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client call did not stop")
	}
}

func TestRequestsCounter(t *testing.T) {
	g := newRig(t, sampleapp.Options{})
	g.do("GET", "/version", "", "")
	g.do("GET", "/v1/orders/o-1", "", "")
	g.createOrder(alice, 100)
	g.do("GET", "/v1/orders/o-1", alice, "")
	g.do("POST", "/v1/orders/o-1/refunds", alice, `{"amount":1}`)
	g.do("POST", "/v1/orders/o-1/refunds", alice, `{"amount":1}`)

	tests := []struct {
		method, prefix string
		want           int
	}{
		{"", "", 6},
		{"", "/v1/", 5},
		{"GET", "/v1/orders", 2},
		{"POST", "/v1/orders", 3},
		{"POST", "/v1/orders/o-1/refunds", 2},
		{"get", "/v1/orders", 0},
		{"DELETE", "/", 0},
		{"GET", "/version", 1},
		{"", "/nothing", 0},
	}
	for _, tc := range tests {
		if got := g.app.Requests(tc.method, tc.prefix); got != tc.want {
			t.Errorf("Requests(%q, %q) = %d, want %d", tc.method, tc.prefix, got, tc.want)
		}
	}
}

func TestOptionsAreCopied(t *testing.T) {
	defects := map[sampleapp.Defect]bool{}
	tokens := map[string]string{"t": "carol"}
	g := newRig(t, sampleapp.Options{Defects: defects, Tokens: tokens})
	defects[sampleapp.RefundServerError] = true
	tokens["t"] = "mallory"
	tokens["other"] = "x"

	order := g.createOrder("t", 10)
	if order.status != 201 || order.field(t, "data", "owner") != "carol" {
		t.Fatalf("create = %d %s", order.status, order.body)
	}
	if got := g.do("POST", "/v1/orders/o-1/refunds", "t", `{"amount":1}`); got.status != 201 {
		t.Fatalf("refund = %d, a later change to Defects leaked in", got.status)
	}
	if got := g.createOrder("other", 10); got.status != 401 {
		t.Fatalf("token added later = %d, want 401", got.status)
	}
	if got := g.createOrder(alice, 10); got.status != 401 {
		t.Fatalf("default token with custom Tokens = %d, want 401", got.status)
	}
}

func TestConcurrentCreatesAndRefunds(t *testing.T) {
	g := newRig(t, sampleapp.Options{})
	const orders, refundsPer, refundAmount = 20, 10, 100

	// A cap on in-flight calls keeps a burst of new connections within the listen backlog.
	slots := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i := 0; i < orders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if got := g.createOrder(alice, orders*refundsPer*refundAmount); got.status != 201 {
				t.Errorf("create = %d", got.status)
			}
		}()
	}
	wg.Wait()

	for i := 1; i <= orders; i++ {
		id := "o-" + itoa(i)
		for j := 0; j < refundsPer; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				if got := g.do("POST", "/v1/orders/"+id+"/refunds", alice, `{"amount":100}`); got.status != 201 {
					t.Errorf("refund on %s = %d", id, got.status)
				}
			}()
		}
	}
	wg.Wait()

	ids := map[any]bool{}
	for i := 1; i <= orders; i++ {
		got := g.do("GET", "/v1/orders/o-"+itoa(i), alice, "")
		if v := got.field(t, "data", "refunded_total"); v != float64(refundsPer*refundAmount) {
			t.Errorf("o-%d refunded_total = %v", i, v)
		}
		ids[got.field(t, "data", "id")] = true
	}
	if len(ids) != orders {
		t.Errorf("distinct order ids = %d, want %d", len(ids), orders)
	}
	if got := g.app.Requests("POST", "/v1/orders"); got != orders+orders*refundsPer {
		t.Errorf("POST count = %d", got)
	}
}
