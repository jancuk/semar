// Package dashboard serves the page on the person's own machine that shows the model
// providers of providers.yaml, changes them, signs a harness in, and shows usage.
//
// Another page in the same browser can send requests to 127.0.0.1, and a host name it
// controls can be made to point there. So every API call needs a random token that only the
// printed address carries, a Host header of the loopback address and port, and, when the
// browser sends an Origin, that same origin. No CORS header is ever sent.
package dashboard

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
	"sync"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/provider"
)

// TokenHeader carries the token on every API call.
const TokenHeader = "X-Semar-Token"

const (
	maxBody      = 64 << 10
	checkTimeout = 6 * time.Second
	loginLimit   = 5 * time.Minute
	contentCSP   = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
)

// Deps are what the server takes from the process. Tests replace each of them.
type Deps struct {
	Home        string                                           // the directory of providers.yaml and usage.jsonl
	NewInstance func(config.Provider) (provider.Instance, error) // builds a provider for health and account status
	Keys        keychain.Store
	LookupEnv   func(string) (string, bool)
	Environ     func() []string
	Now         func() time.Time
}

type Server struct {
	d       Deps
	token   string
	handler http.Handler

	// mu guards load, change, and save of providers.yaml.
	mu sync.Mutex

	checkTimeout time.Duration
	loginLimit   time.Duration
	logins       logins
}

// New makes a server with a new random token.
func New(d Deps) (*Server, error) {
	switch {
	case d.Home == "":
		return nil, errors.New("the dashboard needs the directory of the providers file")
	case d.NewInstance == nil, d.Keys == nil, d.LookupEnv == nil, d.Environ == nil, d.Now == nil:
		return nil, errors.New("the dashboard is missing one of its dependencies")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("make the dashboard token: %w", err)
	}
	s := &Server{d: d, token: hex.EncodeToString(b), checkTimeout: checkTimeout, loginLimit: loginLimit}
	s.logins.init()
	s.handler = s.routes()
	return s, nil
}

func (s *Server) Token() string { return s.token }

func (s *Server) Handler() http.Handler { return s.handler }

// Serve answers on ln until ctx ends. Then it ends every running sign-in job and waits for
// it, and lets the requests in flight finish for a few seconds.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       time.Minute,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		s.logins.stop()
		return err
	case <-ctx.Done():
	}
	s.logins.stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/{$}", allow(map[string]http.HandlerFunc{"GET": page("index.html")}))
	mux.Handle("/app.js", allow(map[string]http.HandlerFunc{"GET": page("app.js")}))
	mux.Handle("/style.css", allow(map[string]http.HandlerFunc{"GET": page("style.css")}))

	api := http.NewServeMux()
	api.Handle("/api/state", allow(map[string]http.HandlerFunc{"GET": s.getState}))
	api.Handle("/api/providers/{id}", allow(map[string]http.HandlerFunc{"PUT": s.putProvider, "DELETE": s.deleteProvider}))
	api.Handle("/api/providers/{id}/key", allow(map[string]http.HandlerFunc{"PUT": s.putKey}))
	api.Handle("/api/providers/{id}/login", allow(map[string]http.HandlerFunc{"POST": s.postLogin}))
	api.Handle("/api/default", allow(map[string]http.HandlerFunc{"PUT": s.putDefault}))
	api.Handle("/api/logins/{job}", allow(map[string]http.HandlerFunc{"GET": s.getLogin, "DELETE": s.deleteLogin}))
	api.Handle("/api/usage", allow(map[string]http.HandlerFunc{"GET": s.getUsage}))
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotFound, "There is no such API path.")
	})
	mux.Handle("/api/", s.guardAPI(api))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not found.", http.StatusNotFound)
	})
	return s.guardAll(mux)
}

// guardAll sets the headers of every answer and refuses a Host other than the loopback
// address and the port that the request arrived on, which defeats DNS rebinding.
func (s *Server) guardAll(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", contentCSP)
		h.Set("X-Frame-Options", "DENY")
		if !hostAllowed(r) {
			fail(w, http.StatusForbidden, "The Host header must be 127.0.0.1 or localhost with the port of the dashboard.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hostAllowed(r *http.Request) bool {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	return r.Host == "127.0.0.1:"+port || r.Host == "localhost:"+port
}

// guardAPI checks the token and the origin, and that a change is a small JSON body.
func (s *Server) guardAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			fail(w, http.StatusForbidden, "The request comes from another page.")
			return
		}
		got := r.Header.Get(TokenHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			fail(w, http.StatusForbidden, "The dashboard token is missing or wrong. Open the address that semar dashboard printed.")
			return
		}
		if r.Method != http.MethodGet {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				fail(w, http.StatusUnsupportedMediaType, "A change must be sent as application/json.")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		next.ServeHTTP(w, r)
	})
}

// allow dispatches on the method and refuses every other one, HEAD included.
func allow(methods map[string]http.HandlerFunc) http.Handler {
	names := make([]string, 0, len(methods))
	for m := range methods {
		names = append(names, m)
	}
	allowed := strings.Join(names, ", ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, ok := methods[r.Method]
		if !ok {
			w.Header().Set("Allow", allowed)
			fail(w, http.StatusMethodNotAllowed, "This method is not allowed here.")
			return
		}
		h(w, r)
	})
}

type errorBody struct {
	Error string `json:"error"`
}

func fail(w http.ResponseWriter, code int, msg string) {
	reply(w, code, errorBody{Error: msg})
}

func reply(w http.ResponseWriter, code int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		code, data = http.StatusInternalServerError, []byte(`{"error":"The answer could not be encoded."}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(append(data, '\n'))
}

// decode reads one JSON value into v and refuses unknown fields, a second value, and a body
// over the limit. An empty body is allowed only when empty is true.
func decode(w http.ResponseWriter, r *http.Request, v any, empty bool) bool {
	data, err := io.ReadAll(r.Body)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		fail(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("The request is larger than %d KiB.", maxBody>>10))
		return false
	case err != nil:
		fail(w, http.StatusBadRequest, "The request could not be read.")
		return false
	case len(strings.TrimSpace(string(data))) == 0:
		if empty {
			return true
		}
		fail(w, http.StatusBadRequest, "The request has no body.")
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "The request is not valid: "+jsonProblem(err))
		return false
	}
	if dec.More() {
		fail(w, http.StatusBadRequest, "The request holds more than one JSON value.")
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
