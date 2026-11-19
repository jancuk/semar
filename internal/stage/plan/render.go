package plan

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jancuk/semar/internal/changeset"
	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/prompt"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/sends"
)

const (
	maxTextShown  = 2000
	maxValueShown = 200
	// maxSendsShown bounds the Sends line of a step.
	maxSendsShown = 600
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
	accounting := Accounting(d, in.Tickets, ids, out.Again)
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
			writeScenario(&b, sc, scriptOf(out.Scripts, sc.ID), fixtures, after[sc.ID], d.Differences)
		}
		if drafted == 0 {
			b.WriteString("\nNo scenario was drafted for this ticket.\n")
		}
		writeWebTests(&b, d.WebTests, t.Key)
		writeManualChecks(&b, d.ManualChecks, t.Key)
		writeCriteria(&b, accounting, t.Key)
		if out.Pipeline != nil {
			writeCriteriaTip(&b, accounting, t.Key)
		}
	}

	writeAgain(&b, out.Again)

	b.WriteString("\n## Not drafted\n\n")
	if len(d.NotDrafted) == 0 {
		b.WriteString("Nothing.\n")
	}
	for _, n := range d.NotDrafted {
		fmt.Fprintf(&b, "- %s: %s. Needs: %s\n", shown(n.Ticket, maxValueShown), shown(n.What, maxTextShown), shown(n.Needs, maxTextShown))
	}

	writeFixturesDrafted(&b, out)
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
	if out.Pipeline != nil {
		writePipeline(&b, out)
		writeRefused(&b, out.Refused)
	} else {
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
	}
	b.WriteString("\nEvery expected result here was read from the code by a model. Confirm each scenario before its result counts.\n")
	if trialRan(out) {
		b.WriteString("\nThe read-only requests were tried once. The others were not sent before the first run.\n")
	} else {
		b.WriteString("\nThe requests were drafted from the source and were not sent before the first run.\n")
	}
	b.WriteString("\nA request uses a literal ID only when the same scenario captured it or the source holds it as a fixed test value, except an ID that cannot exist, written as `missing-{{run.id}}`, for a not-found test. " +
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

func writeScenario(b *strings.Builder, sc domain.Scenario, script *domain.Script, fixtures map[domain.FixtureID]fixture.Entry, afterTrial bool, differences []Difference) {
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
			note = fmt.Sprintf(" (not approved yet: semar review %s)", shown(string(g.Fixture), maxValueShown))
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
		if line := sendsLine(script, st.ID, route); line != "" {
			fmt.Fprintf(b, "   - Sends %s\n", shown(line, maxSendsShown))
		}
		for _, e := range st.Expect {
			from := ""
			if e.Source == domain.SourceTicket {
				from = " Source: ticket."
			}
			fmt.Fprintf(b, "   - Expects %s.%s Cite: %s\n", shown(check.DescribeFull(e.Check), maxValueShown), from, shown(e.Cite, maxTextShown))
		}
	}
}

func scriptOf(scripts []domain.Script, id domain.ScenarioID) *domain.Script {
	for i := range scripts {
		if scripts[i].Scenario == id {
			return &scripts[i]
		}
	}
	return nil
}

