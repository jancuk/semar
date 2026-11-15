package cli_test

import (
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/claude"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/sampleapp"
)

const knowledgeLine = "added knowledge: api (docs/openapi.yaml), docs (docs)\n"

var wantKnowledge = []config.Knowledge{
	{ID: "api", Kind: config.KnowledgeOpenAPI, Path: "docs/openapi.yaml"},
	{ID: "docs", Kind: config.KnowledgeFiles, Path: "docs"},
}

func TestInitAddsTheConventionalKnowledge(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	p.write(t, "docs/openapi.yaml", "openapi: 3.0.3\n")
	p.write(t, "docs/guide.md", "# Guide\n")

	res := p.must(t, cli.ExitOK, "init", "--base-url", "https://orders.example.com", "--service", "orders")
	contains(t, "stdout", res.stdout, knowledgeLine, "export SEMAR_TOKEN=")
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Knowledge, wantKnowledge) {
		t.Errorf("knowledge = %+v, want %+v", cfg.Knowledge, wantKnowledge)
	}

	// The setup flags stay repeatable when the file holds knowledge.
	res = p.must(t, cli.ExitOK, "init", "--base-url", "https://orders.example.com", "--service", "orders")
	contains(t, "stdout", res.stdout, ".semar/semar.yaml already has these settings; nothing was written")
}

func TestInitWithNothingConventionalWritesNoKnowledge(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	res := p.must(t, cli.ExitOK, "init", "--base-url", "https://orders.example.com", "--service", "orders")
	if strings.Contains(res.stdout, "knowledge") {
		t.Errorf("stdout names knowledge:\n%s", res.stdout)
	}
	data, err := os.ReadFile(p.path(".semar", "semar.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "knowledge") {
		t.Errorf("the file has a knowledge key:\n%s", data)
	}
}

func TestInitProductionSaysWhatKnowledgeWasAdded(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	p.write(t, "docs/guide.md", "# Guide\n")
	res := p.must(t, cli.ExitOK, "init", "--base-url", "https://orders.example.com", "--kind", "production")
	contains(t, "stdout", res.stdout, "added knowledge: docs (docs)\n", "the production environment is read only")
}

func TestFirstPlanAddsTheConventionalKnowledge(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	outputs := p.drafts(t, goodDraft)
	p.write(t, "docs/openapi.yaml", "openapi: 3.0.3\n")
	p.write(t, "docs/guide.md", "# Guide\n")
	p.write(t, "drafts/renamed.json", strings.NewReplacer("Refund above the order total is rejected", "A refund of the whole total still works",
		"An order is read by its ID", "An order is read twice").Replace(goodDraft))
	var jobs, builds atomic.Int64
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(claude.Kind, func(cfg config.Provider) (provider.Instance, error) {
			cfg.Outputs = outputs
			if builds.Add(1) > 1 {
				cfg.Outputs = []string{"drafts/renamed.json"}
			}
			f, err := fake.New(cfg, root)
			return counted{f, &jobs}, err
		})
		return r
	}
	first := []string{"plan", "--diff", "main..release", "--base-url", "http://" + p.host, "--service", "orders", "--token-env", planTokenEnv}
	res := p.must(t, cli.ExitOK, first...)
	contains(t, "stderr", res.stderr, "wrote "+p.path(".semar", "semar.yaml"), knowledgeLine)
	cfg, err := config.Load(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Knowledge, wantKnowledge) {
		t.Errorf("knowledge = %+v, want %+v", cfg.Knowledge, wantKnowledge)
	}

	// The same flags again: the file already has them, and the plan runs.
	before := jobs.Load()
	res = p.must(t, cli.ExitOK, first...)
	contains(t, "stderr", res.stderr, "semar: .semar/semar.yaml already has these settings, so the setup flags are not needed")
	if jobs.Load() == before {
		t.Error("the repeated command did not plan")
	}
}
