package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
)

const knowledgeBase = `version: 1
environments:
  uat:
    kind: test
    hosts: [orders.uat.example.com]
    methods: [GET]
    effects: {mode: read_only}
    services:
      - name: orders
        baseUrl: https://orders.uat.example.com
`

func TestKnowledgeLoads(t *testing.T) {
	cfg, err := loadText(t, knowledgeBase+`knowledge:
  - id: api
    kind: openapi
    path: docs/openapi.yaml
    service: orders
  - id: docs
    kind: files
    path: docs/
`)
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Knowledge{
		{ID: "api", Kind: "openapi", Path: "docs/openapi.yaml", Service: "orders"},
		{ID: "docs", Kind: "files", Path: "docs"},
	}
	if !reflect.DeepEqual(cfg.Knowledge, want) {
		t.Errorf("knowledge = %+v, want %+v", cfg.Knowledge, want)
	}
}

func TestKnowledgeAbsentLoads(t *testing.T) {
	cfg, err := loadText(t, knowledgeBase)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Knowledge != nil {
		t.Errorf("knowledge = %+v, want none", cfg.Knowledge)
	}
}

func TestKnowledgeRefusals(t *testing.T) {
	var many strings.Builder
	for i := range 17 {
		many.WriteString("  - {id: s" + string(rune('a'+i)) + ", kind: files, path: d}\n")
	}
	cases := []struct{ name, body, want string }{
		{"seventeen sources", many.String(), "knowledge lists 17 sources; at most 16 are allowed"},
		{"unknown key", "  - {id: a, kind: files, path: d, url: x}\n", "semar.yaml:"},
		{"bad id", "  - {id: _a, kind: files, path: d}\n", `knowledge[0].id: invalid source id "_a"`},
		{"empty id", "  - {kind: files, path: d}\n", "knowledge[0].id: invalid source id"},
		{"id twice with other case", "  - {id: Docs, kind: files, path: d}\n  - {id: docs, kind: files, path: e}\n", `knowledge[1].id: "docs" is already used by knowledge[0]`},
		{"unknown kind", "  - {id: a, kind: wiki, path: d}\n", `knowledge[0].kind must be files or openapi, got "wiki"`},
		{"no kind", "  - {id: a, path: d}\n", "knowledge[0].kind must be files or openapi"},
		{"files with service", "  - {id: a, kind: files, path: d, service: orders}\n", "knowledge[0].service is allowed on an openapi source only"},
		{"unknown service", "  - {id: a, kind: openapi, path: d.yaml, service: billing}\n", `knowledge[0].service: "billing" is not the name of a service of any environment; known services: orders`},
		{"empty path", "  - {id: a, kind: files}\n", "knowledge[0].path: the path is empty"},
		{"absolute path", "  - {id: a, kind: files, path: /etc}\n", "knowledge[0].path: the path \"/etc\" is not a path inside the repository"},
		{"parent path", "  - {id: a, kind: files, path: ../x}\n", "is not a path inside the repository"},
		{"dot", "  - {id: a, kind: files, path: .}\n", "names the whole repository"},
		{"dot with slash", "  - {id: a, kind: files, path: ./}\n", "names the whole repository"},
		{"unclean", "  - {id: a, kind: files, path: docs//api}\n", `is not in its clean form; write "docs/api"`},
		{"two trailing slashes", "  - {id: a, kind: files, path: \"docs//\"}\n", "is not in its clean form"},
		{"git folder", "  - {id: a, kind: files, path: .git/config}\n", "is inside .git"},
		{"plan folder", "  - {id: a, kind: files, path: .semar-plan/x/}\n", "is inside .semar-plan"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadText(t, knowledgeBase+"knowledge:\n"+c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error with %q, got %v", c.want, err)
			}
		})
	}
}

func TestCheckKnowledgePath(t *testing.T) {
	ok := []string{"docs", "docs/", "docs/openapi.yaml", "a/b/c.md", ".github/docs", "my docs/a b.md"}
	for _, p := range ok {
		if err := config.CheckKnowledgePath(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	bad := map[string]string{
		"":                       "empty",
		strings.Repeat("a", 513): "513 bytes",
		`docs\api`:               "backslash",
		"docs/`x`":               "back quote",
		"docs/a\x00b":            "control character",
		"docs/a\nb":              "control character",
		"docs/a\xffb":            "UTF-8",
		"/":                      "names the whole repository",
		".":                      "names the whole repository",
		"..":                     "not a path inside",
		"docs/../x":              "clean form",
		"./docs":                 "clean form",
		".GIT/x":                 "inside .git",
		".semar-plan":            "inside .semar-plan",
	}
	for p, want := range bad {
		if err := config.CheckKnowledgePath(p); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want an error with %q, got %v", p, want, err)
		}
	}
	if err := config.CheckKnowledgePath(strings.Repeat("a", 512)); err != nil {
		t.Errorf("512 bytes: %v", err)
	}
}
