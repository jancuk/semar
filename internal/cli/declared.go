package cli

import (
	"context"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/vcs"
)

// declaredBuilds holds the builds of the services that no probe reads. A person names a commit with
// --build. A service with no probe that runs on this machine, and that no --build names, runs the
// working copy of the repository, so its build is the head commit and the changes to tracked files.
type declaredBuilds struct {
	commits map[string]string
	local   []string // the services whose commit is the working copy
}

// readDeclaredBuilds is the one place that gives every command that takes --build its builds. It
// checks the values of --build against the services, and then adds the working copy.
func readDeclaredBuilds(ctx context.Context, dir string, values []string, services []config.Service) (declaredBuilds, error) {
	named, err := build.ParseDeclared(values, services)
	if err != nil {
		return declaredBuilds{}, err
	}
	var repo build.Repo
	if g, err := vcs.Open(dir); err == nil {
		repo = g
	}
	commits, local, err := build.WithLocal(ctx, named, services, repo)
	if err != nil {
		return declaredBuilds{}, err
	}
	return declaredBuilds{commits: commits, local: local}, nil
}

// has reports whether the service has a build that no probe reads.
func (d declaredBuilds) has(service string) bool {
	_, ok := d.commits[service]
	return ok
}

// prober answers with the declared builds, and reads every other build from its probe.
func (d declaredBuilds) prober(services []config.Service) build.Prober {
	return build.NewDeclared(d.commits, build.NewHTTP(services, nil), d.local...)
}
