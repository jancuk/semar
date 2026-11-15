package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/stage/plan"
)

// A plan is read and committed by people, so its files are not private.
const planFileMode = 0o644

type planFile struct {
	rel  string // below the repository
	data []byte
}

// writePlan writes every file or none (R21). Every byte is made, and every directory on the
// way to a target is checked, before the first write. Every write goes through a root at the
// repository, so a link that the repository commits under .semar cannot carry it elsewhere.
// The plan directory comes first because it is the cheapest to fail, then the scenario files,
// then the scripts. A failure removes what this call wrote.
func writePlan(dir string, in plan.Input, out plan.Output, known map[domain.ScenarioID]bool) (string, error) {
	if len(out.Scripts) != len(out.Scenarios) {
		return "", fmt.Errorf("the plan has %d scenarios and %d scripts", len(out.Scenarios), len(out.Scripts))
	}
	var scenarioFiles []planFile
	var scripts []domain.Script
	checked := []string{filepath.Join(".semar", "plans")}
	for i, sc := range out.Scenarios {
		if err := idcheck.Check("ticket", sc.Ticket); err != nil {
			return "", fmt.Errorf("scenario %s: %w", sc.ID, err)
		}
		if err := idcheck.Check("scenario ID", string(sc.ID)); err != nil {
			return "", err
		}
		if known[sc.ID] {
			return "", fmt.Errorf("scenario %s exists already; nothing was written", sc.ID)
		}
		data, err := scenario.Marshal(sc)
		if err != nil {
			return "", err
		}
		f := planFile{filepath.Join(".semar", "scenarios", sc.Ticket, string(sc.ID)+".yaml"), data}
		scenarioFiles = append(scenarioFiles, f)
		checked = append(checked, filepath.Dir(f.rel), filepath.Join(".semar", "store", "scripts", string(sc.ID)))
		s := out.Scripts[i]
		s.ExpectHash = domain.ExpectHash(sc)
		s.AuthoredBy = domain.ProviderRef{Instance: out.PlannerID, Model: out.Result.Model, Harness: out.PlannerKind}
		scripts = append(scripts, s)
	}
	var md bytes.Buffer
	if err := plan.Render(&md, in, out); err != nil {
		return "", err
	}
	draft, err := atomicfile.EncodeJSON(out.Draft)
	if err != nil {
		return "", fmt.Errorf("encode the draft: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	for _, d := range slices.Compact(checked) {
		if err := atomicfile.CheckDirIn(root, d); err != nil {
			return "", fmt.Errorf("%w; nothing was written", err)
		}
	}
	for _, f := range scenarioFiles {
		if _, err := root.Lstat(f.rel); !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s exists already; nothing was written", filepath.Join(dir, f.rel))
		}
	}
	store, err := evidence.Open(semarDir(dir))
	if err != nil {
		return "", err
	}
	for _, s := range scripts {
		if _, found, err := store.Script(s.Scenario, s.ExpectHash); err != nil {
			return "", fmt.Errorf("%w; nothing was written", err)
		} else if found {
			return "", fmt.Errorf("a script of scenario %s for expectation hash %s is stored already; nothing was written", s.Scenario, s.ExpectHash)
		}
	}

	w := &planWriter{root: root, store: store}
	planDir, err := w.newPlanDir(in.Change.HeadCommit)
	if err != nil {
		return w.undo(err)
	}
	for _, f := range scenarioFiles {
		if err := w.writeFile(f); err != nil {
			return w.undo(err)
		}
	}
	for _, s := range scripts {
		if err := store.PutScript(s); err != nil {
			return w.undo(err)
		}
		w.scripts = append(w.scripts, s)
	}
	for _, f := range []planFile{{filepath.Join(planDir, "plan.md"), md.Bytes()}, {filepath.Join(planDir, "draft.json"), draft}} {
		if err := w.writeFile(f); err != nil {
			return w.undo(err)
		}
	}
	return filepath.Join(dir, planDir, "plan.md"), nil
}

// planWriter remembers what one plan wrote, so that undo can remove exactly that.
type planWriter struct {
	root    *os.Root
	store   *evidence.Store
	dirs    []string // created, outermost first
	files   []string
	scripts []domain.Script
	planDir string
}

func (w *planWriter) writeFile(f planFile) error {
	created, err := atomicfile.MkdirIn(w.root, filepath.Dir(f.rel))
	w.dirs = append(w.dirs, created...)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteNewIn(w.root, f.rel, f.data, planFileMode); err != nil {
		return err
	}
	w.files = append(w.files, f.rel)
	return nil
}

// newPlanDir claims .semar/plans/<first 12 characters of the head>, or the first free -2, -3, ...
func (w *planWriter) newPlanDir(head string) (string, error) {
	if len(head) > 12 {
		head = head[:12]
	}
	if err := idcheck.Check("commit", head); err != nil {
		return "", err
	}
	parent := filepath.Join(".semar", "plans")
	created, err := atomicfile.MkdirIn(w.root, parent)
	w.dirs = append(w.dirs, created...)
	if err != nil {
		return "", err
	}
	for n := 1; n <= 1000; n++ {
		p := filepath.Join(parent, head)
		if n > 1 {
			p = fmt.Sprintf("%s-%d", p, n)
		}
		err := w.root.Mkdir(p, atomicfile.DirMode)
		if err == nil {
			w.planDir = p
			return p, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create %s: %w", filepath.Join(w.root.Name(), p), err)
		}
	}
	return "", fmt.Errorf("%s holds 1000 plans of commit %s already", filepath.Join(w.root.Name(), parent), head)
}

func (w *planWriter) undo(cause error) (string, error) {
	errs := []error{cause}
	for _, s := range w.scripts {
		if err := w.store.RemoveScript(s.Scenario, s.ExpectHash); err != nil {
			errs = append(errs, err)
		}
	}
	for _, f := range w.files {
		if err := w.root.Remove(f); err != nil {
			errs = append(errs, err)
		}
	}
	if w.planDir != "" {
		if err := w.root.RemoveAll(w.planDir); err != nil {
			errs = append(errs, err)
		}
	}
	for _, d := range slices.Backward(w.dirs) {
		if err := w.root.Remove(d); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return "", fmt.Errorf("the plan was not written, and what it had written was removed: %w", errors.Join(errs...))
}
