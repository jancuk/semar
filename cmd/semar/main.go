package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/jancuk/semar/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals()...)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// stopSignals are the signals that cancel the command instead of ending the process, so
// that it removes its temporary files. SIGHUP arrives when the terminal closes; Windows never
// sends it. A hangup that the parent ignored, as nohup does, stays ignored: asking for it would
// turn the handling back on.
func stopSignals() []os.Signal {
	sigs := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if !signal.Ignored(syscall.SIGHUP) {
		sigs = append(sigs, syscall.SIGHUP)
	}
	return sigs
}
