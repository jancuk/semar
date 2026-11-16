package fixture

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/script"
)

// Problem is one reason a fixture file cannot be used. File is the file as the caller names it,
// and Path is the place in the file, such as create[1].request.path. Either may be empty.
type Problem struct{ File, Path, Message string }

func (p Problem) String() string {
	var parts []string
	for _, s := range []string{p.File, p.Path, p.Message} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ": ")
}

const (
	maxIDBytes     = 64
	maxDescription = 500
	maxParams      = 16
	maxFind        = 8
	maxCreate      = 16
	maxCleanup     = 8
	maxOutputs     = 16
	maxUntil       = 15 * time.Minute
	minEvery       = 100 * time.Millisecond
	maxDataKey     = 128

	idRule      = `^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)*$`
	paramRule   = `^[a-z][a-z0-9_]*$`
	captureRule = `^[A-Za-z][A-Za-z0-9_]*$`
	dataKeyRule = `^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`
)

var (
	idPattern       = regexp.MustCompile(idRule)
	paramPattern    = regexp.MustCompile(paramRule)
	capturePattern  = regexp.MustCompile(captureRule)
	dataKeyPattern  = regexp.MustCompile(dataKeyRule)
	versionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	variablePattern = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*)\}\}`)
	blankLine       = regexp.MustCompile(`\n[ \t]*\n`)

	// credentialHeaders are added by the engine from the auth profile, or set by the client.
	credentialHeaders = []string{"Authorization", "Cookie", "Proxy-Authorization", "Host"}
)

// Validate checks rules F2 to F14 on a parsed fixture. With services set, it also checks F16: every
// step names one of them. With services nil, F16 is skipped; an empty list refuses every service.
// F1 and F15 are checked by Parse.
func Validate(f domain.Fixture, services []string) []Problem {
	return validate(f, "", services)
}

// validate is Validate for a fixture that came from file, as the caller names it. An empty file
// skips the check of the file name.
func validate(f domain.Fixture, file string, services []string) []Problem {
	v := &validator{file: file, services: services, f: f}
	v.header()
	v.params()
	if f.Lease != "" && f.Lease != domain.LeaseExclusive {
		v.add("lease", "lease must be exclusive or empty")
	}
	v.shape()
	v.steps()
	v.outputs()
	return v.ps
}

type validator struct {
	file     string
	services []string
	f        domain.Fixture
	ps       []Problem
	// captured holds, for each phase, every name that the steps of the phase capture
	captured map[string]map[string]bool
}

func (v *validator) add(where, format string, args ...any) {
	v.ps = append(v.ps, Problem{File: v.file, Path: where, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) header() {
	f := v.f
	if f.APIVersion != "semar/v1" {
		v.add("apiVersion", "apiVersion must be semar/v1")
	}
	if f.Kind != "Fixture" {
		v.add("kind", "kind must be Fixture")
	}
	switch {
	case !idPattern.MatchString(string(f.ID)):
		v.add("id", "id must match %s", idRule)
	case len(f.ID) > maxIDBytes:
		v.add("id", "id is longer than %d bytes", maxIDBytes)
	case v.file != "":
		base := path.Base(strings.ReplaceAll(v.file, `\`, "/"))
		if stem := strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml"); stem != string(f.ID) || stem == base {
			v.add("id", "id %s does not match the file name %s", f.ID, base)
		}
	}

	d := f.Description
	switch {
	case strings.TrimSpace(d) == "":
		v.add("description", "description is required")
	case !utf8.ValidString(d):
		v.add("description", "description is not valid UTF-8")
	case utf8.RuneCountInString(d) > maxDescription:
		v.add("description", "description has %d characters; at most %d", utf8.RuneCountInString(d), maxDescription)
	}
	// A line feed may fold a long description over lines. Any other control character may not.
	if strings.ContainsFunc(d, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) {
		v.add("description", "description holds a control character")
	}
	if blankLine.MatchString(strings.TrimSpace(d)) {
		v.add("description", "description must be one paragraph")
	}

	if !versionPattern.MatchString(f.Version) {
		v.add("version", "version %s must be MAJOR.MINOR.PATCH", f.Version)
	}
	if err := idcheck.Check("owner", f.Owner); err != nil {
		v.add("owner", "%v", err)
	}
}

func (v *validator) params() {
	if n := len(v.f.Params); n > maxParams {
		v.add("params", "%d parameters; at most %d", n, maxParams)
	}
	for _, name := range slices.Sorted(maps.Keys(v.f.Params)) {
		where := "params." + name
		switch {
		case name == "run" || name == "data":
			v.add(where, "the name %s is reserved", name)
		case !paramPattern.MatchString(name):
			v.add(where, "name must match %s", paramRule)
		}
		if d := v.f.Params[name].Default; d != nil {
			if msg := defaultProblem(*d); msg != "" {
				v.add(where+".default", "%s", msg)
			}
		}
	}
}

// defaultProblem is why a parameter default is not allowed. A default is a literal, a literal with
// {{run.id}}, or exactly one {{data.<key>}}.
func defaultProblem(d string) string {
	if strings.Contains(variablePattern.ReplaceAllString(d, ""), "{{") {
		return fmt.Sprintf("malformed variable in %q", d)
	}
	var others []string
	for _, m := range variablePattern.FindAllStringSubmatch(d, -1) {
		if m[1] != "run.id" {
			others = append(others, m[1])
		}
	}
	if len(others) == 0 {
		return ""
	}
	if key, ok := strings.CutPrefix(others[0], "data."); ok && len(others) == 1 && d == "{{"+others[0]+"}}" {
		if len(key) > maxDataKey || !dataKeyPattern.MatchString(key) {
			return fmt.Sprintf("data key %s must match %s and be at most %d bytes", key, dataKeyRule, maxDataKey)
		}
		return ""
	}
	return "a default is a literal, a literal with {{run.id}}, or one {{data.<key>}}"
}

func (v *validator) shape() {
	f := v.f
	if len(f.Find) == 0 && len(f.Create) == 0 {
		v.add("find", "a fixture needs find or create steps")
	}
	for _, p := range []struct {
		name  string
		n, at int
	}{{domain.PhaseFind, len(f.Find), maxFind}, {domain.PhaseCreate, len(f.Create), maxCreate}, {domain.PhaseCleanup, len(f.Cleanup), maxCleanup}} {
		if p.n > p.at {
			v.add(p.name, "%s has %d steps; at most %d", p.name, p.n, p.at)
		}
	}
}

func (v *validator) steps() {
	createNames := map[string]bool{} // a cleanup step may use what create captured
	for _, st := range v.f.Create {
		for _, c := range st.Captures {
			createNames[c.Name] = true
		}
	}
	v.captured = map[string]map[string]bool{}
	for _, phase := range []struct {
		name  string
		steps []domain.FixtureStep
	}{{domain.PhaseFind, v.f.Find}, {domain.PhaseCreate, v.f.Create}, {domain.PhaseCleanup, v.f.Cleanup}} {
		v.phase(phase.name, phase.steps, createNames)
	}
}

func (v *validator) phase(name string, steps []domain.FixtureStep, createNames map[string]bool) {
	done := map[string]bool{} // captures of the steps before the one in hand
	all := map[string]bool{}  // every capture of the phase, for the duplicate rule
	v.captured[name] = all
	for i, st := range steps {
		where := fmt.Sprintf("%s[%d]", name, i)
		known := func(n string) bool {
			if n == "run.id" || done[n] {
				return true
			}
			if _, ok := v.f.Params[n]; ok && !strings.Contains(n, ".") {
				return true
			}
			return name == domain.PhaseCleanup && createNames[n]
		}
		v.request(where, name, st.Request, known)
		v.captures(where, name, st.Captures, all)
		for _, c := range st.Captures {
			done[c.Name] = true
		}
		v.until(where, st)
	}
}

const unknownVariable = " is not a known variable"

func (v *validator) request(where, phase string, r domain.APIRequest, known func(string) bool) {
	for _, m := range script.RequestProblems(r, known) {
		if before, ok := strings.CutSuffix(m, "}}"+unknownVariable); ok {
			if i := strings.LastIndex(before, ": {{"); i >= 0 {
				name := before[i+len(": {{"):]
				v.add(where+".request."+before[:i], "%s", variableProblem(name, phase))
				continue
			}
		}
		v.add(where+".request", "%s", m)
	}
	for _, name := range slices.Sorted(maps.Keys(r.Headers)) {
		if slices.ContainsFunc(credentialHeaders, func(h string) bool { return strings.EqualFold(h, name) }) {
			v.add(where+".request", "header %s is added by Semar from the service's auth profile and cannot be in a fixture", name)
		}
	}
	if v.services != nil && r.Service != "" && !slices.Contains(v.services, r.Service) {
		v.add(where+".request.service", "%s is not a service of any environment", r.Service)
	}
}

func variableProblem(name, phase string) string {
	head, _, _ := strings.Cut(name, ".")
	switch head {
	case "secret":
		return fmt.Sprintf("{{%s}} cannot be used in a fixture: the session comes from the auth profile of the service", name)
	case "data":
		return fmt.Sprintf("{{%s}} cannot be used in a request: put it in the default of a parameter", name)
	}
	from := "an earlier " + phase + " step"
	if phase == domain.PhaseCleanup {
		from += " or of a create step"
	}
	return fmt.Sprintf("{{%s}} is not a parameter, run.id, or a capture of %s", name, from)
}

func (v *validator) captures(where, phase string, cs []domain.Capture, seen map[string]bool) {
	for j, c := range cs {
		cp := fmt.Sprintf("%s.captures[%d]", where, j)
		switch {
		case c.Name == "":
			v.add(cp, "a capture has no name")
		case !capturePattern.MatchString(c.Name):
			v.add(cp, "capture name %s must match %s", c.Name, captureRule)
		}
		if _, err := jsonpath.Parse(c.JSONPath); err != nil {
			v.add(cp, "capture %s: jsonpath does not parse: %v", c.Name, err)
		}
		if _, isParam := v.f.Params[c.Name]; isParam {
			v.add(cp, "capture %s has the name of a parameter", c.Name)
		}
		if c.Name != "" && seen[c.Name] {
			v.add(cp, "capture %s is defined more than once in %s", c.Name, phase)
		}
		seen[c.Name] = true
	}
}

func (v *validator) until(where string, st domain.FixtureStep) {
	u := st.Until
	if u == nil {
		return
	}
	up := where + ".until"
	if m := st.Request.Method; m != "GET" && m != "HEAD" {
		v.add(up, "until is allowed only on a GET or HEAD step: repeating a write would repeat its effect")
	}
	if _, err := jsonpath.Parse(u.JSONPath); err != nil {
		v.add(up+".jsonpath", "until jsonpath does not parse: %v", err)
	}
	switch u.Equals.(type) {
	case string, bool, int, int64, uint64, float64:
	default:
		v.add(up+".equals", "until equals must be a string, a number, or a boolean")
	}
	switch {
	case u.Timeout <= 0:
		v.add(up+".timeout", "until needs a timeout above 0, such as 30s")
	case u.Timeout > maxUntil:
		v.add(up+".timeout", "until timeout %s is above the limit of %s", u.Timeout, maxUntil)
	}
	switch {
	case u.Every == 0:
	case u.Every < minEvery:
		v.add(up+".every", "until every %s is under %s", u.Every, minEvery)
	case u.Timeout > 0 && u.Every > u.Timeout:
		v.add(up+".every", "until every %s is above the timeout %s", u.Every, u.Timeout)
	}
}

func (v *validator) outputs() {
	f := v.f
	switch n := len(f.Outputs); {
	case n == 0:
		v.add("outputs", "a fixture needs 1 to %d outputs", maxOutputs)
	case n > maxOutputs:
		v.add("outputs", "%d outputs; at most %d", n, maxOutputs)
	}
	seen := map[string]bool{}
	for i, o := range f.Outputs {
		where := fmt.Sprintf("outputs[%d]", i)
		if seen[o] {
			v.add(where, "output %s is listed more than once", o)
			continue
		}
		seen[o] = true
		if len(f.Find) > 0 && !v.captured[domain.PhaseFind][o] {
			v.add(where, "output %s is not captured by any find step", o)
		}
		if len(f.Create) > 0 && !v.captured[domain.PhaseCreate][o] {
			v.add(where, "output %s is not captured by any create step", o)
		}
	}
}
