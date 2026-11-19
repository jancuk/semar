package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/edge"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/screens"
)

const maxRetireReason = 300

type rmFlags struct {
	reason string
	dir    string
}

func newRmCommand(o options) *cobra.Command {
	var f rmFlags
	cmd := &cobra.Command{
		Use:   "rm <scenario id> --reason <text>",
		Short: "Retire a scenario: remove it, and keep a record on the use cases that it covered",
		Long: "Remove the scenario file and its stored scripts, take the scenario out of the covers of every use case, and write on each of those use cases the scenario, the reason, your name, and the day.\n" +
			"A use case that loses its last test is shown as having no test, with the reason and who retired the test, so that removing a failing test never makes a release look ready. Stored runs stay.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return retireScenario(cmd.OutOrStdout(), o, f, args[0])
		},
	}
	cmd.Flags().StringVar(&f.reason, "reason", "", "why the scenario is retired: one line of at most 300 characters")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func retireScenario(stdout io.Writer, o options, f rmFlags, id string) error {
	switch {
	case strings.TrimSpace(f.reason) == "":
		return errors.New("--reason is required: say why the scenario is retired")
	case strings.ContainsAny(f.reason, "\n\r"):
		return errors.New("--reason must be one line")
	case utf8.RuneCountInString(f.reason) > maxRetireReason:
		return fmt.Errorf("--reason is longer than %d characters", maxRetireReason)
	}
	if err := idcheck.Check("ID", id); err != nil {
		// A use case ID holds a slash, so it fails the check and is still worth naming.
		if named := notAScenario(f.dir, id); strings.Contains(named.Error(), "not a scenario") {
			return named
		}
		return err
	}
	files, err := scenarioFiles(f.dir)
	if err != nil {
		return err
	}
	sid := domain.ScenarioID(id)
	if len(files[sid]) == 0 || idcheck.Scenario(id) != nil {
		return notAScenario(f.dir, id)
	}
	name, err := person(o, "the person who retires")
	if err != nil {
		return err
	}
	// A dropped edge case is remembered, so that a later plan does not draft the same case again.
	var edgeKey string
	if sc, err := loadScenario(f.dir, sid); err == nil {
		edgeKey = edge.KeyOf(sc)
	}

	uses, problems, err := obligation.Load(f.dir)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("fix the obligations files first: %s", oneLine(terminalSafe(problems[0].String())))
	}
	day := o.Clock().UTC().Format(dayLayout)
	var left []string
	for _, l := range uses {
		if !slices.Contains(l.Covers, sid) {
			continue
		}
		err := obligation.Edit(f.dir, l.ID, func(ob *obligation.Obligation) error {
			ob.Covers = slices.DeleteFunc(ob.Covers, func(c domain.ScenarioID) bool { return c == sid })
			ob.Retired = append(ob.Retired, obligation.Retired{Scenario: sid, Reason: f.reason, User: name, Day: day})
			return nil
		})
		if err != nil {
			return err
		}
		left = append(left, l.ID)
	}

	root, err := os.OpenRoot(f.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, p := range files[sid] {
		rel, err := filepath.Rel(f.dir, p)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("the scenario file %s is not inside the repository", terminalSafe(p))
		}
		if err := root.Remove(rel); err != nil {
			return fmt.Errorf("remove %s: %w", terminalSafe(rel), err)
		}
	}
	store, err := evidence.Open(semarDir(f.dir))
	if err != nil {
		return err
	}
	scripts, err := store.RemoveScripts(sid)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "retired scenario %s as %s: removed its file and %s\n", sid, terminalSafe(oneLine(name)), plural(scripts, "stored script"))
	if edgeKey != "" {
		if err := edge.AddDropped(f.dir, edge.Dropped{Key: edgeKey, Scenario: sid, Reason: f.reason, User: name, Day: day}); err != nil {
			return fmt.Errorf("the scenario was retired, but the edge case could not be remembered: %w", err)
		}
		fmt.Fprintf(stdout, "a later plan does not draft this edge case again: %s\n", edge.DroppedFile)
	}
	if len(left) == 0 {
		fmt.Fprintln(stdout, "no use case covered it")
		return nil
	}
	fmt.Fprintf(stdout, "it left %s, where it now shows as retired: see semar status\n", strings.Join(left, ", "))
	return nil
}

// notAScenario says what an ID is when it is not a scenario, so that a person who typed a use case
// or a ticket learns what rm takes.
func notAScenario(dir, id string) error {
	known := func(what string) error {
		return fmt.Errorf("%s is %s, not a scenario: rm retires a scenario, and a scenario ID looks like SHOP-482-01", terminalSafe(oneLine(id)), what)
	}
	if uses, _, _ := obligation.Load(dir); len(uses) > 0 {
		for _, l := range uses {
			if l.ID == id {
				return known("a use case")
			}
		}
		for _, l := range uses {
			if ticketKey(l) == id {
				return known("a ticket")
			}
		}
	}
	if scs, err := loadAllScenarios(dir); err == nil {
		for _, sc := range scs {
			if sc.Ticket == id {
				return known("a ticket")
			}
		}
	}
	if js, _, _ := loadJourneys(dir); slices.ContainsFunc(js, func(j domain.Journey) bool { return string(j.ID) == id }) {
		return known("a journey")
	}
	if cat, err := fixture.Load(dir, nil); err == nil {
		if _, ok := cat.Get(domain.FixtureID(id)); ok {
			return known("a fixture")
		}
	}
	if apps, _ := screens.List(dir); slices.Contains(apps, id) {
		return known("the app of a screens file")
	}
	if err := idcheck.Check("ID", id); err != nil {
		return err
	}
	if _, err := loadScenario(dir, domain.ScenarioID(id)); err != nil {
		return err
	}
	return fmt.Errorf("%s is not a scenario that rm can retire", terminalSafe(oneLine(id)))
}
