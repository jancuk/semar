package status

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/jancuk/semar/internal/obligation"
)

// line makes text from a file or a service one safe line.
func line(s string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(obligation.Safe(s))
}

// Text writes what semar status prints: the first two lines, the groups with their use cases, the
// tickets that have no use case, and the verdict.
func Text(w io.Writer, r Report) error {
	if _, err := fmt.Fprintf(w, "%s\n%s\n\n", line(r.Lead()), line(r.Count())); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, g := range Groups {
		n := r.Counts[g]
		if n == 0 {
			continue
		}
		head := fmt.Sprintf("  %s\t%d", g, n)
		first := true
		for _, it := range r.Items {
			if it.Group != g {
				continue
			}
			rows := rowsOf(it)
			if len(rows) == 0 {
				continue
			}
			id := line(it.Entry.Obligation.ID)
			for _, row := range rows {
				if first {
					fmt.Fprintf(tw, "%s\t%s\t%s\n", head, id, row)
					first = false
				} else {
					fmt.Fprintf(tw, "\t\t%s\t%s\n", id, row)
				}
				id = ""
			}
		}
		if first {
			fmt.Fprintln(tw, head)
		}
	}
	if len(r.NotPlanned) > 0 {
		fmt.Fprintf(tw, "  not planned\t%d\t%s\n", len(r.NotPlanned), line(strings.Join(r.NotPlanned, ", "))+" → semar plan")
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, c := range r.NoTicket {
		if _, err := fmt.Fprintf(w, "\nA commit has no ticket: %s", line(c)); err != nil {
			return err
		}
	}
	if len(r.NoTicket) > 0 {
		fmt.Fprintln(w)
	}
	_, err := fmt.Fprintf(w, "\n%s\n", line(r.Verdict()))
	return err
}

// rowsOf is the text of a use case in its group: the reasons, with the next command on the last.
// A verified use case that has nothing to say has no row.
func rowsOf(it Item) []string {
	texts := it.Notes
	if it.Group != Verified {
		texts = it.Entry.Reasons
	}
	rows := make([]string, 0, len(texts))
	for _, t := range texts {
		rows = append(rows, line(t))
	}
	if len(rows) == 0 && it.Group != Verified {
		rows = append(rows, line(it.Entry.Obligation.Title))
	}
	if len(rows) > 0 && it.Next != "" {
		rows[len(rows)-1] += " → " + it.Next
	}
	return rows
}
