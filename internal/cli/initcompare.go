package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/config"
)

const configFileName = ".semar/semar.yaml"

// givenInitFlags returns the names of the setup flags that the person set, so that a default
// is never compared with the file.
func givenInitFlags(cmd *cobra.Command) map[string]bool {
	given := map[string]bool{}
	for _, name := range initFlagNames {
		given[name] = cmd.Flags().Changed(name)
	}
	return given
}

// diffInitFlags compares the setup flags that were given with the environment that the command
// would use, and returns one line for each flag that differs.
func diffInitFlags(cfg config.Config, envFlag, profileFlag string, f initFlags, given map[string]bool) ([]string, error) {
	envName, env, err := cfg.Environment(envFlag)
	if err != nil {
		return nil, err
	}
	where := "for the environment " + terminalSafe(envName)
	var diffs []string
	differ := func(flag, got, have string) {
		diffs = append(diffs, fmt.Sprintf("  --%s is %s, and the file has %s", flag, terminalSafe(got), have))
	}
	list := func(vals []string) string {
		if len(vals) == 0 {
			return "none"
		}
		safe := make([]string, len(vals))
		for i, v := range vals {
			safe[i] = terminalSafe(v)
		}
		return strings.Join(safe, ", ")
	}

	byName := env.Services
	if given["service"] {
		byName = nil
		for _, s := range env.Services {
			if s.Name == f.service {
				byName = append(byName, s)
			}
		}
		if len(byName) == 0 {
			var names []string
			for _, s := range env.Services {
				names = append(names, s.Name)
			}
			differ("service", f.service, list(names)+" "+where)
		}
	}
	cands := byName
	if given["base-url"] {
		trim := func(s string) string { return strings.TrimSuffix(s, "/") }
		var match []config.Service
		var have []string
		for _, s := range byName {
			have = append(have, s.BaseURL)
			if trim(s.BaseURL) == trim(f.baseURL) {
				match = append(match, s)
			}
		}
		if len(match) == 0 {
			if len(byName) > 0 || !given["service"] {
				differ("base-url", f.baseURL, list(have)+" "+where)
			}
		} else {
			cands = match
		}
	}
	if given["kind"] {
		kind := env.Kind
		if kind == "" {
			kind = config.KindTest
		}
		if kind != f.kind {
			differ("kind", f.kind, kind+" "+where)
		}
	}

	probe := func(flag, want string, field func(*config.BuildProbe) string) {
		var have []string
		for _, s := range cands {
			if s.Build == nil {
				continue
			}
			if v := field(s.Build); v == want {
				return
			} else {
				have = append(have, v)
			}
		}
		if len(have) == 0 {
			diffs = append(diffs, fmt.Sprintf("  --%s is %s, and the file has no build probe %s", flag, terminalSafe(want), where))
			return
		}
		differ(flag, want, list(have)+" "+where)
	}
	if given["build-url"] {
		probe("build-url", f.buildURL, func(b *config.BuildProbe) string { return b.URL })
	}
	if given["build-commit"] {
		probe("build-commit", f.buildCommit, func(b *config.BuildProbe) string { return b.Commit })
	}

	if given["auth-header"] || given["auth-value"] || given["token-env"] {
		name, err := pickAuthProfile(cfg, profileFlag)
		if err != nil {
			return nil, err
		}
		prof := cfg.AuthProfiles[name]
		at := "for the auth profile " + terminalSafe(name)
		if given["auth-header"] {
			switch {
			case prof.Session == nil:
				differ("auth-header", f.authHeader, "no session "+at)
			case !strings.EqualFold(prof.Session.Header, f.authHeader):
				differ("auth-header", f.authHeader, terminalSafe(prof.Session.Header)+" "+at)
			}
		}
		if given["auth-value"] && (prof.Session == nil || prof.Session.Value != f.authValue) {
			diffs = append(diffs, "  --auth-value differs from the file "+at)
		}
		if given["token-env"] {
			ref := prof.Secrets["TOKEN"]
			if ref != "env:"+f.tokenEnv {
				have := "a secret that is not an environment variable"
				if n, ok := strings.CutPrefix(ref, "env:"); ok {
					have = n
				} else if ref == "" {
					have = "no TOKEN secret"
				}
				differ("token-env", f.tokenEnv, have+" "+at)
			}
		}
	}
	if given["account"] {
		accounts, err := parseAccounts(f.accounts)
		if err != nil {
			return nil, err
		}
		for _, a := range accounts {
			prof, ok := cfg.AuthProfiles[a.name]
			switch ref := prof.Secrets["TOKEN"]; {
			case !ok:
				diffs = append(diffs, fmt.Sprintf("  --account %s is not an auth profile in the file", terminalSafe(a.name)))
			case ref != "env:"+a.env:
				diffs = append(diffs, fmt.Sprintf("  --account %s uses %s, and the file has %s", terminalSafe(a.name), terminalSafe(a.env), terminalSafe(ref)))
			}
		}
	}
	slices.Sort(diffs)
	return diffs, nil
}

func initFlagsDiffer(diffs []string, advice string) error {
	return fmt.Errorf("%s exists and differs from the flags:\n%s\n%s", configFileName, strings.Join(diffs, "\n"), advice)
}
