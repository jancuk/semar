package status

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/obligation"
)

const (
	lineWidth     = 100
	minTitleWidth = 30
	maxIDWidth    = 40
)

// line makes text from a file or a service one safe line.
func line(s string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(obligation.Safe(s))
}

// showsReasons is false for a group whose next command already says what to do, and whose reason
// is the same sentence on every use case.
func showsReasons(g Group) bool { return g != NeedsReview && g != Verified }

// Text writes what semar status prints: the first two lines, each group with one line per use case,
// the tickets that have no use case, and the verdict.
func Text(w io.Writer, r Report) error {
	if _, err := fmt.Fprintf(w, "%s\n%s\n\n", line(r.Lead()), line(r.Count())); err != nil {
		return err
	}
	var b strings.Builder
	countWidth := len(fmt.Sprint(max(len(r.NotPlanned), maxCount(r.Counts))))
	for _, g := range Groups {
		if r.Counts[g] == 0 {
			continue
		}
		var items []Item
		for _, it := range r.Items {
			if it.Group == g {
				items = append(items, it)
			}
		}
		shared := sharedCommand(items)
		groupLine(&b, string(g), r.Counts[g], countWidth, shared)
		writeItems(&b, items, shared != "")
	}
	if m := r.ModelLine(); m != "" {
		fmt.Fprintf(&b, "\n%s\n", m)
	}
	if len(r.NotPlanned) > 0 {
		groupLine(&b, "not planned", len(r.NotPlanned), countWidth, "semar plan")
		fmt.Fprintf(&b, "    %s\n", line(strings.Join(r.NotPlanned, ", ")))
	}
	if len(r.NoTicket) > 0 {
		fmt.Fprintf(&b, "\n%s\n", line(NoTicketLine(r.NoTicket)))
	}
	fmt.Fprintf(&b, "\n%s\n", line(r.Verdict()))
	_, err := io.WriteString(w, b.String())
	return err
}

func maxCount(m map[Group]int) int {
	n := 0
	for _, c := range m {
		n = max(n, c)
	}
	return n
}

func groupLine(b *strings.Builder, name string, n, countWidth int, command string) {
	head := fmt.Sprintf("  %-12s  %-*d", name, countWidth, n)
	if command != "" {
		head += "  → " + command
	}
	b.WriteString(strings.TrimRight(head, " ") + "\n")
}

// sharedCommand is the command that every use case of a group continues with, or "".
func sharedCommand(items []Item) string {
	if len(items) == 0 || items[0].Command == "" {
		return ""
	}
	for _, it := range items[1:] {
		if it.Command != items[0].Command {
			return ""
		}
	}
	return items[0].Command
}

func writeItems(b *strings.Builder, items []Item, commandShared bool) {
	idWidth := 0
	for _, it := range items {
		idWidth = max(idWidth, min(len([]rune(line(it.Entry.Obligation.ID))), maxIDWidth))
	}
	for _, it := range items {
		notes := it.Notes
		if showsReasons(it.Group) {
			notes = withoutSentence(it.Entry)
		}
		if len(notes) == 0 && it.Group == Verified {
			continue
		}
		id := line(it.Entry.Obligation.ID)
		title := cut(line(it.Entry.Obligation.Title), lineWidth-4-idWidth-2)
		row := fmt.Sprintf("    %-*s  %s", idWidth, id, title)
		if !commandShared && it.Next != "" {
			row += " → " + it.Next
		}
		b.WriteString(strings.TrimRight(row, " ") + "\n")
		for _, n := range notes {
			fmt.Fprintf(b, "      %s\n", line(n))
		}
	}
}

// cut shortens s to width characters, with a mark where it ends, and never below a readable minimum.
func cut(s string, width int) string {
	width = max(width, minTitleWidth)
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return strings.TrimRight(string(r[:width-1]), " ") + "…"
}

// withoutSentence is the reasons of an entry less the sentence that a manual check carries for every
// person, which says nothing about this check.
func withoutSentence(e obligation.Entry) []string {
	if e.Obligation.Manual == nil {
		return e.Reasons
	}
	return slices.DeleteFunc(slices.Clone(e.Reasons), func(r string) bool { return r == e.Obligation.Manual.Text })
}

// NoTicketLine is one line for the commits of a change that name no ticket, each as its short ID and
// its subject. With more than three, two are named and the rest are counted.
func NoTicketLine(commits []string) string {
	n := len(commits)
	if n == 0 {
		return ""
	}
	verb, noun := "have", "commits"
	if n == 1 {
		verb, noun = "has", "commit"
	}
	shown, more := commits, ""
	if n > 3 {
		shown, more = commits[:2], fmt.Sprintf(", and %d more", n-2)
	}
	return fmt.Sprintf("%d %s %s no ticket: %s%s", n, noun, verb, strings.Join(shown, ", "), more)
}
