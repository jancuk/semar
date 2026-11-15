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
	"path/filepath"
	"runtime"
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
	cfg    config.Provider
	lookup func(string) (string, bool)
}

// New takes lookup, normally os.LookupEnv, for BinaryEnv and the child's environment.
func New(cfg config.Provider, lookup func(string) (string, bool)) (*Driver, error) {
	if lookup == nil {
		return nil, errors.New("claude: no environment lookup")
	}
	return &Driver{cfg: cfg, lookup: lookup}, nil
}

func Factory(lookup func(string) (string, bool)) provider.Factory {
	return func(cfg config.Provider) (provider.Instance, error) {
		d, err := New(cfg, lookup)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
}

func (d *Driver) ID() string   { return d.cfg.ID }
func (d *Driver) Kind() string { return Kind }

func (d *Driver) binary() (string, *provider.Error) {
	notInstalled := func(format string, args ...any) (string, *provider.Error) {
		return "", &provider.Error{Class: provider.NotInstalled, Detail: fmt.Sprintf(format, args...)}
	}
	if p, ok := d.lookup(BinaryEnv); ok && p != "" {
		if !filepath.IsAbs(p) {
			return notInstalled("%s is %q, which is not an absolute path", BinaryEnv, p)
		}
		info, err := os.Stat(p)
		switch {
		case err != nil:
			return notInstalled("%s names %q, which cannot be read: %v", BinaryEnv, p, errors.Unwrap(err))
		case !info.Mode().IsRegular():
			return notInstalled("%s names %q, which is not a regular file", BinaryEnv, p)
		case runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0:
			return notInstalled("%s names %q, which is not executable", BinaryEnv, p)
		}
		return p, nil
	}
	path, err := exec.LookPath(defaultBinary)
	switch {
	case errors.Is(err, exec.ErrDot):
		return notInstalled("the binary %q was found on PATH only through a relative directory; set %s to an absolute path", defaultBinary, BinaryEnv)
	case err != nil:
		return notInstalled("the binary %q was not found on PATH, and %s is not set", defaultBinary, BinaryEnv)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return notInstalled("the binary %q on PATH has no absolute path: %v", path, err)
	}
	return abs, nil
}

// insideWorkdir refuses a binary that the copy of the repository under test could have supplied.
func insideWorkdir(bin, workdir string) *provider.Error {
	realBin, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return &provider.Error{Class: provider.NotInstalled, Detail: fmt.Sprintf("the binary %q cannot be resolved: %v", bin, errors.Unwrap(err))}
	}
	realDir, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return &provider.Error{Class: provider.ValidationError, Detail: fmt.Sprintf("the working directory %q cannot be resolved", workdir)}
	}
	if rel, err := filepath.Rel(realDir, realBin); err == nil && filepath.IsLocal(rel) {
		return &provider.Error{Class: provider.PermissionError, Detail: fmt.Sprintf("the binary %q lies inside the working directory of the job", bin)}
	}
	return nil
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
		Env:  provider.ChildEnv(d.lookup),
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
	if perr == nil {
		perr = insideWorkdir(path, job.Workdir)
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
