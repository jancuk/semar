package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/usage"
	"github.com/jancuk/semar/internal/userconfig"
)

// spy builds every kind of provider as one that records its definition and its job, and then
// fails with err, so that a test sees what the command chose without a model.
type spy struct {
	mu   sync.Mutex
	cfgs []config.Provider
	jobs []provider.Job
	err  error
	res  provider.Result
}

func (s *spy) registry(string) *provider.Registry {
	r := provider.NewRegistry()
	for _, kind := range []string{"claude", "codex", "anthropic", "openai", "fake"} {
		r.Register(kind, func(cfg config.Provider) (provider.Instance, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.cfgs = append(s.cfgs, cfg)
			return spyInstance{s, cfg}, nil
		})
	}
	return r
}

type spyInstance struct {
	s   *spy
	cfg config.Provider
}

func (i spyInstance) ID() string                           { return i.cfg.ID }
func (i spyInstance) Kind() string                         { return i.cfg.Kind }
func (spyInstance) Health(context.Context) provider.Health { return provider.Health{Installed: true} }
func (i spyInstance) Run(_ context.Context, job provider.Job) (provider.Result, error) {
	i.s.mu.Lock()
	defer i.s.mu.Unlock()
	i.s.jobs = append(i.s.jobs, job)
	return i.s.res, i.s.err
}

func (s *spy) chosen(t *testing.T) config.Provider {
	t.Helper()
	if len(s.cfgs) != 1 {
		t.Fatalf("%d providers were built, want 1", len(s.cfgs))
	}
	return s.cfgs[0]
}

func ptr[T any](v T) *T { return &v }

// useProviders replaces the providers of semar.yaml and, when it is not empty, plan.provider.
func (p *planRepo) useProviders(t *testing.T, providers, planProvider string) {
	t.Helper()
	p.rewriteConfig(t, func(s string) string {
		head, _, _ := strings.Cut(s, "providers:\n")
		out := head
		if providers != "" {
			out += "providers:\n" + providers
		}
		if planProvider != "" {
			out += "plan:\n  provider: " + planProvider + "\n"
		}
		return out
	})
}

func (p *planRepo) yourFile(t *testing.T, f userconfig.File) {
	t.Helper()
	if err := userconfig.Save(p.opts.Home, f); err != nil {
		t.Fatal(err)
	}
}

