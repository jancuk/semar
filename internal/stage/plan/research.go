package plan

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/openapi"
)

const (
	// MaxPacketDiffBytes is the most diff text that one packet carries. It is the diff of the files
	// that the ticket changed, whole files first as far as the budget lasts.
	MaxPacketDiffBytes = 24 << 10
	// maxPacketOperations bounds the operations of a packet, which the answer schema lists in full.
	maxPacketOperations = 24
	// maxRegistrationBytes bounds the part of a changed file that is read for route registrations.
	maxRegistrationBytes = 256 << 10
	minHandlerNameLen    = 4
)

// PacketOperation is one operation of the API document as a packet shows it, with everything a
// request to it needs. Route is the path template that a request uses, with the document's own
// prefix when it has exactly one.
type PacketOperation struct {
	Key          string
	Method       string
	Route        string
	Summary      string
	Source       string // <document file>:<line>, "" when the line is unknown
	Params       []openapi.Param
	Body         []openapi.Field
	BodyRequired bool
	BodyKnown    bool
	BodyOpen     bool
	Statuses     []string
	Responses    []openapi.Response
}

// packetOperation takes what a packet needs from an operation of a document.
func packetOperation(s knowledge.Source, op openapi.Operation) PacketOperation {
	route := op.Path
	if len(s.API.Prefixes) == 1 && !strings.Contains(s.API.Prefixes[0], "{") {
		route = s.API.Prefixes[0] + op.Path
	}
	po := PacketOperation{
		Key: op.Key, Method: op.Method, Route: route, Summary: oneLine(op.Summary, maxAPISummary),
		Params: op.Params, Body: op.Body, BodyRequired: op.BodyRequired, BodyKnown: op.BodyKnown, BodyOpen: op.BodyOpen,
		Statuses: op.Statuses, Responses: op.Responses,
	}
	if op.Line > 0 {
		po.Source = s.Path + ":" + strconv.Itoa(op.Line)
	}
	return po
}

// registration is a route that a changed source file registers, with the names of the code that
// answers it.
type registration struct {
	Method   string // "" when the line names none
	Route    string // normalised, as normalRoute writes it
	Handlers []string
}

var (
	// A route literal: "/v1/cards/{id}", "POST /v1/cards", '/cards/:id'.
	routeLiteral = regexp.MustCompile(`["'` + "`" + `](?:(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) +)?(/[A-Za-z0-9_\-./{}:*]*)["'` + "`" + `]`)
	identPath    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*`)
	funcDecl     = regexp.MustCompile(`(?:func|def|function|fun|public|private|protected|async)\b[^(\n]*?\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

// registrations reads the head version of each changed source file for lines that register a
// route: a route literal with the handler after it on the line, or a declaration of a handler in
// the next lines, as annotations and decorators have. It is a heuristic for the usual shapes of a
// router, and a miss only leaves an operation out of a packet.
func registrations(sourceDir string, files []domain.FileChange) []registration {
	if sourceDir == "" {
		return nil
	}
	root, err := os.OpenRoot(sourceDir)
	if err != nil {
		return nil
	}
	defer root.Close()
	var out []registration
	for _, f := range files {
		if f.Kind != "source" || f.Status == "deleted" || f.Binary {
			continue
		}
		native := filepath.FromSlash(f.Path)
		if !filepath.IsLocal(native) {
			continue
		}
		fh, err := root.Open(native)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(fh, maxRegistrationBytes))
		_ = fh.Close()
		if err != nil {
			continue
		}
		out = append(out, registrationsIn(string(data))...)
	}
	return out
}

func registrationsIn(text string) []registration {
	lines := strings.Split(text, "\n")
	var out []registration
	for i, line := range lines {
		for _, m := range routeLiteral.FindAllStringSubmatchIndex(line, -1) {
			route := line[m[4]:m[5]]
			if len(route) < 2 || !strings.ContainsAny(route, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
				continue
			}
			r := registration{Route: normalRoute(strings.TrimSuffix(route, "/"))}
			if m[2] >= 0 {
				r.Method = line[m[2]:m[3]]
			}
			after := line[m[1]:]
			for _, id := range identPath.FindAllString(after, -1) {
				r.Handlers = append(r.Handlers, id)
			}
			// An annotation or a decorator names the route, and the declaration that follows names the handler.
			if len(r.Handlers) == 0 {
				for _, next := range lines[i+1 : min(i+5, len(lines))] {
					if d := funcDecl.FindStringSubmatch(next); d != nil {
						r.Handlers = append(r.Handlers, d[1])
						break
					}
				}
			}
			out = append(out, r)
		}
	}
	return out
}

// lastName is the part of a dotted name that a declaration uses: h.Move is declared as Move.
func lastName(id string) string {
	if i := strings.LastIndexByte(id, '.'); i >= 0 {
		return id[i+1:]
	}
	return id
}

// registrationMatches says whether the registered route is the route of the operation, with or
// without a prefix of the document or of a group in the router. A router often registers a route
// without the prefix of its group, so the end of the route counts when it holds a whole path.
func registrationMatches(r registration, doc openapi.Document, op openapi.Operation) bool {
	if r.Method != "" && !strings.EqualFold(r.Method, op.Method) {
		return false
	}
	forms := []string{normalRoute(strings.TrimSuffix(op.Path, "/"))}
	for _, p := range doc.Prefixes {
		forms = append(forms, normalRoute(strings.TrimSuffix(p+op.Path, "/")))
	}
	for _, f := range forms {
		if f == r.Route || (strings.Count(r.Route, "/") >= 2 && strings.HasSuffix(f, r.Route)) {
			return true
		}
	}
	return false
}

// changedLines is the text of a diff that shows what changed: the lines added or removed and the
// function context that git writes after a hunk header.
func changedLines(diff string) string {
	var b strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-"):
			b.WriteString(line[1:] + "\n")
		case strings.HasPrefix(line, "@@"):
			if _, ctx, ok := strings.Cut(strings.TrimPrefix(line, "@@"), "@@"); ok {
				b.WriteString(ctx + "\n")
			}
		}
	}
	return b.String()
}

