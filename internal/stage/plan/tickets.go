package plan

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
)

const (
	// byModel marks every expectation that the planner drafts: a person confirms it (D2).
	byModel = "model"

	sourceTicket = "ticket"

	maxManualChecks = 20 // per ticket
	maxManualSteps  = 20
	maxManualTitle  = 300
	maxManualText   = 500
	maxTicketLine   = 1000 // characters of one line of a ticket that the prompt holds
)

func ticketIndex(tickets []knowledge.Ticket) map[string]knowledge.Ticket {
	m := make(map[string]knowledge.Ticket, len(tickets))
	for _, t := range tickets {
		if _, dup := m[t.Key]; !dup {
			m[t.Key] = t
		}
	}
	return m
}

// source reads the source of an expectation and, for a ticket expectation, checks its cite. The
// cite of any other expectation is checked as the code it names.
func (v *validator) source(path, ticket string, e ExpectDraft) domain.Source {
	switch e.Source {
	case "", string(domain.SourceDiff):
		v.cite(path+".why", e.Why)
		return domain.SourceDiff
	case sourceTicket:
		if v.ticketCite(path+".why", ticket, e.Why) {
			return domain.SourceTicket
		}
		if _, ok := v.ticketFiles[ticket]; !ok && v.known[ticket] {
			v.add(path+".source", "ticket %s has no ticket file in the repository, so the source ticket cannot be used: use diff and cite the code", ticket)
		}
		return domain.SourceTicket
	}
	v.add(path+".source", "source %q must be diff or ticket", e.Source)
	return domain.SourceDiff
}

// ticketCite checks that text starts with <ticket file>:<line> of the file of the ticket and that
// the line exists in the text that the planner was given. It adds the problem, if any.
func (v *validator) ticketCite(path, ticket, text string) bool {
	t, ok := v.ticketFiles[ticket]
	if !ok {
		return false
	}
	m := citePattern.FindStringSubmatch(text)
	if m == nil {
		v.add(path, "the cite must start with %s:<line> of the criterion, for example %s:4", t.Path, t.Path)
		return false
	}
	file, line := cleanSlash(m[1]), atoi(m[2])
	switch {
	case file != t.Path:
		v.add(path, "%s is not the ticket file of %s; cite %s:<line> of the criterion", m[1], ticket, t.Path)
		return false
	case line < 1:
		v.add(path, "%s:%s names no line: lines start at 1", m[1], m[2])
		return false
	}
	if n := textLines(t.Text); line > n {
		v.add(path, "%s has %d lines, so it has no line %d", t.Path, n, line)
		return false
	}
	return true
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
		if n > 1<<30 {
			return 1 << 30
		}
	}
	return n
}

// textLines counts lines as the cite check of a source file does: one for each newline, and one
// more for text after the last.
func textLines(text string) int {
	n := strings.Count(text, "\n")
	if text != "" && !strings.HasSuffix(text, "\n") {
		n++
	}
	return n
}

// account checks that the name is an account of the configuration. An empty name is the account
// of the scenario.
func (v *validator) account(path, name string) {
	if name == "" {
		return
	}
	for _, a := range v.in.Accounts {
		if a.Name == name {
			return
		}
	}
	names := make([]string, len(v.in.Accounts))
	for i, a := range v.in.Accounts {
		names[i] = a.Name
	}
	slices.Sort(names)
	if len(names) == 0 {
		v.add(path, "account %q is not in the configuration: leave out as", name)
		return
	}
	v.add(path, "account %q is not in the configuration; use one of %s", name, strings.Join(names, ", "))
}

func (v *validator) manualChecks(list []ManualCheckDraft) {
	perTicket := map[string]int{}
	for i, m := range list {
		p := fmt.Sprintf("manualChecks[%d]", i)
		switch {
		case !v.known[m.Ticket]:
			v.add(p+".ticket", "ticket %q is not in the change set; use one of %s", m.Ticket, v.ticketList())
		case !ticketHasFile(v.ticketFiles, m.Ticket):
			v.add(p+".ticket", "ticket %s has no ticket file in the repository, so a manual check cannot cite a criterion", m.Ticket)
		default:
			perTicket[m.Ticket]++
			if perTicket[m.Ticket] == maxManualChecks+1 {
				v.add(p, "ticket %s has more than %d manual checks: keep the ones that matter most", m.Ticket, maxManualChecks)
			}
			v.ticketCite(p+".cite", m.Ticket, m.Cite)
		}
		v.manualText(p+".title", "title", "needs a title", m.Title, maxManualTitle)
		v.manualText(p+".expected", "expected result", "needs an expected result", m.Expected, maxManualText)
		if n := len(m.Steps); n < 1 || n > maxManualSteps {
			v.add(p+".steps", "a manual check has 1 to %d steps, and this one has %d", maxManualSteps, n)
		}
		for j, st := range m.Steps {
			v.manualText(fmt.Sprintf("%s.steps[%d]", p, j), "step", "is empty", st, maxManualText)
		}
	}
}

