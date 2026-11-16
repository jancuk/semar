package plan

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/knowledge"
)

// maxCriterionText bounds the text of a criterion that code writes into plan.md and an obligation.
const maxCriterionText = 200

var (
	headingPattern   = regexp.MustCompile(`^ {0,3}#{1,6}(?:[ \t]+(.*?))?[ \t#]*$`)
	criterionPattern = regexp.MustCompile(`^([0-9]{1,6})\. +(.*)$`)
	fencePattern     = regexp.MustCompile("^ {0,3}(```|~~~)")
)

// Criterion is one numbered acceptance criterion of a ticket file (D9). Line is its first line
// in the file and End its last: a criterion is one numbered line and the lines that continue it.
type Criterion struct {
	Ticket string // the key
	Path   string // the slash path of the ticket file
	Number int    // the n of "<n>. "
	Line   int
	End    int
	Text   string // one clean line, cut; the number is not part of it
}

// Cite is the place of the first line of the criterion.
func (c Criterion) Cite() string { return c.Path + ":" + strconv.Itoa(c.Line) }

// Criteria reads the acceptance criteria of a ticket file: the lines that start with "<n>. "
// under a heading whose text contains "acceptance criteria" (any case), up to the next heading.
// A criterion ends at the next numbered line, a blank line, or a heading. A fenced block never
// holds a heading. Lines are counted as the prompt counts them, so a cite names the line that
// the planner saw. The text is repository text: it is returned as data, one clean line.
func Criteria(t knowledge.Ticket) []Criterion {
	var out []Criterion
	var parts []string
	open := false
	in := false // under a matching heading
	fenced := false
	flush := func() {
		if open {
			c := &out[len(out)-1]
			c.Text = cutRunes(oneLine(strings.Join(parts, " "), 1<<20), maxCriterionText)
		}
		open, parts = false, nil
	}
	if t.Text == "" {
		return nil
	}
	for i, line := range strings.Split(t.Text, "\n") {
		n := i + 1
		line = ticketLine(line)
		if fencePattern.MatchString(line) {
			fenced = !fenced
			if open && in {
				parts = append(parts, line)
				out[len(out)-1].End = n
			}
			continue
		}
		if !fenced {
			if m := headingPattern.FindStringSubmatch(line); m != nil {
				flush()
				in = strings.Contains(strings.ToLower(m[1]), "acceptance criteria")
				continue
			}
		}
		if !in {
			continue
		}
		if fenced {
			if open {
				parts = append(parts, line)
				out[len(out)-1].End = n
			}
			continue
		}
		if m := criterionPattern.FindStringSubmatch(line); m != nil {
			flush()
			num, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			out = append(out, Criterion{Ticket: t.Key, Path: t.Path, Number: num, Line: n, End: n})
			open, parts = true, []string{m[2]}
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if open {
			parts = append(parts, line)
			out[len(out)-1].End = n
		}
	}
	flush()
	return out
}

func cutRunes(s string, limit int) string {
	if rs := []rune(s); len(rs) > limit {
		return strings.TrimSpace(string(rs[:limit-1])) + "…"
	}
	return s
}
