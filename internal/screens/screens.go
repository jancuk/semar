// Package screens reads the screen objects of a repository: .semar/screens/<app>.yaml says how to
// find each element of each screen of an app. The file is untrusted text. It is read once, through
// real directories and within fixed limits.
package screens

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
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/xpathlite"
	"github.com/jancuk/semar/internal/yamlfile"
)

const (
	// MaxFileSize is the most that one screens file may hold.
	MaxFileSize = 256 << 10
	maxScreens  = 100
	maxElements = 500
	// maxIndex is the greatest index of a locator.
	maxIndex = 200
	// RelDir is how a message names the directory.
	RelDir = ".semar/screens"
)

// ErrNotFound is the error of a screens file that is not there.
var ErrNotFound = errors.New("the screens file was not found")

// Path is the file of an app, relative to the repository.
func Path(app string) string { return RelDir + "/" + app + ".yaml" }

// Problem is one reason a screens file cannot be used. Path is the place in the file, such as
// screens.Board.card.within.
type Problem struct {
	Path    string
	Message string
}

func (p Problem) String() string {
	if p.Path == "" {
		return p.Message
	}
	return p.Path + ": " + p.Message
}

// Parse decodes and checks the content of one screens file. name is what an error calls the file and
// app is the app the file is for: the file name without .yaml. The error holds every problem, one per line.
func Parse(data []byte, name, app string) (domain.Screens, error) {
	if len(data) > MaxFileSize {
		return domain.Screens{}, fmt.Errorf("%s: the file is larger than 256 KiB", name)
	}
	var s domain.Screens
	if err := yamlfile.Decode(data, name, &s); err != nil {
		return domain.Screens{}, err
	}
	if ps := Validate(s, app); len(ps) > 0 {
		lines := make([]string, len(ps))
		for i, p := range ps {
			lines[i] = name + ": " + p.String()
		}
		return domain.Screens{}, errors.New(strings.Join(lines, "\n"))
	}
	return s, nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Validate returns every reason the content cannot be used, in plain words. app is the app that the
// file name gives, or "" when it is not known.
func Validate(s domain.Screens, app string) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if s.APIVersion != "semar/v1" {
		add("apiVersion", "apiVersion must be semar/v1")
	}
	if s.Kind != "Screens" {
		add("kind", "kind must be Screens")
	}
	if err := idcheck.Check("app name", s.App); err != nil {
		add("app", "%v", err)
	} else if app != "" && s.App != app {
		add("app", "app %s does not match the file name %s.yaml", s.App, app)
	}
	switch n := len(s.Screens); {
	case n == 0 && len(s.Common) == 0:
		add("screens", "a screens file needs at least one screen")
	case n > maxScreens:
		add("screens", "%d screens; at most %d", n, maxScreens)
	}
	total := len(s.Common)
	for _, en := range slices.Sorted(maps.Keys(s.Common)) {
		where := "common." + en
		if !namePattern.MatchString(en) {
			add(where, "the element name %q must start with a letter or a digit and hold letters, digits, - and _", en)
		}
		ps = append(ps, locatorProblems(where, s.Common[en], s.Common, "common elements")...)
	}
	ps = append(ps, withinCycles("common", s.Common, nil)...)
	for _, sn := range s.ScreenNames() {
		if !namePattern.MatchString(sn) {
			add("screens."+sn, "the screen name %q must start with a letter or a digit and hold letters, digits, - and _", sn)
		}
		elements := s.Screens[sn]
		if len(elements) == 0 {
			add("screens."+sn, "screen %s has no element", sn)
		}
		total += len(elements)
		for _, en := range s.ElementNames(sn) {
			where := "screens." + sn + "." + en
			if !namePattern.MatchString(en) {
				add(where, "the element name %q must start with a letter or a digit and hold letters, digits, - and _", en)
			}
			siblings := "this screen"
			if len(s.Common) > 0 {
				siblings = "this screen or the common elements"
			}
			ps = append(ps, locatorProblems(where, elements[en], elementsWithCommon(elements, s.Common), siblings)...)
			// A common element that another common element is inside is found in common only: a screen
			// element of the same name would be a second element behind one name.
			if _, common := s.Common[en]; common {
				if inner := commonInside(s.Common, en); inner != "" {
					add(where, "the element %s has the name of the common element that common.%s is inside: rename it", en, inner)
				}
			}
		}
		ps = append(ps, withinCycles("screens."+sn, elements, s.Common)...)
	}
	if total > maxElements {
		add("screens", "%d elements; at most %d", total, maxElements)
	}
	return ps
}

// commonInside gives the name of a common element whose within is name, or "".
func commonInside(common map[string]domain.Locator, name string) string {
	for _, k := range slices.Sorted(maps.Keys(common)) {
		if common[k].Within == name {
			return k
		}
	}
	return ""
}

