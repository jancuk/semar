package cli

import (
	"context"

	"github.com/jancuk/semar/internal/codehost"
	"github.com/jancuk/semar/internal/codehost/bitbucket"
	"github.com/jancuk/semar/internal/codehost/github"
	"github.com/jancuk/semar/internal/codehost/gitlab"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/claude"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/vcs"
)

// This file is the one place that names adapters (X2).

func compareParsers() []codehost.LinkParser {
	return []codehost.LinkParser{bitbucket.Parse, github.Parse, gitlab.Parse}
}

func providerRegistry(repoRoot string, lookupEnv func(string) (string, bool)) *provider.Registry {
	r := provider.NewRegistry()
	r.Register(claude.Kind, claude.Factory(lookupEnv, workTreeRoot(repoRoot)))
	r.Register(fake.Kind, fake.Factory(repoRoot))
	return r
}

// workTreeRoot widens the directory a harness may not come from to the whole
// work tree, because --dir can name a directory below its root.
func workTreeRoot(dir string) string {
	g, err := vcs.Open(dir)
	if err != nil {
		return dir
	}
	top, err := g.TopLevel(context.Background())
	if err != nil {
		return dir
	}
	return top
}
