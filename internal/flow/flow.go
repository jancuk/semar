// Package flow reads, checks, writes, and runs the reusable flows of a repository. A flow is a short
// sequence of device steps, such as a sign-in, in .semar/flows/<id>.yaml. A scenario script runs it with
// a step that has use: <id>. A flow is android only in this version.
//
// A flow file is untrusted text, as a screens file is. It is read once, through real directories and
// within fixed limits.
package flow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/datevar"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/yamlfile"
)

const (
	// RelDir is how a message names the directory.
	RelDir = ".semar/flows"
	// MaxFileSize is the most that one flow file may hold.
	MaxFileSize = 128 << 10
	// MaxSteps is the most steps that one flow may have.
	MaxSteps  = 50
	maxParams = 20
	// MaxDepth is the most flows that nest: a flow, the flow it uses, and the flow that one uses.
	MaxDepth = 3

	apiVersion = "semar/v1"
	kind       = "Flow"
	fileMode   = 0o644
)

// ErrNotFound is the error of a flow file that is not there.
var ErrNotFound = errors.New("the flow file was not found")

var (
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	stepPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

// Path is the file of a flow, relative to the repository.
func Path(id string) string { return RelDir + "/" + id + ".yaml" }

// CheckID reports why id cannot be the ID of a flow.
func CheckID(id string) error {
	if err := idcheck.Check("flow ID", id); err != nil {
		return err
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid flow ID %q: use letters, digits, underscore, or hyphen, starting with a letter or digit", id)
	}
	return nil
}

// Digest is the sha256 of the bytes of a flow file, as "sha256:<hex>". A run records it, so the
// version of a flow that ran is known when the file changes.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Problem is one reason a flow cannot be used. Path is the place in the file, such as steps[1].mobile.
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

// Parse decodes and checks the content of one flow file. name is what an error calls the file and id
// is the ID that the file name gives, or "" when it is not known. The error holds every problem, one
// per line.
func Parse(data []byte, name, id string) (domain.Flow, error) {
	if len(data) > MaxFileSize {
		return domain.Flow{}, fmt.Errorf("%s: the file is larger than %d KiB", name, MaxFileSize>>10)
	}
	var f domain.Flow
	if err := yamlfile.Decode(data, name, &f); err != nil {
		return domain.Flow{}, err
	}
	if ps := Validate(f, id); len(ps) > 0 {
		lines := make([]string, len(ps))
		for i, p := range ps {
			lines[i] = name + ": " + p.String()
		}
		return domain.Flow{}, errors.New(strings.Join(lines, "\n"))
	}
	return f, nil
}

// Validate returns every reason the flow cannot be used, in plain words. The variables of a step are
// checked in a closed scope: only the params of the flow, the captures of its earlier steps, run.id,
// and the date variables may be used. A step that uses another flow is checked only as far as it can
// be without that flow: ValidateWith looks into it.
func Validate(f domain.Flow, id string) []Problem { return validate(f, id, nil) }

// Resolver gives the flow of an ID, or an error that says why there is none.
type Resolver func(id string) (domain.Flow, error)

// ValidateWith is Validate with the flows that the steps use: each flow that a use step names must
// exist, with must name its params and give the required ones, a flow must not use itself through
// others, and flows nest at most MaxDepth deep. What a used flow captures is known to the steps after it.
func ValidateWith(f domain.Flow, id string, resolve Resolver) []Problem {
	ps := validate(f, id, resolve)
	if resolve == nil {
		return ps
	}
	var walk func(cur domain.Flow, path []string)
	reported := map[string]bool{}
	walk = func(cur domain.Flow, path []string) {
		for i, st := range cur.Steps {
			if st.Use == "" {
				continue
			}
			where := ""
			if len(path) == 1 {
				where = fmt.Sprintf("steps[%d]", i)
			}
			if j := slices.Index(path, st.Use); j >= 0 {
				chain := slices.Clone(path[j:])
				text := "flow " + chain[0]
				for _, c := range chain[1:] {
					text += " uses " + c + ", which"
				}
				text += " uses " + st.Use + ": a flow cannot use itself"
				if !reported[text] {
					reported[text] = true
					ps = append(ps, Problem{Path: where, Message: text})
				}
				continue
			}
			if len(path) >= MaxDepth {
				text := fmt.Sprintf("flows are nested more than %d deep: %s > %s", MaxDepth, strings.Join(path, " > "), st.Use)
				if !reported[text] {
					reported[text] = true
					ps = append(ps, Problem{Path: where, Message: text})
				}
				continue
			}
			inner, err := resolve(st.Use)
			if err != nil {
				continue // validate reported it, or it is below the first level
			}
			walk(inner, append(slices.Clone(path), st.Use))
		}
	}
	walk(f, []string{f.ID})
	return ps
}

// usedCaptures lists the names that a flow and the flows inside it capture, in order.
func usedCaptures(f domain.Flow, resolve Resolver, depth int) []string {
	var out []string
	if depth > MaxDepth+1 {
		return nil
	}
	for _, st := range f.Steps {
		if st.Use != "" && resolve != nil {
			if inner, err := resolve(st.Use); err == nil {
				out = append(out, usedCaptures(inner, resolve, depth+1)...)
			}
			continue
		}
		for _, c := range st.Captures {
			out = append(out, c.Name)
		}
	}
	return out
}

func validate(f domain.Flow, id string, resolve Resolver) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if f.APIVersion != apiVersion {
		add("apiVersion", "apiVersion must be %s", apiVersion)
	}
	if f.Kind != kind {
		add("kind", "kind must be %s", kind)
	}
	if err := CheckID(f.ID); err != nil {
		add("id", "%v", err)
	} else if id != "" && f.ID != id {
		add("id", "id %s does not match the file name %s.yaml", f.ID, id)
	}
	if f.Surface != domain.SurfaceAndroid {
		add("surface", "surface must be android: a flow is android only in this version")
	}
	if len(f.Params) > maxParams {
		add("params", "%d params; at most %d", len(f.Params), maxParams)
	}
	scope := map[string]bool{}
	for i, p := range f.Params {
		where := fmt.Sprintf("params[%d]", i)
		switch {
		case !namePattern.MatchString(p.Name):
			add(where, "the param name %q must start with a letter and hold letters, digits, and _", p.Name)
		case scope[p.Name]:
			add(where, "param %s is defined more than once", p.Name)
		}
		scope[p.Name] = true
		if p.Default != nil && strings.Contains(*p.Default, "{{") {
			add(where+".default", "a default is plain text and holds no variable")
		}
	}
	switch n := len(f.Steps); {
	case n == 0:
		add("steps", "a flow needs at least one step")
	case n > MaxSteps:
		add("steps", "%d steps; at most %d", n, MaxSteps)
	}
	names := map[string]bool{}
	open := false // a step before this one uses a flow that is not looked into, so it may capture anything
	for i, st := range f.Steps {
		where := fmt.Sprintf("steps[%d]", i)
		switch {
		case !stepPattern.MatchString(string(st.Step)):
			add(where+".step", "the step name %q must start with a letter or a digit and hold letters, digits, - and _", st.Step)
		case names[string(st.Step)]:
			add(where+".step", "step %s is defined more than once", st.Step)
		}
		names[string(st.Step)] = true
		switch {
		case st.Use != "":
			if err := CheckID(st.Use); err != nil {
				add(where+".use", "%v", err)
			}
			if st.Request != nil || st.Web != nil || st.Mobile != nil {
				add(where, "a step that uses a flow has no action of its own: use, or a mobile action, and not both")
			}
			if len(st.Captures) > 0 {
				add(where+".captures", "a step that uses a flow takes no captures: the flow captures what it needs")
			}
			if len(st.Expect) > 0 {
				add(where+".expect", "a step that uses a flow takes no checks: put them on a step after it")
			}
			for _, v := range script.WithVariables(st.With) {
				if msg := unknown(v, scope, open); msg != "" {
					add(where+".with", "%s", msg)
				}
			}
			if resolve == nil {
				open = true
				break
			}
			inner, err := resolve(st.Use)
			if err != nil {
				add(where+".use", "flow %s: %v", st.Use, err)
				open = true
				break
			}
			for _, k := range sortedKeys(st.With) {
				if _, ok := inner.Param(k); !ok {
					add(where+".with", "flow %s has no param %s; its params are %s", inner.ID, k, paramNames(inner))
				}
			}
			for _, p := range inner.Params {
				if _, given := st.With[p.Name]; !given && p.Required() {
					add(where+".with", "flow %s needs the param %s", inner.ID, p.Name)
				}
			}
			for _, name := range usedCaptures(inner, resolve, 1) {
				if scope[name] {
					add(where+".use", "flow %s captures %s, which is already a param or a capture of this flow", inner.ID, name)
				}
				scope[name] = true
			}
		case len(st.With) > 0:
			add(where, "with belongs to a step that has use")
		case st.Request != nil || st.Web != nil:
			add(where, "a flow step has a mobile action, and no request and no web action")
		case st.Mobile == nil:
			add(where, "a flow step needs a mobile action")
		}
		if st.Mobile != nil {
			m := *st.Mobile
			if m.Service == "" {
				m.Service = f.Service
			}
			if m.App == "" {
				m.App = f.App
			}
			if m.Service == "" {
				add(where+".mobile", "the step has no service: name one on the step or on the flow")
			}
			if m.App == "" {
				add(where+".mobile", "the step has no app: name the screens file on the step or on the flow")
			} else if err := idcheck.Check("app name", m.App); err != nil {
				add(where+".mobile.app", "%v", err)
			}
			for _, msg := range m.Problems() {
				add(where+".mobile", "%s", msg)
			}
			for _, v := range m.Variables() {
				if msg := unknown(v, scope, open); msg != "" {
					add(where+".mobile", "%s", msg)
				}
			}
		}
		for j, e := range st.Expect {
			ew := fmt.Sprintf("%s.expect[%d]", where, j)
			if st.Mobile == nil {
				if st.Use == "" {
					add(ew, "a check belongs to a step with a mobile action")
				}
				continue
			}
			ps = append(ps, expectProblems(ew, e)...)
			for _, v := range domain.OperandVariables(e.Check) {
				if msg := unknown(v, scope, open); msg != "" {
					add(ew+".check", "%s", msg)
				}
			}
		}
		seen := map[string]bool{}
		for j, c := range st.Captures {
			cw := fmt.Sprintf("%s.captures[%d]", where, j)
			switch {
			case !namePattern.MatchString(c.Name):
				add(cw, "capture name %q is not a valid variable name", c.Name)
			case scope[c.Name] || seen[c.Name]:
				add(cw, "capture %s is defined more than once, or is the name of a param", c.Name)
			}
			switch {
			case c.JSONPath != "":
				add(cw, "a flow step has no response to read: capture an element")
			case c.Element == "":
				add(cw, "capture %s needs an element", c.Name)
			case st.Mobile == nil:
			case st.Mobile.Do == domain.MobileLaunch || st.Mobile.Do == domain.MobileOpenLink || st.Mobile.Do == domain.MobilePress:
				add(cw, "capture %s reads an element of the screen of the step, and %s has no screen", c.Name, st.Mobile.Do)
			}
			if err := domain.CheckPattern(c.Pattern); err != nil {
				add(cw+".pattern", "capture %s: %v", c.Name, err)
			}
			seen[c.Name] = true
		}
		for name := range seen {
			scope[name] = true
		}
	}
	return ps
}