// elementsWithCommon is what a screen can find by name: its own elements, and the common ones that it
// does not shadow.
func elementsWithCommon(own, common map[string]domain.Locator) map[string]domain.Locator {
	if len(common) == 0 {
		return own
	}
	out := make(map[string]domain.Locator, len(own)+len(common))
	maps.Copy(out, common)
	maps.Copy(out, own)
	return out
}

// locatorProblems checks one locator. siblings are the elements that its within may name, and
// siblingsName says in plain words where they are.
func locatorProblems(where string, l domain.Locator, siblings map[string]domain.Locator, siblingsName string) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if l.Empty() {
		if l.HasText != "" {
			add(where, "hasText only filters the matches of another field: give the element id, desc, role, name, text, xpath, or class as well")
		} else {
			add(where, "the element has no way to be found: give it testId, role, name, text, css, id, desc, class, or xpath")
		}
	}
	if l.Index < 0 || l.Index > maxIndex {
		add(where+".index", "index must be a number from 1 to %d", maxIndex)
	}
	for field, text := range l.Fields() {
		if strings.Contains(variable.ReplaceAllString(text, ""), "{{") {
			add(where+"."+field, "%s holds a malformed variable: write {{name}}", field)
		}
		if strings.ContainsFunc(text, isControl) {
			add(where+"."+field, "%s holds a control character", field)
		}
	}
	if l.XPath != "" && !strings.Contains(l.XPath, "{{") {
		if err := xpathlite.Validate(l.XPath); err != nil {
			add(where+".xpath", "%v", err)
		}
	}
	if l.Within != "" {
		if _, ok := siblings[l.Within]; !ok {
			add(where+".within", "within names %s, which is not an element of %s", l.Within, siblingsName)
		}
	}
	return ps
}

var variable = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*)\}\}`)

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// withinCycles reports an element that is, through within, inside itself. prefix is the place of the
// elements in the file. A within that a screen does not answer itself is looked up in common.
func withinCycles(prefix string, own, common map[string]domain.Locator) []Problem {
	var ps []Problem
	reported := map[string]bool{}
	elements := elementsWithCommon(own, common)
	for name := range own {
		seen := []string{name}
		for cur := elements[name].Within; cur != ""; cur = elements[cur].Within {
			if _, ok := elements[cur]; !ok {
				break
			}
			if slices.Contains(seen, cur) {
				key := cur
				// A cycle of common elements is reported once, under common.
				if _, mine := own[cur]; mine && !reported[key] {
					reported[key] = true
					ps = append(ps, Problem{Path: prefix + "." + cur + ".within", Message: "within leads back to the element itself: " + strings.Join(append(seen, cur), " -> ")})
				}
				break
			}
			seen = append(seen, cur)
		}
	}
	slices.SortFunc(ps, func(a, b Problem) int { return strings.Compare(a.Path, b.Path) })
	return ps
}

// Load reads and checks the screens file of an app in the repository. The error says the file is
// not there with ErrNotFound. The repository is untrusted: .semar and .semar/screens must be real
// directories and the file a real file.
func Load(repo, app string) (domain.Screens, error) {
	data, err := read(repo, app)
	if err != nil {
		return domain.Screens{}, err
	}
	return Parse(data, Path(app), app)
}

// Read gives the bytes of the screens file of an app, with the same checks as Load.
func Read(repo, app string) ([]byte, error) { return read(repo, app) }

func read(repo, app string) ([]byte, error) {
	if err := idcheck.Check("app name", app); err != nil {
		return nil, err
	}
	rel := Path(app)
	dir := filepath.Join(repo, ".semar", "screens")
	for _, d := range []string{filepath.Join(repo, ".semar"), dir} {
		info, err := os.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%s: %w", rel, ErrNotFound)
		case err != nil:
			return nil, fmt.Errorf("read screens %s: %w", rel, err)
		case info.Mode()&fs.ModeSymlink != 0:
			return nil, fmt.Errorf("%s is a symbolic link, and Semar reads screens only from real directories: replace it with a directory", d)
		case !info.IsDir():
			return nil, fmt.Errorf("%s is not a directory", d)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("read screens %s: %w", rel, err)
	}
	defer root.Close()
	name := app + ".yaml"
	info, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%s: %w", rel, ErrNotFound)
	case err != nil:
		return nil, fmt.Errorf("read screens %s: %w", rel, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("%s is a symbolic link, and Semar reads screens only from real files: replace it with the file", rel)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file", rel)
	case info.Size() > MaxFileSize:
		return nil, fmt.Errorf("%s: the file is larger than 256 KiB", rel)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("read screens %s: %w", rel, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s changed while it was read", rel)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read screens %s: %w", rel, err)
	}
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("%s: the file is larger than 256 KiB", rel)
	}
	return data, nil
}

// List gives the apps that have a screens file in the repository, in order. It returns none when the
// directory is not there.
func List(repo string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(repo, ".semar", "screens"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var apps []string
	for _, e := range entries {
		if app, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && e.Type().IsRegular() {
			apps = append(apps, app)
		}
	}
	slices.Sort(apps)
	return apps, nil
}
