package journey

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/screens"
)

const (
	maxFiles = 200
	// relDir is how a problem names the directory.
	relDir = ".semar/journeys"
)

// Load reads .semar/journeys of the repository. A missing directory gives no journeys. The
// repository is untrusted: .semar and .semar/journeys must be real directories, and a link or a
// special file below them is an error and not skipped, because a skipped journey would hide a
// critical flow. A file that cannot be used is a Problem, and the valid files stay. The fixtures
// that a journey names are looked up in the valid fixtures of the same repository.
func Load(repo string) ([]domain.Journey, []Problem, error) {
	root, err := openDir(repo)
	if err != nil || root == nil {
		return nil, nil, err
	}
	defer root.Close()

	entries, err := list(root)
	if err != nil {
		return nil, nil, err
	}
	var names []string
	stems := map[string][]string{}
	var problems []Problem
	for _, e := range entries {
		name := e.Name()
		rel := relDir + "/" + name
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			return nil, nil, fmt.Errorf("%s is a symbolic link, and Semar reads journeys only from real files: replace it with the file", rel)
		case e.IsDir():
			problems = append(problems, Problem{File: rel, Message: "journeys must be files directly in " + relDir})
			continue
		case !e.Type().IsRegular():
			return nil, nil, fmt.Errorf("%s is not a regular file (%s), and %s may hold only journey files", rel, e.Type(), relDir)
		}
		if ext := filepath.Ext(name); ext == ".yaml" || ext == ".yml" {
			stem := strings.TrimSuffix(name, ext)
			stems[stem] = append(stems[stem], name)
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, problems, nil
	}

	cat, err := fixture.Load(repo, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("read the fixtures that journeys name: %w", err)
	}
	outputs := func(id domain.FixtureID) ([]string, bool) {
		f, ok := cat.Get(id)
		return f.Outputs, ok
	}

	var out []domain.Journey
	for _, name := range names {
		rel := relDir + "/" + name
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if same := stems[stem]; len(same) > 1 {
			problems = append(problems, Problem{File: rel, Message: fmt.Sprintf("journey %s has two files: %s", stem, strings.Join(same, " and "))})
			continue
		}
		data, big, err := readFile(root, name)
		if err != nil {
			return nil, nil, err
		}
		if big {
			problems = append(problems, Problem{File: rel, Message: tooLarge})
			continue
		}
		j, ps := decode(data, rel, outputs, func(app string) (domain.Screens, error) { return screens.Load(repo, app) })
		if len(ps) > 0 {
			problems = append(problems, ps...)
			continue
		}
		out = append(out, j)
	}
	slices.SortFunc(out, func(a, b domain.Journey) int { return strings.Compare(string(a.ID), string(b.ID)) })
	slices.SortStableFunc(problems, func(a, b Problem) int { return strings.Compare(a.File, b.File) })
	return out, problems, nil
}

// decode parses and validates one file. rel is how a problem names it.
func decode(data []byte, rel string, outputs func(domain.FixtureID) ([]string, bool), screensOf ScreensFunc) (domain.Journey, []Problem) {
	j, err := Parse(data, rel)
	if err != nil {
		return domain.Journey{}, parseProblems(err, rel)
	}
	ps := validateFull(j, rel, outputs, screensOf)
	if len(ps) == 0 {
		return j, nil
	}
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err == nil {
		for i := range ps {
			ps[i].Line = lineOf(&doc, ps[i].Path)
		}
	}
	return domain.Journey{}, ps
}

var (
	linePrefix = regexp.MustCompile(`^(\d+): (.*)$`)
	pathPrefix = regexp.MustCompile(`^((?:legs|given|invariants)\[[0-9]+\][A-Za-z0-9_.\[\]]*): (.*)$`)
)

// parseProblems turns the error of Parse, one "<name>:<line>: <message>" per line, into problems.
func parseProblems(err error, rel string) []Problem {
	var ps []Problem
	for _, text := range strings.Split(err.Error(), "\n") {
		rest := strings.TrimSpace(strings.TrimPrefix(text, rel+":"))
		p := Problem{File: rel, Message: rest}
		if m := linePrefix.FindStringSubmatch(rest); m != nil {
			p.Line, _ = strconv.Atoi(m[1])
			p.Message = m[2]
		}
		if m := pathPrefix.FindStringSubmatch(p.Message); m != nil {
			p.Path, p.Message = m[1], m[2]
		}
		ps = append(ps, p)
	}
	return ps
}

