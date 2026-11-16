package evidence

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jancuk/semar/internal/domain"
)

// MaxActionBytes is the largest action file that ReadAction reads.
const MaxActionBytes = 2 << 20

// ErrActionTooLarge is wrapped by the error of ReadAction for a file above MaxActionBytes.
var ErrActionTooLarge = errors.New("the action file is too large to read")

// ReadAction reads one stored action of a run. rel is the path of an activity entry's
// Evidence, relative to the run directory.
//
// The activity log is untrusted input, so the file is opened through a root at the run
// directory: a link, a directory, a device, or a path that leaves the run is refused.
func (s *Store) ReadAction(id domain.RunID, rel string) (domain.Action, error) {
	dir, err := s.runDir(id)
	if err != nil {
		return domain.Action{}, fmt.Errorf("read action: %w", err)
	}
	native := filepath.FromSlash(rel)
	if !filepath.IsLocal(native) {
		return domain.Action{}, fmt.Errorf("read action of run %s: the path %q is not local to the run directory", id, rel)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return domain.Action{}, fmt.Errorf("read action %s of run %s: %w", rel, id, err)
	}
	defer root.Close()

	info, err := root.Lstat(native)
	if err != nil {
		return domain.Action{}, fmt.Errorf("read action %s of run %s: %w", rel, id, err)
	}
	if err := refuseIrregular(rel, id, info); err != nil {
		return domain.Action{}, err
	}
	f, err := root.Open(native)
	if err != nil {
		return domain.Action{}, fmt.Errorf("read action %s of run %s: %w", rel, id, err)
	}
	defer f.Close()
	// the file could have been swapped for another between the check and the open
	opened, err := f.Stat()
	if err != nil {
		return domain.Action{}, fmt.Errorf("read action %s of run %s: %w", rel, id, err)
	}
	if !os.SameFile(info, opened) {
		return domain.Action{}, fmt.Errorf("read action %s of run %s: the file changed while it was read", rel, id)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxActionBytes+1))
	if err != nil {
		return domain.Action{}, fmt.Errorf("read action %s of run %s: %w", rel, id, err)
	}
	if len(data) > MaxActionBytes {
		return domain.Action{}, fmt.Errorf("read action %s of run %s: %w (above %d bytes)", rel, id, ErrActionTooLarge, MaxActionBytes)
	}
	var a domain.Action
	if err := decodeStrict(data, &a); err != nil {
		return domain.Action{}, fmt.Errorf("parse action %s of run %s: %w", rel, id, err)
	}
	return a, nil
}

func refuseIrregular(rel string, id domain.RunID, info fs.FileInfo) error {
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("read action %s of run %s: the file is a link, and Semar reads only regular files", rel, id)
	case !info.Mode().IsRegular():
		return fmt.Errorf("read action %s of run %s: the file is not a regular file", rel, id)
	case info.Size() > MaxActionBytes:
		return fmt.Errorf("read action %s of run %s: %w (%d bytes, above %d)", rel, id, ErrActionTooLarge, info.Size(), MaxActionBytes)
	}
	return nil
}
