package journey

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func asID(t *testing.T, id string) string {
	t.Helper()
	return strings.Replace(exampleText(t), "id: checkout", "id: "+id, 1)
}

func fixtureText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "order.paid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFixture(t *testing.T, repo string) {
	t.Helper()
	dir := filepath.Join(repo, ".semar", "fixtures")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "order.paid.yaml"), []byte(fixtureText(t)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func problemTexts(ps []Problem) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

func TestLoadTheExample(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"checkout.yaml": exampleText(t),
		"billing.yml":   asID(t, "billing"),
		"notes.md":      "not a journey",
	})
	js, ps, err := Load(repo)
	if err != nil || len(ps) != 0 {
		t.Fatalf("Load = %v, %v", ps, err)
	}
	var ids []domain.JourneyID
	for _, j := range js {
		ids = append(ids, j.ID)
	}
	if want := []domain.JourneyID{"billing", "checkout"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestLoadWithoutAJourneysDirectoryIsEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		mk   func(repo string) error
	}{
		{"no repository files", func(string) error { return nil }},
		{"no journeys directory", func(repo string) error { return os.Mkdir(filepath.Join(repo, ".semar"), 0o700) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if err := tc.mk(repo); err != nil {
				t.Fatal(err)
			}
			js, ps, err := Load(repo)
			if err != nil || len(js) != 0 || len(ps) != 0 {
				t.Fatalf("Load = %v, %v, %v", js, ps, err)
			}
		})
	}
}

func TestLoadReportsInvalidFilesAndKeepsValidOnes(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"checkout.yaml": exampleText(t),
		// the file name differs from the id
		"check-out.yaml": exampleText(t),
		// an unknown key
		"bad-key.yaml": strings.Replace(asID(t, "bad-key"), "criticality: critical", "criticality: critical\nschedule: nightly", 1),
		// two files for one id
		"twice.yaml": asID(t, "twice"),
		"twice.yml":  asID(t, "twice"),
		// a web leg
		"web.yaml": strings.Replace(asID(t, "web"), "surface: api", "surface: web", 1),
		// not a journey
		"empty.yaml": "",
	})
	if err := os.Mkdir(filepath.Join(repo, ".semar", "journeys", "dir.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	js, ps, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 1 || js[0].ID != "checkout" {
		t.Fatalf("journeys = %v", js)
	}
	got := strings.Join(problemTexts(ps), "\n")
	for _, want := range []string{
		".semar/journeys/bad-key.yaml:7: unknown key schedule",
		".semar/journeys/check-out.yaml:3: id: id checkout does not match the file name check-out.yaml",
		".semar/journeys/dir.yaml: journeys must be files directly in .semar/journeys",
		".semar/journeys/empty.yaml: the file is empty",
		".semar/journeys/twice.yaml: journey twice has two files: twice.yaml and twice.yml",
		".semar/journeys/twice.yml: journey twice has two files: twice.yaml and twice.yml",
		".semar/journeys/web.yaml:8: legs[0].app: app is required for a web leg",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("problems do not hold %q:\n%s", want, got)
		}
	}
	for i := 1; i < len(ps); i++ {
		if ps[i-1].File > ps[i].File {
			t.Errorf("problems are not sorted by file: %v", problemTexts(ps))
			break
		}
	}
}

func TestLoadRefusesDuplicateIDs(t *testing.T) {
	// Two files that name one journey cannot both stand: neither is loaded.
	repo := repoWith(t, map[string]string{"checkout.yaml": exampleText(t), "checkout.yml": exampleText(t)})
	js, ps, err := Load(repo)
	if err != nil || len(js) != 0 || len(ps) != 2 {
		t.Fatalf("Load = %v, %v, %v", js, problemTexts(ps), err)
	}
	// A file whose id is another file's name does not stand either: the id must be the file name.
	repo = repoWith(t, map[string]string{"checkout.yaml": exampleText(t), "other.yaml": exampleText(t)})
	js, ps, err = Load(repo)
	if err != nil || len(js) != 1 || len(ps) != 1 || !strings.Contains(ps[0].Message, "id checkout does not match the file name other.yaml") {
		t.Fatalf("Load = %v, %v, %v", js, problemTexts(ps), err)
	}
}

