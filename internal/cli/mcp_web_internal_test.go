package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/secret"
)

func webWiring(t *testing.T, services ...config.Service) *mcpWiring {
	t.Helper()
	env := config.Environment{Kind: "test", Hosts: []string{"shop.test"}, Methods: []string{"GET"}, Effects: config.Effects{Mode: "test_data"}, Services: services}
	pol, err := effects.New(env)
	if err != nil {
		t.Fatal(err)
	}
	return &mcpWiring{
		Dir: t.TempDir(), RepoMode: true, EnvName: "local", Env: env, Policy: pol,
		Secrets: secret.NewEnv(func(string) (string, bool) { return "", false }), Scrubber: mcpkit.NewScrubber(nil),
		LookupEnv: func(string) (string, bool) { return "", false },
	}
}

func TestMCPWebIsUnavailableWithoutAWebService(t *testing.T) {
	w := webWiring(t, config.Service{Name: "app", Android: &config.ServiceAndroid{}})
	_, err := buildMCPWeb(w)
	if !errors.Is(err, errMCPSurfaceUnavailable) || err.Error() == "" {
		t.Fatalf("err = %v", err)
	}
	if _, ok := mcpWebManager(w); ok {
		t.Error("a manager was registered for a surface that is unavailable")
	}
	for name, bad := range map[string]*mcpWiring{
		"standalone":     {Dir: t.TempDir()},
		"no environment": {Dir: t.TempDir(), RepoMode: true},
	} {
		if _, err := buildMCPWeb(bad); !errors.Is(err, errMCPSurfaceUnavailable) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestMCPWebManagerIsRegisteredUntilClose(t *testing.T) {
	w := webWiring(t, config.Service{Name: "shop", BaseURL: "https://shop.test"})
	res, err := buildMCPWeb(w)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 14 {
		t.Errorf("%d tools", len(res.Tools))
	}
	m, ok := mcpWebManager(w)
	if !ok || m == nil {
		t.Fatal("no manager for the wiring")
	}
	other := webWiring(t, config.Service{Name: "shop", BaseURL: "https://shop.test"})
	if _, ok := mcpWebManager(other); ok {
		t.Error("a manager is found for a wiring that never built the surface")
	}
	if err := res.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := mcpWebManager(w); ok {
		t.Error("the manager stays registered after Close")
	}
}

func TestMCPWebAuthorNamesTheClient(t *testing.T) {
	if a := mcpWebAuthor(" claude-code\n"); a.Instance != "agent" || a.Model != "claude-code" || a.Harness != "mcp" {
		t.Errorf("author = %+v", a)
	}
	if a := mcpWebAuthor(""); a.Instance != "agent" || a.Model != "mcp" || a.Harness != "mcp" {
		t.Errorf("author = %+v", a)
	}
	// The mobile bridge's default, and what the journey writes, are the same shape.
	if mcpAuthor != *mcpWebAuthor("") {
		t.Errorf("mcpAuthor = %+v", mcpAuthor)
	}
}
