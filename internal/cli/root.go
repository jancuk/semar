package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/domain"
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
	root.AddCommand(newRunCommand(o), newScriptCommand(), newConfirmCommand(o), newActivityCommand())
	return root
}

func addDirFlag(cmd *cobra.Command, dir *string) {
	cmd.Flags().StringVar(dir, "dir", ".", "the root of the repository that holds .semar")
}
