package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jancuk/semar/internal/vcs/gittest"
)

func TestWorkTreeRoot(t *testing.T) {
	repo := gittest.New(t)
	sub := filepath.Join(repo.Dir, "services", "orders")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := filepath.EvalSymlinks(workTreeRoot(sub))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("workTreeRoot(%s) = %s, want the root of the work tree %s", sub, got, want)
	}

	outside := t.TempDir()
	if got := workTreeRoot(outside); got != outside {
		t.Fatalf("workTreeRoot outside a work tree = %s, want the directory itself %s", got, outside)
	}
}
