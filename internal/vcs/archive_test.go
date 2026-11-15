package vcs

import (
	"archive/tar"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/vcs/gittest"
)

func archiveFixture(t *testing.T) (*Git, string) {
	t.Helper()
	r := gittest.New(t)
	r.Write("target.txt", "target\n")
	if err := os.Symlink("target.txt", filepath.Join(r.Dir, "link")); err != nil {
		t.Skipf("symlinks: %v", err)
	}
	id := r.Commit("files", map[string]*string{
		"main.go":        gittest.S("package main\n"),
		"pkg/sub/dep.go": gittest.S("package sub\n"),
		"big.bin":        gittest.S(strings.Repeat("x", 2000)),
		"pkg/small.txt":  gittest.S("small\n"),
	})
	return open(t, r.Dir), id
}

func listFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b, err := os.ReadFile(p)
		got[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestArchive(t *testing.T) {
	g, id := archiveFixture(t)
	dest := filepath.Join(t.TempDir(), "copy")
	if err := g.Archive(context.Background(), id, dest); err != nil {
		t.Fatal(err)
	}
	got := listFiles(t, dest)
	want := map[string]string{
		"main.go":        "package main\n",
		"pkg/sub/dep.go": "package sub\n",
		"pkg/small.txt":  "small\n",
		"big.bin":        strings.Repeat("x", 2000),
		"target.txt":     "target\n",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", keys(got), keys(want))
	}
	if _, err := os.Lstat(filepath.Join(dest, "link")); err == nil {
		t.Fatal("a symbolic link was copied")
	}
	for _, p := range []string{".", "pkg", "pkg/sub"} {
		info, err := os.Stat(filepath.Join(dest, p))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("directory %s: %v %v", p, info, err)
		}
	}
	info, err := os.Stat(filepath.Join(dest, "main.go"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file mode: %v %v", info, err)
	}
}

func keys(m map[string]string) []string {
	var k []string
	for p := range m {
		k = append(k, p)
	}
	return k
}

func TestArchiveSkipsFileAboveCap(t *testing.T) {
	g, id := archiveFixture(t)
	g.archiveFileCap = 1000
	dest := filepath.Join(t.TempDir(), "copy")
	if err := g.Archive(context.Background(), id, dest); err != nil {
		t.Fatal(err)
	}
	got := listFiles(t, dest)
	if _, ok := got["big.bin"]; ok {
		t.Fatal("a file above the cap was copied")
	}
	if got["main.go"] == "" || got["pkg/small.txt"] == "" {
		t.Fatalf("smaller files were lost: %v", keys(got))
	}
}

func TestArchiveTotalCap(t *testing.T) {
	g, id := archiveFixture(t)
	g.archiveTotalCap = 1500
	dest := filepath.Join(t.TempDir(), "copy")
	err := g.Archive(context.Background(), id, dest)
	if err == nil || !strings.Contains(err.Error(), "larger than 1500 bytes") {
		t.Fatalf("got %v, want a cap error", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Fatal("the partial copy was left behind")
	}
}

func TestArchiveDestExists(t *testing.T) {
	g, id := archiveFixture(t)
	dest := t.TempDir()
	if err := g.Archive(context.Background(), id, dest); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v, want an error", err)
	}
	if err := g.Archive(context.Background(), "no-such-commit", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("an unknown commit was archived")
	}
}

func TestExtractTar(t *testing.T) {
	build := func(entries ...tar.Header) *bytes.Reader {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, h := range entries {
			h := h
			if h.Typeflag == tar.TypeReg || h.Typeflag == 0 {
				h.Typeflag = tar.TypeReg
				h.Size = 3
				if err := tw.WriteHeader(&h); err != nil {
					t.Fatal(err)
				}
				_, _ = tw.Write([]byte("abc"))
				continue
			}
			if err := tw.WriteHeader(&h); err != nil {
				t.Fatal(err)
			}
		}
		_ = tw.Close()
		return bytes.NewReader(buf.Bytes())
	}
	cases := []struct {
		name    string
		tar     *bytes.Reader
		wantErr string
		want    []string
	}{
		{"parent escape", build(tar.Header{Name: "../evil"}), "not local", nil},
		{"nested escape", build(tar.Header{Name: "a/../../evil"}), "not local", nil},
		{"absolute", build(tar.Header{Name: "/etc/evil"}), "not local", nil},
		{"symlink skipped", build(tar.Header{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, tar.Header{Name: "ok"}), "", []string{"ok"}},
		{"hard link skipped", build(tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "ok"}, tar.Header{Name: "ok"}), "", []string{"ok"}},
		{"device skipped", build(tar.Header{Name: "dev", Typeflag: tar.TypeChar}, tar.Header{Name: "ok"}), "", []string{"ok"}},
		{"duplicate", build(tar.Header{Name: "x"}, tar.Header{Name: "x"}), "exist", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "out")
			if err := os.Mkdir(dest, 0o700); err != nil {
				t.Fatal(err)
			}
			err := extractTar(c.tar, dest, 100, 100)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("got %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for p := range listFiles(t, dest) {
				names = append(names, p)
			}
			if !reflect.DeepEqual(names, c.want) {
				t.Fatalf("got %v, want %v", names, c.want)
			}
		})
	}
}
