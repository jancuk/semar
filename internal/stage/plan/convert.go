package plan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/openapi"
)

// CriterionResult is what the answer to one criterion adds to the draft. Exactly one field is set.
type CriterionResult struct {
	Scenario   *ScenarioDraft
	Manual     *ManualCheckDraft
	NotDrafted *NotDrafted
	// Need is set with NotDrafted when the answer names a fixture that the criterion needs.
	Need *FixtureNeed
}

// FixtureNeed is a fixture that one or more criteria need and the catalogue lacks. The plan drafts it
// (D7 of Plan 27) from the operations of the criteria that need it.
type FixtureNeed struct {
	ID      string
	State   string
	Outputs []string
	Cites   []string // the cites of the criteria that need it, in the order of the plan
	// Tickets are the tickets of those criteria.
	Tickets []string
}

var (
	pathParamPattern = regexp.MustCompile(`\{([^{}/]+)\}`)
	refPattern       = regexp.MustCompile(`\{\{[A-Za-z_][A-Za-z0-9_.]*\}\}`)
	captureName      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	indexPattern     = regexp.MustCompile(`\[[0-9]+\]`)
)

// Why is the cite of what an answer says, as every expectation of the answer carries it: the cite of
// the criterion and its first sentence. The model never writes it.
func (p Packet) Why() string { return p.Cite + " " + whyText(p.Criterion) }

// whyText is the first sentence of a criterion, cut: what a cite of the criterion says after its place.
func whyText(criterion string) string {
	if i := strings.Index(criterion, ". "); i > 0 {
		criterion = criterion[:i]
	}
	return cutRunes(strings.TrimRight(criterion, ". "), 160)
}

func (p Packet) operation(key string) (PacketOperation, bool) {
	for _, op := range p.Operations {
		if op.Key == key {
			return op, true
		}
	}
	return PacketOperation{}, false
}

// problems collects what is wrong with an answer, each in plain words that name the place.
type problems struct{ list []Problem }

