package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"sync"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/pagelist"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/webmcp"
)

func init() { registerMCPSurface("web", buildMCPWeb) }

// mcpWebManagers are the web managers of the wiring that are live, so that a surface made later (the
// journey tools, the studio) finds the browser of the web tools of the same server without a field of
// mcpWiring.
var (
	mcpWebMu       sync.Mutex
	mcpWebManagers = map[*mcpWiring]*webmcp.Manager{}
)

// mcpWebManager gives the web manager of a wiring, once the web surface was built.
func mcpWebManager(w *mcpWiring) (*webmcp.Manager, bool) {
	mcpWebMu.Lock()
	defer mcpWebMu.Unlock()
	m, ok := mcpWebManagers[w]
	return m, ok
}

// mcpWebAuthor is who wrote what an agent recorded: the agent, over MCP, by the name that its client gave.
// It is the author of the mobile and journey tools too.
func mcpWebAuthor(client string) *domain.ProviderRef {
	a := mcpkit.AgentAuthor(client)
	return &a
}

func buildMCPWeb(w *mcpWiring) (mcpSurfaceResult, error) {
	switch {
	case !w.RepoMode:
		return mcpSurfaceResult{}, mcpUnavailable("there is no repository")
	case w.EnvName == "":
		return mcpSurfaceResult{}, mcpUnavailable("no environment is bound: start semar mcp with --env <name>")
	case w.Policy == nil:
		return mcpSurfaceResult{}, mcpUnavailable("the environment has no effects policy")
	}
	env := withServiceAuth(w.Cfg, w.Env)
	var services []webmcp.Service
	for _, s := range env.Services {
		if s.BaseURL != "" {
			services = append(services, webmcp.Service{Name: s.Name, BaseURL: s.BaseURL, Start: s.WebStart()})
		}
	}
	if len(services) == 0 {
		return mcpSurfaceResult{}, mcpUnavailable("no web service configured: no service of the environment has a baseUrl")
	}
	be := &mcpWebBackend{w: w, env: env}
	m := webmcp.New(webmcp.Options{
		Backend: be, Services: services, Hosts: env.Hosts, Policy: w.Policy, Guard: w.Guard, Scrub: w.Scrubber,
		Outputs: w.Outputs, LookupEnv: w.LookupEnv, OnSecret: func(v string) { w.AddSecrets(v) }, Baton: w.Baton,
	})
	mcpWebMu.Lock()
	mcpWebManagers[w] = m
	mcpWebMu.Unlock()
	return mcpSurfaceResult{Tools: m.Tools(), Close: func(ctx context.Context) error {
		mcpWebMu.Lock()
		delete(mcpWebManagers, w)
		mcpWebMu.Unlock()
		return m.Close(ctx)
	}}, nil
}

// mcpWebBackend opens the recorder for the web tools and writes what it heard with the same code that
// semar plan uses for its web tests.
type mcpWebBackend struct {
	w   *mcpWiring
	env config.Environment
}

func (b *mcpWebBackend) service(name string) (config.Service, error) {
	for _, s := range b.env.Services {
		if s.Name == name && s.BaseURL != "" {
			return s, nil
		}
	}
	return config.Service{}, fmt.Errorf("the environment has no web service %s", name)
}

// policy is the policy of the window. While the server only observes, it is the policy of the
// environment with its mode forced to read_only, so that the scripts of a page cannot write either.
func (b *mcpWebBackend) policy(readOnly bool) (*effects.Policy, error) {
	if !readOnly && !b.w.Guard.Observe {
		return b.w.Policy, nil
	}
	env := b.env
	env.Effects.Mode = "read_only"
	p, err := effects.New(env)
	if err != nil {
		return nil, fmt.Errorf("semar.yaml: %w", err)
	}
	return p, nil
}

func (b *mcpWebBackend) Open(ctx context.Context, r webmcp.OpenRequest) (webmcp.Browser, webmcp.OpenInfo, error) {
	w := b.w
	svc, err := b.service(r.Service)
	if err != nil {
		return nil, webmcp.OpenInfo{}, err
	}
	account, _, err := recordAccount(w.Cfg, svc, r.Account)
	if err != nil {
		return nil, webmcp.OpenInfo{}, err
	}
	pol, err := b.policy(r.ReadOnly)
	if err != nil {
		return nil, webmcp.OpenInfo{}, err
	}
	home, err := w.Opts.home()
	if err != nil {
		return nil, webmcp.OpenInfo{}, err
	}
	if _, err := web.FindChrome(b.env.Web.Chrome); err != nil {
		if text, ok := web.ChromeNotFoundText(err); ok {
			return nil, webmcp.OpenInfo{}, errors.New(text)
		}
		return nil, webmcp.OpenInfo{}, err
	}
	rec, err := startRecording(ctx, w.Opts, w.Cfg, recordingWindow{
		Home: home, Environment: w.EnvName, Chrome: b.env.Web.Chrome, Account: account,
		Hidden: !w.Headed && !r.Headed, Policy: pol,
	}, r.URL)
	if err != nil {
		return nil, webmcp.OpenInfo{}, fmt.Errorf("nothing was opened: %w", err)
	}
	x := web.NewExplorer(rec)
	x.RunID = string(w.Opts.RunID())
	info := webmcp.OpenInfo{Service: svc.Name, Account: account, URL: r.URL}
	if who, ok := rec.SignedIn(); ok {
		info.SignedIn = who
	}
	return &mcpWebBrowser{rec: rec, x: x}, info, nil
}