// sendsLine is what the script sends for a step, in one line. The step already shows the route that
// the planner named, so the route of the script is added only when it differs, as it does for a path
// with a variable. It is empty when the script has no such step or the step sends nothing.
func sendsLine(script *domain.Script, id domain.StepID, hintRoute string) string {
	if script == nil {
		return ""
	}
	i := slices.IndexFunc(script.Steps, func(st domain.ScriptStep) bool { return st.Step == id })
	if i < 0 {
		return ""
	}
	lines := sends.Lines(script.Steps[i])
	if r := script.Steps[i].Request; r != nil && r.Method+" "+r.Path == hintRoute {
		lines = lines[1:]
	}
	return strings.Join(lines, "; ")
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

// writeWebTests lists the web tests that flows wrote for a ticket, with the checks that the page did not
// meet when the flow read it.
func writeWebTests(b *strings.Builder, tests []WebTestDraft, ticket string) {
	var mine []WebTestDraft
	for _, w := range tests {
		if w.Ticket == ticket {
			mine = append(mine, w)
		}
	}
	if len(mine) == 0 {
		return
	}
	b.WriteString("\nWeb tests that a model wrote, replayed once, and not approved: read each one with semar review:\n")
	for i, w := range mine {
		fmt.Fprintf(b, "\n%d. %s: %s. Cite: %s. Replay: %s.\n", i+1, shown(w.ID, maxValueShown), shown(strings.TrimRight(strings.TrimSpace(w.Title), "."), maxTextShown), shown(w.Cite, maxTextShown), shown(w.Replay, maxValueShown))
		if w.Unproved != "" {
			fmt.Fprintf(b, "   - Not proved by the test, left to a person: %s\n", shown(w.Unproved, maxTextShown))
		}
		for _, f := range w.FailsNow {
			fmt.Fprintf(b, "   - Fails now: %s should show %s, and the page shows %s.\n", shown(f.Element, maxValueShown), strconv.Quote(shown(f.Expect, maxValueShown)), strconv.Quote(shown(f.Shown, maxValueShown)))
		}
	}
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
		if m.Surface != "" {
			fmt.Fprintf(b, "   - Surface: %s\n", shown(m.Surface, maxValueShown))
		}
		for _, s := range m.Steps {
			fmt.Fprintf(b, "   - %s\n", shown(s, maxTextShown))
		}
		fmt.Fprintf(b, "   - Expected: %s\n", shown(m.Expected, maxTextShown))
	}
}

// writeAgain lists what a second plan did not plan again, and why (D11). It writes nothing for a plan
// that found no criterion that an earlier plan had decided something for.
func writeAgain(b *strings.Builder, a *Again) {
	if a == nil || (a.Count(StateKept) == 0 && a.Count(StateChanged) == 0 && len(a.Removed) == 0) {
		return
	}
	b.WriteString("\n## Criteria that earlier plans covered\n\n")
	for _, s := range a.Criteria {
		switch s.State {
		case StateKept:
			fmt.Fprintf(b, "- Kept, criterion %d of %s. %s No job ran for it.\n", s.Number, shown(s.Ticket, maxValueShown), keptWords(s))
		case StateChanged:
			fmt.Fprintf(b, "- Changed, criterion %d of %s. It was edited, so it was planned again.", s.Number, shown(s.Ticket, maxValueShown))
			if len(s.Tests) > 0 {
				fmt.Fprintf(b, " Tests of the earlier text: %s. Read them against the new text, and remove one with `semar rm` if it is wrong now.", testList(s.Tests))
			}
			if s.Manual {
				b.WriteString(" A manual check was written for the earlier text: read it again.")
			}
			b.WriteString("\n")
		}
	}
	for _, r := range a.Removed {
		if r.Number > 0 {
			fmt.Fprintf(b, "- Removed, a criterion of %s that was number %d. The ticket file no longer has it.", shown(r.Ticket, maxValueShown), r.Number)
		} else {
			fmt.Fprintf(b, "- Removed, a criterion of %s that began \"%s\". The ticket file has none with that text any more.", shown(r.Ticket, maxValueShown), shown(r.Cite, maxValueShown))
		}
		if len(r.Tests) > 0 {
			fmt.Fprintf(b, " Tests written for it: %s. Remove one with `semar rm` if no other criterion needs it.", testList(r.Tests))
		}
		if r.Manual {
			b.WriteString(" A manual check was written for it: remove it from its obligations file if it is not needed.")
		}
		b.WriteString("\n")
	}
}

