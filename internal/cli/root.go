package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/userconfig"
)

// Version is set at build time with -ldflags and never changed at run time.
var Version = "dev"

// options holds what the commands take from the process. Tests replace them (G8).
type options struct {
	Clock     func() time.Time
	RunID     func() domain.RunID
	User      func() (string, error)
	Stdin     io.Reader
	Terminal  func() bool // whether Stdin is a terminal
	LookupEnv func(string) (string, bool)
	TempRoot  string // where plan copies the source; "" is the system default
	Providers func(repoRoot string) *provider.Registry
	Environ   func() []string // the person's environment, for a harness that runs with their setup
	Home      string          // where providers.yaml and usage.jsonl live; "" is $SEMAR_HOME or ~/.config/semar
	Keychain  keychain.Store  // nil is the keychain of the operating system

	StderrTerminal func() bool // whether standard error is a terminal that takes a status line
	Tick           ticker      // the ticks of the progress line; nil is a real ticker

	drop *envDrop
}

// home is the directory of the person's own files.
func (o options) home() (string, error) {
	if o.Home != "" {
		return o.Home, nil
	}
	return userconfig.Home(o.LookupEnv)
}

func (o options) withDefaults() options {
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.RunID == nil {
		clock := o.Clock
		o.RunID = func() domain.RunID { return newRunID(clock()) }
	}
	if o.User == nil {
		o.User = osUser
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Terminal == nil {
		o.Terminal = stdinIsTerminal
	}
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	if o.StderrTerminal == nil {
		lookup := o.LookupEnv
		o.StderrTerminal = func() bool { return stderrIsTerminal(lookup) }
	}
	if o.Tick == nil {
		o.Tick = realTicker
	}
	if o.Environ == nil {
		o.Environ = os.Environ
	}
	if o.Keychain == nil {
		o.Keychain = keychain.OS()
	}
	if o.drop == nil {
		o.drop = &envDrop{}
	}
	if o.Providers == nil {
		deps := providerDeps{lookupEnv: o.LookupEnv, environ: o.Environ, store: o.Keychain, drop: o.drop}
		o.Providers = func(repoRoot string) *provider.Registry { return providerRegistry(repoRoot, deps) }
	}
	return o
}

// newRunID is r-<UTC date and time>-<4 random hex characters>.
func newRunID(now time.Time) domain.RunID {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return domain.RunID("r-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b))
}

func osUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("find the local user: %w", err)
	}
	return u.Username, nil
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// exitError ends a command with its exit code once the command has printed why.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return run(ctx, args, stdout, stderr, options{})
}

// StopSignals are the signals that cancel the command instead of ending the process, so
// that it removes its temporary files and ends the programs it started. SIGHUP arrives when
// the terminal closes; Windows never sends it. A hangup that the parent ignored, as nohup
// does, stays ignored: asking for it would turn the handling back on.
func StopSignals() []os.Signal {
	sigs := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if !signal.Ignored(syscall.SIGHUP) {
		sigs = append(sigs, syscall.SIGHUP)
	}
	return sigs
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, o options) int {
	root := newRoot(stdout, stderr, o.withDefaults())
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		var exit exitError
		if errors.As(err, &exit) {
			return exit.code
		}
		fmt.Fprintf(stderr, "semar: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func newRoot(stdout, stderr io.Writer, o options) *cobra.Command {
	root := &cobra.Command{
		Use:           "semar",
		Short:         "Replay and verify scenarios against a running system",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the semar version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "semar %s\n", Version)
			return err
		},
	})
	root.AddCommand(newRunCommand(o), newScriptCommand(), newConfirmCommand(o), newActivityCommand(),
		newInitCommand(), newChangesCommand(), newPlanCommand(o), newProvidersCommand(o), newDashboardCmd(o))
	return root
}

func addDirFlag(cmd *cobra.Command, dir *string) {
	cmd.Flags().StringVar(dir, "dir", ".", "the root of the repository that holds .semar")
}
