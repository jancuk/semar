// Package obligation holds the use cases that need testing, the records that a person makes
// about them, and the code that derives one state for each from the outcomes of the scenarios
// that cover it.
package obligation

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/yamlfile"
)

const (
	apiVersion = "semar/v1"
	kind       = "Obligations"

	MaxFileBytes = 256 << 10
	MaxPerFile   = 500
	MaxFiles     = 100
	MaxTotal     = 5000

	maxIDBytes   = 128
	maxTitle     = 300
	maxCite      = 500
	maxGap       = 300
	maxManual    = 500 // text, expected, and one step
	maxSteps     = 20
	obligationsD = ".semar/obligations"
)

type Source string

const (
	SourceCriterion  Source = "criterion"
	SourceDiff       Source = "diff"
	SourceKnowledge  Source = "knowledge"
	SourceTestcase   Source = "testcase"
	SourceFeatureMap Source = "feature_map"
	SourceDefect     Source = "defect"
)

var sources = []Source{SourceCriterion, SourceDiff, SourceKnowledge, SourceTestcase, SourceFeatureMap, SourceDefect}

var manualKinds = []string{"physical_device", "third_party_otp", "judgement", "other"}

type Obligation struct {
	ID       string              `yaml:"id"`
	Title    string              `yaml:"title"`
	Source   Source              `yaml:"source"`
	Cite     string              `yaml:"cite,omitempty"`
	Ticket   string              `yaml:"ticket,omitempty"`
	Covers   []domain.ScenarioID `yaml:"covers,omitempty"`
	Journeys []string            `yaml:"journeys,omitempty"`
	Gap      string              `yaml:"gap,omitempty"`
	Manual   *Manual             `yaml:"manual,omitempty"`
	// Retired are the scenarios that a person took away from this use case with semar rm. The use
	// case stays, and it shows who retired each test and why, so that removing a test never makes a
	// use case look done.
	Retired []Retired `yaml:"retired,omitempty"`
}

// Retired is one scenario that semar rm took out of the covers of a use case.
type Retired struct {
	Scenario domain.ScenarioID `yaml:"scenario"`
	Reason   string            `yaml:"reason"`
	User     string            `yaml:"user"`
	Day      string            `yaml:"day"` // YYYY-MM-DD
}

type Manual struct {
	Kind      string   `yaml:"kind"`
	Text      string   `yaml:"text"`
	Steps     []string `yaml:"steps"`
	Expected  string   `yaml:"expected"`
	WrittenBy string   `yaml:"writtenBy"`
}

// File has no field for a state: code derives it (L1), and a file that names one is refused.
type File struct {
	APIVersion  string       `yaml:"apiVersion"`
	Kind        string       `yaml:"kind"`
	Ticket      string       `yaml:"ticket,omitempty"`
	Obligations []Obligation `yaml:"obligations"`

	lines map[string]int // path to line, from Parse. Nil for a file that code built
}

// Problem is one thing wrong with a file. Line is 0 when the file was not parsed from text.
type Problem struct {
	File    string
	Line    int
	Path    string
	Message string
}

func (p Problem) String() string {
	s := p.File
	if p.Line > 0 {
		s += ":" + strconv.Itoa(p.Line)
	}
	s += ": "
	if p.Path != "" {
		s += p.Path + ": "
	}
	return s + p.Message
}

// ParseError is the problems of a file that could not be read.
type ParseError struct{ Problems []Problem }

func (e *ParseError) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.String()
	}
	return strings.Join(lines, "\n")
}

func parseProblem(name string, line int, path, format string, args ...any) *ParseError {
	return &ParseError{Problems: []Problem{{File: name, Line: line, Path: path, Message: fmt.Sprintf(format, args...)}}}
}

