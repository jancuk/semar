package cli

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/jancuk/semar/internal/apicheck"
	"github.com/jancuk/semar/internal/apimcp"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/openapi"
)

// mcpAPIBudget is the most requests that the API tools send in one process.
const mcpAPIBudget = 200

// mcpAPIManagers maps the wiring of a running tool server to its API Manager, so that the journey tools
// (cli/mcp_journey.go) can read the log and set the variables of the API tools without a change to
// cli/mcp.go. An entry is removed when the server closes the surface.
var mcpAPIManagers = struct {
	sync.Mutex
	m map[*mcpWiring]*apimcp.Manager
}{m: map[*mcpWiring]*apimcp.Manager{}}

// mcpAPIManagerOf gives the API Manager of a server, or nil when the api group is not served.
func mcpAPIManagerOf(w *mcpWiring) *apimcp.Manager {
	mcpAPIManagers.Lock()
	defer mcpAPIManagers.Unlock()
	return mcpAPIManagers.m[w]
}

func init() { registerMCPSurface("api", buildMCPAPI) }

func buildMCPAPI(w *mcpWiring) (mcpSurfaceResult, error) {
	switch {
	case !w.RepoMode:
		return mcpSurfaceResult{}, mcpUnavailable("there is no repository")
	case w.EnvName == "" || w.Policy == nil:
		return mcpSurfaceResult{}, mcpUnavailable("no environment is bound: start semar mcp with --env <name>")
	}
	services := map[string]string{}
	for _, s := range w.Env.Services {
		if s.BaseURL != "" {
			services[s.Name] = s.BaseURL
		}
	}
	if len(services) == 0 {
		return mcpSurfaceResult{}, mcpUnavailable("the environment %q has no service with a baseUrl", w.EnvName)
	}

	docs, checker := mcpAPIDocs(w)
	profiles := slices.Sorted(maps.Keys(w.Cfg.AuthProfiles))
	var infos []apimcp.ServiceInfo
	for _, s := range w.Env.Services {
		if s.BaseURL == "" {
			continue
		}
		n := 0
		for _, d := range append(slices.Clone(docs[s.Name]), docs[""]...) {
			n += len(d.Operations)
		}
		infos = append(infos, apimcp.ServiceInfo{
			Name: s.Name, BaseURL: s.BaseURL, Hosts: slices.Clone(w.Env.Hosts), Profiles: profiles,
			DefaultProfile: s.AuthProfile, OpenAPI: n, Mode: w.Env.Effects.Mode, Kind: w.Env.Kind,
			Deny: len(w.Env.Effects.Deny), Once: len(w.Env.Effects.Once),
		})
	}

	// The run and the engine are made when the first request needs them (Decision E1).
	var mu sync.Mutex
	var engine *api.Engine
	sessions := newMCPSessions(w)
	backend := func(context.Context) (apimcp.RequestBackend, error) {
		mu.Lock()
		defer mu.Unlock()
		if engine != nil {
			return engine, nil
		}
		id, rec, err := w.EnsureRun()
		if err != nil {
			return nil, err
		}
		engine = api.New(api.Options{
			Policy: w.Policy, Recorder: rec, Services: services, Session: sessions.source,
			Run: id, Version: Version, Clock: w.Opts.Clock, Budget: mcpAPIBudget, Doc: checker,
		})
		return engine, nil
	}

	m := apimcp.New(apimcp.Options{
		Guard: w.Guard, Scrub: w.Scrubber, Baton: w.Baton, Policy: w.Policy,
		Services: infos, Docs: docs, Backend: backend, Profiles: profiles,
		RunID: func(context.Context) (string, error) {
			id, _, err := w.EnsureRun()
			return string(id), err
		},
	})
	mcpAPIManagers.Lock()
	mcpAPIManagers.m[w] = m
	mcpAPIManagers.Unlock()
	return mcpSurfaceResult{
		Tools: m.Tools(),
		Close: func(context.Context) error {
			mcpAPIManagers.Lock()
			delete(mcpAPIManagers.m, w)
			mcpAPIManagers.Unlock()
			return nil
		},
	}, nil
}

// mcpAPIDocs reads the OpenAPI documents of the knowledge sources, by service. A document that cannot be
// read is left out. The checker judges each answer against them, as every other run of the environment does.
func mcpAPIDocs(w *mcpWiring) (map[string][]openapi.Document, *apicheck.Checker) {
	docs := map[string][]openapi.Document{}
	var sources []apicheck.Source
	if found, err := knowledge.Resolve(w.Dir, w.Cfg.Knowledge); err == nil {
		for _, s := range found {
			if s.Kind != config.KnowledgeOpenAPI || !s.Usable() || s.API == nil {
				continue
			}
			docs[s.Service] = append(docs[s.Service], *s.API)
			if w.Env.Checks.Schema != apicheck.Off {
				sources = append(sources, apicheck.Source{Service: s.Service, Doc: *s.API})
			}
		}
	}
	return docs, apicheck.New(apicheck.Options{
		Env: w.Env.Name, Schema: w.Env.Checks.Schema, Limit: w.Env.Checks.ResponseTime.Limit,
		LimitOn: w.Env.Checks.ResponseTime.On, Docs: sources,
	})
}

// mcpSessions loads the session of an auth profile the first time that a request signs in as it, and
// teaches the scrubber and the redactor of the run its values before anything is sent.
type mcpSessions struct {
	w     *mcpWiring
	mu    sync.Mutex
	cache map[string]session
}

func newMCPSessions(w *mcpWiring) *mcpSessions {
	return &mcpSessions{w: w, cache: map[string]session{}}
}

func (s *mcpSessions) source(ctx context.Context, profile string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cache[profile]; !ok {
		loaded, err := loadSessions(ctx, s.w.Cfg, s.w.Secrets, map[string]string{profile: "api_request"})
		if err != nil {
			return "", "", err
		}
		maps.Copy(s.cache, loaded)
		s.w.AddSecrets(sessionSecrets(loaded)...)
		s.w.AddSecrets(s.w.Secrets.Loaded()...)
	}
	return sessionSource(s.w.Cfg, s.cache)(ctx, profile)
}
