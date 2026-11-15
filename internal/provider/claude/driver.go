// Package claude drives the Claude Code command line as an agent provider with read-only tools.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

const (
	Kind = "claude"
	// BinaryEnv names the variable that holds the absolute path of the harness binary.
	// The path is never read from the configuration, which the repository under test owns.
	BinaryEnv       = "SEMAR_CLAUDE_BIN"
	defaultBinary   = "claude"
	healthTimeout   = 10 * time.Second
	streamStdoutCap = 64 << 20
)

type Driver struct {
	cfg      config.Provider
	lookup   func(string) (string, bool)
	environ  func() []string
	repoRoot string
	dropEnv  []string
}

// Options are what a driver needs besides the provider's definition.
type Options struct {
	// Lookup, normally os.LookupEnv, gives BinaryEnv and the short environment of a job
	// without the person's setup.
	Lookup func(string) (string, bool)
	// Environ, normally os.Environ, gives the person's environment for a job with the setup.
	// It may be nil when the setup is off.
	Environ func() []string
	// RepoRoot is the root of the repository under test, inside which no harness binary is
	// started. An empty RepoRoot skips that check.
	RepoRoot string
	// DropEnv names more variables that a job with the setup never inherits, such as the
	// variables that semar.yaml names as secrets.
	DropEnv []string
}

// New takes lookup, normally os.LookupEnv, for BinaryEnv and the child's environment, and the
// root of the repository under test, inside which no harness binary is started. An empty
// repoRoot skips that check. It cannot start a job with the person's setup; NewWith can.
func New(cfg config.Provider, lookup func(string) (string, bool), repoRoot string) (*Driver, error) {
	return NewWith(cfg, Options{Lookup: lookup, RepoRoot: repoRoot})
}

func NewWith(cfg config.Provider, opts Options) (*Driver, error) {
	if opts.Lookup == nil {
		return nil, errors.New("claude: no environment lookup")
	}
	if usesSetup(cfg.Setup) && opts.Environ == nil {
		return nil, fmt.Errorf("claude: provider %q uses the person's setup, and the driver was given no environment for it", cfg.ID)
	}
	if err := config.CheckDeny(cfg.Setup.Deny); err != nil {
		return nil, fmt.Errorf("claude: provider %q: %w", cfg.ID, err)
	}
	cfg.Setup.Deny = slices.Clone(cfg.Setup.Deny)
	return &Driver{
		cfg:      cfg,
		lookup:   opts.Lookup,
		environ:  opts.Environ,
		repoRoot: opts.RepoRoot,
		dropEnv:  slices.Clone(opts.DropEnv),
	}, nil
}

func Factory(lookup func(string) (string, bool), repoRoot string) provider.Factory {
	return FactoryWith(Options{Lookup: lookup, RepoRoot: repoRoot})
}

func FactoryWith(opts Options) provider.Factory {
	return func(cfg config.Provider) (provider.Instance, error) {
		d, err := NewWith(cfg, opts)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
}

// childEnv is the environment of a job and of the status command: the short list without the
// setup, and the person's environment without vendors' and Semar's variables with it.
func (d *Driver) childEnv() []string {
	if !usesSetup(d.cfg.Setup) {
		return provider.ChildEnv(d.lookup)
	}
	return append(provider.SetupEnv(d.environ(), d.dropEnv), setupEnvExtra...)
}

func (d *Driver) ID() string   { return d.cfg.ID }
func (d *Driver) Kind() string { return Kind }

func (d *Driver) binary() (string, *provider.Error) {
	return provider.FindBinary(d.lookup, BinaryEnv, defaultBinary)
}

// Health starts the binary with --version only, never a model turn.
func (d *Driver) Health(ctx context.Context) provider.Health {
	path, perr := d.binary()
	if perr == nil {
		perr = provider.RefuseInside(path, d.repoRoot, "")
	}
	if perr != nil {
		return provider.Health{Detail: perr.Detail}
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	res, err := provider.RunProcess(ctx, provider.ProcessSpec{
		Path: path,
		Args: []string{"--version"},
		Env:  provider.ChildEnv(d.lookup),
	})
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
	path, perr := d.binary()
	if perr == nil {
		perr = provider.RefuseInside(path, d.repoRoot, job.Workdir)
	}
	if perr != nil {
		return provider.Result{}, perr
	}
	runCtx := ctx
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}
	// The stream repeats every tool result, so it is allowed more than the one object.
	stdoutCap := provider.DefaultStdoutCap
	if usesSetup(d.cfg.Setup) {
		stdoutCap = streamStdoutCap
	}
	res, err := provider.RunProcess(runCtx, provider.ProcessSpec{
		Path:      path,
		Args:      buildArgs(job, d.cfg),
		Dir:       job.Workdir,
		Env:       d.childEnv(),
		Stdin:     strings.NewReader(job.Prompt),
		StdoutCap: stdoutCap,
	})
	switch {
	case err == nil && usesSetup(d.cfg.Setup):
		return parseStream(res.Stdout, res.ExitCode, res.StderrTail)
	case err == nil:
		return parseOutput(res.Stdout, res.ExitCode, res.StderrTail)
	case errors.Is(err, context.DeadlineExceeded):
		return provider.Result{}, &provider.Error{Class: provider.TransportError, Detail: "the harness did not finish in " + deadlineText(job)}
	case errors.Is(err, context.Canceled):
		return provider.Result{}, fmt.Errorf("run claude: %w", ctx.Err())
	case errors.Is(err, provider.ErrOutputTooLarge):
		return provider.Result{}, &provider.Error{Class: provider.ProviderError, Detail: fmt.Sprintf("the harness wrote more than %d MiB of output", stdoutCap>>20)}
	case errors.Is(err, os.ErrNotExist):
		return provider.Result{}, &provider.Error{Class: provider.NotInstalled, Detail: fmt.Sprintf("the binary %q could not be started", path)}
	}
	return provider.Result{}, &provider.Error{Class: provider.ProviderError, Detail: provider.Truncate(provider.Redact(err.Error()))}
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
