// Package claude drives the Claude Code command line as an agent provider with read-only tools.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

const (
	Kind          = "claude"
	defaultBinary = "claude"
	healthTimeout = 10 * time.Second
)

type Driver struct {
	cfg config.Provider
}

func New(cfg config.Provider) (*Driver, error) { return &Driver{cfg: cfg}, nil }

func Factory() provider.Factory {
	return func(cfg config.Provider) (provider.Instance, error) { return New(cfg) }
}

func (d *Driver) ID() string   { return d.cfg.ID }
func (d *Driver) Kind() string { return Kind }

func (d *Driver) binary() (string, *provider.Error) {
	name := d.cfg.BinaryPath
	if name == "" {
		name = defaultBinary
	}
	path, err := exec.LookPath(name)
	if err != nil {
		where := "on PATH"
		if d.cfg.BinaryPath != "" {
			where = "at the configured binaryPath"
		}
		return "", &provider.Error{Class: provider.NotInstalled, Detail: fmt.Sprintf("the binary %q was not found %s", name, where)}
	}
	return path, nil
}

// Health starts the binary with --version only, never a model turn.
func (d *Driver) Health(ctx context.Context) provider.Health {
	path, perr := d.binary()
	if perr != nil {
		return provider.Health{Detail: perr.Detail}
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	res, err := provider.RunProcess(ctx, provider.ProcessSpec{
		Path: path,
		Args: []string{"--version"},
		Env:  provider.ChildEnv(os.LookupEnv),
	})
	switch {
	case err != nil:
		return provider.Health{Detail: fmt.Sprintf("%s --version: %v", path, err)}
	case res.ExitCode != 0:
		return provider.Health{Detail: fmt.Sprintf("%s --version ended with exit code %d: %s", path, res.ExitCode, lastLine(res.StderrTail))}
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(res.Stdout)), "\n")
	return provider.Health{Installed: true, Version: strings.TrimSpace(version)}
}

func (d *Driver) Run(ctx context.Context, job provider.Job) (provider.Result, error) {
	if err := checkJob(job); err != nil {
		return provider.Result{}, err
	}
	path, perr := d.binary()
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
		Env:   provider.ChildEnv(os.LookupEnv),
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
	return provider.Result{}, &provider.Error{Class: provider.ProviderError, Detail: truncate(redact(err.Error()))}
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
