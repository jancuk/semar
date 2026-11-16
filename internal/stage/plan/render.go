package plan

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/jancuk/semar/internal/changeset"
	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/prompt"
	"github.com/jancuk/semar/internal/provider"
)

const (
	maxTextShown  = 2000
	maxValueShown = 200
)

// Render writes plan.md: what the planner found, what it drafted, what it left out, and how
// the plan was made. Every text from the draft or the repository is escaped.
func Render(w io.Writer, in Input, out Output) error {
	var b strings.Builder
	d := out.Draft
	cs := in.Change

	fmt.Fprintf(&b, "# Test plan for %s at %s\n\n", shown(cs.Head, maxValueShown), shortID(cs.HeadCommit))
	if s := strings.TrimSpace(d.Summary); s != "" {
		fmt.Fprintf(&b, "%s\n\n", paragraph(shown(s, maxTextShown)))
	}

	b.WriteString("## The change\n\n")
	var table strings.Builder
	if err := changeset.Table(&table, cs); err != nil {
		return fmt.Errorf("render plan: %w", err)
	}
	for _, line := range strings.SplitAfter(strings.TrimRight(table.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "## ") {
			b.WriteString("#")
		}
		b.WriteString(line)
	}
	b.WriteString("\n\n## Tickets\n")

	fixtures := fixtureIndex(in.Fixtures)
	after := map[domain.ScenarioID]bool{}
	for _, id := range out.AfterTrial {
		after[id] = true
	}
	ids := make([]domain.ScenarioID, len(out.Scenarios))
	for i, sc := range out.Scenarios {
		ids[i] = sc.ID
	}
	accounting := Accounting(d, in.Tickets, ids)
	tickets := slices.Clone(d.Tickets)
	slices.SortStableFunc(tickets, func(a, b TicketDraft) int { return strings.Compare(a.Key, b.Key) })
	for _, t := range tickets {
		fmt.Fprintf(&b, "\n### %s\n\n", shown(t.Key, maxValueShown))
		fmt.Fprintf(&b, "Kind: %s. Risk: %s", shown(t.Kind, maxValueShown), shown(t.Risk, maxValueShown))
		if r := strings.TrimRight(strings.TrimSpace(t.Reason), "."); r != "" {
			fmt.Fprintf(&b, ", because %s", shown(r, maxTextShown))
		}
		b.WriteString(".\n")
		if s := strings.TrimSpace(t.Behaviour); s != "" {
			fmt.Fprintf(&b, "\nBehaviour: %s\n", shown(s, maxTextShown))
		}
		drafted := 0
		for _, sc := range out.Scenarios {
			if sc.Ticket != t.Key {
				continue
			}
			drafted++
			writeScenario(&b, sc, fixtures, after[sc.ID], d.Differences)
		}
		if drafted == 0 {
			b.WriteString("\nNo scenario was drafted for this ticket.\n")
		}
		writeManualChecks(&b, d.ManualChecks, t.Key)
		writeCriteria(&b, accounting, t.Key)
	}

	b.WriteString("\n## Not drafted\n\n")
	if len(d.NotDrafted) == 0 {
		b.WriteString("Nothing.\n")
	}
	for _, n := range d.NotDrafted {
		fmt.Fprintf(&b, "- %s: %s. Needs: %s\n", shown(n.Ticket, maxValueShown), shown(n.What, maxTextShown), shown(n.Needs, maxTextShown))
	}

	writeFixturesNeeded(&b, d.FixturesNeeded)
	writeJourneysToRerun(&b, in, out)

	b.WriteString("\n## Files that no scenario covers\n\n")
	if len(d.Uncovered) == 0 {
		b.WriteString("None.\n")
	}
	for _, u := range d.Uncovered {
		fmt.Fprintf(&b, "- %s: %s\n", shown(u.File, maxValueShown), shown(u.Reason, maxTextShown))
	}

	writeTrial(&b, in, out)

	if len(in.Knowledge) > 0 {
		writeDocuments(&b, in, out)
	}

	if len(in.LeftOut) > 0 {
		b.WriteString("\n## Files left out of the copy\n\nThe planner could not read these files of the head commit.\n\n")
		for _, l := range in.LeftOut {
			fmt.Fprintf(&b, "- %s: %s\n", shown(l.Path, maxValueShown), shown(l.Reason, maxValueShown))
		}
	}

	r := out.Result
	b.WriteString("\n## How this plan was made\n\n")
	fmt.Fprintf(&b, "- Provider: %s (%s)\n", shown(out.PlannerID, maxValueShown), shown(out.PlannerKind, maxValueShown))
	fmt.Fprintf(&b, "- Model: %s\n", shown(valueOr(r.Model, "the harness default"), maxValueShown))
	if level := valueOr(r.Reasoning, in.Reasoning); level != "" {
		fmt.Fprintf(&b, "- Reasoning: %s\n", shown(level, maxValueShown))
	}
	fmt.Fprintf(&b, "- Attempts: %d of %d\n", out.Attempts, MaxAttempts)
	writeRefused(&b, out.Refused)
	fmt.Fprintf(&b, "- Tokens: %d input, %d output\n", r.InputTokens, r.OutputTokens)
	fmt.Fprintf(&b, "- Setup: %s\n", setupLine(in.SetupOn, r.Loaded))
	fmt.Fprintf(&b, "- Tool calls: %s\n", toolLine(r.Tools))
	fmt.Fprintf(&b, "- Refused tool calls: %d\n", r.Denied)
	fmt.Fprintf(&b, "- Prompt version: %s\n", shown(prompt.PlanVersion(), maxValueShown))
	b.WriteString("\nEvery expected result here was read from the code by a model. Confirm each scenario before its result counts.\n")
	if trialRan(out) {
		b.WriteString("\nThe read-only requests were tried once. The others were not sent before the first run.\n")
	} else {
		b.WriteString("\nThe requests were drafted from the source and were not sent before the first run.\n")
	}
	b.WriteString("\nA request uses a literal ID only when the same scenario captured it or the source holds it as a fixed test value. " +
		"The planner was told to list every other case under \"Not drafted\". Code cannot check this, so read the requests before the first run.\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// writeDocuments lists the configured sources, the notes of code, and the differences that the
// planner reported.
func writeDocuments(b *strings.Builder, in Input, out Output) {
	value := func(s string) string { return shown(s, maxValueShown) }
	b.WriteString("\n## Documents\n\nThe planner was told to read these before it drafted.\n\n")
	apis := 0
	for _, s := range in.Knowledge {
		if !s.Usable() {
			fmt.Fprintf(b, "- %s: %s\n", value(s.ID), value(s.Problem))
			continue
		}
		fmt.Fprintf(b, "- %s: %s\n", value(s.ID), describe(s, value, ": "+value(s.Note)))
		if s.API != nil {
			apis++
		}
	}

	if apis > 0 {
		b.WriteString("\n### Compared with the API document\n\nCode compared every drafted request with the API document.\n\n")
		if len(out.Notes) == 0 {
			b.WriteString("No drafted request differs from the API document.\n")
		}
		for _, n := range out.Notes {
			source := ""
			if apis > 1 {
				source = ", " + value(n.Source)
			}
			fmt.Fprintf(b, "- %s, step %s%s: %s\n", value(string(n.Scenario)), value(n.Step), source, shown(n.Message, maxTextShown))
		}
	}

	b.WriteString("\n### Where the planner says a document and the code differ\n\n" +
		"The planner reported these. Code checked that both places exist, and did not check that they differ.\n\n")
	if len(out.Draft.Differences) == 0 {
		b.WriteString("The planner reported no difference.\n")
	}
	for _, d := range out.Draft.Differences {
		fmt.Fprintf(b, "- %s: %s and %s. %s\n", value(d.Ticket), value(citeOf(d.Document)), value(citeOf(d.Code)), shown(d.What, maxTextShown))
		fmt.Fprintf(b, "  - Document: %s\n  - Code: %s\n", shown(d.Document, maxTextShown), shown(d.Code, maxTextShown))
	}
}

// citeOf is the <file>:<line> that a validated text starts with.
func citeOf(text string) string {
	if m := citePattern.FindStringSubmatch(text); m != nil {
		return m[1] + ":" + m[2]
	}
	return text
}

// setupLine counts what the planner loaded and never names it: the file may be committed, and
// the names of a person's MCP servers are theirs.
func setupLine(setupOn bool, l provider.Loaded) string {
	switch {
	case !setupOn:
		return "the planner had the three file tools only"
	case l.PermissionMode == "" && l.Skills == 0 && len(l.MCPServers) == 0:
		return "the planner could use your own setup, and the provider did not report what it loaded"
	}
	mode := ""
	if l.PermissionMode != "" {
		mode = ", under the permission mode " + shown(l.PermissionMode, maxValueShown)
	}
	return fmt.Sprintf("the planner could use %d MCP servers and %d skills of your own setup%s", len(l.MCPServers), l.Skills, mode)
}

// toolLine counts MCP tool calls together, because the name of such a tool holds the name of
// its server.
func toolLine(tools []provider.ToolUse) string {
	var parts []string
	mcp := 0
	for _, t := range tools {
		if strings.HasPrefix(t.Name, "mcp__") {
			mcp += t.Calls
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d", shown(t.Name, maxValueShown), t.Calls))
	}
	if mcp > 0 {
		parts = append(parts, fmt.Sprintf("MCP tools (names withheld) %d", mcp))
	}
	if len(parts) == 0 {
		return "none reported"
	}
	return strings.Join(parts, ", ")
}

// writeFixturesNeeded lists the fixtures that the planner proposed. A proposal is text of the
// model, so every part is escaped. It never counts: a person writes the file and approves it.
func writeFixturesNeeded(b *strings.Builder, needed []FixtureNeeded) {
	b.WriteString("\n## Fixtures needed\n\n")
	if len(needed) == 0 {
		b.WriteString("None.\n")
		return
	}
	value := func(s string) string { return shown(s, maxValueShown) }
	list := func(items []string) string {
		if len(items) == 0 {
			return "none"
		}
		out := make([]string, len(items))
		for i, s := range items {
			out[i] = value(s)
		}
		return strings.Join(out, ", ")
	}
	sentence := func(s string) string { return shown(strings.TrimRight(strings.TrimSpace(s), "."), maxTextShown) }
	for _, n := range needed {
		p := n.Proposal
		fmt.Fprintf(b, "- %s: %s. Proposed fixture %s: %s. Parameters: %s. Outputs: %s. Services: %s.\n",
			value(n.Ticket), sentence(n.What), value(p.ID), sentence(p.Description), list(p.Params), list(p.Outputs), list(p.Services))
		fmt.Fprintf(b, "  A person writes the fixture in .semar/fixtures/%s.yaml, approves it, and plans again.\n", value(p.ID))
	}
}

func writeScenario(b *strings.Builder, sc domain.Scenario, fixtures map[domain.FixtureID]fixture.Entry, afterTrial bool, differences []Difference) {
	fmt.Fprintf(b, "\n#### %s: %s\n\n", shown(string(sc.ID), maxValueShown), shown(sc.Title, maxValueShown))
	if s := strings.TrimSpace(sc.Intent); s != "" {
		fmt.Fprintf(b, "%s\n\n", paragraph(shown(s, maxTextShown)))
	}
	if unmet := unmetCriteria(sc, differences); len(unmet) > 0 {
		quoted := make([]string, len(unmet))
		for i, c := range unmet {
			quoted[i] = shown(c, maxValueShown)
		}
		fmt.Fprintf(b, "This scenario is expected to fail on this build: the code does not meet %s.\n\n", andList(quoted))
	}
	if afterTrial {
		b.WriteString("First drafted after the trial run. Check that its expected results come from the code.\n\n")
	}
	for _, g := range sc.Given {
		note := ""
		if e, ok := fixtures[g.Fixture]; ok && !e.Approved {
			note = fmt.Sprintf(" (not approved yet: semar confirm --fixture %s)", shown(string(g.Fixture), maxValueShown))
		}
		fmt.Fprintf(b, "Given: %s from %s%s\n\n", shown(g.As, maxValueShown), shown(string(g.Fixture), maxValueShown), note)
	}
	for i, st := range sc.Steps {
		route := ""
		if st.Hint != nil {
			route = st.Hint.Method + " " + st.Hint.Path
		}
		setup := ""
		if st.Setup {
			setup = " (setup)"
		}
		as := ""
		if st.As != "" {
			as = " as " + shown(st.As, maxValueShown)
		}
		fmt.Fprintf(b, "%d. %s%s%s: %s\n", i+1, shown(route, maxValueShown), setup, as, shown(st.Action, maxTextShown))
		for _, e := range st.Expect {
			from := ""
			if e.Source == domain.SourceTicket {
				from = " Source: ticket."
			}
			fmt.Fprintf(b, "   - Expects %s.%s Cite: %s\n", shown(check.DescribeFull(e.Check), maxValueShown), from, shown(e.Cite, maxTextShown))
		}
	}
}

// unmetCriteria are the places in a ticket file that a scenario expects and that the planner says
// the code does not meet: the cite of a ticket expectation equals the document of a difference
// of the same ticket. The places come in the order of the scenario, each once.
func unmetCriteria(sc domain.Scenario, differences []Difference) []string {
	var out []string
	for _, st := range sc.Steps {
		for _, e := range st.Expect {
			if e.Source != domain.SourceTicket {
				continue
			}
			cite := citeOf(e.Cite)
			if slices.Contains(out, cite) {
				continue
			}
			if slices.ContainsFunc(differences, func(d Difference) bool { return d.Ticket == sc.Ticket && citeOf(d.Document) == cite }) {
				out = append(out, cite)
			}
		}
	}
	return out
}

// writeManualChecks lists the checks that only a person can make, under their ticket.
func writeManualChecks(b *strings.Builder, checks []ManualCheckDraft, ticket string) {
	var mine []ManualCheckDraft
	for _, m := range checks {
		if m.Ticket == ticket {
			mine = append(mine, m)
		}
	}
	if len(mine) == 0 {
		return
	}
	b.WriteString("\nManual checks for a person:\n")
	for i, m := range mine {
		fmt.Fprintf(b, "\n%d. %s. Cite: %s\n", i+1, shown(strings.TrimRight(strings.TrimSpace(m.Title), "."), maxTextShown), shown(m.Cite, maxTextShown))
		for _, s := range m.Steps {
			fmt.Fprintf(b, "   - %s\n", shown(s, maxTextShown))
		}
		fmt.Fprintf(b, "   - Expected: %s\n", shown(m.Expected, maxTextShown))
	}
}

// writeCriteria is the table of the acceptance criteria of a ticket file, with what cites each
// one (D9). A criterion that nothing cites says so, and becomes a gap obligation.
func writeCriteria(b *strings.Builder, accounting []Accounted, ticket string) {
	var mine []Accounted
	for _, a := range accounting {
		if a.Ticket == ticket {
			mine = append(mine, a)
		}
	}
	if len(mine) == 0 {
		return
	}
	b.WriteString("\nAcceptance criteria:\n\n| Line | No. | Criterion | Cited by |\n| --- | --- | --- | --- |\n")
	for _, a := range mine {
		by := "not accounted for"
		if len(a.CitedBy) > 0 {
			cited := make([]string, len(a.CitedBy))
			for i, c := range a.CitedBy {
				cited[i] = shown(c, maxValueShown)
			}
			by = strings.Join(cited, "; ")
		}
		fmt.Fprintf(b, "| %d | %d | %s | %s |\n", a.Line, a.Number, shown(a.Text, maxCriterionText), by)
	}
}

const maxRefusedShown = 30

// writeRefused lists the problems of every draft that went back to the planner, so that a person
// can judge a plan whose earlier drafts are not in it.
func writeRefused(b *strings.Builder, refused []RefusedDraft) {
	if len(refused) == 0 {
		return
	}
	fmt.Fprintf(b, "- Drafts that went back to the planner: %d\n", len(refused))
	for _, r := range refused {
		fmt.Fprintf(b, "  - Draft %d had %s:\n", r.Attempt, counted(len(r.Problems), "problem"))
		for i, p := range r.Problems {
			if i == maxRefusedShown {
				fmt.Fprintf(b, "    - and %d more\n", len(r.Problems)-i)
				break
			}
			if p.Path == "" {
				fmt.Fprintf(b, "    - %s\n", shown(p.Message, maxTextShown))
				continue
			}
			fmt.Fprintf(b, "    - %s: %s\n", shown(p.Path, maxValueShown), shown(p.Message, maxTextShown))
		}
	}
}

var listStart = regexp.MustCompile(`^([0-9]+)([.)])`)

// paragraph keeps escaped text that starts a line from starting a list.
func paragraph(s string) string {
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return `\` + s
	}
	return listStart.ReplaceAllString(s, `$1\$2`)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// shown cuts a text and escapes it, so the mark of the cut stays readable.
func shown(s string, limit int) string {
	rs := []rune(s)
	if len(rs) <= limit {
		return escape(s)
	}
	return escape(string(rs[:limit])) + fmt.Sprintf("… [cut: %d more characters]", len(rs)-limit)
}

// escape makes text safe inside one line of Markdown, with the rules of the run report:
// format characters are dropped, Markdown punctuation and HTML are escaped, no scheme or
// "www." becomes a link, "$" and "@" make no math or mention, and a line break shows as ↵.
func escape(s string) string {
	var b strings.Builder
	rs := make([]rune, 0, len(s))
	for _, r := range s {
		if !unicode.Is(unicode.Cf, r) {
			rs = append(rs, r)
		}
	}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '&':
			b.WriteString("&amp;")
		case r == '_' && i > 0 && i+1 < len(rs) && isWordRune(rs[i-1]) && isWordRune(rs[i+1]):
			b.WriteRune(r)
		case strings.ContainsRune("\\`*_[]|~#$@", r):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == ':' && i+2 < len(rs) && rs[i+1] == '/' && rs[i+2] == '/':
			b.WriteString(`\:`)
		case r == '.' && i >= 3 && strings.EqualFold(string(rs[i-3:i]), "www"):
			b.WriteString(`\.`)
		case r == '\r' && i+1 < len(rs) && rs[i+1] == '\n':
		case r == '\n' || r == '\r' || r == ' ' || r == ' ':
			b.WriteString("↵")
		case unicode.IsControl(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func trialRan(out Output) bool { return out.Trial != nil && out.Trial.Skipped == "" }

// writeTrial shows each trial result beside its step. It shows the route template and the
// status, never a rendered path, an error text, or a value of a response (D11).
func writeTrial(b *strings.Builder, in Input, out Output) {
	tr := out.Trial
	switch {
	case tr == nil && in.TrialOff == "":
		return
	case tr == nil:
		fmt.Fprintf(b, "\n## Trial run\n\nThe trial run was %s.\n", shown(in.TrialOff, maxTextShown))
		return
	case tr.Skipped != "":
		fmt.Fprintf(b, "\n## Trial run\n\nThe trial run was skipped: %s.\n", shown(strings.TrimRight(tr.Skipped, "."), maxTextShown))
		return
	}
	value := func(s string) string { return shown(s, maxValueShown) }
	b.WriteString("\n## Trial run\n\n")
	fmt.Fprintf(b, "Code sent the read-only steps once to %s before the plan was written. %s ", value(tr.Env), buildsSentence(tr.Builds, value))
	b.WriteString("A trial result is evidence about a request. It is not a test result, and no expected result was compared.")
	if tr.Run != "" {
		fmt.Fprintf(b, " Run %s holds every request: semar activity %s.", value(string(tr.Run)), value(string(tr.Run)))
	}
	b.WriteString("\n")
	if len(out.AfterTrial) > 0 {
		b.WriteString("\nThe planner kept the expected results of each scenario that it kept, matched by ticket and title. A scenario that was renamed counts as a new one.\n")
	}
	if out.UsedAttempt != 0 && out.UsedAttempt != out.Attempts {
		fmt.Fprintf(b, "\nThe last draft had problems, so this plan is draft %d of %d, which passed.\n", out.UsedAttempt, out.Attempts)
	}

	for i, sc := range out.Scenarios {
		var steps []TrialStep
		for _, ts := range tr.Steps {
			if ts.Scenario == sc.ID {
				steps = append(steps, ts)
			}
		}
		if len(steps) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n### %s %s\n\n", value(string(sc.ID)), value(sc.Title))
		for _, ts := range steps {
			fmt.Fprintf(b, "- %s\n", trialLine(ts, sc, scriptCaptures(out.Scripts, i, ts.Step), value))
		}
	}

	back, second := trialProblems(*tr, out.Scenarios, in.BaseAPI, headDocuments(in.Knowledge), in.AuthProfile)
	if len(second) > 0 {
		b.WriteString("\n### Needs a second look\n\n")
		for _, ts := range second {
			fmt.Fprintf(b, "- %s, step %s: %s %s answered %s\n", value(string(ts.Scenario)), value(string(ts.Step)), value(ts.Method), value(ts.Route), shown(secondLookText(ts.Status), maxValueShown))
		}
	}
	if len(back) > 0 {
		b.WriteString("\n### Problems that remain\n\nThe planner had no attempt left to fix these.\n\n")
		for _, p := range back {
			if p.Path == "" {
				fmt.Fprintf(b, "- %s\n", shown(p.Message, maxTextShown))
				continue
			}
			fmt.Fprintf(b, "- %s: %s\n", value(p.Path), shown(p.Message, maxTextShown))
		}
	}
}

func buildsSentence(builds []domain.BuildRef, value func(string) string) string {
	if len(builds) == 0 {
		return "The build that the environment runs is not known."
	}
	parts := make([]string, len(builds))
	for i, bl := range builds {
		parts[i] = value(bl.Service) + " " + value(bl.Commit[:min(len(bl.Commit), 7)])
	}
	return "The environment ran " + strings.Join(parts, ", ") + "."
}

// scriptCaptures are the names of the captures of one step of the script of scenario i.
func scriptCaptures(scripts []domain.Script, i int, step domain.StepID) []string {
	if i >= len(scripts) || step == "" {
		return nil
	}
	for _, s := range scripts[i].Steps {
		if s.Step == step {
			names := make([]string, len(s.Captures))
			for k, c := range s.Captures {
				names[k] = c.Name
			}
			return names
		}
	}
	return nil
}

func trialLine(ts TrialStep, sc domain.Scenario, captures []string, value func(string) string) string {
	if ts.Given != "" {
		fixtureID := ""
		for _, g := range sc.Given {
			if g.As == ts.Given {
				fixtureID = " (" + value(string(g.Fixture)) + ")"
			}
		}
		head := fmt.Sprintf("given %s%s:", value(ts.Given), fixtureID)
		switch {
		case ts.Result == TrialNotTried:
			return head + " not tried: " + shown(ts.Why, maxValueShown)
		case ts.Found:
			return head + " find found a record"
		}
		return head + " find found no record"
	}
	head := value(string(ts.Step)) + ":"
	if ts.Result == TrialNotTried {
		return head + " not tried: " + shown(ts.Why, maxValueShown)
	}
	on := value(ts.Method) + " " + value(ts.Route)
	if ts.Status == 0 {
		return head + " " + on + " got no answer"
	}
	line := fmt.Sprintf("%s %s answered %d", head, on, ts.Status)
	if ts.Status >= 400 {
		return line
	}
	for _, name := range captures {
		found := " found"
		if slices.Contains(ts.Missing, name) {
			found = " found no value"
		}
		line += ", capture " + value(name) + found
	}
	return line
}
