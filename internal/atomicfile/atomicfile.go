// Package atomicfile writes a stored file so that a reader never sees half of it.
package atomicfile

import (
	"bytes"
	"encoding/json"
	"fmt"
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
