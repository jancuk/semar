package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/journey"
	"github.com/jancuk/semar/internal/screens"
)

// The state of one line of the check of a machine.
const (
	stateOK      = "ok"
	stateMissing = "missing"
	stateReview  = "review"
)

type checkItem struct{ state, text string }

// machineCheck is the list of what this machine has and lacks for the repository's semar.yaml.
type machineCheck struct{ items []checkItem }

func (m *machineCheck) add(state, format string, args ...any) {
	m.items = append(m.items, checkItem{state, oneLine(terminalSafe(fmt.Sprintf(format, args...)))})
}

func (m *machineCheck) missing() int {
	n := 0
	for _, it := range m.items {
		if it.state == stateMissing {
			n++
		}
	}
	return n
}

// checkMachine is semar init in a repository that is set up. It writes nothing. It prints one line
// for each item that a run needs from this machine, with the way to fix what is missing, and ends
// with exit code 2 when something is missing.
func checkMachine(ctx context.Context, o options, stdout io.Writer, dir string) error {
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s exists: checking this machine.\n\n", configFileName)
	var m machineCheck
	m.tokens(o, cfg)
	m.services(ctx, o, cfg)
	m.provider(ctx, o, dir, cfg)
	m.chrome(dir, cfg)
	m.approvals(dir, cfg)
	for _, it := range m.items {
		fmt.Fprintf(stdout, "  %-7s  %s\n", it.state, it.text)
	}
	fmt.Fprintln(stdout)
	if n := m.missing(); n > 0 {
		fmt.Fprintf(stdout, "%d missing. Next: fix them, then semar run\n", n)
		return exitError{ExitUndecided}
	}
	fmt.Fprintln(stdout, "Nothing is missing. Next: semar run")
	return nil
}

// tokens checks the environment variable of each secret of each auth profile, once each.
func (m *machineCheck) tokens(o options, cfg config.Config) {
	seen := map[string]bool{}
	names := slices.Sorted(maps.Keys(cfg.AuthProfiles))
	if i := slices.Index(names, "default"); i > 0 {
		names = slices.Concat([]string{"default"}, slices.Delete(names, i, i+1))
	}
	for _, name := range names {
		prof := cfg.AuthProfiles[name]
		for _, key := range slices.Sorted(maps.Keys(prof.Secrets)) {
			env, ok := strings.CutPrefix(prof.Secrets[key], "env:")
			if !ok || seen[env] {
				continue
			}
			seen[env] = true
			if v, set := o.LookupEnv(env); set && v != "" {
				m.add(stateOK, "%s is set", env)
				continue
			}
			m.add(stateMissing, "%s is not set: export it with the token of the account %q", env, name)
		}
		if prof.Web == nil {
			continue
		}
		env := prof.Web.PasswordEnv()
		if seen[env] {
			continue
		}
		seen[env] = true
		if v, set := o.LookupEnv(env); set && v != "" {
			m.add(stateOK, "%s is set", env)
			continue
		}
		m.add(stateMissing, "%s is not set: export it with the web password of the account %q", env, name)
	}
}

// services reads the build probe of each service of each test environment, and for a service with
// none, one GET to its address to see that it answers.
func (m *machineCheck) services(ctx context.Context, o options, cfg config.Config) {
	client := o.HTTP
	if client == nil {
		client = &http.Client{Timeout: probeTimeout}
	}
	for _, envName := range slices.Sorted(maps.Keys(cfg.Environments)) {
		env := cfg.Environments[envName]
		if env.Kind == config.KindProduction {
			continue
		}
		prober := build.NewHTTP(env.Services, client)
		for _, s := range env.Services {
			if s.Build != nil {
				b, err := prober.Probe(ctx, s.Name)
				if err != nil {
					m.add(stateMissing, "%s: %s did not answer its build probe at %s: %s: start the service, or correct the address", envName, s.Name, s.Build.URL, probeReason(err))
					continue
				}
				m.add(stateOK, "%s: %s answers, build %s", envName, s.Name, b.Commit[:min(7, len(b.Commit))])
				continue
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL, nil)
			if err == nil {
				var resp *http.Response
				if resp, err = client.Do(req); err == nil {
					resp.Body.Close()
				}
			}
			if err != nil {
				m.add(stateMissing, "%s: %s did not answer at %s: start the service, or correct the address", envName, s.Name, s.BaseURL)
				continue
			}
			m.add(stateOK, "%s: %s answers; it has no build probe, so name the build with semar run --build <commit>", envName, s.Name)
		}
	}
}

// probeReason is the part of a probe error that a person can act on.
func probeReason(err error) string {
	return strings.TrimPrefix(err.Error(), "build probe for service ")
}

