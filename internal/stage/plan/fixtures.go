package plan

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
)

const (
	maxFixturesShown      = 100 // entries of the catalogue that the prompt lists
	maxFixtureDescription = 300 // characters of a description that the prompt holds
	maxFixturesNeeded     = 10
	maxProposalNames      = 16
	maxProposalDesc       = 500
	maxFixtureIDBytes     = 64
	maxIDsInProblem       = 20

	// The patterns are those of the fixture file (rules F3, F6, and F12), which the planner
	// must follow when it proposes a fixture.
	fixtureIDRule = "must match ^[a-z][a-z0-9_-]*(\\.[a-z][a-z0-9_-]*)*$"
	paramRule     = "must match ^[a-z][a-z0-9_]*$ and not be run or data"
	outputRule    = "must match ^[A-Za-z][A-Za-z0-9_]*$"
)

var (
	fixtureIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)*$`)
	paramPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	outputPattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	blankLine        = regexp.MustCompile(`\n[ \t]*\n`)
)

// fixtureList is the catalogue as the prompt shows it: one line per fixture, with no step. A
// description is text of the repository, so it is cut, put on one line, and stripped of back
// quotes and control characters. Every name is quoted as code.
func fixtureList(entries []fixture.Entry) string {
	var lines []string
	for i, e := range entries {
		if i == maxFixturesShown {
			lines = append(lines, fmt.Sprintf("- and %d more fixtures that are not listed here", len(entries)-i))
			break
		}
		lines = append(lines, fixtureLine(e))
	}
	return strings.Join(lines, "\n")
}

func fixtureLine(e fixture.Entry) string {
	var b strings.Builder
	b.WriteString("- " + codeName(string(e.ID)))
	if !e.Approved {
		b.WriteString(" (not approved yet)")
	}
	desc := strings.TrimRight(strings.ReplaceAll(plain(e.Description, maxFixtureDescription), "`", "'"), ".")
	if desc == "" {
		desc = "(no description)"
	}
	b.WriteString(": " + desc + ".")
	if len(e.Params) > 0 {
		parts := make([]string, len(e.Params))
		for i, p := range e.Params {
			parts[i] = codeName(p.Name) + " (has a default)"
			if p.Required {
				parts[i] = codeName(p.Name) + " (required)"
			}
		}
		b.WriteString(" Parameters: " + strings.Join(parts, ", ") + ".")
	}
	if len(e.Outputs) > 0 {
		names := make([]string, len(e.Outputs))
		for i, o := range e.Outputs {
			names[i] = codeName(o)
		}
		b.WriteString(" Outputs: " + strings.Join(names, ", ") + ".")
	}
	return b.String()
}

func codeName(s string) string {
	return "`" + strings.ReplaceAll(plain(s, 100), "`", "'") + "`"
}

// plain puts a text on one line, drops control and format characters, and cuts it.
func plain(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && !unicode.IsSpace(r)) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	return oneLine(s, limit)
}

// fixtureIndex finds an entry of the catalogue by its ID. The first entry of an ID wins.
func fixtureIndex(entries []fixture.Entry) map[domain.FixtureID]fixture.Entry {
	m := make(map[domain.FixtureID]fixture.Entry, len(entries))
	for _, e := range entries {
		if _, dup := m[e.ID]; !dup {
			m[e.ID] = e
		}
	}
	return m
}