// Parse decodes one file strictly (O1). A problem in the text is a *ParseError. The rules of
// the values are Validate's.
func Parse(data []byte, name string) (File, error) {
	if len(data) > MaxFileBytes {
		return File{}, parseProblem(name, 0, "", "the file is larger than %d KiB", MaxFileBytes>>10)
	}
	var f File
	if err := yamlfile.Decode(data, name, &f); err != nil {
		return File{}, decodeProblems(data, name, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err == nil {
		w := &walker{name: name, lines: map[string]int{}, seen: map[*yaml.Node]bool{}}
		w.node(&root, "", shapeFile)
		f.lines = w.lines
	}
	return f, nil
}

// decodeProblems turns the error of a strict decode into problems. An unknown key gets its own
// message, because a person who writes a state must learn that code computes it.
func decodeProblems(data []byte, name string, err error) error {
	var root yaml.Node
	if yaml.Unmarshal(data, &root) == nil {
		w := &walker{name: name, lines: map[string]int{}, seen: map[*yaml.Node]bool{}}
		w.node(&root, "", shapeFile)
		if len(w.unknown) > 0 {
			return &ParseError{Problems: w.unknown}
		}
	}
	var errs []error
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs = j.Unwrap()
	} else {
		errs = []error{err}
	}
	pe := &ParseError{}
	for _, e := range errs {
		msg := e.Error()
		rest, ok := strings.CutPrefix(msg, name+":")
		if !ok {
			pe.Problems = append(pe.Problems, Problem{File: name, Message: msg})
			continue
		}
		line := 0
		if num, after, found := strings.Cut(rest, ": "); found {
			if n, convErr := strconv.Atoi(num); convErr == nil {
				line, rest = n, after
			}
		}
		pe.Problems = append(pe.Problems, Problem{File: name, Line: line, Message: strings.TrimPrefix(rest, " ")})
	}
	return pe
}

const (
	shapeFile       = "file"
	shapeObligation = "obligation"
	shapeManual     = "manual"
	shapeRetired    = "retired"
)

var knownKeys = map[string][]string{
	shapeFile:       {"apiVersion", "kind", "ticket", "obligations"},
	shapeObligation: {"id", "title", "source", "cite", "ticket", "covers", "journeys", "gap", "manual", "retired"},
	shapeRetired:    {"scenario", "reason", "user", "day"},
	shapeManual:     {"kind", "text", "steps", "expected", "writtenBy"},
}

// walker records the line of every key and item, and the keys that no file may hold. A node
// that an alias points to is read once, so an alias cannot multiply the work.
type walker struct {
	name    string
	lines   map[string]int
	unknown []Problem
	seen    map[*yaml.Node]bool
}

func (w *walker) node(n *yaml.Node, p, shape string) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) > 0 {
			w.node(n.Content[0], p, shape)
		}
	case yaml.AliasNode:
		w.lines[p] = n.Line
	case yaml.MappingNode:
		if w.seen[n] {
			return
		}
		w.seen[n] = true
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			child := k.Value
			if p != "" {
				child = p + "." + k.Value
			}
			w.lines[child] = k.Line
			known := knownKeys[shape]
			if known != nil && !slices.Contains(known, k.Value) {
				msg := "unknown key " + k.Value
				if k.Value == "state" {
					msg += ": a state is computed by code and cannot be written"
				}
				w.unknown = append(w.unknown, Problem{File: w.name, Line: k.Line, Path: p, Message: msg})
				continue
			}
			w.node(v, child, childShape(shape, k.Value))
		}
	case yaml.SequenceNode:
		if w.seen[n] {
			return
		}
		w.seen[n] = true
		for i, item := range n.Content {
			ip := p + "[" + strconv.Itoa(i) + "]"
			w.lines[ip] = item.Line
			w.node(item, ip, seqShape(shape))
		}
	}
}

func childShape(shape, key string) string {
	switch {
	case shape == shapeFile && key == "obligations":
		return shapeObligation + "[]"
	case shape == shapeObligation && key == "manual":
		return shapeManual
	case shape == shapeObligation && key == "retired":
		return shapeRetired + "[]"
	}
	return ""
}

func seqShape(shape string) string { return strings.TrimSuffix(shape, "[]") }

