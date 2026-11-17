package plan

import (
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/openapi"
	"github.com/jancuk/semar/internal/prompt"
)

const maxFieldsShown = 24

// Prompt is the text of a job for this packet. With a previous output and problems it is the
// prompt of a correction: the same packet, the answer that was rejected, and what is wrong with it.
func (p Packet) Prompt(previous []byte, problems []Problem) (string, error) {
	d := prompt.CriterionData{
		Criterion:  fenced("text", p.Cite+"\n"+p.Criterion),
		Operations: operationList(p.Operations),
		Fixtures:   fixtureList(p.Fixtures),
		Accounts:   accountList(p.Accounts),
		Account:    p.Account,
	}
	if p.Diff != "" {
		d.Diff = fenced("diff", p.Diff)
	}
	d.DiffOmitted = strings.Join(p.DiffOmitted, "\n")
	var existing []string
	for _, e := range p.Existing {
		id, title, _ := strings.Cut(e, ": ")
		existing = append(existing, fmt.Sprintf("- %s: %s", code(id), oneLine(title, maxProblemRunes)))
	}
	d.Existing = strings.Join(existing, "\n")
	if len(problems) > 0 {
		d.Correction = true
		d.Problems = problemList(problems)
		if len(previous) > 0 {
			d.Previous = fenced("json", string(previous))
		} else {
			d.Previous = "The output was empty."
		}
	}
	return prompt.Criterion(d)
}

// operationList is one Markdown item for each operation, with what a request to it may hold.
func operationList(ops []PacketOperation) string {
	if len(ops) == 0 {
		return "None. Answer `notDrafted`."
	}
	items := make([]string, len(ops))
	for i, op := range ops {
		items[i] = operationText(op)
	}
	return strings.Join(items, "\n")
}

func operationText(op PacketOperation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s: %s.", code(op.Key), code(op.Method+" "+op.Route))
	if op.Summary != "" {
		fmt.Fprintf(&b, " %s.", strings.TrimRight(oneLine(op.Summary, maxAPISummary), "."))
	}
	for _, in := range []struct{ where, label string }{{"path", "Path parameters"}, {"query", "Query parameters"}, {"header", "Header parameters"}} {
		var parts []string
		for _, pa := range op.Params {
			if pa.In != in.where || in.where == "header" {
				continue
			}
			parts = append(parts, code(pa.Name)+" ("+paramNotes(pa)+")")
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "\n  - %s: %s.", in.label, strings.Join(parts, ", "))
		}
	}
	switch {
	case op.BodyKnown && len(op.Body) > 0:
		label := "JSON body"
		if op.BodyRequired {
			label += " (required)"
		}
		parts := make([]string, len(op.Body))
		for i, f := range op.Body {
			parts[i] = code(f.Name) + " (" + fieldNotes(f) + ")"
		}
		fmt.Fprintf(&b, "\n  - %s: %s.", label, strings.Join(parts, ", "))
		if op.BodyOpen {
			b.WriteString(" Other fields are allowed.")
		}
	case op.BodyKnown:
		b.WriteString("\n  - JSON body: any fields.")
	default:
		b.WriteString("\n  - No JSON body.")
	}
	declared := map[string]bool{}
	for _, r := range op.Responses {
		declared[r.Status] = true
		parts := make([]string, 0, len(r.Fields))
		for i, f := range r.Fields {
			if i == maxFieldsShown {
				parts = append(parts, fmt.Sprintf("and %d more", len(r.Fields)-i))
				break
			}
			parts = append(parts, code(f.Name)+" ("+fieldNotes(f)+")")
		}
		fmt.Fprintf(&b, "\n  - Answers %s with %s.", r.Status, strings.Join(parts, ", "))
	}
	var plainStatuses []string
	for _, s := range op.Statuses {
		if !declared[s] {
			plainStatuses = append(plainStatuses, s)
		}
	}
	if len(plainStatuses) > 0 {
		fmt.Fprintf(&b, "\n  - Also answers %s.", strings.Join(plainStatuses, ", "))
	}
	if op.Source != "" {
		fmt.Fprintf(&b, "\n  - Document: %s.", code(op.Source))
	}
	return b.String()
}

func paramNotes(pa openapi.Param) string {
	notes := []string{valueOr(pa.Type, "string")}
	if pa.Required && pa.In != "path" {
		notes = append(notes, "required")
	}
	if len(pa.Enum) > 0 {
		notes = append(notes, "one of "+strings.Join(pa.Enum, ", "))
	}
	return strings.Join(notes, ", ")
}

func fieldNotes(f openapi.Field) string {
	notes := []string{valueOr(f.Type, "any")}
	if f.Required {
		notes = append(notes, "required")
	}
	if f.Nullable {
		notes = append(notes, "can be null")
	}
	if len(f.Enum) > 0 {
		notes = append(notes, "one of "+strings.Join(f.Enum, ", "))
	}
	return strings.Join(notes, ", ")
}
