package sampleapp_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/sampleapp"
)

const pay = "tok-pay"

// payRig is an orders service and a payments service that share the orders.
type payRig struct {
	orders   *rig
	payments *rig
	service  *sampleapp.Payments
}

func newPayRig(t *testing.T, oo sampleapp.Options, po sampleapp.PaymentOptions) *payRig {
	t.Helper()
	o := newRig(t, oo)
	svc := sampleapp.NewPayments(o.app, po)
	srv := httptest.NewServer(svc)
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &payRig{orders: o, payments: &rig{t: t, server: srv, client: client}, service: svc}
}

func (g *payRig) status(t *testing.T, order string) any {
	t.Helper()
	return g.orders.do("GET", "/v1/orders/"+order, alice, "").field(t, "data", "status")
}

func TestPaymentSettlesAfterReads(t *testing.T) {
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	g.orders.do("POST", "/v1/orders", alice, `{"amount":1000,"customer":"c-1"}`)

	got := g.payments.do("POST", "/v1/payments", pay, `{"orderId":"o-1"}`)
	const want = `{"data":{"id":"p-1","orderId":"o-1","amount":1000,"status":"pending"}}`
	if got.status != 202 || strings.TrimSpace(got.body) != want {
		t.Fatalf("payment = %d %s", got.status, got.body)
	}
	if v := g.payments.do("GET", "/v1/payments/p-1", pay, "").field(t, "data", "status"); v != "pending" {
		t.Fatalf("payment status = %v", v)
	}

	// The default is two reads: the first still shows created, the second shows paid.
	if v := g.status(t, "o-1"); v != "created" {
		t.Fatalf("after one read: %v, want created", v)
	}
	if v := g.status(t, "o-1"); v != "paid" {
		t.Fatalf("after two reads: %v, want paid", v)
	}
	if v := g.status(t, "o-1"); v != "paid" {
		t.Fatalf("a paid order stays paid, got %v", v)
	}
	if v := g.payments.do("GET", "/v1/payments/p-1", pay, "").field(t, "data", "status"); v != "settled" {
		t.Fatalf("payment status = %v", v)
	}
	if ids := listIDs(t, g.orders.do("GET", "/v1/orders?status=paid&customer=c-1", alice, "")); len(ids) != 1 || ids[0] != "o-1" {
		t.Fatalf("paid list = %v", ids)
	}
}

func TestPaymentSettleAfterReadsIsConfigurable(t *testing.T) {
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{SettleAfterReads: 4})
	g.orders.createOrder(alice, 10)
	g.payments.do("POST", "/v1/payments", pay, `{"orderId":"o-1"}`)
	for i := 1; i <= 3; i++ {
		if v := g.status(t, "o-1"); v != "created" {
			t.Fatalf("read %d: %v", i, v)
		}
	}
	if v := g.status(t, "o-1"); v != "paid" {
		t.Fatalf("read 4: %v", v)
	}
}

func TestOnlyAllowedReadsCountTowardsSettling(t *testing.T) {
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	g.orders.createOrder(alice, 10)
	g.payments.do("POST", "/v1/payments", pay, `{"orderId":"o-1"}`)
	for range 5 {
		g.orders.do("GET", "/v1/orders/o-1", bob, "")   // 403
		g.orders.do("GET", "/v1/orders/o-1", "", "")    // 401
		g.orders.do("GET", "/v1/orders/o-9", alice, "") // 404
	}
	if v := g.status(t, "o-1"); v != "created" {
		t.Fatalf("refused reads settled the payment: %v", v)
	}
}

