package dashboard

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/userconfig"
)

type kindInfo struct {
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Harness bool   `json:"harness"`
}

var kinds = []kindInfo{
	{provider.KindClaude, "Claude Code", true},
	{provider.KindCodex, "Codex", true},
	{provider.KindAnthropic, "Anthropic API", false},
	{provider.KindOpenAI, "OpenAI format API", false},
}

type stateBody struct {
	Home        string          `json:"home"`
	File        string          `json:"file"`
	FileDisplay string          `json:"fileDisplay"` // File with the home directory of the person shown as ~
	Exists      bool            `json:"exists"`
	FileError   string          `json:"fileError,omitempty"`
	Default     string          `json:"default"`
	Kinds       []kindInfo      `json:"kinds"`
	Providers   []providerState `json:"providers"`
}

// definition is one provider as the file holds it. APIKey is a reference, never a key.
type definition struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Model     string     `json:"model"`
	Reasoning string     `json:"reasoning"`
	BaseURL   string     `json:"baseUrl,omitempty"`
	APIKey    string     `json:"apiKey,omitempty"`
	Setup     *setupBody `json:"setup,omitempty"`
}

type setupBody struct {
	MCP    *bool    `json:"mcp"`
	Skills *bool    `json:"skills"`
	Deny   []string `json:"deny"`
}

type providerState struct {
	definition
	Health       *healthBody             `json:"health,omitempty"`
	Account      *provider.AccountStatus `json:"account,omitempty"`
	KeyReady     *bool                   `json:"keyReady,omitempty"`
	LoginCommand []string                `json:"loginCommand,omitempty"`
	Error        string                  `json:"error,omitempty"`
}

type healthBody struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	f, exists, err := userconfig.Load(s.d.Home)
	file := filepath.Join(s.d.Home, userconfig.FileName)
	body := stateBody{
		Home:        s.d.Home,
		File:        file,
		FileDisplay: s.tildePath(file),
		Exists:      exists,
		Kinds:       kinds,
	}
	if err != nil {
		body.FileError = scrub(err.Error())
		f = userconfig.Builtin()
	}
	body.Default = f.Default
	body.Providers = s.check(r.Context(), f)
	reply(w, http.StatusOK, body)
}

// check runs the health and account checks of every provider at once, each with a time
// limit, so that one slow harness cannot hold up the page.
func (s *Server) check(ctx context.Context, f userconfig.File) []providerState {
	out := make([]providerState, len(f.Providers))
	var wg sync.WaitGroup
	for i, p := range f.Providers {
		out[i].definition = toDefinition(p)
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.checkOne(ctx, f, p.ID, &out[i])
		}()
	}
	wg.Wait()
	return out
}

func (s *Server) checkOne(ctx context.Context, f userconfig.File, id string, st *providerState) {
	cfg, _ := f.Find(id)
	inst, err := s.d.NewInstance(cfg)
	if err != nil {
		st.Error = "This provider cannot be checked: " + scrub(err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(ctx, s.checkTimeout)
	defer cancel()

	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		late bool
	)
	// Each check writes to its own value, and only a check that finished in time is kept,
	// so a check that ignores ctx cannot race with the answer.
	run := func(do func(context.Context, *providerState)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local providerState
			done := make(chan struct{})
			go func() { defer close(done); do(ctx, &local) }()
			select {
			case <-done:
			case <-ctx.Done():
				mu.Lock()
				late = true
				mu.Unlock()
				return
			}
			mu.Lock()
			defer mu.Unlock()
			merge(st, local)
		}()
	}
	run(func(ctx context.Context, out *providerState) {
		h := inst.Health(ctx)
		out.Health = &healthBody{Installed: h.Installed, Version: scrub(h.Version), Detail: scrub(h.Detail)}
	})
	if acc, ok := inst.(provider.Account); ok {
		run(func(ctx context.Context, out *providerState) {
			a := acc.Status(ctx)
			a.Method, a.Plan, a.Detail = scrub(a.Method), scrub(a.Plan), scrub(a.Detail)
			out.Account = &a
			out.LoginCommand = acc.LoginCommand()
		})
	}
	if provider.IsAPI(cfg.Kind) {
		ready := false
		st.KeyReady = &ready
		run(func(_ context.Context, out *providerState) {
			ok := cfg.APIKey == ""
			if !ok {
				_, err := keychain.Resolver(s.d.LookupEnv, s.d.Keys)(cfg.APIKey)
				ok = err == nil
			}
			out.KeyReady = &ok
		})
	}
	wg.Wait()
	if late {
		st.Error = "A check did not answer within " + plainDuration(s.checkTimeout) + "."
	}
}

func merge(st *providerState, local providerState) {
	if local.Health != nil {
		st.Health = local.Health
	}
	if local.Account != nil {
		st.Account = local.Account
		st.LoginCommand = local.LoginCommand
	}
	if local.KeyReady != nil {
		st.KeyReady = local.KeyReady
	}
}

func toDefinition(p userconfig.Provider) definition {
	d := definition{ID: p.ID, Kind: p.Kind, Model: p.Model, Reasoning: p.Reasoning, BaseURL: p.BaseURL, APIKey: p.APIKey}
	if provider.IsHarness(p.Kind) {
		yes := true
		d.Setup = &setupBody{MCP: &yes, Skills: &yes, Deny: []string{}}
		if p.Setup != nil {
			if p.Setup.MCP != nil {
				d.Setup.MCP = p.Setup.MCP
			}
			if p.Setup.Skills != nil {
				d.Setup.Skills = p.Setup.Skills
			}
			if p.Setup.Deny != nil {
				d.Setup.Deny = p.Setup.Deny
			}
		}
	}
	return d
}

var (
	emailPattern = regexp.MustCompile(`[^\s@<>()"',;:]+@[^\s@<>()"',;:]+\.[A-Za-z]{2,}`)
	secretValues = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(bearer\s+)\S+`),
		regexp.MustCompile(`(?i)((?:api[_-]?key|[a-z_-]*token|secret|password|authorization)["']?\s*[=:]\s*)\S+`),
	}
)

// scrub removes e-mail addresses and what looks like a credential from text that a harness
// or a file wrote. It keeps long words, so that a sign-in address stays usable.
func scrub(s string) string {
	s = emailPattern.ReplaceAllString(s, "[e-mail address]")
	for _, re := range secretValues {
		s = re.ReplaceAllString(s, "${1}[redacted]")
	}
	return s
}

// tildePath shows path with the person's home directory as ~, so that a page does not print
// the name of the account.
func (s *Server) tildePath(path string) string {
	home, _ := s.d.LookupEnv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" || !filepath.IsAbs(home) {
		return path
	}
	rel, err := filepath.Rel(filepath.Clean(home), path)
	if err != nil || !filepath.IsLocal(rel) {
		return path
	}
	return filepath.Join("~", rel)
}
