package build

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Declared answers with a fixed commit for each service that a person declared, and asks next
// for every other service. A declared build is recorded as declared, because nothing read it
// from the service: it cannot show that the service was redeployed during a run.
type Declared struct {
	commits map[string]string
	local   map[string]bool
	next    Prober
}

// NewDeclared takes the commits, and the services among them whose commit is the working copy that
// WithLocal read, not one that a person named.
func NewDeclared(commits map[string]string, next Prober, local ...string) *Declared {
	d := &Declared{commits: maps.Clone(commits), local: map[string]bool{}, next: next}
	for _, name := range local {
		d.local[name] = true
	}
	return d
}

func (d *Declared) Probe(ctx context.Context, service string) (domain.BuildRef, error) {
	if commit, ok := d.commits[service]; ok {
		return domain.BuildRef{Service: service, Commit: commit, Declared: true, Local: d.local[service]}, nil
	}
	if d.next == nil {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: the service has no build probe and no declared build", service)
	}
	return d.next.Probe(ctx, service)
}

// ParseDeclared reads values of the form <commit> or <service>=<commit>, and returns the
// commit of each service. A service may be declared only when it has no build probe, because a
// probe reads the build from the service itself. The bare form names the one service of the
// environment that has no probe.
func ParseDeclared(values []string, services []config.Service) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	var names, unprobed []string
	probed := map[string]bool{}
	for _, s := range services {
		names = append(names, s.Name)
		if s.Build != nil {
			probed[s.Name] = true
		} else {
			unprobed = append(unprobed, s.Name)
		}
	}
	out := map[string]string{}
	for _, v := range values {
		service, commit, named := strings.Cut(v, "=")
		if !named {
			service, commit = "", v
		}
		if !commitPattern.MatchString(commit) {
			return nil, fmt.Errorf("--build %q: a commit is 7 to 40 lower-case hexadecimal characters", v)
		}
		switch {
		case !named && len(unprobed) == 0:
			return nil, fmt.Errorf("--build %s: every service of the environment has a build probe, which reads its build", v)
		case !named && len(unprobed) > 1:
			return nil, fmt.Errorf("--build %s: %s have no build probe, so name the service: --build <service>=<commit>", v, strings.Join(unprobed, ", "))
		case !named:
			service = unprobed[0]
		case !slices.Contains(names, service):
			return nil, fmt.Errorf("--build %s: the environment has no service %q; its services are %s", v, service, strings.Join(names, ", "))
		case probed[service]:
			return nil, fmt.Errorf("--build %s: service %s has a build probe, which reads its build", v, service)
		}
		if _, twice := out[service]; twice {
			return nil, fmt.Errorf("--build names service %s more than once", service)
		}
		out[service] = commit
	}
	return out, nil
}