func TestPaymentNeverSettles(t *testing.T) {
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{Defects: map[sampleapp.Defect]bool{sampleapp.PaymentNeverSettles: true}})
	g.orders.createOrder(alice, 10)
	if got := g.payments.do("POST", "/v1/payments", pay, `{"orderId":"o-1"}`); got.status != 202 {
		t.Fatalf("payment = %d %s", got.status, got.body)
	}
	for i := range 20 {
		if v := g.status(t, "o-1"); v != "created" {
			t.Fatalf("read %d: %v, the order must never be paid", i+1, v)
		}
	}
	if v := g.payments.do("GET", "/v1/payments/p-1", pay, "").field(t, "data", "status"); v != "pending" {
		t.Fatalf("payment status = %v", v)
	}
}

func TestPaymentAmountWrong(t *testing.T) {
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{Defects: map[sampleapp.Defect]bool{sampleapp.PaymentAmountWrong: true}})
	g.orders.createOrder(alice, 1000)
	got := g.payments.do("POST", "/v1/payments", pay, `{"orderId":"o-1"}`)
	if got.status != 202 || got.field(t, "data", "amount") != float64(1001) {
		t.Fatalf("payment = %d %s", got.status, got.body)
	}
	// The order is paid all the same: only the amount in the payment is wrong.
	g.status(t, "o-1")
	if v := g.status(t, "o-1"); v != "paid" {
		t.Fatalf("status = %v", v)
	}
}

func TestPaymentsRefusesOrdersToken(t *testing.T) {
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	g.orders.createOrder(alice, 10)
	for _, tc := range []struct{ method, path, token, body string }{
		{"POST", "/v1/payments", alice, `{"orderId":"o-1"}`},
		{"POST", "/v1/payments", "", `{"orderId":"o-1"}`},
		{"GET", "/v1/payments/p-1", alice, ""},
		{"GET", "/v2/nothing", "", ""},
	} {
		got := g.payments.do(tc.method, tc.path, tc.token, tc.body)
		if got.status != 401 || got.field(t, "error", "code") != "UNAUTHENTICATED" {
			t.Errorf("%s %s with %q = %d %s", tc.method, tc.path, tc.token, got.status, got.body)
		}
	}
	if v := g.status(t, "o-1"); v != "created" {
		t.Fatalf("a refused payment changed the order to %v", v)
	}
	// And the other way round: the payments token is not valid for orders.
	if got := g.orders.createOrder(pay, 10); got.status != 401 {
		t.Fatalf("orders accepted the payments token: %d", got.status)
	}
}

func TestPaymentsRoutes(t *testing.T) {
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{Commit: "abc1234"})
	g.orders.createOrder(alice, 10)
	g.orders.createOrder(alice, 20)
	g.payments.do("POST", "/v1/payments", pay, `{"orderId":"o-2"}`)
	g.orders.do("POST", "/v1/orders/o-2/cancel", alice, "")

	tests := []struct {
		name, method, path, token, body string
		want                            int
		wantCode                        string
	}{
		{"version needs no token", "GET", "/version", "", "", 200, ""},
		{"body not JSON", "POST", "/v1/payments", pay, `{`, 400, "INVALID_REQUEST"},
		{"no order id", "POST", "/v1/payments", pay, `{}`, 400, "INVALID_REQUEST"},
		{"order id of another type", "POST", "/v1/payments", pay, `{"orderId":7}`, 400, "INVALID_REQUEST"},
		{"unknown order", "POST", "/v1/payments", pay, `{"orderId":"o-99"}`, 404, "ORDER_NOT_FOUND"},
		{"cancelled order", "POST", "/v1/payments", pay, `{"orderId":"o-2"}`, 409, "ORDER_NOT_PAYABLE"},
		{"unknown payment", "GET", "/v1/payments/p-99", pay, "", 404, "NOT_FOUND"},
		{"unknown route", "GET", "/v2/nothing", pay, "", 404, "NOT_FOUND"},
		{"first payment", "POST", "/v1/payments", pay, `{"orderId":"o-1"}`, 202, ""},
		{"second payment for the same order", "POST", "/v1/payments", pay, `{"orderId":"o-1"}`, 409, "ORDER_NOT_PAYABLE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := g.payments.do(tc.method, tc.path, tc.token, tc.body)
			if got.status != tc.want {
				t.Fatalf("status %d, want %d: %s", got.status, tc.want, got.body)
			}
			if tc.wantCode != "" && got.field(t, "error", "code") != tc.wantCode {
				t.Fatalf("body %s", got.body)
			}
		})
	}
	if got := g.payments.do("GET", "/version", "", "").field(t, "commit"); got != "abc1234" {
		t.Fatalf("commit = %v", got)
	}
}

