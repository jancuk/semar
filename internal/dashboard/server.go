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
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/localhttp"
	"github.com/jancuk/semar/internal/provider"
)

// TokenHeader carries the token on every API call.
const TokenHeader = localhttp.TokenHeader

const (
	maxBody      = 64 << 10
	checkTimeout = 6 * time.Second
	loginLimit   = 5 * time.Minute
	contentCSP   = localhttp.CSP
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
	listTimeout  time.Duration
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
	token, err := localhttp.NewToken()
	if err != nil {
		return nil, fmt.Errorf("make the dashboard token: %w", err)
	}
	s := &Server{d: d, token: token, checkTimeout: checkTimeout, listTimeout: listTimeout, loginLimit: loginLimit}
	s.logins.init()
	s.handler = s.routes()
	return s, nil
}

func (s *Server) Token() string { return s.token }

func (s *Server) Handler() http.Handler { return s.handler }

// Serve answers on ln until ctx ends. Then it ends every running sign-in job and waits for
// it, and lets the requests in flight finish for a few seconds.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	return localhttp.Serve(ctx, ln, s.handler, s.logins.stop)
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
	api.Handle("/api/providers/{id}/models", allow(map[string]http.HandlerFunc{"GET": s.getModels}))
	api.Handle("/api/providers/{id}/login", allow(map[string]http.HandlerFunc{"POST": s.postLogin}))
	api.Handle("/api/default", allow(map[string]http.HandlerFunc{"PUT": s.putDefault}))
	api.Handle("/api/logins/{job}", allow(map[string]http.HandlerFunc{"GET": s.getLogin, "DELETE": s.deleteLogin}))
	api.Handle("/api/usage", allow(map[string]http.HandlerFunc{"GET": s.getUsage}))
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotFound, "There is no such API path.")
	})
	mux.Handle("/api/", localhttp.GuardAPI("dashboard", "semar dashboard", s.token, maxBody, api))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not found.", http.StatusNotFound)
	})
	return localhttp.GuardAll("dashboard", contentCSP, mux)
}

func allow(methods map[string]http.HandlerFunc) http.Handler { return localhttp.Allow(methods) }

type errorBody = localhttp.ErrorBody

func fail(w http.ResponseWriter, code int, msg string) { localhttp.Fail(w, code, msg) }

func reply(w http.ResponseWriter, code int, v any) { localhttp.Reply(w, code, v) }

// decode reads one JSON value into v: see localhttp.Decode.
func decode(w http.ResponseWriter, r *http.Request, v any, empty bool) bool {
	return localhttp.Decode(w, r, v, empty, maxBody)
}
