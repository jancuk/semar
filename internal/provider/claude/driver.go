// Package claude drives the Claude Code command line as an agent provider with read-only tools.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

const (
	Kind = "claude"
	// BinaryEnv names the variable that holds the absolute path of the harness binary.
	// The path is never read from the configuration, which the repository under test owns.
	BinaryEnv     = "SEMAR_CLAUDE_BIN"
	defaultBinary = "claude"
	healthTimeout = 10 * time.Second
)

type Driver struct {
	cfg      config.Provider
	lookup   func(string) (string, bool)
	repoRoot string
}

// New takes lookup, normally os.LookupEnv, for BinaryEnv and the child's environment, and the
// root of the repository under test, inside which no harness binary is started. An empty
// repoRoot skips that check.
func New(cfg config.Provider, lookup func(string) (string, bool), repoRoot string) (*Driver, error) {
	if lookup == nil {
		return nil, errors.New("claude: no environment lookup")
	}
	return &Driver{cfg: cfg, lookup: lookup, repoRoot: repoRoot}, nil
}

func Factory(lookup func(string) (string, bool), repoRoot string) provider.Factory {
	return func(cfg config.Provider) (provider.Instance, error) {
		d, err := New(cfg, lookup, repoRoot)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
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
	res, err := provider.RunProcess(runCtx, provider.ProcessSpec{
		Path:  path,
		Args:  buildArgs(job, d.cfg.Model),
		Dir:   job.Workdir,
		Env:   provider.ChildEnv(d.lookup),
		Stdin: strings.NewReader(job.Prompt),
	})
	switch {
	case err == nil:
		return parseOutput(res.Stdout, res.ExitCode, res.StderrTail)
	case errors.Is(err, context.DeadlineExceeded):
		return provider.Result{}, &provider.Error{Class: provider.TransportError, Detail: "the harness did not finish in " + deadlineText(job)}
	case errors.Is(err, context.Canceled):
		return provider.Result{}, fmt.Errorf("run claude: %w", ctx.Err())
	case errors.Is(err, provider.ErrOutputTooLarge):
		return provider.Result{}, &provider.Error{Class: provider.ProviderError, Detail: "the harness wrote more than 16 MiB of output"}
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