// ticketOperations are the operations that a packet offers, in the order of how near each is to the
// ticket. First come the ones that the criterion names, then the ones that the ticket reaches: its
// route or operationId is in the diff of the ticket's files or in the text of the ticket, or a
// changed file registers its route to code that the diff changes. Then come the operations that
// share a word of their path with one of those, such as the route that creates the record the
// ticket changes. The rest of the document follows only when the whole document fits the limit,
// because a scenario that changes a record has to create it and read it again.
func ticketOperations(sources []knowledge.Source, diff, ticketText, criterionText string, regs []registration) []PacketOperation {
	type found struct {
		op   PacketOperation
		path string
		rank int
	}
	normal := normalRoute(diff)
	changed := changedLines(diff)
	text := normalRoute(ticketText)
	named := normalRoute(criterionText)
	var all []found
	words := map[string]bool{}
	for _, s := range sources {
		if !s.Usable() || s.Kind != config.KnowledgeOpenAPI || s.API == nil {
			continue
		}
		for _, op := range s.API.Operations {
			rank := 3
			switch {
			case mentions(named, *s.API, op):
				rank = 0
			case mentions(normal, *s.API, op) || mentions(text, *s.API, op) ||
				slices.ContainsFunc(regs, func(r registration) bool {
					return registrationMatches(r, *s.API, op) && handlerChanged(r.Handlers, changed)
				}):
				rank = 1
			}
			if rank < 3 {
				for _, w := range pathWords(op.Path) {
					words[w] = true
				}
			}
			all = append(all, found{packetOperation(s, op), op.Path, rank})
		}
	}
	for i := range all {
		if all[i].rank == 3 && slices.ContainsFunc(pathWords(all[i].path), func(w string) bool { return words[w] }) {
			all[i].rank = 2
		}
	}
	slices.SortStableFunc(all, func(a, b found) int { return a.rank - b.rank })
	if len(all) > maxPacketOperations {
		all = slices.DeleteFunc(all, func(f found) bool { return f.rank == 3 })
		all = all[:min(len(all), maxPacketOperations)]
	}
	out := make([]PacketOperation, len(all))
	for i, f := range all {
		out[i] = f.op
	}
	return out
}

var versionSegment = regexp.MustCompile(`^(?:v[0-9]+|api)$`)

// pathWords are the segments of a path that name a thing: not a parameter, a version, or a prefix.
func pathWords(path string) []string {
	var out []string
	for _, seg := range strings.Split(path, "/") {
		if seg != "" && !strings.ContainsAny(seg, "{:") && !versionSegment.MatchString(seg) {
			out = append(out, strings.ToLower(seg))
		}
	}
	return out
}

// handlerChanged says whether the changed lines of a diff hold the name of a handler.
func handlerChanged(handlers []string, changed string) bool {
	for _, h := range handlers {
		name := lastName(h)
		if len(name) >= minHandlerNameLen && hasWord(changed, name, isIdentByte) {
			return true
		}
	}
	return false
}

// ticketSections are the parts of the diff for the files that a ticket changed. A ticket that no
// file names gets every part, except Semar's own files, which a plan never shows.
func ticketSections(diff []byte, files []domain.FileChange, key string) []diffSection {
	var mine, rest []diffSection
	for _, s := range splitDiff(string(diff), files) {
		switch {
		case isSemarFile(s):
		case s.file == nil || slices.Contains(s.file.Tickets, key):
			mine = append(mine, s)
		default:
			rest = append(rest, s)
		}
	}
	if len(mine) == 0 {
		return rest
	}
	return mine
}

func joinSections(sections []diffSection) string {
	var b strings.Builder
	for _, s := range sections {
		b.WriteString(s.text)
	}
	return b.String()
}

// budgetDiff is the text of the sections that fits the budget, whole files first, ranked as the
// plan's own diff is. A file that does not fit is cut at a line while at least a kilobyte is left,
// and the files that are not shown whole are listed.
func budgetDiff(sections []diffSection, budget int) (text string, omitted []string) {
	order := make([]int, len(sections))
	for i := range order {
		order[i] = i
	}
	kept := make([]bool, len(sections))
	keepByRank(sections, order, kept, budget)
	left := budget
	for i, s := range sections {
		if kept[i] {
			left -= len(s.text)
		}
	}
	var b strings.Builder
	for i, s := range sections {
		switch {
		case kept[i]:
			b.WriteString(s.text)
		case left >= 1024:
			cut := cutAtLine(s.text, left)
			b.WriteString(cut + "... the rest of this file is not shown\n")
			left -= len(cut)
			omitted = append(omitted, omittedLine(s)+", cut")
		default:
			omitted = append(omitted, omittedLine(s))
		}
	}
	return b.String(), omitted
}

func cutAtLine(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		cut = cut[:i+1]
	}
	return cut
}