func TestLoadRefusesALargeFile(t *testing.T) {
	big := exampleText(t) + "#" + strings.Repeat("x", MaxFileSize) + "\n"
	repo := repoWith(t, map[string]string{"checkout.yaml": exampleText(t), "big.yaml": big})
	js, ps, err := Load(repo)
	if err != nil || len(js) != 1 || len(ps) != 1 || ps[0].File != ".semar/journeys/big.yaml" || ps[0].Message != "the file is larger than 256 KiB" {
		t.Fatalf("Load = %v, %v, %v", js, problemTexts(ps), err)
	}
	// exactly 256 KiB is allowed
	exact := exampleText(t) + "#" + strings.Repeat("x", MaxFileSize-len(exampleText(t))-2) + "\n"
	if len(exact) != MaxFileSize {
		t.Fatalf("test file is %d bytes", len(exact))
	}
	js, ps, err = Load(repoWith(t, map[string]string{"checkout.yaml": exact}))
	if err != nil || len(js) != 1 || len(ps) != 0 {
		t.Fatalf("a file of 256 KiB: %v, %v, %v", js, problemTexts(ps), err)
	}
}

func TestLoadRefusesTooManyFiles(t *testing.T) {
	files := map[string]string{}
	for i := range maxFiles + 1 {
		files[fmt.Sprintf("f%03d.txt", i)] = ""
	}
	if _, _, err := Load(repoWith(t, files)); err == nil || !strings.Contains(err.Error(), "more than 200 files") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadRefusesLinks(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte(exampleText(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	link := func(t *testing.T, target, at string) {
		t.Helper()
		if err := os.Symlink(target, at); err != nil {
			t.Skip("no symbolic links:", err)
		}
	}

	t.Run("a file", func(t *testing.T) {
		repo := repoWith(t, map[string]string{"good.yaml": asID(t, "good")})
		link(t, outside, filepath.Join(repo, ".semar", "journeys", "checkout.yaml"))
		_, _, err := Load(repo)
		if err == nil || !strings.Contains(err.Error(), "checkout.yaml is a symbolic link") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a file that is not a journey", func(t *testing.T) {
		repo := repoWith(t, nil)
		link(t, outside, filepath.Join(repo, ".semar", "journeys", "notes.md"))
		if _, _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the journeys directory", func(t *testing.T) {
		repo := t.TempDir()
		elsewhere := t.TempDir()
		if err := os.WriteFile(filepath.Join(elsewhere, "checkout.yaml"), []byte(exampleText(t)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(repo, ".semar"), 0o700); err != nil {
			t.Fatal(err)
		}
		link(t, elsewhere, filepath.Join(repo, ".semar", "journeys"))
		_, _, err := Load(repo)
		if err == nil || !strings.Contains(err.Error(), "journeys is a symbolic link") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the .semar directory", func(t *testing.T) {
		repo := t.TempDir()
		elsewhere := t.TempDir()
		if err := os.MkdirAll(filepath.Join(elsewhere, "journeys"), 0o700); err != nil {
			t.Fatal(err)
		}
		link(t, elsewhere, filepath.Join(repo, ".semar"))
		_, _, err := Load(repo)
		if err == nil || !strings.Contains(err.Error(), ".semar is a symbolic link") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a directory that is a file", func(t *testing.T) {
		repo := t.TempDir()
		if err := os.MkdirAll(filepath.Join(repo, ".semar"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".semar", "journeys"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "is not a directory") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestLoadLooksUpTheFixturesOfTheRepository(t *testing.T) {
	withGiven := strings.Replace(exampleText(t), "legs:\n", "given:\n  - fixture: order.paid\n    as: order\nlegs:\n", 1)
	withGiven = strings.Replace(withGiven, `ref: "semar-{{run.id}}"`, `ref: "semar-{{run.id}}", customer: "{{order.id}}"`, 1)

	t.Run("the fixture is there", func(t *testing.T) {
		repo := repoWith(t, map[string]string{"checkout.yaml": withGiven})
		writeFixture(t, repo)
		js, ps, err := Load(repo)
		if err != nil || len(ps) != 0 || len(js) != 1 || len(js[0].Given) != 1 {
			t.Fatalf("Load = %v, %v, %v", js, problemTexts(ps), err)
		}
	})
	t.Run("the fixture does not exist", func(t *testing.T) {
		repo := repoWith(t, map[string]string{"checkout.yaml": withGiven})
		js, ps, err := Load(repo)
		if err != nil || len(js) != 0 || len(ps) == 0 {
			t.Fatalf("Load = %v, %v, %v", js, problemTexts(ps), err)
		}
		want := ".semar/journeys/checkout.yaml:8: given[0].fixture: fixture order.paid was not found among the valid fixtures of .semar/fixtures"
		if got := ps[0].String(); got != want {
			t.Fatalf("problem = %q, want %q", got, want)
		}
	})
	t.Run("the fixture file is not valid", func(t *testing.T) {
		repo := repoWith(t, map[string]string{"checkout.yaml": withGiven})
		writeFixture(t, repo)
		path := filepath.Join(repo, ".semar", "fixtures", "order.paid.yaml")
		if err := os.WriteFile(path, []byte(strings.Replace(fixtureText(t), "version: 1.0.0", "version: one", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		js, ps, err := Load(repo)
		if err != nil || len(js) != 0 || len(ps) != 1 || !strings.Contains(ps[0].Message, "was not found among the valid fixtures") {
			t.Fatalf("Load = %v, %v, %v", js, problemTexts(ps), err)
		}
	})
	t.Run("a link in the fixtures directory", func(t *testing.T) {
		repo := repoWith(t, map[string]string{"checkout.yaml": withGiven})
		writeFixture(t, repo)
		if err := os.Symlink(filepath.Join(repo, "x"), filepath.Join(repo, ".semar", "fixtures", "other.yaml")); err != nil {
			t.Skip("no symbolic links:", err)
		}
		if _, _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestLoadGivesEachProblemItsLine: a problem of validation has the line of the place it names.
func TestLoadGivesEachProblemItsLine(t *testing.T) {
	text := edited(t, `json: { orderId: "{{place.id}}" }`, `json: { orderId: "{{settled.id}}" }`)
	text = strings.Replace(text, "owner: team-commerce", "owner: team commerce", 1)
	text = strings.Replace(text, "timeout: 30s", "timeout: 20m", 1)
	_, ps, err := Load(repoWith(t, map[string]string{"checkout.yaml": text}))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"owner":                                 5,
		"legs[1].steps[0].request.json.orderId": 27,
		"legs[2].wait.timeout":                  42,
	}
	got := map[string]int{}
	for _, p := range ps {
		got[p.Path] = p.Line
	}
	for path, line := range want {
		if got[path] != line {
			t.Errorf("line of %s = %d, want %d (%v)", path, got[path], line, problemTexts(ps))
		}
	}
	// a place that is missing gets the line of the nearest place that exists
	text = strings.Replace(exampleText(t), "owner: team-commerce\n", "", 1)
	_, ps, err = Load(repoWith(t, map[string]string{"checkout.yaml": text}))
	if err != nil || len(ps) != 1 || ps[0].Path != "owner" || ps[0].Line != 1 {
		t.Fatalf("problems = %v, %v", problemTexts(ps), err)
	}
}

func TestLineOf(t *testing.T) {
	var doc = mustNode(t, "a: 1\nlist:\n  - x: 1\n    y:\n      - 7\n      - z: 2\nflow: { k: { m: 3 } }\n")
	for path, want := range map[string]int{
		"":                1,
		"a":               1,
		"list":            2,
		"list[0]":         3,
		"list[0].y":       4,
		"list[0].y[1]":    6,
		"list[0].y[1].z":  6,
		"list[5]":         2,
		"list[0].nothing": 3,
		"nothing":         1,
		"flow.k.m":        7,
		"list[0].y[x]":    3,
		"list[]":          1,
	} {
		if got := lineOf(doc, path); got != want {
			t.Errorf("lineOf(%q) = %d, want %d", path, got, want)
		}
	}
}
