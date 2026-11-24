package build_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

var _ build.Prober = (*build.HTTP)(nil)

func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func svc(name, url, commit string) config.Service {
	return config.Service{Name: name, BaseURL: "http://unused", Build: &config.BuildProbe{Probe: "http", URL: url, Commit: commit}}
}

func TestHTTPProbe(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		commit  string
		want    string
		wantErr string
	}{
		{"string", 200, `{"commit":"9f3c2ab"}`, "$.commit", "9f3c2ab", ""},
		{"number", 200, `{"build":{"id":12345}}`, "$.build.id", "12345", ""},
		{"big number stays exact", 200, `{"id":12345678901234567890}`, "$.id", "12345678901234567890", ""},
		{"server error", 500, `oops`, "$.commit", "", "orders"},
		{"not json", 200, `<html>`, "$.commit", "", "orders"},
		{"missing path", 200, `{"other":1}`, "$.commit", "", "orders"},
		{"several matches", 200, `{"a":[{"c":"x"},{"c":"y"}]}`, "$.a[*].c", "", "orders"},
		{"object value", 200, `{"commit":{"a":1}}`, "$.commit", "", "orders"},
		{"bool value", 200, `{"commit":true}`, "$.commit", "", "orders"},
		{"null value", 200, `{"commit":null}`, "$.commit", "", "orders"},
		{"empty string", 200, `{"commit":""}`, "$.commit", "", "orders"},
		{"bad jsonpath", 200, `{"commit":"x"}`, "$[", "", "orders"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := serve(t, tt.status, tt.body)
			p := build.NewHTTP([]config.Service{svc("orders", srv.URL, tt.commit)}, srv.Client())
			got, err := p.Probe(t.Context(), "orders")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to name %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != (domain.BuildRef{Service: "orders", Commit: tt.want}) {
				t.Errorf("ref = %+v", got)
			}
		})
	}
}

func TestProbeErrors(t *testing.T) {
	srv := serve(t, 200, `{"commit":"x"}`)
	p := build.NewHTTP([]config.Service{
		svc("orders", srv.URL, "$.commit"),
		{Name: "billing", BaseURL: "http://unused"},
		{Name: "odd", Build: &config.BuildProbe{Probe: "grpc", URL: srv.URL, Commit: "$.commit"}},
	}, srv.Client())
	tests := []struct {
		service string
		want    []string
	}{
		{"billing", []string{"billing", "no build probe"}},
		{"ghost", []string{"ghost", "not configured"}},
		{"odd", []string{"odd", "grpc"}},
	}
	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			_, err := p.Probe(t.Context(), tt.service)
			if err == nil {
				t.Fatal("no error")
			}
			if got := errors.Is(err, build.ErrNoBuild); got != (tt.service == "billing") {
				t.Errorf("errors.Is(err, build.ErrNoBuild) = %v for %s", got, tt.service)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want %q in it", err, w)
				}
			}
		})
	}
}

func TestProbeErrorHoldsNoLongBody(t *testing.T) {
	srv := serve(t, 502, strings.Repeat("secret-", 500))
	p := build.NewHTTP([]config.Service{svc("orders", srv.URL, "$.commit")}, srv.Client())
	_, err := p.Probe(t.Context(), "orders")
	if err == nil {
		t.Fatal("no error")
	}
	if len(err.Error()) > 400 || !strings.Contains(err.Error(), "502") {
		t.Errorf("error is %d bytes: %v", len(err.Error()), err)
	}
}

func TestProbeReadsAtMost64KiB(t *testing.T) {
	// valid JSON larger than 64 KiB with the commit near the end: without the cap it would be read in full
	big := `{"pad":"` + strings.Repeat("a", 70<<10) + `","commit":"late"}`
	srv := serve(t, 200, big)
	p := build.NewHTTP([]config.Service{svc("orders", srv.URL, "$.commit")}, srv.Client())
	_, err := p.Probe(t.Context(), "orders")
	if err == nil || !strings.Contains(err.Error(), "orders") || !strings.Contains(err.Error(), "larger") {
		t.Errorf("err = %v", err)
	}
}

func TestProbeRefusesTrailingData(t *testing.T) {
	srv := serve(t, 200, `{"commit":"x"} junk`)
	p := build.NewHTTP([]config.Service{svc("orders", srv.URL, "$.commit")}, srv.Client())
	if _, err := p.Probe(t.Context(), "orders"); err == nil || !strings.Contains(err.Error(), "orders") {
		t.Errorf("err = %v", err)
	}
}

func TestProbeErrorHidesTheURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/health?token=hunter2"
	srv.Close()
	p := build.NewHTTP([]config.Service{svc("orders", url, "$.commit")}, nil)
	_, err := p.Probe(t.Context(), "orders")
	if err == nil || strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), "orders") {
		t.Errorf("err = %v", err)
	}
}

func TestProbeTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	p := build.NewHTTP([]config.Service{svc("orders", srv.URL, "$.commit")}, srv.Client())
	start := time.Now()
	if _, err := p.Probe(ctx, "orders"); err == nil || !strings.Contains(err.Error(), "orders") {
		t.Errorf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("probe ignored the context deadline")
	}
}
