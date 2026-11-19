package cli

import (
	"context"
	"io"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/stage/plan"
)

type Options = options

func RunWith(ctx context.Context, args []string, stdout, stderr io.Writer, o Options) int {
	return run(ctx, args, stdout, stderr, o)
}

func FirstRung(o Options, dir string, cfg config.Config) (string, string, error) {
	return firstRung(o.withDefaults(), dir, cfg)
}

// WithRecording makes semar record run with no window, and lets drive send the input of the person
// once the window is open. The window is closed when drive returns, as the person does.
func WithRecording(ctx context.Context, drive func(tab context.Context)) context.Context {
	return withRecording(ctx, recordingHooks{hidden: true, drive: drive})
}

// WithLogin makes semar login open no window that a person can see, and lets drive stand where the
// person stands: it closes the pages when it is done.
func WithLogin(ctx context.Context, drive func(tab context.Context)) context.Context {
	return withLogin(ctx, loginHooks{hidden: true, drive: drive})
}

// NewWebExplorer is the explorer of a plan for the one service of the environment in dir, as planChange
// builds it.
func NewWebExplorer(o Options, dir, service, as string) (plan.WebExplorer, error) {
	o = o.withDefaults()
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	envName, env, err := cfg.Environment("")
	if err != nil {
		return nil, err
	}
	env = withServiceAuth(cfg, env)
	svc, err := recordService(env, service)
	if err != nil {
		return nil, err
	}
	return newWebExplorer(o, dir, cfg, envName, env, svc, as, "")
}
