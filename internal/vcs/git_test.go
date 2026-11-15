package vcs

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/vcs/gittest"
)

type fixture struct {
	repo                      *gittest.Repo
	base, f1, f2, side, merge string
	f3, head                  string
}

// build makes main with one commit and a release branch with a side branch merged in.
func build(t *testing.T) fixture {
	t.Helper()
	r := gittest.New(t)
	var fx fixture
	fx.repo = r
	fx.base = r.Commit("base", map[string]*string{
		"a.txt":    gittest.S("one\ntwo\nthree\n"),
		"old.txt":  gittest.S("this file is renamed later\nit has several lines\nso that git sees a rename\n"),
		"gone.txt": gittest.S("removed later\n"),
	})
	r.Git("checkout", "--quiet", "-b", "release")
	fx.f1 = r.Commit("SHOP-1 add new file", map[string]*string{
		"new.txt": gittest.S("new\n"),
		"a.txt":   gittest.S("one\ntwo\nthree\nfour\n"),
	})
	r.Git("checkout", "--quiet", "-b", "side")
	fx.side = r.Commit("side work", map[string]*string{"side.txt": gittest.S("side\n")})
	r.Git("checkout", "--quiet", "release")
	fx.f2 = r.Commit("rename and delete\n\nbody line one\nbody line two\n", map[string]*string{
		"old.txt":   nil,
		"moved.txt": gittest.S("this file is renamed later\nit has several lines\nso that git sees a rename\n"),
		"gone.txt":  nil,
	})
	fx.merge = r.MergeNoFF("side", "Merge side")
	fx.f3 = r.Commit("add binary", map[string]*string{"bin.dat": gittest.S("\x00\x01\x02binary\x00")})
	fx.head = fx.f3
	return fx
}

func open(t *testing.T, dir string) *Git {
	t.Helper()
	g, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestOpen(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("Open on a directory outside a work tree succeeded")
	}
	if _, err := Open(""); err == nil {
		t.Fatal("Open with an empty directory succeeded")
	}
	fx := build(t)
	if _, err := Open(fx.repo.Dir); err != nil {
		t.Fatal(err)
	}
}

func TestLog(t *testing.T) {
	fx := build(t)
	g := open(t, fx.repo.Dir)
	commits, err := g.Log(context.Background(), fx.base, fx.head)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id      string
		subject string
		parents int
		files   []string
	}
	var got []row
	for _, c := range commits {
		got = append(got, row{c.ID, c.Subject, len(c.Parents), c.Files})
	}
	if len(got) != 5 {
		t.Fatalf("got %d commits, want 5: %+v", len(got), got)
	}
	byID := map[string]row{}
	for _, r := range got {
		byID[r.id] = r
	}
	cases := []struct {
		name    string
		id      string
		parents int
		files   []string
	}{
		{"first", fx.f1, 1, []string{"a.txt", "new.txt"}},
		{"side", fx.side, 1, []string{"side.txt"}},
		{"rename and delete", fx.f2, 1, []string{"gone.txt", "moved.txt", "old.txt"}},
		{"merge has no files", fx.merge, 2, nil},
		{"binary", fx.f3, 1, []string{"bin.dat"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ok := byID[c.id]
			if !ok {
				t.Fatalf("commit %s missing", c.id)
			}
			if r.parents != c.parents || !reflect.DeepEqual(r.files, c.files) {
				t.Fatalf("got parents %d files %v, want %d %v", r.parents, r.files, c.parents, c.files)
			}
		})
	}
	if got[len(got)-1].id != fx.f3 || got[len(got)-2].id != fx.merge {
		t.Fatalf("not oldest first: %+v", got)
	}
	if got[0].id != fx.f1 {
		t.Fatalf("first commit is %s, want %s", got[0].id, fx.f1)
	}
	var f2 Commit
	for _, c := range commits {
		if c.ID == fx.f2 {
			f2 = c
		}
	}
	if f2.Subject != "rename and delete" || f2.Body != "body line one\nbody line two" || f2.Author != "Ada Author" {
		t.Fatalf("commit fields: %+v", f2)
	}
	if want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC); !f2.At.After(want) || f2.At.Location() != time.UTC {
		t.Fatalf("commit time %v", f2.At)
	}
}

func TestLogEmptyRange(t *testing.T) {
	fx := build(t)
	g := open(t, fx.repo.Dir)
	commits, err := g.Log(context.Background(), fx.head, fx.base)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 0 {
		t.Fatalf("got %d commits, want none", len(commits))
	}
}

