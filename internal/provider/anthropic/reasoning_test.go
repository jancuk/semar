package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

func TestReasoningInTheRequest(t *testing.T) {
	cases := []struct{ name, cfgLevel, jobLevel, want string }{
		{"none", "", "", ""},
		{"configured", "medium", "", "medium"},
		{"job wins", "medium", "high", "high"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var sent []bool
			var level []any
			srv, _ := server(t, func(t *testing.T, r *http.Request, body map[string]any) (int, string) {
				_, has := body["output_config"]
				sent = append(sent, has)
				level = append(level, get(body, "output_config", "effort"))
				return submitStep(t, r, body)
			})
			d := newDriver(t, srv.URL, "env:ACME_KEY")
			d.cfg.Reasoning = c.cfgLevel
			j := newJob(t)
			j.Reasoning = c.jobLevel
			res, err := d.Run(context.Background(), j)
			if err != nil {
				t.Fatal(err)
			}
			if len(sent) != 1 || sent[0] != (c.want != "") {
				t.Fatalf("the output_config field was sent: %v", sent)
			}
			if c.want != "" && level[0] != c.want {
				t.Fatalf("level = %v, want %s", level[0], c.want)
			}
			if res.Reasoning != c.want {
				t.Fatalf("result level = %q", res.Reasoning)
			}
		})
	}
}

func TestVendorRefusesAnUnknownLevel(t *testing.T) {
	cfg := config.Provider{ID: "acme", Kind: Kind, Model: "acme-large", BaseURL: DefaultBase, APIKey: "env:ACME_KEY", Reasoning: "extreme"}
	_, err := New(cfg, Options{ResolveKey: func(string) (string, error) { return testKey, nil }})
	if err == nil || !strings.Contains(err.Error(), "the levels are ") || !strings.Contains(err.Error(), "high") {
		t.Fatalf("New: %v", err)
	}
	cfg.Reasoning = ""
	d, err := New(cfg, Options{ResolveKey: func(string) (string, error) { return testKey, nil }})
	if err != nil {
		t.Fatal(err)
	}
	j := newJob(t)
	j.Reasoning = "extreme"
	_, err = d.Run(context.Background(), j)
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Class != provider.ValidationError || !strings.Contains(pe.Detail, "the levels are ") {
		t.Fatalf("Run: %v", err)
	}
}

func TestAnotherServerIsAskedAboutAnyLevel(t *testing.T) {
	srv, _ := server(t, func(t *testing.T, r *http.Request, body map[string]any) (int, string) {
		if get(body, "output_config", "effort") != "acme" {
			t.Errorf("body = %v", body)
		}
		return submitStep(t, r, body)
	})
	j := newJob(t)
	j.Reasoning = "acme"
	if _, err := newDriver(t, srv.URL, "env:ACME_KEY").Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
}

func TestModelLevels(t *testing.T) {
	cases := []struct {
		id     string
		levels []string
		dflt   string
	}{
		{"claude-opus-5-5", []string{"low", "medium", "high", "xhigh", "max"}, "medium"},
		{"claude-opus-5-5-20261101", []string{"low", "medium", "high", "xhigh", "max"}, "medium"},
		{"claude-sonnet-4-6", []string{"low", "medium", "high", "max"}, "high"},
		{"claude-opus-4-5-20251101", []string{"low", "medium", "high"}, "high"},
		{"claude-haiku-4-5", nil, ""},
		{"acme-large", nil, ""},
	}
	for _, c := range cases {
		m := withEffort(provider.Model{ID: c.id})
		if !slices.Equal(m.Reasoning, c.levels) || m.DefaultReasoning != c.dflt {
			t.Errorf("%s: %v, default %q", c.id, m.Reasoning, m.DefaultReasoning)
		}
	}
}

func TestOnlyTheVendorsListCarriesLevels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-opus-5-5"}]}`))
	}))
	defer srv.Close()
	got, err := newDriver(t, srv.URL, "env:ACME_KEY").Models(context.Background())
	if err != nil || len(got) != 1 || len(got[0].Reasoning) != 0 || got[0].DefaultReasoning != "" {
		t.Fatalf("models %+v, error %v", got, err)
	}
}
