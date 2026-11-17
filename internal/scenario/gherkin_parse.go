package scenario

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	gherkin "github.com/cucumber/gherkin/go/v43"
	messages "github.com/cucumber/messages/go/v35"

	"github.com/jancuk/semar/internal/domain"
)

// AcceptedThen lists the forms of a Then line in plain words. An error about a line that Semar
// cannot read ends with it.
const AcceptedThen = `A Then or And line after a When step is one of these. A value is JSON: text in double quotes, then numbers, true, false, lists, and objects.
  the status is 422
  <$.json.path> equals "text"
  <$.json.path> does not equal "text"
  <$.json.path> contains "text"
  <$.json.path> exists
  <$.json.path> does not exist
  <$.json.path> is greater than 3
  <$.json.path> is less than 3
  <$.json.path> has 3 items
  <$.json.path> equals the value at <$.other.path>
  the header <Name> equals "text"   (a header takes the operators of a JSON path)
  the element <id> equals "text"    (an element takes equals, does not equal, contains, exists, and has N items)`

// LoadGherkin reads the Gherkin file at path.
func LoadGherkin(path string) (domain.Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Scenario{}, fmt.Errorf("read scenario %s: %w", path, err)
	}
	return ParseGherkin(data, path)
}

// ParseGherkin reads a Gherkin feature file that holds one Scenario. name is how an error calls the file.
// An error starts with <name>:<line> and says in plain words what Semar cannot read there.
func ParseGherkin(data []byte, name string) (domain.Scenario, error) {
	p := &reader{name: name, comments: map[int64]string{}}
	var next int
	doc, err := gherkin.ParseGherkinDocument(bytes.NewReader(data), func() string { next++; return strconv.Itoa(next) })
	if err != nil {
		return domain.Scenario{}, p.parserErrors(err)
	}
	return p.scenario(doc)
}

type reader struct {
	name     string
	comments map[int64]string // the text after the # of each comment line, by line
}

func (p *reader) fail(line int64, format string, args ...any) error {
	return fmt.Errorf("%s:%d: %s", p.name, line, fmt.Sprintf(format, args...))
}

var parserLine = regexp.MustCompile(`^\((\d+):\d+\): (.*)$`)

// parserErrors turns the messages of the Gherkin parser, which list token names, into one line each
// that says where and what.
func (p *reader) parserErrors(err error) error {
	var errs []error
	for _, line := range strings.Split(err.Error(), "\n") {
		m := parserLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.ParseInt(m[1], 10, 64)
		msg := m[2]
		if i := strings.Index(msg, ", got '"); strings.HasPrefix(msg, "expected: ") && i >= 0 {
			got := strings.TrimSuffix(msg[i+len(", got '"):], "'")
			msg = fmt.Sprintf("Gherkin cannot place the line %q here. A line is a Feature, a tag, a Scenario, a step that starts with Given, When, Then, or And, a table row, or a comment that starts with #", got)
		}
		errs = append(errs, p.fail(n, "%s", msg))
	}
	if len(errs) == 0 {
		return fmt.Errorf("%s: %w", p.name, err)
	}
	return errors.Join(errs...)
}

func (p *reader) scenario(doc *messages.GherkinDocument) (domain.Scenario, error) {
	for _, c := range doc.Comments {
		p.comments[c.Location.Line] = c.Text
	}
	f := doc.Feature
	if f == nil {
		return domain.Scenario{}, p.fail(1, "the file has no Feature: line. A scenario file starts with Feature: and holds one Scenario")
	}
	if f.Language != "en" {
		return domain.Scenario{}, p.fail(f.Location.Line, "Semar reads English Gherkin: remove the # language: line")
	}
	if len(f.Tags) > 0 {
		return domain.Scenario{}, p.fail(f.Tags[0].Location.Line, "tags go on the Scenario line, not on the Feature")
	}
	var found *messages.Scenario
	for _, c := range f.Children {
		switch {
		case c.Background != nil:
			return domain.Scenario{}, p.fail(c.Background.Location.Line, "a Background is not used: put its Given lines in the Scenario")
		case c.Rule != nil:
			return domain.Scenario{}, p.fail(c.Rule.Location.Line, "a Rule is not used: a file holds one Scenario")
		case found != nil:
			return domain.Scenario{}, p.fail(c.Scenario.Location.Line, "a file holds one Scenario: put this one in its own file")
		default:
			found = c.Scenario
		}
	}
	if found == nil {
		return domain.Scenario{}, p.fail(f.Location.Line, "the Feature has no Scenario")
	}
	return p.read(found)
}

