package mobilerec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/screens"
)

const (
	fileMode = 0o644
	// maxFile bounds what Undo reads of a file: a screens file, a feature, a script, and a flow are small.
	maxFile = 4 << 20
)

// SaveOptions change how a save treats what is there.
type SaveOptions struct {
	// Replace lets the save write over the flow or the scenario of the same ID.
	Replace bool
}

// snap is a file that a save wrote: what it held before, and the sha256 of what the save made of it.
type snap struct {
	rel  string
	had  bool
	prev []byte
	sum  string
}

// Saved is one save of a recording. It can undo that save, once, and only while every file that the save
// wrote is as the save left it.
type Saved struct {
	// Files are the files that the save wrote, relative to the repository, with slashes.
	Files []string
	// ScreensPath, FlowPath, and ScenarioPath are the files that the recording is about.
	ScreensPath  string
	FlowPath     string
	ScenarioPath string
	Draft        Draft

	repo  string
	snaps []snap
	dirs  []string // folders that the save made, outer first
	mu    sync.Mutex
	done  bool
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Save writes the draft of the recording: the flow, or the feature and the script of the scenario, and
// the screens file with the new elements added, and nothing else of it changed. It builds the draft
// again, so what it writes fits the screens file as it is now. The save can be undone once.
func (s *Session) Save(o SaveOptions) (*Saved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.buildLocked()
	if err != nil {
		return nil, err
	}
	sv, err := writeDraft(s.o.Repo, d, o, s.o.Title)
	if err != nil {
		return nil, err
	}
	s.last = sv
	return sv, nil
}

// Undo puts the repository back as it was before the latest save. It is one shot, it refuses when a file
// that the save wrote has changed since, and it does nothing for an older save.
func (s *Session) Undo() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return ErrNothingToUndo
	}
	if err := s.last.Undo(); err != nil {
		return err
	}
	s.last = nil
	return nil
}

// Last is the latest save that has not been undone, or nil.
func (s *Session) Last() *Saved {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func writeDraft(repo string, d Draft, o SaveOptions, title string) (*Saved, error) {
	sv := &Saved{repo: repo, Draft: d, ScreensPath: screens.Path(d.App)}
	type file struct {
		rel  string
		data []byte
		// replace is whether a file that is there may be written over.
		replace bool
	}
	var files []file

	// The screens file first: what a flow or a scenario names must be there before it is.
	current, err := screens.Read(repo, d.App)
	missing := errors.Is(err, screens.ErrNotFound)
	if err != nil && !missing {
		return nil, err
	}
	var merged []byte
	switch {
	case missing && len(d.Added) == 0:
		return nil, errors.New("the recording uses no element, so there is no screens file to write")
	case missing:
		merged, err = screens.New(d.App, d.Added)
	case len(d.Added) == 0:
		merged = current
	default:
		merged, err = screens.Add(current, d.App, d.Added)
	}
	if err != nil {
		return nil, err
	}
	if _, err := screens.Parse(merged, sv.ScreensPath, d.App); err != nil {
		return nil, err
	}
	if missing || len(d.Added) > 0 {
		files = append(files, file{rel: sv.ScreensPath, data: merged, replace: true})
	}

	switch d.Kind {
	case KindFlow:
		data, err := flow.Marshal(d.Flow)
		if err != nil {
			return nil, err
		}
		sv.FlowPath = flow.Path(d.ID)
		files = append(files, file{rel: sv.FlowPath, data: data, replace: o.Replace})
	default:
		if err := idcheck.Check("ticket", d.Scenario.Ticket); err != nil {
			return nil, err
		}
		if err := idcheck.Scenario(string(d.Scenario.ID)); err != nil {
			return nil, err
		}
		feature, err := scenario.Render(d.Scenario, title)
		if err != nil {
			return nil, err
		}
		sv.ScenarioPath = filepath.ToSlash(filepath.Join(".semar", "scenarios", d.Scenario.Ticket, string(d.Scenario.ID)+".feature"))
		files = append(files, file{rel: sv.ScenarioPath, data: feature, replace: o.Replace})
	}

	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	// What exists now, so that a failure and an undo put it back.
	scriptRel := ""
	if d.Kind == KindScenario {
		p, err := evidence.ScriptPath(d.Script.Scenario, d.Script.ExpectHash)
		if err != nil {
			return nil, err
		}
		scriptRel = filepath.ToSlash(filepath.Join(".semar", p))
	}
	targets := []string{}
	for _, f := range files {
		targets = append(targets, f.rel)
	}
	if scriptRel != "" {
		targets = append(targets, scriptRel)
	}
	for _, rel := range targets {
		prev, err := readFile(root, rel)
		switch {
		case err == nil:
			sv.snaps = append(sv.snaps, snap{rel: rel, had: true, prev: prev})
		case errors.Is(err, fs.ErrNotExist):
			sv.snaps = append(sv.snaps, snap{rel: rel})
		default:
			return nil, err
		}
	}
	for _, f := range files {
		if f.rel == sv.ScreensPath {
			continue
		}
		if sn := sv.snapOf(f.rel); sn.had && !f.replace {
			return nil, fmt.Errorf("%s exists already: %w; save it with replace to record it again", f.rel, fs.ErrExist)
		}
	}
	dirs := candidateDirs(targets)
	missingDirs := map[string]bool{}
	for _, dir := range dirs {
		if _, err := root.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
			missingDirs[dir] = true
		}
	}

	rollback := func(cause error) error {
		sv.rollback(root)
		return cause
	}
	for _, f := range files {
		made, err := atomicfile.MkdirIn(root, filepath.ToSlash(filepath.Dir(f.rel)))
		if err != nil {
			return nil, rollback(err)
		}
		_ = made
		if err := atomicfile.WriteIn(root, f.rel, f.data, fileMode); err != nil {
			return nil, rollback(err)
		}
	}
	if scriptRel != "" {
		store, err := evidence.Open(filepath.Join(repo, ".semar"))
		if err != nil {
			return nil, rollback(err)
		}
		if err := store.PutScript(d.Script); err != nil {
			return nil, rollback(err)
		}
	}
	for i := range sv.snaps {
		data, err := readFile(root, sv.snaps[i].rel)
		if err != nil {
			return nil, rollback(err)
		}
		sv.snaps[i].sum = sum(data)
		sv.Files = append(sv.Files, sv.snaps[i].rel)
	}
	for _, dir := range dirs {
		if missingDirs[dir] {
			sv.dirs = append(sv.dirs, dir)
		}
	}
	return sv, nil
}

