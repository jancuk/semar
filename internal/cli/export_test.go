package cli

import (
	"context"
	"io"
)

type Options = options

func RunWith(ctx context.Context, args []string, stdout, stderr io.Writer, o Options) int {
	return run(ctx, args, stdout, stderr, o)
}
