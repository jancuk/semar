// Package codex drives the Codex command line, `codex exec`, as an agent provider.
//
// A job runs `codex exec --json --ephemeral` in the working directory, with the prompt on
// standard input, no approval prompts, and no web search. The read-only sandbox of Codex lets a
// command read every file of the machine, so the driver does not use it: it defines a permission
// profile of its own that lets commands read the working directory and the system's own files,
// and nothing else. Writing and the network are refused. Hooks, memories, the computer, browser
// and image tools, sub-agents, and the execpolicy rule files are always off. The instruction
// files of the working directory are not read, and the working directory is marked as an
// untrusted project, so a .codex directory inside it does not load. The commands that the model
// runs inherit only the core variables of Codex's shell_environment_policy, such as PATH and
// HOME, whatever environment Codex itself has.
//
// The setup switches:
//
//   - Both off: --ignore-user-config, plugins and apps off, and no skill is offered. The
//     sign-in still works, because Codex reads it from its home directory either way.
//   - MCP on: the person's config.toml loads, with its MCP servers, plugins, apps, model, and
//     whatever else it defines.
//   - MCP off and skills on: config.toml loads, its MCP servers are emptied, and plugins and
//     apps, which bring MCP servers of their own, are off.
//   - Skills on: the person's skill directories become readable. The skills of the working
//     directory are switched off one by one.
//   - Skills off: no skill is offered to the model.
//   - Deny: Codex cannot refuse single tools, so New refuses a provider with setup.deny.
//
// Weaker than the Claude driver:
//
//   - The person's own instruction file, AGENTS.md in the Codex home, loads in every mode.
//     Codex has no switch for it that keeps the sign-in.
//   - The person's execpolicy rules never load, so their refusals of commands do not apply.
//   - There is no turn limit: MaxTurns is checked and not passed on.
//   - Codex sends the output schema in the strict mode of the OpenAI API. A schema that the
//     strict mode refuses, such as one with optional properties, is not passed: the prompt asks
//     for it, and the driver checks only that the answer is one JSON object.
//   - The events name neither the model, nor the MCP servers that loaded, nor every tool call.
//     Model is the requested model, Loaded stays empty, and Tools counts the calls that the
//     events show. Turns counts Codex turns, one per job.
//   - The profile was verified on macOS. On Linux, Codex enforces it with its own sandbox.
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

const (
	Kind = "codex"
	// BinaryEnv names the variable that holds the absolute path of the harness binary.
	// The path is never read from the configuration, which the repository under test owns.
	BinaryEnv     = "SEMAR_CODEX_BIN"
	defaultBinary = "codex"
	healthTimeout = 10 * time.Second
	statusTimeout = 10 * time.Second
)

type Options struct {
	Lookup   func(string) (string, bool) // normally os.LookupEnv
	Environ  func() []string             // normally os.Environ; nil means os.Environ
	RepoRoot string                      // no harness binary inside it is started; "" skips the check
	DropEnv  []string                    // variables the child never inherits, such as the secrets of semar.yaml
}

type Driver struct {
	cfg config.Provider
	o   Options
}

func New(cfg config.Provider, o Options) (*Driver, error) {
	if o.Lookup == nil {
		return nil, errors.New("codex: no environment lookup")
	}
	if o.Environ == nil {
		o.Environ = os.Environ
	}
	if len(cfg.Setup.Deny) > 0 {
		return nil, fmt.Errorf("provider %q: Codex cannot refuse single tools, so setup.deny is not supported; remove it, or turn setup.mcp off to keep the MCP servers out", cfg.ID)
	}
	if cfg.Model != "" {
		if err := config.CheckModel(cfg.Model); err != nil {
			return nil, fmt.Errorf("provider %q: %w", cfg.ID, err)
		}
	}
	if cfg.Reasoning != "" {
		if err := config.CheckReasoning(cfg.Reasoning); err != nil {
			return nil, fmt.Errorf("provider %q: %w", cfg.ID, err)
		}
	}
	return &Driver{cfg: cfg, o: o}, nil
}

