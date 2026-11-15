package cli

import (
	"github.com/jancuk/semar/internal/codehost"
	"github.com/jancuk/semar/internal/codehost/bitbucket"
	"github.com/jancuk/semar/internal/codehost/github"
	"github.com/jancuk/semar/internal/codehost/gitlab"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/claude"
	"github.com/jancuk/semar/internal/provider/fake"
)

// This file is the one place that names adapters (X2).

func compareParsers() []codehost.LinkParser {
	return []codehost.LinkParser{bitbucket.Parse, github.Parse, gitlab.Parse}
}

func providerRegistry(repoRoot string, lookupEnv func(string) (string, bool)) *provider.Registry {
	r := provider.NewRegistry()
	r.Register(claude.Kind, claude.Factory(lookupEnv, repoRoot))
	r.Register(fake.Kind, fake.Factory(repoRoot))
	return r
}
