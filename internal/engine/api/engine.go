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
	"net/http"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/apicheck"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/recorder"
)

const (
	defaultTimeout = 30 * time.Second
	defaultMaxBody = 1 << 20
	defaultBudget  = 200
	maxRedirects   = 5

	refusedNoUseCase       = "a write must belong to a scenario step, an exploration, or a probe"
	refusedBudget          = "request budget reached"
	refusedFixtureOnce     = "a fixture request must not match effects.once"
	refusedRedirectedWrite = "redirect refused: a write is not followed"
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
	// Doc checks each answer of a step against the API document and a time limit. Nil checks nothing.
	Doc *apicheck.Checker
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

// flight holds what one call needs and learns while it is sent. CheckRedirect
// runs in the goroutine that called client.Do, but the transport may report a
// connection from another one.
type flight struct {
	session  string // canonical name of the session header, or empty
	followed int
	refused  string
	err      string
	gotConn  atomic.Bool
}

type flightKey struct{}

// Do never returns an error for a refusal, a timeout, or a failed request:
// those are in Result.Action. It returns an error for an unknown service, a
// recorder failure, or a context that was cancelled before or during the call.
func (e *Engine) Do(ctx context.Context, c Call) (Result, error) {
	base, known := e.o.Services[c.Service]
	method := strings.ToUpper(c.Method)
	label := describe(method, c.PathTemplate, base)
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("api %s: %w", label, err)
	}
	if !known {
		return Result{}, fmt.Errorf("api: unknown service %q", c.Service)
	}
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

	u, err := JoinURL(base, c.Path, c.Query)
	if err != nil {
		a.Err = err.Error()
		return e.record(ctx, label, a, nil, missing)
	}
	a.Request.URL = u.String()

	preq := effects.Request{Method: method, URL: u}
	if err := e.o.Policy.Admit(preq); err != nil {
		a.Refused = refusalText(err, label)
		return e.record(ctx, label, a, nil, missing)
	}
	if c.Use.Fixture != nil && e.o.Policy.Once(preq) {
		// A fixture runs on every run, and once means a single time in the life of the environment.
		a.Refused = refusedFixtureOnce
		return e.record(ctx, label, a, nil, missing)
	}
	write := e.o.Policy.Classify(preq) == effects.Write
	if write && c.Use.Pass == "" {
		a.Refused = refusedNoUseCase
		return e.record(ctx, label, a, nil, missing)
	}

	var session [2]string
	if c.AuthProfile != "" {
		if e.o.Session == nil {
			a.Err = "sign-in failed: the engine has no session source"
			return e.record(ctx, label, a, nil, missing)
		}
		name, value, err := e.o.Session(ctx, c.AuthProfile)
		if err != nil {
			a.Err = "sign-in failed: " + err.Error()
			return e.record(ctx, label, a, nil, missing)
		}
		session = [2]string{name, value}
	}

	var reqBody io.Reader
	if len(c.JSON) > 0 {
		reqBody = bytes.NewReader(c.JSON)
	}
	f := &flight{session: http.CanonicalHeaderKey(session[0])}
	reqCtx := httptrace.WithClientTrace(context.WithValue(ctx, flightKey{}, f), &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { f.gotConn.Store(true) },
	})
	req, err := http.NewRequestWithContext(reqCtx, method, u.String(), reqBody)
	if err != nil {
		a.Err = "the request could not be built"
		return e.record(ctx, label, a, nil, missing)
	}
	req.Header = e.headers(ctx, a.TraceID, c, session)
	a.Request.Headers = req.Header.Clone()
	if f.session != "" {
		a.Request.Headers[f.session] = []string{"[session of " + c.AuthProfile + "]"}
	}

	if !e.take() {
		a.Refused = refusedBudget
		return e.record(ctx, label, a, nil, missing)
	}

	start := e.o.Clock()
	// Once a connection is obtained, the server may have acted on a write
	// whatever the error, so its effect is unknown.
	resp, err := e.client.Do(req)
	if err != nil {
		a.Err = transportText(err, u, label)
		a.EffectUnknown = write && f.gotConn.Load()
		a.DurationMS = e.o.Clock().Sub(start).Milliseconds()
		return e.record(ctx, label, a, nil, missing)
	}
	body, truncated, readErr := readBody(resp.Body, e.o.MaxBody)
	_ = resp.Body.Close()
	a.DurationMS = e.o.Clock().Sub(start).Milliseconds()
	a.Response = &domain.HTTPResponse{Status: resp.StatusCode, Headers: resp.Header.Clone(), Body: domain.Body(body), Truncated: truncated}
	if final := resp.Request.URL.String(); f.followed > 0 && final != a.Request.URL {
		a.Response.URL = final
	}
	a.Refused, a.Err = f.refused, f.err
	if readErr != nil {
		msg := "read response body: " + readErr.Error()
		if a.Err != "" {
			msg = a.Err + "; " + msg
		}
		a.Err = msg
		a.EffectUnknown = write && f.gotConn.Load()
	}
	if a.Refused == "" && a.Err == "" && a.Response.URL == "" && c.Use.Fixture == nil {
		a.Doc = e.o.Doc.Check(apicheck.Response{
			Service: c.Service, Method: method, Path: c.Path, FullPath: u.Path,
			Status: resp.StatusCode, Body: body, Truncated: truncated, DurationMS: a.DurationMS,
		})
	}
	if a.Refused != "" || a.Err != "" || truncated {
		return e.record(ctx, label, a, nil, missing)
	}
	captured, missing := capture(body, c.Captures)
	return e.record(ctx, label, a, captured, missing)
}

