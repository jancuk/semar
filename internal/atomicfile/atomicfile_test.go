package atomicfile_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/atomicfile"
)

func noTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temporary file %s left behind", e.Name())
		}
	}
}

func TestWriteCreatesWithMode0600(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "a.txt")
	if err := atomicfile.Write(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}
	if d, _ := os.Stat(filepath.Dir(p)); d.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %o", d.Mode().Perm())
	}
}

func TestOverwriteKeepsModeAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	for _, content := range []string{"one", "two"} {
		if err := atomicfile.Write(p, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(p)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "two" || info.Mode().Perm() != 0o600 {
		t.Errorf("content %q mode %o", data, info.Mode().Perm())
	}
	noTemp(t, dir)
}

func TestFailedWriteLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	// the target is a non-empty directory, so the rename fails after the temp file is written
	p := filepath.Join(dir, "target")
	if err := os.MkdirAll(filepath.Join(p, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(p, []byte("x")); err == nil {
		t.Fatal("write over a directory succeeded")
	}
	noTemp(t, dir)
}

func TestEncodeJSON(t *testing.T) {
	got, err := atomicfile.EncodeJSON(map[string]any{"a": "<b>&", "n": []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": \"<b>&\",\n  \"n\": [\n    1\n  ]\n}\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteNewKeepsAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := atomicfile.WriteNew(p, []byte("first")); err != nil {
		t.Fatal(err)
	}
	err := atomicfile.WriteNew(p, []byte("second"))
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if data, _ := os.ReadFile(p); string(data) != "first" {
		t.Errorf("content = %q", data)
	}
	noTemp(t, dir)
}
