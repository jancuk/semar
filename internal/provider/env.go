package provider

import "strings"

var childEnvNames = []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "TERM", "TMPDIR"}

// ChildEnv returns the only variables a harness inherits. Vendor keys and every other
// variable stay behind, so that a stray one cannot change who pays or which rules apply.
func ChildEnv(lookup func(string) (string, bool)) []string {
	var env []string
	for _, name := range childEnvNames {
		if v, ok := lookup(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// A variable with one of these prefixes, or one of these names, never reaches a harness
// that runs with the person's setup. The vendor variables hold keys and addresses, which
// would change who pays or where the source goes; the harness uses its own sign-in. The
// session markers tell a harness that it runs inside another agent session, which changes
// how it behaves, and some of them hold a token of that session.
var (
	setupDropPrefixes = []string{
		"ANTHROPIC_", "OPENAI_", "AZURE_OPENAI_", "DEEPSEEK_", "GEMINI_", "GOOGLE_GENAI_",
		"OPENROUTER_", "MISTRAL_", "GROQ_", "XAI_",
		"SEMAR_",
		"CLAUDE_CODE_", "CLAUDE_AGENT_SDK_", "CLAUDE_PLUGIN_",
	}
	setupDropNames = []string{"GOOGLE_API_KEY", "CLAUDECODE", "CLAUDE_PID", "CLAUDE_EFFORT", "AI_AGENT"}
)

// SetupEnv returns the environment of a harness that runs with the person's setup: environ,
// normally os.Environ(), without the variables of model vendors, of Semar, and of an
// enclosing agent session, and without the names in drop, such as the variables that
// semar.yaml names as secrets. Names are compared without case, because Windows ignores it.
func SetupEnv(environ []string, drop []string) []string {
	dropped := make(map[string]bool, len(drop)+len(setupDropNames))
	for _, names := range [][]string{drop, setupDropNames} {
		for _, name := range names {
			dropped[strings.ToUpper(name)] = true
		}
	}
	var env []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		upper := strings.ToUpper(name)
		if dropped[upper] || hasAnyPrefix(upper, setupDropPrefixes) {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