func (sv *Saved) snapOf(rel string) snap {
	for _, sn := range sv.snaps {
		if sn.rel == rel {
			return sn
		}
	}
	return snap{}
}

// candidateDirs lists the folders above the files, outer first, without repeats.
func candidateDirs(files []string) []string {
	var out []string
	for _, f := range files {
		var chain []string
		for d := filepath.ToSlash(filepath.Dir(f)); d != "." && d != "/" && d != ""; d = filepath.ToSlash(filepath.Dir(d)) {
			chain = append([]string{d}, chain...)
		}
		for _, d := range chain {
			if !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	return out
}

func readFile(root *os.Root, rel string) ([]byte, error) {
	info, err := root.Lstat(rel)
	switch {
	case err != nil:
		return nil, err
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file, and Semar replaces only real files", rel)
	case info.Size() > maxFile:
		return nil, fmt.Errorf("%s is larger than %d MiB", rel, maxFile>>20)
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxFile+1))
}

// rollback puts back what a failed save had changed, with no check: nothing else wrote since.
func (sv *Saved) rollback(root *os.Root) {
	sv.restore(root)
}

func (sv *Saved) restore(root *os.Root) []error {
	var problems []error
	for _, sn := range sv.snaps {
		var err error
		if sn.had {
			err = atomicfile.WriteIn(root, sn.rel, sn.prev, fileMode)
		} else if rmErr := root.Remove(sn.rel); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			err = rmErr
		}
		if err != nil {
			problems = append(problems, err)
		}
	}
	for i := len(sv.dirs) - 1; i >= 0; i-- {
		// A folder that something else put a file in is left alone.
		_ = root.Remove(sv.dirs[i])
	}
	return problems
}

// Undo puts back what the save changed: the files that it made are removed, and the files that it replaced
// get their old text. It runs once. It refuses, and changes nothing, when a file that the save wrote is
// gone or is not what the save left, so a later edit or a later save is never lost.
func (sv *Saved) Undo() error {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.done {
		return errors.New("the save was undone already")
	}
	root, err := os.OpenRoot(sv.repo)
	if err != nil {
		return fmt.Errorf("undo: %w", err)
	}
	defer root.Close()
	for _, sn := range sv.snaps {
		cur, err := readFile(root, sn.rel)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("undo: %s is gone, so nothing is undone", sn.rel)
		case err != nil:
			return fmt.Errorf("undo: %w", err)
		case sum(cur) != sn.sum:
			return fmt.Errorf("undo: %s changed since it was saved, so nothing is undone", sn.rel)
		}
	}
	if problems := sv.restore(root); len(problems) > 0 {
		return fmt.Errorf("undo: %w", errors.Join(problems...))
	}
	sv.done = true
	return nil
}
