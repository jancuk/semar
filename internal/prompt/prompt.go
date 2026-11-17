// Package prompt holds the prompts that Semar gives to an agent, each embedded with a version line (A9).
package prompt

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed plan.md
var planSource string

//go:embed criterion.md
var criterionSource string

// The prompt text shows {{name}} variables to the model, so the template uses other delimiters.
var planVersion, planTemplate = mustParse("plan.md", planSource)
var criterionVersion, criterionTemplate = mustParse("criterion.md", criterionSource)

// PlanData fills the planner prompt. Every field is data: text from the repository or the
// model reaches the prompt only through these values, never through the template itself.
type PlanData struct {
	Attempt      bool   // false on the first attempt
	Problems     string // a Markdown list of what the previous draft got wrong
	Previous     string // the previous draft as a fenced block, to repair; empty when the previous output was not a draft
	ChangeTable  string
	LeftOut      string // a Markdown list of the files that the copy of the source lacks; empty when there is none
	Diff         string // the difference as a fenced block; empty when none of it is shown
	DiffWhole    bool   // Diff is the whole difference
	DiffOmitted  string // a Markdown list of the files whose hunks Diff lacks; empty when there is none
	API          string // a Markdown list of the API operations that the difference names; empty when there is none
	Services     string
	Methods      string
	MaxScenarios int
	Existing     string // a Markdown list, indented under its bullet; empty when there is none
	Knowledge    string // a Markdown list of the documents to read; empty when there is none
	Fixtures     string // a Markdown list of the fixtures the planner may name; empty when there is none
	Tickets      string // the ticket files of the change, numbered by line; empty when there is none
	Accounts     string // a Markdown list of the accounts a step may act as; empty with fewer than two
}

// CriterionData fills the prompt of a job for one criterion. Every field is data, as in PlanData.
type CriterionData struct {
	Criterion   string // the criterion with its cite, as a fenced block
	Operations  string // a Markdown list of the operations that the job can call
	Fixtures    string // a Markdown list of the fixtures; empty when there is none
	Accounts    string // a Markdown list of the accounts; empty with fewer than two
	Account     string // the account a step acts as when it names none
	Diff        string // the diff of the ticket's files as a fenced block; empty when none is shown
	DiffOmitted string // a Markdown list of the files that Diff lacks; empty when there is none
	Existing    string // a Markdown list of the scenarios that exist; empty when there is none
	Correction  bool   // the job repairs a previous answer
	Previous    string // the previous answer as a fenced block
	Problems    string // a Markdown list of what the previous answer got wrong
}

// CriterionVersion is the version line of the criterion prompt.
func CriterionVersion() string { return criterionVersion }

// Criterion renders the prompt of a job for one criterion, without its version line.
func Criterion(d CriterionData) (string, error) {
	var b strings.Builder
	if err := criterionTemplate.Execute(&b, d); err != nil {
		return "", fmt.Errorf("render the criterion prompt: %w", err)
	}
	return b.String(), nil
}

// PlanVersion is the version line of the planner prompt.
func PlanVersion() string { return planVersion }

// Plan renders the planner prompt without its version line.
func Plan(d PlanData) (string, error) {
	var b strings.Builder
	if err := planTemplate.Execute(&b, d); err != nil {
		return "", fmt.Errorf("render the planner prompt: %w", err)
	}
	return b.String(), nil
}

func mustParse(name, src string) (string, *template.Template) {
	version, body, err := split(src)
	if err != nil {
		panic(fmt.Sprintf("prompt %s: %v", name, err))
	}
	t := template.Must(template.New(name).Delims("[[", "]]").Option("missingkey=error").Parse(body))
	return version, t
}

// split takes the first line, "version: <n>", off a prompt.
func split(src string) (version, body string, err error) {
	first, rest, _ := strings.Cut(src, "\n")
	v, ok := strings.CutPrefix(strings.TrimSpace(first), "version:")
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return "", "", fmt.Errorf("the first line must be \"version: <n>\", got %q", first)
	}
	return v, strings.TrimLeft(rest, "\n"), nil
}
