package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/script"
)

func newScriptCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "script",
		Short: "Manage the scripts that replay scenarios",
	}
	var dir string
	add := &cobra.Command{
		Use:   "add <file>",
		Short: "Check a script against its scenario and store it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return addScript(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, args[0])
		},
	}
	addDirFlag(add, &dir)
	cmd.AddCommand(add)
	return cmd
}

// addScript binds a script that carries no hash to its scenario's current expected results.
func addScript(ctx context.Context, stdout, stderr io.Writer, repo, file string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("script add: %w", err)
	}
	s, err := script.Load(file)
	if err != nil {
		return err
	}
	sc, s, problems, err := storeScript(repo, s)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		fmt.Fprintf(stderr, "semar: script %s does not fit scenario %s, and nothing was stored:\n", file, sc.ID)
		for _, p := range problems {
			fmt.Fprintf(stderr, "  - %s\n", p)
		}
		return exitError{ExitError}
	}
	fmt.Fprintf(stdout, "stored the script of scenario %s for expectation hash %s\n", sc.ID, s.ExpectHash)
	return nil
}

// storeScript checks a script against the scenario that it names and, when nothing is wrong, stores
// it under the hash of the scenario's expected results. A script with no hash gets the current one.
// It returns the scenario, the script as stored, and the reasons for storing nothing.
func storeScript(repo string, s domain.Script) (domain.Scenario, domain.Script, []string, error) {
	sc, err := loadScenario(repo, s.Scenario)
	if err != nil {
		return domain.Scenario{}, s, nil, err
	}
	want := domain.ExpectHash(sc)
	var problems []string
	s.ShapeHash = domain.ShapeHash(sc)
	switch s.ExpectHash {
	case "":
		s.ExpectHash = want
	case want:
	default:
		problems = append(problems, fmt.Sprintf("its expectation hash %s differs from %s, the hash of the expected results of scenario %s now", s.ExpectHash, want, sc.ID))
	}
	for _, p := range script.Validate(sc, s) {
		if p.Step != "" {
			problems = append(problems, fmt.Sprintf("step %s: %s", p.Step, p.Message))
		} else {
			problems = append(problems, p.Message)
		}
	}
	if len(problems) > 0 {
		return sc, s, problems, nil
	}
	store, err := evidence.Open(semarDir(repo))
	if err != nil {
		return sc, s, nil, err
	}
	if err := store.PutScript(s); err != nil {
		return sc, s, nil, err
	}
	return sc, s, nil, nil
}
