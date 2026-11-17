package report

import (
	_ "embed"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/jancuk/semar/internal/activity"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/words"
)

//go:embed report.md.tmpl
var markdownSource string

var markdownTemplate = template.Must(template.New("report.md.tmpl").Option("missingkey=error").Parse(markdownSource))

// Data is everything the report reads. It holds no request body and no response body.
type Data struct {
	Run       domain.Run
	Scenarios map[domain.ScenarioID]domain.Scenario
	Activity  []domain.Activity
	// Coverage is the line that opens the report, such as "42 use cases: 35 verified". It is empty
	// for a repository with no obligation, and then nothing is printed.
	Coverage string
	// CoverageTickets are the tickets of the use cases that the line counts, for the command
	// that shows the detail.
	CoverageTickets []string
	// Journeys are the journey results of the run. The report opens with them, and a leg that did not
	// pass is reproduced through its journey.
	Journeys []domain.JourneyResult
	// JourneyTitles gives the title of each journey, which the result does not hold.
	JourneyTitles map[domain.JourneyID]string
}

type reasonView struct{ Text, Step string }

type checkView struct {
	Label, State, Expected, Source, Cite, Observed, Detail string
}

type scenarioView struct {
	ID, Title, Outcome string
	Next               string
	Reproduce          string
	Reasons            []reasonView
	Fixtures           []string
	Checks             []checkView
}

type ledgerView struct {
	Verb, Object, Scenario, Step, Status string
}

// browserStep is one action of the browser: the step's action text beside the operation and the target.
type browserStep struct {
	Scenario, Step, Why, Target, Result, Screenshot string
}

type markdownView struct {
	Browser                            []browserStep
	RunID, Environment, Started, Ended string
	EnvironmentKind                    string
	BuildsStart, BuildsEnd, Counts     string
	Interrupted, OnlyDeclared          bool
	Coverage, CoverageDetail           string
	Journeys                           []journeyView
	Attention, Passed                  []scenarioView
	Ledger                             []ledgerView
	StillExists                        []string
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
		RunID:           shown(string(run.ID)),
		Environment:     shown(run.Environment),
		EnvironmentKind: shown(run.EnvironmentKind),
		Started:         utc(run.StartedAt),
		Ended:           utc(run.EndedAt),
		BuildsStart:     builds(run.BuildsStart),
		BuildsEnd:       builds(run.BuildsEnd),
		Counts:          countLine(run.Results),
		Interrupted:     run.EndedAt.IsZero(),
		OnlyDeclared:    onlyDeclared(run),
	}
	if d.Coverage != "" {
		v.Coverage = shown(d.Coverage)
		v.CoverageDetail = ledgerCommand(d.CoverageTickets)
	}
	v.Journeys = journeyViews(d)
	legs := legJourneys(d.Journeys)
	for _, r := range ordered(run.Results) {
		word := resultWord(string(r.Outcome), r.Reasons)
		sv := scenarioView{
			ID:      shown(string(r.Scenario)),
			Title:   shown(d.Scenarios[r.Scenario].Title),
			Outcome: shown(word),
			Next:    words.Next(word, shown(string(r.Scenario))),
		}
		sv.Reproduce = "semar run --scenario " + sv.ID
		if j, ok := legs[r.Scenario]; ok {
			sv.Reproduce = journeyCommand(j, run.Environment)
		}
		for _, rec := range r.Fixtures {
			sv.Fixtures = append(sv.Fixtures, fixtureLine(rec))
			for _, left := range rec.Left {
				v.StillExists = append(v.StillExists, shown(string(r.Scenario))+": "+shown(left))
			}
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
	v.StillExists = append(v.StillExists, journeyStillExists(d.Journeys)...)
	for _, row := range activity.Ledger(d.Activity) {
		v.Ledger = append(v.Ledger, ledgerViewOf(row))
	}
	v.Browser = browserSteps(d.Activity)
	return v
}

// ledgerCommand is the command that shows the use cases of the tickets. A key that is not safe
// as an ID is left out, because it sits in a code span where nothing can be escaped.
func ledgerCommand(tickets []string) string {
	cmd := "semar ledger"
	for _, t := range tickets {
		if idcheck.Check("ticket", t) == nil {
			cmd += " --ticket " + t
		}
	}
	return cmd
}

// fixtureLine says what one given entry did: which fixture, in which version, how it gave its
// state, and what became of what it made.
func fixtureLine(r domain.FixtureRecord) string {
	line := "Given " + shown(r.As) + ": " + shown(string(r.Fixture))
	if r.Version != "" {
		line += " " + shown(r.Version)
	}
	if r.Hash != "" {
		line += " (hash " + shown(string(r.Hash[:min(len(r.Hash), 12)])) + ")"
	}
	switch r.Via {
	case "find":
		line += ", found by find"
	case "create":
		line += ", made by create"
	default:
		line += ", no state was given"
	}
	return line + "; cleanup " + shown(strings.ReplaceAll(r.Cleanup, "_", " "))
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

// screenshotPath is where the engine puts a screenshot. The activity log is read back from disk, so a
// path is linked only when it has this form.
var screenshotPath = regexp.MustCompile(`^screenshots/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+\.png$`)

func browserSteps(acts []domain.Activity) []browserStep {
	var out []browserStep
	for _, e := range acts {
		if e.Surface != domain.SurfaceWeb {
			continue
		}
		b := browserStep{
			Scenario: shown(string(e.Use.Scenario)),
			Step:     shown(string(e.Use.Step)),
			Why:      shown(e.Why),
			Target:   shown(e.Target),
			Result:   shown(e.Result),
		}
		if screenshotPath.MatchString(e.Screenshot) {
			b.Screenshot = e.Screenshot
		}
		out = append(out, b)
	}
	return out
}
