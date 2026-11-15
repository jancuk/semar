package plan

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/script"
)

// Input is everything a plan needs besides the planner. It holds names, never values (A5).
type Input struct {
	Change        domain.ChangeSet
	SourceDir     string   // the copy of the source at the head commit
	Services      []string // names from the configuration
	Methods       []string // methods the environment allows
	AuthProfile   string
	SessionHeader string                       // the name of the auth profile's session header
	Existing      map[string][]domain.Scenario // by ticket: scenarios that already exist
	Limits        config.Plan
	Model         string
	Reasoning     string             // the level that the planner was asked to use; "" for its default
	SetupOn       bool               // the planner ran with the person's own MCP servers or skills switched on
	LeftOut       []LeftOut          // files of the head commit that the copy of the source lacks
	Knowledge     []knowledge.Source // the documents that the configuration lists, as found in the copy
}

// LeftOut is a file that the copy of the source does not hold, and why.
type LeftOut struct{ Path, Reason string }

// Problem is one reason a draft was refused. Path names the place in the draft, such as
// scenarios[1].steps[2].expect[0].check, so that the planner can find it.
type Problem struct{ Path, Message string }

// PlanDir is the directory of the source copy that holds what Semar wrote for the planner.
const PlanDir = ".semar-plan"

const (
	maxCitedFile   = 5 << 20
	maxDifferences = 20
)

var (
	ticketKinds = []string{"behaviour", "refactor", "config", "none"}
	risks       = []string{"low", "standard", "high"}
	// Authentication is added by the engine, and a Host header would point a request elsewhere.
	refusedHeaders = []string{"Authorization", "Cookie", "Proxy-Authorization", "Host"}
	citePattern    = regexp.MustCompile(`^([^\s:]+):([0-9]+)(?:[^0-9]|$)`)
)

// Validate builds one scenario and one script per drafted scenario and returns every problem
// of the draft. The values come back only when there is no problem. It reads SourceDir to
// check cites and does no other I/O.
func Validate(d Draft, in Input) ([]domain.Scenario, []domain.Script, []Problem) {
	v := &validator{in: in, known: map[string]bool{}, files: map[string]citedFile{}}
	defer v.close()
	for _, t := range in.Change.Tickets {
		v.known[t.Key] = true
	}
	v.tickets(d)
	if limit := in.Limits.MaxScenarios; limit > 0 && len(d.Scenarios) > limit {
		v.add("scenarios", "the draft has %d scenarios, and the limit is %d: keep the ones that would catch the most likely defects", len(d.Scenarios), limit)
	}
	for i, n := range d.NotDrafted {
		if !v.known[n.Ticket] {
			v.add(fmt.Sprintf("notDrafted[%d].ticket", i), "ticket %q is not in the change set; use one of %s", n.Ticket, v.ticketList())
		}
	}

	next := map[string]int{}
	titles := map[string]map[string]bool{}
	for ticket, scs := range in.Existing {
		titles[ticket] = map[string]bool{}
		for _, sc := range scs {
			titles[ticket][normalTitle(sc.Title)] = true
			if n, ok := numberOf(ticket, sc.ID); ok && n > next[ticket] {
				next[ticket] = n
			}
		}
	}

	var scenarios []domain.Scenario
	var scripts []domain.Script
	for i, sd := range d.Scenarios {
		next[sd.Ticket]++
		id := domain.ScenarioID(fmt.Sprintf("%s-%02d", sd.Ticket, next[sd.Ticket]))
		if titles[sd.Ticket] == nil {
			titles[sd.Ticket] = map[string]bool{}
		}
		sc, scr := v.scenario(i, sd, id, titles[sd.Ticket])
		scenarios = append(scenarios, sc)
		scripts = append(scripts, scr)
	}
	v.behaviourCovered(d)
	v.differences(d.Differences)

	if len(v.ps) > 0 {
		return nil, nil, v.ps
	}
	return scenarios, scripts, nil
}

type citedFile struct {
	lines   int
	problem string
}

type validator struct {
	in      Input
	known   map[string]bool
	ps      []Problem
	files   map[string]citedFile
	root    *os.Root
	rootErr error
}

