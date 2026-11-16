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
	yes      bool
	all      bool
	dir      string
	fixtures []string
}

func newConfirmCommand(o options) *cobra.Command {
	var f confirmFlags
	cmd := &cobra.Command{
		Use:   "confirm (<scenario id> | --all | --fixture <id>...)",
		Short: "Confirm the expected results of scenarios, or approve fixtures, that need a person",
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(f.fixtures) > 0 && (f.all || len(args) > 0):
				return errors.New("pass --fixture, or the ID of one scenario, or --all, and not several of them")
			case len(f.fixtures) > 0:
				return nil
			case f.all && len(args) > 0:
				return errors.New("pass the ID of one scenario, or --all, and not both")
			case !f.all && len(args) != 1:
				return errors.New("pass the ID of one scenario, or --all")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(f.fixtures) > 0 {
				return approveFixtures(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
			}
			return confirmScenarios(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f, args)
		},
	}
	cmd.Flags().BoolVar(&f.yes, "yes", false, "confirm or approve without asking")
	cmd.Flags().StringArrayVar(&f.fixtures, "fixture", nil, "approve the fixture with this ID, as it is now in .semar/fixtures (repeatable)")
	cmd.Flags().BoolVar(&f.all, "all", false, "confirm every scenario in .semar/scenarios that needs a confirmation")
	addDirFlag(cmd, &f.dir)
	return cmd
}

// unconfirmed is a scenario with the expected results that need a person.
type unconfirmed struct {
	sc      domain.Scenario
	lines   []string
	sources []domain.Source
}

func confirmScenarios(ctx context.Context, stdout, stderr io.Writer, o options, f confirmFlags, args []string) error {
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	var scs []domain.Scenario
	if f.all {
		if scs, err = loadAllScenarios(f.dir); err != nil {
			return err
		}
	} else {
		sc, err := loadScenario(f.dir, domain.ScenarioID(args[0]))
		if err != nil {
			return err
		}
		scs = []domain.Scenario{sc}
	}
	var todo []unconfirmed
	for _, sc := range scs {
		u := unconfirmed{sc: sc}
		for _, st := range sc.Steps {
			for _, e := range st.Expect {
				if !confirm.Needs(e.Source, cfg.Policy.Trust.Confirm) {
					continue
				}
				line := fmt.Sprintf("step %s: %s (source %s", st.ID, check.DescribeFull(e.Check), e.Source)
				if e.Cite != "" {
					line += ", cite: " + e.Cite
				}
				u.lines = append(u.lines, terminalSafe(line+")"))
				u.sources = append(u.sources, e.Source)
			}
		}
		if len(u.lines) > 0 {
			todo = append(todo, u)
		}
	}
	if len(todo) == 0 {
		if f.all {
			fmt.Fprintln(stdout, "no expected result of any scenario needs a confirmation")
		} else {
			fmt.Fprintf(stdout, "no expected result of scenario %s needs a confirmation\n", scs[0].ID)
		}
		return nil
	}
	for _, u := range todo {
		fmt.Fprintf(stdout, "Scenario %s has %d expected results that a person must confirm:\n", u.sc.ID, len(u.lines))
		for _, line := range u.lines {
			fmt.Fprintf(stdout, "  - %s\n", line)
		}
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
	confirmations := confirm.NewLocal(store.Root())
	for _, u := range todo {
		rec := confirm.Record{
			Scenario:   u.sc.ID,
			ExpectHash: domain.ExpectHash(u.sc),
			User:       name,
			Identity:   localIdentity,
			At:         o.Clock().UTC(),
			Sources:    slices.Compact(slices.Sorted(slices.Values(u.sources))),
		}
		if err := confirmations.Put(rec); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "confirmed scenario %s for expectation hash %s as %s (%s)\n", u.sc.ID, rec.ExpectHash, name, localIdentity)
	}
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