func keptWords(s Standing) string {
	var parts []string
	if len(s.Tests) > 0 {
		parts = append(parts, "Tests: "+testList(s.Tests)+".")
	}
	if s.Manual {
		parts = append(parts, "A manual check keeps it.")
	}
	return strings.Join(parts, " ")
}

func testList(ids []domain.ScenarioID) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = shown(string(id), maxValueShown)
	}
	return strings.Join(names, ", ")
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

// writeCriteriaTip suggests the form that a small model plans best, once for a ticket whose
// criteria are all prose.
func writeCriteriaTip(b *strings.Builder, accounting []Accounted, ticket string) {
	has := false
	for _, a := range accounting {
		if a.Ticket != ticket {
			continue
		}
		if strings.HasPrefix(a.Full, "Given ") {
			return
		}
		has = true
	}
	if has {
		b.WriteString("\nTip: write each criterion as Given, When, and Then lines in the ticket file. A small model plans a criterion in that form more reliably than a sentence of prose, and the plan cites its first line.\n")
	}
}

// FixtureRunWords says whether a drafted fixture was run, as the end of a sentence: "Ran once: ok." or
// "Not run: <why>.", with what may still exist after the cleanup.
func FixtureRunWords(d FixtureDraft) string {
	if !d.Trial.Ran {
		return "Not run: " + strings.TrimRight(oneLine(valueOr(d.Trial.Skipped, "no run was set up"), maxTextShown), ".") + "."
	}
	words := "Ran once: ok."
	if len(d.Trial.Left) > 0 {
		words += " Left behind: " + strings.TrimRight(oneLine(strings.Join(d.Trial.Left, "; "), maxTextShown), ".") + "."
	}
	return words
}

// DraftedEntries are the drafted fixtures as the catalogue shows them, not approved, in the order of
// the drafts. plan.md names them like any fixture that a scenario uses.
func (o Output) DraftedEntries() []fixture.Entry {
	var out []fixture.Entry
	for _, d := range o.FixtureDrafts {
		if d.Drafted {
			out = append(out, entryOf(d.Fixture))
		}
	}
	return out
}

// writeFixturesDrafted lists the fixtures that the plan drafted, which scenarios use them, and the
// ones that no model could write.
func writeFixturesDrafted(b *strings.Builder, out Output) {
	if len(out.FixtureDrafts) == 0 {
		return
	}
	b.WriteString("\n## Fixtures drafted\n\n")
	for _, d := range out.FixtureDrafts {
		id := shown(d.Need.ID, maxValueShown)
		if !d.Drafted {
			why := d.Reason
			if why == "" {
				why = firstProblem(d.Problems)
			}
			fmt.Fprintf(b, "- %s: not drafted. %s. Needed by %s.\n", id, strings.TrimRight(shown(why, maxTextShown), "."), shown(strings.Join(d.Need.Cites, ", "), maxTextShown))
			continue
		}
		clean := "no cleanup"
		if len(d.Fixture.Cleanup) > 0 {
			clean = "cleans up"
		}
		var users []string
		for _, sc := range out.Scenarios {
			if slices.ContainsFunc(sc.Given, func(g domain.Given) bool { return g.Fixture == d.Fixture.ID }) {
				users = append(users, string(sc.ID))
			}
		}
		used := "no scenario"
		if len(users) > 0 {
			used = strings.Join(users, ", ")
		}
		requests := counted(len(d.Fixture.Create)+len(d.Fixture.Cleanup), "request")
		if as := d.Fixture.Accounts(); len(as) > 0 {
			requests += " as " + shown(strings.Join(as, ", "), maxValueShown)
		}
		fmt.Fprintf(b, "- %s: %s. %s, %s. %s\n", id, strings.TrimRight(shown(d.Fixture.Description, maxTextShown), "."),
			requests, clean, FixtureRunWords(d))
		fmt.Fprintf(b, "  Used by %s. Not approved → semar review %s\n", shown(used, maxTextShown), id)
	}
}

