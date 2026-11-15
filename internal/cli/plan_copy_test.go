package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/sampleapp"
)

// readsChanges keeps the changes.md that the planner was given.
type readsChanges struct {
	provider.Instance
	changes *string
}

func (r readsChanges) Run(ctx context.Context, job provider.Job) (provider.Result, error) {
	data, err := os.ReadFile(filepath.Join(job.Workdir, ".semar-plan", "changes.md"))
	if err != nil {
		return provider.Result{}, err
	}
	*r.changes = string(data)
	return r.Instance.Run(ctx, job)
}

func TestPlanReportsTheFilesLeftOutOfTheCopy(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	if err := os.Symlink("handler.go", p.path("internal", "refund", "alias.go")); err != nil {
		t.Skip("symlinks are not available:", err)
	}
	p.git.Git("add", "internal/refund/alias.go")
	p.git.Git("commit", "--quiet", "-m", "SHOP-482 add an alias")
	p.configure(t, goodDraft)
	var changes string
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(fake.Kind, func(cfg config.Provider) (provider.Instance, error) {
			f, err := fake.New(cfg, root)
			return readsChanges{f, &changes}, err
		})
		return r
	}
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	contains(t, "stderr", res.stderr, "semar: 1 file of "+p.head(t)[:12]+" was left out of the copy that the planner reads; plan.md lists them")
	contains(t, "changes.md", changes, "\n## Files left out of the copy\n", `- "internal/refund/alias.go": a link`)
	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "plan.md", string(md), "## Files left out of the copy", "- internal/refund/alias.go: a link")
}
