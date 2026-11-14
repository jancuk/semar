// Package api is the one place that sends HTTP requests to a test
// environment. Every request is admitted by the effects policy first and
// recorded afterwards, whatever its outcome.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/recorder"
)

const (
	defaultTimeout = 30 * time.Second
	defaultMaxBody = 1 << 20
	defaultBudget  = 200
	maxRedirects   = 5

	refusedNoUseCase = "a write must belong to a scenario step, an exploration, or a probe"
	refusedBudget    = "request budget reached"
)

type Options struct {
	Policy   *effects.Policy
	Recorder *recorder.Recorder
	Services map[string]string // service name to base URL
	Session  func(ctx context.Context, authProfile string) (header, value string, err error)
	Run      domain.RunID
	Version  string
	Timeout  time.Duration // default 30s
	MaxBody  int64         // default 1 MiB
	Budget   int           // default 200 requests per engine
	Clock    func() time.Time
	TraceID  func() string     // 32 lower-case hex characters
	SpanID   func() string     // 16 lower-case hex characters
	HTTP     http.RoundTripper // default: a transport that never reuses a connection
	Logger   *slog.Logger      // default: discards
}

type Call struct {
	Use          domain.UseCase
	By           domain.Actor
	Service      string
	Method       string
	Path         string // already rendered
	PathTemplate string
	Query        map[string]string
	Headers      map[string]string
	JSON         []byte
	AuthProfile  string
	Why          string
	Captures     []domain.Capture
}

type Result struct {
	Action   domain.Action     // redacted, as stored
	Captured map[string]string // raw values, for later requests only. Never store them
	Missing  []string          // capture names that gave no value
}

type Engine struct {
	o      Options
	client *http.Client

	mu   sync.Mutex
	sent int
}

func New(o Options) *Engine {
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	if o.MaxBody <= 0 {
		o.MaxBody = defaultMaxBody
	}
	if o.Budget <= 0 {
		o.Budget = defaultBudget
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.TraceID == nil {
		o.TraceID = func() string { return randomHex(16) }
	}
	if o.SpanID == nil {
		o.SpanID = func() string { return randomHex(8) }
	}
	if o.HTTP == nil {
		// http.Transport resends an idempotent request when a reused connection
		// fails, and the server may already have acted on the first one. A
		// connection that is never reused is never retried.
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableKeepAlives = true
		o.HTTP = t
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	e := &Engine{o: o}
	e.client = &http.Client{Transport: o.HTTP, Timeout: o.Timeout, CheckRedirect: e.checkRedirect}
	return e
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// flight holds what CheckRedirect learns about one call. CheckRedirect runs in
// the goroutine that called client.Do, so it needs no lock.
type flight struct {
	refused string
	err     string
}

type flightKey struct{}

// Do never returns an error for a refusal, a timeout, or a failed request:
// those are in Result.Action. It returns an error for an unknown service, a
// recorder failure, or a context that was cancelled before or during the call.
func (e *Engine) Do(ctx context.Context, c Call) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("api %s %s: %w", c.Method, c.Path, err)
	}
	base, ok := e.o.Services[c.Service]
	if !ok {
		return Result{}, fmt.Errorf("api: unknown service %q", c.Service)
	}
	method := strings.ToUpper(c.Method)
	a := domain.Action{
		Surface: domain.SurfaceAPI,
		Op:      "request",
		Use:     c.Use,
		By:      c.By,
		TraceID: e.o.TraceID(),
		At:      e.o.Clock(),
		Why:     c.Why,
		Request: &domain.HTTPRequest{Method: method, URL: base + c.Path, PathTemplate: c.PathTemplate, Body: domain.Body(c.JSON)},
	}
	missing := captureNames(c.Captures)

	u, err := joinURL(base, c.Path, c.Query)
	if err != nil {
		a.Err = err.Error()
		return e.record(ctx, a, nil, missing)
	}
	a.Request.URL = u.String()

	preq := effects.Request{Method: method, URL: u}
	if err := e.o.Policy.Admit(preq); err != nil {
		a.Refused = err.Error()
		return e.record(ctx, a, nil, missing)
	}
	write := e.o.Policy.Classify(preq) == effects.Write
	if write && c.Use.Pass == "" {
		a.Refused = refusedNoUseCase
		return e.record(ctx, a, nil, missing)
	}

	var session [2]string
	if c.AuthProfile != "" {
		if e.o.Session == nil {
			a.Err = "sign-in failed: no session source"
			return e.record(ctx, a, nil, missing)
		}
		name, value, err := e.o.Session(ctx, c.AuthProfile)
		if err != nil {
			a.Err = "sign-in failed: " + err.Error()
			return e.record(ctx, a, nil, missing)
		}
		session = [2]string{name, value}
	}

	var reqBody io.Reader
	if len(c.JSON) > 0 {
		reqBody = bytes.NewReader(c.JSON)
	}
	f := &flight{}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, flightKey{}, f), method, u.String(), reqBody)
	if err != nil {
		a.Err = err.Error()
		return e.record(ctx, a, nil, missing)
	}
	req.Header = e.headers(ctx, a.TraceID, c, session)
	a.Request.Headers = req.Header.Clone()

	if !e.take() {
		a.Refused = refusedBudget
		return e.record(ctx, a, nil, missing)
	}

	start := e.o.Clock()
	resp, err := e.client.Do(req)
	if err != nil {
		a.Err = err.Error()
		a.EffectUnknown = write && (isTimeout(err) || ctx.Err() != nil)
		a.DurationMS = e.o.Clock().Sub(start).Milliseconds()
		return e.record(ctx, a, nil, missing)
	}
	body, truncated, readErr := readBody(resp.Body, e.o.MaxBody)
	_ = resp.Body.Close()
	a.DurationMS = e.o.Clock().Sub(start).Milliseconds()
	a.Response = &domain.HTTPResponse{Status: resp.StatusCode, Headers: resp.Header.Clone(), Body: domain.Body(body), Truncated: truncated}
	a.Refused, a.Err = f.refused, f.err
	if readErr != nil {
		a.Err = "read response body: " + readErr.Error()
		a.EffectUnknown = write && (isTimeout(readErr) || ctx.Err() != nil)
	}
	if a.Refused != "" || a.Err != "" || truncated {
		return e.record(ctx, a, nil, missing)
	}
	captured, missing := capture(body, c.Captures)
	return e.record(ctx, a, captured, missing)
}

