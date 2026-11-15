package changeset

import (
	"fmt"
	"io"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

const shortLen = 12

// Table writes the change set as Markdown.
func Table(w io.Writer, cs domain.ChangeSet) error {
	var b strings.Builder
	subjects := make(map[string]string, len(cs.Commits))
	merge := map[string]bool{}
	for _, c := range cs.Commits {
		subjects[c.ID] = c.Subject
		merge[c.ID] = c.Merge
	}

	fmt.Fprintf(&b, "Base %s (%s), head %s (%s). %s, %s, %d without a ticket, %s.\n",
		cell(cs.Base), short(cs.BaseCommit), cell(cs.Head), short(cs.HeadCommit),
		plural(len(cs.Commits), "commit"), plural(len(cs.Tickets), "ticket"), len(cs.NoTicket), plural(len(cs.Files), "file"))

	b.WriteString("\n## Tickets\n\n")
	if len(cs.Tickets) == 0 {
		b.WriteString("No commit names a ticket.\n")
	} else {
		b.WriteString("| Ticket | Commits | Files | First subject |\n| --- | ---: | ---: | --- |\n")
		for _, t := range cs.Tickets {
			first := ""
			for _, id := range t.Commits {
				if !merge[id] {
					first = subjects[id]
					break
				}
			}
			fmt.Fprintf(&b, "| %s | %d | %d | %s |\n", cell(t.Key), len(t.Commits), len(t.Files), cell(first))
		}
	}

	b.WriteString("\n## Commits with no ticket\n\n")
	if len(cs.NoTicket) == 0 {
		b.WriteString("Every commit names a ticket.\n")
	} else {
		for _, id := range cs.NoTicket {
			fmt.Fprintf(&b, "- %s %s\n", short(id), cell(subjects[id]))
		}
	}

	b.WriteString("\n## Files\n\n")
	if len(cs.Files) == 0 {
		b.WriteString("No file differs.\n")
	} else {
		b.WriteString("| Path | Status | Kind | + | - | Tickets |\n| --- | --- | --- | ---: | ---: | --- |\n")
		for _, f := range cs.Files {
			path := cell(f.Path)
			if f.OldPath != "" {
				path += " (was " + cell(f.OldPath) + ")"
			}
			added, deleted := fmt.Sprintf("+%d", f.Added), fmt.Sprintf("-%d", f.Deleted)
			if f.Binary {
				added, deleted = "binary", "binary"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", path, f.Status, f.Kind, added, deleted, cell(strings.Join(f.Tickets, ", ")))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func short(id string) string {
	if len(id) > shortLen {
		return id[:shortLen]
	}
	return id
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

var cellEscaper = strings.NewReplacer(`\`, `\\`, "|", `\|`, "\r\n", " ", "\n", " ", "\r", " ")

// cell makes text safe inside a Markdown table cell or a list item.
func cell(s string) string { return cellEscaper.Replace(s) }
