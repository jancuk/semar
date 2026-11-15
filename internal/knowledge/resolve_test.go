package knowledge_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/knowledge"
)

const apiDoc = `openapi: 3.0.3
paths:
  /orders/{id}:
    get:
      responses:
        "200": {description: ok}
    put:
      responses:
        "200": {description: ok}
`

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "docs/openapi.yaml", apiDoc)
	write(t, dir, "docs/refunds.md", "# Refunds\n")
	write(t, dir, "docs/guide/start.md", "# Start\n")
	write(t, dir, "docs/broken.yaml", "title: not an API document\n")
	write(t, dir, "README.md", "# Orders\n")
	if err := os.Mkdir(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	list := []config.Knowledge{
		{ID: "api", Kind: config.KnowledgeOpenAPI, Path: "docs/openapi.yaml", Service: "orders"},
		{ID: "docs", Kind: config.KnowledgeFiles, Path: "docs"},
		{ID: "readme", Kind: config.KnowledgeFiles, Path: "README.md"},
		{ID: "empty", Kind: config.KnowledgeFiles, Path: "empty"},
		{ID: "old", Kind: config.KnowledgeFiles, Path: "docs/old"},
		{ID: "apidir", Kind: config.KnowledgeOpenAPI, Path: "docs/guide"},
		{ID: "broken", Kind: config.KnowledgeOpenAPI, Path: "docs/broken.yaml"},
	}
	got, err := knowledge.Resolve(dir, list)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(list) {
		t.Fatalf("%d sources, want %d", len(got), len(list))
	}
	byID := map[string]knowledge.Source{}
	for i, s := range got {
		if s.ID != list[i].ID || s.Kind != list[i].Kind || s.Path != list[i].Path || s.Service != list[i].Service {
			t.Errorf("source %d = %+v, want the fields of %+v", i, s, list[i])
		}
		byID[s.ID] = s
	}

	api := byID["api"]
	if !api.Usable() || api.Folder || api.Files != 1 || api.Note != "" || api.API == nil {
		t.Fatalf("api = %+v", api)
	}
	if api.API.Version != "3.0.3" || len(api.API.Operations) != 2 {
		t.Errorf("api document = %+v", *api.API)
	}

	for id, want := range map[string]struct {
		folder bool
		files  int
	}{"docs": {true, 4}, "readme": {false, 1}, "empty": {true, 0}} {
		s := byID[id]
		if !s.Usable() || s.Folder != want.folder || s.Files != want.files || s.API != nil || s.Note != "" {
			t.Errorf("%s = %+v, want a usable source with folder %v and %d files", id, s, want.folder, want.files)
		}
	}

	for id, want := range map[string]string{
		"old":    "docs/old is not in the copy of the source",
		"apidir": "docs/guide is a folder, and an API document is one file",
	} {
		if s := byID[id]; s.Usable() || s.Problem != want {
			t.Errorf("%s problem = %q, want %q", id, s.Problem, want)
		}
	}

	broken := byID["broken"]
	if !broken.Usable() || broken.API != nil || broken.Files != 1 {
		t.Errorf("broken = %+v, want a usable document that was not read as an API document", broken)
	}
	if !strings.Contains(broken.Note, `"openapi" or "swagger"`) || strings.HasPrefix(broken.Note, "docs/broken.yaml") {
		t.Errorf("broken note = %q, want the reason without the path", broken.Note)
	}
}

func TestResolveWithNoSource(t *testing.T) {
	got, err := knowledge.Resolve(filepath.Join(t.TempDir(), "missing"), nil)
	if err != nil || got != nil {
		t.Errorf("Resolve(nil) = %v, %v; want nothing", got, err)
	}
	if _, err := knowledge.Resolve(filepath.Join(t.TempDir(), "missing"), []config.Knowledge{{ID: "docs", Kind: config.KnowledgeFiles, Path: "docs"}}); err == nil {
		t.Error("Resolve accepted a copy that does not exist")
	}
}

