package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
		before := tree(t, p.path(".semar"))
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--no-trial")
		contains(t, "stderr", res.stderr, "the plan was not written, and what it had written was removed")
		if after := tree(t, p.path(".semar")); !slices.Equal(after, before) {
			t.Errorf(".semar changed:\nbefore %q\nafter  %q", before, after)
		}
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

func TestInitRefusesALinkOrAFileAtSemar(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"init", []string{"init", "--base-url", "https://orders.example"}},
		{"plan --base-url", []string{"plan", "--diff", "main..release", "--base-url", "https://orders.example"}},
	} {
		t.Run(c.name+" through a link", func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.opts.Providers = noProvider(t)
			target := t.TempDir()
			if err := os.Symlink(target, p.path(".semar")); err != nil {
				t.Skip("symlinks are not available:", err)
			}
			res := p.must(t, cli.ExitError, c.args...)
			contains(t, "stderr", res.stderr, p.path(".semar"), "symbolic link")
			if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
				t.Errorf("the link's target holds %v (%v)", entries, err)
			}
		})
		t.Run(c.name+" with a file", func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.opts.Providers = noProvider(t)
			p.write(t, ".semar", "a file\n")
			res := p.must(t, cli.ExitError, c.args...)
			contains(t, "stderr", res.stderr, p.path(".semar"), "not a directory")
		})
	}
}

// tree lists every directory and file below root, with a digest of each file's content.
func tree(t *testing.T, root string) []string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case d.IsDir():
			entries = append(entries, "dir  "+filepath.ToSlash(rel))
		case d.Type().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			entries = append(entries, "file "+filepath.ToSlash(rel)+" "+hex.EncodeToString(sum[:8]))
		default:
			entries = append(entries, "other "+filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestARefusedPlanLeavesSemarAsItWas(t *testing.T) {
	t.Run("a target scenario file exists", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft)
		if err := os.MkdirAll(p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-01.feature"), 0o700); err != nil {
			t.Fatal(err)
		}
		before := tree(t, p.path(".semar"))
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--no-trial")
		contains(t, "stderr", res.stderr, "SHOP-482-01.feature exists already; nothing was written")
		if after := tree(t, p.path(".semar")); !slices.Equal(after, before) {
			t.Errorf(".semar changed:\nbefore %q\nafter  %q", before, after)
		}
	})

	t.Run("a script is stored already", func(t *testing.T) {
		p := newPlanRepo(t, sampleapp.Options{})
		p.configure(t, goodDraft, goodDraft)
		p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
		for _, d := range []string{"scenarios", "plans", "runs"} {
			if err := os.RemoveAll(p.path(".semar", d)); err != nil {
				t.Fatal(err)
			}
		}
		before := tree(t, p.path(".semar"))
		res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--no-trial")
		contains(t, "stderr", res.stderr, "is stored already; nothing was written")
		if after := tree(t, p.path(".semar")); !slices.Equal(after, before) {
			t.Errorf(".semar changed:\nbefore %q\nafter  %q", before, after)
		}
	})
}

const existingObligations = `apiVersion: semar/v1
kind: Obligations
ticket: SHOP-482
obligations:
  - id: SHOP-482/scenario-SHOP-482-01
    title: Edited by a person
    source: diff
    covers: [SHOP-482-01]
`

func TestPlanWritesObligations(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.must(t, cli.ExitOK, "plan", "--diff", "main..release")

	data, err := os.ReadFile(p.path(".semar", "obligations", "SHOP-482.plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"ticket: SHOP-482", "id: SHOP-482/scenario-SHOP-482-01", "id: SHOP-482/not-drafted-1", "not drafted: needs a second test account"} {
		contains(t, "SHOP-482.plan.yaml", got, want)
	}
	other, err := os.ReadFile(p.path(".semar", "obligations", "SHOP-483.plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "SHOP-483.plan.yaml", string(other), "id: SHOP-483/scenario-SHOP-483-01")
	info, err := os.Stat(p.path(".semar", "obligations", "SHOP-482.plan.yaml"))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("mode %v, %v", info, err)
	}
	for _, name := range []string{"SHOP-482.plan.yaml", "SHOP-483.plan.yaml"} {
		if strings.Contains(string(mustRead(t, p.path(".semar", "obligations", name))), "state") {
			t.Errorf("%s names a state", name)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPlanAddsToAnExistingObligationsFile(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.write(t, ".semar/obligations/SHOP-482.plan.yaml", existingObligations)
	p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	got := string(mustRead(t, p.path(".semar", "obligations", "SHOP-482.plan.yaml")))
	contains(t, "file", got, "title: Edited by a person", "id: SHOP-482/not-drafted-1")
	if strings.Count(got, "scenario-SHOP-482-01") != 1 {
		t.Errorf("the scenario entry is not kept once:\n%s", got)
	}
}

func TestWritePlanRestoresReplacedFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory that the user cannot write")
	}
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.write(t, ".semar/obligations/SHOP-482.plan.yaml", existingObligations)
	locked := p.path(".semar", "store", "scripts", "SHOP-483-01")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	before := tree(t, p.path(".semar"))
	res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--no-trial")
	contains(t, "stderr", res.stderr, "the plan was not written, and what it had written was removed")
	if got := string(mustRead(t, p.path(".semar", "obligations", "SHOP-482.plan.yaml"))); got != existingObligations {
		t.Errorf("the replaced file was not put back:\n%s", got)
	}
	if exists(t, p.path(".semar", "obligations", "SHOP-483.plan.yaml")) {
		t.Error("a new obligations file stays")
	}
	if after := tree(t, p.path(".semar")); !slices.Equal(after, before) {
		t.Errorf(".semar changed:\nbefore %q\nafter  %q", before, after)
	}
}

func TestWritePlanStopsOnBadExistingFile(t *testing.T) {
	for _, tt := range []struct{ name, content, want string }{
		{"unknown key", existingObligations + "    state: verified\n", "a state is computed by code"},
		{"not YAML", "obligations: [\n", ".semar/obligations/SHOP-482.plan.yaml"},
		{"a rule broken", strings.Replace(existingObligations, "source: diff", "source: review", 1), "source review is not a source"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.configure(t, goodDraft)
			p.write(t, ".semar/obligations/SHOP-482.plan.yaml", tt.content)
			before := tree(t, p.path(".semar"))
			res := p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--no-trial")
			contains(t, "stderr", res.stderr, ".semar/obligations/SHOP-482.plan.yaml", tt.want, "fix or remove it, and plan again; nothing was written")
			if after := tree(t, p.path(".semar")); !slices.Equal(after, before) {
				t.Errorf(".semar changed:\nbefore %q\nafter  %q", before, after)
			}
			p.assertNothingPlanned(t)
			p.assertNoTempCopy(t)
		})
	}
}

func TestPlanRefusesALinkAtObligations(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	target := t.TempDir()
	link := p.path(".semar", "obligations")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks are not available:", err)
	}
	res := p.must(t, cli.ExitError, "plan", "--diff", "main..release")
	contains(t, "stderr", res.stderr, link, "symbolic link")
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Errorf("the link's target holds %v (%v)", entries, err)
	}
	p.assertNothingPlanned(t)
}
