package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		base, key string
		ok        bool
		loopback  bool
	}{
		{"", "env:ACME_KEY", true, false},
		{"https://api.example.com/v1/", "keychain:acme", true, false},
		{"http://127.0.0.1:8080/v1", "", true, true},
		{"http://localhost:11434", "", true, true},
		{"http://[::1]:9000", "env:LOCAL_KEY", true, true},
		{"http://api.example.com", "env:ACME_KEY", false, false},
		{"https://api.example.com", "", false, false},
		{"https://user:pass@api.example.com", "env:ACME_KEY", false, false},
		{"https://api.example.com?x=1", "env:ACME_KEY", false, false},
		{"https://api.example.com#top", "env:ACME_KEY", false, false},
		{"ftp://api.example.com", "env:ACME_KEY", false, false},
		{"api.example.com", "env:ACME_KEY", false, false},
		{"https://api.example.com", "sk-literal-key", false, false},
	}
	for _, c := range cases {
		ep, err := ParseEndpoint(config.Provider{ID: "acme", BaseURL: c.base, APIKey: c.key}, "https://api.example.org")
		if (err == nil) != c.ok {
			t.Errorf("%q %q: error %v", c.base, c.key, err)
			continue
		}
		if err == nil && (ep.Loopback != c.loopback || strings.HasSuffix(ep.Base.Path, "/")) {
			t.Errorf("%q: %+v", c.base, ep)
		}
		if err != nil && strings.Contains(err.Error(), "sk-literal-key") {
			t.Errorf("the error holds the value: %v", err)
		}
	}
}

func TestKeyHealthStatus(t *testing.T) {
	ep, err := ParseEndpoint(config.Provider{APIKey: "env:ACME_KEY"}, "https://api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	good := func(string) (string, error) { return "acme-key-1", nil }
	bad := func(string) (string, error) { return "", errors.New("the variable ACME_KEY is not set") }
	if h := ep.Health(good); !h.Installed || h.Version != "api.example.com" {
		t.Errorf("health = %+v", h)
	}
	if h := ep.Health(bad); h.Installed || !strings.Contains(h.Detail, "the API key cannot be used: the variable ACME_KEY is not set") {
		t.Errorf("health = %+v", h)
	}
	if st := ep.Status(good); !st.SignedIn || st.Method != "api key" || strings.Contains(st.Detail, "acme-key-1") {
		t.Errorf("status = %+v", st)
	}
	if st := ep.Status(bad); st.SignedIn {
		t.Errorf("status = %+v", st)
	}
	_, perr := ep.Key(func(string) (string, error) { return " ", nil })
	if perr == nil || perr.Class != provider.PermissionError {
		t.Errorf("an empty key = %v", perr)
	}
}

func TestPostRetryAfter(t *testing.T) {
	for _, c := range []struct {
		retryAfter string
		requests   int
	}{
		{"0", 2},
		{"", 2},
		{"120", 1}, // longer than MaxWait: no retry
		{time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), 1},
	} {
		n := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n++
			if c.retryAfter != "" {
				w.Header().Set("Retry-After", c.retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		var waited []time.Duration
		caller := &Caller{HTTP: Client(nil), Wait: func(_ context.Context, d time.Duration) error { waited = append(waited, d); return nil }}
		_, err := caller.Post(context.Background(), srv.URL, []byte(`{}`))
		srv.Close()
		var perr *provider.Error
		if !errors.As(err, &perr) || perr.Class != provider.UsageLimit || n != c.requests {
			t.Errorf("Retry-After %q: %d requests, error %v", c.retryAfter, n, err)
		}
		if c.retryAfter == "" && (len(waited) != 1 || waited[0] != DefaultWait) {
			t.Errorf("waits %v", waited)
		}
	}
}

func TestPostLimitsTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", MaxResponseBytes+10)))
	}))
	defer srv.Close()
	_, err := (&Caller{HTTP: Client(nil)}).Post(context.Background(), srv.URL, []byte(`{}`))
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.ProviderError || !strings.Contains(perr.Detail, "larger than 8 MiB") {
		t.Fatalf("error = %v", err)
	}
}

func TestErrorMessage(t *testing.T) {
	for body, want := range map[string]string{
		`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`: "overloaded_error: Overloaded",
		`{"error":{"message":"Insufficient Balance","code":"invalid_request_error"}}`: "invalid_request_error: Insufficient Balance",
		`{"error":"model not found"}`:       "model not found",
		"<html>\n<b>Bad gateway</b></html>": "<html> <b>Bad gateway</b></html>",
	} {
		if got := errorMessage([]byte(body)); got != want {
			t.Errorf("%s: %q", body, got)
		}
	}
}