func TestCancelledOrderIsNotPaidByAPendingPayment(t *testing.T) {
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	g.orders.createOrder(alice, 10)
	g.payments.do("POST", "/v1/payments", pay, `{"orderId":"o-1"}`)
	g.orders.do("POST", "/v1/orders/o-1/cancel", alice, "")
	for range 5 {
		if v := g.status(t, "o-1"); v != "cancelled" {
			t.Fatalf("status = %v", v)
		}
	}
}

func TestPaymentsRequestsCounter(t *testing.T) {
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	g.payments.do("GET", "/version", "", "")
	g.payments.do("POST", "/v1/payments", alice, `{}`)
	g.payments.do("POST", "/v1/payments", pay, `{"orderId":"o-9"}`)
	for _, tc := range []struct {
		method, prefix string
		want           int
	}{{"", "", 3}, {"POST", "/v1/payments", 2}, {"GET", "/", 1}, {"", "/nothing", 0}} {
		if got := g.service.Requests(tc.method, tc.prefix); got != tc.want {
			t.Errorf("Requests(%q, %q) = %d, want %d", tc.method, tc.prefix, got, tc.want)
		}
	}
	if got := g.orders.app.Requests("", ""); got != 0 {
		t.Fatalf("the orders service saw %d requests", got)
	}
}

func TestPaymentOptionsAreCopied(t *testing.T) {
	tokens := map[string]string{"t": "carol"}
	defects := map[sampleapp.Defect]bool{}
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{Tokens: tokens, Defects: defects})
	defects[sampleapp.PaymentNeverSettles] = true
	tokens["other"] = "x"
	g.orders.createOrder(alice, 10)
	if got := g.payments.do("POST", "/v1/payments", "t", `{"orderId":"o-1"}`); got.status != 202 {
		t.Fatalf("payment = %d", got.status)
	}
	if got := g.payments.do("POST", "/v1/payments", "other", `{"orderId":"o-1"}`); got.status != 401 {
		t.Fatalf("token added later = %d", got.status)
	}
	if got := g.payments.do("POST", "/v1/payments", pay, `{"orderId":"o-1"}`); got.status != 401 {
		t.Fatalf("default token with custom Tokens = %d", got.status)
	}
	g.status(t, "o-1")
	if v := g.status(t, "o-1"); v != "paid" {
		t.Fatalf("a later change to Defects leaked in: %v", v)
	}
}

func TestConcurrentPaymentsAndReads(t *testing.T) {
	g := newPayRig(t, sampleapp.Options{}, sampleapp.PaymentOptions{SettleAfterReads: 3})
	const n = 20
	for range n {
		g.orders.createOrder(alice, 10)
	}
	var wg sync.WaitGroup
	errs := make(chan error, n*4)
	for i := 1; i <= n; i++ {
		id := "o-" + itoa(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := g.payments.send("POST", "/v1/payments", pay, `{"orderId":"`+id+`"}`)
			if err == nil && r.status != 202 {
				err = fmt.Errorf("payment for %s = %d %s", id, r.status, r.body)
			}
			if err != nil {
				errs <- err
				return
			}
			for range 3 {
				if _, err := g.orders.send("GET", "/v1/orders/"+id, alice, ""); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("request failed: %v", err)
	}
	if ids := listIDs(t, g.orders.do("GET", "/v1/orders?status=paid", alice, "")); len(ids) != n {
		t.Fatalf("%d orders are paid, want %d", len(ids), n)
	}
}
