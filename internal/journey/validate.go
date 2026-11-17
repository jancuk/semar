package journey

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/datevar"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/script"
)

// Problem is one reason a journey file cannot be used. File is the file as the caller names it,
// Line is the line in it (0 when unknown), and Path is the place in the file, such as
// legs[1].steps[0].request.path. Any of them may be empty.
type Problem struct {
	File    string
	Line    int
	Path    string
	Message string
}

func (p Problem) String() string {
	file := p.File
	if p.Line > 0 {
		file = fmt.Sprintf("%s:%d", file, p.Line)
	}
	var parts []string
	for _, s := range []string{file, p.Path, p.Message} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ": ")
}

const (
	maxLegs       = 12
	maxSteps      = 20
	maxInvariants = 8
	minEqual      = 2
	maxEqual      = 6
	maxTitle      = 200
	maxScenarioID = 128
	maxWait       = 15 * time.Minute
	minEvery      = 100 * time.Millisecond
	// No hyphen: a leg ID is the head of {{<leg>.<output>}}, and a variable name has none.
	legIDRule = `^[a-z][a-z0-9_]*$`
	notYet    = "legs are not supported yet: this version runs api legs and web legs; android legs come in M3"
)

var (
	legIDPattern = regexp.MustCompile(legIDRule)
	namePattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	// oneVariable is a whole string that is exactly one {{<head>.<name>}}.
	oneVariable = regexp.MustCompile(`^\{\{([A-Za-z][A-Za-z0-9_]*)\.([A-Za-z0-9_]+)\}\}$`)
)

const unknownVariable = " is not a known variable"

// Validate checks rules J2 to J17 on a parsed journey. outputs gives the outputs of a fixture and
// whether the fixture exists; with nil, the fixtures are not looked up and any output of a given
// entry is accepted. J1 and J9 are checked by Parse. The file name is not known here, so J2 checks
// the file name only for a journey that Load read.
func Validate(j domain.Journey, outputs func(domain.FixtureID) ([]string, bool)) []Problem {
	return validate(j, "", outputs)
}

// ScreensFunc gives the screens file of an app. The error is screens.ErrNotFound for a file that is not there.
type ScreensFunc func(app string) (domain.Screens, error)

// ValidateWithScreens is Validate for a journey that has web legs, with a lookup of the screens files,
// so that a screen or an element that a step names and the file lacks is a problem. With a nil lookup,
// the screens are not looked up.
func ValidateWithScreens(j domain.Journey, outputs func(domain.FixtureID) ([]string, bool), screensOf ScreensFunc) []Problem {
	return validateFull(j, "", outputs, screensOf)
}

func validate(j domain.Journey, file string, outputs func(domain.FixtureID) ([]string, bool)) []Problem {
	return validateFull(j, file, outputs, nil)
}

func validateFull(j domain.Journey, file string, outputs func(domain.FixtureID) ([]string, bool), screensOf ScreensFunc) []Problem {
	v := &validator{file: file, j: j, outputs: outputs, screens: screensOf, aliases: map[string]alias{}, legIndex: map[domain.LegID]int{}}
	for i, l := range j.Legs {
		if _, dup := v.legIndex[l.ID]; !dup {
			v.legIndex[l.ID] = i
		}
	}
	v.header()
	v.given()
	v.legs()
	v.invariants()
	return v.ps
}

// alias is one given entry as a later leg sees it.
type alias struct {
	fixture domain.FixtureID
	outs    []string
	known   bool // false when the fixture was not looked up or was not found: any output name passes
}

type validator struct {
	file     string
	j        domain.Journey
	screens  ScreensFunc
	outputs  func(domain.FixtureID) ([]string, bool)
	aliases  map[string]alias
	legIndex map[domain.LegID]int // the first leg with each ID
	ps       []Problem
}

