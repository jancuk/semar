package cli_test

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

// runIn runs a command with --dir set to a folder of the work tree.
func (p *planRepo) runIn(t *testing.T, folder string, want int, args ...string) result {
	t.Helper()
	var out, errw bytes.Buffer
	code := cli.RunWith(t.Context(), append(args, "--dir", p.path(folder)), &out, &errw, p.opts)
	if code != want {
		t.Fatalf("semar %s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, want, out.String(), errw.String())
	}
	return result{code: code, stdout: out.String(), stderr: errw.String()}
}

func TestInitInAFolderBelowTheRootWritesPathsFromTheRoot(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	p.write(t, "services/orders/docs/openapi.yaml", "openapi: 3.0.3\n")
	p.write(t, "services/orders/docs/guide.md", "# Guide\n")

	res := p.runIn(t, "services/orders", cli.ExitOK, "init", "--base-url", "https://orders.example.com", "--service", "orders")
	contains(t, "stdout", res.stdout, "added knowledge: api (services/orders/docs/openapi.yaml), docs (services/orders/docs)\n")
	cfg, err := config.Load(p.path("services", "orders"))
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Knowledge{
		{ID: "api", Kind: config.KnowledgeOpenAPI, Path: "services/orders/docs/openapi.yaml"},
		{ID: "docs", Kind: config.KnowledgeFiles, Path: "services/orders/docs"},
	}
	if !reflect.DeepEqual(cfg.Knowledge, want) {
		t.Errorf("knowledge = %+v, want %+v", cfg.Knowledge, want)
	}
}

func TestInitLeavesOutAPathThatTheConfigurationRefuses(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.opts.Providers = noProvider(t)
	p.write(t, "svc`x/docs/openapi.yaml", "openapi: 3.0.3\n")
	p.runIn(t, "svc`x", cli.ExitOK, "init", "--base-url", "https://orders.example.com", "--service", "orders")
	data, err := os.ReadFile(p.path("svc`x", ".semar", "semar.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "knowledge") {
		t.Errorf("the file lists an entry that the configuration refuses:\n%s", data)
	}
}

func TestPlanHintsAtTheDocumentsOfTheFolderFromTheRoot(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.git.Commit("SHOP-482 document the refunds", map[string]*string{
		"services/orders/docs/openapi.yaml": gittest.S(ordersAPI),
		"services/orders/docs/refunds.md":   gittest.S(refundsDoc),
	})
	p.configure(t, goodDraft)
	// The configuration and the draft move into the folder, which is the --dir of the plan.
	cfgText, err := os.ReadFile(p.path(".semar", "semar.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p.write(t, "services/orders/.semar/semar.yaml", string(cfgText))
	p.write(t, "services/orders/drafts/1.json", goodDraft)
	made := p.recordJobs()

	res := p.runIn(t, "services/orders", cli.ExitOK, "plan", "--diff", "main..release")
	contains(t, "stderr", res.stderr,
		"semar: the repository has services/orders/docs/openapi.yaml and services/orders/docs; list them under knowledge in .semar/semar.yaml so that the planner reads them first\n")
	if prompt := onlyPrompt(t, *made); strings.Contains(prompt, "What the team wrote down") {
		t.Error("the prompt names documents that the file does not list")
	}
}
