package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	env      string
	fixtures []string
	journeys []string
}

func newConfirmCommand(o options) *cobra.Command {
	var f confirmFlags
	cmd := &cobra.Command{
		Use:   "confirm (<scenario id> | --all | --fixture <id>... | --journey <id>...)",
		Short: "Confirm the expected results of scenarios or journeys, or approve fixtures, that need a person",
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case cmd.Flags().Changed("env") && len(f.journeys) == 0:
				return errors.New("--env is for --journey only: a scenario and a fixture are confirmed for every environment")
			case len(f.fixtures) > 0 && len(f.journeys) > 0:
				return errors.New("pass --fixture, or --journey, and not both")
			case len(f.journeys) > 0 && (f.all || len(args) > 0):
				return errors.New("pass --journey, or the ID of one scenario, or --all, and not several of them")
			case len(f.fixtures) > 0 && (f.all || len(args) > 0):
				return errors.New("pass --fixture, or the ID of one scenario, or --all, and not several of them")
			case len(f.fixtures) > 0 || len(f.journeys) > 0:
				return nil
			case f.all && len(args) > 0:
				return errors.New("pass the ID of one scenario, or --all, and not both")
			case !f.all && len(args) != 1:
				return errors.New("pass the ID of one scenario, or --all")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(f.fixtures) > 0:
				return approveFixtures(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
			case len(f.journeys) > 0:
				return confirmJourneys(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
			}
			return confirmScenarios(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f, args)
		},
	}
	cmd.Flags().BoolVar(&f.yes, "yes", false, "confirm or approve without asking")
	cmd.Flags().StringArrayVar(&f.fixtures, "fixture", nil, "approve the fixture with this ID, as it is now in .semar/fixtures (repeatable)")
	cmd.Flags().StringArrayVar(&f.journeys, "journey", nil, "confirm the expected results of every leg of the journey with this ID (repeatable)")
	cmd.Flags().StringVar(&f.env, "env", "", "with --journey, the environment whose auth profiles the legs use; needed when there are several")
	cmd.Flags().BoolVar(&f.all, "all", false, "confirm every scenario in .semar/scenarios that needs a confirmation")
	addDirFlag(cmd, &f.dir)
	return cmd
}

// unconfirmed is a scenario with the expected results that need a person.
type unconfirmed struct {
	sc      domain.Scenario
	label   string // how the heading names it; "scenario <id>" when empty
	lines   []string
	sources []domain.Source
}

// needsConfirmation lists the expected results of a scenario that a person must confirm. A step
// that acts as another account says so. For a ticket expectation whose cite names a line of a file
// of the repository at dir, the line is printed as it is in the working tree, so that the person can
// compare it with the check.
func needsConfirmation(dir string, sc domain.Scenario, trust []domain.Source) unconfirmed {
	u := unconfirmed{sc: sc}
	for _, st := range sc.Steps {
		for _, e := range st.Expect {
			if !confirm.NeedsExpectation(e, trust) {
				continue
			}
			head := "step " + string(st.ID)
			if st.As != "" {
				head += " as " + st.As
			}
			line := fmt.Sprintf("%s: %s (source %s", head, check.DescribeFull(e.Check), e.Source)
			if e.Cite != "" {
				line += ", cite: " + e.Cite
			}
			line = terminalSafe(line + ")")
			if e.Source == domain.SourceTicket {
				if file, n, ok := citedLine(e.Cite); ok {
					line += "\n      " + citedText(dir, file, n)
				}
			}
			u.lines = append(u.lines, line)
			u.sources = append(u.sources, e.Source)
		}
	}
	return u
}

const maxCitedText = 300

// maxCitedFile bounds how much of a cited file is read.
const maxCitedFile = 1 << 20

var citeLine = regexp.MustCompile(`^([^\s:]+):([0-9]{1,9})(?:\s|$)`)

// citedLine reads <repo file>:<line> from the start of a cite.
func citedLine(cite string) (file string, n int, ok bool) {
	m := citeLine.FindStringSubmatch(cite)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	return m[1], n, err == nil
}

// citedText is the text that the confirm screen prints for a cited line: the line of the file in the
// working tree, as one escaped line of at most maxCitedText characters, or "line not found" when the
// file or the line is not there. A path outside the repository is never read.
func citedText(dir, file string, n int) string {
	notFound := fmt.Sprintf("%s line %d: line not found", terminalSafe(file), n)
	if n < 1 || !filepath.IsLocal(file) {
		return notFound
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return notFound
	}
	defer root.Close()
	f, err := root.Open(file)
	if err != nil {
		return notFound
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, maxCitedFile))
	sc.Buffer(nil, maxCitedFile)
	for i := 1; sc.Scan(); i++ {
		if i < n {
			continue
		}
		text := terminalSafe(strings.ReplaceAll(sc.Text(), "\t", " "))
		if r := []rune(text); len(r) > maxCitedText {
			text = string(r[:maxCitedText-3]) + "..."
		}
		return fmt.Sprintf("%s line %d: %s", terminalSafe(file), n, text)
	}
	return notFound
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
		if u := needsConfirmation(f.dir, sc, cfg.Policy.Trust.Confirm); len(u.lines) > 0 {
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
	return storeConfirmations(ctx, stdout, stderr, o, f, todo)
}

// storeConfirmations shows what each scenario needs, asks once, and stores one confirmation for the
// hash of each. The scenarios of a journey's legs go through it as well, so one flow confirms both.
func storeConfirmations(ctx context.Context, stdout, stderr io.Writer, o options, f confirmFlags, todo []unconfirmed) error {
	for _, u := range todo {
		label := u.label
		if label == "" {
			label = "Scenario " + string(u.sc.ID)
		}
		fmt.Fprintf(stdout, "%s has %d expected results that a person must confirm:\n", label, len(u.lines))
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