// writePipeline says how the criteria pipeline made a plan: the models, the calls, and for each
// criterion which model answered it and how many corrections it took.
func writePipeline(b *strings.Builder, out Output) {
	st, r := out.Pipeline, out.Result
	b.WriteString("- Pipeline: criteria. One job with no tools for each acceptance criterion. Code checked each answer, an answer with a problem went back once to the same model with the problems in plain words, and then to the next model of the ladder.\n")
	for _, u := range st.Uses {
		fmt.Fprintf(b, "- Model %s (%s, provider %s): %d calls, %d input tokens, %d output tokens\n",
			shown(u.Model, maxValueShown), shown(valueOr(u.Result.Model, "reported no model name"), maxValueShown), shown(u.PlannerID, maxValueShown), u.Calls, u.Result.InputTokens, u.Result.OutputTokens)
	}
	if n := out.Again; n != nil && n.Count(StateKept) > 0 {
		fmt.Fprintf(b, "- Kept: %d, which had a test or a manual check already, so no job ran for them\n", n.Count(StateKept))
	}
	fmt.Fprintf(b, "- Criteria: %d, with %d calls, %d corrections, %d escalated to a higher model, %d reused from an earlier plan, and %d that no model answered\n",
		st.Criteria, st.Calls, st.Corrections, st.Escalated, st.Reused, st.Unanswered)
	if st.FixtureJobs > 0 {
		fmt.Fprintf(b, "- Fixtures: %s drafted of %d that criteria needed. A job with no tools drafted each one, code checked it, and a run on the test environment tried it once\n",
			counted(st.FixturesDrafted, "fixture"), st.FixtureJobs)
	}
	fmt.Fprintf(b, "- Tokens: %d input, %d output\n", r.InputTokens, r.OutputTokens)
	writeWebPart(b, out)
	fmt.Fprintf(b, "- Prompt version: criterion %s, fixture %s, web %s\n", shown(prompt.CriterionVersion(), maxValueShown), shown(prompt.FixtureVersion(), maxValueShown), shown(prompt.WebVersion(), maxValueShown))
	if len(out.Outcomes) == 0 {
		return
	}
	b.WriteString("\n| Criterion | Answer | Model | Corrections |\n| --- | --- | --- | --- |\n")
	for _, o := range out.Outcomes {
		kind, model := "none", "-"
		switch {
		case o.Result.Scenario != nil:
			kind = "scenario"
		case o.Web != nil && o.Web.Test != nil:
			kind = "web test"
		case o.Result.Manual != nil:
			kind = "manual check"
		case o.Result.NotDrafted != nil:
			kind = "not drafted"
		case o.NotPlanned:
			kind = "not planned"
		}
		if o.Answered {
			model = valueOr(st.modelOf(o.Rung), "-")
			if o.Reused {
				model += " (reused)"
			}
		}
		fmt.Fprintf(b, "| %s | %s | %s | %d |\n", shown(o.Packet.Cite, maxValueShown), kind, shown(model, maxValueShown), o.Corrections)
	}
	writeReturned(b, out.Outcomes)
}