func ticketHasFile(files map[string]knowledge.Ticket, key string) bool {
	_, ok := files[key]
	return ok
}

// manualText checks one line of a manual check. missing is the message for an empty text.
func (v *validator) manualText(path, what, missing, text string, limit int) {
	switch n := len([]rune(strings.Join(strings.Fields(text), " "))); {
	case n == 0 && what == "step":
		v.add(path, "the step %s", missing)
	case n == 0:
		v.add(path, "a manual check %s", missing)
	case n > limit:
		v.add(path, "the %s is %d characters long, and the limit is %d", what, n, limit)
	}
}

// ticketList is the Markdown of the ticket files for the prompt: for each file its key and path,
// then its text in a fence, one numbered line each. The text is the repository's, so a line is
// stripped of control characters, cut, and numbered; the fence is longer than any run of back
// quotes in the text, so the text cannot end it. A file longer than the limit shows where it was cut.
func ticketList(tickets []knowledge.Ticket, keys []string) string {
	var blocks []string
	for _, key := range keys {
		i := slices.IndexFunc(tickets, func(t knowledge.Ticket) bool { return t.Key == key })
		if i < 0 {
			continue
		}
		blocks = append(blocks, ticketBlock(tickets[i]))
	}
	return strings.Join(blocks, "\n\n")
}

func ticketBlock(t knowledge.Ticket) string {
	text, cut := knowledge.CutTicket(t.Text)
	cut = cut || t.Cut
	body := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		body = nil
	}
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### %s: %s\n\n%stext\n", codeName(t.Key), codeName(t.Path), fence)
	for i, line := range body {
		fmt.Fprintf(&b, "%d: %s\n", i+1, ticketLine(line))
	}
	b.WriteString(fence)
	if cut {
		fmt.Fprintf(&b, "\n\nThe file is cut here: it is longer than %d KiB, and the rest is not shown.", knowledge.MaxTicketBytes>>10)
	}
	return b.String()
}

func ticketLine(s string) string {
	s = strings.TrimRight(s, "\r")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == ' ', r == ' ':
			return -1
		}
		return r
	}, s)
	if rs := []rune(s); len(rs) > maxTicketLine {
		return string(rs[:maxTicketLine]) + "…"
	}
	return s
}

// accountList is the Markdown list of the accounts for the prompt. It is empty with fewer than
// two accounts: a step has no other account to act as.
func accountList(accounts []Account) string {
	if len(accounts) < 2 {
		return ""
	}
	sorted := slices.Clone(accounts)
	slices.SortFunc(sorted, func(a, b Account) int { return strings.Compare(a.Name, b.Name) })
	var lines []string
	for _, a := range sorted {
		desc := strings.TrimRight(strings.ReplaceAll(plain(a.Description, 200), "`", "'"), ".")
		if desc == "" {
			desc = "(no description)"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s.", codeName(a.Name), desc))
	}
	return strings.Join(lines, "\n")
}

// ticketKeys are the keys of the tickets that have a file, in key order.
func ticketKeys(tickets []knowledge.Ticket) []string {
	return slices.Sorted(maps.Keys(ticketIndex(tickets)))
}

// citedDocument is cited for a place in a document. A place in a ticket file is checked against the
// text that the planner was given, which is what a person reads.
func (v *validator) citedDocument(path, text, missing string) (string, bool) {
	if m := citePattern.FindStringSubmatch(text); m != nil {
		file := cleanSlash(m[1])
		for _, key := range slices.Sorted(maps.Keys(v.ticketFiles)) {
			if v.ticketFiles[key].Path == file {
				return file, v.ticketCite(path, key, text)
			}
		}
	}
	return v.cited(path, text, missing)
}
