package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/screens"
)

func newScreensCommand(o options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "screens",
		Short: "Work with the screen objects of web steps",
	}
	cmd.AddCommand(newScreensRepairCommand(o))
	return cmd
}

type repairFlags struct {
	apply []string
	dir   string
}

func newScreensRepairCommand(o options) *cobra.Command {
	var f repairFlags
	cmd := &cobra.Command{
		Use:   "repair <run id|latest> [--apply <screen>.<element>=<n>]...",
		Short: "Show a proposal for each element that a run did not find, and rewrite a locator with --apply",
		Long: "Read a stored run, and for each element that a web step did not find, print its locator beside up to three candidates that the browser found by role, by name, or by text. No model is used.\n" +
			"With --apply <screen>.<element>=<n>, rewrite that element of the screens file with candidate n. Nothing else is touched: not a scenario, an expected result, or a step.\n" +
			"The file then has another hash, so a person must approve it again with semar review <app>.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return repairScreens(cmd.Context(), cmd.OutOrStdout(), f, args[0])
		},
	}
	cmd.Flags().StringArrayVar(&f.apply, "apply", nil, "rewrite <screen>.<element> with candidate <n>, as Board.card=1; repeat for more")
	addDirFlag(cmd, &f.dir)
	return cmd
}

// misses is one element that a run did not find: where it is, what was looked for, and the proposals.
type miss struct {
	app, screen, element string
	scenario             domain.ScenarioID
	step                 domain.StepID
	looked               domain.Locator
	candidates           []domain.Candidate
}

func (m miss) key() string { return m.app + "/" + m.screen + "." + m.element }

// runMisses reads the web actions of a run and gives each element that was not found. A later action
// that missed the same element replaces an earlier one.
func runMisses(store *evidence.Store, id domain.RunID, acts []domain.Activity) ([]miss, error) {
	var out []miss
	for _, e := range acts {
		if e.Surface != domain.SurfaceWeb || e.Evidence == "" {
			continue
		}
		a, err := store.ReadAction(id, e.Evidence)
		if err != nil {
			return nil, err
		}
		if a.Web == nil || a.Web.App == "" || a.Web.Screen == "" {
			continue
		}
		for name, st := range a.Web.Elements {
			if st.Count != 0 || st.Locator == nil || st.Presence {
				continue
			}
			m := miss{app: a.Web.App, screen: a.Web.Screen, element: name, scenario: a.Use.Scenario, step: a.Use.Step, looked: *st.Locator}
			for _, c := range a.Web.Repair {
				if c.Element == name {
					m.candidates = append(m.candidates, c)
				}
			}
			out = slices.DeleteFunc(out, func(x miss) bool { return x.key() == m.key() })
			out = append(out, m)
		}
	}
	slices.SortStableFunc(out, func(a, b miss) int { return strings.Compare(a.key(), b.key()) })
	return out, nil
}

