// Package export writes one stored run as CTRF JSON, JUnit XML, or SARIF. It reads stored,
// redacted evidence and computes no outcome.
package export

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/idcheck"
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
	StepLines  map[domain.StepID]int // the line of each step's id key in the file
	DurationMS int64
	Unknown    bool // an action was too large to read
}

// Load refuses a run that is interrupted, that changed after it was written, or that has no
// scenario result. The scenario files are read only for labels and locations: a file that
// is gone, too large, not YAML, or one of several for an ID leaves the scenario with none.
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

	paths := scenarioPaths(repo)
	out := Run{Run: run, Version: version, Scenarios: make([]Scenario, len(run.Results))}
	for i, res := range run.Results {
		sc := Scenario{Result: res}
		sc.DurationMS, sc.Unknown, err = actionTime(store, id, acts, res.Scenario)
		if err != nil {
			return Run{}, err
		}
		if idcheck.Check("scenario ID", string(res.Scenario)) == nil {
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

// scenarioPaths maps each scenario ID to the files .semar/scenarios/**/<id>.yaml or <id>.yml,
// as paths from the repository root. A link, a special file, and anything that cannot be read
// is left out: the files give labels only.
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
		if ext != ".yaml" && ext != ".yml" {
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
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return
	}
	top := doc.Content[0]
	if got, ok := textOf(field(top, "id")); !ok || got != string(id) {
		return
	}
	sc.File = filepath.ToSlash(rel)
	if t, ok := textOf(field(top, "title")); ok {
		sc.Title = cutMessage(t)
	}
	if t, ok := textOf(field(top, "ticket")); ok {
		sc.Ticket = cutMessage(t)
	}
	steps := field(top, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return
	}
	sc.StepLines = map[domain.StepID]int{}
	for _, step := range steps.Content {
		if step.Kind != yaml.MappingNode {
			continue
		}
		key, val := fieldPair(step, "id")
		if text, ok := textOf(val); ok && text != "" {
			if _, dup := sc.StepLines[domain.StepID(text)]; !dup {
				sc.StepLines[domain.StepID(text)] = key.Line
			}
		}
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

func fieldPair(m *yaml.Node, name string) (key, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == name {
			return k, m.Content[i+1]
		}
	}
	return nil, nil
}

func field(m *yaml.Node, name string) *yaml.Node {
	_, v := fieldPair(m, name)
	return v
}

// textOf accepts a scalar that YAML reads as a string. A number, a list, and an alias are not text.
func textOf(n *yaml.Node) (string, bool) {
	if n == nil || n.Kind != yaml.ScalarNode || n.ShortTag() != "!!str" {
		return "", false
	}
	return n.Value, true
}
