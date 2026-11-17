package record

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/screens"
)

const fileMode = 0o644

// WriteOptions say where the files go.
type WriteOptions struct {
	App     string // the screens file
	Subject string // the Feature line of the scenario
	// Replace lists the scenario files of this ID that the caller means to replace, relative to the
	// repository. A scenario file that is not listed is never written over.
	Replace []string
}

// Written says what Write made. The paths are relative to the repository, with slashes.
type Written struct {
	ScreensPath  string
	ScenarioPath string
	Screens      int // in the screens file after the write
	Elements     int
}

// Write puts the screens and the scenario of a draft in the repository. Both files are made in
// memory first and the target folders are checked, so a draft that cannot be written leaves
// nothing behind. A failure while writing puts back what it had changed.
func Write(repo string, d Draft, o WriteOptions) (Written, error) {
	if err := idcheck.Check("ticket", d.Scenario.Ticket); err != nil {
		return Written{}, err
	}
	if err := idcheck.Scenario(string(d.Scenario.ID)); err != nil {
		return Written{}, err
	}
	feature, err := scenario.Render(d.Scenario, o.Subject)
	if err != nil {
		return Written{}, err
	}
	current, err := screens.Read(repo, o.App)
	missing := errors.Is(err, screens.ErrNotFound)
	if err != nil && !missing {
		return Written{}, err
	}
	var merged []byte
	switch {
	case missing:
		merged, err = screens.New(o.App, d.Added)
	case len(d.Added) == 0:
		merged = current
	default:
		merged, err = screens.Add(current, o.App, d.Added)
	}
	if err != nil {
		return Written{}, err
	}
	parsed, err := screens.Parse(merged, screens.Path(o.App), o.App)
	if err != nil {
		return Written{}, err
	}

	res := Written{
		ScreensPath:  screens.Path(o.App),
		ScenarioPath: filepath.ToSlash(filepath.Join(".semar", "scenarios", d.Scenario.Ticket, string(d.Scenario.ID)+".feature")),
		Screens:      len(parsed.Screens),
	}
	for _, name := range parsed.ScreenNames() {
		res.Elements += len(parsed.Screens[name])
	}

	root, err := os.OpenRoot(repo)
	if err != nil {
		return Written{}, err
	}
	defer root.Close()
	for _, dir := range []string{filepath.Dir(res.ScreensPath), filepath.Dir(res.ScenarioPath)} {
		if err := atomicfile.CheckDirIn(root, dir); err != nil {
			return Written{}, err
		}
	}

	if _, err := root.Lstat(res.ScenarioPath); err == nil && !slices.Contains(o.Replace, res.ScenarioPath) {
		return Written{}, fmt.Errorf("%s exists already: %w", res.ScenarioPath, fs.ErrExist)
	}

	w := &writer{root: root}
	if err := w.run(res, merged, feature, o.Replace); err != nil {
		return Written{}, w.undo(err)
	}
	return res, nil
}

// writer remembers what a write changed, so that undo can put it back.
type writer struct {
	root    *os.Root
	dirs    []string
	made    []string          // files that did not exist
	changed map[string][]byte // files that were replaced or removed, and what they held
}

func (w *writer) run(res Written, screensText []byte, feature []byte, replace []string) error {
	w.changed = map[string][]byte{}
	for _, dir := range []string{filepath.Dir(res.ScreensPath), filepath.Dir(res.ScenarioPath)} {
		created, err := atomicfile.MkdirIn(w.root, dir)
		w.dirs = append(w.dirs, created...)
		if err != nil {
			return err
		}
	}
	if err := w.put(res.ScreensPath, screensText, true); err != nil {
		return err
	}
	if err := w.put(res.ScenarioPath, feature, slices.Contains(replace, res.ScenarioPath)); err != nil {
		return err
	}
	for _, old := range replace {
		if old == res.ScenarioPath {
			continue
		}
		data, err := fs.ReadFile(w.root.FS(), filepath.ToSlash(old))
		if err != nil {
			return fmt.Errorf("read the scenario that is replaced, %s: %w", old, err)
		}
		if err := w.root.Remove(old); err != nil {
			return fmt.Errorf("remove the scenario that is replaced, %s: %w", old, err)
		}
		w.changed[old] = data
	}
	return nil
}

// put writes a file. A file that is there is replaced only when overwrite is set, and a screens
// file that did not change is left alone.
func (w *writer) put(rel string, data []byte, overwrite bool) error {
	old, err := fs.ReadFile(w.root.FS(), rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := atomicfile.WriteNewIn(w.root, rel, data, fileMode); err != nil {
			return err
		}
		w.made = append(w.made, rel)
		return nil
	case err != nil:
		return err
	case !overwrite:
		return fmt.Errorf("%s exists already: %w", rel, fs.ErrExist)
	case string(old) == string(data):
		return nil
	}
	if err := atomicfile.WriteIn(w.root, rel, data, fileMode); err != nil {
		return err
	}
	w.changed[rel] = old
	return nil
}

func (w *writer) undo(cause error) error {
	var problems []error
	for _, rel := range w.made {
		if err := w.root.Remove(rel); err != nil {
			problems = append(problems, err)
		}
	}
	for rel, data := range w.changed {
		if err := atomicfile.WriteIn(w.root, rel, data, fileMode); err != nil {
			problems = append(problems, err)
		}
	}
	for i := len(w.dirs) - 1; i >= 0; i-- {
		if err := w.root.Remove(w.dirs[i]); err != nil {
			problems = append(problems, err)
		}
	}
	if len(problems) > 0 {
		return errors.Join(append([]error{cause}, problems...)...)
	}
	return cause
}
