// Package webdriver is a small client of the W3C WebDriver protocol over HTTP and JSON. It knows no
// platform: Android's UiAutomator2 server, Appium, a device farm, and (later) WebDriverAgent on iOS
// all speak what it sends. It uses only the standard library.
//
// It never uses find-element. The bare UiAutomator2 server takes {strategy, selector} where Appium and
// WebDriverAgent take {using, value}, so Semar reads the page source and matches the locators itself.
// The error text is the first line of what the server said, and never holds what was sent, because a
// request may carry text that a person typed.
package webdriver

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
	"regexp"
	"strings"
	"time"
)

const (
	// DefaultMaxResponse is the most that a server may answer with: a screenshot of a large screen is
	// a few megabytes of base64.
	DefaultMaxResponse = 32 << 20
	// maxMessage is how much of the message of an error is kept.
	maxMessage = 300
)

// Client talks to one server. The zero value is not usable: use New.
type Client struct {
	// Base is the address of the server, such as http://127.0.0.1:4723, without a trailing slash.
	Base string
	HTTP *http.Client
	// User and Key are sent as HTTP basic authentication when User is not empty.
	User, Key string
	// MaxResponse bounds what is read of an answer. Zero means DefaultMaxResponse.
	MaxResponse int64
}

// New gives a client whose HTTP client never goes through a proxy for a server on this machine, never
// follows a redirect (a redirect would carry the credentials to another host), and gives up on a
// connection that does not answer.
func New(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: NewHTTPClient(http.ProxyFromEnvironment)}
}

// NewHTTPClient is the HTTP client of New. fallback chooses the proxy of a request to a host that is
// not on this machine.
func NewHTTPClient(fallback func(*http.Request) (*url.URL, error)) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 proxyFor(fallback),
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          2,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 2 * time.Minute,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       5 * time.Minute,
	}
}

func proxyFor(fallback func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(r *http.Request) (*url.URL, error) {
		if isLoopback(r.URL.Hostname()) || fallback == nil {
			return nil, nil
		}
		return fallback(r)
	}
}

func isLoopback(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Error is an answer of a server that says no.
type Error struct {
	// Status is the HTTP status.
	Status int
	// Code is the W3C error code, such as "no such element", or "" when the server gave none.
	Code string
	// Message is the first line of the message of the server, cut short. It is "" for a request whose
	// body may hold a secret.
	Message string
}

func (e *Error) Error() string {
	s := fmt.Sprintf("the server answered %d", e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// envelope is what every W3C answer wraps its value in.
type envelope struct {
	Value json.RawMessage `json:"value"`
	// SessionID is where a legacy (JSONWP) server puts the id of a new session.
	SessionID string `json:"sessionId"`
}

type failure struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// call sends one request and gives the value of the answer. A body is JSON. hush says that the body
// carries text that a person typed, so the message of an error of the server is left out.
func (c *Client) call(ctx context.Context, method, path string, body any, hush bool) (json.RawMessage, *envelope, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("the request could not be encoded: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return nil, nil, fmt.Errorf("the request could not be made: %w", urlErrorFree(err))
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if c.User != "" {
		req.SetBasicAuth(c.User, c.Key)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, fmt.Errorf("the server could not be reached: %w", urlErrorFree(err))
	}
	defer resp.Body.Close()
	limit := c.MaxResponse
	if limit <= 0 {
		limit = DefaultMaxResponse
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, fmt.Errorf("the answer of the server could not be read: %w", urlErrorFree(err))
	}
	if int64(len(data)) > limit {
		return nil, nil, fmt.Errorf("the answer of the server is larger than %d bytes", limit)
	}
	var env envelope
	jsonErr := json.Unmarshal(data, &env)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		e := &Error{Status: resp.StatusCode}
		var f failure
		if jsonErr == nil && json.Unmarshal(env.Value, &f) == nil {
			e.Code = cleanLine(f.Error, 80)
			if !hush {
				e.Message = cleanLine(f.Message, maxMessage)
			}
		}
		return nil, nil, e
	}
	if jsonErr != nil {
		return nil, nil, errors.New("the server did not answer with JSON")
	}
	// A W3C error can also come with a 200 from a loose server: {"value": {"error": ...}}.
	var f failure
	if len(env.Value) > 0 && env.Value[0] == '{' && json.Unmarshal(env.Value, &f) == nil && f.Error != "" && f.Message != "" {
		e := &Error{Status: resp.StatusCode, Code: cleanLine(f.Error, 80)}
		if !hush {
			e.Message = cleanLine(f.Message, maxMessage)
		}
		return nil, nil, e
	}
	return env.Value, &env, nil
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// urlErrorFree takes the URL out of an error of the HTTP client: the URL of a session is not a secret,
// but the error of a request must never be longer than what a person needs to read.
func urlErrorFree(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// cleanLine is the first line of s, with control characters left out, cut at max bytes.
func cleanLine(s string, max int) string {
	first, _, _ := strings.Cut(s, "\n")
	first = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(first))
	if len(first) > max {
		first = strings.ToValidUTF8(first[:max], "") + "..."
	}
	return first
}

// Ready is what a server says of itself.
type Ready struct {
	Ready   bool
	Message string
}

// Status asks whether the server is up: GET /status. A server that gives no ready flag is ready when
// it answers.
func (c *Client) Status(ctx context.Context) (Ready, error) {
	v, _, err := c.call(ctx, http.MethodGet, "/status", nil, false)
	if err != nil {
		return Ready{}, err
	}
	out := Ready{Ready: true}
	var s struct {
		Ready   *bool  `json:"ready"`
		Message string `json:"message"`
	}
	if json.Unmarshal(v, &s) == nil {
		if s.Ready != nil {
			out.Ready = *s.Ready
		}
		out.Message = cleanLine(s.Message, maxMessage)
	}
	return out, nil
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,256}$`)

// Elements are named by this key in the W3C protocol, and by ELEMENT in the legacy one.
const (
	w3cElementKey    = "element-6066-11e4-a52e-4f735466cecf"
	legacyElementKey = "ELEMENT"
)
