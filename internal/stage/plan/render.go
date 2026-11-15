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
	"github.com/jancuk/semar/internal/prompt"
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
			writeScenario(&b, sc)
		}
		if drafted == 0 {
			b.WriteString("\nNo scenario was drafted for this ticket.\n")
		}
	}

	b.WriteString("\n## Not drafted\n\n")
	if len(d.NotDrafted) == 0 {
		b.WriteString("Nothing.\n")
	}
	for _, n := range d.NotDrafted {
		fmt.Fprintf(&b, "- %s: %s. Needs: %s\n", shown(n.Ticket, maxValueShown), shown(n.What, maxTextShown), shown(n.Needs, maxTextShown))
	}

	b.WriteString("\n## Files that no scenario covers\n\n")
	if len(d.Uncovered) == 0 {
		b.WriteString("None.\n")
	}
	for _, u := range d.Uncovered {
		fmt.Fprintf(&b, "- %s: %s\n", shown(u.File, maxValueShown), shown(u.Reason, maxTextShown))
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
	fmt.Fprintf(&b, "- Attempts: %d of %d\n", out.Attempts, MaxAttempts)
	fmt.Fprintf(&b, "- Tokens: %d input, %d output\n", r.InputTokens, r.OutputTokens)
	fmt.Fprintf(&b, "- Prompt version: %s\n", shown(prompt.PlanVersion(), maxValueShown))
	b.WriteString("\nEvery expected result here was read from the code by a model. Confirm each scenario before its result counts.\n")
	b.WriteString("\nThe requests were drafted from the source and were not sent before the first run.\n")
	b.WriteString("\nA request uses a literal ID only when the same scenario captured it or the source holds it as a fixed test value. " +
		"The planner was told to list every other case under \"Not drafted\". Code cannot check this, so read the requests before the first run.\n")

	_, err := io.WriteString(w, b.String())
	return err
}

func writeScenario(b *strings.Builder, sc domain.Scenario) {
	fmt.Fprintf(b, "\n#### %s: %s\n\n", shown(string(sc.ID), maxValueShown), shown(sc.Title, maxValueShown))
	if s := strings.TrimSpace(sc.Intent); s != "" {
		fmt.Fprintf(b, "%s\n\n", paragraph(shown(s, maxTextShown)))
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
		fmt.Fprintf(b, "%d. %s%s: %s\n", i+1, shown(route, maxValueShown), setup, shown(st.Action, maxTextShown))
		for _, e := range st.Expect {
			fmt.Fprintf(b, "   - Expects %s. Cite: %s\n", shown(check.DescribeFull(e.Check), maxValueShown), shown(e.Cite, maxTextShown))
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
