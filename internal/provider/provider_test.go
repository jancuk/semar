package provider_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

type stub struct{ id, kind string }

func (s stub) ID() string                             { return s.id }
func (s stub) Kind() string                           { return s.kind }
func (s stub) Health(context.Context) provider.Health { return provider.Health{} }
func (s stub) Run(context.Context, provider.Job) (provider.Result, error) {
	return provider.Result{}, nil
}

func TestRegistry(t *testing.T) {
	r := provider.NewRegistry()
	for _, kind := range []string{"zeta", "alpha"} {
		r.Register(kind, func(cfg config.Provider) (provider.Instance, error) {
			return stub{cfg.ID, cfg.Kind}, nil
		})
	}
	t.Run("known kind", func(t *testing.T) {
		in, err := r.New(config.Provider{ID: "p", Kind: "alpha"})
		if err != nil || in.ID() != "p" || in.Kind() != "alpha" {
			t.Fatalf("got %v %v", in, err)
		}
	})
	t.Run("unknown kind lists the kinds sorted", func(t *testing.T) {
		_, err := r.New(config.Provider{ID: "p", Kind: "gpt"})
		if err == nil || !strings.Contains(err.Error(), `"gpt"`) || !strings.HasSuffix(err.Error(), "known kinds: alpha, zeta") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestErrorText(t *testing.T) {
	e := &provider.Error{Class: provider.UsageLimit, Detail: "try later"}
	if e.Error() != "usage_limit: try later" {
		t.Fatalf("got %q", e.Error())
	}
}
