package claude

import (
	"slices"
	"strconv"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

// setupSettings is passed with the person's setup. Hooks run programs and add a long text to
// every job, so none runs. The person's permission mode can let the file tools read outside
// the working directory without asking, which Claude Code 2.1.325 did under the mode "auto";
// the read block refuses that in every mode and cannot be approved by a classifier.
const setupSettings = `{"disableAllHooks":true,"permissions":{"blockReadsOutsideWorkingDirectories":true}}`

// setupEnvExtra keeps the person's instruction files and the automatic memory out of a job
// with the setup. --setting-sources user would load the instruction file of the person.
var setupEnvExtra = []string{"CLAUDE_CODE_DISABLE_CLAUDE_MDS=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"}

// Levels are the values of the --effort flag of the installed version, weakest first.
var Levels = []string{"low", "medium", "high", "xhigh", "max"}

func checkLevel(level string) error {
	if level == "" || slices.Contains(Levels, level) {
		return nil
	}
	return provider.RefuseReasoning(Kind, level, Levels)
}

func usesSetup(s config.Setup) bool { return s.MCP || s.Skills }

// buildArgs never passes --bare, which turns off the subscription sign-in. Without the setup,
// --setting-sources takes one empty argument, and with it only the user's own settings load;
// either way the settings, hooks, and MCP servers of the repository under test stay out. The
// model is joined to its flag, so that a value cannot be read as another flag. The deny
// entries follow their flag, which takes several values, and the next flag ends the list.
func buildArgs(job provider.Job, cfg config.Provider) []string {
	s := cfg.Setup
	setup := usesSetup(s)
	// The stream is the only format that shows the job's progress while it runs.
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	tools := "Read,Grep,Glob"
	if s.Skills {
		tools += ",Skill"
	}
	args = append(args, "--json-schema", string(job.Schema), "--tools", tools)
	if !s.MCP {
		args = append(args, "--strict-mcp-config")
	}
	if setup {
		args = append(args, "--setting-sources", "user", "--settings", setupSettings)
	} else {
		args = append(args, "--setting-sources", "")
	}
	if len(s.Deny) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, s.Deny...)
	}
	args = append(args, "--no-session-persistence", "--max-turns", strconv.Itoa(job.MaxTurns))
	model := job.Model
	if model == "" {
		model = cfg.Model
	}
	if model != "" {
		args = append(args, "--model="+model)
	}
	if level := provider.JobReasoning(job, cfg); level != "" {
		args = append(args, "--effort="+level)
	}
	return args
}
