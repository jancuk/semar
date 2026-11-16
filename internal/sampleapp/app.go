// Package sampleapp is an in-memory HTTP service for tests. Each Defect switch seeds one bug
// that Semar must detect.
package sampleapp

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Defect string

const (
	RefundOverTotal   Defect = "refund-over-total"
	RefundServerError Defect = "refund-server-error"
	SlowRefund        Defect = "slow-refund"
	BuildChanges      Defect = "build-changes"
	RedirectAway      Defect = "redirect-away"
	OtherUserReads    Defect = "other-user-reads"
	// CancelFails makes POST /v1/orders/{id}/cancel answer 500 and leave the order as it is.
	CancelFails Defect = "cancel-fails"
)

// The states of an order. A payment that settles moves a created order to paid.
const (
	StatusCreated   = "created"
	StatusPaid      = "paid"
	StatusCancelled = "cancelled"
)

type Options struct {
	Defects    map[Defect]bool
	Commit     string
	Tokens     map[string]string
	Delay      time.Duration
	RedirectTo string
}

const (
	defaultCommit = "9f3c2ab"
	changedCommit = "c0ffee1"
	maxBody       = 64 << 10
	// Bounds a single order or refund amount; it only delays int64 overflow once RefundOverTotal lets refunded_total grow.
	maxAmount = 1 << 53
)

type order struct {
	ID            string `json:"id"`
	Amount        int64  `json:"amount"`
	RefundedTotal int64  `json:"refunded_total"`
	Owner         string `json:"owner"`
	Status        string `json:"status"`
	Customer      string `json:"customer,omitempty"`
	Ref           string `json:"ref,omitempty"`
}

type refund struct {
	ID      string `json:"id"`
	Amount  int64  `json:"amount"`
	OrderID string `json:"order_id"`
}

type requestKey struct{ method, path string }

type App struct {
	defects    map[Defect]bool
	commit     string
	tokens     map[string]string
	delay      time.Duration
	redirectTo string
	mux        *http.ServeMux

	mu           sync.Mutex
	orders       map[string]*order
	orderList    []*order // in the order of creation
	payments     map[string]*payment
	paymentCount int
	pending      map[string]*payment // the unsettled payment of an order
	orderCount   int
	refundCount  int
	versionCalls int
	requests     map[requestKey]int
}

func New(o Options) *App {
	a := &App{
		defects:    maps.Clone(o.Defects),
		commit:     o.Commit,
		tokens:     maps.Clone(o.Tokens),
		delay:      o.Delay,
		redirectTo: o.RedirectTo,
		orders:     map[string]*order{},
		payments:   map[string]*payment{},
		pending:    map[string]*payment{},
		requests:   map[requestKey]int{},
	}
	if a.commit == "" {
		a.commit = defaultCommit
	}
	if a.tokens == nil {
		a.tokens = map[string]string{"tok-alice": "alice", "tok-bob": "bob"}
	}
	a.mux = http.NewServeMux()
	a.mux.HandleFunc("GET /version", a.version)
	a.mux.HandleFunc("POST /v1/orders", a.createOrder)
	a.mux.HandleFunc("GET /v1/orders", a.listOrders)
	a.mux.HandleFunc("POST /v1/orders/{id}/cancel", a.cancelOrder)
	a.mux.HandleFunc("GET /v1/orders/{id}", a.readOrder)
	a.mux.HandleFunc("POST /v1/orders/{id}/refunds", a.createRefund)
	a.mux.HandleFunc("/", a.notFound)
	return a
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.requests[requestKey{r.Method, r.URL.Path}]++
	a.mu.Unlock()
	a.mux.ServeHTTP(w, r)
}

// Requests counts the requests received so far, answered or not. An empty method matches all.
func (a *App) Requests(method, pathPrefix string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for k, c := range a.requests {
		if (method == "" || k.method == method) && strings.HasPrefix(k.path, pathPrefix) {
			n += c
		}
	}
	return n
}

func (a *App) on(d Defect) bool { return a.defects[d] }

func write(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":{"code":"INTERNAL"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

type errorBody struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func fail(w http.ResponseWriter, status int, code string) {
	var e errorBody
	e.Error.Code = code
	write(w, status, e)
}

func (a *App) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ok {
		if user, known := a.tokens[token]; known {
			return user, true
		}
	}
	fail(w, http.StatusUnauthorized, "UNAUTHENTICATED")
	return "", false
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticate(w, r); !ok {
		return
	}
	fail(w, http.StatusNotFound, "NOT_FOUND")
}

func (a *App) version(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	a.versionCalls++
	commit := a.commit
	if a.on(BuildChanges) && a.versionCalls > 1 {
		commit = changedCommit
	}
	a.mu.Unlock()
	write(w, http.StatusOK, map[string]string{"commit": commit})
}

