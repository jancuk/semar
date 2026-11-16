package config

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/idcheck"
)

// Knowledge is one source of documents that the planner is told to read. Path names a
// file or a folder of the repository.
type Knowledge struct {
	ID      string `yaml:"id"`
	Kind    string `yaml:"kind"`
	Path    string `yaml:"path"`
	Service string `yaml:"service"`
}

// The kinds of knowledge source.
const (
	KnowledgeFiles   = "files"
	KnowledgeOpenAPI = "openapi"
	// KnowledgeTickets names a folder of <ticket key>.md files: the requirements of the tickets.
	KnowledgeTickets = "tickets"
)

const (
	maxKnowledge     = 16
	maxKnowledgePath = 512
)

// CheckKnowledgePath accepts a path inside the repository that is written in its clean
// form with / between the names. One trailing / is allowed, and Load removes it.
func CheckKnowledgePath(p string) error {
	if p == "" {
		return fmt.Errorf("the path is empty")
	}
	if len(p) > maxKnowledgePath {
		return fmt.Errorf("the path is %d bytes long; the limit is %d", len(p), maxKnowledgePath)
	}
	switch {
	case !utf8.ValidString(p):
		return fmt.Errorf("the path %q is not valid UTF-8", p)
	case strings.Contains(p, `\`):
		return fmt.Errorf("the path %q holds a backslash; write / between the names", p)
	case strings.Contains(p, "`"):
		return fmt.Errorf("the path %q holds a back quote", p)
	case strings.ContainsFunc(p, unicode.IsControl):
		return fmt.Errorf("the path %q holds a control character", p)
	}
	clean := strings.TrimSuffix(p, "/")
	switch {
	case clean == "" || clean == ".":
		return fmt.Errorf("the path %q names the whole repository; name a file or a folder in it", p)
	case !filepath.IsLocal(clean):
		return fmt.Errorf("the path %q is not a path inside the repository", p)
	case path.Clean(clean) != clean:
		return fmt.Errorf("the path %q is not in its clean form; write %q", p, path.Clean(clean))
	}
	first, _, _ := strings.Cut(clean, "/")
	for _, reserved := range []string{".git", ".semar-plan"} {
		if strings.EqualFold(first, reserved) {
			return fmt.Errorf("the path %q is inside %s, which Semar keeps for itself", p, reserved)
		}
	}
	return nil
}

func validateKnowledge(c Config, add func(string, ...any)) {
	if len(c.Knowledge) > maxKnowledge {
		add("knowledge lists %d sources; at most %d are allowed", len(c.Knowledge), maxKnowledge)
	}
	var services []string
	for _, e := range c.Environments {
		for _, s := range e.Services {
			if !slices.Contains(services, s.Name) {
				services = append(services, s.Name)
			}
		}
	}
	slices.Sort(services)
	seen := map[string]int{}
	for i, k := range c.Knowledge {
		p := fmt.Sprintf("knowledge[%d]", i)
		if err := idcheck.Check("source id", k.ID); err != nil {
			add("%s.id: %v", p, err)
		} else if first, ok := seen[strings.ToLower(k.ID)]; ok {
			add("%s.id: %q is already used by knowledge[%d]; IDs are compared without regard to case", p, k.ID, first)
		} else {
			seen[strings.ToLower(k.ID)] = i
		}
		if k.Kind != KnowledgeFiles && k.Kind != KnowledgeOpenAPI && k.Kind != KnowledgeTickets {
			add("%s.kind must be %s, %s, or %s, got %q", p, KnowledgeFiles, KnowledgeOpenAPI, KnowledgeTickets, k.Kind)
		}
		if err := CheckKnowledgePath(k.Path); err != nil {
			add("%s.path: %v", p, err)
		}
		switch {
		case k.Service == "":
		case k.Kind != KnowledgeOpenAPI:
			add("%s.service is allowed on an %s source only", p, KnowledgeOpenAPI)
		case !slices.Contains(services, k.Service):
			add("%s.service: %q is not the name of a service of any environment; known services: %s", p, k.Service, strings.Join(services, ", "))
		}
	}
}