func (v *validator) add(path, format string, args ...any) {
	p := Problem{Path: path, Message: fmt.Sprintf(format, args...)}
	if !slices.Contains(v.ps, p) {
		v.ps = append(v.ps, p)
	}
}

func (v *validator) close() {
	if v.root != nil {
		_ = v.root.Close()
	}
}

func (v *validator) ticketList() string {
	keys := slices.Sorted(maps.Keys(v.known))
	if len(keys) == 0 {
		return "none: the change set has no ticket"
	}
	return strings.Join(keys, ", ")
}

func (v *validator) tickets(d Draft) {
	listed := map[string]bool{}
	for i, t := range d.Tickets {
		p := fmt.Sprintf("tickets[%d]", i)
		switch {
		case !v.known[t.Key]:
			v.add(p+".key", "ticket %q is not in the change set; the change set has %s", t.Key, v.ticketList())
		case listed[t.Key]:
			v.add(p+".key", "ticket %s is listed more than once", t.Key)
		}
		listed[t.Key] = true
		if !slices.Contains(ticketKinds, t.Kind) {
			v.add(p+".kind", "kind %q must be one of %s", t.Kind, strings.Join(ticketKinds, ", "))
		}
		if !slices.Contains(risks, t.Risk) {
			v.add(p+".risk", "risk %q must be one of %s", t.Risk, strings.Join(risks, ", "))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(v.known)) {
		if !listed[key] {
			v.add("tickets", "ticket %s of the change set has no entry: list every ticket of the change table", key)
		}
	}
}

func (v *validator) behaviourCovered(d Draft) {
	for i, t := range d.Tickets {
		if t.Kind != "behaviour" || !v.known[t.Key] {
			continue
		}
		has := slices.ContainsFunc(d.Scenarios, func(s ScenarioDraft) bool { return s.Ticket == t.Key }) ||
			slices.ContainsFunc(d.NotDrafted, func(n NotDrafted) bool { return n.Ticket == t.Key })
		if !has {
			v.add(fmt.Sprintf("tickets[%d]", i), "ticket %s changes behaviour and has neither a scenario nor a notDrafted entry", t.Key)
		}
	}
}

func (v *validator) scenario(i int, sd ScenarioDraft, id domain.ScenarioID, titles map[string]bool) (domain.Scenario, domain.Script) {
	sp := fmt.Sprintf("scenarios[%d]", i)
	if !v.known[sd.Ticket] {
		v.add(sp+".ticket", "ticket %q is not in the change set; use one of %s", sd.Ticket, v.ticketList())
	}
	switch t := normalTitle(sd.Title); {
	case t == "":
		v.add(sp+".title", "a scenario needs a title")
	case titles[t]:
		v.add(sp+".title", "title %q repeats a scenario of ticket %s: give each scenario its own title", sd.Title, sd.Ticket)
	default:
		titles[t] = true
	}

	sc := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: id, Title: sd.Title, Ticket: sd.Ticket, Intent: sd.Intent,
		Surface: domain.SurfaceAPI, AuthProfile: v.in.AuthProfile,
	}
	scr := domain.Script{APIVersion: "semar/v1", Kind: "Script", Scenario: id}
	stepIndex := map[domain.StepID]int{}
	expectations := 0
	for j, st := range sd.Steps {
		p := fmt.Sprintf("%s.steps[%d]", sp, j)
		if _, seen := stepIndex[domain.StepID(st.ID)]; !seen {
			stepIndex[domain.StepID(st.ID)] = j
		}
		service := v.service(p+".service", st.Service)
		if !slices.Contains(v.in.Methods, st.Method) {
			v.add(p+".method", "method %q is not allowed; the environment allows %s", st.Method, strings.Join(v.in.Methods, ", "))
		}
		for _, name := range slices.Sorted(maps.Keys(st.Headers)) {
			v.header(fmt.Sprintf("%s.headers[%q]", p, name), name)
		}
		var expect []domain.Expectation
		for k, e := range st.Expect {
			ep := fmt.Sprintf("%s.expect[%d]", p, k)
			expect = append(expect, domain.Expectation{Check: v.check(ep+".check", e.Check), Source: domain.SourceDiff, Cite: e.Why})
			v.cite(ep+".why", e.Why)
		}
		expectations += len(expect)
		sc.Steps = append(sc.Steps, domain.Step{
			ID: domain.StepID(st.ID), Setup: st.Setup, Action: st.Action,
			Hint: &domain.RequestHint{Method: st.Method, Path: st.Hint}, Expect: expect,
		})
		var captures []domain.Capture
		for _, c := range st.Captures {
			captures = append(captures, domain.Capture{Name: c.Name, JSONPath: c.JSONPath})
		}
		scr.Steps = append(scr.Steps, domain.ScriptStep{
			Step: domain.StepID(st.ID),
			Request: &domain.APIRequest{
				Service: service, Method: st.Method, Path: st.Path,
				Query: st.Query, Headers: st.Headers, JSON: st.JSON,
			},
			Captures: captures,
		})
	}
	if expectations == 0 {
		v.add(sp, "a scenario needs at least one expected result")
	}

	for _, pr := range scenario.Validate(sc, scenario.FromFile) {
		v.add(scenarioPath(sp, pr.Path), "%s", pr.Message)
	}
	if data, err := scenario.Marshal(sc); err != nil {
		path, msg := writerProblem(sp, err)
		v.add(path, "the scenario cannot be written: %s", msg)
	} else if back, err := scenario.Parse(data, string(id)); err != nil {
		v.add(sp, "the scenario cannot be read back: %v", err)
	} else {
		sc = back
	}

	scr.ExpectHash = domain.ExpectHash(sc)
	for _, pr := range script.Validate(sc, scr) {
		if expectations == 0 && scr.ExpectHash == "" && pr.Step == "" {
			continue
		}
		path := sp
		if j, ok := stepIndex[pr.Step]; ok && pr.Step != "" {
			path = fmt.Sprintf("%s.steps[%d]", sp, j)
		}
		v.add(path, "%s", pr.Message)
	}
	if data, err := script.Marshal(scr); err != nil {
		path, msg := writerProblem(sp, err)
		v.add(path, "the request cannot be written: %s", msg)
	} else if back, err := script.Parse(data, string(id)); err != nil {
		v.add(sp, "the request cannot be read back: %v", err)
	} else {
		scr = back
	}
	return sc, scr
}