// expectProblems checks one check of a flow step: the source is person, and the check reads an element.
func expectProblems(path string, e domain.Expectation) []Problem {
	var ps []Problem
	if e.Source != domain.SourcePerson {
		ps = append(ps, Problem{Path: path + ".source", Message: "the source of a check of a flow must be person"})
	}
	c := e.Check
	if c.Element == "" || c.SubjectCount() != 1 {
		ps = append(ps, Problem{Path: path + ".check", Message: "a check of a flow reads an element: name an element, and no status, jsonpath, header, or page"})
	}
	// scenario.Validate holds the rules of a check. Its steps are not the flow's, so only the path of
	// this expectation is kept.
	probe := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "flow-check", Ticket: "flow", Surface: domain.SurfaceAndroid,
		Steps: []domain.Step{{ID: "check", Expect: []domain.Expectation{e}}},
	}
	for _, p := range scenario.Validate(probe, scenario.FromFile) {
		if rest, ok := strings.CutPrefix(p.Path, "steps[0].expect[0]"); ok && !strings.HasSuffix(p.Path, ".source") && !(rest == ".check" && p.Message == "a check needs exactly one of status, jsonpath, header, element, page") {
			ps = append(ps, Problem{Path: path + rest, Message: p.Message})
		}
	}
	return ps
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func paramNames(f domain.Flow) string {
	if len(f.Params) == 0 {
		return "none"
	}
	names := make([]string, len(f.Params))
	for i, p := range f.Params {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}

// unknown gives the problem of a variable in a flow step, or "" when the flow may use it. open says
// that a flow that is not looked into may have captured the name.
func unknown(name string, scope map[string]bool, open bool) string {
	if name == "run.id" || scope[name] || datevar.Is(name) || (open && !strings.Contains(name, ".")) {
		return ""
	}
	if strings.Contains(name, ".") {
		return fmt.Sprintf("{{%s}} is not known in a flow: a flow uses only its params, its own earlier captures, run.id, and the date variables", name)
	}
	return fmt.Sprintf("{{%s}} is not a param of the flow or a capture of an earlier step of the flow: a flow cannot see the variables of the scenario, so give it a param", name)
}

// Steps gives the steps of the flow as concrete script steps: a step that names no service or app has
// those of the flow. The mobile actions, the captures, and the checks are copies, and the with of a use step is the one of f.
func Steps(f domain.Flow) []domain.ScriptStep {
	out := make([]domain.ScriptStep, len(f.Steps))
	for i, st := range f.Steps {
		cp := st
		if st.Mobile != nil {
			m := *st.Mobile
			if m.Service == "" {
				m.Service = f.Service
			}
			if m.App == "" {
				m.App = f.App
			}
			cp.Mobile = &m
		}
		cp.Captures = slices.Clone(st.Captures)
		cp.Expect = slices.Clone(st.Expect)
		out[i] = cp
	}
	return out
}

// Marshal writes the flow as block YAML that Parse reads back to the same flow. An empty apiVersion or
// kind is written as the value that Parse requires. It refuses a flow that Validate refuses, and one
// that no YAML text can carry through Parse.
func Marshal(f domain.Flow) ([]byte, error) {
	if f.APIVersion == "" {
		f.APIVersion = apiVersion
	}
	if f.Kind == "" {
		f.Kind = kind
	}
	if ps := Validate(f, f.ID); len(ps) > 0 {
		return nil, fmt.Errorf("flow %s cannot be written: %s", f.ID, ps[0])
	}
	var buf bytes.Buffer
	w := yaml.NewEncoder(&buf)
	w.SetIndent(2)
	if err := w.Encode(f); err != nil {
		return nil, fmt.Errorf("write flow %s: %w", f.ID, err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("write flow %s: %w", f.ID, err)
	}
	got, err := Parse(buf.Bytes(), "flow "+f.ID, f.ID)
	if err != nil {
		return nil, fmt.Errorf("flow %s cannot be written as YAML that Parse accepts: %w", f.ID, err)
	}
	if !reflect.DeepEqual(normal(got), normal(f)) {
		return nil, fmt.Errorf("flow %s cannot be written: Parse would read it back as a different flow", f.ID)
	}
	return buf.Bytes(), nil
}

// normal makes the empty collections of a flow nil, as Parse reads them.
func normal(f domain.Flow) domain.Flow {
	if len(f.Params) == 0 {
		f.Params = nil
	}
	steps := make([]domain.ScriptStep, len(f.Steps))
	for i, st := range f.Steps {
		if len(st.Captures) == 0 {
			st.Captures = nil
		}
		if len(st.With) == 0 {
			st.With = nil
		}
		if len(st.Expect) == 0 {
			st.Expect = nil
		}
		steps[i] = st
	}
	f.Steps = steps
	return f
}

// Entry is a flow with the digest of the file it was read from.
type Entry struct {
	Flow   domain.Flow
	Digest string
}

// Read gives the bytes of the flow file of an id, with the checks that make a repository's file safe to
// read: .semar and .semar/flows must be real directories, and the file a real file of limited size.
func Read(repo, id string) ([]byte, error) {
	if err := CheckID(id); err != nil {
		return nil, err
	}
	rel := Path(id)
	dir := filepath.Join(repo, ".semar", "flows")
	for _, d := range []string{filepath.Join(repo, ".semar"), dir} {
		info, err := os.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%s: %w", rel, ErrNotFound)
		case err != nil:
			return nil, fmt.Errorf("read flow %s: %w", rel, err)
		case info.Mode()&fs.ModeSymlink != 0:
			return nil, fmt.Errorf("%s is a symbolic link, and Semar reads flows only from real directories: replace it with a directory", d)
		case !info.IsDir():
			return nil, fmt.Errorf("%s is not a directory", d)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("read flow %s: %w", rel, err)
	}
	defer root.Close()
	name := id + ".yaml"
	info, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%s: %w", rel, ErrNotFound)
	case err != nil:
		return nil, fmt.Errorf("read flow %s: %w", rel, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("%s is a symbolic link, and Semar reads flows only from real files: replace it with the file", rel)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file", rel)
	case info.Size() > MaxFileSize:
		return nil, fmt.Errorf("%s: the file is larger than %d KiB", rel, MaxFileSize>>10)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("read flow %s: %w", rel, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s changed while it was read", rel)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read flow %s: %w", rel, err)
	}
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("%s: the file is larger than %d KiB", rel, MaxFileSize>>10)
	}
	return data, nil
}

// Load reads and checks the flow of an id in the repository. The error says the file is not there with
// ErrNotFound.
func Load(repo, id string) (Entry, error) {
	data, err := Read(repo, id)
	if err != nil {
		return Entry{}, err
	}
	f, err := Parse(data, Path(id), id)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Flow: f, Digest: Digest(data)}, nil
}

