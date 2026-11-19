package report

import (
	"fmt"
	"io"

	"github.com/jancuk/semar/internal/domain"
)

// maxDocLines is how many warnings a run prints before the rest is a count.
const maxDocLines = 20

// DocFinding is one thing that the check against the API document found in a step: a response that
// does not fit its schema, a status that the document does not declare, or a response over the time
// limit. Failed is true when the finding failed the step, and false when it is a warning.
type DocFinding struct {
	Scenario, Step, Operation, Text string
	Failed                          bool
}

// DocFindings lists the findings of the activity in the order of the requests.
func DocFindings(acts []domain.Activity) []DocFinding {
	var out []DocFinding
	for _, a := range acts {
		d := a.Doc
		if d == nil {
			continue
		}
		f := DocFinding{Scenario: string(a.Use.Scenario), Step: string(a.Use.Step), Operation: d.Operation}
		if f.Operation == "" {
			f.Operation = a.Target
		}
		if len(d.Misfits) > 0 {
			g := f
			g.Text, g.Failed = "the response does not fit the API document: "+d.SchemaText(), d.SchemaFail
			out = append(out, g)
		}
		if d.Undeclared {
			g := f
			g.Text = fmt.Sprintf("the API document does not declare status %d for this operation", d.Status)
			out = append(out, g)
		}
		if d.Slow() {
			g := f
			g.Text, g.Failed = fmt.Sprintf("the response took %d ms, over the limit of %d ms", d.TookMS, d.LimitMS), d.SlowFail
			out = append(out, g)
		}
	}
	return out
}

// WriteDocWarnings prints the findings that did not fail a step. A finding that failed one is a
// reason of its scenario, so it is not printed twice. It prints nothing when there is none.
func WriteDocWarnings(w io.Writer, env string, findings []DocFinding) {
	var warns []DocFinding
	for _, f := range findings {
		if !f.Failed {
			warns = append(warns, f)
		}
	}
	if len(warns) == 0 {
		return
	}
	noun := "warnings"
	if len(warns) == 1 {
		noun = "warning"
	}
	fmt.Fprintf(w, "response checks: %d %s from environments.%s.checks, no outcome changed\n", len(warns), noun, plain(env))
	for i, f := range warns {
		if i == maxDocLines {
			fmt.Fprintf(w, "  and %d more, in the report\n", len(warns)-i)
			break
		}
		fmt.Fprintf(w, "  %s step %s: %s: %s\n", plain(f.Scenario), plain(f.Step), plain(f.Operation), plain(f.Text))
	}
}

// docView is one finding in the section of the report.
type docView struct{ Scenario, Step, Operation, Text, Effect string }

func (vw viewer) docViews(acts []domain.Activity) []docView {
	var out []docView
	for _, f := range DocFindings(acts) {
		effect := "warning, no outcome changed"
		if f.Failed {
			effect = "failed the step"
		}
		out = append(out, docView{Scenario: vw.show(f.Scenario), Step: vw.show(f.Step), Operation: vw.show(f.Operation), Text: vw.show(f.Text), Effect: effect})
	}
	return out
}
