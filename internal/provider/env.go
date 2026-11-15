package provider

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
