package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/stage/plan"
)

// A plan is read and committed by people, so its files are not private.
const planFileMode = 0o644

type planFile struct {
	rel  string // below the repository
	data []byte
}

// maxSubjectRunes bounds the Feature line of a scenario file.
const maxSubjectRunes = 100

// featureSubject is what the Feature line of a ticket's scenario files says: the planner's sentence on
// what the ticket changes, cut at a word, and the ticket key when the planner gave none.
func featureSubject(d plan.Draft, ticket string) string {
	for _, t := range d.Tickets {
		if t.Key != ticket {
			continue
		}
		words := strings.Fields(t.Behaviour)
		var b strings.Builder
		for _, w := range words {
			if utf8.RuneCountInString(b.String())+utf8.RuneCountInString(w) >= maxSubjectRunes {
				return strings.TrimRight(b.String(), " ,;:") + "..."
			}
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(w)
		}
		return b.String()
	}
	return ""
}

// writePlan writes every file or none (R21). Every byte is made, and every directory on the
// way to a target is checked, before the first write. Every write goes through a root at the
// repository, so a link that the repository commits under .semar cannot carry it elsewhere.
// The plan directory comes first because it is the cheapest to fail, then the scenario files,
// then the obligations files, then the scripts. A failure removes what this call wrote and puts
// back an obligations file that it replaced.
func writePlan(dir string, in plan.Input, out plan.Output, known map[domain.ScenarioID]bool) (string, error) {
	if len(out.Scripts) != len(out.Scenarios) {
		return "", fmt.Errorf("the plan has %d scenarios and %d scripts", len(out.Scenarios), len(out.Scripts))
	}
	var scenarioFiles []planFile
	var scripts []domain.Script
	var scriptFiles []string
	storeDirs := []string{filepath.Join(".semar", "runs"), filepath.Join(".semar", "store"), filepath.Join(".semar", "store", "scripts")}
	checked := []string{filepath.Join(".semar", "plans"), storeDirs[0], obligationsDir}
	for i, sc := range out.Scenarios {
		if err := idcheck.Check("ticket", sc.Ticket); err != nil {
			return "", fmt.Errorf("scenario %s: %w", sc.ID, err)
		}
		if err := idcheck.Scenario(string(sc.ID)); err != nil {
			return "", err
		}
		if known[sc.ID] {
			return "", fmt.Errorf("scenario %s exists already; nothing was written", sc.ID)
		}
		data, err := scenario.Render(sc, featureSubject(out.Draft, sc.Ticket))
		if err != nil {
			return "", fmt.Errorf("scenario %s: %w", sc.ID, err)
		}
		f := planFile{filepath.Join(".semar", "scenarios", sc.Ticket, string(sc.ID)+featureExt), data}
		scenarioFiles = append(scenarioFiles, f)
		s := out.Scripts[i]
		s.ExpectHash = domain.ExpectHash(sc)
		if s.AuthoredBy == (domain.ProviderRef{}) { // the criteria pipeline names the model of each script
			s.AuthoredBy = domain.ProviderRef{Instance: out.PlannerID, Model: out.Result.Model, Harness: out.PlannerKind}
		}
		scripts = append(scripts, s)
		rel, err := evidence.ScriptPath(s.Scenario, s.ExpectHash)
		if err != nil {
			return "", err
		}
		scriptFiles = append(scriptFiles, filepath.Join(".semar", rel))
		storeDirs = append(storeDirs, filepath.Dir(scriptFiles[i]))
		checked = append(checked, filepath.Dir(f.rel), filepath.Dir(scriptFiles[i]))
	}
	var fixtureFiles []planFile
	for _, d := range out.FixtureDrafts {
		if !d.Drafted {
			continue
		}
		if err := idcheck.Check("fixture", string(d.Fixture.ID)); err != nil {
			return "", fmt.Errorf("fixture %s: %w", d.Fixture.ID, err)
		}
		data, err := fixture.Marshal(d.Fixture)
		if err != nil {
			return "", err
		}
		f := planFile{filepath.Join(".semar", "fixtures", string(d.Fixture.ID)+".yaml"), data}
		fixtureFiles = append(fixtureFiles, f)
		checked = append(checked, filepath.Dir(f.rel))
	}
	var md bytes.Buffer
	if err := plan.Render(&md, in, out); err != nil {
		return "", err
	}
	draft, err := atomicfile.EncodeJSON(out.Draft)
	if err != nil {
		return "", fmt.Errorf("encode the draft: %w", err)
	}

	var answers []byte
	if len(out.Answers) > 0 {
		if answers, err = atomicfile.EncodeJSON(out.Answers); err != nil {
			return "", fmt.Errorf("encode the answers: %w", err)
		}
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
	obligationFiles, replaced, err := obligationsOf(root, in, out)
	if err != nil {
		return "", err
	}
	for _, f := range slices.Concat(fixtureFiles, scenarioFiles) {
		if _, err := root.Lstat(f.rel); !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s exists already; nothing was written", filepath.Join(dir, f.rel))
		}
	}
	for i, p := range scriptFiles {
		if _, err := root.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("a script of scenario %s for expectation hash %s is stored already; nothing was written", scripts[i].Scenario, scripts[i].ExpectHash)
		}
	}

	w := &planWriter{root: root, replaced: map[string][]byte{}}
	for _, d := range slices.Compact(storeDirs) {
		if _, err := root.Lstat(d); errors.Is(err, fs.ErrNotExist) {
			w.storeDirs = append(w.storeDirs, d)
		}
	}
	store, err := evidence.Open(semarDir(dir))
	if err != nil {
		return w.undo(err)
	}
	planDir, err := w.newPlanDir(in.Change.HeadCommit)
	if err != nil {
		return w.undo(err)
	}
	for _, f := range slices.Concat(fixtureFiles, scenarioFiles) {
		if err := w.writeFile(f); err != nil {
			return w.undo(err)
		}
	}
	for _, f := range obligationFiles {
		if old, ok := replaced[f.rel]; ok {
			if err := w.replaceFile(f, old); err != nil {
				return w.undo(err)
			}
			continue
		}
		if err := w.writeFile(f); err != nil {
			return w.undo(err)
		}
	}
	for i, s := range scripts {
		if err := store.PutScript(s); err != nil {
			return w.undo(err)
		}
		w.files = append(w.files, scriptFiles[i])
	}
	files := []planFile{{filepath.Join(planDir, "plan.md"), md.Bytes()}, {filepath.Join(planDir, "draft.json"), draft}}
	if len(answers) > 0 {
		files = append(files, planFile{filepath.Join(planDir, answersFile), answers})
	}
	for _, f := range files {
		if err := w.writeFile(f); err != nil {
			return w.undo(err)
		}
	}
	return filepath.Join(dir, planDir, "plan.md"), nil
}