func (p *planRepo) records(t *testing.T) []usage.Record {
	t.Helper()
	recs, err := usage.Read(p.opts.Home, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestWhichProviderPlans(t *testing.T) {
	const repo = "  - id: planner\n    kind: claude\n    model: repo-model\n  - id: other\n    kind: codex\n"
	mine := userconfig.Provider{ID: "mine", Kind: "claude", Model: "mine-model", Setup: &userconfig.Setup{MCP: ptr(false), Skills: ptr(true)}}
	theirs := userconfig.Provider{ID: "planner", Kind: "claude", Model: "file-model", Setup: &userconfig.Setup{MCP: ptr(true), Skills: ptr(false)}}
	both := func(def string) *userconfig.File {
		return &userconfig.File{Version: 1, Default: def, Providers: []userconfig.Provider{mine, theirs}}
	}
	on := config.Setup{MCP: true, Skills: true}
	cases := []struct {
		name      string
		file      *userconfig.File
		providers string
		plan      string
		args      []string
		wantID    string
		wantModel string
		wantSetup config.Setup
		wantUser  bool
	}{
		{"nothing names one", nil, "", "", nil, "claude", "", on, false},
		{"semar.yaml lists providers", nil, repo, "", nil, "planner", "repo-model", on, false},
		{"semar.yaml names one", nil, repo, "other", nil, "other", "", on, false},
		{"your file beats semar.yaml", both("mine"), repo, "other", nil, "mine", "mine-model", config.Setup{Skills: true}, true},
		{"the flag beats your file and semar.yaml", both("mine"), repo, "planner", []string{"--provider", "other"}, "other", "", on, false},
		{"the flag beats the built-in", nil, repo, "", []string{"--provider", "claude"}, "claude", "", on, false},
		{"an ID in both comes from your file", both("mine"), repo, "planner", []string{"--provider", "planner"}, "planner", "file-model", config.Setup{MCP: true}, true},
		{"semar.yaml names an ID of your file", both("mine"), "", "planner", []string{"--provider", "planner"}, "planner", "file-model", config.Setup{MCP: true}, true},
		{"no setup", nil, repo, "", []string{"--no-setup"}, "planner", "repo-model", config.Setup{}, false},
		{"no setup beats your file", both("mine"), repo, "", []string{"--no-setup"}, "mine", "mine-model", config.Setup{}, true},
		{"the flag model beats the provider's", both("mine"), repo, "", []string{"--model", "flag-model"}, "mine", "flag-model", config.Setup{Skills: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.configure(t, goodDraft)
			p.useProviders(t, tc.providers, tc.plan)
			if tc.file != nil {
				p.yourFile(t, *tc.file)
			}
			s := &spy{err: &provider.Error{Class: provider.ProviderError, Detail: "stop"}}
			p.opts.Providers = s.registry
			p.must(t, cli.ExitError, append([]string{"plan", "--diff", "main..release"}, tc.args...)...)
			got := s.chosen(t)
			if got.ID != tc.wantID || got.Setup.MCP != tc.wantSetup.MCP || got.Setup.Skills != tc.wantSetup.Skills || got.User != tc.wantUser {
				t.Errorf("provider = %+v, want %s with setup %+v and user %v", got, tc.wantID, tc.wantSetup, tc.wantUser)
			}
			var model string
			if len(s.jobs) == 1 {
				model = s.jobs[0].Model
			}
			if model != tc.wantModel {
				t.Errorf("the job's model = %q, want %q", model, tc.wantModel)
			}
		})
	}
}

func TestProviderErrors(t *testing.T) {
	apiNoModel := userconfig.File{Version: 1, Default: "claude", Providers: []userconfig.Provider{
		{ID: "claude", Kind: "claude"},
		{ID: "acme", Kind: "openai", BaseURL: "http://127.0.0.1:9/v1", APIKey: "env:ACME_KEY"},
	}}
	t.Run("an unknown ID lists the known ones and the files", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		p.yourFile(t, apiNoModel)
		p.opts.Providers = noProvider(t)
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--provider", "nope")
		contains(t, "stderr", res.stderr, `unknown provider "nope"`, "claude, acme", "providers.yaml", "planner", "semar.yaml")
	})
	t.Run("an unknown ID without your file", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		p.opts.Providers = noProvider(t)
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--provider", "nope")
		contains(t, "stderr", res.stderr, `unknown provider "nope"`, "planner", "claude, built in")
	})
	t.Run("an invalid file of yours is an error even with the flag", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		if err := os.WriteFile(filepath.Join(p.opts.Home, userconfig.FileName), []byte("version: 1\nbogus: true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		p.opts.Providers = noProvider(t)
		for _, args := range [][]string{nil, {"--provider", "planner"}} {
			res := p.must(t, cli.ExitError, append([]string{"plan", "--diff", "main..release"}, args...)...)
			contains(t, "stderr", res.stderr, "providers.yaml")
		}
	})
	t.Run("an API kind without a model", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		p.yourFile(t, apiNoModel)
		p.opts.Providers = noProvider(t)
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--provider", "acme")
		contains(t, "stderr", res.stderr, `provider "acme" (openai) has no model`, "--model", "providers.yaml")
		p.assertNoTempCopy(t)
		if recs := p.records(t); len(recs) != 0 {
			t.Errorf("%d usage records, want none, because no job started", len(recs))
		}
	})
	t.Run("an API kind in semar.yaml", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		p.useProviders(t, "  - id: remote\n    kind: openai\n    model: m\n", "remote")
		p.opts.Providers = noProvider(t)
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release")
		contains(t, "stderr", res.stderr, `provider "remote"`, "your own file")
	})
}