// line is the line of path, or of the nearest place above it that has one.
func (f File) line(p string) int {
	for {
		if n, ok := f.lines[p]; ok {
			return n
		}
		i := strings.LastIndexAny(p, ".[")
		if i < 0 {
			return 0
		}
		p = p[:i]
	}
}

// Validate checks the rules O2 to O10 and the count of O1. It reports every problem.
func Validate(f File, name string) []Problem {
	var ps []Problem
	add := func(p, format string, args ...any) {
		ps = append(ps, Problem{File: name, Line: f.line(p), Path: p, Message: fmt.Sprintf(format, args...)})
	}
	if f.APIVersion != apiVersion {
		add("apiVersion", "apiVersion must be %s", apiVersion)
	}
	if f.Kind != kind {
		add("kind", "kind must be %s", kind)
	}
	if f.Ticket != "" {
		if err := idcheck.Check("ticket", f.Ticket); err != nil {
			add("ticket", "%s", err)
		}
	}
	if len(f.Obligations) > MaxPerFile {
		add("obligations", "the file has more than %d obligations", MaxPerFile)
	}
	firstAt := map[string]int{}
	for i, o := range f.Obligations {
		p := fmt.Sprintf("obligations[%d]", i)
		validateObligation(o, f.Ticket, p, add)
		if o.ID == "" {
			continue
		}
		if j, dup := firstAt[o.ID]; dup {
			add(p+".id", "id %s is also at obligations[%d]", o.ID, j)
		} else {
			firstAt[o.ID] = i
		}
	}
	return ps
}

func validateObligation(o Obligation, fileTicket, p string, add func(path, format string, args ...any)) {
	validateID(o.ID, fileTicket, p+".id", add)

	switch title := strings.TrimSpace(o.Title); {
	case title == "":
		add(p+".title", "title is required")
	case multiline(o.Title):
		add(p+".title", "title must be one line")
	case utf8.RuneCountInString(o.Title) > maxTitle:
		add(p+".title", "title is longer than %d characters", maxTitle)
	}

	switch {
	case o.Source == "":
		add(p+".source", "source is required; use one of %s", sourceList())
	case !slices.Contains(sources, o.Source):
		add(p+".source", "source %s is not a source; use one of %s", o.Source, sourceList())
	}

	switch {
	case o.Cite == "" && (o.Source == SourceCriterion || o.Source == SourceTestcase || o.Source == SourceDefect):
		add(p+".cite", "cite is required for the source %s", o.Source)
	case multiline(o.Cite):
		add(p+".cite", "cite must be one line")
	case utf8.RuneCountInString(o.Cite) > maxCite:
		add(p+".cite", "cite is longer than %d characters", maxCite)
	}

	for i, c := range o.Covers {
		if err := idcheck.Scenario(string(c)); err != nil {
			add(fmt.Sprintf("%s.covers[%d]", p, i), "%s", err)
		}
	}
	for i, j := range o.Journeys {
		if err := idcheck.Check("journey ID", j); err != nil {
			add(fmt.Sprintf("%s.journeys[%d]", p, i), "%s", err)
		}
	}

	switch {
	case multiline(o.Gap):
		add(p+".gap", "gap must be one line")
	case utf8.RuneCountInString(o.Gap) > maxGap:
		add(p+".gap", "gap is longer than %d characters", maxGap)
	}

	switch {
	case o.Ticket == "":
	case fileTicket != "" && o.Ticket != fileTicket:
		add(p+".ticket", "ticket %s differs from the file's ticket %s", o.Ticket, fileTicket)
	default:
		if err := idcheck.Check("ticket", o.Ticket); err != nil {
			add(p+".ticket", "%s", err)
		}
	}

	if o.Manual != nil {
		validateManual(*o.Manual, p+".manual", add)
	}
	for i, r := range o.Retired {
		validateRetired(r, fmt.Sprintf("%s.retired[%d]", p, i), add)
	}
}