func TestResolveStopsCountingAfterTheLimit(t *testing.T) {
	dir := t.TempDir()
	for i := range knowledge.MaxFiles + 1 {
		write(t, dir, fmt.Sprintf("docs/d%d/f%d.md", i%7, i), "x")
	}
	write(t, dir, "small/a.md", "x")
	for i := range knowledge.MaxFiles - 1 {
		write(t, dir, fmt.Sprintf("small/b%d.md", i), "x")
	}
	got, err := knowledge.Resolve(dir, []config.Knowledge{
		{ID: "docs", Kind: config.KnowledgeFiles, Path: "docs"},
		{ID: "small", Kind: config.KnowledgeFiles, Path: "small"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Files != knowledge.MaxFiles+1 {
		t.Errorf("docs counts %d files, want %d to say there are more than %d", got[0].Files, knowledge.MaxFiles+1, knowledge.MaxFiles)
	}
	if got[1].Files != knowledge.MaxFiles {
		t.Errorf("small counts %d files, want exactly %d", got[1].Files, knowledge.MaxFiles)
	}
}

func TestResolveNeverFollowsALink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "secret.md", "x")
	write(t, outside, "openapi.yaml", apiDoc)
	write(t, dir, "docs/a.md", "x")
	if err := os.Symlink(outside, filepath.Join(dir, "docs", "linked")); err != nil {
		t.Skip("symlinks are not available:", err)
	}
	if err := os.Symlink(filepath.Join(outside, "openapi.yaml"), filepath.Join(dir, "openapi.yaml")); err != nil {
		t.Fatal(err)
	}
	got, err := knowledge.Resolve(dir, []config.Knowledge{
		{ID: "docs", Kind: config.KnowledgeFiles, Path: "docs"},
		{ID: "api", Kind: config.KnowledgeOpenAPI, Path: "openapi.yaml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Files != 1 {
		t.Errorf("docs counts %d files, want 1: a link is not followed", got[0].Files)
	}
	if got[1].Usable() || got[1].API != nil {
		t.Errorf("api = %+v, want a problem for a link", got[1])
	}
}

func TestHolds(t *testing.T) {
	folder := knowledge.Source{Path: "docs"}
	file := knowledge.Source{Path: "docs/openapi.yaml"}
	for _, c := range []struct {
		s    knowledge.Source
		file string
		want bool
	}{
		{folder, "docs", true},
		{folder, "docs/refunds.md", true},
		{folder, "docs/guide/start.md", true},
		{folder, "./docs/refunds.md", true},
		{folder, "docs-old/refunds.md", false},
		{folder, "docsrefunds.md", false},
		{folder, "internal/docs/a.md", false},
		{folder, "docs/../internal/a.go", false},
		{file, "docs/openapi.yaml", true},
		{file, "docs/openapi.yaml.bak", false},
		{file, "docs", false},
	} {
		if got := c.s.Holds(c.file); got != c.want {
			t.Errorf("%q holds %q = %v, want %v", c.s.Path, c.file, got, c.want)
		}
	}
}

func TestResolveRefusesALinkInAFolderName(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "real/openapi.yaml", apiDoc)
	write(t, dir, "real/guide.md", "x")
	// The target is inside the copy, so a root alone would follow the link.
	if err := os.Symlink("real", filepath.Join(dir, "linked")); err != nil {
		t.Skip("symlinks are not available:", err)
	}
	got, err := knowledge.Resolve(dir, []config.Knowledge{
		{ID: "api", Kind: config.KnowledgeOpenAPI, Path: "linked/openapi.yaml"},
		{ID: "guide", Kind: config.KnowledgeFiles, Path: "linked/guide.md"},
		{ID: "docs", Kind: config.KnowledgeFiles, Path: "linked"},
		{ID: "real", Kind: config.KnowledgeFiles, Path: "real/guide.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s := got[2]; s.Usable() || s.Folder {
		t.Errorf("docs = %+v, want a link that is not followed", s)
	}
	for _, s := range got[:2] {
		if s.Usable() || s.API != nil || !strings.Contains(s.Problem, "is not in the copy of the source") {
			t.Errorf("%s = %+v, want the problem of a path that is not in the copy", s.ID, s)
		}
	}
	if !got[3].Usable() {
		t.Errorf("real = %+v, want a usable source", got[3])
	}
}

func TestResolveComparesNamesExactly(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "internal/refunds.md", "x")
	got, err := knowledge.Resolve(dir, []config.Knowledge{
		{ID: "dir", Kind: config.KnowledgeFiles, Path: "INTERNAL/refunds.md"},
		{ID: "file", Kind: config.KnowledgeFiles, Path: "internal/Refunds.md"},
		{ID: "exact", Kind: config.KnowledgeFiles, Path: "internal/refunds.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got[:2] {
		if s.Usable() || !strings.Contains(s.Problem, "is not in the copy of the source") {
			t.Errorf("%s = %+v, want a path that is not in the copy: a file system may ignore case, Holds does not", s.ID, s)
		}
	}
	if !got[2].Usable() {
		t.Errorf("exact = %+v", got[2])
	}
}
