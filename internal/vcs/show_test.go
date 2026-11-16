package vcs

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/vcs/gittest"
)

func TestShowReadsFileAtCommit(t *testing.T) {
	r := gittest.New(t)
	first := r.Commit("one", map[string]*string{"docs/openapi.yaml": gittest.S("openapi: old\n")})
	second := r.Commit("two", map[string]*string{"docs/openapi.yaml": gittest.S("openapi: new\n")})
	g := open(t, r.Dir)

	for _, tc := range []struct{ name, commit, want string }{
		{"the old content after a later commit changed it", first, "openapi: old\n"},
		{"the new content at the later commit", second, "openapi: new\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := g.Show(context.Background(), tc.commit, "docs/openapi.yaml", 1<<10)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("Show = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShowMissingPath(t *testing.T) {
	r := gittest.New(t)
	id := r.Commit("one", map[string]*string{"a.txt": gittest.S("a\n")})
	g := open(t, r.Dir)
	_, err := g.Show(context.Background(), id, "docs/missing.yaml", 1<<10)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Show of a missing path = %v, want fs.ErrNotExist", err)
	}
}

func TestShowRefusesPath(t *testing.T) {
	r := gittest.New(t)
	id := r.Commit("one", map[string]*string{"a.txt": gittest.S("a\n")})
	g := open(t, r.Dir)
	for _, path := range []string{"../x", "a/../../x", "a:b", "-x", "/etc/passwd", "", "a\nb"} {
		t.Run(path, func(t *testing.T) {
			_, err := g.Show(context.Background(), id, path, 1<<10)
			if err == nil {
				t.Fatalf("Show accepted path %q", path)
			}
			if errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Show %q reached git: %v", path, err)
			}
		})
	}
}

func TestShowRefusesCommit(t *testing.T) {
	r := gittest.New(t)
	id := r.Commit("one", map[string]*string{"a.txt": gittest.S("a\n")})
	g := open(t, r.Dir)
	upper := strings.ToUpper(id)
	for _, commit := range []string{"HEAD", "main", id[:7], "--upload-pack=x", "", upper, id + "0", id[:39] + "g"} {
		t.Run(commit, func(t *testing.T) {
			if _, err := g.Show(context.Background(), commit, "a.txt", 1<<10); err == nil {
				t.Fatalf("Show accepted commit %q", commit)
			}
		})
	}
}

func TestShowLimit(t *testing.T) {
	r := gittest.New(t)
	id := r.Commit("one", map[string]*string{"a.txt": gittest.S(strings.Repeat("x", 100))})
	g := open(t, r.Dir)
	if got, err := g.Show(context.Background(), id, "a.txt", 100); err != nil || len(got) != 100 {
		t.Fatalf("Show at exactly the limit = %d bytes, %v", len(got), err)
	}
	_, err := g.Show(context.Background(), id, "a.txt", 99)
	if err == nil || !strings.Contains(err.Error(), "99") {
		t.Fatalf("Show above the limit = %v, want an error that names 99", err)
	}
	if _, err := g.Show(context.Background(), id, "a.txt", 0); err == nil {
		t.Fatal("Show with a zero limit succeeded")
	}
}

func TestShowStoppedContext(t *testing.T) {
	r := gittest.New(t)
	id := r.Commit("one", map[string]*string{"a.txt": gittest.S("a\n")})
	g := open(t, r.Dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Show(ctx, id, "a.txt", 1<<10); !errors.Is(err, context.Canceled) {
		t.Fatalf("Show with a cancelled context = %v", err)
	}
}