// provider checks that the model provider that would plan is installed.
func (m *machineCheck) provider(ctx context.Context, o options, dir string, cfg config.Config) {
	src, err := loadSources(o, dir, cfg)
	if err != nil {
		m.add(stateMissing, "your providers file cannot be read: %v", err)
		return
	}
	pc, _, err := src.choose("")
	if err != nil {
		m.add(stateMissing, "no model provider can plan: %v", err)
		return
	}
	o.drop.set(append(secretEnvNames(cfg), keyEnvNames(src.file)...))
	inst, err := o.Providers(dir).New(pc)
	if err != nil {
		m.add(stateMissing, "the provider %s cannot plan: %v", pc.ID, err)
		return
	}
	if h := inst.Health(ctx); !h.Installed {
		m.add(stateMissing, "the provider %s cannot plan: %s", inst.ID(), h.Detail)
		return
	}
	m.add(stateOK, "the provider %s would plan", inst.ID())
}

// chrome is checked only when a scenario or a leg of a journey has a web step.
func (m *machineCheck) chrome(dir string, cfg config.Config) {
	scs, _ := loadAllScenarios(dir)
	n := 0
	for _, sc := range scs {
		if sc.Surface == domain.SurfaceWeb {
			n++
		}
	}
	js, _, _ := journey.Load(dir)
	for _, j := range js {
		if slices.ContainsFunc(j.Legs, func(l domain.Leg) bool { return l.Surface == domain.SurfaceWeb }) {
			n++
		}
	}
	if n == 0 {
		return
	}
	configured := ""
	if _, env, err := cfg.Environment(""); err == nil {
		configured = env.Web.Chrome
	}
	if path, err := web.FindChrome(configured); err != nil {
		m.add(stateMissing, "Chrome was not found: %s", webStepsWords(n))
	} else {
		m.add(stateOK, "Chrome is at %s", path)
	}
}

func webStepsWords(n int) string {
	if n == 1 {
		return "1 scenario has web steps"
	}
	return fmt.Sprintf("%d scenarios have web steps", n)
}

// approvals counts what a person has not approved yet: scenarios with an expected result that needs a
// person, fixtures, and screens files.
func (m *machineCheck) approvals(dir string, cfg config.Config) {
	approvals := confirm.NewLocal(semarDir(dir))
	var parts []string
	scs, _ := loadAllScenarios(dir)
	n := 0
	for _, sc := range scs {
		if !slices.ContainsFunc(sc.Steps, func(st domain.Step) bool {
			return slices.ContainsFunc(st.Expect, func(e domain.Expectation) bool { return confirm.NeedsExpectation(e, cfg.Policy.Trust.Confirm) })
		}) {
			continue
		}
		rec, found, err := approvals.Get(sc.ID, domain.ExpectHash(sc))
		if err != nil || !found || !confirmedAll(sc, rec, cfg) {
			n++
		}
	}
	if n > 0 {
		parts = append(parts, plural(n, "scenario"))
	}
	if cat, err := fixture.Load(dir, allServices(cfg), fixtureAccounts(cfg)); err == nil {
		n = 0
		for _, fx := range cat.Fixtures {
			if ok, err := approvals.FixtureApproved(fx.ID, domain.FixtureHash(fx)); err != nil || !ok {
				n++
			}
		}
		if n > 0 {
			parts = append(parts, plural(n, "fixture"))
		}
	}
	n = 0
	files, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(screens.RelDir), "*.yaml"))
	for _, f := range files {
		app := strings.TrimSuffix(filepath.Base(f), ".yaml")
		scr, err := screens.Load(dir, app)
		if err != nil {
			continue
		}
		if ok, err := approvals.ScreensApproved(app, domain.ScreensHash(scr)); err != nil || !ok {
			n++
		}
	}
	if n > 0 {
		parts = append(parts, plural(n, "screens file"))
	}
	if len(parts) == 0 {
		return
	}
	list := strings.Join(parts, ", ")
	if i := strings.LastIndex(list, ", "); i >= 0 {
		list = list[:i] + " and " + list[i+2:]
	}
	verb := "are"
	if len(parts) == 1 && strings.HasPrefix(parts[0], "1 ") {
		verb = "is"
	}
	m.add(stateReview, "%s %s not approved → semar review", list, verb)
}

// confirmedAll says whether the record holds every source that the scenario's expected results need.
func confirmedAll(sc domain.Scenario, rec confirm.Record, cfg config.Config) bool {
	for _, st := range sc.Steps {
		for _, e := range st.Expect {
			if confirm.NeedsExpectation(e, cfg.Policy.Trust.Confirm) && !slices.Contains(rec.Sources, e.Source) {
				return false
			}
		}
	}
	return true
}