// record ends every path through Do that does not return early with an error.
func (e *Engine) record(ctx context.Context, a domain.Action, captured map[string]string, missing []string) (Result, error) {
	if captured == nil {
		captured = map[string]string{}
	}
	if len(captured) > 0 {
		a.Captures = maps.Clone(captured)
	}
	stored, err := e.o.Recorder.Record(a)
	if err != nil {
		return Result{}, fmt.Errorf("api %s %s: %w", a.Request.Method, a.Request.URL, err)
	}
	res := Result{Action: stored, Captured: captured, Missing: missing}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("api %s %s: %w", stored.Request.Method, stored.Request.URL, err)
	}
	return res, nil
}

func (e *Engine) checkRedirect(req *http.Request, via []*http.Request) error {
	f, _ := req.Context().Value(flightKey{}).(*flight)
	if f == nil {
		return errors.New("api: redirect outside a call")
	}
	if len(via) > maxRedirects {
		f.err = fmt.Sprintf("stopped after %d redirects", maxRedirects)
		return http.ErrUseLastResponse
	}
	if err := e.o.Policy.Admit(effects.Request{Method: req.Method, URL: req.URL}); err != nil {
		var r *effects.Refusal
		if errors.As(err, &r) && r.Kind == effects.HostNotAllowed {
			f.refused = "redirect to a host that is not allowed: " + err.Error()
		} else {
			f.refused = "redirect refused: " + err.Error()
		}
		return http.ErrUseLastResponse
	}
	if !e.take() {
		f.refused = refusedBudget
		return http.ErrUseLastResponse
	}
	return nil
}

// take reserves one request of the budget. A reserved request is counted as
// sent even when the transport then fails, because it may have reached the server.
func (e *Engine) take() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sent >= e.o.Budget {
		return false
	}
	e.sent++
	return true
}

func (e *Engine) headers(ctx context.Context, traceID string, c Call, session [2]string) http.Header {
	h := http.Header{}
	h.Set("traceparent", "00-"+traceID+"-"+e.o.SpanID()+"-01")
	h.Set("baggage", "semar.run="+string(e.o.Run))
	h.Set("User-Agent", "Semar/"+e.o.Version)
	h.Set("X-Semar-Run", string(e.o.Run))
	if session[0] != "" {
		h.Set(session[0], session[1])
	}
	reserved := slices.Collect(maps.Keys(h))
	for _, name := range slices.Sorted(maps.Keys(c.Headers)) {
		if slices.Contains(reserved, http.CanonicalHeaderKey(name)) {
			e.o.Logger.DebugContext(ctx, "ignored a call header that the engine sets", "header", name)
			continue
		}
		h.Add(name, c.Headers[name])
	}
	if len(c.JSON) > 0 && h.Get("Content-Type") == "" {
		h.Set("Content-Type", "application/json")
	}
	return h
}

// joinURL keeps the path prefix of the base URL. The path is parsed so that
// its escaping reaches the policy and the server as written.
func joinURL(base, path string, query map[string]string) (*url.URL, error) {
	b, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("service base url: %w", err)
	}
	p, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("request path %q: %w", path, err)
	}
	if p.Scheme != "" || p.Host != "" || p.User != nil || p.RawQuery != "" || p.Fragment != "" || p.Opaque != "" {
		return nil, fmt.Errorf("request path %q must be a path only", path)
	}
	u := *b
	if p.Path != "" {
		u.Path = strings.TrimSuffix(b.Path, "/") + "/" + strings.TrimPrefix(p.Path, "/")
		u.RawPath = strings.TrimSuffix(b.EscapedPath(), "/") + "/" + strings.TrimPrefix(p.EscapedPath(), "/")
	}
	if len(query) > 0 {
		q := b.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}
	return &u, nil
}

func readBody(r io.Reader, limit int64) (body []byte, truncated bool, err error) {
	body, err = io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(body)) > limit {
		return body[:limit], true, err
	}
	return body, false, err
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded)
}

func captureNames(caps []domain.Capture) []string {
	names := make([]string, len(caps))
	for i, c := range caps {
		names[i] = c.Name
	}
	return names
}

// capture gives the text of each capture that selects exactly one string,
// number, or boolean. Anything else is missing.
func capture(body []byte, caps []domain.Capture) (map[string]string, []string) {
	if len(caps) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil || dec.More() {
		return nil, captureNames(caps)
	}
	captured := map[string]string{}
	var missing []string
	for _, c := range caps {
		v, ok := selectScalar(doc, c.JSONPath)
		if !ok {
			missing = append(missing, c.Name)
			continue
		}
		captured[c.Name] = v
	}
	return captured, missing
}

func selectScalar(doc any, path string) (string, bool) {
	p, err := jsonpath.Parse(path)
	if err != nil {
		return "", false
	}
	nodes := p.Select(doc)
	if len(nodes) != 1 {
		return "", false
	}
	switch v := nodes[0].(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	case bool:
		return strconv.FormatBool(v), true
	}
	return "", false
}
