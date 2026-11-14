package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/hostrule"
)

var sources = []domain.Source{
	domain.SourceTicket, domain.SourceTestcase, domain.SourcePerson, domain.SourceDiff, domain.SourceBaseline,
}

func joinErrors(errs []error) error { return errors.Join(errs...) }

func validate(c Config, name string) error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{name}, args...)...))
	}
	if c.Version != 1 {
		add("version must be 1")
	}
	if c.Replay.Workers < 0 {
		add("replay.workers must not be negative")
	}
	for _, s := range c.Policy.Trust.Confirm {
		if !slices.Contains(sources, s) {
			add("policy.trust.confirm: %s is not a source", s)
		}
	}
	envNames := make([]string, 0, len(c.Environments))
	for n := range c.Environments {
		envNames = append(envNames, n)
	}
	slices.Sort(envNames)
	for _, n := range envNames {
		validateEnvironment(n, c.Environments[n], add)
	}
	return joinErrors(errs)
}

func validateEnvironment(name string, e Environment, add func(string, ...any)) {
	p := "environments." + name
	if e.Kind != "test" {
		add("%s.kind must be test, got %q", p, e.Kind)
	}
	if len(e.Hosts) == 0 {
		add("%s.hosts needs at least one entry", p)
	}
	if len(e.Methods) == 0 {
		add("%s.methods needs at least one entry", p)
	}
	if e.Effects.Mode != "test_data" && e.Effects.Mode != "read_only" {
		add("%s.effects.mode is required: test_data or read_only", p)
	}
	for i, s := range e.Services {
		sp := fmt.Sprintf("%s.services[%d]", p, i)
		if s.Name == "" {
			add("%s.name is required", sp)
		}
		u, err := url.Parse(s.BaseURL)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "":
			add("%s.baseUrl must be an absolute http or https URL, got %q", sp, s.BaseURL)
		case !hostrule.Allowed(u, e.Hosts):
			add("%s: baseUrl host %s is not in hosts", sp, originHost(u))
		}
	}
}

// originHost returns host[:port] for an error message, with the default port
// of the scheme dropped.
func originHost(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if port == "" || (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		return host
	}
	return host + ":" + port
}
