package cli

import (
	"context"
	"io"

	"github.com/jancuk/semar/internal/config"
)

type Options = options

func RunWith(ctx context.Context, args []string, stdout, stderr io.Writer, o Options) int {
	return run(ctx, args, stdout, stderr, o)
}

func FirstRung(o Options, dir string, cfg config.Config) (string, string, error) {
	return firstRung(o.withDefaults(), dir, cfg)
}
