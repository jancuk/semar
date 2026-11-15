package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/jancuk/semar/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), cli.StopSignals()...)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
