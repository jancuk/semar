package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/api"
)

// leak is a value the redactor does not know, as a captured id or a token in a query would be.
const leak = "LEAKED-7f3a91"

// secretCall carries the value in the rendered path and in the query. Its template is the step's hint.
func secretCall(method string) api.Call {
	c := get("/v1/orders/" + leak + "/refunds")
	c.Method = method
	c.PathTemplate = "/v1/orders/{id}/refunds"
	c.Query = map[string]string{"token": leak}
	if method == "POST" {
		c.JSON = []byte(`{}`)
	}
	return c
}

func assertNoLeak(t *testing.T, what string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(raw)) // the policy lower-cases what it quotes
	if strings.Contains(text, strings.ToLower(leak)) || strings.Contains(text, "token=") {
		t.Errorf("%s holds the request data: %s", what, raw)
	}
}

func TestTransportErrorHoldsNoRequestURL(t *testing.T) {
	srv := newServer(t, ok)
	e, sink := newEngine(t, srv, setup{})
	srv.Close()
	res, err := e.Do(t.Context(), secretCall("GET"))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if res.Action.Err == "" {
		t.Fatal("Err is empty, want the transport error")
	}
	assertNoLeak(t, "Action.Err", res.Action.Err)
	assertNoLeak(t, "the stored Err", sink.last(t).Err)
	assertNoLeak(t, "the stored Refused", sink.last(t).Refused)
}

func TestTransportErrorFromTheRoundTripperHoldsNoRequestURL(t *testing.T) {
	srv := newServer(t, ok)
	e, _ := newEngine(t, srv, setup{options: func(o *api.Options) {
		o.HTTP = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("no route to " + r.URL.String())
		})
	}})
	res := do(t, e, secretCall("GET"))
	if res.Action.Err == "" {
		t.Fatal("Err is empty, want the transport error")
	}
	assertNoLeak(t, "Action.Err", res.Action.Err)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestErrorsHoldNoRequestData(t *testing.T) {
	boom := errors.New("disk full")
	cases := []struct {
		name string
		run  func(t *testing.T) (api.Result, error)
	}{
		{"recorder failure", func(t *testing.T) (api.Result, error) {
			e, sink := newEngine(t, newServer(t, ok), setup{})
			sink.err = boom
			return e.Do(t.Context(), secretCall("GET"))
		}},
		{"cancelled before the call", func(t *testing.T) (api.Result, error) {
			e, _ := newEngine(t, newServer(t, ok), setup{})
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return e.Do(ctx, secretCall("GET"))
		}},
		{"cancelled in flight", func(t *testing.T) (api.Result, error) {
			arrived := make(chan struct{})
			srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
				close(arrived)
				hang(r)
			})
			e, _ := newEngine(t, srv, setup{})
			ctx, cancel := context.WithCancel(t.Context())
			go func() {
				<-arrived
				cancel()
			}()
			return e.Do(ctx, secretCall("POST"))
		}},
		{"timeout", func(t *testing.T) (api.Result, error) {
			e, _ := newEngine(t, newServer(t, func(w http.ResponseWriter, r *http.Request) { hang(r) }),
				setup{options: func(o *api.Options) { o.Timeout = 50 * time.Millisecond }})
			return e.Do(t.Context(), secretCall("POST"))
		}},
		{"a read only environment", func(t *testing.T) (api.Result, error) {
			e, _ := newEngine(t, newServer(t, ok), setup{mode: "read_only"})
			return e.Do(t.Context(), secretCall("POST"))
		}},
		{"the deny list", func(t *testing.T) (api.Result, error) {
			e, _ := newEngine(t, newServer(t, ok), setup{env: func(env *config.Environment) {
				env.Effects.Deny = []config.OpPattern{{Method: "GET", Path: "/v1/orders/**"}}
			}})
			return e.Do(t.Context(), secretCall("GET"))
		}},
		{"a path that does not parse", func(t *testing.T) (api.Result, error) {
			e, _ := newEngine(t, newServer(t, ok), setup{})
			c := secretCall("GET")
			c.Path = "/v1/%zz" + leak
			return e.Do(t.Context(), c)
		}},
		{"a path with a query", func(t *testing.T) (api.Result, error) {
			e, _ := newEngine(t, newServer(t, ok), setup{})
			c := secretCall("GET")
			c.Path = "/v1/x?token=" + leak
			return e.Do(t.Context(), c)
		}},
		{"a redirect to a denied path", func(t *testing.T) (api.Result, error) {
			srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/v1/admin/"+leak, http.StatusFound)
			})
			e, _ := newEngine(t, srv, setup{env: func(env *config.Environment) {
				env.Effects.Deny = []config.OpPattern{{Method: "GET", Path: "/v1/admin/**"}}
			}})
			return e.Do(t.Context(), secretCall("GET"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.run(t)
			if err != nil {
				assertNoLeak(t, "the returned error", err.Error())
			}
			assertNoLeak(t, "Action.Err", res.Action.Err)
			assertNoLeak(t, "Action.Refused", res.Action.Refused)
		})
	}
}
