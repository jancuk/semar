package changeset

import "github.com/bmatcuk/doublestar/v4"

// kindRules are tried in order, so a test file in a docs directory is a test
// and a JSON file in a migrations directory is a migration.
var kindRules = []struct {
	kind     string
	patterns []string
}{
	{"test", []string{"**/*_test.go", "**/*.test.*", "**/__tests__/**", "**/testdata/**"}},
	{"docs", []string{"**/*.md", "docs/**"}},
	{"generated", []string{"**/*.pb.go", "**/*_gen.go"}},
	{"migration", []string{"migrations/**", "**/migrations/**"}},
	{"config", []string{
		"**/*.yaml", "**/*.yml", "**/*.json", "**/*.toml",
		"**/.semar/**",
		"deployments/**", ".github/**", ".circleci/**",
		"**/bitbucket-pipelines.*", "**/.gitlab-ci.*", "**/Jenkinsfile*", "**/azure-pipelines.*", "**/Dockerfile*",
	}},
}

// Kind classifies a slash-separated path by the default path rules.
func Kind(path string) string {
	for _, r := range kindRules {
		for _, p := range r.patterns {
			if ok, _ := doublestar.Match(p, path); ok {
				return r.kind
			}
		}
	}
	return "source"
}