func TestStartLine(t *testing.T) {
	cases := []struct {
		name string
		file userconfig.File
		args []string
		want []string
	}{
		{"harness with the setup", userconfig.File{Version: 1, Default: "claude", Providers: []userconfig.Provider{{ID: "claude", Kind: "claude"}}}, nil,
			[]string{"provider claude (claude) plans release with its default model, and can use your MCP servers and skills (--no-setup turns them off); this can take several minutes"}},
		{"harness without the setup", userconfig.File{Version: 1, Default: "claude", Providers: []userconfig.Provider{{ID: "claude", Kind: "claude"}}}, []string{"--no-setup"},
			[]string{"and can only read the copy of the source"}},
		{"API kind", userconfig.File{Version: 1, Default: "acme", Providers: []userconfig.Provider{{ID: "acme", Kind: "openai", Model: "acme-chat", BaseURL: "http://127.0.0.1:9/v1", APIKey: "env:ACME_KEY"}}}, nil,
			[]string{"provider acme (openai) plans release with acme-chat, and sends the files it reads to http://127.0.0.1:9/v1;"}},
		{"API kind at the vendor", userconfig.File{Version: 1, Default: "ant", Providers: []userconfig.Provider{{ID: "ant", Kind: "anthropic", Model: "m", APIKey: "env:K"}}}, nil,
			[]string{"sends the files it reads to https://api.anthropic.com;"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.configure(t, goodDraft)
			p.yourFile(t, tc.file)
			p.opts.Providers = (&spy{err: &provider.Error{Class: provider.ProviderError, Detail: "stop"}}).registry
			res := p.must(t, cli.ExitError, append([]string{"plan", "--diff", "main..release"}, tc.args...)...)
			contains(t, "stderr", res.stderr, tc.want...)
		})
	}
}

func TestProvidersCommand(t *testing.T) {
	t.Run("without your file", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		res := p.must(t, cli.ExitOK, "providers")
		contains(t, "stdout", res.stdout, filepath.Join(p.opts.Home, "providers.yaml"), "does not exist", "planner", "fake", "* plans here: planner, chosen by semar.yaml")
		if !strings.Contains(res.stdout, "*  planner") {
			t.Errorf("the planning provider has no mark:\n%s", res.stdout)
		}
	})
	t.Run("without any file", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		p.useProviders(t, "", "")
		res := p.must(t, cli.ExitOK, "providers")
		contains(t, "stdout", res.stdout, "claude", "(its default)", "built-in", "* plans here: claude, chosen by built-in")
	})
	t.Run("with your file", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		p.yourFile(t, userconfig.File{Version: 1, Default: "acme", Providers: []userconfig.Provider{
			{ID: "claude", Kind: "claude"},
			{ID: "acme", Kind: "openai", Model: "acme-chat", BaseURL: "http://127.0.0.1:9/v1", APIKey: "env:ACME_KEY"},
		}})
		res := p.must(t, cli.ExitOK, "providers")
		contains(t, "stdout", res.stdout, filepath.Join(p.opts.Home, "providers.yaml"), "acme-chat", "your file", "planner", "semar.yaml", "* plans here: acme, chosen by your file")
		if strings.Contains(res.stdout, "does not exist") {
			t.Errorf("the file exists:\n%s", res.stdout)
		}
		if strings.Contains(res.stdout, "ACME_KEY") || strings.Contains(res.stdout, "127.0.0.1") {
			t.Errorf("the listing shows a key reference or an address:\n%s", res.stdout)
		}
	})
	t.Run("an invalid file", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		if err := os.WriteFile(filepath.Join(p.opts.Home, userconfig.FileName), []byte("version: 2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		res := p.must(t, cli.ExitError, "providers")
		contains(t, "stderr", res.stderr, "providers.yaml")
	})
}

