package plan

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/openapi"
)

// MaxDiffBytes is the most diff text that the prompt carries. A change at or under it is shown
// whole. A larger one is shown file by file, most important first, until the budget is spent, and
// the rest stays in .semar-plan/change.diff for the planner to search. The text goes into every
// turn, so the budget is a few thousand tokens and not the size of a release.
const MaxDiffBytes = 48 << 10

// maxAPIOperations bounds the operations of the API excerpt.
const maxAPIOperations = 40

// maxAPISummary bounds the text that an excerpt quotes from the document.
const maxAPISummary = 200

// diffChoice is the part of the diff that goes into the prompt.
type diffChoice struct {
	Text    string   // the diff, in git's unified format, as chosen; "" when none of it is shown
	Whole   bool     // Text is the whole diff
	Omitted []string // one Markdown line for each file whose hunks are not in Text
}

type diffSection struct {
	header string // the "diff --git" line
	file   *domain.FileChange
	text   string
}

// chooseDiff keeps the whole diff when it fits the budget. Otherwise it ranks the files and keeps
// whole files, in the order of the diff, while the budget lasts: a file named by a ticket before a
// file that no ticket names, then source before migrations, configuration, tests, documents, and
// generated files, then the smaller file first so that more files fit. The files of .semar come
// last, because the copy of the source has none of them.
func chooseDiff(diff []byte, files []domain.FileChange, budget int) diffChoice {
	if len(diff) <= budget {
		return diffChoice{Text: string(diff), Whole: true}
	}
	sections := splitDiff(string(diff), files)
	order := make([]int, len(sections))
	for i := range order {
		order[i] = i
	}
	rank := func(s diffSection) [3]int {
		if s.file == nil {
			return [3]int{3, 0, 0}
		}
		named := 1
		if len(s.file.Tickets) > 0 {
			named = 0
		}
		if s.file.Path == ".semar" || strings.HasPrefix(s.file.Path, ".semar/") {
			named = 2
		}
		return [3]int{named, kindRank(s.file.Kind), 0}
	}
	slices.SortStableFunc(order, func(a, b int) int {
		ra, rb := rank(sections[a]), rank(sections[b])
		for i := range ra {
			if ra[i] != rb[i] {
				return ra[i] - rb[i]
			}
		}
		return len(sections[a].text) - len(sections[b].text)
	})
	kept := make([]bool, len(sections))
	left := budget
	for _, i := range order {
		if n := len(sections[i].text); n <= left {
			kept[i] = true
			left -= n
		}
	}
	var out diffChoice
	var text strings.Builder
	for i, s := range sections {
		if kept[i] {
			text.WriteString(s.text)
			continue
		}
		out.Omitted = append(out.Omitted, omittedLine(s))
	}
	out.Text = text.String()
	return out
}

func kindRank(kind string) int {
	if i := slices.Index([]string{"source", "migration", "config", "test", "docs", "generated"}, kind); i >= 0 {
		return i
	}
	return 3
}

func omittedLine(s diffSection) string {
	if s.file == nil {
		return "- " + code(strings.TrimPrefix(s.header, "diff --git "))
	}
	return fmt.Sprintf("- %s (+%d -%d, %s)", code(s.file.Path), s.file.Added, s.file.Deleted, s.file.Kind)
}

// splitDiff cuts a git diff at its "diff --git" lines. A section is matched to its file by the
// header that git writes for the file's paths, so that a path with a space needs no parsing. A
// file whose path git quotes has no match and stays unnamed.
func splitDiff(diff string, files []domain.FileChange) []diffSection {
	byHeader := make(map[string]*domain.FileChange, len(files))
	for i := range files {
		f := &files[i]
		old := f.Path
		if f.OldPath != "" {
			old = f.OldPath
		}
		byHeader["diff --git a/"+old+" b/"+f.Path] = f
	}
	var out []diffSection
	start := 0
	cut := func(end int) {
		if end <= start {
			return
		}
		text := diff[start:end]
		header, _, _ := strings.Cut(text, "\n")
		out = append(out, diffSection{header: header, file: byHeader[header], text: text})
	}
	for pos := 0; pos < len(diff); {
		if pos > start && strings.HasPrefix(diff[pos:], "diff --git ") {
			cut(pos)
			start = pos
		}
		nl := strings.IndexByte(diff[pos:], '\n')
		if nl < 0 {
			break
		}
		pos += nl + 1
	}
	cut(len(diff))
	return out
}