// writeWebPart says what the web part of the plan did: the flows, their models, and for each web
// criterion the test that was written or why a person must do the check.
func writeWebPart(b *strings.Builder, out Output) {
	w := out.Pipeline.Web
	if w.Explored == 0 {
		if out.WebOff != "" {
			fmt.Fprintf(b, "- Web: no criterion was explored in a browser: %s. Each web criterion stays a manual check\n", strings.TrimRight(shown(out.WebOff, maxTextShown), "."))
		}
		return
	}
	criteria := "criteria"
	if w.Explored == 1 {
		criteria = "criterion"
	}
	line := fmt.Sprintf("- Web: %d %s explored in a browser, %s written", w.Explored, criteria, counted(w.Written, "test"))
	if w.FailsNow == 1 {
		line += " (1 check fails on this build)"
	} else if w.FailsNow > 1 {
		line += fmt.Sprintf(" (%d checks fail on this build)", w.FailsNow)
	}
	fmt.Fprintf(b, "%s, %d left for a person, %s with %s, %d escalated to a higher model, in %s\n",
		line, w.Manual, counted(w.Turns, "turn"), counted(w.Calls, "call"), w.Escalated, w.Took.Round(time.Second))
	for _, u := range out.Pipeline.WebUses {
		fmt.Fprintf(b, "- Web model %s (%s, provider %s): %d calls, %d input tokens, %d output tokens\n",
			shown(u.Model, maxValueShown), shown(valueOr(u.Result.Model, "reported no model name"), maxValueShown), shown(u.PlannerID, maxValueShown), u.Calls, u.Result.InputTokens, u.Result.OutputTokens)
	}
	for _, o := range out.Outcomes {
		if o.Web == nil {
			continue
		}
		if o.Web.Test != nil {
			fmt.Fprintf(b, "- Web criterion %s: test %s written by %s in %s%s\n", shown(o.Packet.Cite, maxValueShown), shown(string(o.Web.Test.Scenario.ID), maxValueShown),
				shown(valueOr(out.Pipeline.modelOf(o.Web.Rung), "a model"), maxValueShown), counted(o.Web.Turns, "turn"), earlierFlows(out, *o.Web))
			continue
		}
		fmt.Fprintf(b, "- Web criterion %s: left for a person, because %s%s\n", shown(o.Packet.Cite, maxValueShown), shown(o.Web.Manual, maxTextShown), earlierFlows(out, *o.Web))
	}
}

const maxReturnedShown = 40

// writeReturned lists the problems that went back to a model, and the problems of the last answer to
// a criterion that no model answered, so that a person can judge an answer whose first version is not
// in the plan.
func writeReturned(b *strings.Builder, outcomes []CriterionOutcome) {
	shownSoFar := 0
	for _, o := range outcomes {
		for _, p := range append(slices.Clone(o.Returned), o.Problems...) {
			if shownSoFar == 0 {
				b.WriteString("\nProblems that went back to a model:\n\n")
			}
			if shownSoFar == maxReturnedShown {
				b.WriteString("- and more that are not listed\n")
				return
			}
			shownSoFar++
			msg := shown(p.Message, maxValueShown*2)
			if p.Path != "" {
				msg = shown(p.Path, maxValueShown) + ": " + msg
			}
			fmt.Fprintf(b, "- %s: %s\n", shown(o.Packet.Cite, maxValueShown), msg)
		}
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
		fmt.Fprintf(b, " Run %s holds every request: semar show %s --activity.", value(string(tr.Run)), value(string(tr.Run)))
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
	line += docNote(ts.Doc, value)
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

// earlierFlows says why each lower model wrote no test, because the person pays for the higher one.
func earlierFlows(out Output, w WebResult) string {
	var b strings.Builder
	for rung, why := range w.Earlier {
		fmt.Fprintf(&b, ", after %s wrote no test: %s", shown(valueOr(out.Pipeline.modelOf(rung), "a model"), maxValueShown), shown(strings.TrimRight(why, "."), maxTextShown))
	}
	return b.String()
}

// docNote says what the check against the API document found in a trial response. It names the
// first misfit and counts the rest, never a value, so that a person who reviews the draft sees it.
func docNote(d *domain.DocCheck, value func(string) string) string {
	if d == nil {
		return ""
	}
	var parts []string
	if len(d.Misfits) > 0 {
		parts = append(parts, "the response does not fit the API document: "+value(d.SchemaText()))
	}
	if d.Undeclared {
		parts = append(parts, fmt.Sprintf("the API document does not declare status %d", d.Status))
	}
	if d.Slow() {
		parts = append(parts, fmt.Sprintf("the response took %d ms, over the limit of %d ms", d.TookMS, d.LimitMS))
	}
	if len(parts) == 0 {
		return ""
	}
	return ", " + strings.Join(parts, ", ")
}