func (v *validator) service(path, name string) string {
	list := strings.Join(v.in.Services, ", ")
	switch {
	case name == "" && len(v.in.Services) == 1:
		return v.in.Services[0]
	case name == "":
		v.add(path, "name the service of the request: one of %s", list)
	case !slices.Contains(v.in.Services, name):
		v.add(path, "service %q is not in the configuration; use one of %s", name, list)
	}
	return name
}

func (v *validator) header(path, name string) {
	switch {
	case v.in.SessionHeader != "" && strings.EqualFold(name, v.in.SessionHeader):
		v.add(path, "header %s is the session header of the auth profile; the program adds it to every request", name)
	case slices.ContainsFunc(refusedHeaders, func(h string) bool { return strings.EqualFold(h, name) }):
		v.add(path, "header %s cannot be drafted; the program adds authentication and chooses the host", name)
	}
}

func (v *validator) check(path string, c CheckDraft) domain.Check {
	out := domain.Check{
		Status: c.Status, JSONPath: c.JSONPath, Header: c.Header,
		Equals: canonical(c.Equals), NotEquals: canonical(c.NotEquals), Contains: canonical(c.Contains),
		Exists: c.Exists, Count: c.Count, EqualsPath: c.EqualsPath,
	}
	for _, b := range []struct {
		key string
		n   *json.Number
		out **float64
	}{{"greater", c.Greater, &out.Greater}, {"less", c.Less, &out.Less}} {
		if b.n == nil {
			continue
		}
		f, err := exactFloat(*b.n)
		if err != nil {
			v.add(path+"."+b.key, "%v", err)
			continue
		}
		*b.out = &f
	}
	return out
}