func Factory(o Options) provider.Factory {
	return func(cfg config.Provider) (provider.Instance, error) {
		d, err := New(cfg, o)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
}

func (d *Driver) ID() string   { return d.cfg.ID }
func (d *Driver) Kind() string { return Kind }

func (d *Driver) binary(workdir string) (string, *provider.Error) {
	path, perr := provider.FindBinary(d.o.Lookup, BinaryEnv, defaultBinary)
	if perr == nil {
		perr = provider.RefuseInside(path, d.o.RepoRoot, workdir)
	}
	return path, perr
}

func (d *Driver) setupOn() bool { return d.cfg.Setup.MCP || d.cfg.Setup.Skills }

// env is the environment of Codex itself, not of the commands that the model runs. With the setup
// on, it is the person's, which their MCP servers may need. With the setup off, it is the short
// list of every harness and the variable that moves the Codex home, where the sign-in lives.
func (d *Driver) env() []string {
	if d.setupOn() {
		return provider.SetupEnv(d.o.Environ(), d.o.DropEnv)
	}
	env := provider.ChildEnv(d.o.Lookup)
	if v, ok := d.o.Lookup("CODEX_HOME"); ok {
		env = append(env, "CODEX_HOME="+v)
	}
	return env
}

// Health starts the binary with --version only, never a model turn.
func (d *Driver) Health(ctx context.Context) provider.Health {
	path, perr := d.binary("")
	if perr != nil {
		return provider.Health{Detail: perr.Detail}
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	res, err := provider.RunProcess(ctx, provider.ProcessSpec{Path: path, Args: []string{"--version"}, Env: d.env()})
	switch {
	case err != nil:
		return provider.Health{Detail: fmt.Sprintf("%s --version: %v", path, err)}
	case res.ExitCode != 0:
		return provider.Health{Detail: fmt.Sprintf("%s --version ended with exit code %d: %s", path, res.ExitCode, provider.LastLine(res.StderrTail))}
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(res.Stdout)), "\n")
	return provider.Health{Installed: true, Version: strings.TrimSpace(version)}
}

func (d *Driver) Run(ctx context.Context, job provider.Job) (provider.Result, error) {
	if err := checkJob(job); err != nil {
		return provider.Result{}, err
	}
	model := job.Model
	if model == "" {
		model = d.cfg.Model
	}
	if model != "" {
		if err := config.CheckModel(model); err != nil {
			return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: err.Error()}
		}
	}
	level := provider.JobReasoning(job, d.cfg)
	if level != "" {
		if err := config.CheckReasoning(level); err != nil {
			return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: err.Error()}
		}
	}
	workdir, err := filepath.EvalSymlinks(job.Workdir)
	if err != nil {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: fmt.Sprintf("the working directory %q cannot be resolved", job.Workdir)}
	}
	path, perr := d.binary(workdir)
	if perr != nil {
		return provider.Result{}, perr
	}

	prompt := job.Prompt
	schemaFile := ""
	if strictSchema(job.Schema) {
		f, cleanup, err := writeSchema(job.Schema, workdir)
		if err != nil {
			return provider.Result{}, err
		}
		defer cleanup()
		schemaFile = f
	} else {
		prompt += schemaPrompt(job.Schema)
	}
	args, err := buildArgs(argSpec{
		workdir:    workdir,
		schemaFile: schemaFile,
		model:      model,
		reasoning:  level,
		setup:      d.cfg.Setup,
		skillRoots: d.skillRoots(),
		repoSkills: repoSkills(workdir),
		noTools:    job.NoTools,
	})
	if err != nil {
		return provider.Result{}, err
	}

	runCtx := ctx
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}
	spec := provider.ProcessSpec{
		Path:  path,
		Args:  args,
		Dir:   workdir,
		Env:   d.env(),
		Stdin: strings.NewReader(prompt),
	}
	if job.Progress != nil {
		live := newTally()
		spec.OnStdoutLine = func(line []byte) {
			if ev, ok := decodeEvent(line); ok && live.add(ev) {
				job.Progress(live.now)
			}
		}
	}
	res, err := provider.RunProcess(runCtx, spec)
	switch {
	case err == nil:
		out, perr := parseEvents(res.Stdout, res.ExitCode, res.StderrTail)
		if perr != nil {
			return provider.Result{}, perr
		}
		out.Model = model
		out.Reasoning = level
		return out, nil
	case errors.Is(err, context.DeadlineExceeded):
		return provider.Result{}, &provider.Error{Class: provider.TransportError, Detail: "the harness did not finish in " + deadlineText(job)}
	case errors.Is(err, context.Canceled):
		return provider.Result{}, fmt.Errorf("run codex: %w", ctx.Err())
	case errors.Is(err, provider.ErrOutputTooLarge):
		return provider.Result{}, &provider.Error{Class: provider.ProviderError, Detail: "the harness wrote more than 16 MiB of output"}
	case errors.Is(err, os.ErrNotExist):
		return provider.Result{}, &provider.Error{Class: provider.NotInstalled, Detail: fmt.Sprintf("the binary %q could not be started", path)}
	}
	return provider.Result{}, &provider.Error{Class: provider.ProviderError, Detail: provider.Truncate(provider.Redact(err.Error()))}
}

