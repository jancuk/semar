// Package httpapi is what the model API adapters share: the rules for the address and the key
// of a provider, and one POST with the error classes, one retry, and size limits.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

const (
	MaxResponseBytes = 8 << 20
	maxErrorBytes    = 64 << 10
	DefaultWait      = 2 * time.Second  // before the retry when the server names no time
	MaxWait          = 30 * time.Second // a longer Retry-After is not waited for
)

// Endpoint is the checked address and key reference of one provider.
type Endpoint struct {
	Base     *url.URL // no trailing slash
	KeyRef   string   // "" only for a loopback address
	Loopback bool
}

// ParseEndpoint applies the rules of the person's file: https, or http on a loopback host; no
// user info, query, or fragment; a key reference unless the host is a loopback address.
func ParseEndpoint(cfg config.Provider, defaultBase string) (Endpoint, error) {
	raw := cfg.BaseURL
	if raw == "" {
		raw = defaultBase
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return Endpoint{}, fmt.Errorf("the address %q is not an absolute URL", raw)
	}
	loopback := IsLoopback(u.Hostname())
	switch {
	case u.User != nil:
		return Endpoint{}, errors.New("the address must not hold a user name or a password")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return Endpoint{}, errors.New("the address must not have a query or a fragment")
	case u.Scheme == "http" && !loopback:
		return Endpoint{}, errors.New("http is allowed only for a loopback address such as localhost; use https")
	case u.Scheme != "https" && u.Scheme != "http":
		return Endpoint{}, fmt.Errorf("the address must use https, not %q", u.Scheme)
	}
	ref := strings.TrimSpace(cfg.APIKey)
	switch {
	case ref == "" && !loopback:
		return Endpoint{}, errors.New("an API key reference such as env:NAME or keychain:NAME is required for an address that is not a loopback address")
	case ref != "" && !strings.HasPrefix(ref, "env:") && !strings.HasPrefix(ref, "keychain:"):
		return Endpoint{}, errors.New("the API key must be a reference such as env:NAME or keychain:NAME, never the key itself")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return Endpoint{Base: u, KeyRef: ref, Loopback: loopback}, nil
}

func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// URL is the address of an API path below the base.
func (e Endpoint) URL(path string) string { return e.Base.String() + path }

// Key resolves the key reference. It returns "" with no error when no reference is set.
func (e Endpoint) Key(resolve func(string) (string, error)) (string, *provider.Error) {
	if e.KeyRef == "" {
		return "", nil
	}
	key, err := resolve(e.KeyRef)
	if err == nil && strings.TrimSpace(key) == "" {
		err = errors.New("it is empty")
	}
	if err != nil {
		detail := "the API key cannot be used: " + provider.Redact(err.Error())
		if key != "" {
			detail = strings.ReplaceAll(detail, key, "[redacted]")
		}
		return "", &provider.Error{Class: provider.PermissionError, Detail: provider.Truncate(detail)}
	}
	return key, nil
}

// Health says whether the provider is ready to use: the address is valid, which New already
// checked, and the key resolves. It makes no request.
func (e Endpoint) Health(resolve func(string) (string, error)) provider.Health {
	if _, perr := e.Key(resolve); perr != nil {
		return provider.Health{Version: e.Base.Host, Detail: perr.Detail}
	}
	return provider.Health{Installed: true, Version: e.Base.Host}
}

func (e Endpoint) Status(resolve func(string) (string, error)) provider.AccountStatus {
	st := provider.AccountStatus{Method: "api key"}
	if e.KeyRef == "" {
		st.SignedIn = true
		st.Detail = "a loopback address needs no key"
		return st
	}
	if _, perr := e.Key(resolve); perr != nil {
		st.Detail = perr.Detail
		return st
	}
	st.SignedIn = true
	st.Detail = "the key reference " + e.KeyRef + " resolves"
	return st
}

// Client returns a copy of c, or of a new client, that refuses every redirect, so that the key
// is sent to the configured host only.
func Client(c *http.Client) *http.Client {
	out := &http.Client{}
	if c != nil {
		*out = *c
	}
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return out
}

// Encode writes v as JSON without escaping <, >, and &, so that text the model wrote, such as
// a thinking block, goes back as the same bytes.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// Caller sends the requests of one job.
type Caller struct {
	HTTP   *http.Client // from Client
	Header http.Header  // sent with every request, the key included
	Secret string       // removed from every error detail, even when the server echoes it
	// Wait waits before the retry. Nil means a timer that the context can stop.
	Wait func(ctx context.Context, d time.Duration) error
	// DefaultWait is the wait when the server sends no Retry-After. Zero means DefaultWait.
	DefaultWait time.Duration
}

// Post sends body as JSON and returns the body of a 2xx response. It retries once after a 429
// or a 5xx. An error is a *provider.Error, or the context's error.
func (c *Caller) Post(ctx context.Context, target string, body []byte) ([]byte, error) {
	return c.send(ctx, http.MethodPost, target, body)
}

// Get is Post for a request without a body, such as the list of models.
func (c *Caller) Get(ctx context.Context, target string) ([]byte, error) {
	return c.send(ctx, http.MethodGet, target, nil)
}

func (c *Caller) send(ctx context.Context, method, target string, body []byte) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		status, header, data, err := c.do(ctx, method, target, body)
		if err != nil {
			return nil, err
		}
		if status >= 200 && status < 300 {
			return data, nil
		}
		perr := c.statusError(status, data)
		if attempt > 1 || (status != http.StatusTooManyRequests && status < 500) {
			return nil, perr
		}
		wait, ok := c.retryWait(header)
		if !ok {
			return nil, perr
		}
		if err := c.wait(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func (c *Caller) do(ctx context.Context, method, target string, body []byte) (int, http.Header, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return 0, nil, nil, &provider.Error{Class: provider.ProviderError, Detail: "the request could not be built"}
	}
	for k, v := range c.Header {
		req.Header[k] = v
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		return 0, nil, nil, &provider.Error{Class: provider.TransportError, Detail: c.clean("the API could not be reached: " + err.Error())}
	}
	defer resp.Body.Close()
	limit := int64(MaxResponseBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limit = maxErrorBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	switch {
	case err != nil && ctx.Err() != nil:
		return 0, nil, nil, ctx.Err()
	case err != nil:
		return 0, nil, nil, &provider.Error{Class: provider.TransportError, Detail: c.clean("the response could not be read: " + err.Error())}
	case int64(len(data)) > limit && limit == MaxResponseBytes:
		return 0, nil, nil, &provider.Error{Class: provider.ProviderError, Detail: "the response is larger than 8 MiB"}
	}
	if int64(len(data)) > limit {
		data = data[:limit]
	}
	return resp.StatusCode, resp.Header, data, nil
}

func (c *Caller) statusError(status int, body []byte) *provider.Error {
	class := provider.ProviderError
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		class = provider.PermissionError
	case status == http.StatusTooManyRequests || status == http.StatusPaymentRequired:
		class = provider.UsageLimit
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ProviderError, Detail: fmt.Sprintf("the API answered %d with a redirect, which is refused so that the key goes to no other address", status)}
	}
	detail := fmt.Sprintf("the API answered %d", status)
	if msg := errorMessage(body); msg != "" {
		detail += ": " + msg
	}
	return &provider.Error{Class: class, Detail: c.clean(detail)}
}

