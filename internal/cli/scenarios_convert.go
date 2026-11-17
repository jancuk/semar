package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

func newScenariosCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scenarios",
		Short: "Work with the scenario files in .semar/scenarios",
	}
	var dir string
	convert := &cobra.Command{
		Use:   "convert",
		Short: "Convert every YAML scenario file to a Gherkin .feature file, in place",
		Long: "Scenarios were YAML files and are Gherkin .feature files now. convert writes the .feature file beside each\n" +
			".yaml or .yml scenario file under .semar/scenarios and deletes the YAML file. The scenario is the same: its\n" +
			"expectation hash does not change, so every confirmation and every stored script stays valid. convert never\n" +
			"overwrites a .feature file, and it writes nothing when any file cannot be converted.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return convertScenarios(cmd.OutOrStdout(), cmd.ErrOrStderr(), dir)
		},
	}
	addDirFlag(convert, &dir)
	cmd.AddCommand(convert)
	return cmd
}

// conversion is one YAML scenario file and the Gherkin file that replaces it, both as paths below the repository.
type conversion struct {
	from, to string
	data     []byte
	hash     domain.Hash
}

// convertScenarios makes every Gherkin file first, in memory, and writes none of them unless all can be
// written: a repository must not be left with some scenarios in each format.
func convertScenarios(stdout, stderr io.Writer, repo string) error {
	var legacy []string
	if err := walkScenarios(repo, func(p string, _ fs.DirEntry) {
		if isLegacyScenario(p) {
			legacy = append(legacy, p)
		}
	}); err != nil {
		return err
	}
	if len(legacy) == 0 {
		fmt.Fprintf(stdout, "no YAML scenario file in %s: nothing to convert\n", scenariosDir(repo))
		return nil
	}

	var todo []conversion
	var problems []string
	targets := map[string]string{}
	for _, p := range legacy {
		rel, err := filepath.Rel(repo, p)
		if err != nil {
			rel = p
		}
		c, err := convertFile(repo, p, rel)
		if err == nil {
			if other, dup := targets[c.to]; dup {
				err = fmt.Errorf("it would be written to %s, which %s writes too", c.to, other)
			}
		}
		if err == nil {
			if _, statErr := os.Lstat(filepath.Join(repo, c.to)); !errors.Is(statErr, fs.ErrNotExist) {
				err = fmt.Errorf("%s exists already, and convert does not overwrite a file: move it or delete it", c.to)
			}
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", filepath.ToSlash(rel), err))
			continue
		}
		targets[c.to] = c.from
		todo = append(todo, c)
	}
	if len(problems) > 0 {
		fmt.Fprintf(stderr, "semar: %d of %d scenario files cannot be converted, and nothing was changed:\n", len(problems), len(legacy))
		for _, p := range problems {
			fmt.Fprintf(stderr, "  - %s\n", strings.ReplaceAll(p, "\n", "\n    "))
		}
		return exitError{ExitError}
	}

	root, err := os.OpenRoot(repo)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, c := range todo {
		if err := atomicfile.WriteNewIn(root, c.to, c.data, planFileMode); err != nil {
			return fmt.Errorf("write %s: %w; %s was kept", c.to, err, c.from)
		}
		if err := root.Remove(c.from); err != nil {
			return fmt.Errorf("remove %s: %w; its Gherkin file %s is written, so remove the YAML file by hand", c.from, err, c.to)
		}
		fmt.Fprintf(stdout, "converted %s to %s (expectation hash %s is the same)\n", filepath.ToSlash(c.from), filepath.ToSlash(c.to), c.hash)
	}
	fmt.Fprintf(stdout, "converted %d scenario files to Gherkin and deleted the YAML files\n", len(todo))
	return nil
}

func convertFile(repo, path, rel string) (conversion, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return conversion{}, err
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	sc, err := scenario.ParseYAML(data, filepath.ToSlash(rel))
	if err != nil {
		return conversion{}, err
	}
	if string(sc.ID) != stem {
		return conversion{}, fmt.Errorf("it holds scenario %q, and its file name says %s", sc.ID, stem)
	}
	out, err := scenario.Render(sc, "")
	if err != nil {
		return conversion{}, fmt.Errorf("%w\n  Gherkin cannot hold this text exactly, and changing it changes the expectation hash, which needs a new confirmation: edit the YAML file, then run semar scenarios convert again", err)
	}
	return conversion{
		from: rel,
		to:   filepath.Join(filepath.Dir(rel), stem+featureExt),
		data: out,
		hash: domain.ExpectHash(sc),
	}, nil
}