func TestMergeBase(t *testing.T) {
	fx := build(t)
	g := open(t, fx.repo.Dir)
	got, err := g.MergeBase(context.Background(), "main", "release")
	if err != nil {
		t.Fatal(err)
	}
	if got != fx.base {
		t.Fatalf("merge base %s, want %s", got, fx.base)
	}
	fx.repo.Git("checkout", "--quiet", "--orphan", "unrelated")
	other := fx.repo.Commit("unrelated root", nil)
	if _, err := g.MergeBase(context.Background(), other, fx.head); err == nil || !strings.Contains(err.Error(), "no common ancestor") {
		t.Fatalf("got %v, want no common ancestor", err)
	}
}

func TestDiffStat(t *testing.T) {
	fx := build(t)
	g := open(t, fx.repo.Dir)
	got, err := g.DiffStat(context.Background(), fx.base, fx.head)
	if err != nil {
		t.Fatal(err)
	}
	want := []FileStat{
		{Path: "a.txt", Status: "modified", Added: 1},
		{Path: "bin.dat", Status: "added", Binary: true},
		{Path: "gone.txt", Status: "deleted", Deleted: 1},
		{Path: "moved.txt", OldPath: "old.txt", Status: "renamed"},
		{Path: "new.txt", Status: "added", Added: 1},
		{Path: "side.txt", Status: "added", Added: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestDiffStatOddNames(t *testing.T) {
	r := gittest.New(t)
	base := r.Commit("base", map[string]*string{"keep": gittest.S("x\n")})
	head := r.Commit("odd", map[string]*string{
		"with space.txt": gittest.S("a\n"),
		"tab\there.txt":  gittest.S("b\n"),
		"new\nline.txt":  gittest.S("c\n"),
		"café/ü":         gittest.S("d\n"),
	})
	g := open(t, r.Dir)
	got, err := g.DiffStat(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, s := range got {
		paths = append(paths, s.Path)
	}
	want := []string{"café/ü", "new\nline.txt", "tab\there.txt", "with space.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("got %q, want %q", paths, want)
	}
	commits, err := g.Log(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(commits[0].Files, want) {
		t.Fatalf("log files %q, want %q", commits[0].Files, want)
	}
}

func TestDiff(t *testing.T) {
	fx := build(t)
	g := open(t, fx.repo.Dir)
	out, err := g.Diff(context.Background(), fx.base, fx.head)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rename from old.txt", "rename to moved.txt", "deleted file mode", "Binary files /dev/null and b/bin.dat differ"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("diff lacks %q", want)
		}
	}
}

func TestOutputCap(t *testing.T) {
	fx := build(t)
	g := open(t, fx.repo.Dir)
	_, err := g.run(context.Background(), 20, "log", "--format=%H")
	if err == nil || !strings.Contains(err.Error(), "larger than 20 bytes") {
		t.Fatalf("got %v, want a cap error", err)
	}
}

func TestResolve(t *testing.T) {
	fx := build(t)
	bare := fx.repo.Bare()
	clone := filepath.Join(t.TempDir(), "clone")
	gittest.Run(t, filepath.Dir(clone), nil, "clone", "--quiet", bare, clone)
	fx.repo.Git("tag", "v1", fx.f1)
	fx.repo.Git("branch", "only-local", fx.f2)
	g := open(t, fx.repo.Dir)
	gc := open(t, clone)
	ctx := context.Background()

	cases := []struct {
		name    string
		g       *Git
		remote  string
		ref     string
		want    string
		wantErr string
	}{
		{"remote tracking branch", gc, "origin", "release", fx.head, ""},
		{"remote ref wins over a local branch", gc, "origin", "main", fx.base, ""},
		{"local branch", g, "origin", "only-local", fx.f2, ""},
		{"tag", g, "origin", "v1", fx.f1, ""},
		{"full commit", g, "origin", fx.f2, fx.f2, ""},
		{"no remote given", g, "", "release", fx.head, ""},
		{"unknown names both tries", g, "origin", "nope", "", "tried refs/remotes/origin/nope and nope"},
		{"option as name", g, "origin", "--upload-pack=x", "", "starts with a dash"},
		{"option as remote", g, "--x", "main", "", "starts with a dash"},
		{"control character", g, "origin", "a\nb", "", "control character"},
		{"empty", g, "origin", "", "", "empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.g.Resolve(ctx, c.remote, c.ref)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("got %v, want error with %q", err, c.wantErr)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v, want %q", got, err, c.want)
			}
		})
	}
}

