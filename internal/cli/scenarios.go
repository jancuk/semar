package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/scenario"
)

func semarDir(repo string) string { return filepath.Join(repo, ".semar") }

func scenariosDir(repo string) string { return filepath.Join(semarDir(repo), "scenarios") }

// featureExt is the extension of a scenario file.
const featureExt = ".feature"

// walkScenarios calls visit for every regular file below .semar/scenarios, in path order.
// .semar and .semar/scenarios must be real directories, and an entry below them that is a link
// or a special file is an error and not skipped: a linked directory would hide its scenarios
// from a plan, which would then reuse their IDs and titles. A repository with no directory
// has no scenario.
func walkScenarios(repo string, visit func(path string, d fs.DirEntry)) error {
	dir := scenariosDir(repo)
	for _, d := range []string{semarDir(repo), dir} {
		info, err := os.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return fmt.Errorf("list scenarios in %s: %w", dir, err)
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symbolic link, and Semar reads scenarios only from real directories: replace it with a directory", d)
		case !info.IsDir():
			return fmt.Errorf("%s is not a directory, and Semar reads scenarios from a directory there", d)
		}
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symbolic link, and Semar reads scenarios only from real files and directories", p)
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file (%s), and .semar/scenarios may hold only files and directories", p, d.Type())
		}
		visit(p, d)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list scenarios in %s: %w", dir, err)
	}
	return nil
}

// isLegacyScenario is true for a file of the YAML format that scenarios had before they were Gherkin.
func isLegacyScenario(path string) bool {
	ext := filepath.Ext(path)
	return ext == ".yaml" || ext == ".yml"
}

// scenarioFiles maps each scenario ID to its files: .semar/scenarios/**/<id>.feature. A scenario file
// in the YAML format is an error that says how to convert it, because a plan that skipped it would
// reuse its ID.
func scenarioFiles(repo string) (map[domain.ScenarioID][]string, error) {
	files := map[domain.ScenarioID][]string{}
	var legacy []string
	err := walkScenarios(repo, func(p string, d fs.DirEntry) {
		switch {
		case filepath.Ext(p) == featureExt:
			id := domain.ScenarioID(strings.TrimSuffix(d.Name(), featureExt))
			files[id] = append(files[id], p)
		case isLegacyScenario(p):
			legacy = append(legacy, p)
		}
	})
	if err != nil {
		return nil, err
	}
	if len(legacy) > 0 {
		return nil, legacyError(legacy)
	}
	return files, nil
}

func legacyError(files []string) error {
	shown := files
	if len(shown) > 3 {
		shown = shown[:3]
	}
	more := ""
	if len(files) > len(shown) {
		more = fmt.Sprintf(" and %d more", len(files)-len(shown))
	}
	return fmt.Errorf("scenario files are Gherkin now: run semar scenarios convert (a YAML scenario file is left: %s%s)", strings.Join(shown, ", "), more)
}

func loadScenario(repo string, id domain.ScenarioID) (domain.Scenario, error) {
	if err := idcheck.Scenario(string(id)); err != nil {
		return domain.Scenario{}, err
	}
	files, err := scenarioFiles(repo)
	if err != nil {
		return domain.Scenario{}, err
	}
	if len(files[id]) == 0 {
		return domain.Scenario{}, fmt.Errorf("scenario %s was not found: no file %s%s in %s", id, id, featureExt, scenariosDir(repo))
	}
	return readScenario(id, files[id])
}

// loadAllScenarios returns every scenario in ID order, or every reason one could not be read.
func loadAllScenarios(repo string) ([]domain.Scenario, error) {
	files, err := scenarioFiles(repo)
	if err != nil {
		return nil, err
	}
	var scs []domain.Scenario
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(files)) {
		if err := idcheck.Scenario(string(id)); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", files[id][0], err))
			continue
		}
		sc, err := readScenario(id, files[id])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		scs = append(scs, sc)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return scs, nil
}

func readScenario(id domain.ScenarioID, paths []string) (domain.Scenario, error) {
	if len(paths) > 1 {
		return domain.Scenario{}, fmt.Errorf("scenario %s is defined by more than one file: %s", id, strings.Join(paths, ", "))
	}
	p := paths[0]
	sc, err := scenario.Load(p)
	if err != nil {
		return domain.Scenario{}, err
	}
	if sc.ID != id {
		return domain.Scenario{}, fmt.Errorf("%s holds scenario %q, and its file name says %s", p, sc.ID, id)
	}
	if problems := scenario.Validate(sc, scenario.FromFile); len(problems) > 0 {
		lines := make([]string, len(problems))
		for i, pr := range problems {
			lines[i] = fmt.Sprintf("%s: %s: %s", p, pr.Path, pr.Message)
		}
		return domain.Scenario{}, errors.New(strings.Join(lines, "\n"))
	}
	return sc, nil
}
