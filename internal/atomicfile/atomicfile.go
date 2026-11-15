// Package atomicfile writes a stored file so that a reader never sees half of it.
package atomicfile

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	DirMode  os.FileMode = 0o700
	FileMode os.FileMode = 0o600
)

// Write creates the parent directories, writes data to a temporary file in the
// same directory, syncs it, and renames it over path. A failure leaves no temporary file.
func Write(path string, data []byte) error {
	tmpName, err := writeTemp(path, data)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	return nil
}

// WriteNew publishes data at path only when nothing is there, and never replaces a file.
// It reports an error that wraps fs.ErrExist when the file exists, so of two concurrent
// callers one wins whole and the other changes nothing.
func WriteNew(path string, data []byte) error {
	tmpName, err := writeTemp(path, data)
	if err != nil {
		return err
	}
	defer os.Remove(tmpName)
	if err := os.Link(tmpName, path); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	return nil
}

// writeTemp writes data to a synced temporary file next to path and returns its name.
func writeTemp(path string, data []byte) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return "", fmt.Errorf("create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	fail := func(step string, err error) (string, error) {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("%s %s: %w", step, path, err)
	}
	if err := tmp.Chmod(FileMode); err != nil {
		return fail("set mode of", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail("write", err)
	}
	if err := tmp.Sync(); err != nil {
		return fail("sync", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("close %s: %w", path, err)
	}
	return tmpName, nil
}

// MkdirIn creates each missing directory of rel under root, one component at a time, and
// returns the ones it created, deepest last. A component that is a symbolic link or not a
// directory is an error that names it: a repository can commit a link there, and writing
// through it would put files elsewhere.
func MkdirIn(root *os.Root, rel string) ([]string, error) {
	var created []string
	for _, p := range components(rel) {
		info, err := root.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			switch err := root.Mkdir(p, DirMode); {
			case err == nil:
				created = append(created, p)
			case !errors.Is(err, fs.ErrExist):
				return created, fmt.Errorf("create directory %s: %w", filepath.Join(root.Name(), p), err)
			}
			info, err = root.Lstat(p)
		}
		if err != nil {
			return created, fmt.Errorf("check %s: %w", filepath.Join(root.Name(), p), err)
		}
		if err := realDir(filepath.Join(root.Name(), p), info); err != nil {
			return created, err
		}
	}
	return created, nil
}

// CheckDirIn refuses a component of rel under root that exists and is a symbolic link or not
// a directory. A missing component ends the check, because nothing can be below it.
func CheckDirIn(root *os.Root, rel string) error {
	for _, p := range components(rel) {
		info, err := root.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("check %s: %w", filepath.Join(root.Name(), p), err)
		}
		if err := realDir(filepath.Join(root.Name(), p), info); err != nil {
			return err
		}
	}
	return nil
}

func realDir(shown string, info fs.FileInfo) error {
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link, and Semar writes only into real directories: replace it with a directory", shown)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory: remove it so that Semar can create the directory", shown)
	}
	return nil
}

// components returns a, a/b, a/b/c for a/b/c. "." and "" have none.
func components(rel string) []string {
	rel = filepath.Clean(rel)
	if rel == "." {
		return nil
	}
	var out []string
	for i := 0; i < len(rel); i++ {
		if os.IsPathSeparator(rel[i]) {
			out = append(out, rel[:i])
		}
	}
	return append(out, rel)
}

// WriteIn is Write for a name under root: the parent must exist, and the name is resolved
// inside root, so a link swapped in after a check cannot carry the write outside it.
func WriteIn(root *os.Root, name string, data []byte, mode os.FileMode) error {
	tmp, err := writeTempIn(root, name, data, mode)
	if err != nil {
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("rename to %s: %w", filepath.Join(root.Name(), name), err)
	}
	return nil
}

// WriteNewIn is WriteNew for a name under root. It never replaces a file, and it reports an
// error that wraps fs.ErrExist when one is there.
func WriteNewIn(root *os.Root, name string, data []byte, mode os.FileMode) error {
	tmp, err := writeTempIn(root, name, data, mode)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if err := root.Link(tmp, name); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Join(root.Name(), name), err)
	}
	return nil
}

func writeTempIn(root *os.Root, name string, data []byte, mode os.FileMode) (string, error) {
	shown := filepath.Join(root.Name(), name)
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("name a temporary file for %s: %w", shown, err)
	}
	tmpName := filepath.Join(filepath.Dir(name), ".tmp-"+hex.EncodeToString(suffix))
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, FileMode)
	if err != nil {
		return "", fmt.Errorf("create temporary file for %s: %w", shown, err)
	}
	fail := func(step string, err error) (string, error) {
		_ = tmp.Close()
		_ = root.Remove(tmpName)
		return "", fmt.Errorf("%s %s: %w", step, shown, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail("set mode of", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail("write", err)
	}
	if err := tmp.Sync(); err != nil {
		return fail("sync", err)
	}
	if err := tmp.Close(); err != nil {
		_ = root.Remove(tmpName)
		return "", fmt.Errorf("close %s: %w", shown, err)
	}
	return tmpName, nil
}

// EncodeJSON indents with two spaces and leaves <, >, and & as they are, so a stored body stays readable.
func EncodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func WriteJSON(path string, v any) error {
	data, err := EncodeJSON(v)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return Write(path, data)
}