func TestOptionLookalikesAreRefused(t *testing.T) {
	fx := build(t)
	g := open(t, fx.repo.Dir)
	ctx := context.Background()
	bad := "--upload-pack=x"
	calls := map[string]func() error{
		"RemoteURL":  func() error { _, err := g.RemoteURL(ctx, bad); return err },
		"Fetch":      func() error { return g.Fetch(ctx, bad) },
		"Fetch name": func() error { return g.Fetch(ctx, "origin", bad) },
		"MergeBase":  func() error { _, err := g.MergeBase(ctx, bad, "main"); return err },
		"Log":        func() error { _, err := g.Log(ctx, "main", bad); return err },
		"DiffStat":   func() error { _, err := g.DiffStat(ctx, bad, "main"); return err },
		"Diff":       func() error { _, err := g.Diff(ctx, "main", bad); return err },
		"Archive":    func() error { return g.Archive(ctx, bad, filepath.Join(t.TempDir(), "x")) },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil || !strings.Contains(err.Error(), "starts with a dash") {
				t.Fatalf("got %v, want a refusal", err)
			}
		})
	}
}

func TestFetch(t *testing.T) {
	fx := build(t)
	bare := fx.repo.Bare()
	clone := filepath.Join(t.TempDir(), "clone")
	gittest.Run(t, filepath.Dir(clone), nil, "clone", "--quiet", bare, clone)
	next := fx.repo.Commit("later work", map[string]*string{"later.txt": gittest.S("x\n")})
	fx.repo.Git("push", "--quiet", bare, "release")
	fx.repo.Git("checkout", "--quiet", "-b", "other")
	otherTip := fx.repo.Commit("on other", map[string]*string{"o.txt": gittest.S("x\n")})
	fx.repo.Git("push", "--quiet", bare, "other")

	head := gittest.Run(t, clone, nil, "rev-parse", "HEAD")
	branch := gittest.Run(t, clone, nil, "branch", "--show-current")
	status := gittest.Run(t, clone, nil, "status", "--porcelain")
	mainTip := gittest.Run(t, clone, nil, "rev-parse", "main")

	g := open(t, clone)
	ctx := context.Background()
	if err := g.Fetch(ctx, "origin", "release"); err != nil {
		t.Fatal(err)
	}
	got, err := g.Resolve(ctx, "origin", "release")
	if err != nil || got != next {
		t.Fatalf("release resolves to %q, %v, want %q", got, err, next)
	}
	if _, err := g.Resolve(ctx, "origin", "other"); err == nil {
		t.Fatal("a branch that was not asked for was fetched")
	}
	if err := g.Fetch(ctx, "origin"); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.Resolve(ctx, "origin", "other"); got != otherTip {
		t.Fatalf("other resolves to %q, want %q", got, otherTip)
	}
	if h := gittest.Run(t, clone, nil, "rev-parse", "HEAD"); h != head {
		t.Fatalf("HEAD moved from %s to %s", head, h)
	}
	if b := gittest.Run(t, clone, nil, "branch", "--show-current"); b != branch {
		t.Fatalf("branch changed from %s to %s", branch, b)
	}
	if s := gittest.Run(t, clone, nil, "status", "--porcelain"); s != status {
		t.Fatalf("work tree changed: %s", s)
	}
	if tip := gittest.Run(t, clone, nil, "rev-parse", "main"); tip != mainTip {
		t.Fatal("the local branch moved")
	}
	if err := g.Fetch(ctx, "nowhere", "release"); err == nil {
		t.Fatal("Fetch from an unknown remote succeeded")
	}
	for _, b := range []string{"a:b", "x y", "a..b", "-x", "a*"} {
		if err := g.Fetch(ctx, "origin", b); err == nil {
			t.Errorf("Fetch of branch %q succeeded", b)
		}
	}
}

func TestHooksDoNotRun(t *testing.T) {
	fx := build(t)
	bare := fx.repo.Bare()
	clone := filepath.Join(t.TempDir(), "clone")
	gittest.Run(t, filepath.Dir(clone), nil, "clone", "--quiet", bare, clone)
	marker := filepath.Join(t.TempDir(), "ran")
	hook := "#!/bin/sh\ntouch '" + marker + "'\n"
	for _, name := range []string{"reference-transaction", "post-checkout", "post-merge", "pre-auto-gc"} {
		if err := os.WriteFile(filepath.Join(clone, ".git", "hooks", name), []byte(hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fx.repo.Commit("later", map[string]*string{"later.txt": gittest.S("x\n")})
	fx.repo.Git("push", "--quiet", bare, "release")

	// Without the hooks path override the hook fires, so the check below proves something.
	gittest.Run(t, clone, nil, "fetch", "--quiet", "origin", "release")
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("this git does not run the reference-transaction hook: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	fx.repo.Commit("later again", map[string]*string{"later2.txt": gittest.S("x\n")})
	fx.repo.Git("push", "--quiet", bare, "release")

	g := open(t, clone)
	ctx := context.Background()
	if err := g.Fetch(ctx, "origin", "release"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Log(ctx, "main", "origin/release"); err != nil {
		t.Fatal(err)
	}
	if err := g.Archive(ctx, "origin/release", filepath.Join(t.TempDir(), "copy")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a hook of the repository ran")
	}
}
