package sampleapp_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// do must be called on the test goroutine: it ends the test on a transport error.
func (g *rig) do(method, path, token, body string) reply {
	g.t.Helper()
	got, err := g.send(method, path, token, body)
	if err != nil {
		g.t.Fatal(err)
	}
	return got
}

// send is safe on any goroutine and returns the transport error instead of ending the test.
func (g *rig) send(method, path, token, body string) (reply, error) {
	req, err := http.NewRequest(method, g.server.URL+path, strings.NewReader(body))
	if err != nil {
		return reply{}, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return reply{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return reply{}, err
	}
	return reply{status: resp.StatusCode, location: resp.Header.Get("Location"), body: string(b)}, nil
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
		{"create", "POST", "/v1/orders", alice, `{"amount":100000}`, 201, `{"data":{"id":"o-1","amount":100000,"refunded_total":0,"owner":"alice","status":"created"}}`},
		{"read", "GET", "/v1/orders/o-1", alice, "", 200, `{"data":{"id":"o-1","amount":100000,"refunded_total":0,"owner":"alice","status":"created"}}`},
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
		{"read after refund", "GET", "/v1/orders/o-1", alice, "", 200, `{"data":{"id":"o-1","amount":100000,"refunded_total":40000,"owner":"alice","status":"created"}}`},
		{"second refund over the rest", "POST", "/v1/orders/o-1/refunds", alice, `{"amount":60001}`, 422, `{"error":{"code":"REFUND_EXCEEDS_TOTAL"}}`},
		{"second refund of the rest", "POST", "/v1/orders/o-1/refunds", alice, `{"amount":60000}`, 201, `{"data":{"id":"r-2","amount":60000,"order_id":"o-1"}}`},
		{"order ids count up", "POST", "/v1/orders", bob, `{"amount":5}`, 201, `{"data":{"id":"o-2","amount":5,"refunded_total":0,"owner":"bob","status":"created"}}`},
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

	t.Run("RedirectAway sends a relative target as given", func(t *testing.T) {
		g := newRig(t, sampleapp.Options{
			Defects:    map[sampleapp.Defect]bool{sampleapp.RedirectAway: true},
			RedirectTo: "elsewhere",
		})
		g.createOrder(alice, 100)
		got := g.do("GET", "/v1/orders/o-1", alice, "")
		if got.status != 302 || got.location != "elsewhere" {
			t.Fatalf("read = %d, Location %q, want 302 and %q", got.status, got.location, "elsewhere")
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

func TestSlowRefundAnswersNoEarlierThanItsDelay(t *testing.T) {
	const delay = 50 * time.Millisecond
	g := newRig(t, sampleapp.Options{
		Defects: map[sampleapp.Defect]bool{sampleapp.SlowRefund: true},
		Delay:   delay,
	})
	g.createOrder(alice, 100000)

	start := time.Now()
	got := g.do("POST", "/v1/orders/o-1/refunds", alice, `{"amount":25000}`)
	elapsed := time.Since(start)
	if got.status != 201 || got.body != `{"data":{"id":"r-1","amount":25000,"order_id":"o-1"}}` {
		t.Fatalf("refund = %d %s", got.status, got.body)
	}
	if elapsed < delay {
		t.Fatalf("refund answered after %v, want at least %v", elapsed, delay)
	}
	if v := g.do("GET", "/v1/orders/o-1", alice, "").field(t, "data", "refunded_total"); v != float64(25000) {
		t.Fatalf("refunded_total = %v", v)
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
			got, err := g.send("POST", "/v1/orders", alice, `{"amount":`+itoa(orders*refundsPer*refundAmount)+`}`)
			if err != nil {
				t.Errorf("create: %v", err)
				return
			}
			if got.status != 201 {
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
				got, err := g.send("POST", "/v1/orders/"+id+"/refunds", alice, `{"amount":100}`)
				if err != nil {
					t.Errorf("refund on %s: %v", id, err)
					return
				}
				if got.status != 201 {
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

func TestRefundIDsCountAcrossOrders(t *testing.T) {
	g := newRig(t, sampleapp.Options{})
	g.createOrder(alice, 100)
	g.createOrder(alice, 100)
	for i, order := range []string{"o-1", "o-2"} {
		want := "r-" + itoa(i+1)
		got := g.do("POST", "/v1/orders/"+order+"/refunds", alice, `{"amount":1}`)
		if got.status != 201 || got.field(t, "data", "id") != want || got.field(t, "data", "order_id") != order {
			t.Fatalf("refund on %s = %d %s, want id %s", order, got.status, got.body, want)
		}
	}
}

func TestRefundErrorPrecedence(t *testing.T) {
	g := newRig(t, sampleapp.Options{})
	g.createOrder(alice, 100)
	tests := []struct {
		name, path string
		want       int
	}{
		{"unknown order beats forbidden and bad body", "/v1/orders/o-99/refunds", 404},
		{"forbidden beats bad body", "/v1/orders/o-1/refunds", 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := g.do("POST", tc.path, bob, `not json`); got.status != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", got.status, tc.want, got.body)
			}
		})
	}
}

func TestEmptyTokensMeansNoTokenIsValid(t *testing.T) {
	empty := newRig(t, sampleapp.Options{Tokens: map[string]string{}})
	if got := empty.createOrder(alice, 10); got.status != 401 {
		t.Fatalf("empty Tokens, tok-alice = %d, want 401", got.status)
	}
	nilMap := newRig(t, sampleapp.Options{})
	if got := nilMap.createOrder(alice, 10); got.status != 201 {
		t.Fatalf("nil Tokens, tok-alice = %d, want 201", got.status)
	}
}

func TestCreateKeepsCustomerAndRef(t *testing.T) {
	g := newRig(t, sampleapp.Options{})
	got := g.do("POST", "/v1/orders", alice, `{"amount":1000,"customer":"c-1","ref":"semar-r-1"}`)
	const want = `{"data":{"id":"o-1","amount":1000,"refunded_total":0,"owner":"alice","status":"created","customer":"c-1","ref":"semar-r-1"}}`
	if got.status != 201 || strings.TrimSpace(got.body) != want {
		t.Fatalf("create = %d %s", got.status, got.body)
	}
	for _, body := range []string{`{"amount":1,"customer":5}`, `{"amount":1,"ref":["x"]}`, `{"amount":1,"customer":"` + strings.Repeat("c", 129) + `"}`} {
		if got := g.do("POST", "/v1/orders", alice, body); got.status != 400 {
			t.Errorf("body %.40s: status %d, want 400", body, got.status)
		}
	}
}

func listIDs(t *testing.T, r reply) []string {
	t.Helper()
	data, ok := r.field(t, "data").([]any)
	if !ok {
		t.Fatalf("data is not a list: %s", r.body)
	}
	ids := []string{}
	for _, e := range data {
		ids = append(ids, e.(map[string]any)["id"].(string))
	}
	return ids
}

func TestListFiltersByStatusAndCustomer(t *testing.T) {
	g := newRig(t, sampleapp.Options{})
	for _, body := range []string{
		`{"amount":10,"customer":"c-1"}`, // o-1
		`{"amount":20,"customer":"c-2"}`, // o-2
		`{"amount":30,"customer":"c-1"}`, // o-3
	} {
		g.do("POST", "/v1/orders", alice, body)
	}
	g.do("POST", "/v1/orders", bob, `{"amount":40,"customer":"c-1"}`) // o-4
	g.do("POST", "/v1/orders/o-3/cancel", alice, "")

	tests := []struct {
		name, path, token string
		want              []string
	}{
		{"all of the caller", "/v1/orders", alice, []string{"o-1", "o-2", "o-3"}},
		{"another caller", "/v1/orders", bob, []string{"o-4"}},
		{"by customer", "/v1/orders?customer=c-1", alice, []string{"o-1", "o-3"}},
		{"by status", "/v1/orders?status=created", alice, []string{"o-1", "o-2"}},
		{"by cancelled status", "/v1/orders?status=cancelled", alice, []string{"o-3"}},
		{"by both", "/v1/orders?status=created&customer=c-1", alice, []string{"o-1"}},
		{"nothing paid", "/v1/orders?status=paid", alice, []string{}},
		{"unknown customer", "/v1/orders?customer=nobody", alice, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := g.do("GET", tc.path, tc.token, "")
			if got.status != 200 {
				t.Fatalf("status %d: %s", got.status, got.body)
			}
			if ids := listIDs(t, got); fmt.Sprint(ids) != fmt.Sprint(tc.want) {
				t.Fatalf("ids = %v, want %v", ids, tc.want)
			}
		})
	}
	if got := g.do("GET", "/v1/orders?status=paid", alice, ""); strings.TrimSpace(got.body) != `{"data":[]}` {
		t.Fatalf("an empty list must be [], got %s", got.body)
	}
	if got := g.do("GET", "/v1/orders", "", ""); got.status != 401 {
		t.Fatalf("list without a token = %d", got.status)
	}
}

func TestCancel(t *testing.T) {
	g := newRig(t, sampleapp.Options{})
	g.createOrder(alice, 100)
	tests := []struct {
		name, path, token string
		want              int
		wantCode          string
	}{
		{"without a token", "/v1/orders/o-1/cancel", "", 401, "UNAUTHENTICATED"},
		{"another user", "/v1/orders/o-1/cancel", bob, 403, "FORBIDDEN"},
		{"unknown order", "/v1/orders/o-9/cancel", alice, 404, "NOT_FOUND"},
		{"the owner", "/v1/orders/o-1/cancel", alice, 200, ""},
		{"twice", "/v1/orders/o-1/cancel", alice, 409, "ALREADY_CANCELLED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := g.do("POST", tc.path, tc.token, "")
			if got.status != tc.want {
				t.Fatalf("status %d, want %d: %s", got.status, tc.want, got.body)
			}
			if tc.wantCode != "" && got.field(t, "error", "code") != tc.wantCode {
				t.Fatalf("body %s", got.body)
			}
		})
	}
	if v := g.do("GET", "/v1/orders/o-1", alice, "").field(t, "data", "status"); v != "cancelled" {
		t.Fatalf("status = %v", v)
	}
}

func TestCancelFails(t *testing.T) {
	g := newRig(t, sampleapp.Options{Defects: map[sampleapp.Defect]bool{sampleapp.CancelFails: true}})
	g.createOrder(alice, 100)
	got := g.do("POST", "/v1/orders/o-1/cancel", alice, "")
	if got.status != 500 || got.field(t, "error", "code") != "INTERNAL" {
		t.Fatalf("cancel = %d %s", got.status, got.body)
	}
	if v := g.do("GET", "/v1/orders/o-1", alice, "").field(t, "data", "status"); v != "created" {
		t.Fatalf("a failed cancel changed the order to %v", v)
	}
	if got := g.do("POST", "/v1/orders/o-1/cancel", "", ""); got.status != 401 {
		t.Fatalf("cancel without a token = %d, want 401 before the defect", got.status)
	}
}