// errorMessage reads the error object that both API formats send, and otherwise the start of
// the body.
func errorMessage(body []byte) string {
	var env struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && len(env.Error) > 0 {
		var obj struct {
			Type    string `json:"type"`
			Code    any    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(env.Error, &obj) == nil {
			kind := obj.Type
			if kind == "" && obj.Code != nil {
				kind = fmt.Sprint(obj.Code)
			}
			if kind != "" && obj.Message != "" {
				return kind + ": " + obj.Message
			}
			return kind + obj.Message
		}
		var s string
		if json.Unmarshal(env.Error, &s) == nil {
			return s
		}
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}

// clean removes the key, then anything else that looks like a credential, and cuts the text.
func (c *Caller) clean(s string) string {
	if c.Secret != "" {
		s = strings.ReplaceAll(s, c.Secret, "[redacted]")
	}
	return provider.Truncate(provider.Redact(s))
}

func (c *Caller) retryWait(h http.Header) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		if c.DefaultWait > 0 {
			return c.DefaultWait, true
		}
		return DefaultWait, true
	}
	var d time.Duration
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		d = time.Duration(secs * float64(time.Second))
	} else if at, err := http.ParseTime(v); err == nil {
		d = time.Until(at)
	} else {
		return DefaultWait, true
	}
	if d > MaxWait {
		return 0, false
	}
	return max(d, 0), true
}

func (c *Caller) wait(ctx context.Context, d time.Duration) error {
	if c.Wait != nil {
		return c.Wait(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