// lineOf is the line of the place that path names, such as legs[1].steps[0].request.path. A place
// that is not in the file, such as a key that is missing, gets the line of the nearest place that
// is. It returns 0 when the file has no place at all.
func lineOf(doc *yaml.Node, path string) int {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	n = resolve(n)
	line := n.Line
	if path == "" {
		return line
	}
	for _, part := range strings.Split(path, ".") {
		key, idx, ok := splitIndex(part)
		if !ok {
			return line
		}
		if key != "" {
			next := keyNode(n, key)
			if next == nil {
				return line
			}
			n, line = next, next.Line
		}
		for _, i := range idx {
			if n.Kind != yaml.SequenceNode || i >= len(n.Content) {
				return line
			}
			n = resolve(n.Content[i])
			line = n.Line
		}
	}
	return line
}

// keyNode returns the value of key in a mapping node, with the line of the key kept in its place.
func keyNode(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			v := *resolve(m.Content[i+1])
			v.Line = k.Line
			return &v
		}
	}
	return nil
}

var indexPattern = regexp.MustCompile(`\[([0-9]+)\]`)

// splitIndex splits "steps[2][0]" into the key "steps" and the indexes 2 and 0.
func splitIndex(part string) (key string, idx []int, ok bool) {
	key, rest, _ := strings.Cut(part, "[")
	if rest == "" {
		return key, nil, true
	}
	rest = "[" + rest
	for _, m := range indexPattern.FindAllStringSubmatch(rest, -1) {
		i, err := strconv.Atoi(m[1])
		if err != nil {
			return "", nil, false
		}
		idx = append(idx, i)
	}
	return key, idx, indexPattern.ReplaceAllString(rest, "") == ""
}

// openDir opens .semar/journeys as a root. It returns nil and no error when the directory is not
// there. Both directories must be real: a link would read files from outside the repository.
func openDir(repo string) (*os.Root, error) {
	dirs := []string{filepath.Join(repo, ".semar"), filepath.Join(repo, ".semar", "journeys")}
	var last fs.FileInfo
	for _, d := range dirs {
		info, err := os.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, nil
		case err != nil:
			return nil, fmt.Errorf("list journeys in %s: %w", dirs[1], err)
		case info.Mode()&fs.ModeSymlink != 0:
			return nil, fmt.Errorf("%s is a symbolic link, and Semar reads journeys only from real directories: replace it with a directory", d)
		case !info.IsDir():
			return nil, fmt.Errorf("%s is not a directory, and Semar reads journeys from a directory there", d)
		}
		last = info
	}
	root, err := os.OpenRoot(dirs[1])
	if err != nil {
		return nil, fmt.Errorf("list journeys in %s: %w", dirs[1], err)
	}
	// The directory must be the one that was checked, not one that replaced it since.
	opened, err := root.Lstat(".")
	if err != nil || !os.SameFile(last, opened) {
		root.Close()
		return nil, fmt.Errorf("%s changed while it was read", dirs[1])
	}
	return root, nil
}

// list returns the entries of the directory by name, and an error for more than 200 of them.
func list(root *os.Root) ([]fs.DirEntry, error) {
	d, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("list journeys in %s: %w", root.Name(), err)
	}
	defer d.Close()
	entries, err := d.ReadDir(maxFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("list journeys in %s: %w", root.Name(), err)
	}
	if len(entries) > maxFiles {
		return nil, fmt.Errorf("%s holds more than %d files: Semar reads at most %d journeys", root.Name(), maxFiles, maxFiles)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

// readFile reads one file of the directory. A link, a file that is not regular, and a file that was
// swapped between the check and the open are errors. A file over 256 KiB is reported as big, and
// is not read.
func readFile(root *os.Root, name string) (data []byte, big bool, err error) {
	rel := relDir + "/" + name
	info, err := root.Lstat(name)
	switch {
	case err != nil:
		return nil, false, fmt.Errorf("read journey %s: %w", rel, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, false, fmt.Errorf("%s is a symbolic link, and Semar reads journeys only from real files: replace it with the file", rel)
	case !info.Mode().IsRegular():
		return nil, false, fmt.Errorf("%s is not a regular file", rel)
	case info.Size() > MaxFileSize:
		return nil, true, nil
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, false, fmt.Errorf("read journey %s: %w", rel, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("read journey %s: %w", rel, err)
	}
	if !os.SameFile(info, opened) {
		return nil, false, fmt.Errorf("%s changed while it was read", rel)
	}
	data, err = io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, false, fmt.Errorf("read journey %s: %w", rel, err)
	}
	return data, len(data) > MaxFileSize, nil
}
