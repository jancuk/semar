package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func cacheAt(dir string) func() (string, error) { return func() (string, error) { return dir, nil } }

func TestSourceCopyRootDefaultsToTheCacheDirectory(t *testing.T) {
	cache := t.TempDir()
	root, err := sourceCopyRoot(options{CacheDir: cacheAt(cache)})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cache, "semar", "work"); root != want {
		t.Fatalf("root = %q, want %q", root, want)
	}
	fi, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v, want 0700", fi.Mode().Perm())
	}
}

func TestSourceCopyRootDefaultReadsTheEnvironment(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	root, err := sourceCopyRoot(options{}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(root, cache) {
		t.Fatalf("root = %q, want it under %q", root, cache)
	}
}

func TestSourceCopyRootTempRootWins(t *testing.T) {
	temp, cache := t.TempDir(), t.TempDir()
	root, err := sourceCopyRoot(options{TempRoot: temp, CacheDir: cacheAt(cache)})
	if err != nil {
		t.Fatal(err)
	}
	if root != temp {
		t.Fatalf("root = %q, want %q", root, temp)
	}
	if _, err := os.Stat(filepath.Join(cache, "semar")); err == nil {
		t.Fatal("the cache directory was used")
	}
}

func TestSourceCopyRootFallsBackWithoutACacheDirectory(t *testing.T) {
	root, err := sourceCopyRoot(options{CacheDir: func() (string, error) { return "", errors.New("no home") }})
	if err != nil {
		t.Fatal(err)
	}
	if root != "" {
		t.Fatalf("root = %q, want the system default", root)
	}
}

func TestSourceCopyRootRemovesOldCopies(t *testing.T) {
	cache, outside := t.TempDir(), t.TempDir()
	// A link keeps its creation time, which Go cannot set, so the clock runs ahead of it.
	now := time.Now().Add(72 * time.Hour)
	work := filepath.Join(cache, "semar", "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	mk := func(name string, age time.Duration) string {
		p := filepath.Join(work, name)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := mk("semar-plan-old", 25*time.Hour)
	fresh := mk("semar-plan-new", time.Hour)
	other := mk("keep-me", 48*time.Hour)

	target := filepath.Join(outside, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(work, "semar-plan-x")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if _, err := sourceCopyRoot(options{CacheDir: cacheAt(cache), Clock: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	gone := func(p string) bool { _, err := os.Lstat(p); return os.IsNotExist(err) }
	if !gone(old) {
		t.Error("the old copy was kept")
	}
	if gone(fresh) || gone(other) {
		t.Error("a new copy or an entry with another name was removed")
	}
	if !gone(link) {
		t.Error("the link was kept")
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("the target of the link was touched: %v", err)
	}
}
