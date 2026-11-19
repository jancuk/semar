package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	gherkin "github.com/cucumber/gherkin/go/v43"

	"github.com/jancuk/semar/internal/domain"
)

const (
	apiVersion = "semar/v1"
	kind       = "Scenario"
	maxDepth   = 200

	defaultProfile = "default"

	indent      = "    " // the steps and the description lines of the scenario
	tableIndent = "      "
)

// Render writes s as a Gherkin feature file that Parse reads back to the same scenario, with the
// same expectation hash. subject names the Feature line, and the ticket key is used when it is
// empty. Render refuses a scenario that no Gherkin text can carry through Parse, and the error
// names the field, in the form <field>: <what is wrong>. An empty apiVersion or kind is written as
// the value that Parse requires.
func Render(s domain.Scenario, subject string) ([]byte, error) {
	if s.APIVersion == "" {
		s.APIVersion = apiVersion
	}
	if s.Kind == "" {
		s.Kind = kind
	}
	if s.APIVersion != apiVersion {
		return nil, fmt.Errorf("scenario apiVersion is %q, want %s", s.APIVersion, apiVersion)
	}
	if s.Kind != kind {
		return nil, fmt.Errorf("scenario kind is %q, want %s", s.Kind, kind)
	}

	r := &renderer{}
	want := s
	want.Given, want.Steps = nil, nil

	var b strings.Builder
	b.WriteString("Feature: " + featureName(subject, s.Ticket) + "\n\n")

	for _, t := range []struct{ field, value string }{{"id", string(s.ID)}, {"ticket", s.Ticket}, {"surface", string(s.Surface)}} {
		if t.value == "" {
			r.fail(t.field, "is required")
		}
		r.word(t.field, t.value)
	}
	r.word("authProfile", s.AuthProfile)
	tags := []string{"@id:" + string(s.ID), "@ticket:" + s.Ticket, "@surface:" + string(s.Surface)}
	switch s.AuthProfile {
	case defaultProfile:
	case "":
		tags = append(tags, "@noauth")
	default:
		tags = append(tags, "@auth:"+s.AuthProfile)
	}
	if len(s.Suites) > 0 {
		for i, name := range s.Suites {
			if !SuiteName.MatchString(name) || slices.Contains(s.Suites[:i], name) {
				r.fail(fmt.Sprintf("suites[%d]", i), "must be a name of lower-case letters, digits, and -, used once")
			}
		}
		tags = append(tags, "@suite:"+strings.Join(s.Suites, ","))
	}
	body, newline := strings.CutSuffix(s.Intent, "\n")
	if newline {
		tags = append(tags, "@intent-newline")
	}
	b.WriteString("  " + strings.Join(tags, " ") + "\n")
	r.oneLine("title", s.Title)
	b.WriteString("  Scenario: " + s.Title + "\n")
	if body != "" {
		r.intent(body)
		for _, line := range strings.Split(body, "\n") {
			if line == "" {
				b.WriteString("\n")
			} else {
				b.WriteString(indent + escapeLine(line) + "\n")
			}
		}
		b.WriteString("\n")
	}

	for i, g := range s.Given {
		path := fmt.Sprintf("given[%d]", i)
		w, text := r.given(path, g, i == 0)
		want.Given = append(want.Given, w)
		b.WriteString(text)
	}

	prev := kindNone
	if len(s.Given) > 0 {
		prev = kindContext
	}
	for i, st := range s.Steps {
		path := fmt.Sprintf("steps[%d]", i)
		var w domain.Step
		var text string
		w, text, prev = r.step(path, st, prev)
		want.Steps = append(want.Steps, w)
		b.WriteString(text)
	}
	if r.err != nil {
		return nil, r.err
	}

	out := []byte(b.String())
	got, err := Parse(out, "scenario "+string(s.ID))
	if err != nil {
		return nil, fmt.Errorf("scenario %s cannot be written as Gherkin that Parse accepts: %w", s.ID, err)
	}
	if path := difference(want, got); path != "" {
		return nil, fmt.Errorf("%s: cannot be written as Gherkin: Parse would read it back as a different value", path)
	}
	if domain.ExpectHash(got) != domain.ExpectHash(s) {
		return nil, fmt.Errorf("scenario %s cannot be written: its expectation hash would change", s.ID)
	}
	return out, nil
}

