package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/scenario"
)

func semarDir(repo string) string { return filepath.Join(repo, ".semar") }

func scenariosDir(repo string) string { return filepath.Join(semarDir(repo), "scenarios") }

// scenarioFiles maps each scenario ID to its files: .semar/scenarios/**/<id>.yaml or <id>.yml.
// A .yaml or .yml entry that is not a regular file, such as a symlink, is an error and not skipped.
func scenarioFiles(repo string) (map[domain.ScenarioID][]string, error) {
	dir := scenariosDir(repo)
	files := map[domain.ScenarioID][]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ext := filepath.Ext(p)
		if d.IsDir() || (ext != ".yaml" && ext != ".yml") {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file (%s), and a scenario file must be one", p, d.Type())
		}
		id := domain.ScenarioID(strings.TrimSuffix(d.Name(), ext))
		files[id] = append(files[id], p)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list scenarios in %s: %w", dir, err)
	}
	return files, nil
}

func loadScenario(repo string, id domain.ScenarioID) (domain.Scenario, error) {
	if err := idcheck.Check("scenario ID", string(id)); err != nil {
		return domain.Scenario{}, err
	}
	files, err := scenarioFiles(repo)
	if err != nil {
		return domain.Scenario{}, err
	}
	if len(files[id]) == 0 {
		return domain.Scenario{}, fmt.Errorf("scenario %s was not found: no file %s.yaml or %s.yml in %s", id, id, id, scenariosDir(repo))
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
		if err := idcheck.Check("scenario ID", string(id)); err != nil {
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
