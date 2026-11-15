package plan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
)

// Note is one difference between a drafted request and an API document.
type Note struct {
	Scenario domain.ScenarioID
	Step     string
	Source   string // the ID of the knowledge source
	Message  string
}

// compare checks every step of accepted scenarios against every API document that was read.
// A difference never refuses a draft: the document can be the stale one (D3).
func compare(in Input, scenarios []domain.Scenario, scripts []domain.Script) []Note {
	var apis []knowledge.Source
	for _, s := range in.Knowledge {
		if s.Usable() && s.API != nil {
			apis = append(apis, s)
		}
	}
	if len(apis) == 0 {
		return nil
	}
	var notes []Note
	for i, sc := range scenarios {
		for _, st := range sc.Steps {
			if st.Hint == nil {
				continue
			}
			service := stepService(scripts, i, st.ID)
			for _, src := range apis {
				if src.Service != "" && src.Service != service {
					continue
				}
				for _, msg := range compareStep(src, st) {
					notes = append(notes, Note{Scenario: sc.ID, Step: string(st.ID), Source: src.ID, Message: msg})
				}
			}
		}
	}
	return notes
}

// stepService is the service of the request of a step. Validation has already given a step
// with no service the only service of the configuration.
func stepService(scripts []domain.Script, i int, id domain.StepID) string {
	if i >= len(scripts) {
		return ""
	}
	for _, s := range scripts[i].Steps {
		if s.Step == id && s.Request != nil {
			return s.Request.Service
		}
	}
	return ""
}

func compareStep(src knowledge.Source, st domain.Step) []string {
	method, route := st.Hint.Method, st.Hint.Path
	m := src.API.Lookup(method, route)
	switch {
	case !m.PathKnown:
		return []string{fmt.Sprintf("%s %s is not in the API document", method, route)}
	case !m.Found:
		return []string{fmt.Sprintf("the API document has %s with %s, and not with %s", route, andList(m.Methods), method)}
	}
	var out []string
	var seen []int
	for _, e := range st.Expect {
		if e.Check.Status == nil || slices.Contains(seen, *e.Check.Status) {
			continue
		}
		status := *e.Check.Status
		seen = append(seen, status)
		if m.Operation.Declares(status) {
			continue
		}
		declared := "declares no status"
		if len(m.Operation.Statuses) > 0 {
			declared = "declares " + andList(m.Operation.Statuses)
		}
		out = append(out, fmt.Sprintf("status %d is not declared for %s %s; the document %s", status, method, route, declared))
	}
	return out
}

// andList joins words as a sentence does: "a", "a and b", "a, b, and c".
func andList(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[0] + " and " + words[1]
	}
	return strings.Join(words[:len(words)-1], ", ") + ", and " + words[len(words)-1]
}

// knowledgeList is the Markdown list of the usable sources for the prompt. Every text from the
// configuration or a document is quoted as code on one line, so it cannot change the prompt.
func knowledgeList(sources []knowledge.Source) string {
	var b strings.Builder
	for _, s := range sources {
		if s.Usable() {
			fmt.Fprintf(&b, "- %s: %s\n", code(s.ID), describe(s, code, ""))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// describe says what a usable source is and what was found in it. quote is applied to every
// text that comes from the configuration or a document. why follows the words that say that
// an API document could not be read.
func describe(s knowledge.Source, quote func(string) string, why string) string {
	switch {
	case s.Kind == config.KnowledgeOpenAPI && s.API != nil:
		return fmt.Sprintf("the API document %s (%s %s, %s)", quote(s.Path), apiFamily(s.API.Version), quote(s.API.Version), counted(len(s.API.Operations), "operation"))
	case s.Kind == config.KnowledgeOpenAPI:
		return fmt.Sprintf("the API document %s (code could not read its routes%s)", quote(s.Path), why)
	case s.Folder && s.Files > knowledge.MaxFiles:
		return fmt.Sprintf("the documents under %s (more than %s files)", quote(s.Path), thousands(knowledge.MaxFiles))
	case s.Folder:
		return fmt.Sprintf("the documents under %s (%s)", quote(s.Path), counted(s.Files, "file"))
	}
	return "the document " + quote(s.Path)
}

// apiFamily names the format by its version: Swagger documents are version 2, and OpenAPI
// documents start at 3.
func apiFamily(version string) string {
	if strings.HasPrefix(version, "2") {
		return "Swagger"
	}
	return "OpenAPI"
}

func counted(n int, noun string) string {
	switch n {
	case 0:
		return "no " + noun
	case 1:
		return "1 " + noun
	}
	return thousands(n) + " " + noun + "s"
}

// thousands writes n with a comma between each group of three digits.
func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
