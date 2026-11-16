package fixture

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
)

// Leases are file locks under the person's Semar home. The operating system holds each lock for
// the process, so a crash releases it. A lease covers one machine only (FR-281).
type Leases struct {
	dir  string
	info fs.FileInfo // the directory as it was opened, so a swapped one is refused
}

// OpenLeases creates <home>/leases with mode 0700 when it is missing. home is the Semar home,
// never a path from the repository. A link or a file at leases is an error.
func OpenLeases(home string) (*Leases, error) {
	if err := os.MkdirAll(home, atomicfile.DirMode); err != nil {
		return nil, fmt.Errorf("open leases: %w", err)
	}
	dir := filepath.Join(home, "leases")
	if err := os.Mkdir(dir, atomicfile.DirMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("open leases: %w", err)
	}
	info, err := os.Lstat(dir)
	switch {
	case err != nil:
		return nil, fmt.Errorf("open leases: %w", err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("%s is a symbolic link, and Semar keeps leases only in a real directory: replace it with a directory", dir)
	case !info.IsDir():
		return nil, fmt.Errorf("%s is not a directory: remove it so that Semar can create the directory", dir)
	}
	return &Leases{dir: dir, info: info}, nil
}

// leaseName is the file of one lease. The values are hashed, so no value names a path.
func leaseName(env string, id domain.FixtureID, value string) string {
	sum := sha256.Sum256([]byte(env + "\n" + string(id) + "\n" + value))
	return hex.EncodeToString(sum[:]) + ".lock"
}

// TryLock takes the lease of one record and never blocks. ok is false when another holder, in
// this process or another one, has it. release closes the file and is safe to call twice. The
// file is never removed: removing it would let two holders lock two different files.
func (l *Leases) TryLock(env string, id domain.FixtureID, value string) (release func(), ok bool, err error) {
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return nil, false, fmt.Errorf("take lease: %w", err)
	}
	defer root.Close()
	opened, err := root.Lstat(".")
	if err != nil || !os.SameFile(l.info, opened) {
		return nil, false, fmt.Errorf("take lease: %s changed since it was opened", l.dir)
	}
	name := leaseName(env, id, value)
	f, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE, atomicfile.FileMode)
	if err != nil {
		return nil, false, fmt.Errorf("take lease: %w", err)
	}
	locked, err := lockFile(f)
	if err != nil || !locked {
		_ = f.Close()
		if err != nil {
			return nil, false, fmt.Errorf("take lease %s: %w", filepath.Join(l.dir, name), err)
		}
		return nil, false, nil
	}
	var once sync.Once
	return func() { once.Do(func() { _ = f.Close() }) }, true, nil
}