func TestPlanRecordsUsage(t *testing.T) {
	t.Run("a failed job", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		p.opts.Providers = (&spy{
			err: &provider.Error{Class: provider.UsageLimit, Detail: "limit"},
			res: provider.Result{Turns: 2, InputTokens: 70, OutputTokens: 3, CostUSD: 0.5, Limits: []provider.Limit{{Window: "five_hour", Utilization: 0.9, Status: "allowed"}}},
		}).registry
		p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--model", "m1")
		recs := p.records(t)
		if len(recs) != 1 {
			t.Fatalf("%d records, want 1", len(recs))
		}
		r := recs[0]
		if r.Provider != "planner" || r.Kind != "fake" || r.Model != "m1" || r.Command != "plan" || r.Outcome != "failed:usage_limit" || r.Ref != "" ||
			r.Attempts != 1 || r.Turns != 2 || r.InputTokens != 70 || r.OutputTokens != 3 || r.CostUSD != 0.5 || len(r.Limits) != 1 {
			t.Errorf("record = %+v", r)
		}
	})
	t.Run("a rejected draft", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, badDraft, badDraft, badDraft)
		p.must(t, cli.ExitError, "plan", "--diff", "main..release")
		recs := p.records(t)
		if len(recs) != 1 || recs[0].Outcome != "rejected" || recs[0].Attempts != 3 || recs[0].Ref != "" {
			t.Fatalf("records = %+v", recs)
		}
	})
	t.Run("an accepted plan after a correction", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, badDraft, goodDraft)
		p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
		recs := p.records(t)
		if len(recs) != 1 || recs[0].Outcome != "ok" || recs[0].Attempts != 2 || recs[0].Ref != p.head(t)[:12] {
			t.Fatalf("records = %+v", recs)
		}
	})
	t.Run("a job that cannot be recorded", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		if err := os.Mkdir(filepath.Join(p.opts.Home, usage.FileName), 0o700); err != nil {
			t.Fatal(err)
		}
		res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
		if n := strings.Count(res.stderr, "was not recorded"); n != 1 {
			t.Errorf("%d notices, want 1:\n%s", n, res.stderr)
		}
	})
}

func TestNoTestWritesTheDefaultHome(t *testing.T) {
	// TestMain points SEMAR_HOME at a directory of its own, so a command that takes the default
	// home writes there and never to the person's.
	home, ok := os.LookupEnv("SEMAR_HOME")
	if !ok || home == "" {
		t.Fatal("SEMAR_HOME is not set by TestMain")
	}
	if real, err := userconfig.Home(func(string) (string, bool) { return "", false }); err == nil && real == home {
		t.Fatalf("the tests use the real home %s", home)
	}
}

func TestReasoningOrder(t *testing.T) {
	file := userconfig.File{Version: 1, Default: "acme", Providers: []userconfig.Provider{
		{ID: "acme", Kind: "claude", Reasoning: "medium"},
		{ID: "plain", Kind: "claude"},
	}}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"the flag beats the provider", []string{"--reasoning", "max"}, "max"},
		{"the provider", nil, "medium"},
		{"none", []string{"--provider", "plain"}, ""},
		{"the flag on a provider without one", []string{"--provider", "plain", "--reasoning", "low"}, "low"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.configure(t, goodDraft)
			p.yourFile(t, file)
			s := &spy{err: &provider.Error{Class: provider.ProviderError, Detail: "stop"}}
			p.opts.Providers = s.registry
			res := p.must(t, cli.ExitError, append([]string{"plan", "--diff", "main..release"}, tc.args...)...)
			if len(s.jobs) != 1 || s.jobs[0].Reasoning != tc.want {
				t.Fatalf("jobs = %+v, want the level %q", s.jobs, tc.want)
			}
			if got := p.records(t); len(got) != 1 || got[0].Reasoning != tc.want {
				t.Fatalf("records = %+v", got)
			}
			if has := strings.Contains(res.stderr, " reasoning, "); has != (tc.want != "") || tc.want != "" && !strings.Contains(res.stderr, "at "+tc.want+" reasoning") {
				t.Errorf("start line:\n%s", res.stderr)
			}
		})
	}
	t.Run("a level that is not a word", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--reasoning", "High;")
		contains(t, "stderr", res.stderr, "--reasoning", "lower-case letters")
	})
}

func TestProvidersCommandShowsReasoning(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.yourFile(t, userconfig.File{Version: 1, Default: "acme", Providers: []userconfig.Provider{
		{ID: "acme", Kind: "claude", Reasoning: "xhigh"},
		{ID: "plain", Kind: "claude"},
	}})
	res := p.must(t, cli.ExitOK, "providers")
	contains(t, "stdout", res.stdout, "REASONING", "xhigh", "(its default)")
}
