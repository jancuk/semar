package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

var allServices = []string{"orders", "payments"}

func asID(t *testing.T, text, id string) string {
	t.Helper()
	return strings.Replace(text, "id: order.paid", "id: "+id, 1)
}

func TestLoadTheExample(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"order.paid.yaml":       exampleText(t),
		"customer.verified.yml": asID(t, exampleText(t), "customer.verified"),
		"notes.md":              "not a fixture",
	})
	c, err := Load(repo, allServices)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Problems) != 0 {
		t.Fatalf("problems = %v", c.Problems)
	}
	var ids []domain.FixtureID
	for _, f := range c.Fixtures {
		ids = append(ids, f.ID)
	}
	if want := []domain.FixtureID{"customer.verified", "order.paid"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	got, ok := c.Get("order.paid")
	if !ok || got.ID != "order.paid" {
		t.Fatalf("Get = %v, %v", got.ID, ok)
	}
	if _, ok := c.Get("order.missing"); ok {
		t.Error("Get found a fixture that is not there")
	}
	if _, ok := c.Get(""); ok {
		t.Error("Get found the empty ID")
	}
}

func TestLoadWithoutAFixturesDirectoryIsEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		mk   func(repo string) error
	}{
		{"no repository files", func(string) error { return nil }},
		{"no fixtures directory", func(repo string) error { return os.Mkdir(filepath.Join(repo, ".semar"), 0o700) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if err := tc.mk(repo); err != nil {
				t.Fatal(err)
			}
			c, err := Load(repo, nil)
			if err != nil || len(c.Fixtures) != 0 || len(c.Problems) != 0 {
				t.Fatalf("Load = %+v, %v", c, err)
			}
		})
	}
}

func TestLoadReportsInvalidFilesAndKeepsValidOnes(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"order.paid.yaml": exampleText(t),
		// the file name differs from the id
		"order-paid.yaml": exampleText(t),
		// an unknown key
		"bad-key.yaml": asID(t, edited(t, "version: 1.0.0", "version: 1.0.0\nretries: 3"), "bad-key"),
		// a rule of validate
		"bad-version.yaml": asID(t, edited(t, "version: 1.0.0", "version: 1"), "bad-version"),
		// a service that no environment lists
		"bad-service.yaml": asID(t, strings.ReplaceAll(exampleText(t), "service: payments", "service: ledger"), "bad-service"),
		"empty.yaml":       "",
	})
	if err := os.Mkdir(filepath.Join(repo, ".semar", "fixtures", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	c, err := Load(repo, allServices)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Fixtures) != 1 || c.Fixtures[0].ID != "order.paid" {
		t.Fatalf("fixtures = %v", c.Fixtures)
	}
	for _, want := range []struct{ file, path, msg string }{
		{".semar/fixtures/order-paid.yaml", "id", "id order.paid does not match the file name order-paid.yaml"},
		{".semar/fixtures/bad-key.yaml", "line 6", "retries"},
		{".semar/fixtures/bad-version.yaml", "version", "version 1 must be MAJOR.MINOR.PATCH"},
		{".semar/fixtures/bad-service.yaml", "create[1].request.service", "ledger is not a service of any environment"},
		{".semar/fixtures/empty.yaml", "", "the file is empty"},
		{".semar/fixtures/nested", "", "fixtures must be files directly in .semar/fixtures"},
	} {
		found := false
		for _, p := range c.Problems {
			if p.File == want.file && p.Path == want.path && strings.Contains(p.Message, want.msg) {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %+v in %v", want, c.Problems)
		}
	}
	for i := 1; i < len(c.Problems); i++ {
		if c.Problems[i-1].File > c.Problems[i].File {
			t.Errorf("problems are not sorted by file: %v", c.Problems)
			break
		}
	}
}

func TestParseProblemsKeepTheLine(t *testing.T) {
	repo := repoWith(t, map[string]string{"bad-key.yaml": asID(t, edited(t, "version: 1.0.0", "version: 1.0.0\nretries: 3"), "bad-key")})
	c, err := Load(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Problems) != 1 {
		t.Fatalf("problems = %v", c.Problems)
	}
	if got := c.Problems[0].String(); !strings.HasPrefix(got, ".semar/fixtures/bad-key.yaml: line 6: ") {
		t.Fatalf("String = %q", got)
	}
}

func TestLoadRefusesDuplicateIDs(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"order.paid.yaml": exampleText(t),
		"order.paid.yml":  exampleText(t),
		"other.yaml":      asID(t, exampleText(t), "other"),
	})
	c, err := Load(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Fixtures) != 1 || c.Fixtures[0].ID != "other" {
		t.Fatalf("a fixture with two files was kept: %v", c.Fixtures)
	}
	for _, file := range []string{".semar/fixtures/order.paid.yaml", ".semar/fixtures/order.paid.yml"} {
		found := false
		for _, p := range c.Problems {
			if p.File == file && strings.Contains(p.Message, "fixture order.paid has two files: order.paid.yaml and order.paid.yml") {
				found = true
			}
		}
		if !found {
			t.Errorf("no duplicate problem for %s in %v", file, c.Problems)
		}
	}
}