func (v *validator) cite(path, why string) {
	v.cited(path, why, "why must start with <file>:<line> of the code that produces the result, for example internal/refund/handler.go:41")
}

// cited checks that text starts with <file>:<line> of a line of a file of the source copy, and
// returns that file in its clean slash form. missing is the problem of a text that does not.
func (v *validator) cited(path, text, missing string) (string, bool) {
	m := citePattern.FindStringSubmatch(text)
	if m == nil {
		v.add(path, "%s", missing)
		return "", false
	}
	file := m[1]
	line, err := strconv.Atoi(m[2])
	if err != nil || line < 1 {
		v.add(path, "%s:%s names no line: lines start at 1", file, m[2])
		return "", false
	}
	f := v.file(file)
	switch {
	case f.problem != "":
		v.add(path, "%s", f.problem)
		return "", false
	case line > f.lines:
		v.add(path, "%s has %d lines, so it has no line %d", file, f.lines, line)
		return "", false
	}
	return cleanSlash(file), true
}

func cleanSlash(file string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(file)))
}

// differences checks the places that the planner names. It cannot check that the two texts
// disagree; plan.md says so.
func (v *validator) differences(ds []Difference) {
	if len(ds) == 0 {
		return
	}
	var usable []knowledge.Source
	for _, s := range v.in.Knowledge {
		if s.Usable() {
			usable = append(usable, s)
		}
	}
	if len(usable) == 0 {
		v.add("differences", "no document was listed for you to read, so there is no difference to report: leave differences out")
		return
	}
	if len(ds) > maxDifferences {
		v.add("differences", "the draft has %d differences, and the limit is %d: keep the ones that a caller would notice first", len(ds), maxDifferences)
	}
	holds := func(file string) bool {
		return slices.ContainsFunc(usable, func(s knowledge.Source) bool { return s.Holds(file) })
	}
	paths := make([]string, len(usable))
	for i, s := range usable {
		paths[i] = s.Path
	}
	for i, d := range ds {
		p := fmt.Sprintf("differences[%d]", i)
		if !v.known[d.Ticket] {
			v.add(p+".ticket", "ticket %q is not in the change set; use one of %s", d.Ticket, v.ticketList())
		}
		if strings.TrimSpace(d.What) == "" {
			v.add(p+".what", "say in one sentence what differs between the document and the code")
		}
		doc, docOK := v.cited(p+".document", d.Document, "document must start with <file>:<line> of a document that the team listed, for example docs/refunds.md:14")
		code, codeOK := v.cited(p+".code", d.Code, "code must start with <file>:<line> of the code, for example internal/refund/handler.go:41")
		if docOK && !holds(doc) {
			v.add(p+".document", "%s is not one of the documents that the team listed; cite a file of %s", doc, strings.Join(paths, ", "))
		}
		switch {
		case docOK && codeOK && doc == code:
			v.add(p+".code", "document and code both cite %s; cite the code that does what the document describes", code)
		case codeOK && holds(code):
			v.add(p+".code", "%s is one of the documents that the team listed; cite the code that does what the document describes", code)
		}
	}
}

// file counts the lines of a file of the source copy, once per path.
func (v *validator) file(name string) citedFile {
	if f, ok := v.files[name]; ok {
		return f
	}
	f := v.readFile(name)
	v.files[name] = f
	return f
}