func validateRetired(r Retired, p string, add func(path, format string, args ...any)) {
	if err := idcheck.Scenario(string(r.Scenario)); err != nil {
		add(p+".scenario", "%s", err)
	}
	switch {
	case strings.TrimSpace(r.Reason) == "":
		add(p+".reason", "reason is required")
	case multiline(r.Reason):
		add(p+".reason", "reason must be one line")
	case utf8.RuneCountInString(r.Reason) > maxGap:
		add(p+".reason", "reason is longer than %d characters", maxGap)
	}
	switch {
	case strings.TrimSpace(r.User) == "":
		add(p+".user", "user is required")
	case multiline(r.User):
		add(p+".user", "user must be one line")
	}
	if _, err := time.Parse("2006-01-02", r.Day); err != nil {
		add(p+".day", "day must be YYYY-MM-DD")
	}
}

func validateID(id, ticket, p string, add func(path, format string, args ...any)) {
	if id == "" {
		add(p, "id is required")
		return
	}
	group, name, ok := strings.Cut(id, "/")
	if !ok || strings.Contains(name, "/") {
		add(p, "id must be <group>/<name>")
		return
	}
	if len(id) > maxIDBytes {
		add(p, "id is longer than %d bytes", maxIDBytes)
	}
	if err := idcheck.Check("id group", group); err != nil {
		add(p, "%s", err)
	}
	if err := idcheck.Check("id name", name); err != nil {
		add(p, "%s", err)
	}
	if ticket != "" && group != ticket {
		add(p, "id group must be the ticket %s", ticket)
	}
}

func validateManual(m Manual, p string, add func(path, format string, args ...any)) {
	if !slices.Contains(manualKinds, m.Kind) {
		add(p+".kind", "manual.kind must be one of %s", strings.Join(manualKinds, ", "))
	}
	oneLine := func(field, text string, required bool) {
		switch {
		case strings.TrimSpace(text) == "":
			if required {
				add(p+"."+field, "manual.%s is required", field)
			}
		case multiline(text):
			add(p+"."+field, "manual.%s must be one line", field)
		case utf8.RuneCountInString(text) > maxManual:
			add(p+"."+field, "manual.%s is longer than %d characters", field, maxManual)
		}
	}
	oneLine("text", m.Text, true)
	oneLine("expected", m.Expected, true)
	switch {
	case len(m.Steps) == 0:
		add(p+".steps", "manual.steps needs at least one step")
	case len(m.Steps) > maxSteps:
		add(p+".steps", "manual.steps has more than %d steps", maxSteps)
	}
	for i, s := range m.Steps {
		switch {
		case strings.TrimSpace(s) == "":
			add(fmt.Sprintf("%s.steps[%d]", p, i), "manual.steps[%d] is empty", i)
		case multiline(s):
			add(fmt.Sprintf("%s.steps[%d]", p, i), "manual.steps[%d] must be one line", i)
		case utf8.RuneCountInString(s) > maxManual:
			add(fmt.Sprintf("%s.steps[%d]", p, i), "manual.steps[%d] is longer than %d characters", i, maxManual)
		}
	}
	switch m.WrittenBy {
	case "person", "code", "ai":
	default:
		add(p+".writtenBy", "manual.writtenBy must be person, code, or ai")
	}
}

func multiline(s string) bool { return strings.ContainsAny(s, "\n\r  ") }

func sourceList() string {
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

// Safe makes text from a file safe to print on a terminal: a control character is shown as an
// escape, and a format character is dropped. Text in a file is data, never a command.
func Safe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r):
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == ' ' || r == ' ':
			fmt.Fprintf(&b, `\u%04x`, r)
		case unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Located is an obligation with the place it was read from.
type Located struct {
	Obligation
	File string // the path from the repository root, with slashes
	Line int
}

