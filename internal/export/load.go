// Package export writes one stored run as CTRF JSON, JUnit XML, or SARIF. It reads stored,
// redacted evidence and computes no outcome.
package export

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/scenario"
)

// maxScenarioFile is the largest scenario file that Load reads. A larger one is treated as gone.
const maxScenarioFile = 1 << 20

// Run is what every format reads. Load builds it, and it is the only I/O of the package.
type Run struct {
	Run       domain.Run
	Scenarios []Scenario // in the order of Run.Results
	Version   string     // the Semar version
}

type Scenario struct {
	Result     domain.ScenarioResult
	Title      string                // "" when the scenario file is gone
	Ticket     string                // "" when the scenario file is gone or names none
	File       string                // the scenario file from the repository root, "" when gone
	StepLines  map[domain.StepID]int // the line of each step in the file
	DurationMS int64
	Unknown    bool // an action was too large to read
}

// Load refuses a run that is interrupted, that changed after it was written, or that has no
// scenario result. The scenario files are read only for labels and locations: a file that
// is gone, too large, not Gherkin, or one of several for an ID leaves the scenario with none.
func Load(store *evidence.Store, repo string, id domain.RunID, version string) (Run, error) {
	if err := idcheck.Check("run ID", string(id)); err != nil {
		return Run{}, err
	}
	changed, err := store.Verify(id)
	if errors.Is(err, evidence.ErrInterrupted) {
		return Run{}, fmt.Errorf("run %s is interrupted: it has no run.json, so it has no outcome to export", id)
	}
	if err != nil {
		return Run{}, err
	}
	if len(changed) > 0 {
		return Run{}, fmt.Errorf("the evidence of run %s changed after it was written: %s; nothing was exported", id, changedFiles(id, changed))
	}
	run, acts, err := store.LoadRun(id)
	if err != nil {
		return Run{}, err
	}
	if run.ID != id {
		return Run{}, fmt.Errorf("run.json of run %s holds run %s", id, run.ID)
	}
	if len(run.Results) == 0 {
		return Run{}, fmt.Errorf("run %s has no scenario result: a trial run or a fixture check has nothing to export", id)
	}

	return Build(store, repo, run, acts, version)
}

// Build makes the Run of a run that is already in memory, with the labels and the action times that
// Load reads. It does not verify the evidence, so the caller that writes a run calls it before the run
// is closed.
func Build(store *evidence.Store, repo string, run domain.Run, acts []domain.Activity, version string) (Run, error) {
	paths := scenarioPaths(repo)
	out := Run{Run: run, Version: version, Scenarios: make([]Scenario, len(run.Results))}
	for i, res := range run.Results {
		sc := Scenario{Result: res}
		var err error
		sc.DurationMS, sc.Unknown, err = actionTime(store, run.ID, acts, res.Scenario)
		if err != nil {
			return Run{}, err
		}
		if idcheck.Scenario(string(res.Scenario)) == nil {
			if p := paths[res.Scenario]; len(p) == 1 {
				readLabels(repo, res.Scenario, p[0], &sc)
			}
		}
		out.Scenarios[i] = sc
	}
	return out, nil
}

// changedFiles names each file that Verify reported, as runs/<id>/<path>. The reason follows
// unless it is "changed".
func changedFiles(id domain.RunID, problems []string) string {
	names := make([]string, len(problems))
	for i, p := range problems {
		rel, reason, ok := strings.Cut(p, ": ")
		names[i] = "runs/" + string(id) + "/" + rel
		if ok && reason != "changed" {
			names[i] += " (" + reason + ")"
		}
	}
	return strings.Join(names, ", ")
}

// actionTime adds the duration of every action that the activity log records for a scenario,
// in any pass. An action file that is too large counts as 0 and sets unknown.
func actionTime(store *evidence.Store, id domain.RunID, acts []domain.Activity, scenario domain.ScenarioID) (ms int64, unknown bool, err error) {
	seen := map[string]bool{}
	for _, e := range acts {
		if e.Use.Scenario != scenario || e.Evidence == "" || seen[e.Evidence] {
			continue
		}
		seen[e.Evidence] = true
		a, err := store.ReadAction(id, e.Evidence)
		if errors.Is(err, evidence.ErrActionTooLarge) {
			unknown = true
			continue
		}
		if err != nil {
			return 0, false, err
		}
		if a.DurationMS > 0 {
			ms += a.DurationMS
		}
	}
	return ms, unknown, nil
}

// scenarioPaths maps each scenario ID to the files .semar/scenarios/**/<id>.feature, as paths from
// the repository root. A link, a special file, and anything that cannot be read is left out: the
// files give labels only.
func scenarioPaths(repo string) map[domain.ScenarioID][]string {
	paths := map[domain.ScenarioID][]string{}
	base := filepath.Join(repo, ".semar")
	for _, d := range []string{base, filepath.Join(base, "scenarios")} {
		if info, err := os.Lstat(d); err != nil || !info.IsDir() {
			return paths
		}
	}
	_ = filepath.WalkDir(filepath.Join(base, "scenarios"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		ext := filepath.Ext(p)
		if ext != ".feature" {
			return nil
		}
		rel, err := filepath.Rel(repo, p)
		if err != nil {
			return nil
		}
		id := domain.ScenarioID(strings.TrimSuffix(d.Name(), ext))
		paths[id] = append(paths[id], rel)
		return nil
	})
	for _, list := range paths {
		sort.Strings(list)
	}
	return paths
}

// readLabels fills the title, ticket, file, and step lines of sc from the scenario file at rel.
func readLabels(repo string, id domain.ScenarioID, rel string, sc *Scenario) {
	if !filepath.IsLocal(rel) {
		return
	}
	data, ok := readSmallFile(repo, rel)
	if !ok {
		return
	}
	o, ok := scenario.ReadOutline(data)
	if !ok || o.ID != string(id) {
		return
	}
	sc.File = filepath.ToSlash(rel)
	sc.Title = cutMessage(o.Title)
	sc.Ticket = cutMessage(o.Ticket)
	if len(o.StepLines) > 0 {
		sc.StepLines = o.StepLines
	}
}

// readSmallFile reads a regular file below repo through a root, so that a link cannot lead out of it.
func readSmallFile(repo, rel string) ([]byte, bool) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, false
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxScenarioFile {
		return nil, false
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxScenarioFile+1))
	if err != nil || len(data) > maxScenarioFile {
		return nil, false
	}
	return data, true
}
