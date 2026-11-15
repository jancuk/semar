package config

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/hostrule"
	"github.com/jancuk/semar/internal/idcheck"
)

var sources = []domain.Source{
	domain.SourceTicket, domain.SourceTestcase, domain.SourcePerson, domain.SourceDiff, domain.SourceBaseline,
}

var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,127}$`)

// CheckModel accepts a model name that cannot be read as a flag or hold a control character.
func CheckModel(model string) error {
	if !modelPattern.MatchString(model) {
		return fmt.Errorf("%q is not a model name: use up to 128 letters, digits, and . _ : / [ ] -, starting with a letter or digit", model)
	}
	return nil
}

var reasoningPattern = regexp.MustCompile(`^[a-z]{1,16}$`)

// CheckReasoning accepts a reasoning level that cannot be read as a flag or hold a control
// character: 1 to 16 lower-case letters. Each kind decides which levels it can pass on.
func CheckReasoning(level string) error {
	if !reasoningPattern.MatchString(level) {
		return fmt.Errorf("%q is not a reasoning level: use 1 to 16 lower-case letters, such as high", level)
	}
	return nil
}

// CheckDeny refuses a setup.deny entry that a harness could read as a flag or split into
// several. The person's file and the Claude driver both use it.
func CheckDeny(deny []string) error {
	for i, entry := range deny {
		var problem string
		switch {
		case strings.TrimSpace(entry) == "":
			problem = "is empty"
		case strings.HasPrefix(entry, "-"):
			problem = "starts with a dash"
		case strings.Contains(entry, ","):
			problem = "holds a comma; give each tool as its own entry"
		case strings.ContainsFunc(entry, unicode.IsControl):
			problem = "holds a control character"
		}
		if problem != "" {
			return fmt.Errorf("entry %d of setup.deny, %q, %s", i+1, entry, problem)
		}
	}
	return nil
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
	validatePlan(c, add)
	validateKnowledge(c, add)
	envNames := make([]string, 0, len(c.Environments))
	for n := range c.Environments {
		envNames = append(envNames, n)
	}
	slices.Sort(envNames)
	for _, n := range envNames {
		if err := idcheck.Check("environment name", n); err != nil {
			add("environments: %v", err)
		}
		validateEnvironment(n, c.Environments[n], add)
	}
	for _, n := range slices.Sorted(maps.Keys(c.AuthProfiles)) {
		if err := idcheck.Check("auth profile name", n); err != nil {
			add("authProfiles: %v", err)
		}
	}
	return joinErrors(errs)
}

func validateEnvironment(name string, e Environment, add func(string, ...any)) {
	p := "environments." + name
	if e.Kind != KindTest && e.Kind != KindProduction {
		add("%s.kind must be %s or %s, got %q", p, KindTest, KindProduction, e.Kind)
	}
	if len(e.Hosts) == 0 {
		add("%s.hosts needs at least one entry", p)
	}
	for i, h := range e.Hosts {
		if err := hostrule.CheckEntry(h); err != nil {
			add("%s.hosts[%d]: %v", p, i, err)
		}
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
		} else if err := idcheck.Check("service name", s.Name); err != nil {
			add("%s.name: %v", sp, err)
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

func validatePlan(c Config, add func(string, ...any)) {
	ids := map[string]bool{}
	for i, p := range c.Providers {
		pp := fmt.Sprintf("providers[%d]", i)
		switch err := idcheck.Check("provider id", p.ID); {
		case p.ID == "":
			add("%s.id is required", pp)
		case err != nil:
			add("%s.id: %v", pp, err)
		case ids[p.ID]:
			add("%s.id: %q is used twice", pp, p.ID)
		}
		ids[p.ID] = true
		if p.Kind == "" {
			add("%s.kind is required", pp)
		}
		if p.Model != "" {
			if err := CheckModel(p.Model); err != nil {
				add("%s.model: %v", pp, err)
			}
		}
		for j, o := range p.Outputs {
			if !filepath.IsLocal(o) {
				add("%s.outputs[%d]: %q must be a relative path inside the repository", pp, j, o)
			}
		}
	}
	if _, err := regexp.Compile(c.Tickets.Pattern); err != nil {
		add("tickets.pattern: %v", err)
	}
	// plan.provider may name a provider that only the person's own file defines, so the
	// command line checks that the name is known.
	if c.Plan.Provider != "" {
		if err := idcheck.Check("provider id", c.Plan.Provider); err != nil {
			add("plan.provider: %v", err)
		}
	}
	if c.Plan.MaxScenarios < 0 {
		add("plan.maxScenarios must be positive")
	}
	if c.Plan.MaxTurns < 0 {
		add("plan.maxTurns must be positive")
	}
	if c.Plan.Timeout < 0 {
		add("plan.timeout must be positive")
	}
	// The cleanup of old copies of the source keeps a copy for 7 days, which is more than the
	// three drafts of a plan can take at this limit.
	if c.Plan.Timeout > MaxPlanTimeout {
		add("plan.timeout must be at most %s", MaxPlanTimeout)
	}
}

// MaxPlanTimeout is the longest time that one planner draft may take.
const MaxPlanTimeout = 24 * time.Hour
