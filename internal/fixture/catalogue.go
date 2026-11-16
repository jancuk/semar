package fixture

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

const (
	maxFiles = 200
	// relDir is how a problem names the directory.
	relDir = ".semar/fixtures"
)

// Catalogue is every fixture file of a repository, valid or not.
type Catalogue struct {
	Fixtures []domain.Fixture // valid ones, sorted by ID
	Problems []Problem        // one per problem of an invalid file, sorted by file
}

// Get returns the valid fixture with this ID.
func (c Catalogue) Get(id domain.FixtureID) (domain.Fixture, bool) {
	i, found := slices.BinarySearchFunc(c.Fixtures, id, func(f domain.Fixture, id domain.FixtureID) int {
		return strings.Compare(string(f.ID), string(id))
	})
	if !found {
		return domain.Fixture{}, false
	}
	return c.Fixtures[i], true
}

// Load reads .semar/fixtures of the repository. A missing directory is an empty catalogue. The
// repository is untrusted: .semar and .semar/fixtures must be real directories, and a link or a
// special file below them is an error and not skipped, because a skipped fixture would let a plan
// reuse its ID. A file that cannot be used is a Problem, and the valid files stay.
//
// services is the list of services of every environment of the configuration, for rule F16. With
// nil, F16 is not checked.
func Load(repo string, services []string) (Catalogue, error) {
	root, err := openDir(repo)
	if err != nil || root == nil {
		return Catalogue{}, err
	}
	defer root.Close()

	entries, err := list(root)
	if err != nil {
		return Catalogue{}, err
	}
	var c Catalogue
	stems := map[string][]string{}
	var names []string
	for _, e := range entries {
		name := e.Name()
		rel := relDir + "/" + name
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			return Catalogue{}, fmt.Errorf("%s is a symbolic link, and Semar reads fixtures only from real files: replace it with the file", rel)
		case e.IsDir():
			c.Problems = append(c.Problems, Problem{File: rel, Message: "fixtures must be files directly in " + relDir})
			continue
		case !e.Type().IsRegular():
			return Catalogue{}, fmt.Errorf("%s is not a regular file (%s), and %s may hold only fixture files", rel, e.Type(), relDir)
		}
		if ext := filepath.Ext(name); ext == ".yaml" || ext == ".yml" {
			stem := strings.TrimSuffix(name, ext)
			stems[stem] = append(stems[stem], name)
			names = append(names, name)
		}
	}
	for _, name := range names {
		rel := relDir + "/" + name
		if same := stems[strings.TrimSuffix(name, filepath.Ext(name))]; len(same) > 1 {
			c.Problems = append(c.Problems, Problem{File: rel, Message: fmt.Sprintf("fixture %s has two files: %s", strings.TrimSuffix(name, filepath.Ext(name)), strings.Join(same, " and "))})
			continue
		}
		data, big, err := readFile(root, name)
		if err != nil {
			return Catalogue{}, err
		}
		if big {
			c.Problems = append(c.Problems, Problem{File: rel, Message: tooLarge})
			continue
		}
		f, ps := decode(data, rel, services)
		if len(ps) > 0 {
			c.Problems = append(c.Problems, ps...)
			continue
		}
		c.Fixtures = append(c.Fixtures, f)
	}
	slices.SortFunc(c.Fixtures, func(a, b domain.Fixture) int { return strings.Compare(string(a.ID), string(b.ID)) })
	slices.SortStableFunc(c.Problems, func(a, b Problem) int { return strings.Compare(a.File, b.File) })
	return c, nil
}

// LoadOne reads the one fixture file with this ID, for a person who is about to approve it. The ID
// comes from the command line, so it must match the ID pattern before it names a file. An unusable
// file is an error that lists every problem, one per line.
func LoadOne(repo string, id domain.FixtureID, services []string) (domain.Fixture, error) {
	if !idPattern.MatchString(string(id)) || len(id) > maxIDBytes {
		return domain.Fixture{}, fmt.Errorf("invalid fixture ID %q: use lower-case letters, digits, hyphen, and underscore in parts that a dot separates, starting with a letter, at most %d bytes", id, maxIDBytes)
	}
	notFound := fmt.Errorf("fixture %s was not found: no file %s.yaml or %s.yml in %s", id, id, id, filepath.Join(repo, ".semar", "fixtures"))
	root, err := openDir(repo)
	if err != nil {
		return domain.Fixture{}, err
	}
	if root == nil {
		return domain.Fixture{}, notFound
	}
	defer root.Close()

	var found []string
	for _, ext := range []string{".yaml", ".yml"} {
		name := string(id) + ext
		switch _, err := root.Lstat(name); {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return domain.Fixture{}, fmt.Errorf("find fixture %s: %w", id, err)
		default:
			found = append(found, name)
		}
	}
	switch len(found) {
	case 0:
		return domain.Fixture{}, notFound
	case 2:
		return domain.Fixture{}, fmt.Errorf("fixture %s has two files, %s and %s: keep one", id, found[0], found[1])
	}
	data, big, err := readFile(root, found[0])
	if err != nil {
		return domain.Fixture{}, err
	}
	rel := relDir + "/" + found[0]
	if big {
		return domain.Fixture{}, fmt.Errorf("%s", Problem{File: rel, Message: tooLarge})
	}
	f, ps := decode(data, rel, services)
	if len(ps) > 0 {
		lines := make([]string, len(ps))
		for i, p := range ps {
			lines[i] = p.String()
		}
		return domain.Fixture{}, errors.New(strings.Join(lines, "\n"))
	}
	return f, nil
}

