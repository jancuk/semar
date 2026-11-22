package cli

import (
	"context"
	"slices"

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
	apk     []string // the services whose commit is the digest of their APK
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
	// The APK comes before the working copy: the app that runs is what a result belongs to.
	withAPK, apk := build.WithAPK(named, services, dir)
	commits, local, err := build.WithLocal(ctx, withAPK, services, repo)
	if err != nil {
		return declaredBuilds{}, err
	}
	return declaredBuilds{commits: commits, local: local, apk: apk}, nil
}

// has reports whether the service has a build that no probe reads.
func (d declaredBuilds) has(service string) bool {
	_, ok := d.commits[service]
	return ok
}

// isAPK reports whether the build of the service is the digest of its APK.
func (d declaredBuilds) isAPK(service string) bool { return slices.Contains(d.apk, service) }

// prober answers with the declared builds, and reads every other build from its probe.
func (d declaredBuilds) prober(services []config.Service) build.Prober {
	return build.NewDeclared(d.commits, build.NewHTTP(services, nil), d.local...).APK(d.apk...)
}
