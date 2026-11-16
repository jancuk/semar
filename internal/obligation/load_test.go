package obligation_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/obligation"
)

// repoWith writes each file below .semar/obligations of a new repository.
func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	for name, data := range files {
		p := filepath.Join(repo, ".semar", "obligations", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func one(id string) string {
	return fmt.Sprintf("%sobligations:\n  - {id: %s, title: T, source: diff}\n", head, id)
}

func TestLoadReadsEveryFile(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"SHOP-482.plan.yaml": string(readTestdata(t, "example.yaml")),
		"refunds.yaml":       string(readTestdata(t, "hand.yaml")),
		"notes.txt":          "not a file of obligations",
	})
	list, problems, err := obligation.Load(repo)
	if err != nil || len(problems) > 0 {
		t.Fatalf("Load: %v, %v", err, problems)
	}
	if len(list) != 5 {
		t.Fatalf("got %d obligations", len(list))
	}
	first := list[0]
	if first.File != ".semar/obligations/SHOP-482.plan.yaml" || first.Line != 5 || first.Ticket != "SHOP-482" {
		t.Errorf("first is %+v", first)
	}
	if last := list[4]; last.ID != "refunds/refund-email" || last.File != ".semar/obligations/refunds.yaml" || last.Ticket != "" {
		t.Errorf("last is %+v", last)
	}
}

func TestLoadWithoutAFolder(t *testing.T) {
	list, problems, err := obligation.Load(t.TempDir())
	if err != nil || len(problems) > 0 || len(list) > 0 {
		t.Fatalf("got %v, %v, %v", list, problems, err)
	}
}

func TestLoadReportsEveryBadFile(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"a.yaml": head + "obligations:\n  - {id: g/a, title: T, source: review}\n",
		"b.yaml": head + "state: x\nobligations: []\n",
	})
	_, problems, err := obligation.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, p := range problems {
		lines = append(lines, p.String())
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{".semar/obligations/a.yaml:4: obligations[0].source: source review is not a source", ".semar/obligations/b.yaml:3: unknown key state"} {
		if !strings.Contains(all, want) {
			t.Errorf("problems lack %q:\n%s", want, all)
		}
	}
}

func TestDuplicateIDAcrossFiles(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"a.yaml": one("SHOP-482/AC-1"),
		"b.yaml": one("SHOP-482/AC-1"),
	})
	_, problems, err := obligation.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 {
		t.Fatalf("problems: %v", problems)
	}
	p := problems[0].String()
	want := ".semar/obligations/b.yaml:4: obligations[0].id: id SHOP-482/AC-1 is also in .semar/obligations/a.yaml"
	if p != want {
		t.Errorf("got %q, want %q", p, want)
	}
}

func TestLoadRefusesLinks(t *testing.T) {
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "x.yaml"), []byte(one("g/x")), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, repo string)
	}{
		{"semar is a link", func(t *testing.T, repo string) { link(t, target, filepath.Join(repo, ".semar")) }},
		{"obligations is a link", func(t *testing.T, repo string) {
			mkdir(t, filepath.Join(repo, ".semar"))
			link(t, target, filepath.Join(repo, ".semar", "obligations"))
		}},
		{"a file is a link", func(t *testing.T, repo string) {
			mkdir(t, filepath.Join(repo, ".semar", "obligations"))
			link(t, filepath.Join(target, "x.yaml"), filepath.Join(repo, ".semar", "obligations", "x.yaml"))
		}},
		{"a folder is a link", func(t *testing.T, repo string) {
			mkdir(t, filepath.Join(repo, ".semar", "obligations"))
			link(t, target, filepath.Join(repo, ".semar", "obligations", "sub"))
		}},
		{"obligations is a file", func(t *testing.T, repo string) {
			mkdir(t, filepath.Join(repo, ".semar"))
			if err := os.WriteFile(filepath.Join(repo, ".semar", "obligations"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			tt.setup(t, repo)
			list, _, err := obligation.Load(repo)
			if err == nil {
				t.Fatalf("Load read %v through a link or a wrong kind of entry", list)
			}
			if !strings.Contains(err.Error(), "symbolic link") && !strings.Contains(err.Error(), "not a directory") {
				t.Errorf("error %q does not say what is wrong", err)
			}
		})
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Skip("symbolic links are not available:", err)
	}
}

func TestLoadLimits(t *testing.T) {
	t.Run("a file over 256 KiB is a problem", func(t *testing.T) {
		repo := repoWith(t, map[string]string{"big.yaml": head + "# " + strings.Repeat("a", 256*1024) + "\nobligations: []\n"})
		_, problems, err := obligation.Load(repo)
		if err != nil || len(problems) != 1 || !strings.Contains(problems[0].Message, "larger than 256 KiB") {
			t.Fatalf("got %v, %v", problems, err)
		}
	})
	t.Run("101 files", func(t *testing.T) {
		files := map[string]string{}
		for i := range 101 {
			files[fmt.Sprintf("f%03d.yaml", i)] = one(fmt.Sprintf("g/n%d", i))
		}
		_, _, err := obligation.Load(repoWith(t, files))
		if err == nil || !strings.Contains(err.Error(), "more than 100 files") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("5001 obligations", func(t *testing.T) {
		files := map[string]string{}
		for i := range 11 {
			var b strings.Builder
			b.WriteString(head + "obligations:\n")
			for j := range 500 {
				fmt.Fprintf(&b, "  - {id: g/n%d-%d, title: T, source: diff}\n", i, j)
			}
			files[fmt.Sprintf("f%02d.yaml", i)] = b.String()
		}
		_, _, err := obligation.Load(repoWith(t, files))
		if err == nil || !strings.Contains(err.Error(), "more than 5000 obligations") {
			t.Fatalf("got %v", err)
		}
	})
}