// skillRoots are the person's skill directories that exist. Commands may read them only when
// skills are on, because a skill is a file that the model reads with a command.
func (d *Driver) skillRoots() []string {
	if !d.cfg.Setup.Skills {
		return nil
	}
	home, _ := d.o.Lookup("HOME")
	codexHome, ok := d.o.Lookup("CODEX_HOME")
	if !ok || codexHome == "" {
		if home == "" {
			return nil
		}
		codexHome = filepath.Join(home, ".codex")
	}
	candidates := []string{filepath.Join(codexHome, "skills"), filepath.Join(codexHome, "plugins")}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".agents", "skills"))
	}
	var roots []string
	for _, c := range candidates {
		if !filepath.IsAbs(c) {
			continue
		}
		real, err := filepath.EvalSymlinks(c)
		if err != nil {
			continue
		}
		if info, err := os.Stat(real); err == nil && info.IsDir() {
			roots = append(roots, real)
		}
	}
	return roots
}

// repoSkills are the skill files that Codex would find in the working directory.
func repoSkills(workdir string) []string {
	var found []string
	for _, dir := range []string{filepath.Join(workdir, ".agents", "skills"), filepath.Join(workdir, ".codex", "skills")} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*", "SKILL.md"))
		found = append(found, matches...)
	}
	return found
}

func deadlineText(job provider.Job) string {
	if job.Timeout > 0 {
		return job.Timeout.String()
	}
	return "the time that the caller allowed"
}

func checkJob(job provider.Job) error {
	bad := func(format string, args ...any) error {
		return &provider.Error{Class: provider.ValidationError, Detail: fmt.Sprintf(format, args...)}
	}
	switch {
	case job.MaxTurns <= 0:
		return bad("the job needs a positive turn limit")
	case strings.TrimSpace(job.Prompt) == "":
		return bad("the job has no prompt")
	case job.Workdir == "":
		return bad("the job has no working directory")
	}
	if info, err := os.Stat(job.Workdir); err != nil || !info.IsDir() {
		return bad("the working directory %q is not a directory", job.Workdir)
	}
	var obj map[string]json.RawMessage
	if schema := bytes.TrimSpace(job.Schema); len(schema) == 0 || schema[0] != '{' || json.Unmarshal(schema, &obj) != nil || obj == nil {
		return bad("the schema is not a JSON object")
	}
	return nil
}

// writeSchema writes the schema to a file of mode 0600 outside the working directory, where
// the model cannot read or change it. Codex itself reads the file before the first turn.
func writeSchema(schema json.RawMessage, workdir string) (string, func(), error) {
	f, err := os.CreateTemp("", "semar-codex-schema-*.json")
	if err != nil {
		return "", nil, &provider.Error{Class: provider.ProviderError, Detail: fmt.Sprintf("the schema file cannot be created: %v", errors.Unwrap(err))}
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	realDir, _ := filepath.EvalSymlinks(filepath.Dir(f.Name()))
	if rel, err := filepath.Rel(workdir, realDir); err == nil && filepath.IsLocal(rel) {
		_ = f.Close()
		cleanup()
		return "", nil, &provider.Error{Class: provider.ValidationError, Detail: "the temporary directory lies inside the working directory, so the schema file would be readable by the model"}
	}
	_, werr := f.Write(schema)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(f.Name(), 0o600)
	}
	if werr != nil {
		cleanup()
		return "", nil, &provider.Error{Class: provider.ProviderError, Detail: fmt.Sprintf("the schema file cannot be written: %v", werr)}
	}
	return f.Name(), cleanup, nil
}