// Load reads every .yaml and .yml file below .semar/obligations. .semar and
// .semar/obligations must be real directories when they exist, and an entry below them that is
// a link or a special file is an error, because a linked folder would hide its obligations.
// An ID that two files hold is a problem that names both (O3).
func Load(repo string) (list []Located, problems []Problem, err error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, nil, fmt.Errorf("read obligations: %w", err)
	}
	defer root.Close()
	for _, d := range []string{".semar", obligationsD} {
		info, err := root.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, nil, nil
		case err != nil:
			return nil, nil, fmt.Errorf("read obligations in %s: %w", filepath.Join(repo, obligationsD), err)
		case info.Mode()&fs.ModeSymlink != 0:
			return nil, nil, fmt.Errorf("%s is a symbolic link, and Semar reads obligations only from real directories: replace it with a directory", filepath.Join(repo, d))
		case !info.IsDir():
			return nil, nil, fmt.Errorf("%s is not a directory, and Semar reads obligations from a directory there", filepath.Join(repo, d))
		}
	}
	var files []string
	if err := walk(root, obligationsD, &files); err != nil {
		return nil, nil, err
	}

	idFile := map[string]string{}
	for _, rel := range files {
		data, err := ReadFile(root, rel)
		if err != nil {
			return nil, nil, err
		}
		f, err := Parse(data, rel)
		var pe *ParseError
		switch {
		case errors.As(err, &pe):
			problems = append(problems, pe.Problems...)
			continue
		case err != nil:
			return nil, nil, err
		}
		problems = append(problems, Validate(f, rel)...)
		for i, o := range f.Obligations {
			if len(list) >= MaxTotal {
				return nil, nil, fmt.Errorf("%s holds more than %d obligations in all: remove the ones that no longer apply", filepath.Join(repo, obligationsD), MaxTotal)
			}
			p := fmt.Sprintf("obligations[%d]", i)
			if first, dup := idFile[o.ID]; dup && first != rel {
				problems = append(problems, Problem{File: rel, Line: f.line(p + ".id"), Path: p + ".id", Message: fmt.Sprintf("id %s is also in %s", o.ID, first)})
			} else if !dup {
				idFile[o.ID] = rel
			}
			if o.Ticket == "" {
				o.Ticket = f.Ticket
			}
			list = append(list, Located{Obligation: o, File: rel, Line: f.line(p)})
		}
	}
	return list, problems, nil
}

func walk(root *os.Root, dir string, files *[]string) error {
	d, err := root.Open(dir)
	if err != nil {
		return fmt.Errorf("read obligations in %s: %w", filepath.Join(root.Name(), dir), err)
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return fmt.Errorf("read obligations in %s: %w", filepath.Join(root.Name(), dir), err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, e := range entries {
		rel := path.Join(dir, e.Name())
		shown := filepath.Join(root.Name(), rel)
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symbolic link, and Semar reads obligations only from real files and directories", shown)
		case e.IsDir():
			if err := walk(root, rel, files); err != nil {
				return err
			}
		case !e.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file (%s), and .semar/obligations may hold only files and directories", shown, e.Type())
		default:
			if ext := path.Ext(rel); ext == ".yaml" || ext == ".yml" {
				if len(*files) >= MaxFiles {
					return fmt.Errorf("%s holds more than %d files: merge or remove some", filepath.Join(root.Name(), obligationsD), MaxFiles)
				}
				*files = append(*files, rel)
			}
		}
	}
	return nil
}

// ReadFile reads a regular file of a root, at most one byte more than the limit, so Parse can
// refuse a file that is too large without the caller holding all of it. A link, a file that is
// not regular, and a file that was swapped between the check and the open are errors.
func ReadFile(root *os.Root, rel string) ([]byte, error) {
	shown := filepath.Join(root.Name(), rel)
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, fmt.Errorf("read obligations %s: %w", shown, err)
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("%s is a symbolic link, and Semar reads obligations only from real files", shown)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file", shown)
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, fmt.Errorf("read obligations %s: %w", shown, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read obligations %s: %w", shown, err)
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s changed while it was read", shown)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read obligations %s: %w", shown, err)
	}
	return data, nil
}