func (v *validator) add(where, format string, args ...any) {
	v.ps = append(v.ps, Problem{File: v.file, Path: where, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) header() {
	j := v.j
	if j.APIVersion != "semar/v1" {
		v.add("apiVersion", "apiVersion must be semar/v1")
	}
	if j.Kind != "Journey" {
		v.add("kind", "kind must be Journey")
	}
	if err := idcheck.Check("journey ID", string(j.ID)); err != nil {
		v.add("id", "%v", err)
	} else {
		if strings.Contains(string(j.ID), ".") {
			v.add("id", "id %s must not hold a dot: the scenario of a leg is named <journey>.<leg>", j.ID)
		}
		if v.file != "" {
			base := path.Base(strings.ReplaceAll(v.file, `\`, "/"))
			if stem := strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml"); stem != string(j.ID) || stem == base {
				v.add("id", "id %s does not match the file name %s", j.ID, base)
			}
		}
	}

	switch t := j.Title; {
	case strings.TrimSpace(t) == "":
		v.add("title", "title is required")
	case !utf8.ValidString(t):
		v.add("title", "title is not valid UTF-8")
	case strings.ContainsFunc(t, unicode.IsControl):
		v.add("title", "title must be one line without control characters")
	case utf8.RuneCountInString(t) > maxTitle:
		v.add("title", "title has %d characters; at most %d", utf8.RuneCountInString(t), maxTitle)
	}

	if j.Owner == "" {
		v.add("owner", "owner is required: a named product or QA owner owns a journey")
	} else if err := idcheck.Check("owner", j.Owner); err != nil {
		v.add("owner", "%v", err)
	}

	if j.Criticality != "critical" && j.Criticality != "standard" {
		v.add("criticality", "criticality must be critical or standard")
	}
	if j.AuthProfile != "" {
		if err := idcheck.Check("auth profile name", j.AuthProfile); err != nil {
			v.add("authProfile", "%v", err)
		}
	}
}

// given applies the scenario rules of plan 17 (J5), looks up each fixture, and records each alias.
func (v *validator) given() {
	for _, p := range scenario.Validate(domain.Scenario{Given: v.j.Given}, scenario.FromFile) {
		if strings.HasPrefix(p.Path, "given") {
			v.add(p.Path, "%s", p.Message)
		}
	}
	for i, g := range v.j.Given {
		where := fmt.Sprintf("given[%d]", i)
		if _, isLeg := v.legIndex[domain.LegID(g.As)]; isLeg {
			v.add(where+".as", "%s is also a leg ID", g.As)
		}
		a := alias{fixture: g.Fixture}
		if v.outputs != nil && !slices.ContainsFunc(v.ps, func(p Problem) bool { return p.Path == where+".fixture" }) {
			outs, found := v.outputs(g.Fixture)
			if !found {
				v.add(where+".fixture", "fixture %s was not found among the valid fixtures of .semar/fixtures", g.Fixture)
			}
			a.outs, a.known = outs, found
		}
		if _, dup := v.aliases[g.As]; !dup {
			v.aliases[g.As] = a
		}
	}
}

func (v *validator) legs() {
	legs := v.j.Legs
	switch n := len(legs); {
	case n == 0:
		v.add("legs", "a journey needs 1 to %d legs", maxLegs)
		return
	case n > maxLegs:
		v.add("legs", "%d legs; at most %d", n, maxLegs)
		return
	}
	seen := map[domain.LegID]bool{}
	for i, l := range legs {
		where := fmt.Sprintf("legs[%d]", i)
		v.legID(where, l.ID, seen)
		seen[l.ID] = true
		v.needs(where, l)
		v.surface(where, l)
		if l.Surface != domain.SurfaceAPI && l.Surface != domain.SurfaceWeb {
			continue
		}
		v.app(where, l)
		switch {
		case l.Service == "":
			v.add(where+".service", "service is required")
		default:
			if err := idcheck.Check("service name", l.Service); err != nil {
				v.add(where+".service", "%v", err)
			}
		}
		reach := v.reach(l)
		v.steps(i, l, reach)
		if l.Surface == domain.SurfaceWeb {
			v.screensOf(i, l)
		}
		v.wait(i, l)
		v.legOutputs(i, l)
	}
	if cycle := findCycle(v.j, v.legIndex, true); len(cycle) > 0 {
		v.add(fmt.Sprintf("legs[%d].needs", v.legIndex[cycle[0]]), "needs has a cycle: %s", joinLegs(cycle))
	}
}

func (v *validator) legID(where string, id domain.LegID, seen map[domain.LegID]bool) {
	switch {
	case !legIDPattern.MatchString(string(id)):
		v.add(where+".id", "id %q must match %s", id, legIDRule)
		return
	case slices.Contains(scenario.ReservedHeads, string(id)):
		v.add(where+".id", "the name %s is reserved", id)
	case seen[id]:
		v.add(where+".id", "%s is used twice", id)
	}
	// J16: the scenario of the leg is <journey>.<leg>, and it names evidence and confirmations.
	sid := string(legScenarioID(v.j.ID, id))
	if len(sid) > maxScenarioID {
		v.add(where+".id", "the leg scenario %s is too long: at most %d bytes", sid, maxScenarioID)
	}
}

func (v *validator) needs(where string, l domain.Leg) {
	seen := map[domain.LegID]bool{}
	for k, n := range l.Needs {
		np := fmt.Sprintf("%s.needs[%d]", where, k)
		_, isLeg := v.legIndex[n]
		switch {
		case n == l.ID:
			v.add(np, "leg %s cannot need itself", n)
		case !isLeg:
			v.add(np, "%s is not a leg of this journey", n)
		case seen[n]:
			v.add(np, "%s is listed twice", n)
		}
		seen[n] = true
	}
}

func (v *validator) surface(where string, l domain.Leg) {
	switch l.Surface {
	case domain.SurfaceAPI:
	case domain.SurfaceWeb:
	case domain.SurfaceAndroid:
		v.add(where+".surface", "%s "+notYet, l.Surface)
	case "":
		v.add(where+".surface", "surface is required: it is api or web")
	default:
		v.add(where+".surface", "surface %q is not known: this version runs api legs and web legs; android legs come in M3", l.Surface)
	}
}

// app checks the screens file that a web leg uses, and that an api leg uses none.
func (v *validator) app(where string, l domain.Leg) {
	switch {
	case l.Surface == domain.SurfaceWeb && l.App == "":
		v.add(where+".app", "app is required for a web leg: it names the screens file .semar/screens/<app>.yaml")
	case l.Surface == domain.SurfaceWeb:
		if err := idcheck.Check("app name", l.App); err != nil {
			v.add(where+".app", "%v", err)
		}
	case l.App != "":
		v.add(where+".app", "app is for a web leg: an api leg has no screens file")
	}
	if l.Surface == domain.SurfaceWeb && l.Wait != nil {
		v.add(where+".wait", "a web leg cannot wait: a wait repeats the last request, and a web step has none. Use a step with do: wait for an element")
	}
}

// reach is every leg that l needs, directly or through other legs.
func (v *validator) reach(l domain.Leg) map[domain.LegID]bool {
	out := map[domain.LegID]bool{}
	var walk func(id domain.LegID)
	walk = func(id domain.LegID) {
		i, ok := v.legIndex[id]
		if !ok {
			return
		}
		for _, n := range v.j.Legs[i].Needs {
			if !out[n] {
				out[n] = true
				walk(n)
			}
		}
	}
	walk(l.ID)
	delete(out, l.ID)
	return out
}

func (v *validator) steps(li int, l domain.Leg, reach map[domain.LegID]bool) {
	base := fmt.Sprintf("legs[%d]", li)
	switch n := len(l.Steps); {
	case n == 0:
		v.add(base+".steps", "a leg needs 1 to %d steps", maxSteps)
		return
	case n > maxSteps:
		v.add(base+".steps", "%d steps; at most %d", n, maxSteps)
		return
	}
	// The binding below needs a service. A leg without one is reported once, above.
	vl := l
	if vl.Service == "" {
		vl.Service = "service"
	}
	stepAt := map[domain.StepID]int{}
	dup := false
	for k, st := range l.Steps {
		sp := fmt.Sprintf("%s.steps[%d]", base, k)
		if l.Surface == domain.SurfaceWeb {
			v.webStep(sp, l, st)
		} else {
			if st.Web != nil {
				v.add(sp+".web", "a step of an api leg has a request, and not a web action: web is for a leg with surface web")
			}
			if st.Hint == nil {
				v.add(sp+".hint", "a step needs a hint: a method and a path template")
			}
			if rs := st.Request.Service; rs != "" && rs != l.Service {
				v.add(sp+".request.service", "%s differs from the leg's service %s", rs, l.Service)
			}
		}
		if _, ok := stepAt[st.ID]; ok {
			dup = true
		} else {
			stepAt[st.ID] = k
		}
	}

	// J17: the name of the leg's account is judged here, with the path of the leg, and not again
	// as the account of each step.
	if l.As != "" {
		if err := idcheck.Check("auth profile name", l.As); err != nil {
			v.add(base+".as", "%v", err)
			vl.As = ""
		}
	}
	sc, sr := legScenario(v.j, vl, v.j.AuthProfile)
	for _, p := range scenario.Validate(sc, scenario.FromFile) {
		if strings.HasPrefix(p.Path, "steps[") {
			v.add(base+"."+p.Path, "%s", p.Message)
		}
	}
	if !dup {
		for _, p := range script.ValidateWith(sc, sr, nil) {
			// A variable with a dot is judged below, with the leg's needs. The messages of
			// script.ValidateWith for it speak of given entries only.
			if strings.Contains(p.Message, " is not an output of fixture ") ||
				strings.Contains(p.Message, "is not a capture, run.id, or an output of a given entry") {
				continue
			}
			if k, ok := stepAt[p.Step]; ok {
				place, msg := "request", p.Message
				if l.Steps[k].Web != nil {
					place, msg = "web", strings.TrimPrefix(p.Message, "web: ")
				}
				v.add(fmt.Sprintf("%s.steps[%d].%s", base, k, place), "%s", msg)
			} else {
				v.add(base+".steps", "%s", p.Message)
			}
		}
	}

	known := func(name string) bool {
		head, _, dotted := strings.Cut(name, ".")
		if !dotted || name == "run.id" || head == "secret" || head == "data" || datevar.Is(name) {
			return true
		}
		return v.refProblem(l, reach, name) == ""
	}
	for k, st := range l.Steps {
		if st.Web != nil {
			for _, name := range st.Web.Variables() {
				if head, _, dotted := strings.Cut(name, "."); dotted && name != "run.id" && head != "secret" && head != "data" && !datevar.Is(name) {
					if msg := v.refProblem(l, reach, name); msg != "" {
						v.add(fmt.Sprintf("%s.steps[%d].web", base, k), "%s", msg)
					}
				}
			}
			continue
		}
		req := st.Request
		req.Service = vl.Service
		for _, m := range script.RequestProblems(req, known) {
			before, ok := strings.CutSuffix(m, "}}"+unknownVariable)
			if !ok {
				continue
			}
			i := strings.LastIndex(before, ": {{")
			if i < 0 {
				continue
			}
			where, name := before[:i], before[i+len(": {{"):]
			v.add(fmt.Sprintf("%s.steps[%d].request.%s", base, k, requestPlace(where)), "%s", v.refProblem(l, reach, name))
		}
	}
}

// requestPlace turns the place that script names ("query page", "header X-A", "json.orderId") into
// a path of the file.
func requestPlace(where string) string {
	switch {
	case strings.HasPrefix(where, "query "):
		return "query." + strings.TrimPrefix(where, "query ")
	case strings.HasPrefix(where, "header "):
		return "headers." + strings.TrimPrefix(where, "header ")
	}
	return where
}

// refProblem is why a leg may not read the dotted variable name, or "" when it may (J12).
func (v *validator) refProblem(from domain.Leg, reach map[domain.LegID]bool, name string) string {
	head, rest, _ := strings.Cut(name, ".")
	if a, ok := v.aliases[head]; ok {
		switch {
		case !a.known && namePattern.MatchString(rest), slices.Contains(a.outs, rest):
			return ""
		case len(a.outs) == 0:
			return fmt.Sprintf("{{%s}} is not an output of fixture %s", name, a.fixture)
		}
		return fmt.Sprintf("{{%s}} is not an output of fixture %s; its outputs are %s", name, a.fixture, strings.Join(a.outs, ", "))
	}
	i, isLeg := v.legIndex[domain.LegID(head)]
	switch {
	case !isLeg:
		return fmt.Sprintf("{{%s}} is not a capture, run.id, an output of a given entry, or an output of a leg that leg %s needs", name, from.ID)
	case head == string(from.ID):
		return fmt.Sprintf("{{%s}} is an output of leg %s itself: use the name of the capture, such as {{%s}}", name, head, rest)
	case !reach[domain.LegID(head)]:
		return fmt.Sprintf("{{%s}} is an output of leg %s, which leg %s does not need", name, head, from.ID)
	}
	outs := v.j.Legs[i].Outputs
	if slices.Contains(outs, rest) {
		return ""
	}
	return notAnOutput(name, head, outs)
}

func notAnOutput(name, leg string, outs []string) string {
	if len(outs) == 0 {
		return fmt.Sprintf("{{%s}} is not an output of leg %s: it has no outputs", name, leg)
	}
	return fmt.Sprintf("{{%s}} is not an output of leg %s; its outputs are %s", name, leg, strings.Join(outs, ", "))
}

func (v *validator) wait(li int, l domain.Leg) {
	w := l.Wait
	if w == nil {
		return
	}
	wp := fmt.Sprintf("legs[%d].wait", li)
	switch {
	case w.Timeout <= 0:
		v.add(wp+".timeout", "wait needs a timeout above 0, such as 30s")
	case w.Timeout > maxWait:
		v.add(wp+".timeout", "wait timeout %s is above the limit of %s", w.Timeout, maxWait)
	}
	switch {
	case w.Every == 0:
	case w.Every < minEvery:
		v.add(wp+".every", "wait every %s is under %s", w.Every, minEvery)
	case w.Timeout > 0 && w.Every > w.Timeout:
		v.add(wp+".every", "wait every %s is above the timeout %s", w.Every, w.Timeout)
	}
	if n := len(l.Steps); n > 0 && l.Surface != domain.SurfaceWeb {
		if m := l.Steps[n-1].Request.Method; m != "" && m != "GET" && m != "HEAD" {
			v.add(wp, "the last step of leg %s is %s; a wait repeats the last step, which must read", l.ID, m)
		}
	}
	if _, err := jsonpath.Parse(w.Until.JSONPath); err != nil {
		v.add(wp+".until.jsonpath", "until jsonpath does not parse: %v", err)
	}
	switch w.Until.Equals.(type) {
	case string, bool, int, int64, uint64, float64:
	default:
		v.add(wp+".until.equals", "until equals must be a string, a number, or a boolean")
	}
}

// legOutputs checks J13: an output is the name of a capture of one of the leg's own steps.
func (v *validator) legOutputs(li int, l domain.Leg) {
	where := fmt.Sprintf("legs[%d].outputs", li)
	captured := map[string]bool{}
	for _, st := range l.Steps {
		for _, c := range st.Captures {
			captured[c.Name] = true
		}
	}
	seen := map[string]bool{}
	for _, o := range l.Outputs {
		switch {
		case !namePattern.MatchString(o):
			v.add(where, "output %q must match %s", o, namePattern)
		case seen[o]:
			v.add(where, "%s is listed twice", o)
		case !captured[o]:
			v.add(where, "%s is not captured by any step of leg %s", o, l.ID)
		}
		seen[o] = true
	}
}

// invariants checks J15. Each entry is exactly one variable that names an output.
func (v *validator) invariants() {
	inv := v.j.Invariants
	if len(inv) > maxInvariants {
		v.add("invariants", "%d invariants; at most %d", len(inv), maxInvariants)
		return
	}
	for i, in := range inv {
		where := fmt.Sprintf("invariants[%d]", i)
		if n := len(in.Equal); n < minEqual || n > maxEqual {
			v.add(where+".equal", "equal needs %d to %d entries, there are %d", minEqual, maxEqual, n)
			continue
		}
		for k, e := range in.Equal {
			ep := where + ".equal[" + strconv.Itoa(k) + "]"
			m := oneVariable.FindStringSubmatch(e)
			if m == nil {
				v.add(ep, "%q must be one variable and nothing else, such as {{leg.output}}", e)
				continue
			}
			if msg := v.invariantRef(m[1], m[2]); msg != "" {
				v.add(ep, "%s", msg)
			}
		}
	}
}

func (v *validator) invariantRef(head, rest string) string {
	name := head + "." + rest
	if a, ok := v.aliases[head]; ok {
		if !a.known || slices.Contains(a.outs, rest) {
			return ""
		}
		return fmt.Sprintf("{{%s}} is not an output of fixture %s; its outputs are %s", name, a.fixture, strings.Join(a.outs, ", "))
	}
	i, ok := v.legIndex[domain.LegID(head)]
	if !ok {
		return fmt.Sprintf("{{%s}} is not an output of a leg or of a given entry", name)
	}
	if outs := v.j.Legs[i].Outputs; !slices.Contains(outs, rest) {
		return notAnOutput(name, head, outs)
	}
	return ""
}

// webStep checks the one action of a step of a web leg, and that the step has no request and no hint.
func (v *validator) webStep(sp string, l domain.Leg, st domain.LegStep) {
	switch {
	case st.Web == nil:
		v.add(sp+".web", "a step of a web leg needs web: its one browser action")
		return
	case st.Hint != nil:
		v.add(sp+".hint", "a step of a web leg has no hint: a hint is the method and the path of a request")
	}
	if !requestEmpty(st.Request) {
		v.add(sp+".request", "a step of a web leg has a web action, and not a request")
	}
	if w := st.Web; w.Service != "" && w.Service != l.Service {
		v.add(sp+".web.service", "%s differs from the leg's service %s: write the service on the leg", w.Service, l.Service)
	}
	if w := st.Web; w.App != "" && w.App != l.App {
		v.add(sp+".web.app", "%s differs from the leg's app %s: write the app on the leg", w.App, l.App)
	}
}

func requestEmpty(r domain.APIRequest) bool {
	return r.Service == "" && r.Method == "" && r.Path == "" && len(r.Query) == 0 && len(r.Headers) == 0 && r.JSON == nil
}

// screensOf checks that the screens file of a web leg is there and has every screen and element that
// the steps of the leg name.
func (v *validator) screensOf(li int, l domain.Leg) {
	if v.screens == nil || l.App == "" || idcheck.Check("app name", l.App) != nil {
		return
	}
	where := fmt.Sprintf("legs[%d]", li)
	scr, err := v.screens(l.App)
	switch {
	case errors.Is(err, screens.ErrNotFound):
		v.add(where+".app", "the screens of %s were not found: %s", l.App, screens.Path(l.App))
		return
	case err != nil:
		v.add(where+".app", "the screens of %s cannot be used: %v", l.App, err)
		return
	}
	for k, st := range l.Steps {
		if st.Web == nil {
			continue
		}
		var elements []string
		for _, e := range st.Expect {
			if e.Check.Element != "" {
				elements = append(elements, e.Check.Element)
			}
		}
		for _, c := range st.Captures {
			if c.Element != "" {
				elements = append(elements, c.Element)
			}
		}
		for _, msg := range screens.Check(scr, []screens.Use{{Action: *st.Web, Elements: elements}}) {
			v.add(fmt.Sprintf("%s.steps[%d].web", where, k), "%s", msg)
		}
	}
}