// fenced puts text in a code fence that is longer than any run of back quotes in the text, so
// that the text cannot end the fence.
func fenced(lang, text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + lang + "\n" + strings.TrimRight(text, "\n") + "\n" + fence
}

func leftOutList(left []LeftOut) string {
	var b strings.Builder
	for _, l := range left {
		fmt.Fprintf(&b, "- %s: %s\n", code(l.Path), oneLine(l.Reason, maxProblemRunes))
	}
	return strings.TrimRight(b.String(), "\n")
}

var (
	braceParam = regexp.MustCompile(`\{[^{}\n]{0,64}\}`)
	colonParam = regexp.MustCompile(`/:[A-Za-z_][A-Za-z0-9_]*`)
)

// normalRoute turns every path parameter, written {name} or :name, into {}, as the OpenAPI
// reader does for a document, so that a router's name for a parameter need not equal the
// document's.
func normalRoute(s string) string {
	return colonParam.ReplaceAllString(braceParam.ReplaceAllString(s, "{}"), "/{}")
}

// apiExcerpt lists the operations of the usable API documents whose route or operation ID the diff
// mentions. It is the answer to "which routes does this change reach" for the cases where a name
// in the diff is enough, and it says where in the document to read the rest.
func apiExcerpt(sources []knowledge.Source, diff []byte) string {
	text := normalRoute(string(diff))
	var lines []string
	for _, s := range sources {
		if !s.Usable() || s.Kind != config.KnowledgeOpenAPI || s.API == nil {
			continue
		}
		for _, op := range s.API.Operations {
			if mentions(text, *s.API, op) {
				lines = append(lines, operationLine(s, op))
			}
		}
	}
	if len(lines) > maxAPIOperations {
		more := len(lines) - maxAPIOperations
		lines = append(lines[:maxAPIOperations], fmt.Sprintf("- and %d more operations", more))
	}
	return strings.Join(lines, "\n")
}

// mentions reports whether the normalised diff holds the route of the operation, with or without
// a prefix of the document, or its operation ID. A route counts only where it ends: /v1/orders is
// not in /v1/orders/{}/refunds.
func mentions(text string, doc openapi.Document, op openapi.Operation) bool {
	if len(op.ID) >= 5 && strings.ContainsAny(op.ID, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-.") && hasWord(text, op.ID, isIdentByte) {
		return true
	}
	if len(op.Path) < 2 || !strings.ContainsAny(op.Path, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return false
	}
	routes := []string{normalRoute(op.Path)}
	for _, p := range doc.Prefixes {
		routes = append(routes, normalRoute(p+op.Path))
	}
	return slices.ContainsFunc(routes, func(r string) bool { return hasWord(text, strings.TrimSuffix(r, "/"), isRouteByte) })
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '-' || b == '.' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isRouteByte(b byte) bool { return isIdentByte(b) || b == '/' || b == '{' }

// hasWord reports whether text holds word with no byte that continues it on either side. The
// test for the byte before a route is not applied, because a router may add a prefix.
func hasWord(text, word string, continues func(byte) bool) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], word)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(word)
		afterOK := end == len(text) || !continues(text[end])
		beforeOK := true
		if !strings.HasPrefix(word, "/") {
			beforeOK = i == 0 || !continues(text[i-1])
		}
		if afterOK && beforeOK {
			return true
		}
		from = i + 1
	}
}

func operationLine(s knowledge.Source, op openapi.Operation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s", code(op.Method+" "+op.Path))
	if op.ID != "" {
		fmt.Fprintf(&b, " (%s)", code(op.ID))
	}
	if op.Summary != "" {
		fmt.Fprintf(&b, ": %s", oneLine(op.Summary, maxAPISummary))
	}
	if len(op.Statuses) > 0 {
		fmt.Fprintf(&b, ". Declares %s", strings.Join(op.Statuses, ", "))
	}
	if op.Line > 0 {
		fmt.Fprintf(&b, ". See %s:%s", code(s.Path), strconv.Itoa(op.Line))
	}
	return b.String()
}
