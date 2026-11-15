package cli_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
)

// plannedFiles lists the regular files that a plan writes, wherever they are below .semar.
func (p *planRepo) plannedFiles(t *testing.T) []string {
	t.Helper()
	var found []string
	for _, dir := range []string{p.path(".semar", "scenarios"), p.path(".semar", "plans"), p.path(".semar", "store", "scripts")} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if path != dir && d.Type().IsRegular() {
				found = append(found, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return found
}

func TestPlanRefusesALinkOnTheWayToItsFiles(t *testing.T) {
	for _, component := range [][]string{
		{".semar", "scenarios"},
		{".semar", "scenarios", "SHOP-482"},
		{".semar", "plans"},
		{".semar", "store", "scripts"},
		{".semar", "store", "scripts", "SHOP-483-01"},
	} {
		t.Run(filepath.Join(component...), func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.configure(t, goodDraft)
			link := p.path(component...)
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			if err := os.Symlink(target, link); err != nil {
				t.Skip("symlinks are not available:", err)
			}
			res := p.must(t, cli.ExitError, "plan", "--diff", "main..release")
			contains(t, "stderr", res.stderr, link, "symbolic link")
			if files := p.plannedFiles(t); len(files) != 0 {
				t.Errorf("the plan wrote %v", files)
			}
			if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
				t.Errorf("the link's target holds %v (%v)", entries, err)
			}
			p.assertNoTempCopy(t)
		})
	}

	t.Run("a link at .semar", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		target := t.TempDir()
		if err := os.Rename(p.path(".semar", "semar.yaml"), filepath.Join(target, "semar.yaml")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(p.path(".semar")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p.path(".semar")); err != nil {
			t.Skip("symlinks are not available:", err)
		}
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release")
		contains(t, "stderr", res.stderr, p.path(".semar"), "symbolic link")
		if entries, err := os.ReadDir(target); err != nil || len(entries) != 1 {
			t.Errorf("the link's target holds %v (%v), want only semar.yaml", entries, err)
		}
	})
}

func TestScenariosUnderALinkOrASpecialFileAreNotSkipped(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	other := t.TempDir()
	link := r.path(".semar", "scenarios", "LINKED")
	if err := os.Symlink(other, link); err != nil {
		t.Skip("symlinks are not available:", err)
	}
	for _, args := range [][]string{{"run", "--all"}, {"confirm", "--all", "--yes"}} {
		res := r.must(t, cli.ExitError, args...)
		contains(t, "stderr", res.stderr, link, "symbolic link")
	}
}

func TestAPlanThatCannotBeWrittenLeavesNothing(t *testing.T) {
	t.Run("the plan directory cannot be created", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		p.write(t, ".semar/plans", "not a directory\n")
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release")
		contains(t, "stderr", res.stderr, p.path(".semar", "plans"), "not a directory")
		if files := p.plannedFiles(t); len(files) != 0 {
			t.Errorf("the plan wrote %v", files)
		}
		for _, dir := range []string{p.path(".semar", "scenarios"), p.path(".semar", "store", "scripts")} {
			if exists(t, dir) {
				t.Errorf("%s was created", dir)
			}
		}
	})

	t.Run("a script cannot be stored after the first one", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs a directory that the user cannot write")
		}
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		locked := p.path(".semar", "store", "scripts", "SHOP-483-01")
		if err := os.MkdirAll(locked, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release")
		contains(t, "stderr", res.stderr, "the plan was not written, and what it had written was removed")
		if files := p.plannedFiles(t); len(files) != 0 {
			t.Errorf("the plan left %v", files)
		}
		for _, dir := range []string{p.path(".semar", "scenarios"), p.path(".semar", "plans"), p.path(".semar", "store", "scripts", "SHOP-482-01")} {
			if exists(t, dir) {
				t.Errorf("%s stays", dir)
			}
		}
	})
}
