package knowledge_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/knowledge"
)

func put(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func api(path string) config.Knowledge {
	return config.Knowledge{ID: "api", Kind: config.KnowledgeOpenAPI, Path: path}
}

var docs = config.Knowledge{ID: "docs", Kind: config.KnowledgeFiles, Path: "docs"}

func TestConventionalFindsEachName(t *testing.T) {
	names := []string{
		"docs/openapi.yaml", "docs/openapi.yml", "docs/openapi.json",
		"docs/swagger.yaml", "docs/swagger.yml", "docs/swagger.json",
		"api/openapi.yaml", "api/openapi.yml", "api/openapi.json",
		"api/swagger.yaml", "api/swagger.yml", "api/swagger.json",
		"openapi.yaml", "openapi.yml", "openapi.json",
		"swagger.yaml", "swagger.yml", "swagger.json",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			put(t, dir, name)
			want := []config.Knowledge{api(name)}
			if filepath.Dir(name) == "docs" {
				want = append(want, docs)
			}
			if got := knowledge.Conventional(dir); !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestConventionalNothing(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "README.md")
	if got := knowledge.Conventional(dir); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestConventionalOrderOfPreference(t *testing.T) {
	dir := t.TempDir()
	order := []string{
		"swagger.json", "openapi.yaml", "api/swagger.yaml", "api/openapi.json",
		"docs/swagger.json", "docs/openapi.yml", "docs/openapi.yaml",
	}
	// Add the files from the least to the most preferred, and the winner follows.
	for _, name := range order {
		put(t, dir, name)
		got := knowledge.Conventional(dir)
		if len(got) == 0 || got[0] != api(name) {
			t.Fatalf("after %s: got %+v", name, got)
		}
	}
}

func TestConventionalDocsOnly(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "docs/guide.md")
	if got, want := knowledge.Conventional(dir), []config.Knowledge{docs}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestConventionalNeverFollowsALink(t *testing.T) {
	outside := t.TempDir()
	put(t, outside, "openapi.yaml")
	put(t, outside, "real/openapi.yaml")

	t.Run("docs is a link", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(filepath.Join(outside, "real"), filepath.Join(dir, "docs")); err != nil {
			t.Skip(err)
		}
		if got := knowledge.Conventional(dir); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})
	t.Run("the document is a link", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "openapi.yaml"), filepath.Join(dir, "docs", "openapi.yaml")); err != nil {
			t.Skip(err)
		}
		if got, want := knowledge.Conventional(dir), []config.Knowledge{docs}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})
	t.Run("a link does not hide a later regular file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(filepath.Join(outside, "openapi.yaml"), filepath.Join(dir, "openapi.yaml")); err != nil {
			t.Skip(err)
		}
		put(t, dir, "swagger.json")
		if got, want := knowledge.Conventional(dir), []config.Knowledge{api("swagger.json")}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})
}

func TestConventionalFolderNamedLikeADocument(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "openapi.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := knowledge.Conventional(dir); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestConventionalDocsIsAFile(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "docs")
	if got := knowledge.Conventional(dir); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestWithFolderAndConventionalBelow(t *testing.T) {
	root := t.TempDir()
	put(t, root, "services/orders/docs/openapi.yaml")
	want := []config.Knowledge{api("services/orders/docs/openapi.yaml"), {ID: "docs", Kind: config.KnowledgeFiles, Path: "services/orders/docs"}}
	if got := knowledge.ConventionalBelow(root, "services/orders"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := knowledge.ConventionalBelow(root, "services/missing"); got != nil {
		t.Errorf("a missing folder gave %+v", got)
	}
	if got := knowledge.WithFolder([]config.Knowledge{api("docs/openapi.yaml")}, "a`b"); got != nil {
		t.Errorf("a path with a back quote was kept: %+v", got)
	}
	if got := knowledge.WithFolder([]config.Knowledge{docs}, ""); !reflect.DeepEqual(got, []config.Knowledge{docs}) {
		t.Errorf("an empty folder changed the list: %+v", got)
	}

	outside := t.TempDir()
	put(t, outside, "docs/openapi.yaml")
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skip("symlinks are not available:", err)
	}
	if got := knowledge.ConventionalBelow(root, "linked"); got != nil {
		t.Errorf("a link in the folder was followed: %+v", got)
	}
}