// record ends every path through Do that does not return early with an error.
// label names the request in an error. It never holds the rendered path, the query, or a value.
func (e *Engine) record(ctx context.Context, label string, a domain.Action, captured map[string]string, missing []string) (Result, error) {
	if captured == nil {
		captured = map[string]string{}
	}
	if len(captured) > 0 {
		a.Captures = maps.Clone(captured)
	}
	stored, err := e.o.Recorder.Record(a)
	if err != nil {
		return Result{}, fmt.Errorf("api %s: %w", label, err)
	}
	res := Result{Action: stored, Captured: captured, Missing: missing}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("api %s: %w", label, err)
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
	// A write is sent only from a call that a person or a scenario made, never
	// because a server asked for it.
	target := effects.Request{Method: req.Method, URL: req.URL}
	if e.o.Policy.Classify(target) == effects.Write {
		f.refused = refusedRedirectedWrite
		return http.ErrUseLastResponse
	}
	if err := e.o.Policy.Admit(target); err != nil {
		var r *effects.Refusal
		if errors.As(err, &r) && r.Kind == effects.HostNotAllowed {
			f.refused = "redirect to a host that is not allowed: " + refusalText(err, redirectLabel(req))
		} else {
			f.refused = "redirect refused: " + refusalText(err, redirectLabel(req))
		}
		return http.ErrUseLastResponse
	}
	if !e.take() {
		f.refused = refusedBudget
		return http.ErrUseLastResponse
	}
	if f.session != "" && !sameHost(via[0].URL, req.URL) {
		req.Header.Del(f.session)
	}
	f.followed++
	return nil
}

// sameHost compares host and port. A URL without a port has its scheme's default.
func sameHost(a, b *url.URL) bool {
	return strings.EqualFold(a.Hostname(), b.Hostname()) && portOf(a) == portOf(b)
}

func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// take counts a request as soon as the engine starts to send it, so a dial failure also uses a slot: over-counting is the safe direction.
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

// describe names a request by its method and the step's path template, or by the host of the
// service when the call has no template. A template holds {{variables}} and not their values.
func describe(method, template, base string) string {
	if template != "" {
		return method + " " + template
	}
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return method + " " + u.Host
	}
	return method + " (the service)"
}

func redirectLabel(req *http.Request) string {
	return strings.ToUpper(req.Method) + " " + req.URL.Host
}

// refusalText keeps the kind and the fixed reason of a policy refusal. The policy
// quotes the path of the request in some of its reasons, and the path can hold a value.
func refusalText(err error, label string) string {
	var r *effects.Refusal
	if !errors.As(err, &r) {
		return "the effects policy refused " + label
	}
	switch r.Kind {
	case effects.HostNotAllowed:
		return r.Error()
	case effects.ReadOnly:
		return string(r.Kind) + ": " + strings.ToLower(label) + " writes, and the environment is read only"
	case effects.MethodNotAllowed:
		return string(r.Kind) + ": the method of " + strings.ToLower(label) + " is not in the environment methods"
	default:
		return string(r.Kind) + ": " + strings.ToLower(label) + " is not allowed: its path is not normal or it matches the deny list"
	}
}

// transportText is the error of net/http without the URL that it quotes. The URL holds the
// rendered path and the query.
func transportText(err error, u *url.URL, label string) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		inner := ue.Err
		for {
			var nested *url.Error
			if !errors.As(inner, &nested) {
				break
			}
			inner = nested.Err
		}
		err = fmt.Errorf("%s: %w", ue.Op, inner)
	}
	text := err.Error()
	for _, raw := range []string{u.String(), u.RequestURI(), u.RawQuery} {
		if raw != "" {
			text = strings.ReplaceAll(text, raw, label)
		}
	}
	return text
}

// JoinURL keeps the path prefix of the base URL. The path is parsed so that
// its escaping reaches the policy and the server as written. A caller that
// classifies a request before it sends one builds the URL with this function,
// so that it judges the same URL that Do sends.
func JoinURL(base, path string, query map[string]string) (*url.URL, error) {
	b, err := url.Parse(base)
	if err != nil {
		return nil, errors.New("the service base url does not parse")
	}
	p, err := url.Parse(path)
	if err != nil {
		return nil, errors.New("request path does not parse")
	}
	if p.Scheme != "" || p.Host != "" || p.User != nil || p.RawQuery != "" || p.Fragment != "" || p.Opaque != "" {
		return nil, errors.New("request path must be a path only")
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
