package agentloop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	toolReadFile  = "read_file"
	toolSearch    = "search"
	toolListFiles = "list_files"
	toolSubmit    = "submit"

	maxResultBytes     = 256 << 10 // any tool result, and the text of one read_file call
	defaultReadLines   = 2000
	maxLineBytes       = 4 << 10 // a longer line of read_file is cut
	maxHits            = 200
	maxHitBytes        = 300 // the text of one search hit
	maxSearchFileBytes = 2 << 20
	maxListPaths       = 2000
	sniffBytes         = 8 << 10
)

// maxVisited is how many entries one walk of the tree may visit. Tests lower it.
var maxVisited = 50_000

func knownTool(name string) bool {
	switch name {
	case toolReadFile, toolSearch, toolListFiles, toolSubmit:
		return true
	}
	return false
}

func toolDefs(schema json.RawMessage) []Tool {
	return []Tool{
		{
			Name: toolReadFile,
			Description: "Read lines of one text file in the working directory. The result holds each line with its " +
				"line number. It returns at most 2000 lines or 256 KiB per call; a note says where to continue. " +
				"A binary file is refused.",
			Schema: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"The file path, relative to the working directory."},` +
				`"offset":{"type":"integer","description":"The first line to return, counted from 1. Default 1."},` +
				`"limit":{"type":"integer","description":"The number of lines to return. Default 2000."}},` +
				`"required":["path"]}`),
		},
		{
			Name: toolSearch,
			Description: "Search the contents of the text files in the working directory with a regular expression " +
				"in RE2 syntax. Each hit is one line, written as path:line: text. At most 200 hits are returned. " +
				"Files over 2 MiB and binary files are skipped.",
			Schema: json.RawMessage(`{"type":"object","properties":{` +
				`"pattern":{"type":"string","description":"An RE2 regular expression, matched against each line."},` +
				`"glob":{"type":"string","description":"Only files whose path matches, for example **/*.go or internal/**/*_test.go. A pattern without a slash matches the file name. Default: every file."}},` +
				`"required":["pattern"]}`),
		},
		{
			Name: toolListFiles,
			Description: "List the files in the working directory whose path matches a glob, sorted. " +
				"At most 2000 paths are returned; a note says when the list was cut.",
			Schema: json.RawMessage(`{"type":"object","properties":{` +
				`"glob":{"type":"string","description":"For example **/*.go or cmd/*. A pattern without a slash matches the file name. Default: every file."}}}`),
		},
		{
			Name: toolSubmit,
			Description: "Return the final result. The arguments are the complete result object in the required " +
				"schema. Calling this tool ends the job, so call it once, when the result is complete.",
			Schema: schema,
		},
	}
}

// workspace runs the file tools. Every access goes through an os.Root, so the operating
// system refuses a path that leaves the working directory, including through a link.
type workspace struct {
	root *os.Root
	fsys fs.FS
}

func openWorkspace(dir string) (*workspace, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &workspace{root: root, fsys: root.FS()}, nil
}

func (w *workspace) close() { _ = w.root.Close() }

// call runs one tool call. A failure is text for the model, never a failure of the job.
func (w *workspace) call(ctx context.Context, c ToolCall) (string, bool) {
	var text string
	var err error
	switch c.Name {
	case toolReadFile:
		var a struct {
			Path          string
			Offset, Limit flexInt
		}
		if err = decodeArgs(c.Args, &a); err == nil {
			text, err = w.readFile(ctx, a.Path, int(a.Offset), int(a.Limit))
		}
	case toolSearch:
		var a struct{ Pattern, Glob string }
		if err = decodeArgs(c.Args, &a); err == nil {
			text, err = w.search(ctx, a.Pattern, a.Glob)
		}
	case toolListFiles:
		var a struct{ Glob string }
		if err = decodeArgs(c.Args, &a); err == nil {
			text, err = w.listFiles(ctx, a.Glob)
		}
	case toolSubmit:
		err = errors.New("submit needs the complete result as one JSON object")
	default:
		err = fmt.Errorf("there is no tool named %q; the tools are read_file, search, list_files, and submit", cutText(c.Name, 100))
	}
	if err != nil {
		return capResult("error: " + err.Error()), true
	}
	return capResult(text), false
}

func decodeArgs(raw json.RawMessage, v any) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("the arguments are not a valid JSON object: %v", err)
	}
	return nil
}

// flexInt accepts a number or a string that holds one, as models write both.
type flexInt int

func (n *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f != float64(int(f)) {
		return fmt.Errorf("%s is not a whole number", cutText(string(b), 40))
	}
	*n = flexInt(f)
	return nil
}

func (w *workspace) readFile(ctx context.Context, name string, offset, limit int) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("read_file needs a path")
	}
	if offset < 1 {
		offset = 1
	}
	if limit < 1 {
		limit = defaultReadLines
	}
	f, err := w.root.Open(filepath.FromSlash(name))
	if err != nil {
		return "", openError(name, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", openError(name, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; use list_files", name)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", name)
	}
	r := bufio.NewReaderSize(f, 64<<10)
	if head, _ := r.Peek(sniffBytes); isBinary(head) {
		return "", fmt.Errorf("%s is a binary file", name)
	}
	var out strings.Builder
	n := 0
	for {
		line, cut, err := readLine(r)
		if line == nil && err == io.EOF {
			break
		}
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("reading %s: %v", name, err)
		}
		n++
		if n%1000 == 0 && ctx.Err() != nil {
			return "", ctx.Err()
		}
		if n >= offset+limit {
			fmt.Fprintf(&out, "[more lines follow; continue with offset %d]\n", n)
			break
		}
		if n >= offset {
			entry := strconv.Itoa(n) + "\t" + string(line)
			if cut {
				entry += " [line cut]"
			}
			entry += "\n"
			if out.Len()+len(entry) > maxResultBytes-100 {
				fmt.Fprintf(&out, "[stopped at 256 KiB; continue with offset %d]\n", n)
				break
			}
			out.WriteString(entry)
		}
		if err == io.EOF {
			break
		}
	}
	switch {
	case n == 0:
		return fmt.Sprintf("[%s is empty]", name), nil
	case out.Len() == 0:
		return fmt.Sprintf("[%s has %d lines; offset %d is past the end]", name, n, offset), nil
	}
	return out.String(), nil
}

// readLine returns one line without its end, cut to maxLineBytes. The error is nil, io.EOF
// after a last line without a newline, or the error of the reader.
func readLine(r *bufio.Reader) (line []byte, cut bool, err error) {
	over := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !over {
			line = append(line, chunk...)
			over = len(line) > maxLineBytes+2
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(line) == 0 {
			return nil, false, io.EOF
		}
		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		if cut = over || len(line) > maxLineBytes; cut {
			line = validUTF8Prefix(line[:min(len(line), maxLineBytes)])
		}
		return line, cut, err
	}
}

func openError(name string, err error) error {
	var pe *fs.PathError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("no such file: %s", name)
	case errors.As(err, &pe):
		return fmt.Errorf("cannot open %s: %v", name, pe.Err)
	}
	return fmt.Errorf("cannot open %s: %v", name, err)
}

func (w *workspace) search(ctx context.Context, pattern, glob string) (string, error) {
	if pattern == "" {
		return "", errors.New("search needs a pattern")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("the pattern is not a valid RE2 regular expression: %v", err)
	}
	m, err := newGlob(glob)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	hits, large := 0, 0
	walkNote, err := w.walk(ctx, func(p string, info fs.FileInfo) bool {
		if !m.match(p) {
			return true
		}
		if info.Size() > maxSearchFileBytes {
			large++
			return true
		}
		data, err := fs.ReadFile(w.fsys, p)
		if err != nil || isBinary(data[:min(len(data), sniffBytes)]) {
			return true
		}
		for n, line := range bytes.Split(data, []byte("\n")) {
			if !re.Match(line) {
				continue
			}
			if hits == maxHits {
				return false
			}
			hits++
			fmt.Fprintf(&out, "%s:%d: %s\n", p, n+1, cutText(strings.TrimRight(string(line), "\r"), maxHitBytes))
		}
		return true
	})
	if err != nil {
		return "", err
	}
	switch {
	case hits == 0:
		out.WriteString("no match\n")
	case hits == maxHits:
		fmt.Fprintf(&out, "[stopped at %d hits; narrow the pattern or the glob]\n", maxHits)
	}
	if large > 0 {
		fmt.Fprintf(&out, "[files skipped because they are over 2 MiB: %d]\n", large)
	}
	out.WriteString(walkNote)
	return out.String(), nil
}

func (w *workspace) listFiles(ctx context.Context, glob string) (string, error) {
	m, err := newGlob(glob)
	if err != nil {
		return "", err
	}
	var paths []string
	walkNote, err := w.walk(ctx, func(p string, _ fs.FileInfo) bool {
		if m.match(p) {
			paths = append(paths, p)
		}
		return true
	})
	if err != nil {
		return "", err
	}
	slices.Sort(paths)
	var out strings.Builder
	for _, p := range paths[:min(len(paths), maxListPaths)] {
		out.WriteString(p + "\n")
	}
	switch {
	case len(paths) == 0:
		out.WriteString("no file matches\n")
	case len(paths) > maxListPaths:
		fmt.Fprintf(&out, "[showing the first %d of %d paths; use a narrower glob]\n", maxListPaths, len(paths))
	}
	out.WriteString(walkNote)
	return out.String(), nil
}

// walk calls fn for each regular file, by its slash path, until fn returns false. It never
// follows a link, skips .git, and stops after maxVisited entries, which the note then says.
func (w *workspace) walk(ctx context.Context, fn func(p string, info fs.FileInfo) bool) (note string, err error) {
	visited := 0
	err = fs.WalkDir(w.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if p == "." {
				return err
			}
			return nil
		}
		if p == "." {
			return nil
		}
		visited++
		if visited > maxVisited {
			note = fmt.Sprintf("[the walk stopped after %d entries; the rest of the tree was not read]\n", maxVisited)
			return fs.SkipAll
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !fn(p, info) {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("the working directory could not be read: %v", err)
	}
	return note, nil
}

func isBinary(head []byte) bool { return bytes.IndexByte(head, 0) >= 0 }

func cutText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return string(validUTF8Prefix([]byte(s[:n]))) + "..."
}

func validUTF8Prefix(b []byte) []byte {
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return b
}

func capResult(s string) string {
	if len(s) <= maxResultBytes {
		return s
	}
	return string(validUTF8Prefix([]byte(s[:maxResultBytes-64]))) + "\n[the result was cut at 256 KiB]\n"
}

// glob matches slash paths. "**" matches any number of directories, "{a,b}" either word,
// and a pattern without a slash matches the file name, as in most search programs.
type glob struct{ alts [][]string }

func newGlob(pattern string) (glob, error) {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
	if pattern == "" {
		return glob{}, nil
	}
	var g glob
	for _, alt := range expandBraces(pattern) {
		alt = strings.TrimPrefix(alt, "/")
		if !strings.Contains(alt, "/") {
			alt = "**/" + alt
		}
		segs := strings.Split(alt, "/")
		for _, s := range segs {
			if _, err := path.Match(s, ""); err != nil {
				return glob{}, fmt.Errorf("the glob %q is not valid", pattern)
			}
		}
		g.alts = append(g.alts, segs)
	}
	return g, nil
}

func (g glob) match(p string) bool {
	if g.alts == nil {
		return true
	}
	parts := strings.Split(p, "/")
	for _, segs := range g.alts {
		if matchSegs(segs, parts) {
			return true
		}
	}
	return false
}

func matchSegs(segs, parts []string) bool {
	for len(segs) > 0 {
		if segs[0] == "**" {
			for i := 0; i <= len(parts); i++ {
				if matchSegs(segs[1:], parts[i:]) {
					return true
				}
			}
			return false
		}
		if len(parts) == 0 {
			return false
		}
		if ok, _ := path.Match(segs[0], parts[0]); !ok {
			return false
		}
		segs, parts = segs[1:], parts[1:]
	}
	return len(parts) == 0
}

func expandBraces(p string) []string {
	open := strings.IndexByte(p, '{')
	if open < 0 {
		return []string{p}
	}
	end := strings.IndexByte(p[open:], '}')
	if end < 0 {
		return []string{p}
	}
	end += open
	var out []string
	for _, word := range strings.Split(p[open+1:end], ",") {
		out = append(out, expandBraces(p[:open]+word+p[end+1:])...)
	}
	return out
}
