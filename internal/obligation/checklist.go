package obligation

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

var fileLine = regexp.MustCompile(`^\S+:[0-9]+`)

// Checklist writes the manual checks of every needs_a_person entry of a group, or of every group
// when group is empty. A person copies the record command from it. Every text is data, and goes
// through Safe.
func Checklist(w io.Writer, entries []Entry, group string) error {
	byGroup := map[string][]Entry{}
	var groups []string
	for _, e := range entries {
		g, _, _ := strings.Cut(e.Obligation.ID, "/")
		if e.State != NeedsAPerson || e.Obligation.Manual == nil || (group != "" && g != group) {
			continue
		}
		if _, ok := byGroup[g]; !ok {
			groups = append(groups, g)
		}
		byGroup[g] = append(byGroup[g], e)
	}
	slices.Sort(groups)
	for gi, g := range groups {
		if gi > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "Manual checks for %s (%d)\n", Safe(g), len(byGroup[g])); err != nil {
			return err
		}
		for i, e := range byGroup[g] {
			if err := checkItem(w, i+1, e); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkItem(w io.Writer, n int, e Entry) error {
	o, m := e.Obligation, *e.Obligation.Manual
	var b strings.Builder
	fmt.Fprintf(&b, "%d. %s  %s\n", n, Safe(o.ID), Safe(o.Title))
	fmt.Fprintf(&b, "   Why a person: %s\n", Safe(m.Text))
	fmt.Fprintf(&b, "   Steps (%s):\n", writtenBy(m.WrittenBy))
	for i, s := range m.Steps {
		fmt.Fprintf(&b, "     %d. %s\n", i+1, Safe(s))
	}
	fmt.Fprintf(&b, "   Expected: %s\n", Safe(m.Expected))
	if o.Cite != "" {
		check := "not checked by code"
		if o.Source == SourceKnowledge && fileLine.MatchString(o.Cite) {
			check = "checked when the plan was made"
		}
		fmt.Fprintf(&b, "   Cite: %s (%s)\n", Safe(o.Cite), check)
	}
	if r := e.Manual; r != nil {
		fmt.Fprintf(&b, "   Recorded: %s, checked by a person (%s, %s)\n", Safe(r.Result), Safe(r.User), day(r.At))
	}
	fmt.Fprintf(&b, "   Check:    semar check %s pass|fail --note \"<what you saw>\"\n", Safe(o.ID))
	_, err := io.WriteString(w, b.String())
	return err
}

func writtenBy(s string) string {
	switch s {
	case "person":
		return "written by a person"
	case "code":
		return "written by code"
	case "ai":
		return "written by AI"
	}
	return "written by " + Safe(s)
}
