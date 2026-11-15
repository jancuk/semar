package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Errorf("headers %v", r.Header)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"acme-small"},{"id":"acme-large"},{"id":"not a model"}]}`))
	}))
	defer srv.Close()
	got, err := newDriver(t, srv.URL, "env:ACME_KEY").Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "acme-large" || got[1].ID != "acme-small" {
		t.Fatalf("models = %+v", got)
	}
}

func TestModelsAreCut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		b.WriteString(`{"data":[`)
		for i := 0; i < 400; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"id":"acme-%03d"}`, i)
		}
		b.WriteString(`]}`)
		_, _ = w.Write([]byte(b.String()))
	}))
	defer srv.Close()
	got, err := newDriver(t, srv.URL, "env:ACME_KEY").Models(context.Background())
	if err != nil || len(got) != 300 || got[0].ID != "acme-000" {
		t.Fatalf("%d models, error %v", len(got), err)
	}
}

func TestModelsKeyReachesOnlyTheConfiguredHost(t *testing.T) {
	var other atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { other.Add(1) }))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/models", http.StatusTemporaryRedirect)
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
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_api_key","message":"bad key ` + testKey + `"}}`))
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
}

func TestModelsLoopbackNeedsNoKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("a key was sent")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"acme-chat"}]}`))
	}))
	defer srv.Close()
	got, err := newDriver(t, srv.URL, "").Models(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("models %+v, error %v", got, err)
	}
}
