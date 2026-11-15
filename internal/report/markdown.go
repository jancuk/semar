package report

import (
	_ "embed"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/jancuk/semar/internal/activity"
	"github.com/jancuk/semar/internal/domain"
)

//go:embed report.md.tmpl
var markdownSource string

var markdownTemplate = template.Must(template.New("report.md.tmpl").Option("missingkey=error").Parse(markdownSource))

// Data is everything the report reads. It holds no request body and no response body.
type Data struct {
	Run       domain.Run
	Scenarios map[domain.ScenarioID]domain.Scenario
	Activity  []domain.Activity
}

type reasonView struct{ Text, Step string }

type checkView struct {
	Label, State, Expected, Source, Cite, Observed, Detail string
}

type scenarioView struct {
	ID, Title, Outcome string
	Reasons            []reasonView
	Checks             []checkView
}

type ledgerView struct {
	Verb, Object, Scenario, Step, Status string
}

type markdownView struct {
	RunID, Environment, Started, Ended string
	BuildsStart, BuildsEnd, Counts     string
	Interrupted, OnlyDeclared          bool
	Attention, Passed                  []scenarioView
	Ledger                             []ledgerView
}

// Markdown writes the report: scenarios that need attention first, passed scenarios in one details block.
func Markdown(w io.Writer, d Data) error {
	if err := markdownTemplate.Execute(w, buildView(d)); err != nil {
		return fmt.Errorf("render report %s: %w", d.Run.ID, err)
	}
	return nil
}

func buildView(d Data) markdownView {
	run := d.Run
	v := markdownView{
		RunID:        shown(string(run.ID)),
		Environment:  shown(run.Environment),
		Started:      utc(run.StartedAt),
		Ended:        utc(run.EndedAt),
		BuildsStart:  builds(run.BuildsStart),
		BuildsEnd:    builds(run.BuildsEnd),
		Counts:       countLine(run.Results),
		Interrupted:  run.EndedAt.IsZero(),
		OnlyDeclared: onlyDeclared(run),
	}
	for _, r := range ordered(run.Results) {
		sv := scenarioView{
			ID:      shown(string(r.Scenario)),
			Title:   shown(d.Scenarios[r.Scenario].Title),
			Outcome: shown(string(r.Outcome)),
		}
		if r.Outcome == domain.OutcomePassed {
			v.Passed = append(v.Passed, sv)
			continue
		}
		for _, reason := range r.Reasons {
			sv.Reasons = append(sv.Reasons, reasonView{Text: shown(reason.Text), Step: shown(string(reason.Step))})
		}
		for _, c := range r.Checks {
			if c.State == domain.CheckTrue {
				continue
			}
			sv.Checks = append(sv.Checks, checkViewOf(c, d.Scenarios[r.Scenario]))
		}
		v.Attention = append(v.Attention, sv)
	}
	for _, row := range activity.Ledger(d.Activity) {
		v.Ledger = append(v.Ledger, ledgerViewOf(row))
	}
	return v
}

func checkViewOf(c domain.CheckResult, s domain.Scenario) checkView {
	cv := checkView{
		Label:    shown(checkLabel(c)),
		State:    shown(string(c.State)),
		Expected: valueOrEmpty(c.Expected),
		Source:   shown(c.Source),
		Observed: valueOrEmpty(c.Observed),
		Detail:   shown(c.Detail),
	}
	for _, step := range s.Steps {
		if step.ID == c.Step && c.Index >= 0 && c.Index < len(step.Expect) {
			cv.Cite = shown(step.Expect[c.Index].Cite)
		}
	}
	return cv
}

func checkLabel(c domain.CheckResult) string {
	if c.Index < 0 {
		return fmt.Sprintf("%s (invariant)", c.Step)
	}
	return fmt.Sprintf("%s, check %d", c.Step, c.Index+1)
}

func valueOrEmpty(s string) string {
	if s == "" {
		return "(empty)"
	}
	return shown(s)
}

func ledgerViewOf(row activity.LedgerRow) ledgerView {
	status := "left in place"
	if row.CleanedUp {
		status = "cleaned up"
	}
	return ledgerView{
		Verb:     shown(string(row.Verb)),
		Object:   shown(row.Object.Type + " " + row.Object.ID),
		Scenario: shown(string(row.Use.Scenario)),
		Step:     shown(string(row.Use.Step)),
		Status:   status,
	}
}

func builds(bs []domain.BuildRef) string {
	if len(bs) == 0 {
		return "none recorded"
	}
	parts := make([]string, len(bs))
	for i, b := range bs {
		if b.Declared {
			parts[i] = shown(declaredBuild(b))
		} else {
			parts[i] = shown(fmt.Sprintf("%s %s config %s", b.Service, b.Commit, b.ConfigHash))
		}
	}
	return strings.Join(parts, "; ")
}

func declaredBuild(b domain.BuildRef) string {
	return fmt.Sprintf("%s %s (declared on the command line, not read from the service)", b.Service, b.Commit)
}

// onlyDeclared reports whether the run recorded a build and every build it recorded was declared.
func onlyDeclared(run domain.Run) bool {
	all := append(slices.Clone(run.BuildsStart), run.BuildsEnd...)
	return len(all) > 0 && !slices.ContainsFunc(all, func(b domain.BuildRef) bool { return !b.Declared })
}

func utc(t time.Time) string {
	if t.IsZero() {
		return "not recorded"
	}
	return t.UTC().Format(time.RFC3339)
}
