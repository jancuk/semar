package obligation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jancuk/semar/internal/atomicfile"
)

// editFileMode is the mode of an obligations file that the code writes back. People commit these
// files and read them, so they are not private.
const editFileMode = 0o644

// Edit changes one use case in the file that holds it, and writes the file back. The file is read
// again as it is now, so an edit never rests on an older copy. A change that leaves the file
// invalid is refused, and the file stays as it was. Comments in the file are not kept: the file is
// written as Marshal writes it.
func Edit(repo, id string, change func(*Obligation) error) error {
	list, problems, err := Load(repo)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("fix the obligations files first: %s", problems[0])
	}
	var rel string
	for _, l := range list {
		if l.ID == id {
			rel = l.File
		}
	}
	if rel == "" {
		return fmt.Errorf("obligation %s was not found in %s", id, obligationsD)
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return fmt.Errorf("edit obligations: %w", err)
	}
	defer root.Close()
	data, err := ReadFile(root, filepath.FromSlash(rel))
	if err != nil {
		return err
	}
	f, err := Parse(data, rel)
	if err != nil {
		return err
	}
	i := -1
	for j, o := range f.Obligations {
		if o.ID == id {
			i = j
		}
	}
	if i < 0 {
		return errors.New("the use case is not in its file any more")
	}
	if err := change(&f.Obligations[i]); err != nil {
		return err
	}
	if ps := Validate(f, rel); len(ps) > 0 {
		return fmt.Errorf("the change leaves %s invalid: %s", rel, ps[0])
	}
	out, err := Marshal(f)
	if err != nil {
		return err
	}
	return atomicfile.WriteIn(root, filepath.FromSlash(rel), out, editFileMode)
}