type renderer struct{ err error }

func (r *renderer) fail(path, format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...))
	}
}

// word refuses text that a tag or a name on a line cannot hold.
func (r *renderer) word(path, text string) {
	r.oneLine(path, text)
	if strings.ContainsAny(text, " \t#@") {
		r.fail(path, "must be one word without a space, #, or @")
	}
}

func (r *renderer) oneLine(path, text string) {
	switch {
	case !utf8.ValidString(text):
		r.fail(path, "the text is not valid UTF-8")
	case strings.ContainsAny(text, "\n\r"):
		r.fail(path, "must be one line: Gherkin has no way to carry a line break here")
	case text != strings.TrimSpace(text):
		r.fail(path, "must not start or end with a space: Gherkin trims it")
	}
}

// featureName is the subject on one line, or the ticket key.
func featureName(subject, ticket string) string {
	if s := strings.Join(strings.Fields(subject), " "); s != "" {
		return s
	}
	return strings.Join(strings.Fields(ticket), " ")
}

// A line of the description that starts with one of these is a part of the scenario for Gherkin: a
// step, a keyword with a colon, a tag, a comment, a table row, or a doc string.
var descriptionTriggers = func() []string {
	d := gherkin.DialectsBuiltin().GetDialect("en")
	list := []string{"@", "#", "|", `"""`, "```"}
	list = append(list, d.StepKeywords()...)
	for _, words := range [][]string{d.FeatureKeywords(), d.RuleKeywords(), d.BackgroundKeywords(), d.ScenarioKeywords(), d.ScenarioOutlineKeywords(), d.ExamplesKeywords()} {
		for _, w := range words {
			list = append(list, w+":")
		}
	}
	return list
}()

