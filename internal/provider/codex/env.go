package codex

import (
	"slices"
	"strings"
)

var (
	vendorPrefixes = []string{"ANTHROPIC_", "OPENAI_", "DEEPSEEK_", "GEMINI_", "SEMAR_"}
	vendorNames    = []string{"GOOGLE_API_KEY"}
)

// setupEnv is the person's environment without the model vendors' credentials, without Semar's
// own variables, and without drop. A vendor key would change who pays for the job, and drop
// holds the secrets that the repository's configuration names.
func setupEnv(environ []string, drop []string) []string {
	var env []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" || dropped(name, drop) {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func dropped(name string, drop []string) bool {
	for _, p := range vendorPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return slices.Contains(vendorNames, name) || slices.Contains(drop, name)
}