func (p *reader) read(sc *messages.Scenario) (domain.Scenario, error) {
	line := sc.Location.Line
	if len(sc.Examples) > 0 {
		return domain.Scenario{}, p.fail(sc.Examples[0].Location.Line, "Examples are not used: write each case as its own Scenario file")
	}
	s := domain.Scenario{APIVersion: apiVersion, Kind: kind, Title: sc.Name, Intent: intentOf(sc.Description)}
	var have struct{ id, ticket, surface, auth bool }
	newline := false
	for _, t := range sc.Tags {
		tl := t.Location.Line
		key, value, _ := strings.Cut(strings.TrimPrefix(t.Name, "@"), ":")
		hasValue := strings.Contains(t.Name, ":")
		set := func(seen *bool, dst *string) error {
			switch {
			case *seen:
				return p.fail(tl, "the tag %s appears twice", "@"+key+":")
			case !hasValue || value == "":
				return p.fail(tl, "the tag @%s: has no value", key)
			}
			*seen, *dst = true, value
			return nil
		}
		var err error
		switch key {
		case "id":
			var v string
			err = set(&have.id, &v)
			s.ID = domain.ScenarioID(v)
		case "ticket":
			err = set(&have.ticket, &s.Ticket)
		case "surface":
			var v string
			err = set(&have.surface, &v)
			s.Surface = domain.Surface(v)
		case "auth":
			err = set(&have.auth, &s.AuthProfile)
		case "intent-newline":
			switch {
			case hasValue:
				err = p.fail(tl, "the tag @intent-newline takes no value")
			case newline:
				err = p.fail(tl, "the tag @intent-newline appears twice")
			default:
				newline = true
			}
		case "noauth":
			switch {
			case hasValue:
				err = p.fail(tl, "the tag @noauth takes no value")
			case have.auth:
				err = p.fail(tl, "a scenario takes @auth:<profile> or @noauth, not both")
			default:
				have.auth = true
			}
		default:
			err = p.fail(tl, "the tag %s is not used. A Scenario takes @id:<id>, @ticket:<key>, @surface:api|web, @auth:<profile>, @noauth, and @intent-newline", t.Name)
		}
		if err != nil {
			return domain.Scenario{}, err
		}
	}
	if !have.auth {
		s.AuthProfile = defaultProfile
	}
	if newline {
		s.Intent += "\n"
	}
	for _, m := range []struct {
		ok  bool
		tag string
	}{{have.id, "@id:<scenario id>"}, {have.ticket, "@ticket:<key>"}, {have.surface, "@surface:api"}} {
		if !m.ok {
			return domain.Scenario{}, p.fail(line, "the Scenario has no %s tag: put it on the line above the Scenario", m.tag)
		}
	}

	steps, given, err := p.steps(sc.Steps)
	if err != nil {
		return domain.Scenario{}, err
	}
	s.Steps, s.Given = steps, given
	return s, nil
}