// escapedLine is the first character that is not a space of a description line. A line whose text
// would read as a part of the scenario, or that starts with the escape itself, gets a backslash there,
// and Parse takes one off every line that has it.
func needsEscape(trimmed string) bool {
	return strings.HasPrefix(trimmed, `\`) || slices.ContainsFunc(descriptionTriggers, func(t string) bool { return strings.HasPrefix(trimmed, t) })
}

func escapeLine(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if !needsEscape(trimmed) {
		return line
	}
	return line[:len(line)-len(trimmed)] + `\` + trimmed
}

func (r *renderer) intent(text string) {
	if !utf8.ValidString(text) {
		r.fail("intent", "the text is not valid UTF-8")
		return
	}
	if strings.Contains(text, "\r") {
		r.fail("intent", "must not hold a carriage return")
		return
	}
	if strings.HasSuffix(text, "\n") {
		r.fail("intent", "ends with more than one line break: Gherkin drops the blank lines at the end of a description, so remove them")
		return
	}
	if strings.HasPrefix(text, "\n") {
		r.fail("intent", "starts with a blank line: Gherkin drops it, so remove it")
		return
	}
	common := -1
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case line == "":
			continue
		case trimmed == "":
			r.fail("intent", "line %d holds only spaces: make it empty", i+1)
			return
		}
		if n := len(line) - len(trimmed); common < 0 || n < common {
			common = n
		}
	}
	if common > 0 {
		r.fail("intent", "every line starts with a space: Gherkin takes that as indentation, so remove it")
	}
}

// given writes one fixture line, with its parameters as a table of two columns.
func (r *renderer) given(path string, g domain.Given, first bool) (domain.Given, string) {
	want := g
	want.With = nil
	var b strings.Builder
	key := "Given"
	if !first {
		key = "And"
	}
	b.WriteString(indent + key + " " + r.givenLine(path, g) + "\n")
	if len(g.With) > 0 {
		want.With = maps.Clone(g.With)
		keys := slices.Sorted(maps.Keys(g.With))
		var kw, vw int
		for _, k := range keys {
			r.cell(path+".with key "+strconv.Quote(k), k)
			r.cell(path+".with."+k, g.With[k])
			kw, vw = max(kw, utf8.RuneCountInString(escapeCell(k))), max(vw, utf8.RuneCountInString(escapeCell(g.With[k])))
		}
		for _, k := range keys {
			ek, ev := escapeCell(k), escapeCell(g.With[k])
			b.WriteString(tableIndent + "| " + ek + strings.Repeat(" ", kw-utf8.RuneCountInString(ek)) +
				" | " + ev + strings.Repeat(" ", vw-utf8.RuneCountInString(ev)) + " |\n")
		}
	}
	return want, b.String()
}

// givenLine is the sentence of a fixture line, with its lease.
func (r *renderer) givenLine(path string, g domain.Given) string {
	line := fmt.Sprintf("the fixture %s as %s", g.Fixture, g.As)
	switch g.Lease {
	case "":
	case domain.LeaseExclusive:
		line += " with an exclusive lease"
	default:
		r.fail(path+".lease", "lease must be exclusive or empty")
	}
	r.word(path+".fixture", string(g.Fixture))
	r.word(path+".as", g.As)
	return line
}

func (r *renderer) cell(path, text string) {
	switch {
	case !utf8.ValidString(text):
		r.fail(path, "the text is not valid UTF-8")
	case text != strings.TrimSpace(text):
		r.fail(path, "must not start or end with a space: Gherkin trims it")
	}
}

var cellEscaper = strings.NewReplacer(`\`, `\\`, "|", `\|`, "\n", `\n`)

func escapeCell(s string) string { return cellEscaper.Replace(s) }

type stepKind int

const (
	kindNone stepKind = iota
	kindContext
	kindAction
	kindOutcome
)

// step writes a setup step as a Given and an action step as a When, each with a comment that holds
// its ID and request hint, then the expectations as Then lines. A keyword that repeats the one before
// is And. It returns the kind of the last line.
func (r *renderer) step(path string, st domain.Step, prev stepKind) (domain.Step, string, stepKind) {
	want := st
	want.Expect = nil
	var b strings.Builder

	k, word := kindAction, "When"
	if st.Setup {
		k, word = kindContext, "Given"
	}
	if prev == k {
		word = "And"
	}
	r.oneLine(path+".id", string(st.ID))
	if strings.Contains(string(st.ID), ";") || st.ID == "" {
		r.fail(path+".id", "must not be empty or hold a semicolon")
	}
	r.oneLine(path+".action", st.Action)
	if st.Action == "" {
		r.fail(path+".action", "is required: a step is a sentence")
	}
	if st.As != "" {
		r.word(path+".as", st.As)
		if strings.Contains(st.As, ",") {
			r.fail(path+".as", "must not hold a comma")
		}
	}
	text := stepText(st)
	b.WriteString(indent + word + " " + text + "\n")
	b.WriteString(indent + "# step: " + string(st.ID))
	if st.Hint != nil {
		r.oneLine(path+".hint.method", st.Hint.Method)
		r.oneLine(path+".hint.path", st.Hint.Path)
		if st.Hint.Method == "" || st.Hint.Path == "" || strings.ContainsAny(st.Hint.Method, " \t") {
			r.fail(path+".hint", "needs a method of one word and a path")
		}
		b.WriteString("; " + st.Hint.Method + " " + st.Hint.Path)
	}
	b.WriteString("\n")
	last := k

	if st.Setup && len(st.Expect) > 0 {
		r.fail(path+".expect", "a setup step is a Given and has no expected result: Gherkin puts a Then under a When")
	}
	for i, ex := range st.Expect {
		p := fmt.Sprintf("%s.expect[%d]", path, i)
		word := "Then"
		if last == kindOutcome {
			word = "And"
		}
		last = kindOutcome
		sentence, w := r.check(p+".check", ex.Check)
		ex.Check = w
		want.Expect = append(want.Expect, ex)
		b.WriteString(indent + word + " " + sentence + "\n")
		b.WriteString(indent + "# " + r.source(p, ex) + "\n")
	}
	return want, b.String(), last
}

// stepText is the sentence of a step: its action, led by the account that it acts as.
func stepText(st domain.Step) string {
	if st.As != "" {
		return "as " + st.As + ", " + st.Action
	}
	return st.Action
}

func (r *renderer) source(path string, ex domain.Expectation) string {
	r.oneLine(path+".source", string(ex.Source))
	if ex.Source == "" || strings.ContainsAny(string(ex.Source), " \t") {
		r.fail(path+".source", "must be one word")
	}
	r.oneLine(path+".cite", ex.Cite)
	switch ex.By {
	case "", "model":
	default:
		r.fail(path+".by", "by must be model or empty")
	}
	text := string(ex.Source)
	if ex.Cite != "" {
		text += " " + ex.Cite
	}
	if ex.By == "model" {
		text += ", by model"
	}
	return text
}

// check writes a check as the sentence of a Then line. It also returns the check as Parse gives it
// back: an operand takes its normal form.
func (r *renderer) check(path string, c domain.Check) (string, domain.Check) {
	want := c
	kinds := 0
	subject := ""
	if c.Status != nil {
		kinds++
		subject = "the status"
	}
	if c.JSONPath != "" {
		kinds++
		subject = c.JSONPath
		if !strings.HasPrefix(c.JSONPath, "$") {
			r.fail(path+".jsonpath", "%q must start with $", c.JSONPath)
		}
	}
	if c.Header != "" {
		kinds++
		subject = "the header " + c.Header
		if strings.ContainsAny(c.Header, " \t\n\r") {
			r.fail(path+".header", "%q must be one word", c.Header)
		}
	}
	if c.Element != "" {
		kinds++
		subject = "the element " + c.Element
		if strings.ContainsAny(c.Element, " \t\n\r") {
			r.fail(path+".element", "%q must be one word", c.Element)
		}
		if c.Attribute != "" {
			if !attributeName.MatchString(c.Attribute) {
				r.fail(path+".attribute", "%q is not the name of an attribute", c.Attribute)
			}
			subject += " attribute " + c.Attribute
		}
	}
	if c.Page != "" {
		kinds++
		subject = "the page " + c.Page
		if c.Page != domain.PagePath && c.Page != domain.PageTitle {
			r.fail(path+".page", "%q must be path or title", c.Page)
		}
	}
	if kinds != 1 {
		r.fail(path, "a check needs exactly one of status, jsonpath, header, element, page")
		return "", want
	}
	if c.Attribute != "" && c.Element == "" {
		r.fail(path+".attribute", "an attribute belongs to an element check")
	}
	if subject != strings.TrimSpace(subject) || strings.ContainsAny(subject, "\n\r") {
		r.fail(path, "the subject %q must be one line without spaces at its ends", subject)
	}

	var ops []string
	add := func(set bool, text string) {
		if set {
			ops = append(ops, text)
		}
	}
	for _, o := range []struct {
		key string
		val *any
		fmt string
	}{
		{"equals", &want.Equals, "equals %s"},
		{"notEquals", &want.NotEquals, "does not equal %s"},
		{"contains", &want.Contains, "contains %s"},
	} {
		if *o.val == nil {
			continue
		}
		text, norm := r.literal(path+"."+o.key, *o.val, 0)
		*o.val = norm
		add(true, fmt.Sprintf(o.fmt, text))
	}
	if c.State != "" {
		words, known := domain.ElementStates[c.State]
		if !known || c.Element == "" {
			r.fail(path+".state", "%q is not a state of an element", c.State)
		}
		add(true, words)
	}
	if c.Exists != nil {
		if *c.Exists {
			add(true, "exists")
		} else {
			add(true, "does not exist")
		}
	}
	for _, b := range []struct {
		key  string
		val  *float64
		text string
	}{{"greater", c.Greater, "is greater than "}, {"less", c.Less, "is less than "}} {
		if b.val == nil {
			continue
		}
		if math.IsNaN(*b.val) || math.IsInf(*b.val, 0) {
			r.fail(path+"."+b.key, "%v is not a finite number", *b.val)
			continue
		}
		add(true, b.text+floatText(*b.val, false))
	}
	if c.Count != nil {
		unit := "items"
		if *c.Count == 1 {
			unit = "item"
		}
		add(true, fmt.Sprintf("has %d %s", *c.Count, unit))
	}
	if c.EqualsPath != "" {
		r.oneLine(path+".equalsPath", c.EqualsPath)
		add(true, "equals the value at "+c.EqualsPath)
	}

	if c.Status != nil {
		if len(ops) > 0 {
			r.fail(path, "a status check takes no operator")
		}
		return fmt.Sprintf("the status is %d", *c.Status), want
	}
	if len(ops) != 1 {
		r.fail(path, "a jsonpath, header, or element check needs exactly one operator")
		return "", want
	}
	return subject + " " + ops[0], want
}

// literal writes v as a JSON value on one line, and returns the value that Parse gives back for it.
func (r *renderer) literal(path string, v any, depth int) (string, any) {
	if depth > maxDepth {
		r.fail(path, "nested more than %d levels", maxDepth)
		return "null", nil
	}
	switch x := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(x), x
	case string:
		if !utf8.ValidString(x) {
			r.fail(path, "the text is not valid UTF-8")
			return `""`, x
		}
		return jsonString(x), x
	case int:
		return strconv.Itoa(x), x
	case int64:
		return strconv.FormatInt(x, 10), int(x)
	case uint64:
		if x <= math.MaxInt64 {
			return strconv.FormatUint(x, 10), int(x)
		}
		return strconv.FormatUint(x, 10), x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			r.fail(path, "%v is not a finite number", x)
			return "null", nil
		}
		return floatText(x, true), x
	case json.Number:
		return r.number(path, x)
	case map[string]any:
		if x == nil {
			r.fail(path, "a nil object cannot be told from null")
			return "null", nil
		}
		out := make(map[string]any, len(x))
		var b strings.Builder
		b.WriteString("{")
		for i, k := range slices.Sorted(maps.Keys(x)) {
			if i > 0 {
				b.WriteString(", ")
			}
			if !utf8.ValidString(k) {
				r.fail(path, "a key is not valid UTF-8")
			}
			text, norm := r.literal(path+"."+k, x[k], depth+1)
			b.WriteString(jsonString(k) + ": " + text)
			out[k] = norm
		}
		b.WriteString("}")
		return b.String(), out
	case []any:
		if x == nil {
			r.fail(path, "a nil list cannot be told from null")
			return "null", nil
		}
		out := make([]any, len(x))
		parts := make([]string, len(x))
		for i, el := range x {
			parts[i], out[i] = r.literal(fmt.Sprintf("%s[%d]", path, i), el, depth+1)
		}
		return "[" + strings.Join(parts, ", ") + "]", out
	}
	r.fail(path, "a value of type %T cannot be written", v)
	return "null", nil
}

// jsonString is s as a JSON string literal. A character that HTML would escape stays as it is.
func jsonString(s string) string {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	return strings.TrimRight(buf.String(), "\n")
}

var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// number writes a JSON number as its own text. The expectation hash covers the number as
// encoding/json writes the value that Parse reads, so a text that differs from it is refused.
func (r *renderer) number(path string, n json.Number) (string, any) {
	text := string(n)
	if !jsonNumber.MatchString(text) {
		r.fail(path, "%q is not a JSON number", text)
		return "null", nil
	}
	var value any
	if !strings.ContainsAny(text, ".eE") {
		if i, err := strconv.ParseInt(text, 10, 64); err == nil {
			value = int(i)
		} else if u, err := strconv.ParseUint(text, 10, 64); err == nil {
			value = u
		} else {
			r.fail(path, "%s is a whole number too wide to be held exactly", text)
			return "null", nil
		}
	} else {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(f, 0) {
			r.fail(path, "%s is outside the range of a number", text)
			return "null", nil
		}
		if decimalKey(text) != decimalKey(strconv.FormatFloat(f, 'e', -1, 64)) {
			r.fail(path, "%s cannot be held exactly: it would be read as %s", text, strconv.FormatFloat(f, 'g', -1, 64))
			return "null", nil
		}
		value = f
	}
	if stored, err := json.Marshal(value); err != nil || string(stored) != text {
		r.fail(path, "the number %s is stored as %s, which changes the expectation hash: write %s", text, stored, stored)
		return "null", nil
	}
	return text, value
}

var decimalParts = regexp.MustCompile(`^(-?)([0-9]+)(?:\.([0-9]*))?(?:[eE]([-+]?[0-9]+))?$`)

// decimalKey names the value of a decimal text so that two texts of one value have one key.
func decimalKey(text string) string {
	m := decimalParts.FindStringSubmatch(text)
	if m == nil {
		return "?" + text
	}
	exp := 0
	if m[4] != "" {
		n, err := strconv.Atoi(m[4])
		if err != nil {
			return "?" + text
		}
		exp = n
	}
	digits := strings.TrimLeft(m[2]+m[3], "0")
	exp -= len(m[3])
	trimmed := strings.TrimRight(digits, "0")
	exp += len(digits) - len(trimmed)
	if trimmed == "" {
		return "0"
	}
	return m[1] + trimmed + "e" + strconv.Itoa(exp)
}

// floatText is the shortest decimal that reads back as f. When whole is set, a text that has no
// point or exponent gets ".0" so that it reads back as a float and not as an integer.
func floatText(f float64, whole bool) string {
	var s string
	if a := math.Abs(f); a == 0 || (a >= 1e-6 && a < 1e21) {
		s = strconv.FormatFloat(f, 'f', -1, 64)
	} else {
		s = strconv.FormatFloat(f, 'e', -1, 64)
		if i := strings.IndexByte(s, 'e'); i >= 0 {
			exp := strings.TrimLeft(s[i+2:], "0")
			if exp == "" {
				exp = "0"
			}
			s = s[:i+2] + exp
		}
	}
	if whole && !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// difference names the first field in which two scenarios differ, or is empty when they are equal.
func difference(want, got domain.Scenario) string {
	switch {
	case want.ID != got.ID:
		return "id"
	case want.Title != got.Title:
		return "title"
	case want.Ticket != got.Ticket:
		return "ticket"
	case want.Intent != got.Intent:
		return "intent"
	case want.Surface != got.Surface:
		return "surface"
	case want.AuthProfile != got.AuthProfile:
		return "authProfile"
	case !slices.Equal(want.Suites, got.Suites):
		return "suites"
	}
	if len(want.Given) != len(got.Given) {
		return "given"
	}
	for i := range want.Given {
		if !reflect.DeepEqual(want.Given[i], got.Given[i]) {
			return fmt.Sprintf("given[%d]", i)
		}
	}
	if len(want.Steps) != len(got.Steps) {
		return "steps"
	}
	for i, w := range want.Steps {
		g := got.Steps[i]
		p := fmt.Sprintf("steps[%d]", i)
		switch {
		case w.ID != g.ID:
			return p + ".id"
		case w.Action != g.Action:
			return p + ".action"
		case w.As != g.As:
			return p + ".as"
		case w.Setup != g.Setup:
			return p + ".setup"
		case !reflect.DeepEqual(w.Hint, g.Hint):
			return p + ".hint"
		case len(w.Expect) != len(g.Expect):
			return p + ".expect"
		}
		for j := range w.Expect {
			if !reflect.DeepEqual(w.Expect[j], g.Expect[j]) {
				return fmt.Sprintf("%s.expect[%d]", p, j)
			}
		}
	}
	if !reflect.DeepEqual(want, got) {
		return "scenario"
	}
	return ""
}