func TestLoadLimits(t *testing.T) {
	t.Run("201 files", func(t *testing.T) {
		files := map[string]string{}
		for i := range 201 {
			files[fmt.Sprintf("f%03d.yaml", i)] = ""
		}
		_, err := Load(repoWith(t, files), nil)
		if err == nil || !strings.Contains(err.Error(), "more than 200 files") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("200 files", func(t *testing.T) {
		files := map[string]string{}
		for i := range 200 {
			files[fmt.Sprintf("f%03d.yaml", i)] = ""
		}
		c, err := Load(repoWith(t, files), nil)
		if err != nil || len(c.Problems) != 200 {
			t.Fatalf("Load = %d problems, %v", len(c.Problems), err)
		}
	})
	t.Run("257 KiB", func(t *testing.T) {
		big := exampleText(t) + "#" + strings.Repeat("x", 257<<10) + "\n"
		c, err := Load(repoWith(t, map[string]string{"order.paid.yaml": big}), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Fixtures) != 0 || len(c.Problems) != 1 || !strings.Contains(c.Problems[0].Message, "the file is larger than 256 KiB") {
			t.Fatalf("Load = %+v", c)
		}
	})
	t.Run("256 KiB", func(t *testing.T) {
		text := exampleText(t)
		text += "#" + strings.Repeat("x", MaxFileSize-len(text)-2) + "\n"
		if len(text) != MaxFileSize {
			t.Fatalf("len = %d", len(text))
		}
		c, err := Load(repoWith(t, map[string]string{"order.paid.yaml": text}), nil)
		if err != nil || len(c.Fixtures) != 1 {
			t.Fatalf("Load = %+v, %v", c, err)
		}
	})
}

func TestLoadRefusesLinks(t *testing.T) {
	t.Run("a linked fixtures directory", func(t *testing.T) {
		repo := t.TempDir()
		elsewhere := repoWith(t, map[string]string{"order.paid.yaml": exampleText(t)})
		if err := os.Mkdir(filepath.Join(repo, ".semar"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(elsewhere, ".semar", "fixtures"), filepath.Join(repo, ".semar", "fixtures")); err != nil {
			t.Skip("symlinks are not available:", err)
		}
		_, err := Load(repo, nil)
		if err == nil || !strings.Contains(err.Error(), "is a symbolic link") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a linked .semar", func(t *testing.T) {
		repo := t.TempDir()
		elsewhere := repoWith(t, map[string]string{"order.paid.yaml": exampleText(t)})
		if err := os.Symlink(filepath.Join(elsewhere, ".semar"), filepath.Join(repo, ".semar")); err != nil {
			t.Skip("symlinks are not available:", err)
		}
		_, err := Load(repo, nil)
		if err == nil || !strings.Contains(err.Error(), "is a symbolic link") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a linked file", func(t *testing.T) {
		repo := repoWith(t, map[string]string{"order.paid.yaml": exampleText(t)})
		outside := filepath.Join(t.TempDir(), "secret.yaml")
		if err := os.WriteFile(outside, []byte(asID(t, exampleText(t), "linked")), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(repo, ".semar", "fixtures", "linked.yaml")); err != nil {
			t.Skip("symlinks are not available:", err)
		}
		_, err := Load(repo, nil)
		if err == nil || !strings.Contains(err.Error(), "linked.yaml is a symbolic link") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a link to a directory with a non-yaml name", func(t *testing.T) {
		repo := repoWith(t, nil)
		if err := os.Symlink(t.TempDir(), filepath.Join(repo, ".semar", "fixtures", "more")); err != nil {
			t.Skip("symlinks are not available:", err)
		}
		if _, err := Load(repo, nil); err == nil {
			t.Fatal("a link was skipped")
		}
	})
	t.Run(".semar is a file", func(t *testing.T) {
		repo := t.TempDir()
		if err := os.WriteFile(filepath.Join(repo, ".semar"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(repo, nil)
		if err == nil || !strings.Contains(err.Error(), "is not a directory") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestEntries(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"order.paid.yaml": strings.Replace(exampleText(t), "customer: { default: \"{{data.customers.default}}\" }", "customer: { default: \"{{data.customers.default}}\" }\n  tier: {}\n  amount: { default: \"5\" }", 1),
		"a.first.yaml":    asID(t, exampleText(t), "a.first"),
	})
	c, err := Load(repo, nil)
	if err != nil || len(c.Problems) != 0 {
		t.Fatalf("Load = %+v, %v", c, err)
	}
	got := c.Entries(func(f domain.Fixture) bool { return f.ID == "order.paid" })
	want := []Entry{
		{ID: "a.first", Description: "A paid order of 1000 that belongs to the given customer", Params: []ParamEntry{{"customer", false}}, Outputs: []string{"id"}, Approved: false},
		{ID: "order.paid", Description: "A paid order of 1000 that belongs to the given customer", Params: []ParamEntry{{"amount", false}, {"customer", false}, {"tier", true}}, Outputs: []string{"id"}, Approved: true},
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
	if got := c.Entries(nil); got[0].Approved || got[1].Approved {
		t.Error("a nil approval function approved a fixture")
	}
}

func TestEntriesDoNotShareSlicesWithTheCatalogue(t *testing.T) {
	c := Catalogue{Fixtures: []domain.Fixture{example(t)}}
	e := c.Entries(nil)
	e[0].Outputs[0] = "changed"
	if c.Fixtures[0].Outputs[0] != "id" {
		t.Fatal("an entry shares its outputs with the fixture")
	}
}

func TestLoadOne(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"order.paid.yaml": exampleText(t),
		"other.yaml":      "not yaml: [",
	})
	f, err := LoadOne(repo, "order.paid", allServices)
	if err != nil || f.ID != "order.paid" {
		t.Fatalf("LoadOne = %v, %v", f.ID, err)
	}
	for _, tc := range []struct{ id, want string }{
		{"../order.paid", "invalid fixture ID"},
		{"order/paid", "invalid fixture ID"},
		{"Order.paid", "invalid fixture ID"},
		{"", "invalid fixture ID"},
		{strings.Repeat("a", 65), "invalid fixture ID"},
		{"missing", "fixture missing was not found"},
		{"other", "other.yaml"},
	} {
		if _, err := LoadOne(repo, domain.FixtureID(tc.id), nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("LoadOne(%q) = %v, want %q", tc.id, err, tc.want)
		}
	}
	if _, err := LoadOne(t.TempDir(), "order.paid", nil); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Errorf("no fixtures directory: %v", err)
	}
}

func TestLoadOneRefusals(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"twice.yaml":  asID(t, exampleText(t), "twice"),
		"twice.yml":   asID(t, exampleText(t), "twice"),
		"wrong.yaml":  exampleText(t),
		"huge.yaml":   "x: 1\n#" + strings.Repeat("x", 257<<10) + "\n",
		"badkind.yml": asID(t, strings.Replace(exampleText(t), "kind: Fixture", "kind: Scenario", 1), "badkind"),
	})
	for _, tc := range []struct{ id, want string }{
		{"twice", "fixture twice has two files"},
		{"wrong", "id order.paid does not match the file name wrong.yaml"},
		{"huge", "larger than 256 KiB"},
		{"badkind", "kind must be Fixture"},
	} {
		if _, err := LoadOne(repo, domain.FixtureID(tc.id), nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("LoadOne(%q) = %v, want %q", tc.id, err, tc.want)
		}
	}
}