// planWriter remembers what one plan wrote, so that undo can remove exactly that.
type planWriter struct {
	root      *os.Root
	dirs      []string // created, outermost first
	storeDirs []string // missing before the store was opened, outermost first
	files     []string
	replaced  map[string][]byte // an obligations file that the plan replaced, and what it held
	planDir   string
}

// replaceFile writes over an existing file, and remembers its content so that undo can put it back.
func (w *planWriter) replaceFile(f planFile, old []byte) error {
	if err := atomicfile.WriteIn(w.root, f.rel, f.data, planFileMode); err != nil {
		return err
	}
	w.replaced[f.rel] = old
	return nil
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
	for _, f := range w.files {
		if err := w.root.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	for _, rel := range slices.Sorted(maps.Keys(w.replaced)) {
		if err := atomicfile.WriteIn(w.root, rel, w.replaced[rel], planFileMode); err != nil {
			errs = append(errs, fmt.Errorf("put back %s: %w", filepath.Join(w.root.Name(), rel), err))
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
	for _, d := range slices.Backward(w.storeDirs) {
		if err := removeIfEmpty(w.root, d); err != nil {
			errs = append(errs, err)
		}
	}
	return "", fmt.Errorf("the plan was not written, and what it had written was removed: %w", errors.Join(errs...))
}

// removeIfEmpty keeps a directory that holds anything, because another command may have
// written there since the plan began.
func removeIfEmpty(root *os.Root, d string) error {
	dir, err := root.Open(d)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	names, err := dir.Readdirnames(1)
	_ = dir.Close()
	if len(names) > 0 || !errors.Is(err, io.EOF) {
		return nil
	}
	if err := root.Remove(d); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

var obligationsDir = filepath.Join(".semar", "obligations")

// obligationsOf makes the obligations files of a plan: one per ticket, with the entries of the
// file that is there kept (D3). Only a file that gains an entry is returned. replaced holds the
// bytes of each file that the plan will write over. A file that is there and cannot be read
// stops the plan before any write, because a plan must not hide an obligation that a person wrote.
func obligationsOf(root *os.Root, in plan.Input, out plan.Output) (files []planFile, replaced map[string][]byte, err error) {
	tickets := map[string]bool{}
	for _, t := range in.Change.Tickets {
		tickets[t.Key] = true
	}
	for _, sc := range out.Scenarios {
		tickets[sc.Ticket] = true
	}
	d := out.Draft
	for _, n := range d.NotDrafted {
		tickets[n.Ticket] = true
	}
	for _, df := range d.Differences {
		tickets[df.Ticket] = true
	}
	for _, m := range d.ManualChecks {
		tickets[m.Ticket] = true
	}
	scenarios := slices.Clone(out.Scenarios)
	for _, wt := range out.WebTests {
		tickets[wt.Scenario.Ticket] = true
		scenarios = append(scenarios, wt.Scenario)
	}
	existing := map[string]obligation.File{}
	old := map[string][]byte{}
	for _, key := range slices.Sorted(maps.Keys(tickets)) {
		if idcheck.Check("ticket", key) != nil {
			continue // FromPlan names it
		}
		rel := obligationsFile(key)
		data, err := obligation.ReadFile(root, rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w; nothing was written", err)
		}
		slashed := filepath.ToSlash(rel)
		f, err := obligation.Parse(data, slashed)
		var problems []obligation.Problem
		var pe *obligation.ParseError
		switch {
		case errors.As(err, &pe):
			problems = pe.Problems
		case err != nil:
			return nil, nil, err
		default:
			problems = obligation.Validate(f, slashed)
		}
		if len(problems) > 0 {
			more := ""
			if len(problems) > 1 {
				more = fmt.Sprintf(" (and %d more)", len(problems)-1)
			}
			return nil, nil, fmt.Errorf("%s%s; fix or remove it, and plan again; nothing was written", problems[0], more)
		}
		existing[key], old[rel] = f, data
	}

	made, err := obligation.FromPlanWith(in.Change, d, scenarios, out.Unaccounted, existing)
	if err != nil {
		return nil, nil, fmt.Errorf("make the obligations: %w; nothing was written", err)
	}
	replaced = map[string][]byte{}
	for _, key := range slices.Sorted(maps.Keys(made)) {
		f := made[key]
		if prev, ok := existing[key]; ok && len(f.Obligations) == len(prev.Obligations) {
			continue
		}
		data, err := obligation.Marshal(f)
		if err != nil {
			return nil, nil, fmt.Errorf("make the obligations of %s: %w; nothing was written", key, err)
		}
		rel := obligationsFile(key)
		if problems := obligation.Validate(f, filepath.ToSlash(rel)); len(problems) > 0 {
			return nil, nil, fmt.Errorf("%s; the plan cannot add to it: fix or remove it, and plan again; nothing was written", problems[0])
		}
		files = append(files, planFile{rel, data})
		if b, ok := old[rel]; ok {
			replaced[rel] = b
		}
	}
	return files, replaced, nil
}

// obligationsFile is the file that the plan of a ticket writes.
func obligationsFile(ticket string) string { return filepath.Join(obligationsDir, ticket+".plan.yaml") }
