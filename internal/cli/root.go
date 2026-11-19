package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	"github.com/jancuk/semar/internal/vcs"
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
	TempRoot  string                 // where plan copies the source; "" is a directory in the user cache directory
	CacheDir  func() (string, error) // the user cache directory; nil is os.UserCacheDir
	Providers func(repoRoot string) *provider.Registry
	// Email is the user.email of git for the repository in dir, or "" when it is not set. A review
	// stores it with each approval.
	Email func(dir string) string
	// PlanEstimate says what planning will cost, for the offer of semar run. Nil reads the usage log.
	PlanEstimate planEstimator
	Environ      func() []string // the person's environment, for a harness that runs with their setup
	Home         string          // where providers.yaml and usage.jsonl live; "" is $SEMAR_HOME or ~/.config/semar
	Keychain     keychain.Store  // nil is the keychain of the operating system
	HTTP         *http.Client    // the client of the build probe that init looks for; nil is a client that follows no redirect

	StderrTerminal func() bool // whether standard error is a terminal that takes a status line
	Tick           ticker      // the ticks of the progress line; nil is a real ticker

	// TrialTimeout and TrialLimit bound one request and the whole trial run of semar plan. Zero is
	// 10 seconds and 2 minutes. A test sets them to meet a service that stalls without waiting.
	TrialTimeout time.Duration
	TrialLimit   time.Duration

	// Sleep waits between the attempts of a journey's wait. Nil sleeps for real. A test sets it to
	// wait for nothing.
	Sleep func(ctx context.Context, d time.Duration) error

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
	if o.CacheDir == nil {
		o.CacheDir = os.UserCacheDir
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
	if o.PlanEstimate == nil {
		o.PlanEstimate = usageEstimate
	}
	if o.Email == nil {
		o.Email = gitEmail
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
	root.AddGroup(&cobra.Group{ID: groupDaily, Title: "Every day"}, &cobra.Group{ID: groupMore, Title: "More"})
	root.SetHelpCommandGroupID(groupMore)
	root.SetCompletionCommandGroupID(groupMore)
	version := &cobra.Command{
		Use:   "version",
		Short: "Print the semar version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "semar %s\n", Version)
			return err
		},
	}
	grouped := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	grouped(groupDaily, newInitCommand(o), newLoginCommand(o), newPlanCommand(o), newRunCommand(o), newRecordCommand(o), newCheckCommand(o),
		newDeferCommand(o), newLogCommand(), newShowCommand(), newRmCommand(o))
	grouped(groupMore, newImportCommand(o), newReviewCommand(o), newStatusCommand(o), newChangesCommand(), newExportCommand(), newFixtureCommand(o), newJourneyCommand(o), newProvidersCommand(o),
		newDashboardCmd(o), newScenariosCommand(), newScreensCommand(o), newScriptCommand(), version)
	root.AddCommand(removedCommand("confirm", "use semar review"), removedCommand("ledger", "use semar status, semar check, or semar defer"),
		removedCommand("activity", "use semar show --activity"))
	return root
}

const (
	groupDaily = "daily"
	groupMore  = "more"
)

// removedCommand answers a name that Semar no longer has with one line that names the new command.
// It has no alias: the old arguments mean something else now.
func removedCommand(name, instead string) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("semar %s was removed: %s", name, instead)
		},
	}
}

func addDirFlag(cmd *cobra.Command, dir *string) {
	cmd.Flags().StringVar(dir, "dir", ".", "the root of the repository that holds .semar")
}

// gitEmail is the user.email that git has for the repository in dir. A directory that is not in a
// repository, and a setting that is not there, give "".
func gitEmail(dir string) string {
	g, err := vcs.Open(dir)
	if err != nil {
		return ""
	}
	email, err := g.Config(context.Background(), "user.email")
	if err != nil {
		return ""
	}
	return email
}
