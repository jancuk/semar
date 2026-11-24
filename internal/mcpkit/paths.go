package mcpkit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jancuk/semar/internal/atomicfile"
)

// maxAPK is the largest file that ReadAPK accepts.
const maxAPK = 4 << 30

// Outputs is the one folder that a tool server writes to: screenshots, recordings, and saved logs. A
// path that a tool takes from the model is resolved against it and never leaves it.
type Outputs struct {
	root string // as given, absolute
	real string // with the symlinks resolved
}

// NewOutputs makes the folder, with mode 0700, and a .gitignore of * when the folder has none or an empty
// one. An existing .gitignore with content is left as it is.
func NewOutputs(root string) (*Outputs, error) {
	if root == "" {
		return nil, errors.New("the output folder is not set")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("the output folder %q cannot be resolved: %w", root, err)
	}
	if err := os.MkdirAll(abs, atomicfile.DirMode); err != nil {
		return nil, fmt.Errorf("the output folder could not be made: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("the output folder cannot be resolved: %w", err)
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return nil, errors.New("the output folder is not a folder")
	}
	ign := filepath.Join(real, ".gitignore")
	if info, err := os.Lstat(ign); errors.Is(err, fs.ErrNotExist) || (err == nil && info.Mode().IsRegular() && info.Size() == 0) {
		if err := atomicfile.Write(ign, []byte("*\n")); err != nil {
			return nil, fmt.Errorf("the .gitignore of the output folder could not be written: %w", err)
		}
	}
	return &Outputs{root: abs, real: real}, nil
}

// Root is the folder, as an absolute path.
func (o *Outputs) Root() string { return o.root }

// Resolve gives the absolute path that p means inside the folder. A relative path is joined to the folder,
// and an absolute one must lie in it. It refuses an empty path, a NUL byte, a .. that leaves the folder, a
// parent that resolves outside the folder through a symlink, a target that is a symlink or is not a regular
// file, and an extension that is not in exts (compared without case; none means any).
func (o *Outputs) Resolve(p string, exts []string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("the path is empty")
	}
	if strings.ContainsRune(p, 0) {
		return "", errors.New("the path holds a NUL byte")
	}
	rel, err := o.rel(p)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", errors.New("the path is the output folder itself, not a file in it")
	}
	if len(exts) > 0 {
		ext := strings.ToLower(filepath.Ext(rel))
		ok := false
		for _, e := range exts {
			if ext == strings.ToLower(e) {
				ok = true
				break
			}
		}
		if !ok {
			return "", fmt.Errorf("the extension %q is not one of %s", ext, strings.Join(exts, ", "))
		}
	}
	// The nearest parent that exists must resolve to a place inside the folder.
	parent := filepath.Dir(filepath.Join(o.real, rel))
	for {
		if _, err := os.Lstat(parent); err == nil {
			break
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", errors.New("the folder of the path cannot be resolved")
	}
	if !within(o.real, resolved) {
		return "", errors.New("the path leaves the output folder through a symbolic link")
	}
	target := filepath.Join(o.real, rel)
	if info, err := os.Lstat(target); err == nil {
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			return "", errors.New("the path is a symbolic link")
		case !info.Mode().IsRegular():
			return "", errors.New("the path is not a regular file")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", errors.New("the path cannot be checked")
	}
	return target, nil
}

// rel turns p into a local path below the folder.
func (o *Outputs) rel(p string) (string, error) {
	if filepath.IsAbs(p) {
		clean := filepath.Clean(p)
		for _, base := range []string{o.root, o.real} {
			if r, err := filepath.Rel(base, clean); err == nil && (r == "." || filepath.IsLocal(r)) {
				return r, nil
			}
		}
		return "", errors.New("the path is not inside the output folder")
	}
	if !filepath.IsLocal(p) {
		return "", errors.New("the path leaves the output folder: it must not use .. or start at the root")
	}
	return filepath.Clean(p), nil
}

// within reports whether path is dir or lies below it.
func within(dir, path string) bool {
	r, err := filepath.Rel(dir, path)
	return err == nil && (r == "." || filepath.IsLocal(r))
}

// Write checks the path as Resolve does and writes data to it with mode 0600, so that a reader never sees
// half of the file. It gives the absolute path.
func (o *Outputs) Write(p string, exts []string, data []byte) (string, error) {
	path, err := o.Resolve(p, exts)
	if err != nil {
		return "", err
	}
	if err := atomicfile.Write(path, data); err != nil {
		return "", fmt.Errorf("the file could not be written: %w", err)
	}
	return path, nil
}

// ReadAPK checks the path of an app to install: an absolute path, or one relative to the repository (and
// then repoRoot must be set). It must be a regular file that is not a symbolic link, end in .apk, and hold
// at most 4 GiB. It gives the absolute path.
func ReadAPK(p string, repoRoot string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("the path is empty")
	}
	if strings.ContainsRune(p, 0) {
		return "", errors.New("the path holds a NUL byte")
	}
	path := p
	if !filepath.IsAbs(p) {
		if repoRoot == "" {
			return "", errors.New("the path must be absolute: there is no repository to resolve it against")
		}
		if !filepath.IsLocal(p) {
			return "", errors.New("the path leaves the repository: it must not use ..")
		}
		path = filepath.Join(repoRoot, p)
	}
	path = filepath.Clean(path)
	if !strings.EqualFold(filepath.Ext(path), ".apk") {
		return "", errors.New("the file must end in .apk")
	}
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", errors.New("the file does not exist")
	case err != nil:
		return "", errors.New("the file cannot be checked")
	case info.Mode()&os.ModeSymlink != 0:
		return "", errors.New("the file is a symbolic link")
	case !info.Mode().IsRegular():
		return "", errors.New("the path is not a regular file")
	case info.Size() > maxAPK:
		return "", errors.New("the file is larger than 4 GiB")
	case info.Size() == 0:
		return "", errors.New("the file is empty")
	}
	return path, nil
}