func (v *validator) readFile(name string) citedFile {
	native := filepath.Clean(filepath.FromSlash(name))
	if !filepath.IsLocal(native) {
		return citedFile{problem: fmt.Sprintf("%s is not a path inside the source", name)}
	}
	if first, _, _ := strings.Cut(filepath.ToSlash(native), "/"); first == PlanDir {
		return citedFile{problem: fmt.Sprintf("%s was written for you by the program; cite a file of the source", name)}
	}
	if v.root == nil && v.rootErr == nil {
		v.root, v.rootErr = os.OpenRoot(v.in.SourceDir)
	}
	if v.rootErr != nil {
		return citedFile{problem: fmt.Sprintf("the source copy cannot be read to check %s", name)}
	}
	missing := citedFile{problem: fmt.Sprintf("%s is not a file of the source", name)}
	info, err := v.root.Lstat(native)
	if err != nil {
		return missing
	}
	if !info.Mode().IsRegular() {
		return citedFile{problem: fmt.Sprintf("%s is not a regular file of the source", name)}
	}
	fh, err := v.root.Open(native)
	if err != nil {
		return missing
	}
	defer fh.Close()
	lines, err := countLines(io.LimitReader(fh, maxCitedFile+1))
	if errors.Is(err, errTooLarge) || info.Size() > maxCitedFile {
		return citedFile{problem: fmt.Sprintf("%s is larger than %d MiB; cite a smaller file", name, maxCitedFile>>20)}
	}
	if err != nil {
		return missing
	}
	return citedFile{lines: lines}
}

var errTooLarge = errors.New("the file is too large")

func countLines(r io.Reader) (int, error) {
	br := bufio.NewReader(r)
	lines, size, last := 0, 0, byte('\n')
	buf := make([]byte, 32<<10)
	for {
		n, err := br.Read(buf)
		size += n
		if size > maxCitedFile {
			return 0, errTooLarge
		}
		for _, b := range buf[:n] {
			if b == '\n' {
				lines++
			}
		}
		if n > 0 {
			last = buf[n-1]
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	if last != '\n' {
		lines++
	}
	return lines, nil
}

func normalTitle(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// numberOf reads NN from the ID <ticket>-NN.
func numberOf(ticket string, id domain.ScenarioID) (int, bool) {
	rest, ok := strings.CutPrefix(string(id), ticket+"-")
	if !ok || rest == "" || strings.Trim(rest, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

// scenarioPath maps a path of a built scenario to the draft. A field that code fills is
// reported at the scenario.
func scenarioPath(sp, p string) string {
	switch p {
	case "", "apiVersion", "kind", "id", "surface", "authProfile":
		return sp
	}
	return sp + "." + p
}

// writerProblem reads the path that a writer error starts with, such as
// steps[0].request.json.amount, and maps it to the draft.
func writerProblem(sp string, err error) (path, message string) {
	prefix, rest, ok := strings.Cut(err.Error(), ": ")
	if !ok || !strings.HasPrefix(prefix, "steps[") || strings.ContainsAny(prefix, " \t\n") {
		return sp, err.Error()
	}
	return sp + "." + strings.Replace(prefix, ".request.", ".", 1), rest
}

// plainNumber bounds the text that math/big reads, so a huge exponent costs nothing.
var plainNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,39})(\.[0-9]{1,40})?([eE][-+]?[0-9]{1,3})?$`)

func ratOf(s string) (*big.Rat, bool) {
	if !plainNumber.MatchString(s) {
		return nil, false
	}
	return new(big.Rat).SetString(s)
}

// canonical rewrites a whole number that int64 holds, such as 100.0 or 1e3, as its integer
// text. A model writes such numbers, and the scenario writer refuses a number whose text
// differs from how it is stored. Every other value is kept for the writer to judge.
func canonical(v any) any {
	switch x := v.(type) {
	case json.Number:
		r, ok := ratOf(string(x))
		if !ok || !r.IsInt() || !r.Num().IsInt64() {
			return x
		}
		return json.Number(r.Num().String())
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, el := range x {
			out[k] = canonical(el)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = canonical(el)
		}
		return out
	}
	return v
}

// exactFloat returns the float64 of a bound when the shortest text of that float64 has the
// same value as the drafted text, which is how a scenario file reads a bound.
func exactFloat(n json.Number) (float64, error) {
	want, ok := ratOf(string(n))
	if !ok {
		return 0, fmt.Errorf("%s is not a number that a check can hold", n)
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%s is outside the range of a number", n)
	}
	shortest := strconv.FormatFloat(f, 'g', -1, 64)
	got, ok := new(big.Rat).SetString(shortest)
	if !ok || got.Cmp(want) != 0 {
		return 0, fmt.Errorf("%s cannot be held exactly: it would be read as %s", n, shortest)
	}
	return f, nil
}