// readAmount parses {"amount": n}. A body that is not JSON, or an amount that is not a
// positive integer, is an error.
func readAmount(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return 0, false
	}
	var in struct {
		Amount json.RawMessage `json:"amount"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(string(in.Amount), 10, 64)
	if err != nil || n <= 0 || n > maxAmount {
		return 0, false
	}
	return n, true
}

const maxText = 128

type orderInput struct {
	amount        int64
	customer, ref string
}

// readOrderInput parses {"amount": n, "customer": "...", "ref": "..."}. The last two are optional.
func readOrderInput(w http.ResponseWriter, r *http.Request) (orderInput, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return orderInput{}, false
	}
	var in struct {
		Amount   json.RawMessage `json:"amount"`
		Customer string          `json:"customer"`
		Ref      string          `json:"ref"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || len(in.Customer) > maxText || len(in.Ref) > maxText {
		return orderInput{}, false
	}
	n, err := strconv.ParseInt(string(in.Amount), 10, 64)
	if err != nil || n <= 0 || n > maxAmount {
		return orderInput{}, false
	}
	return orderInput{amount: n, customer: in.Customer, ref: in.Ref}, true
}

func (a *App) createOrder(w http.ResponseWriter, r *http.Request) {
	user, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	in, ok := readOrderInput(w, r)
	if !ok {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	a.mu.Lock()
	a.orderCount++
	o := &order{ID: "o-" + strconv.Itoa(a.orderCount), Amount: in.amount, Owner: user, Status: StatusCreated, Customer: in.customer, Ref: in.ref}
	a.orders[o.ID] = o
	a.orderList = append(a.orderList, o)
	snapshot := *o
	a.mu.Unlock()
	write(w, http.StatusCreated, map[string]any{"data": snapshot})
}

func (a *App) readOrder(w http.ResponseWriter, r *http.Request) {
	user, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	if a.on(RedirectAway) {
		w.Header().Set("Location", a.redirectTo)
		w.WriteHeader(http.StatusFound)
		return
	}
	a.mu.Lock()
	o, found := a.orders[r.PathValue("id")]
	allowed := found && (o.Owner == user || a.on(OtherUserReads))
	var snapshot order
	if allowed {
		a.settleOnReadLocked(o)
		snapshot = *o
	}
	a.mu.Unlock()
	switch {
	case !found:
		fail(w, http.StatusNotFound, "NOT_FOUND")
	case !allowed:
		fail(w, http.StatusForbidden, "FORBIDDEN")
	default:
		write(w, http.StatusOK, map[string]any{"data": snapshot})
	}
}

// settleOnReadLocked counts a read of an order that has an unsettled payment. The read that
// reaches the payment's limit settles it, so a caller that polls needs no clock.
func (a *App) settleOnReadLocked(o *order) {
	p := a.pending[o.ID]
	if p == nil || p.neverSettles {
		return
	}
	p.reads++
	if p.reads < p.settleAfter {
		return
	}
	delete(a.pending, o.ID)
	p.Status = paymentSettled
	if o.Status == StatusCreated {
		o.Status = StatusPaid
	}
}

// listOrders gives the orders of the caller, oldest first, optionally filtered by status and customer.
func (a *App) listOrders(w http.ResponseWriter, r *http.Request) {
	user, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	status, customer := r.URL.Query().Get("status"), r.URL.Query().Get("customer")
	out := []order{}
	a.mu.Lock()
	for _, o := range a.orderList {
		if o.Owner == user && (status == "" || o.Status == status) && (customer == "" || o.Customer == customer) {
			out = append(out, *o)
		}
	}
	a.mu.Unlock()
	write(w, http.StatusOK, map[string]any{"data": out})
}

func (a *App) cancelOrder(w http.ResponseWriter, r *http.Request) {
	user, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	if a.on(CancelFails) {
		fail(w, http.StatusInternalServerError, "INTERNAL")
		return
	}
	a.mu.Lock()
	o, found := a.orders[r.PathValue("id")]
	switch {
	case !found:
		a.mu.Unlock()
		fail(w, http.StatusNotFound, "NOT_FOUND")
		return
	case o.Owner != user:
		a.mu.Unlock()
		fail(w, http.StatusForbidden, "FORBIDDEN")
		return
	case o.Status == StatusCancelled:
		a.mu.Unlock()
		fail(w, http.StatusConflict, "ALREADY_CANCELLED")
		return
	}
	o.Status = StatusCancelled
	delete(a.pending, o.ID)
	snapshot := *o
	a.mu.Unlock()
	write(w, http.StatusOK, map[string]any{"data": snapshot})
}

func (a *App) createRefund(w http.ResponseWriter, r *http.Request) {
	user, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	if a.on(RefundServerError) {
		fail(w, http.StatusInternalServerError, "INTERNAL")
		return
	}
	amount, validAmount := readAmount(w, r)

	a.mu.Lock()
	o, found := a.orders[r.PathValue("id")]
	switch {
	case !found:
		a.mu.Unlock()
		fail(w, http.StatusNotFound, "NOT_FOUND")
		return
	case o.Owner != user:
		a.mu.Unlock()
		fail(w, http.StatusForbidden, "FORBIDDEN")
		return
	case !validAmount:
		a.mu.Unlock()
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	case amount > o.Amount-o.RefundedTotal && !a.on(RefundOverTotal):
		a.mu.Unlock()
		fail(w, http.StatusUnprocessableEntity, "REFUND_EXCEEDS_TOTAL")
		return
	}
	o.RefundedTotal += amount
	a.refundCount++
	rf := refund{ID: "r-" + strconv.Itoa(a.refundCount), Amount: amount, OrderID: o.ID}
	a.mu.Unlock()

	if a.on(SlowRefund) && !sleep(r.Context(), a.delay) {
		return
	}
	write(w, http.StatusCreated, map[string]any{"data": rf})
}

// sleep reports false when ctx ended before d passed.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
