package vcs

import (
	"bytes"
	"context"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
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

// gitStdin runs git in dir with stdin and returns its trimmed standard output.
func gitStdin(t *testing.T, dir string, stdin []byte, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &gitError{args: args, stderr: stderr.String(), err: err}
	}
	return strings.TrimSpace(string(out)), nil
}

// commitIndex commits the index of r as it is, with no check of the work tree.
func commitIndex(t *testing.T, r *gittest.Repo) string {
	t.Helper()
	tree := r.Git("write-tree")
	return r.Git("commit-tree", tree, "-m", "index")
}

func TestArchive(t *testing.T) {
	g, id := archiveFixture(t)
	dest := filepath.Join(t.TempDir(), "copy")
	skipped, err := g.Archive(context.Background(), id, dest)
	if err != nil {
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
	if wantSkipped := []Skipped{{"link", "a link"}}; !reflect.DeepEqual(skipped, wantSkipped) {
		t.Fatalf("skipped = %v, want %v", skipped, wantSkipped)
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
	slices.Sort(k)
	return k
}

func TestArchiveSkipsFileAboveCap(t *testing.T) {
	g, id := archiveFixture(t)
	g.archiveFileCap = 1000
	dest := filepath.Join(t.TempDir(), "copy")
	skipped, err := g.Archive(context.Background(), id, dest)
	if err != nil {
		t.Fatal(err)
	}
	got := listFiles(t, dest)
	if _, ok := got["big.bin"]; ok {
		t.Fatal("a file above the cap was copied")
	}
	if got["main.go"] == "" || got["pkg/small.txt"] == "" {
		t.Fatalf("smaller files were lost: %v", keys(got))
	}
	want := []Skipped{{"big.bin", "larger than 5 MiB"}, {"link", "a link"}}
	if !reflect.DeepEqual(skipped, want) {
		t.Fatalf("skipped = %v, want %v", skipped, want)
	}
}

func TestArchiveTotalCap(t *testing.T) {
	g, id := archiveFixture(t)
	g.archiveTotalCap = 1500
	dest := filepath.Join(t.TempDir(), "copy")
	_, err := g.Archive(context.Background(), id, dest)
	if err == nil || !strings.Contains(err.Error(), "larger than 1500 bytes") {
		t.Fatalf("got %v, want a cap error", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Fatal("a partial copy was left behind")
	}
}

func TestArchiveDestExists(t *testing.T) {
	g, id := archiveFixture(t)
	dest := t.TempDir()
	if _, err := g.Archive(context.Background(), id, dest); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v, want an error", err)
	}
	missing := filepath.Join(t.TempDir(), "x")
	if _, err := g.Archive(context.Background(), "no-such-commit", missing); err == nil {
		t.Fatal("an unknown commit was archived")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("a failed copy left its directory")
	}
}

func TestArchiveIgnoresAttributes(t *testing.T) {
	r := gittest.New(t)
	marker := filepath.Join(t.TempDir(), "smudged")
	filter := filepath.Join(t.TempDir(), "smudge")
	if err := os.WriteFile(filter, []byte("#!/bin/sh\ntouch '"+marker+"'\necho SMUDGED\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := r.Commit("attributes", map[string]*string{
		".gitattributes": gittest.S("hidden.txt export-ignore\nsubst.txt export-subst\nfiltered.txt filter=x\n"),
		"hidden.txt":     gittest.S("hidden\n"),
		"subst.txt":      gittest.S("commit $Format:%H$\n"),
		"filtered.txt":   gittest.S("raw\n"),
	})
	r.Git("config", "filter.x.smudge", filter)

	gittest.Run(t, r.Dir, nil, "archive", "--format=tar", "-o", filepath.Join(t.TempDir(), "a.tar"), id)
	if _, err := os.Stat(marker); err != nil {
		t.Logf("git archive did not run the smudge filter here, so the filter check proves less: %v", err)
	}
	_ = os.Remove(marker)

	dest := filepath.Join(t.TempDir(), "copy")
	skipped, err := open(t, r.Dir).Archive(context.Background(), id, dest)
	if err != nil {
		t.Fatal(err)
	}
	got := listFiles(t, dest)
	want := map[string]string{
		".gitattributes": "hidden.txt export-ignore\nsubst.txt export-subst\nfiltered.txt filter=x\n",
		"hidden.txt":     "hidden\n",
		"subst.txt":      "commit $Format:%H$\n",
		"filtered.txt":   "raw\n",
	}
	if !reflect.DeepEqual(got, want) || len(skipped) != 0 {
		t.Fatalf("got %q, skipped %v", got, skipped)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the smudge filter of the repository ran")
	}
}

func TestArchiveSubmoduleAndCase(t *testing.T) {
	r := gittest.New(t)
	blob := func(content string) string {
		id, err := gitStdin(t, r.Dir, []byte(content), "hash-object", "-w", "--stdin")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	upper, lower, other := blob("upper\n"), blob("lower\n"), blob("other\n")
	r.Git("config", "core.ignorecase", "false")
	for _, entry := range []string{
		"160000," + upper + ",vendor/lib",
		"100644," + upper + ",Dup.txt",
		"100644," + lower + ",dup.txt",
		"100644," + upper + ",Dir",
		"100644," + other + ",dir/inner.txt",
		"100755," + other + ",run.sh",
	} {
		r.Git("update-index", "--add", "--cacheinfo", entry)
	}
	id := commitIndex(t, r)

	dest := filepath.Join(t.TempDir(), "copy")
	skipped, err := open(t, r.Dir).Archive(context.Background(), id, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.IsSortedFunc(skipped, func(a, b Skipped) int { return strings.Compare(a.Path, b.Path) }) {
		t.Fatalf("skipped is not sorted: %v", skipped)
	}
	got := listFiles(t, dest)
	reasons := map[string]string{}
	for _, s := range skipped {
		reasons[s.Path] = s.Reason
	}
	if reasons["vendor/lib"] != "a submodule" {
		t.Fatalf("skipped = %v", skipped)
	}
	if got["run.sh"] != "other\n" {
		t.Fatalf("an executable file was not copied: %q", got)
	}
	pairs := []struct{ a, b, aContent, bContent string }{
		{"Dup.txt", "dup.txt", "upper\n", "lower\n"},
		{"Dir", "dir/inner.txt", "upper\n", "other\n"},
	}
	const caseReason = "its name differs from another file only in case"
	for _, p := range pairs {
		aCopied, bCopied := got[p.a] == p.aContent, got[p.b] == p.bContent
		switch {
		case aCopied && bCopied:
		case aCopied && reasons[p.b] == caseReason:
		case bCopied && reasons[p.a] == caseReason:
		default:
			t.Errorf("%s and %s: copied %q, skipped %v", p.a, p.b, keys(got), skipped)
		}
	}
}

func TestArchiveSkipsAPathThatIsNotLocal(t *testing.T) {
	r := gittest.New(t)
	id, err := gitStdin(t, r.Dir, []byte("content\n"), "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(id)
	if err != nil {
		t.Fatal(err)
	}
	var tree bytes.Buffer
	for _, name := range []string{"..", "ok.txt"} {
		tree.WriteString("100644 " + name + "\x00")
		tree.Write(raw)
	}
	treeID, err := gitStdin(t, r.Dir, tree.Bytes(), "hash-object", "--literally", "-t", "tree", "-w", "--stdin")
	if err != nil {
		t.Skipf("this git does not write a tree with a dot-dot entry: %v", err)
	}
	commit := r.Git("commit-tree", treeID, "-m", "crafted")

	dest := filepath.Join(t.TempDir(), "copy")
	skipped, err := open(t, r.Dir).Archive(context.Background(), commit, dest)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Skipped{{"..", "its path is not local"}}; !reflect.DeepEqual(skipped, want) {
		t.Fatalf("skipped = %v, want %v", skipped, want)
	}
	if got := listFiles(t, dest); !reflect.DeepEqual(got, map[string]string{"ok.txt": "content\n"}) {
		t.Fatalf("got %q", got)
	}
}

func TestPlanCopy(t *testing.T) {
	g := &Git{archiveFileCap: 10, archiveTotalCap: 15}
	cases := []struct {
		name    string
		entries []string
		blobs   int
		wantErr string
	}{
		{"fits", []string{"100644 blob a 5\ta", "100755 blob b 10\tb"}, 2, ""},
		{"total cap", []string{"100644 blob a 10\ta", "100644 blob b 10\tb"}, 0, "larger than 15 bytes"},
		{"no tab", []string{"100644 blob a 5 a"}, 0, "unexpected entry"},
		{"bad size", []string{"100644 blob a x\ta"}, 0, "bad size"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blobs, _, err := g.planCopy(c.entries)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("got %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil || len(blobs) != c.blobs {
				t.Fatalf("got %v %v", blobs, err)
			}
		})
	}
}

func TestArchiveLeavesOutEveryDotGitEntry(t *testing.T) {
	r := gittest.New(t)
	blob := func(content string) string {
		id, err := gitStdin(t, r.Dir, []byte(content), "hash-object", "-w", "--stdin")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	tree := func(entries string) string {
		id, err := gitStdin(t, r.Dir, []byte(entries), "mktree")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	config := blob("[core]\n\tfsmonitor = \"touch pwned; false\"\n")
	dotgit := tree("100644 blob " + config + "\tconfig\n100644 blob " + blob("ref: refs/heads/main\n") + "\tHEAD\n")
	readme := blob("hi\n")
	sub := tree("040000 tree " + dotgit + "\t.git\n100644 blob " + readme + "\tkeep.txt\n")
	names := []string{".git", ".GIT", ".g\u200cit", ".git.", "GIT~1"}
	var root strings.Builder
	for _, n := range names {
		root.WriteString("040000 tree " + dotgit + "\t" + n + "\n")
	}
	root.WriteString("040000 tree " + sub + "\tsub\n")
	root.WriteString("100644 blob " + readme + "\tREADME\n")
	commit := r.Git("commit-tree", tree(root.String()), "-m", "crafted")

	dest := filepath.Join(t.TempDir(), "copy")
	skipped, err := open(t, r.Dir).Archive(context.Background(), commit, dest)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := listFiles(t, dest), map[string]string{"README": "hi\n", "sub/keep.txt": "hi\n"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("copied %q, want %q", got, want)
	}
	reasons := map[string]string{}
	for _, s := range skipped {
		reasons[s.Path] = s.Reason
	}
	for _, n := range append(names, "sub/.git") {
		for _, f := range []string{"config", "HEAD"} {
			if p := n + "/" + f; reasons[p] != "inside a .git directory" {
				t.Errorf("%q: reason %q, skipped %v", p, reasons[p], skipped)
			}
		}
	}
	if _, err := os.Lstat(filepath.Join(dest, ".git")); err == nil {
		t.Fatal("the copy holds .git")
	}
}

func TestIsDotGit(t *testing.T) {
	for name, want := range map[string]bool{
		".git": true, ".GiT": true, ".git.": true, ".git . ": true, "git~1": true, "Git~1.": true,
		".g\u200cit": true, "\u206a.git\ufeff": true, ".\u202eGIT": true,
		"git": false, ".gitmodules": false, ".gitignore": false, "..git": false, ".git~1": false, "x.git": false,
	} {
		if got := isDotGit(name); got != want {
			t.Errorf("isDotGit(%q) = %v, want %v", name, got, want)
		}
	}
}
