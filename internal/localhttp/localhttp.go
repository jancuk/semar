// Package localhttp is what the pages that Semar serves on the person's own machine share: the
// dashboard and the recorder studio.
//
// Another page in the same browser can send requests to 127.0.0.1, and a host name it controls can be
// made to point there. So every API call needs a random token that only the printed address carries, a
// Host header of the loopback address and port, and, when the browser sends an Origin, that same
// origin. No CORS header is ever sent.
package localhttp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
)

// TokenHeader carries the token on every API call.
const TokenHeader = "X-Semar-Token"

// MaxBody is the default limit of the body of a change.
const MaxBody = 64 << 10

// CSP is the content security policy of every answer: scripts, styles, and connections only from the
// page's own origin, and no frame.
const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// NewToken makes a random token of 64 hex characters.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make the token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ErrorBody is the body of every error answer.
type ErrorBody struct {
	Error string `json:"error"`
}

// GuardAll sets the headers of every answer and refuses a Host other than the loopback address and the
// port that the request arrived on, which defeats DNS rebinding. csp is the content security policy,
// and name says what is served, such as "dashboard", in the message.
func GuardAll(name, csp string, next http.Handler) http.Handler {
	if csp == "" {
		csp = CSP
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		if !HostAllowed(r) {
			Fail(w, http.StatusForbidden, "The Host header must be 127.0.0.1 or localhost with the port of the "+name+".")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// HostAllowed reports whether the Host header is the loopback address (127.0.0.1 or [::1]) or
// localhost with the port that the request arrived on.
func HostAllowed(r *http.Request) bool {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	return r.Host == "127.0.0.1:"+port || r.Host == "localhost:"+port || r.Host == "[::1]:"+port
}

// BearerOrHeader returns the token of an "Authorization: Bearer <token>" header, or else the
// X-Semar-Token header, or "" when neither is set.
func BearerOrHeader(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); len(auth) > len("Bearer ") && strings.EqualFold(auth[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(auth[len("Bearer "):])
	}
	return r.Header.Get(TokenHeader)
}

// TokenEqual compares two tokens in constant time. An empty token never matches, not even another
// empty one.
func TokenEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// GuardAPI checks the token and the origin, and that a change is a small JSON body of at most
// maxBody bytes. An empty Origin is allowed: a command line client sends none. A browser page of
// another origin always sends one.
func GuardAPI(name, command, token string, maxBody int64, next http.Handler) http.Handler {
	if maxBody <= 0 {
		maxBody = MaxBody
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			Fail(w, http.StatusForbidden, "The request comes from another page.")
			return
		}
		got := r.Header.Get(TokenHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			Fail(w, http.StatusForbidden, "The "+name+" token is missing or wrong. Open the address that "+command+" printed.")
			return
		}
		if r.Method != http.MethodGet {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				Fail(w, http.StatusUnsupportedMediaType, "A change must be sent as application/json.")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		next.ServeHTTP(w, r)
	})
}

// Allow dispatches on the method and refuses every other one, HEAD included.
func Allow(methods map[string]http.HandlerFunc) http.Handler {
	names := make([]string, 0, len(methods))
	for m := range methods {
		names = append(names, m)
	}
	allowed := strings.Join(names, ", ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, ok := methods[r.Method]
		if !ok {
			w.Header().Set("Allow", allowed)
			Fail(w, http.StatusMethodNotAllowed, "This method is not allowed here.")
			return
		}
		h(w, r)
	})
}

// Fail answers with an error body.
func Fail(w http.ResponseWriter, code int, msg string) {
	Reply(w, code, ErrorBody{Error: msg})
}

// Reply answers with v as JSON.
func Reply(w http.ResponseWriter, code int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		code, data = http.StatusInternalServerError, []byte(`{"error":"The answer could not be encoded."}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(append(data, '\n'))
}

// Decode reads one JSON value into v and refuses unknown fields, a second value, and a body over
// the limit of maxBody bytes that GuardAPI set. An empty body is allowed only when empty is true.
func Decode(w http.ResponseWriter, r *http.Request, v any, empty bool, maxBody int64) bool {
	if maxBody <= 0 {
		maxBody = MaxBody
	}
	data, err := io.ReadAll(r.Body)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		Fail(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("The request is larger than %d KiB.", maxBody>>10))
		return false
	case err != nil:
		Fail(w, http.StatusBadRequest, "The request could not be read.")
		return false
	case len(strings.TrimSpace(string(data))) == 0:
		if empty {
			return true
		}
		Fail(w, http.StatusBadRequest, "The request has no body.")
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		Fail(w, http.StatusBadRequest, "The request is not valid: "+jsonProblem(err))
		return false
	}
	if dec.More() {
		Fail(w, http.StatusBadRequest, "The request holds more than one JSON value.")
		return false
	}
	return true
}

// jsonProblem names an unknown field or a wrong type, and never quotes a value.
func jsonProblem(err error) string {
	var typeErr *json.UnmarshalTypeError
	switch msg := err.Error(); {
	case strings.HasPrefix(msg, "json: unknown field "):
		return "the field " + strings.TrimPrefix(msg, "json: unknown field ") + " is not known."
	case errors.As(err, &typeErr):
		return fmt.Sprintf("the field %s has the wrong type.", typeErr.Field)
	default:
		return "it is not one JSON object."
	}
}

// Serve answers on ln until ctx ends, and lets the requests in flight finish for a few seconds. The
// stop function, when not nil, runs once the server is told to end or fails, before the shutdown.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, stop func()) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       time.Minute,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		if stop != nil {
			stop()
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	if stop != nil {
		stop()
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
