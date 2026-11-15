package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.URL.Query().Get("limit") != "100" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		if r.Header.Get("x-api-key") != testKey || r.Header.Get("anthropic-version") != apiVersion {
			t.Errorf("headers %v", r.Header)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"acme-large","display_name":"Acme Large"},{"id":"-bad id","display_name":"x"},{"id":"acme-small"}]}`))
	}))
	defer srv.Close()
	got, err := newDriver(t, srv.URL, "env:ACME_KEY").Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "acme-large" || got[0].Label != "Acme Large" || got[1].ID != "acme-small" || got[1].Label != "" {
		t.Fatalf("models = %+v", got)
	}
}

func TestModelsKeyReachesOnlyTheConfiguredHost(t *testing.T) {
	var other atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { other.Add(1) }))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v1/models", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, err := newDriver(t, srv.URL, "env:ACME_KEY").Models(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirect") || other.Load() != 0 {
		t.Fatalf("error %v, the other host got %d requests", err, other.Load())
	}
}

func TestModelsErrorsNeverHoldTheKey(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"rejected": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"bad key ` + testKey + `"}}`))
		},
		"broken": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":`)) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			_, err := newDriver(t, srv.URL, "env:ACME_KEY").Models(context.Background())
			if err == nil || strings.Contains(err.Error(), testKey) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	t.Run("an unresolvable key", func(t *testing.T) {
		_, err := newDriver(t, "https://api.example.com", "env:OTHER").Models(context.Background())
		if err == nil || !strings.Contains(err.Error(), "API key") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestModelsLoopbackNeedsNoKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["X-Api-Key"]; ok {
			t.Error("a key was sent")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"acme-large"}]}`))
	}))
	defer srv.Close()
	got, err := newDriver(t, srv.URL, "").Models(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("models %+v, error %v", got, err)
	}
}