func (b *mcpWebBackend) Save(ctx context.Context, r webmcp.SaveRequest, events []record.Event, vars map[string]string) (webmcp.SaveResult, error) {
	w := b.w
	svc, err := b.service(r.Info.Service)
	if err != nil {
		return webmcp.SaveResult{}, err
	}
	ticket := r.Ticket
	if ticket == "" {
		ticket = groupOf(r.ID)
	}
	authProfile := ""
	if _, ok := w.Cfg.AuthProfiles[r.Info.Account]; ok {
		authProfile = r.Info.Account
	}
	replace, err := existingScenario(w.Dir, r.ID, r.Replace)
	if err != nil {
		return webmcp.SaveResult{}, err
	}
	res, err := writeWebTest(ctx, w.Opts, w.Dir, w.EnvName, webWriteRequest{
		ID: domain.ScenarioID(r.ID), Ticket: ticket, App: svc.Name, Service: svc.Name, BaseURL: svc.BaseURL,
		AuthProfile: authProfile, Vars: maps.Clone(vars), SecretEnv: r.SecretEnv,
		Title: r.Title, Retitle: r.Title != "", ByModel: true, Author: mcpWebAuthor(r.Client),
		Replace: replace, Verify: r.Verify,
	}, events)
	if err != nil {
		return webmcp.SaveResult{}, err
	}
	switch {
	case res.Kind == webWriteNoCheck:
		return webmcp.SaveResult{}, errors.New("the recording has no check, so nothing proves anything: record one with web_assert on an element that shows the result, then save again")
	case res.Kind == webWriteSecret:
		return webmcp.SaveResult{}, errors.New("a password was typed and the test has no name for it: type passwords with secretEnv")
	case res.Manual != "":
		return webmcp.SaveResult{}, errors.New(res.Manual)
	}
	rel := func(p string) string {
		if r, err := filepath.Rel(w.Dir, p); err == nil && filepath.IsLocal(r) {
			return filepath.ToSlash(r)
		}
		return p
	}
	out := webmcp.SaveResult{
		ScenarioPath: rel(res.Written.ScenarioPath), ScreensPath: rel(res.Written.ScreensPath),
		Actions: res.Draft.Actions, Checks: res.Draft.Checks, Replay: res.Replay, Notes: res.Draft.Notes,
	}
	for _, f := range res.Draft.FailsNow {
		out.FailsNow = append(out.FailsNow, fmt.Sprintf("%s should show %q and the page shows %q", f.Element, f.Expect, f.Shown))
	}
	return out, nil
}

// mcpWebBrowser is the window of the web tools: the recorder and the explorer of one tab. An action is
// followed by a wait for the page to look still, so that the next list reads the result.
type mcpWebBrowser struct {
	rec *web.Recorder
	x   *web.Explorer
}

var _ webmcp.Browser = (*mcpWebBrowser)(nil)

func (b *mcpWebBrowser) List(ctx context.Context) (pagelist.List, error) {
	b.rec.Settle(ctx)
	l, err := b.x.List(ctx)
	if err != nil {
		return l, err
	}
	return ownElements(l, b.x.RunID), nil
}

func (b *mcpWebBrowser) then(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	b.rec.Settle(ctx)
	return nil
}

func (b *mcpWebBrowser) Click(ctx context.Context, ref string) error {
	return b.then(ctx, b.x.Click(ctx, ref))
}
func (b *mcpWebBrowser) Fill(ctx context.Context, ref, text string) error {
	return b.then(ctx, b.x.Fill(ctx, ref, text))
}
func (b *mcpWebBrowser) Select(ctx context.Context, ref, label string) error {
	return b.then(ctx, b.x.Select(ctx, ref, label))
}
func (b *mcpWebBrowser) SetChecked(ctx context.Context, ref string, on bool) error {
	return b.then(ctx, b.x.SetChecked(ctx, ref, on))
}
func (b *mcpWebBrowser) Press(ctx context.Context, ref, key string) error {
	return b.then(ctx, b.x.Press(ctx, ref, key))
}
func (b *mcpWebBrowser) Hover(ctx context.Context, ref string) error {
	return b.then(ctx, b.x.Hover(ctx, ref))
}
func (b *mcpWebBrowser) Drag(ctx context.Context, from, to string) error {
	return b.then(ctx, b.x.Drag(ctx, from, to))
}
func (b *mcpWebBrowser) Shows(ctx context.Context, ref string) (string, error) {
	return b.x.Shows(ctx, ref)
}
func (b *mcpWebBrowser) Check(ctx context.Context, ref string) (string, error) {
	return b.x.Check(ctx, ref)
}
func (b *mcpWebBrowser) Open(ctx context.Context, rawURL string) error {
	return b.then(ctx, b.rec.Goto(rawURL))
}
func (b *mcpWebBrowser) Screenshot(ctx context.Context) ([]byte, error) { return b.rec.Screenshot(ctx) }
func (b *mcpWebBrowser) Location(ctx context.Context) (string, error)   { return b.rec.Location(ctx) }
func (b *mcpWebBrowser) Mark() int                                      { return b.rec.Mark() }
func (b *mcpWebBrowser) EventsFrom(i int) []record.Event                { return b.rec.EventsFrom(i) }
func (b *mcpWebBrowser) Blocked() []string                              { return b.rec.BlockedHosts() }
func (b *mcpWebBrowser) Close() []record.Event                          { return b.rec.Finish() }

func (b *mcpWebBrowser) Vars() map[string]string {
	vars := map[string]string{}
	maps.Copy(vars, b.x.Variables())
	if b.x.RunID != "" {
		vars["run.id"] = b.x.RunID
	}
	return vars
}