func (ps *problems) add(path, format string, args ...any) {
	ps.list = append(ps.list, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

// Convert checks an answer against its packet and turns it into the pieces of a draft. It checks
// what the packet knows: that every operation, path parameter, query parameter, and body field is
// one that the document declares, that every required one is given, that a value has its type,
// and that a response field exists. Method, hint, and path come from the operation. The pieces come
// back only when no problem was found.
func (p Packet) Convert(a Answer, in Input) (CriterionResult, []Problem) {
	var ps problems
	var res CriterionResult
	switch a.Kind {
	case KindScenario:
		sd := p.convertScenario(a, in, &ps)
		res.Scenario = &sd
	case KindManualCheck:
		res.Manual = p.convertManual(a, &ps)
	case KindNeedsFixture:
		res.NotDrafted, res.Need = p.convertNeed(a, in, &ps)
	case KindNotDrafted:
		if blank(a.What) {
			ps.add("what", "say what cannot be tested through the API")
		}
		if blank(a.Needs) {
			ps.add("needs", "say what is missing, such as fixture <proposed id>")
		}
		res.NotDrafted = &NotDrafted{Ticket: p.Ticket, What: a.What, Needs: a.Needs, Cite: p.Why()}
	}
	if len(ps.list) > 0 {
		return CriterionResult{}, ps.list
	}
	return res, nil
}

// convertNeed checks the fixture that an answer names, by the rules of a fixture file: the form of
// the ID and of the names, and one paragraph of state. The criterion stays a gap until the fixture
// is drafted, so the result is a notDrafted entry whose needs names the fixture.
func (p Packet) convertNeed(a Answer, in Input, ps *problems) (*NotDrafted, *FixtureNeed) {
	switch {
	case !fixtureIDPattern.MatchString(a.FixtureID):
		ps.add("fixtureId", "id %q "+fixtureIDRule, a.FixtureID)
	case len(a.FixtureID) > maxFixtureIDBytes:
		ps.add("fixtureId", "id %q is longer than %d bytes", a.FixtureID, maxFixtureIDBytes)
	default:
		if _, ok := fixtureIndex(in.Fixtures)[domain.FixtureID(a.FixtureID)]; ok {
			ps.add("fixtureId", "%s is in the catalogue already; name it in given", a.FixtureID)
		}
	}
	state := strings.TrimSpace(a.FixtureState)
	switch {
	case state == "":
		ps.add("fixtureState", "say in one sentence what state the fixture prepares")
	case utf8.RuneCountInString(state) > maxProposalDesc:
		ps.add("fixtureState", "the state has %d characters; at most %d", utf8.RuneCountInString(state), maxProposalDesc)
	case blankLine.MatchString(state) || strings.ContainsFunc(state, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }):
		ps.add("fixtureState", "the state must be one paragraph of plain text")
	}
	if n := len(a.FixtureOutputs); n < 1 || n > maxProposalNames {
		ps.add("fixtureOutputs", "name 1 to %d values that a step reads from the fixture, and this answer names %d", maxProposalNames, n)
	}
	seen := map[string]bool{}
	for i, o := range a.FixtureOutputs {
		switch {
		case !outputPattern.MatchString(o):
			ps.add(fmt.Sprintf("fixtureOutputs[%d]", i), "name %q %s", o, outputRule)
		case seen[o]:
			ps.add(fmt.Sprintf("fixtureOutputs[%d]", i), "name %s is listed twice", o)
		}
		seen[o] = true
	}
	if blank(a.What) {
		ps.add("what", "say what the scenario would test")
	}
	if len(ps.list) > 0 {
		return nil, nil
	}
	nd := &NotDrafted{Ticket: p.Ticket, What: a.What, Needs: "fixture " + a.FixtureID + ": " + oneLine(state, 160), Cite: p.Why()}
	return nd, &FixtureNeed{ID: a.FixtureID, State: state, Outputs: slices.Clone(a.FixtureOutputs), Cites: []string{p.Cite}, Tickets: []string{p.Ticket}}
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func (p Packet) convertManual(a Answer, ps *problems) *ManualCheckDraft {
	if blank(a.Title) {
		ps.add("title", "a manual check needs a title")
	}
	if blank(a.Expected) {
		ps.add("expected", "a manual check needs the result that the person must see")
	}
	if !slices.Contains(Surfaces, a.Surface) {
		ps.add("surface", "a manual check names its surface, one of %s, and this one names %q", strings.Join(Surfaces, ", "), a.Surface)
	}
	if n := len(a.ManualSteps); n < 1 || n > maxManualSteps {
		ps.add("manualSteps", "a manual check has 1 to %d steps, and this one has %d", maxManualSteps, n)
	}
	for i, s := range a.ManualSteps {
		if blank(s) {
			ps.add(fmt.Sprintf("manualSteps[%d]", i), "the step is empty")
		}
	}
	return &ManualCheckDraft{Ticket: p.Ticket, Title: a.Title, Surface: a.Surface, Steps: a.ManualSteps, Expected: a.Expected, Cite: p.Why()}
}

func (p Packet) convertScenario(a Answer, in Input, ps *problems) ScenarioDraft {
	sd := ScenarioDraft{Ticket: p.Ticket, Title: a.Title, Intent: a.Intent}
	if blank(a.Title) {
		ps.add("title", "a scenario needs a title")
	}
	if blank(a.Intent) {
		ps.add("intent", "a scenario needs an intent: what was true before the change and what is true now")
	}
	for i, g := range a.Given {
		gd := GivenDraft{Fixture: g.Fixture, As: g.As}
		path := fmt.Sprintf("given[%d]", i)
		if blank(g.As) {
			ps.add(path+".as", "name what the fixture %s gives, for example order", g.Fixture)
		}
		if len(g.With) > 0 {
			gd.With = map[string]string{}
		}
		for j, w := range g.With {
			s, ok := w.Value.(string)
			if !ok {
				ps.add(fmt.Sprintf("%s.with[%d]", path, j), "the value of the fixture parameter %s must be a string", w.Name)
			}
			if _, dup := gd.With[w.Name]; dup {
				ps.add(fmt.Sprintf("%s.with[%d]", path, j), "the fixture parameter %s is given twice", w.Name)
			}
			gd.With[w.Name] = s
		}
		sd.Given = append(sd.Given, gd)
	}
	if len(a.Steps) == 0 {
		ps.add("steps", "a scenario needs at least one step")
	}
	expectations := 0
	seen := map[string]bool{}
	for i, st := range a.Steps {
		path := fmt.Sprintf("steps[%d]", i)
		if seen[st.ID] {
			ps.add(path+".id", "the step ID %s is used twice; give each step its own ID", st.ID)
		}
		seen[st.ID] = true
		sd2, n := p.convertStep(st, path, in, ps)
		sd.Steps = append(sd.Steps, sd2)
		expectations += n
	}
	if expectations == 0 && len(a.Steps) > 0 {
		ps.add("steps", "no step has an expected result: a scenario needs at least one, which says what the criterion requires")
	}
	return sd
}

func (p Packet) convertStep(st AnswerStep, path string, in Input, ps *problems) (StepDraft, int) {
	sd := StepDraft{ID: st.ID, Setup: st.Setup, Action: st.Action}
	if blank(st.Action) {
		ps.add(path+".action", "describe the step in one sentence")
	}
	if st.As != "" && st.As != p.Account {
		sd.As = st.As
	}
	op, ok := p.operation(st.Operation)
	if !ok {
		keys := make([]string, len(p.Operations))
		for i, o := range p.Operations {
			keys[i] = o.Key
		}
		ps.add(path+".operation", "the operation %q is not one of the operations of this criterion: %s", st.Operation, strings.Join(keys, ", "))
		return sd, 0
	}
	sd.Method, sd.Hint, sd.Service = op.Method, op.Route, p.serviceOf(op, in)

	sd.Path = p.fillPath(op, st, path, ps)
	// A step that expects a refusal tests a validation rule, so it may leave out a required field and
	// send a value of another type. It still cannot send a name that the document does not declare.
	refusal := expectsRefusalStatus(st)
	ids := idRule{ticket: ticketText(in.Tickets, p.Ticket), refusal: refusal}
	sd.Query = p.fillQuery(op, st, path, ids, ps)
	sd.JSON = p.fillBody(op, st, path, ids, ps)

	for j, c := range st.Captures {
		cp := fmt.Sprintf("%s.captures[%d]", path, j)
		if !captureName.MatchString(c.Name) {
			ps.add(cp+".name", "the capture name %q must start with a letter and hold letters, digits, and underscores", c.Name)
		}
		before := len(ps.list)
		p.checkResponsePath(op, c.JSONPath, cp+".jsonpath", "capture "+c.Name+" reads", ps)
		if len(ps.list) == before {
			p.checkCaptureIsOneValue(op, c, cp+".jsonpath", ps)
		}
		sd.Captures = append(sd.Captures, c)
	}

	if st.Setup && len(st.Expect) > 0 {
		ps.add(path+".expect", "step %s is a setup step and has expected results: remove them, or set setup to false", st.ID)
	}
	if !st.Setup && len(st.Expect) == 0 {
		ps.add(path+".expect", "step %s has no expected result: add one, or set setup to true if it only prepares data", st.ID)
	}
	for j, c := range st.Expect {
		ep := fmt.Sprintf("%s.expect[%d]", path, j)
		if check, ok := p.convertCheck(op, c, ep, ps); ok {
			sd.Expect = append(sd.Expect, ExpectDraft{Check: check, Why: p.Why(), Source: sourceTicket})
		}
	}
	return sd, len(sd.Expect)
}

// serviceOf names the service of a step only when there is more than one. A document that names
// its service decides it, and one that does not leaves the first service.
func (p Packet) serviceOf(op PacketOperation, in Input) string {
	switch {
	case len(in.Services) <= 1:
		return ""
	case slices.Contains(in.Services, op.Service):
		return op.Service
	}
	return in.Services[0]
}

func (p Packet) fillPath(op PacketOperation, st AnswerStep, path string, ps *problems) string {
	declared := map[string]openapi.Param{}
	var names []string
	for _, pa := range op.Params {
		if pa.In == "path" {
			declared[pa.Name] = pa
		}
	}
	for _, m := range pathParamPattern.FindAllStringSubmatch(op.Route, -1) {
		if _, ok := declared[m[1]]; !ok {
			declared[m[1]] = openapi.Param{Name: m[1], In: "path", Required: true}
		}
		if !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	given := map[string]string{}
	for j, nv := range st.Path {
		pp := fmt.Sprintf("%s.path[%d]", path, j)
		val, isString := nv.Value.(string)
		switch pa, ok := declared[nv.Name]; {
		case !ok:
			ps.add(pp, "path parameter %s does not exist in %s; %s", nv.Name, op.Key, listOf("its path parameters are", names, "it has no path parameters"))
		case !isString:
			ps.add(pp, "the value of path parameter %s must be a string", nv.Name)
		case given[nv.Name] != "":
			ps.add(pp, "path parameter %s is given twice", nv.Name)
		case !refPattern.MatchString(val) && !slices.Contains(pa.Enum, val):
			ps.add(pp, "path parameter %s must be written as {{name}} of an earlier capture or a fixture output, or as missing-{{run.id}} for an ID that cannot exist; the literal %q is an ID that no step or fixture gave", nv.Name, val)
		default:
			given[nv.Name] = val
		}
	}
	route := op.Route
	for _, name := range names {
		val, ok := given[name]
		if !ok {
			ps.add(path+".path", "path parameter %s of %s is missing: give it as {{name}} of an earlier capture or a fixture output", name, op.Key)
			continue
		}
		route = strings.ReplaceAll(route, "{"+name+"}", val)
	}
	return route
}

// expectsRefusalStatus says whether a step expects a status of 400 to 499.
func expectsRefusalStatus(st AnswerStep) bool {
	for _, c := range st.Expect {
		n, ok := c.Value.(json.Number)
		if c.Subject != "status" || c.Operator != "equals" || !ok {
			continue
		}
		if r, isNum := ratOf(n.String()); isNum && r.IsInt() && r.Num().IsInt64() && r.Num().Int64() >= 400 && r.Num().Int64() <= 499 {
			return true
		}
	}
	return false
}

func (p Packet) fillQuery(op PacketOperation, st AnswerStep, path string, ids idRule, ps *problems) map[string]string {
	refusal := ids.refusal
	declared := map[string]openapi.Param{}
	var names []string
	for _, pa := range op.Params {
		if pa.In == "query" {
			declared[pa.Name] = pa
			names = append(names, pa.Name)
		}
	}
	var out map[string]string
	for j, nv := range st.Query {
		qp := fmt.Sprintf("%s.query[%d]", path, j)
		val, isString := nv.Value.(string)
		pa, ok := declared[nv.Name]
		switch {
		case !ok:
			ps.add(qp, "query parameter %s does not exist in %s; %s", nv.Name, op.Key, listOf("its query parameters are", names, "it has no query parameters"))
			continue
		case !isString:
			ps.add(qp, "the value of query parameter %s must be a string", nv.Name)
			continue
		case !refusal && len(pa.Enum) > 0 && !slices.Contains(pa.Enum, val) && !refPattern.MatchString(val):
			ps.add(qp, "query parameter %s of %s takes one of %s, and not %q", nv.Name, op.Key, strings.Join(pa.Enum, ", "), val)
			continue
		case ids.typedIn(nv.Name, pa.Format, pa.Enum, val):
			ps.add(qp, "query parameter %s of %s holds the ID %q, which no step or fixture gave: capture it in an earlier step, take it from a fixture, or name it in the ticket", nv.Name, op.Key, val)
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		if _, dup := out[nv.Name]; dup {
			ps.add(qp, "query parameter %s is given twice", nv.Name)
		}
		out[nv.Name] = val
	}
	for _, pa := range op.Params {
		if _, ok := out[pa.Name]; !refusal && pa.In == "query" && pa.Required && !ok {
			ps.add(path+".query", "query parameter %s of %s is required and is missing", pa.Name, op.Key)
		}
	}
	return out
}

func (p Packet) fillBody(op PacketOperation, st AnswerStep, path string, ids idRule, ps *problems) any {
	refusal := ids.refusal
	if len(st.Body) == 0 {
		if !refusal && op.BodyKnown && op.BodyRequired {
			for _, f := range op.Body {
				if f.Required {
					ps.add(path+".body", "field %s is required by %s and is missing", f.Name, op.Key)
				}
			}
		}
		return nil
	}
	if !op.BodyKnown {
		ps.add(path+".body", "%s declares no JSON body, so it takes no fields", op.Key)
		return nil
	}
	names := make([]string, len(op.Body))
	declared := map[string]openapi.Field{}
	for i, f := range op.Body {
		names[i], declared[f.Name] = f.Name, f
	}
	body := map[string]any{}
	given := map[string]bool{}
	for j, bf := range st.Body {
		bp := fmt.Sprintf("%s.body[%d]", path, j)
		f, ok := declared[bf.Field]
		switch {
		case !ok && !op.BodyOpen:
			ps.add(bp, "field %s does not exist in %s; %s", bf.Field, op.Key, listOf("its fields are", names, "it has no declared fields"))
			continue
		case given[bf.Field]:
			ps.add(bp, "field %s is given twice", bf.Field)
			continue
		}
		v, problem := bf.Value, ""
		if !refusal {
			v, problem = typedValue(f, ok, bf.Value)
		}
		if problem != "" {
			ps.add(bp, "field %s of %s %s", bf.Field, op.Key, problem)
			given[bf.Field] = true // the missing field is not a second problem
			continue
		}
		if f.Type != "object" && f.Type != "array" && ids.typedIn(bf.Field, f.Format, f.Enum, v) {
			ps.add(bp, "field %s of %s holds the ID %q, which no step or fixture gave: capture it in an earlier step, take it from a fixture, or name it in the ticket", bf.Field, op.Key, fmt.Sprint(v))
			given[bf.Field] = true
			continue
		}
		body[bf.Field], given[bf.Field] = v, true
	}
	for _, f := range op.Body {
		if !refusal && f.Required && !given[f.Name] {
			ps.add(path+".body", "field %s is required by %s and is missing", f.Name, op.Key)
		}
	}
	return body
}

// typedValue checks that a value has the type that the document gives its field, and returns the
// value as it goes into the body. problem is the end of a sentence that starts with the field.
func typedValue(f openapi.Field, declared bool, v any) (any, string) {
	if !declared {
		return v, ""
	}
	if v == nil {
		if f.Nullable {
			return nil, ""
		}
		return nil, "cannot be null; leave the field out to leave it unchanged"
	}
	switch f.Type {
	case "string":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Sprintf("is a string, and the value %v is not", v)
		}
		if len(f.Enum) > 0 && !slices.Contains(f.Enum, s) && !refPattern.MatchString(s) {
			return nil, fmt.Sprintf("takes one of %s, and not %q", strings.Join(f.Enum, ", "), s)
		}
		return s, ""
	case "integer", "number":
		n, ok := v.(json.Number)
		if !ok {
			return nil, fmt.Sprintf("is %s %s, and the value %v is not a number", article(f.Type), f.Type, v)
		}
		if f.Type == "integer" {
			if r, ok := ratOf(n.String()); !ok || !r.IsInt() {
				return nil, fmt.Sprintf("is an integer, and %s is not a whole number", n)
			}
		}
		return n, ""
	case "boolean":
		if _, ok := v.(bool); !ok {
			return nil, fmt.Sprintf("is a boolean, and the value %v is not", v)
		}
		return v, ""
	case "object", "array":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Sprintf("is an %s: write its JSON as text", f.Type)
		}
		var out any
		dec := json.NewDecoder(strings.NewReader(s))
		dec.UseNumber()
		if err := dec.Decode(&out); err != nil {
			return nil, fmt.Sprintf("is an %s, and its text is not JSON: %v", f.Type, err)
		}
		return out, ""
	}
	return v, ""
}

func article(word string) string {
	if strings.ContainsAny(word[:1], "aeiou") {
		return "an"
	}
	return "a"
}

// listOf writes "<lead> a, b, c", or none when the list is empty.
func listOf(lead string, names []string, none string) string {
	if len(names) == 0 {
		return none
	}
	return lead + " " + strings.Join(names, ", ")
}

// checkCaptureIsOneValue refuses a capture of a whole object or array, which a later request cannot use
// as an ID or a value. It names a field below it, when the document declares one.
func (p Packet) checkCaptureIsOneValue(op PacketOperation, c CaptureDraft, path string, ps *problems) {
	declared := map[string]openapi.Field{}
	for _, r := range op.Responses {
		for _, f := range r.Fields {
			declared[f.Name] = f
		}
	}
	norm := indexPattern.ReplaceAllString(c.JSONPath, "[*]")
	f, ok := declared[norm]
	if !ok || (f.Type != "object" && f.Type != "array") {
		return
	}
	hint := ""
	var below []string
	for name, g := range declared {
		if strings.HasPrefix(name, norm+".") && g.Type != "object" && g.Type != "array" {
			below = append(below, name)
		}
	}
	slices.SortFunc(below, func(a, b string) int {
		if c := strings.Count(a, ".") - strings.Count(b, "."); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	if len(below) > 0 {
		hint = ", such as " + below[0]
	}
	ps.add(path, "capture %s reads %s, which is %s %s, and a capture holds one value: read a field of it%s", c.Name, c.JSONPath, article(f.Type), f.Type, hint)
}

// checkResponsePath checks that a JSONPath names a field that a response of the operation declares.
// An operation whose document declares no field leaves the path unchecked, and so does a path below
// a field that the document leaves open.
func (p Packet) checkResponsePath(op PacketOperation, jsonpath, path, subject string, ps *problems) {
	if !strings.HasPrefix(jsonpath, "$") {
		ps.add(path, "%s %q, which is not a JSONPath: it starts with $, as in $.data.id", subject, jsonpath)
		return
	}
	declared := map[string]openapi.Field{}
	for _, r := range op.Responses {
		for _, f := range r.Fields {
			declared[f.Name] = f
		}
	}
	if len(declared) == 0 || jsonpath == "$" {
		return
	}
	norm := indexPattern.ReplaceAllString(jsonpath, "[*]")
	if _, ok := declared[norm]; ok {
		return
	}
	// A function or a filter at the end, such as .length(), or a path below an open field.
	for name, f := range declared {
		if f.Open && strings.HasPrefix(norm, name+".") {
			return
		}
	}
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) > 14 {
		names = append(names[:14], "...")
	}
	ps.add(path, "%s %s, which no response of %s declares; its response fields are %s", subject, jsonpath, op.Key, strings.Join(names, ", "))
}

func (p Packet) convertCheck(op PacketOperation, c AnswerCheck, path string, ps *problems) (CheckDraft, bool) {
	var out CheckDraft
	bad := func(format string, args ...any) (CheckDraft, bool) {
		ps.add(path, format, args...)
		return CheckDraft{}, false
	}
	if c.Subject == "status" {
		if c.Name != "" {
			return bad("a status check has no name: leave name empty")
		}
		if c.Operator != "equals" {
			return bad("a status check uses the operator equals, for example the status is 422")
		}
		n, ok := c.Value.(json.Number)
		r, isNum := ratOf(string(n))
		if !ok || !isNum || !r.IsInt() || !r.Num().IsInt64() || r.Num().Int64() < 100 || r.Num().Int64() > 599 {
			return bad("the status must be a whole number from 100 to 599, and not %v", c.Value)
		}
		code := int(r.Num().Int64())
		out.Status = &code
		return out, true
	}
	switch c.Subject {
	case "jsonpath":
		if blank(c.Name) {
			return bad("a jsonpath check needs the JSONPath in name, for example $.error.code")
		}
		before := len(ps.list)
		p.checkResponsePath(op, c.Name, path+".name", "the check reads", ps)
		if len(ps.list) > before {
			return CheckDraft{}, false
		}
		out.JSONPath = c.Name
	case "header":
		if blank(c.Name) {
			return bad("a header check needs the header name in name, for example Content-Type")
		}
		out.Header = c.Name
	default:
		return bad("the subject %q must be one of %s", c.Subject, strings.Join(checkSubjects, ", "))
	}
	switch c.Operator {
	case "equals", "notEquals", "contains":
		if c.Value == nil {
			return bad("the operator %s cannot compare with null, which a check cannot hold; check the status, or another field that has a value, instead", c.Operator)
		}
		switch c.Operator {
		case "equals":
			out.Equals = c.Value
		case "notEquals":
			out.NotEquals = c.Value
		default:
			out.Contains = c.Value
		}
	case "exists", "notExists":
		if c.Value != nil {
			return bad("the operator %s takes no value: write null", c.Operator)
		}
		exists := c.Operator == "exists"
		out.Exists = &exists
	case "greater", "less":
		n, ok := c.Value.(json.Number)
		if !ok {
			return bad("the operator %s needs a number", c.Operator)
		}
		if c.Operator == "greater" {
			out.Greater = &n
		} else {
			out.Less = &n
		}
	case "count":
		n, ok := c.Value.(json.Number)
		r, isNum := ratOf(string(n))
		if !ok || !isNum || !r.IsInt() || !r.Num().IsInt64() || r.Num().Int64() < 0 {
			return bad("the operator count needs a whole number of items, and not %v", c.Value)
		}
		count := int(r.Num().Int64())
		out.Count = &count
	case "equalsPath":
		s, ok := c.Value.(string)
		if !ok || !strings.HasPrefix(s, "$") {
			return bad("the operator equalsPath needs the other JSONPath as a string, such as \"$.data.id\"")
		}
		out.EqualsPath = s
	default:
		return bad("the operator %q must be one of %s", c.Operator, strings.Join(checkOperators, ", "))
	}
	return out, true
}
