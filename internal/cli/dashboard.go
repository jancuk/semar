package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/dashboard"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/userconfig"
)

// dashboardSystem is what the dashboard takes from the machine beyond options, so that a
// test never reaches the real keychain or a browser.
type dashboardSystem struct {
	keys    keychain.Store
	environ func() []string
	open    func(url string) error
}

func newDashboardCmd(o options) *cobra.Command {
	return dashboardCommand(o, dashboardSystem{keys: keychain.OS(), environ: os.Environ, open: openBrowser})
}

func dashboardCommand(o options, sys dashboardSystem) *cobra.Command {
	var port int
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "dashboard [--port n] [--no-open]",
		Short: "Open a page on this computer to manage the model providers and see usage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDashboard(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, sys, port, noOpen)
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "the port on 127.0.0.1 (default: any free port)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "print the address without opening a browser")
	return cmd
}

func runDashboard(ctx context.Context, stdout, stderr io.Writer, o options, sys dashboardSystem, port int, noOpen bool) error {
	if port < 0 || port > 65535 {
		return errors.New("--port must be from 0 to 65535, and 0 means any free port")
	}
	home, err := userconfig.Home(o.LookupEnv)
	if err != nil {
		return err
	}
	registry := o.Providers("")
	srv, err := dashboard.New(dashboard.Deps{
		Home:        home,
		NewInstance: func(cfg config.Provider) (provider.Instance, error) { return registry.New(cfg) },
		Keys:        sys.keys,
		LookupEnv:   o.LookupEnv,
		Environ:     sys.environ,
		Now:         o.Clock,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("listen on 127.0.0.1 port %d: %w", port, err)
	}
	url := fmt.Sprintf("http://%s/#token=%s", ln.Addr(), srv.Token())
	fmt.Fprintln(stdout, url)
	fmt.Fprintln(stderr, "semar: the dashboard runs until you press Ctrl+C. Anyone who has the address above can change your providers.")
	if !noOpen {
		if err := sys.open(url); err != nil {
			fmt.Fprintf(stderr, "semar: no browser was opened (%v); open the address above yourself\n", err)
		}
	}
	return srv.Serve(ctx, ln)
}

// openBrowser starts the system's opener and does not wait for the browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
