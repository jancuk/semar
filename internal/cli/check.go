package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/obligation"
)

type checkFlags struct {
	note   string
	env    string
	builds []string
	dir    string
}

func newCheckCommand(o options) *cobra.Command {
	var f checkFlags
	cmd := &cobra.Command{
		Use:   "check <id> pass|fail [--note <text>]",
		Short: "Record what a person checked by hand",
		Long: "Store the result of a manual check as a new record, under your name. A pass on the build that the environment runs now makes the use case verified, and it is shown as checked by you.\n" +
			"The builds come from the environment of semar.yaml: the one you name with --env, or the only one there is. A pass that names no build stays open for a person.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return checkUseCase(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f, args[0], args[1])
		},
	}
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
	_, current, _, err := readBuilds(ctx, stderr, cfg, f.env, f.builds)
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