// given turns the given entries of a draft into those of a scenario and checks them against the
// catalogue (V1 and V2). The rules that the scenario file has, such as the names and the count,
// are checked by scenario.Validate. It returns the aliases whose fixture is not known.
func (v *validator) given(sp string, gs []GivenDraft) (out []domain.Given, unknown map[string]bool) {
	for i, g := range gs {
		p := fmt.Sprintf("%s.given[%d]", sp, i)
		out = append(out, domain.Given{Fixture: domain.FixtureID(g.Fixture), As: g.As, With: g.With})
		for _, k := range slices.Sorted(maps.Keys(g.With)) {
			if strings.Contains(g.With[k], "{{") {
				v.add(p+".with."+k, "the value holds {{: write the value itself, because a with value is text and not a variable")
			}
		}
		e, ok := v.fixtures[domain.FixtureID(g.Fixture)]
		if !ok {
			if unknown == nil {
				unknown = map[string]bool{}
			}
			unknown[g.As] = true
		}
		// scenario.Validate says why an ID of the wrong form is refused.
		if !fixtureIDPattern.MatchString(g.Fixture) || len(g.Fixture) > maxFixtureIDBytes {
			continue
		}
		if !ok {
			v.add(p+".fixture", "fixture %s is not in the catalogue; %s", g.Fixture, v.catalogueHint())
			continue
		}
		have := map[string]bool{}
		for _, pe := range e.Params {
			have[pe.Name] = true
		}
		for _, k := range slices.Sorted(maps.Keys(g.With)) {
			if have[k] {
				continue
			}
			if len(e.Params) == 0 {
				v.add(p+".with."+k, "%s is not a parameter of fixture %s; it has no parameters", k, g.Fixture)
				continue
			}
			names := make([]string, len(e.Params))
			for j, pe := range e.Params {
				names[j] = pe.Name
			}
			v.add(p+".with."+k, "%s is not a parameter of fixture %s; its parameters are %s", k, g.Fixture, strings.Join(names, ", "))
		}
		for _, pe := range e.Params {
			if _, set := g.With[pe.Name]; pe.Required && !set {
				v.add(p+".with", "parameter %s is required", pe.Name)
			}
		}
	}
	return out, unknown
}

// catalogueHint tells the planner what to use instead of a fixture that does not exist.
func (v *validator) catalogueHint() string {
	if len(v.in.Fixtures) == 0 {
		return "the catalogue has no fixture, so add a fixturesNeeded entry"
	}
	ids := make([]string, 0, len(v.in.Fixtures))
	for id := range v.fixtures {
		ids = append(ids, string(id))
	}
	slices.Sort(ids)
	more := ""
	if len(ids) > maxIDsInProblem {
		more = fmt.Sprintf(" and %d more", len(ids)-maxIDsInProblem)
		ids = ids[:maxIDsInProblem]
	}
	return fmt.Sprintf("use one of %s%s, or add a fixturesNeeded entry", strings.Join(ids, ", "), more)
}

// usesAlias says whether a problem of the script starts with a variable of one of these aliases.
func usesAlias(message string, aliases map[string]bool) bool {
	for a := range aliases {
		if strings.HasPrefix(message, a+".") {
			return true
		}
	}
	return false
}

// outputsOf maps each alias of a scenario to the outputs of its fixture, for script.ValidateWith.
func (v *validator) outputsOf(given []domain.Given) map[string][]string {
	out := map[string][]string{}
	for _, g := range given {
		if e, ok := v.fixtures[g.Fixture]; ok {
			if _, dup := out[g.As]; !dup {
				out[g.As] = e.Outputs
			}
		}
	}
	return out
}

// fixturesNeeded checks the proposals (V4 and V5).
func (v *validator) fixturesNeeded(d Draft) {
	if len(d.FixturesNeeded) == 0 {
		return
	}
	if n := len(d.FixturesNeeded); n > maxFixturesNeeded {
		v.add("fixturesNeeded", "the draft has %d fixturesNeeded entries, and the limit is %d: keep the ones that block the most scenarios", n, maxFixturesNeeded)
	}
	for i, n := range d.FixturesNeeded {
		p := fmt.Sprintf("fixturesNeeded[%d]", i)
		if !v.known[n.Ticket] {
			v.add(p+".ticket", "ticket %q is not in the change set; use one of %s", n.Ticket, v.ticketList())
		}
		if strings.TrimSpace(n.What) == "" {
			v.add(p+".what", "say what the scenario needs")
		}
		v.proposal(p+".proposal", n.Proposal)
		id := n.Proposal.ID
		if !slices.ContainsFunc(d.NotDrafted, func(nd NotDrafted) bool { return nd.Ticket == n.Ticket && needsNames(nd.Needs, id) }) {
			v.add(p, "add a notDrafted entry for %s whose needs says fixture %s", n.Ticket, id)
		}
	}
}

