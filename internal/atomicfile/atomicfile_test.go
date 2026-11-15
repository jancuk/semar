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

func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestMkdirInRefusesALinkOrAFileOnTheWay(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir, outside string)
		bad   string
		want  string
	}{
		{"a link at the first component", func(t *testing.T, dir, outside string) {
			symlinkOrSkip(t, outside, filepath.Join(dir, "a"))
		}, "a", "symbolic link"},
		{"a link deeper down", func(t *testing.T, dir, outside string) {
			if err := os.Mkdir(filepath.Join(dir, "a"), 0o700); err != nil {
				t.Fatal(err)
			}
			symlinkOrSkip(t, outside, filepath.Join(dir, "a", "b"))
		}, filepath.Join("a", "b"), "symbolic link"},
		{"a link to a directory inside the root", func(t *testing.T, dir, _ string) {
			if err := os.Mkdir(filepath.Join(dir, "real"), 0o700); err != nil {
				t.Fatal(err)
			}
			symlinkOrSkip(t, filepath.Join(dir, "real"), filepath.Join(dir, "a"))
		}, "a", "symbolic link"},
		{"a file", func(t *testing.T, dir, _ string) {
			if err := os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "a", "not a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			tc.setup(t, dir, outside)
			root := openRoot(t, dir)
			for name, call := range map[string]func() error{
				"MkdirIn":    func() error { _, err := atomicfile.MkdirIn(root, filepath.Join("a", "b", "c")); return err },
				"CheckDirIn": func() error { return atomicfile.CheckDirIn(root, filepath.Join("a", "b", "c")) },
			} {
				err := call()
				if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, tc.bad)) || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("%s: err = %v, want it to name %s and say %q", name, err, filepath.Join(dir, tc.bad), tc.want)
				}
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("something was created at the link's target: %v", entries)
			}
		})
	}
}

func TestMkdirInCreatesEachComponent(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := openRoot(t, dir)
	created, err := atomicfile.MkdirIn(root, filepath.Join("a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join("a", "b"), filepath.Join("a", "b", "c")}
	if strings.Join(created, ",") != strings.Join(want, ",") {
		t.Errorf("created %v, want %v", created, want)
	}
	if err := atomicfile.CheckDirIn(root, filepath.Join("a", "b", "c", "missing", "d")); err != nil {
		t.Errorf("CheckDirIn of a missing tail: %v", err)
	}
}

func TestWriteInStaysInsideTheRoot(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(dir, "out"))
	root := openRoot(t, dir)
	for name, write := range map[string]func(*os.Root, string, []byte, os.FileMode) error{
		"WriteIn": atomicfile.WriteIn, "WriteNewIn": atomicfile.WriteNewIn,
	} {
		if err := write(root, filepath.Join("out", "a.txt"), []byte("x"), 0o600); err == nil {
			t.Errorf("%s through a link out of the root succeeded", name)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("something was written at the link's target: %v", entries)
	}
}

func TestWriteNewInKeepsAnExistingFileAndSetsTheMode(t *testing.T) {
	dir := t.TempDir()
	root := openRoot(t, dir)
	if err := atomicfile.WriteNewIn(root, "a.txt", []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.WriteNewIn(root, "a.txt", []byte("second"), 0o644); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	info, err := os.Stat(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "first" || info.Mode().Perm() != 0o644 {
		t.Errorf("content %q, mode %o", data, info.Mode().Perm())
	}
	if err := atomicfile.WriteIn(root, "a.txt", []byte("third"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "third" {
		t.Errorf("content after WriteIn %q", data)
	}
	noTemp(t, dir)
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks are not available:", err)
	}
}
