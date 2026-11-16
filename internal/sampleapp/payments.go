package sampleapp

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Defects of the payments service. They go in PaymentOptions.Defects.
const (
	// PaymentNeverSettles leaves every order unpaid, however often it is read.
	PaymentNeverSettles Defect = "payment-never-settles"
	// PaymentAmountWrong makes a payment name an amount one above the order total.
	PaymentAmountWrong Defect = "payment-amount-wrong"
)

const (
	paymentPending = "pending"
	paymentSettled = "settled"

	defaultSettleAfterReads = 2
)

type payment struct {
	ID      string `json:"id"`
	OrderID string `json:"orderId"`
	Amount  int64  `json:"amount"`
	Status  string `json:"status"`

	settleAfter  int
	neverSettles bool
	reads        int
}

type PaymentOptions struct {
	Defects map[Defect]bool
	Commit  string
	// Tokens maps a token to a user. The default is tok-pay for payments-tester. The tokens of
	// the orders service are not valid here.
	Tokens map[string]string
	// SettleAfterReads is how many reads of GET /v1/orders/{id} settle a payment. The default is 2.
	SettleAfterReads int
}

// Payments is a second service. It shares the orders of one App: a payment settles by the reads of
// its order, never by the clock, so a test that polls needs no sleep.
type Payments struct {
	orders  *App
	defects map[Defect]bool
	commit  string
	tokens  map[string]string
	settle  int
	mux     *http.ServeMux

	mu       sync.Mutex
	requests map[requestKey]int
}

func NewPayments(orders *App, o PaymentOptions) *Payments {
	p := &Payments{
		orders:   orders,
		defects:  maps.Clone(o.Defects),
		commit:   o.Commit,
		tokens:   maps.Clone(o.Tokens),
		settle:   o.SettleAfterReads,
		requests: map[requestKey]int{},
	}
	if p.commit == "" {
		p.commit = defaultCommit
	}
	if p.tokens == nil {
		p.tokens = map[string]string{"tok-pay": "payments-tester"}
	}
	if p.settle <= 0 {
		p.settle = defaultSettleAfterReads
	}
	p.mux = http.NewServeMux()
	p.mux.HandleFunc("GET /version", p.version)
	p.mux.HandleFunc("POST /v1/payments", p.create)
	p.mux.HandleFunc("GET /v1/payments/{id}", p.read)
	p.mux.HandleFunc("/", p.notFound)
	return p
}

func (p *Payments) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.requests[requestKey{r.Method, r.URL.Path}]++
	p.mu.Unlock()
	p.mux.ServeHTTP(w, r)
}

// Requests counts the requests received so far, answered or not. An empty method matches all.
func (p *Payments) Requests(method, pathPrefix string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for k, c := range p.requests {
		if (method == "" || k.method == method) && strings.HasPrefix(k.path, pathPrefix) {
			n += c
		}
	}
	return n
}

func (p *Payments) authenticate(w http.ResponseWriter, r *http.Request) bool {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if _, known := p.tokens[token]; known {
			return true
		}
	}
	fail(w, http.StatusUnauthorized, "UNAUTHENTICATED")
	return false
}

func (p *Payments) notFound(w http.ResponseWriter, r *http.Request) {
	if p.authenticate(w, r) {
		fail(w, http.StatusNotFound, "NOT_FOUND")
	}
}

func (p *Payments) version(w http.ResponseWriter, _ *http.Request) {
	write(w, http.StatusOK, map[string]string{"commit": p.commit})
}

// create takes {"orderId": "o-1"} and answers 202. The order is paid later, by reads.
func (p *Payments) create(w http.ResponseWriter, r *http.Request) {
	if !p.authenticate(w, r) {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	var in struct {
		OrderID string `json:"orderId"`
	}
	if err != nil || json.Unmarshal(raw, &in) != nil || in.OrderID == "" {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}

	a := p.orders
	a.mu.Lock()
	o, found := a.orders[in.OrderID]
	switch {
	case !found:
		a.mu.Unlock()
		fail(w, http.StatusNotFound, "ORDER_NOT_FOUND")
		return
	case o.Status != StatusCreated || a.pending[o.ID] != nil:
		a.mu.Unlock()
		fail(w, http.StatusConflict, "ORDER_NOT_PAYABLE")
		return
	}
	a.paymentCount++
	amount := o.Amount
	if p.defects[PaymentAmountWrong] {
		amount++
	}
	pay := &payment{
		ID: "p-" + strconv.Itoa(a.paymentCount), OrderID: o.ID, Amount: amount, Status: paymentPending,
		settleAfter: p.settle, neverSettles: p.defects[PaymentNeverSettles],
	}
	a.payments[pay.ID] = pay
	a.pending[o.ID] = pay
	snapshot := *pay
	a.mu.Unlock()
	write(w, http.StatusAccepted, map[string]any{"data": snapshot})
}

func (p *Payments) read(w http.ResponseWriter, r *http.Request) {
	if !p.authenticate(w, r) {
		return
	}
	a := p.orders
	a.mu.Lock()
	pay, found := a.payments[r.PathValue("id")]
	var snapshot payment
	if found {
		snapshot = *pay
	}
	a.mu.Unlock()
	if !found {
		fail(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	write(w, http.StatusOK, map[string]any{"data": snapshot})
}
