package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Version is set at build time with -ldflags and never changed at run time.
var Version = "dev"

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := newRoot(stdout, stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(stderr, "semar: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func newRoot(stdout, stderr io.Writer) *cobra.Command {
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
	return root
}
