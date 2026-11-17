package cli

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jancuk/semar/internal/codehost"
	"github.com/jancuk/semar/internal/codehost/bitbucket"
	"github.com/jancuk/semar/internal/codehost/github"
	"github.com/jancuk/semar/internal/codehost/gitlab"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/anthropic"
	"github.com/jancuk/semar/internal/provider/claude"
	"github.com/jancuk/semar/internal/provider/codex"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/provider/openai"
	"github.com/jancuk/semar/internal/userconfig"
	"github.com/jancuk/semar/internal/vcs"
)

// This file is the one place that names adapters (X2).

func compareParsers() []codehost.LinkParser {
	return []codehost.LinkParser{bitbucket.Parse, github.Parse, gitlab.Parse}
}

// envDrop holds the names of the variables that a harness with the person's setup never
// inherits. A command sets them once it has read semar.yaml, which is after the registry
// exists, so the registry reads them when it builds a provider.
type envDrop struct {
	mu    sync.Mutex
	names []string
}

func (d *envDrop) set(names []string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.names = append([]string(nil), names...)
	d.mu.Unlock()
}

func (d *envDrop) get() []string {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.names...)
}

// secretEnvNames lists the variables that the auth profiles of semar.yaml name as secrets.
func secretEnvNames(cfg config.Config) []string {
	var names []string
	for _, p := range cfg.AuthProfiles {
		for _, ref := range p.Secrets {
			if name, ok := strings.CutPrefix(ref, "env:"); ok && name != "" {
				names = append(names, name)
			}
		}
		if p.Web != nil && p.Web.PasswordEnv() != "" {
			names = append(names, p.Web.PasswordEnv())
		}
	}
	return names
}

// keyEnvNames lists the variables that the providers of the person's own file name as keys.
// A harness drops them too, whichever provider names them.
func keyEnvNames(f userconfig.File) []string {
	var names []string
	for _, p := range f.Providers {
		if name, ok := strings.CutPrefix(p.APIKey, "env:"); ok && name != "" {
			names = append(names, name)
		}
	}
	return names
}

type providerDeps struct {
	lookupEnv func(string) (string, bool)
	environ   func() []string
	store     keychain.Store
	drop      *envDrop
}

func providerRegistry(repoRoot string, d providerDeps) *provider.Registry {
	r := provider.NewRegistry()
	root := workTreeRoot(repoRoot)
	r.Register(claude.Kind, func(cfg config.Provider) (provider.Instance, error) {
		return claude.FactoryWith(claude.Options{Lookup: d.lookupEnv, Environ: d.environ, RepoRoot: root, DropEnv: d.drop.get()})(cfg)
	})
	r.Register(codex.Kind, func(cfg config.Provider) (provider.Instance, error) {
		return codex.Factory(codex.Options{Lookup: d.lookupEnv, Environ: d.environ, RepoRoot: root, DropEnv: d.drop.get()})(cfg)
	})
	resolve := keychain.Resolver(d.lookupEnv, d.store)
	r.Register(anthropic.Kind, anthropic.Factory(anthropic.Options{ResolveKey: resolve}))
	r.Register(openai.Kind, openai.Factory(openai.Options{ResolveKey: resolve}))
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

// folderInWorkTree is the slash path of dir below the root of its work tree, and "" when dir is
// the root or no work tree holds it. The paths of knowledge start at the root, so a folder
// below it must put its own path in front of what it finds.
func folderInWorkTree(dir string) string {
	top := workTreeRoot(dir)
	// git names the real path, and dir may reach it through a link.
	realTop, err1 := filepath.EvalSymlinks(top)
	realDir, err2 := filepath.EvalSymlinks(dir)
	if err1 != nil || err2 != nil {
		return ""
	}
	rel, err := filepath.Rel(realTop, realDir)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.ToSlash(rel)
}
