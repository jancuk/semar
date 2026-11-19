package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/obligation"
)

type checkFlags struct {
	by     string
	note   string
	env    string
	builds []string
	dir    string
}

func newCheckCommand(o options) *cobra.Command {
	var f checkFlags
	cmd := &cobra.Command{
		Use:   "check <id> (pass|fail [--note <text>] | --by <scenario id>)",
		Short: "Record what a person checked by hand, or say that a scenario does the check",
		Long: "Store the result of a manual check as a new record, under your name. A pass on the build that the environment runs now makes the use case verified, and it is shown as checked by you.\n" +
			"The builds come from the environment of semar.yaml: the one you name with --env, or the only one there is. A pass that names no build stays open for a person.\n" +
			"With --by, say that a scenario does the manual check. The scenario is added to the covers of the use case, and your name is kept as the person who said so. From then on the state of the use case comes from the scenario. Semar never links the two by itself: only a person can say that a test checks the same requirement under the same conditions.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.by != "" {
				if len(args) != 1 {
					return errors.New("--by takes no pass or fail: it says that a scenario does the check")
				}
				return linkUseCase(cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f, args[0])
			}
			if len(args) != 2 {
				return errors.New("say pass or fail, or name the scenario that does the check with --by")
			}
			return checkUseCase(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f, args[0], args[1])
		},
	}
	cmd.Flags().StringVar(&f.by, "by", "", "the ID of a scenario that does the manual check")
	cmd.Flags().StringVar(&f.note, "note", "", "what you saw, at most 2000 characters")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment whose builds the result names (default: the only environment of semar.yaml)")
	cmd.Flags().StringArrayVar(&f.builds, "build", nil, "with --env, the commit that a service with no build probe runs, as <commit> or <service>=<commit>; repeat for more")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func checkUseCase(ctx context.Context, stdout, stderr io.Writer, o options, f checkFlags, id, verb string) error {
	var result string
	switch verb {
	case "pass":
		result = "passed"
	case "fail":
		result = "failed"
	default:
		return fmt.Errorf("say pass or fail, not %q", terminalSafe(oneLine(verb)))
	}
	repo := f.dir
	obs, err := loadObligations(repo)
	if err != nil {
		return err
	}
	l, err := findObligation(obs, id)
	if err != nil {
		return err
	}
	entries, err := deriveEntries(stderr, o, repo, []obligation.Located{l}, "", nil)
	if err != nil {
		return err
	}
	if e := entries[0]; l.Manual == nil || e.Link != nil || (e.State != obligation.NeedsAPerson && e.State != obligation.Verified) {
		return fmt.Errorf("%s is %s, and only a use case with a manual check that no test does takes the result of a person", terminalSafe(id), strings.ReplaceAll(string(e.State), "_", " "))
	}
	name, err := person(o, "the person who records")
	if err != nil {
		return err
	}
	cfg, err := loadConfigOrNone(repo)
	if err != nil {
		return err
	}
	_, current, _, err := readBuilds(ctx, stderr, repo, cfg, f.env, f.builds)
	if err != nil {
		return err
	}
	var builds []obligation.ManualBuild
	for _, b := range current {
		builds = append(builds, obligation.ManualBuild{Service: b.Service, Commit: b.Commit})
	}
	m := obligation.ManualResult{Obligation: id, Result: result, User: name, Identity: obligation.LocalIdentity, At: o.Clock().UTC(), Notes: f.note, Builds: builds}
	if err := obligation.NewStore(semarDir(repo)).Record(m); err != nil {
		return err
	}
	on := ""
	if len(builds) > 0 {
		parts := make([]string, len(builds))
		for i, b := range builds {
			parts[i] = terminalSafe(oneLine(b.Service)) + " " + terminalSafe(oneLine(b.Commit))
		}
		on = " on " + strings.Join(parts, ", ")
	}
	fmt.Fprintf(stdout, "recorded %s for %s as %s (%s), checked by a person%s\n", result, terminalSafe(id), terminalSafe(oneLine(name)), obligation.LocalIdentity, on)
	return nil
}

// linkUseCase adds the scenario to the covers of a manual use case and stores who said so. The
// file comes first: a link that fails to store leaves a scenario in covers that no record backs, and
// the use case then still waits for a person, so a second run of the command finishes the job.
func linkUseCase(stdout, stderr io.Writer, o options, f checkFlags, id string) error {
	if err := idcheck.Scenario(f.by); err != nil {
		return err
	}
	repo := f.dir
	obs, err := loadObligations(repo)
	if err != nil {
		return err
	}
	l, err := findObligation(obs, id)
	if err != nil {
		return err
	}
	if l.Manual == nil {
		return fmt.Errorf("%s has no manual check, so no scenario can do it", terminalSafe(oneLine(id)))
	}
	if _, err := loadScenario(repo, domain.ScenarioID(f.by)); err != nil {
		return err
	}
	name, err := person(o, "the person who links")
	if err != nil {
		return err
	}
	sid := domain.ScenarioID(f.by)
	if err := obligation.Edit(repo, id, func(ob *obligation.Obligation) error {
		if !slices.Contains(ob.Covers, sid) {
			ob.Covers = append(ob.Covers, sid)
		}
		return nil
	}); err != nil {
		return err
	}
	link := obligation.Link{Obligation: id, Scenario: sid, User: name, Identity: obligation.LocalIdentity, At: o.Clock().UTC()}
	if err := obligation.NewStore(semarDir(repo)).Link(link); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s is checked by test %s, linked by %s (%s)\nIts state comes from the scenario now: run it with semar run --scenario %s\n",
		terminalSafe(oneLine(id)), sid, terminalSafe(oneLine(name)), obligation.LocalIdentity, sid)
	return nil
}
