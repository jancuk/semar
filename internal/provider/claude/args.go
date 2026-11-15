package claude

import (
	"strconv"

	"github.com/jancuk/semar/internal/provider"
)

// buildArgs never passes --bare, and --setting-sources takes one empty argument. Both keep
// the settings, hooks, and MCP servers of the repository under test from loading. The model
// is joined to its flag, so that a value cannot be read as another flag.
func buildArgs(job provider.Job, defaultModel string) []string {
	args := []string{
		"-p",
		"--output-format", "json",
		"--json-schema", string(job.Schema),
		"--tools", "Read,Grep,Glob",
		"--strict-mcp-config",
		"--setting-sources", "",
		"--no-session-persistence",
		"--max-turns", strconv.Itoa(job.MaxTurns),
	}
	model := job.Model
	if model == "" {
		model = defaultModel
	}
	if model != "" {
		args = append(args, "--model="+model)
	}
	return args
}
