package codex

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

// profileName names the permission profile that every job defines for itself. The whole table
// is set on the command line, so a profile of the same name in the person's file adds nothing.
const profileName = "semar"

// alwaysOff are the features that no job uses: hooks run programs, memories read and write
// outside the working directory, the computer and browser tools act outside the sandbox, and the
// rest start work that a planner does not need. Codex refuses a name it does not know, so the
// list names features of the installed version only.
var alwaysOff = []string{
	"hooks", "memories", "computer_use", "browser_use", "browser_use_external", "in_app_browser",
	"image_generation", "multi_agent", "tool_suggest", "skill_mcp_dependency_install",
}

type argSpec struct {
	workdir    string // resolved, absolute
	schemaFile string // "" when the schema goes in the prompt
	model      string
	reasoning  string
	setup      config.Setup
	skillRoots []string
	repoSkills []string
}

// buildArgs never passes --sandbox read-only, which lets commands read the whole disk, and never
// --trust, which would write to the person's configuration. The model is joined to its flag, so
// that a value cannot be read as another flag.
func buildArgs(s argSpec) ([]string, error) {
	args := []string{"exec", "--json", "--ephemeral", "--skip-git-repo-check", "--ignore-rules", "--cd", s.workdir}
	if s.schemaFile != "" {
		args = append(args, "--output-schema", s.schemaFile)
	}
	if s.model != "" {
		args = append(args, "--model="+s.model)
	}
	off := slices.Clone(alwaysOff)
	switch {
	case !s.setup.MCP && !s.setup.Skills:
		args = append(args, "--ignore-user-config")
		off = append(off, "plugins", "apps")
	case !s.setup.MCP:
		off = append(off, "plugins", "apps")
	}
	for _, f := range off {
		args = append(args, "--disable", f)
	}

	dir, err := tomlString(s.workdir)
	if err != nil {
		return nil, err
	}
	// The temporary directories stay denied. A model that sees its working directory inside a
	// denied directory may stop without reading, so the copy of the source is made elsewhere.
	readable := []string{`":minimal"="read"`, `":tmpdir"="deny"`, `":slash_tmp"="deny"`, dir + `="read"`}
	for _, root := range s.skillRoots {
		q, err := tomlString(root)
		if err != nil {
			return nil, err
		}
		readable = append(readable, q+`="read"`)
	}
	overrides := []string{
		`approval_policy="never"`,
		`web_search="disabled"`,
		`allow_login_shell=false`,
		// The commands that the model runs get only the core variables, such as PATH and HOME,
		// even when Codex itself has the person's environment for its MCP servers.
		`shell_environment_policy={inherit="core", experimental_use_profile=false}`,
		`project_doc_max_bytes=0`,
		`project_root_markers=[]`,
		`projects={` + dir + `={trust_level="untrusted"}}`,
		`default_permissions="` + profileName + `"`,
		`permissions.` + profileName + `={filesystem={` + strings.Join(readable, ", ") + `}}`,
	}
	if s.reasoning != "" {
		q, err := tomlString(s.reasoning)
		if err != nil {
			return nil, err
		}
		overrides = append(overrides, "model_reasoning_effort="+q)
	}
	if s.setupOn() && !s.setup.MCP {
		overrides = append(overrides, `mcp_servers={}`)
	}
	if s.setup.Skills {
		var entries []string
		for _, p := range s.repoSkills {
			q, err := tomlString(p)
			if err != nil {
				return nil, err
			}
			entries = append(entries, "{path="+q+", enabled=false}")
		}
		if len(entries) > 0 {
			overrides = append(overrides, "skills.config=["+strings.Join(entries, ", ")+"]")
		}
	} else {
		overrides = append(overrides, "skills.include_instructions=false", "skills.bundled.enabled=false")
	}
	for _, o := range overrides {
		args = append(args, "--config", o)
	}
	return append(args, "-"), nil
}

func (s argSpec) setupOn() bool { return s.setup.MCP || s.setup.Skills }

// tomlString quotes a path as a TOML basic string.
func tomlString(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", &provider.Error{Class: provider.ValidationError, Detail: fmt.Sprintf("the path %q is not valid UTF-8", s)}
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}