var applyPattern = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_-]*)\.([A-Za-z0-9][A-Za-z0-9_-]*)=([0-9]+)$`)

func repairScreens(ctx context.Context, stdout io.Writer, f repairFlags, arg string) error {
	if err := requireSemarDir(f.dir); err != nil {
		return err
	}
	store, err := evidence.Open(semarDir(f.dir))
	if err != nil {
		return err
	}
	id := domain.RunID(arg)
	if arg == latestRun {
		runs, err := store.Runs()
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			return fmt.Errorf("no run was found in %s", semarDir(f.dir))
		}
		id = runs[0]
	} else if err := idcheck.Check("run ID", arg); err != nil {
		return err
	}
	acts, err := store.Activity(id)
	if err != nil {
		return err
	}
	misses, err := runMisses(store, id, acts)
	if err != nil {
		return err
	}
	if len(f.apply) == 0 {
		return printMisses(stdout, id, misses)
	}
	return applyRepairs(ctx, stdout, f, misses)
}

func printMisses(stdout io.Writer, id domain.RunID, misses []miss) error {
	if len(misses) == 0 {
		fmt.Fprintf(stdout, "run %s: every element that a web step looked for was found, so there is nothing to repair\n", id)
		return nil
	}
	for _, m := range misses {
		fmt.Fprintf(stdout, "%s %s.%s was not found (scenario %s, step %s)\n", m.app, m.screen, m.element, m.scenario, m.step)
		fmt.Fprintf(stdout, "  old: %s\n", terminalSafe(oneLine(m.looked.String())))
		if len(m.candidates) == 0 {
			fmt.Fprintf(stdout, "  no candidate: the page has nothing like it. Either the product changed or the script is out of date\n")
			continue
		}
		for i, c := range m.candidates {
			fmt.Fprintf(stdout, "  %d. %s\n     %s\n", i+1, terminalSafe(oneLine(c.Locator.String())), terminalSafe(oneLine(c.Why)))
		}
		fmt.Fprintf(stdout, "  to use one: semar screens repair %s --apply %s.%s=<n>\n", id, m.screen, m.element)
	}
	fmt.Fprintf(stdout, "a repair rewrites the screens file and nothing else. Approve the new file with semar review <app>\n")
	return nil
}

func applyRepairs(ctx context.Context, stdout io.Writer, f repairFlags, misses []miss) error {
	type chosen struct {
		m miss
		c domain.Candidate
	}
	byApp := map[string][]chosen{}
	for _, raw := range f.apply {
		m := applyPattern.FindStringSubmatch(raw)
		if m == nil {
			return fmt.Errorf("--apply %q must look like Board.card=1: the screen, the element, and the number of the candidate", raw)
		}
		n, _ := strconv.Atoi(m[3])
		i := slices.IndexFunc(misses, func(x miss) bool { return x.screen == m[1] && x.element == m[2] })
		if i < 0 {
			return fmt.Errorf("--apply %s: the run did not miss %s.%s", raw, m[1], m[2])
		}
		x := misses[i]
		if n < 1 || n > len(x.candidates) {
			return fmt.Errorf("--apply %s: %s.%s has %s, so the number is 1 to %d", raw, x.screen, x.element, plural(len(x.candidates), "candidate"), len(x.candidates))
		}
		byApp[x.app] = append(byApp[x.app], chosen{x, x.candidates[n-1]})
	}
	for _, app := range slices.Sorted(maps.Keys(byApp)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := screens.Read(f.dir, app)
		if err != nil {
			return err
		}
		current, err := screens.Parse(data, screens.Path(app), app)
		if err != nil {
			return err
		}
		for _, ch := range byApp[app] {
			file, ok := current.Locator(ch.m.screen, ch.m.element)
			if !ok {
				return fmt.Errorf("%s no longer has %s.%s", screens.Path(app), ch.m.screen, ch.m.element)
			}
			loc := screens.Repaired(file, ch.m.looked, ch.c.Locator)
			if data, err = screens.Replace(data, app, ch.m.screen, ch.m.element, loc); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s.%s in %s\n  was: %s\n  now: %s\n", ch.m.screen, ch.m.element, screens.Path(app),
				terminalSafe(oneLine(file.String())), terminalSafe(oneLine(loc.String())))
		}
		if err := atomicfile.Write(filepath.Join(f.dir, filepath.FromSlash(screens.Path(app))), data); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "the file of %s has another hash now, so approve it again: semar review %s\n", app, app)
	}
	return nil
}

// screensLines is what a person reads before approving the screens of an app: every element with its
// locator, and what changed since the file that was approved before.
func screensLines(scr domain.Screens, h domain.Hash, before *confirm.ScreensApproval) []string {
	lines := []string{fmt.Sprintf("Screens of %s, hash %s:", scr.App, h)}
	for _, sn := range scr.ScreenNames() {
		lines = append(lines, "  screen "+sn)
		for _, en := range scr.ElementNames(sn) {
			loc, _ := scr.Locator(sn, en)
			lines = append(lines, fmt.Sprintf("    %s: %s", en, loc))
		}
	}
	if before == nil {
		lines = append(lines, "  these screens were never approved before")
		return lines
	}
	changes := screens.Changes(before.Screens(), scr)
	if len(changes) == 0 {
		return lines
	}
	lines = append(lines, fmt.Sprintf("  since the approval of %s (hash %s):", before.At.Format("2006-01-02"), before.Hash))
	for _, c := range changes {
		where := c.Screen + "." + c.Element
		switch {
		case c.Kind == "added":
			lines = append(lines, fmt.Sprintf("    added %s: %s", where, c.New))
		case c.Kind == "removed":
			lines = append(lines, fmt.Sprintf("    removed %s: was %s", where, c.Old))
		case c.SameRoleAndName:
			lines = append(lines, fmt.Sprintf("    changed %s: the role and the name are the same, so the repair followed a moved element", where),
				fmt.Sprintf("      was %s", c.Old), fmt.Sprintf("      now %s", c.New))
		default:
			lines = append(lines, fmt.Sprintf("    changed %s: the element changed: check it is the same thing", where),
				fmt.Sprintf("      was %s", c.Old), fmt.Sprintf("      now %s", c.New))
		}
	}
	return lines
}