func (v *validator) proposal(p string, pr FixtureProposal) {
	switch {
	case !fixtureIDPattern.MatchString(pr.ID):
		v.add(p+".id", "id %q "+fixtureIDRule, pr.ID)
	case len(pr.ID) > maxFixtureIDBytes:
		v.add(p+".id", "id %q is longer than %d bytes", pr.ID, maxFixtureIDBytes)
	default:
		if _, ok := v.fixtures[domain.FixtureID(pr.ID)]; ok {
			v.add(p+".id", "%s is in the catalogue already; name it in given", pr.ID)
		}
	}

	d := pr.Description
	switch {
	case strings.TrimSpace(d) == "":
		v.add(p+".description", "description is required")
	case !utf8.ValidString(d):
		v.add(p+".description", "description is not valid UTF-8")
	case utf8.RuneCountInString(d) > maxProposalDesc:
		v.add(p+".description", "description has %d characters; at most %d", utf8.RuneCountInString(d), maxProposalDesc)
	}
	// As in a fixture file, a line feed may fold a long description and no other control character may.
	if strings.ContainsFunc(d, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) {
		v.add(p+".description", "description holds a control character")
	}
	if blankLine.MatchString(strings.TrimSpace(d)) {
		v.add(p+".description", "description must be one paragraph")
	}

	if len(pr.Params) > maxProposalNames {
		v.add(p+".params", "name at most %d params, there are %d", maxProposalNames, len(pr.Params))
	}
	v.nameList(p+".params", pr.Params, func(s string) bool { return paramPattern.MatchString(s) && s != "run" && s != "data" }, paramRule)
	if len(pr.Outputs) < 1 || len(pr.Outputs) > maxProposalNames {
		v.add(p+".outputs", "name 1 to %d outputs: a scenario needs a value to use", maxProposalNames)
	}
	v.nameList(p+".outputs", pr.Outputs, outputPattern.MatchString, outputRule)

	list := strings.Join(v.in.Services, ", ")
	if len(pr.Services) == 0 {
		v.add(p+".services", "name the services that the fixture would call: use some of %s", list)
	}
	seen := map[string]bool{}
	for j, s := range pr.Services {
		switch {
		case !slices.Contains(v.in.Services, s):
			v.add(fmt.Sprintf("%s.services[%d]", p, j), "service %q is not in the configuration; use one of %s", s, list)
		case seen[s]:
			v.add(fmt.Sprintf("%s.services[%d]", p, j), "service %s is listed twice", s)
		}
		seen[s] = true
	}
}

// nameList checks the names of a proposal: the form, and that a name is used once.
func (v *validator) nameList(path string, list []string, ok func(string) bool, rule string) {
	seen := map[string]bool{}
	for i, s := range list {
		p := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case !ok(s):
			v.add(p, "name %q %s", s, rule)
		case seen[s]:
			v.add(p, "name %s is listed twice", s)
		}
		seen[s] = true
	}
}

// needsNames says whether a text of needs names the fixture, as "fixture order.paid", and not a longer ID.
func needsNames(needs, id string) bool {
	if id == "" {
		return false
	}
	key := "fixture " + id
	for from := 0; ; {
		i := strings.Index(needs[from:], key)
		if i < 0 {
			return false
		}
		i += from
		before := i == 0 || !isNameByte(needs[i-1])
		after := needs[i+len(key):]
		if before && endsID(after) {
			return true
		}
		from = i + 1
	}
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
}

// endsID says whether the text after an ID does not continue it. A full stop ends a sentence
// unless a letter follows it, because an ID has dots inside.
func endsID(after string) bool {
	if after == "" {
		return true
	}
	switch c := after[0]; {
	case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
		return false
	case c == '.':
		return len(after) == 1 || after[1] < 'a' || after[1] > 'z'
	}
	return true
}