// IDs lists the flows that have a file in the repository, in order. It lists none when the directory
// is not there. A name that is not a flow ID is left out.
func IDs(repo string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(repo, ".semar", "flows"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok || CheckID(id) != nil {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

// LoadAll loads every flow of the repository, by ID. A file that cannot be used is an error that names
// it, and the flows that could be read are still returned.
func LoadAll(repo string) (map[string]Entry, error) {
	ids, err := IDs(repo)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	out := make(map[string]Entry, len(ids))
	var errs []error
	for _, id := range ids {
		e, err := Load(repo, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out[id] = e
	}
	return out, errors.Join(errs...)
}

// SecretEnvNames lists the environment variables that the flows of the repository name as secrets,
// without repeats. A flow that cannot be read is left out: the check that loads it reports it. A file
// is read as Load reads it, but its problems do not stop the list, so a flow with a mistake still has
// its secrets kept from a harness.
func SecretEnvNames(repo string) []string {
	ids, _ := IDs(repo)
	var names []string
	for _, id := range ids {
		data, err := Read(repo, id)
		if err != nil {
			continue
		}
		var f domain.Flow
		if yamlfile.Decode(data, Path(id), &f) != nil {
			continue
		}
		for _, st := range f.Steps {
			if st.Mobile == nil {
				continue
			}
			if name, ok := strings.CutPrefix(st.Mobile.Secret, "env:"); ok && name != "" && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// Source loads flows of one repository when they are asked for, and keeps what it loaded. It is safe for
// concurrent use. It is the flow source of the replay and the resolver of the script check.
type Source struct {
	repo string
	mu   sync.Mutex
	raw  map[string]loaded // flows as Parse reads them
	got  map[string]loaded // flows that also fit the flows they use
}

type loaded struct {
	e   Entry
	err error
}

// Loader gives the source of the flows of a repository.
func Loader(repo string) *Source {
	return &Source{repo: repo, raw: map[string]loaded{}, got: map[string]loaded{}}
}

// Entry gives the flow and its digest. The error says the flow is not there with ErrNotFound. A flow
// that uses other flows is read together with them: it is refused when one is missing, when a with is
// wrong, when flows use each other in a circle, or when they nest too deep.
func (s *Source) Entry(id string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.got[id]; ok {
		return l.e, l.err
	}
	e, err := s.rawLocked(id)
	if err == nil {
		if ps := ValidateWith(e.Flow, id, s.resolveLocked); len(ps) > 0 {
			lines := make([]string, len(ps))
			for i, p := range ps {
				lines[i] = Path(id) + ": " + p.String()
			}
			err = errors.New(strings.Join(lines, "\n"))
			e = Entry{}
		}
	}
	s.got[id] = loaded{e, err}
	return e, err
}

func (s *Source) rawLocked(id string) (Entry, error) {
	if l, ok := s.raw[id]; ok {
		return l.e, l.err
	}
	e, err := Load(s.repo, id)
	s.raw[id] = loaded{e, err}
	return e, err
}

func (s *Source) resolveLocked(id string) (domain.Flow, error) {
	e, err := s.rawLocked(id)
	return e.Flow, err
}

// Flow gives the flow and its digest, as replay.FlowSource wants.
func (s *Source) Flow(id string) (domain.Flow, string, error) {
	e, err := s.Entry(id)
	return e.Flow, e.Digest, err
}

// Resolver is the script check's way to the flows: it gives a flow by its ID.
func (s *Source) Resolver() script.FlowResolver {
	return func(id string) (domain.Flow, error) {
		f, _, err := s.Flow(id)
		return f, err
	}
}

// Saved is a flow that Write stored. It can undo that one write.
type Saved struct {
	Path   string
	Digest string

	repo string
	prev []byte
	had  bool
	done bool
	mu   sync.Mutex
}

// Write stores the flow in .semar/flows/<id>.yaml, atomically, and never through a symbolic link. A
// flow of that ID that is there is replaced, and Saved can put it back.
func Write(repo string, f domain.Flow) (*Saved, error) {
	data, err := Marshal(f)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, fmt.Errorf("write flow %s: %w", f.ID, err)
	}
	defer root.Close()
	if _, err := atomicfile.MkdirIn(root, RelDir); err != nil {
		return nil, err
	}
	rel := Path(f.ID)
	s := &Saved{Path: rel, Digest: Digest(data), repo: repo}
	switch prev, err := readIn(root, rel); {
	case err == nil:
		s.prev, s.had = prev, true
	case errors.Is(err, fs.ErrNotExist):
	default:
		return nil, err
	}
	if err := atomicfile.WriteIn(root, rel, data, fileMode); err != nil {
		return nil, err
	}
	return s, nil
}

// Undo puts back what Write replaced, or removes the file when there was none. It runs once, and it
// refuses when the file is no longer the one that Write stored, so a later edit is never lost.
func (s *Saved) Undo() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return fmt.Errorf("%s was already undone", s.Path)
	}
	s.done = true
	root, err := os.OpenRoot(s.repo)
	if err != nil {
		return fmt.Errorf("undo %s: %w", s.Path, err)
	}
	defer root.Close()
	cur, err := readIn(root, s.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("undo %s: the file is gone", s.Path)
	case err != nil:
		return err
	case Digest(cur) != s.Digest:
		return fmt.Errorf("undo %s: the file changed since it was saved, so it is left as it is", s.Path)
	}
	if !s.had {
		if err := root.Remove(s.Path); err != nil {
			return fmt.Errorf("undo %s: %w", s.Path, err)
		}
		return nil
	}
	return atomicfile.WriteIn(root, s.Path, s.prev, fileMode)
}

// readIn reads a regular file of limited size below root, and refuses a link.
func readIn(root *os.Root, rel string) ([]byte, error) {
	info, err := root.Lstat(rel)
	switch {
	case err != nil:
		return nil, err
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file, and Semar replaces only real files", filepath.Join(root.Name(), rel))
	case info.Size() > MaxFileSize:
		return nil, fmt.Errorf("%s is larger than %d KiB", filepath.Join(root.Name(), rel), MaxFileSize>>10)
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, MaxFileSize+1))
}