// openDir opens .semar/fixtures as a root. It returns nil and no error when the directory is not
// there. Both directories must be real: a link would read files from outside the repository.
func openDir(repo string) (*os.Root, error) {
	dirs := []string{filepath.Join(repo, ".semar"), filepath.Join(repo, ".semar", "fixtures")}
	var last fs.FileInfo
	for _, d := range dirs {
		info, err := os.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, nil
		case err != nil:
			return nil, fmt.Errorf("list fixtures in %s: %w", dirs[1], err)
		case info.Mode()&fs.ModeSymlink != 0:
			return nil, fmt.Errorf("%s is a symbolic link, and Semar reads fixtures only from real directories: replace it with a directory", d)
		case !info.IsDir():
			return nil, fmt.Errorf("%s is not a directory, and Semar reads fixtures from a directory there", d)
		}
		last = info
	}
	root, err := os.OpenRoot(dirs[1])
	if err != nil {
		return nil, fmt.Errorf("list fixtures in %s: %w", dirs[1], err)
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
		return nil, fmt.Errorf("list fixtures in %s: %w", root.Name(), err)
	}
	defer d.Close()
	entries, err := d.ReadDir(maxFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("list fixtures in %s: %w", root.Name(), err)
	}
	if len(entries) > maxFiles {
		return nil, fmt.Errorf("%s holds more than %d files: Semar reads at most %d fixtures", root.Name(), maxFiles, maxFiles)
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
		return nil, false, fmt.Errorf("read fixture %s: %w", rel, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, false, fmt.Errorf("%s is a symbolic link, and Semar reads fixtures only from real files: replace it with the file", rel)
	case !info.Mode().IsRegular():
		return nil, false, fmt.Errorf("%s is not a regular file", rel)
	case info.Size() > MaxFileSize:
		return nil, true, nil
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, false, fmt.Errorf("read fixture %s: %w", rel, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("read fixture %s: %w", rel, err)
	}
	if !os.SameFile(info, opened) {
		return nil, false, fmt.Errorf("%s changed while it was read", rel)
	}
	data, err = io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, false, fmt.Errorf("read fixture %s: %w", rel, err)
	}
	return data, len(data) > MaxFileSize, nil
}

// decode parses and validates one file. rel is how a problem names it.
func decode(data []byte, rel string, services []string) (domain.Fixture, []Problem) {
	f, err := Parse(data, rel)
	if err != nil {
		return domain.Fixture{}, parseProblems(err, rel)
	}
	if ps := validate(f, rel, services); len(ps) > 0 {
		return domain.Fixture{}, ps
	}
	return f, nil
}

var linePrefix = regexp.MustCompile(`^(\d+): (.*)$`)

// parseProblems turns the error of Parse, one "<name>:<line>: <message>" per line, into problems.
func parseProblems(err error, rel string) []Problem {
	var ps []Problem
	for _, line := range strings.Split(err.Error(), "\n") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, rel+":"))
		p := Problem{File: rel, Message: rest}
		if m := linePrefix.FindStringSubmatch(rest); m != nil {
			p.Path, p.Message = "line "+m[1], m[2]
		}
		ps = append(ps, p)
	}
	return ps
}

// Entry is what the planner sees of one fixture.
type Entry struct {
	ID          domain.FixtureID
	Description string
	Params      []ParamEntry // sorted by name
	Outputs     []string
	Approved    bool
}

type ParamEntry struct {
	Name     string
	Required bool // no default: every given entry must set it in with
}

// Entries lists the valid fixtures for the planner, in ID order. approved says whether a person
// approved the fixture as it is now; with nil, none is.
func (c Catalogue) Entries(approved func(domain.Fixture) bool) []Entry {
	out := make([]Entry, len(c.Fixtures))
	for i, f := range c.Fixtures {
		e := Entry{ID: f.ID, Description: f.Description, Outputs: slices.Clone(f.Outputs)}
		for _, name := range slices.Sorted(maps.Keys(f.Params)) {
			e.Params = append(e.Params, ParamEntry{Name: name, Required: f.Params[name].Default == nil})
		}
		e.Approved = approved != nil && approved(f)
		out[i] = e
	}
	return out
}