// intentOf is the description with the indentation that the writer added taken off, and the backslash
// that escapes a line that Gherkin would read as a part of the scenario.
func intentOf(desc string) string {
	if desc == "" {
		return ""
	}
	lines := strings.Split(desc, "\n")
	common := -1
	for _, l := range lines {
		if t := strings.TrimLeft(l, " \t"); t != "" {
			if n := len(l) - len(t); common < 0 || n < common {
				common = n
			}
		}
	}
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			lines[i] = ""
			continue
		}
		l = l[common:]
		lead := len(l) - len(strings.TrimLeft(l, " \t"))
		lines[i] = l[:lead] + strings.TrimPrefix(l[lead:], `\`)
	}
	return strings.Join(lines, "\n")
}

var (
	fixtureLine = regexp.MustCompile(`^the fixture (\S+) as (\S+)( with an exclusive lease)?$`)
	asPrefix    = regexp.MustCompile(`^as ([^\s,]+),\s+(.+)$`)
)

// steps reads the steps of a scenario. A Given that names a fixture is a given entry, and any other
// Given is a setup step. A When is an action step. A Then, and an And after a Then, is an expected
// result of the last When.
func (p *reader) steps(in []*messages.Step) ([]domain.Step, []domain.Given, error) {
	var steps []domain.Step
	var given []domain.Given
	prev := kindNone
	for _, st := range in {
		line := st.Location.Line
		k, err := p.kindOf(st, prev)
		if err != nil {
			return nil, nil, err
		}
		prev = k
		if st.DocString != nil {
			return nil, nil, p.fail(st.DocString.Location.Line, "a doc string (a block of text between \"\"\") is not used")
		}
		end := line
		if st.DataTable != nil {
			end = st.DataTable.Rows[len(st.DataTable.Rows)-1].Location.Line
		}
		comment, hasComment := p.comment(end + 1)

		switch k {
		case kindContext:
			if strings.HasPrefix(st.Text, "the fixture ") {
				g, err := p.fixture(st)
				if err != nil {
					return nil, nil, err
				}
				given = append(given, g)
				continue
			}
			fallthrough
		case kindAction:
			if st.DataTable != nil {
				return nil, nil, p.fail(st.DataTable.Location.Line, "a table belongs under a Given that names a fixture, such as Given the fixture order.paid as order")
			}
			step, err := p.step(st, k == kindContext, comment, hasComment, end)
			if err != nil {
				return nil, nil, err
			}
			steps = append(steps, step)
		case kindOutcome:
			if st.DataTable != nil {
				return nil, nil, p.fail(st.DataTable.Location.Line, "a table is not used under a Then")
			}
			if len(steps) == 0 || steps[len(steps)-1].Setup {
				return nil, nil, p.fail(line, "a %s line is an expected result of the When step above it, and there is none: put it under a When", strings.TrimSpace(st.Keyword))
			}
			c, ok := parseThen(st.Text)
			if !ok {
				return nil, nil, p.fail(line, "Semar cannot read the %s line %q.\n%s", strings.TrimSpace(st.Keyword), st.Text, AcceptedThen)
			}
			if !hasComment {
				return nil, nil, p.fail(end, "this expected result has no source. Put a comment line directly under it: # ticket <where the ticket says so>, or # diff, # testcase, # baseline, # person")
			}
			ex, err := p.source(end+1, comment, c)
			if err != nil {
				return nil, nil, err
			}
			last := &steps[len(steps)-1]
			last.Expect = append(last.Expect, ex)
		}
	}
	return steps, given, nil
}

func (p *reader) comment(line int64) (string, bool) {
	text, ok := p.comments[line]
	if !ok {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "#")), true
}

// kindOf is the kind of a step line. And and But take the kind of the line before.
func (p *reader) kindOf(st *messages.Step, prev stepKind) (stepKind, error) {
	switch st.KeywordType {
	case messages.StepKeywordType_CONTEXT:
		return kindContext, nil
	case messages.StepKeywordType_ACTION:
		return kindAction, nil
	case messages.StepKeywordType_OUTCOME:
		return kindOutcome, nil
	}
	if prev == kindNone {
		return 0, p.fail(st.Location.Line, "the first step must start with Given or When, not %s", strings.TrimSpace(st.Keyword))
	}
	return prev, nil
}

func (p *reader) fixture(st *messages.Step) (domain.Given, error) {
	line := st.Location.Line
	m := fixtureLine.FindStringSubmatch(st.Text)
	if m == nil {
		return domain.Given{}, p.fail(line, "Semar cannot read the line %q. A fixture is written Given the fixture <id> as <name>, and may end with: with an exclusive lease", st.Text)
	}
	g := domain.Given{Fixture: domain.FixtureID(m[1]), As: m[2]}
	if m[3] != "" {
		g.Lease = domain.LeaseExclusive
	}
	if st.DataTable != nil {
		g.With = map[string]string{}
		for _, row := range st.DataTable.Rows {
			if len(row.Cells) != 2 {
				return domain.Given{}, p.fail(row.Location.Line, "a fixture parameter is a row of two cells: | name | value |")
			}
			k, v := row.Cells[0].Value, row.Cells[1].Value
			if _, dup := g.With[k]; dup {
				return domain.Given{}, p.fail(row.Location.Line, "the parameter %q appears twice", k)
			}
			g.With[k] = v
		}
	}
	return g, nil
}

func (p *reader) step(st *messages.Step, setup bool, comment string, hasComment bool, end int64) (domain.Step, error) {
	line := st.Location.Line
	step := domain.Step{Setup: setup, Action: st.Text}
	if m := asPrefix.FindStringSubmatch(st.Text); m != nil {
		step.As, step.Action = m[1], m[2]
	}
	rest, ok := strings.CutPrefix(comment, "step:")
	if !hasComment || !ok {
		return domain.Step{}, p.fail(line, "this step has no ID. Put a comment line directly under it: # step: <id>, or # step: <id>; <METHOD> <path> for a request")
	}
	id, hint, hasHint := strings.Cut(rest, ";")
	step.ID = domain.StepID(strings.TrimSpace(id))
	if step.ID == "" {
		return domain.Step{}, p.fail(end+1, "the step comment has no ID: write # step: <id>")
	}
	if hasHint {
		method, path, ok := strings.Cut(strings.TrimSpace(hint), " ")
		if !ok || method == "" || strings.TrimSpace(path) == "" {
			return domain.Step{}, p.fail(end+1, "the request of a step is written <METHOD> <path>, as in # step: %s; POST /v1/orders", step.ID)
		}
		step.Hint = &domain.RequestHint{Method: method, Path: strings.TrimSpace(path)}
	}
	return step, nil
}

// source reads a comment under a Then line: <source> <cite>, with , by model after the cite when a
// model wrote the expectation.
func (p *reader) source(line int64, comment string, c domain.Check) (domain.Expectation, error) {
	ex := domain.Expectation{Check: c}
	if text, ok := strings.CutSuffix(comment, ", by model"); ok {
		comment, ex.By = text, "model"
	}
	src, cite, _ := strings.Cut(comment, " ")
	if src == "" {
		return domain.Expectation{}, p.fail(line, "this comment has no source. Write # ticket <cite>, # diff, # testcase, # baseline, or # person")
	}
	ex.Source, ex.Cite = domain.Source(src), strings.TrimSpace(cite)
	return ex, nil
}

var statusLine = regexp.MustCompile(`^the status is (-?[0-9]+)$`)

// parseThen reads the sentence of a Then line.
func parseThen(text string) (domain.Check, bool) {
	if m := statusLine.FindStringSubmatch(text); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return domain.Check{}, false
		}
		return domain.Check{Status: &n}, true
	}
	for _, s := range []struct {
		prefix string
		set    func(*domain.Check, string)
	}{
		{"the header ", func(c *domain.Check, v string) { c.Header = v }},
		{"the element ", func(c *domain.Check, v string) { c.Element = v }},
	} {
		if rest, ok := strings.CutPrefix(text, s.prefix); ok {
			name, op, ok := strings.Cut(rest, " ")
			if !ok || name == "" {
				return domain.Check{}, false
			}
			var c domain.Check
			s.set(&c, name)
			return c, operator(&c, op)
		}
	}
	if !strings.HasPrefix(text, "$") {
		return domain.Check{}, false
	}
	// A path may hold spaces, so the first split that leaves a sentence that reads is the one.
	for i := 1; i < len(text); i++ {
		if text[i] != ' ' {
			continue
		}
		c := domain.Check{JSONPath: text[:i]}
		if operator(&c, text[i+1:]) {
			return c, true
		}
	}
	return domain.Check{}, false
}

// operator reads the words after the subject of a check into c.
func operator(c *domain.Check, text string) bool {
	switch text {
	case "exists", "does not exist":
		v := text == "exists"
		c.Exists = &v
		return true
	}
	if path, ok := strings.CutPrefix(text, "equals the value at "); ok {
		c.EqualsPath = path
		return strings.HasPrefix(path, "$")
	}
	for _, o := range []struct {
		prefix string
		dst    func(*domain.Check) *any
	}{
		{"equals ", func(c *domain.Check) *any { return &c.Equals }},
		{"does not equal ", func(c *domain.Check) *any { return &c.NotEquals }},
		{"contains ", func(c *domain.Check) *any { return &c.Contains }},
	} {
		if lit, ok := strings.CutPrefix(text, o.prefix); ok {
			v, err := decodeLiteral(lit)
			if err != nil || v == nil {
				return false
			}
			*o.dst(c) = v
			return true
		}
	}
	for _, o := range []struct {
		prefix string
		dst    func(*domain.Check) **float64
	}{
		{"is greater than ", func(c *domain.Check) **float64 { return &c.Greater }},
		{"is less than ", func(c *domain.Check) **float64 { return &c.Less }},
	} {
		if lit, ok := strings.CutPrefix(text, o.prefix); ok {
			if !jsonNumber.MatchString(lit) {
				return false
			}
			f, err := strconv.ParseFloat(lit, 64)
			if err != nil || math.IsInf(f, 0) {
				return false
			}
			*o.dst(c) = &f
			return true
		}
	}
	if m := countLine.FindStringSubmatch(text); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return false
		}
		c.Count = &n
		return true
	}
	return false
}

var countLine = regexp.MustCompile(`^has (-?[0-9]+) items?$`)

// decodeLiteral reads one JSON value. A whole number is an int, or a uint64 when it is too wide for
// int, and any other number is a float64, as the YAML reader gave them.
func decodeLiteral(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("text after the value")
	}
	return normalize(v)
}

func normalize(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		text := string(x)
		if !strings.ContainsAny(text, ".eE") {
			if i, err := strconv.ParseInt(text, 10, 64); err == nil {
				return int(i), nil
			}
			u, err := strconv.ParseUint(text, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s is a whole number too wide to be held exactly", text)
			}
			return u, nil
		}
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%s is outside the range of a number", text)
		}
		return f, nil
	case map[string]any:
		for k, e := range x {
			n, err := normalize(e)
			if err != nil {
				return nil, err
			}
			x[k] = n
		}
		return x, nil
	case []any:
		for i, e := range x {
			n, err := normalize(e)
			if err != nil {
				return nil, err
			}
			x[i] = n
		}
		return x, nil
	}
	return v, nil
}
