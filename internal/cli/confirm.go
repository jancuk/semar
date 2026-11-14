package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
)

// localIdentity says that nothing checked who the user is. FR-11
const localIdentity = "local, unverified identity"

type confirmFlags struct {
	yes bool
	dir string
}

func newConfirmCommand(o options) *cobra.Command {
	var f confirmFlags
	cmd := &cobra.Command{
		Use:   "confirm <scenario id>",
		Short: "Confirm the expected results of a scenario that need a person",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return confirmScenario(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f, domain.ScenarioID(args[0]))
		},
	}
	cmd.Flags().BoolVar(&f.yes, "yes", false, "confirm without asking")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func confirmScenario(ctx context.Context, stdout, stderr io.Writer, o options, f confirmFlags, id domain.ScenarioID) error {
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	sc, err := loadScenario(f.dir, id)
	if err != nil {
		return err
	}
	var needed []string
	var sources []domain.Source
	for _, st := range sc.Steps {
		for _, e := range st.Expect {
			if !confirm.Needs(e.Source, cfg.Policy.Trust.Confirm) {
				continue
			}
			line := fmt.Sprintf("step %s: %s (source %s", st.ID, check.DescribeFull(e.Check), e.Source)
			if e.Cite != "" {
				line += ", cite: " + e.Cite
			}
			needed = append(needed, terminalSafe(line+")"))
			sources = append(sources, e.Source)
		}
	}
	if len(needed) == 0 {
		fmt.Fprintf(stdout, "no expected result of scenario %s needs a confirmation\n", sc.ID)
		return nil
	}
	fmt.Fprintf(stdout, "Scenario %s has %d expected results that a person must confirm:\n", sc.ID, len(needed))
	for _, line := range needed {
		fmt.Fprintf(stdout, "  - %s\n", line)
	}

	if !f.yes {
		if !o.Terminal() {
			fmt.Fprintln(stderr, "semar: nothing was confirmed: the input is not a terminal, so pass --yes to confirm")
			return exitError{ExitUndecided}
		}
		fmt.Fprint(stdout, "Confirm them? [y/N] ")
		if a := strings.ToLower(strings.TrimSpace(readAnswer(ctx, o.Stdin))); a != "y" && a != "yes" {
			fmt.Fprintln(stdout, "nothing was confirmed")
			return exitError{ExitUndecided}
		}
	}

	name, err := o.User()
	if err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("the confirmer has no name; set git config user.name")
	}
	store, err := evidence.Open(semarDir(f.dir))
	if err != nil {
		return err
	}
	rec := confirm.Record{
		Scenario:   sc.ID,
		ExpectHash: domain.ExpectHash(sc),
		User:       name,
		Identity:   localIdentity,
		At:         o.Clock().UTC(),
		Sources:    slices.Compact(slices.Sorted(slices.Values(sources))),
	}
	if err := confirm.NewLocal(store.Root()).Put(rec); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "confirmed scenario %s for expectation hash %s as %s (%s)\n", sc.ID, rec.ExpectHash, name, localIdentity)
	return nil
}

// readAnswer returns one line, or nothing when ctx ends first. A read from a terminal
// cannot be cancelled, so on cancel the reader is left to end with the process.
func readAnswer(ctx context.Context, in io.Reader) string {
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(in).ReadString('\n')
		line <- s
	}()
	select {
	case s := <-line:
		return s
	case <-ctx.Done():
		return ""
	}
}

// terminalSafe makes text from a scenario file safe to print for a person who is about to confirm it.
// Format characters (category Cf, such as the bidi controls) are removed, because they reorder or hide text.
// A control character other than newline and tab, and the line and paragraph separators, show as an escape such as \x1b.
func terminalSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r):
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\u2028' || r == '\u2029':
			fmt.Fprintf(&b, `\u%04x`, r)
		case unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
