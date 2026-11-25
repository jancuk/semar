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

// Files reads the stored evidence of the run that the report is about, so that the HTML report can show
// an exchange and a screenshot. Both reads are bounded and stay inside the run directory.
type Files interface {
	Action(rel string) (domain.Action, error)
	File(rel string, limit int64) ([]byte, error)
}

// Data is everything the report reads. The Markdown report holds no request body and no response body.
type Data struct {
	// Files and RunDir are used by the HTML report only. With no Files it shows no exchange and no
	// screenshot. RunDir is the path that the report names for the screenshots that it leaves out.
	Files     Files
	RunDir    string
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
	ByModel                                                bool
}

type scenarioView struct {
	ID, Title, Outcome string
	Next               string
	Reproduce          string
	Reasons            []reasonView
	Fixtures           []string
	Checks             []checkView
	// Attempts says how a scenario that ran more than once came out. Tries has one line for each attempt.
	Attempts string
	Tries    []string
	// Shots are the screenshots of the browser and device steps that a reason or a check names.
	Shots []shotView
	// Class is the stored outcome. Ticket is the ticket of the scenario file. Lead is the reason that a
	// result line shows. Flaky marks a scenario that passed after an attempt that did not.
	Class domain.Outcome
	// Models is modelNote of the result: how many expected results a model wrote.
	Models string
	Ticket string
	Lead   string
	Flaky  bool
}

type shotView struct{ Step, Path string }

type ledgerView struct {
	Verb, Object, Scenario, Step, Status string
}

// browserStep is one action of the browser or of a device: the step's action text beside the operation and the target.
type browserStep struct {
	Scenario, Step, Why, Target, Result, Screenshot string
	// Crash and Logcat are the files of evidence of a device step that failed, when it has them.
	Crash, Logcat string
}

type markdownView struct {
	Browser, Device                    []browserStep
	Blocked                            []blockedView
	DocChecks                          []docView
	A11y                               []a11yView
	Misses                             []missView
	MissesMore                         int
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
	if err := markdownTemplate.Execute(w, markdownViewer.buildView(d)); err != nil {
		return fmt.Errorf("render report %s: %w", d.Run.ID, err)
	}
	return nil
}

func (vw viewer) buildView(d Data) markdownView {
	run := d.Run
	v := markdownView{
		RunID:           vw.show(string(run.ID)),
		Environment:     vw.show(run.Environment),
		EnvironmentKind: vw.show(run.EnvironmentKind),
		Started:         utc(run.StartedAt),
		Ended:           utc(run.EndedAt),
		BuildsStart:     vw.builds(run.BuildsStart),
		BuildsEnd:       vw.builds(run.BuildsEnd),
		Counts:          countLine(run.Results),
		Interrupted:     run.EndedAt.IsZero(),
		OnlyDeclared:    onlyDeclared(run),
	}
	if d.Coverage != "" {
		v.Coverage = vw.show(d.Coverage)
		v.CoverageDetail = statusCommand(d.CoverageTickets)
	}
	v.Journeys = vw.journeyViews(d)
	legs := legJourneys(d.Journeys)
	for _, r := range ordered(run.Results) {
		word := string(r.Outcome)
		sv := scenarioView{
			ID:      vw.show(string(r.Scenario)),
			Title:   vw.show(d.Scenarios[r.Scenario].Title),
			Outcome: vw.show(word),
			Next:    words.Next(word, vw.show(string(r.Scenario))),
			Class:   r.Outcome,
			Ticket:  vw.show(d.Scenarios[r.Scenario].Ticket),
			Flaky:   r.Flaky,
			Models:  modelNote(r),
		}
		if len(r.Reasons) > 0 {
			sv.Lead = vw.show(shownReason(r.Reasons))
		}
		sv.Attempts = vw.show(attemptNote(r))
		for i, a := range r.Attempts {
			sv.Tries = append(sv.Tries, fmt.Sprintf("Attempt %d: %s%s", i+1, a.Outcome, vw.shownAttemptReason(a)))
		}
		sv.Reproduce = "semar run --scenario " + sv.ID
		if j, ok := legs[r.Scenario]; ok {
			sv.Reproduce = vw.journeyCommand(j, run.Environment)
		}
		for _, rec := range r.Fixtures {
			sv.Fixtures = append(sv.Fixtures, vw.fixtureLine(rec))
			for _, left := range rec.Left {
				v.StillExists = append(v.StillExists, vw.show(string(r.Scenario))+": "+vw.show(left))
			}
		}
		if r.Outcome == domain.OutcomePassed {
			v.Passed = append(v.Passed, sv)
			continue
		}
		for _, reason := range r.Reasons {
			sv.Reasons = append(sv.Reasons, reasonView{Text: vw.show(reason.Text), Step: vw.show(string(reason.Step))})
		}
		for _, c := range r.Checks {
			if c.State == domain.CheckTrue {
				continue
			}
			sv.Checks = append(sv.Checks, vw.checkViewOf(c, d.Scenarios[r.Scenario]))
		}
		sv.Shots = vw.failedShots(d.Activity, r)
		v.Attention = append(v.Attention, sv)
	}
	v.StillExists = append(v.StillExists, vw.journeyStillExists(d.Journeys)...)
	for _, row := range activity.Ledger(d.Activity) {
		v.Ledger = append(v.Ledger, vw.ledgerViewOf(row))
	}
	v.Browser = vw.browserSteps(d.Activity, domain.SurfaceWeb)
	v.Device = vw.browserSteps(d.Activity, domain.SurfaceAndroid)
	v.Blocked = vw.blockedViews(d.Activity)
	v.DocChecks = vw.docViews(d.Activity)
	v.A11y = vw.a11yViews(d.Activity)
	v.Misses = vw.missViews(string(run.ID), d.Activity)
	if n := len(Misses(d.Activity)); n > len(v.Misses) {
		v.MissesMore = n - len(v.Misses)
	}
	return v
}

// shownAttemptReason is ": " and the first reason of an attempt that did not pass, or nothing.
func (vw viewer) shownAttemptReason(a domain.Attempt) string {
	if a.Outcome == domain.OutcomePassed || len(a.Reasons) == 0 {
		return ""
	}
	return ": " + vw.show(shownReason(a.Reasons))
}

// failedShots gives the screenshot of each browser or device step that a reason or a failed check of the result
// names, in the order of the steps that name them.
func (vw viewer) failedShots(acts []domain.Activity, r domain.ScenarioResult) []shotView {
	var steps []domain.StepID
	add := func(id domain.StepID) {
		if id != "" && !slices.Contains(steps, id) {
			steps = append(steps, id)
		}
	}
	for _, reason := range r.Reasons {
		add(reason.Step)
	}
	for _, c := range r.Checks {
		if c.State != domain.CheckTrue {
			add(c.Step)
		}
	}
	var out []shotView
	for _, id := range steps {
		for i := len(acts) - 1; i >= 0; i-- {
			e := acts[i]
			if e.Surface.OnScreen() && e.Use.Scenario == r.Scenario && e.Use.Step == id && screenshotPath.MatchString(e.Screenshot) {
				out = append(out, shotView{Step: vw.show(string(id)), Path: e.Screenshot})
				break
			}
		}
	}
	return out
}

// statusCommand is the command that shows the use cases of the tickets. A key that is not safe
// as an ID is left out, because it sits in a code span where nothing can be escaped.
func statusCommand(tickets []string) string {
	cmd := "semar status"
	for _, t := range tickets {
		if idcheck.Check("ticket", t) == nil {
			cmd += " --ticket " + t
		}
	}
	return cmd
}

// fixtureLine says what one given entry did: which fixture, in which version, how it gave its
// state, and what became of what it made.
func (vw viewer) fixtureLine(r domain.FixtureRecord) string {
	line := "Given " + vw.show(r.As) + ": " + vw.show(string(r.Fixture))
	if r.Version != "" {
		line += " " + vw.show(r.Version)
	}
	if r.Hash != "" {
		line += " (hash " + vw.show(string(r.Hash[:min(len(r.Hash), 12)])) + ")"
	}
	switch r.Via {
	case "find":
		line += ", found by find"
	case "create":
		line += ", made by create"
	default:
		line += ", no state was given"
	}
	return line + "; cleanup " + vw.show(strings.ReplaceAll(r.Cleanup, "_", " "))
}

func (vw viewer) checkViewOf(c domain.CheckResult, s domain.Scenario) checkView {
	cv := checkView{
		Label:    vw.show(checkLabel(c)),
		State:    vw.show(string(c.State)),
		Expected: vw.valueOrEmpty(c.Expected),
		Source:   vw.show(c.Source),
		Observed: vw.valueOrEmpty(c.Observed),
		Detail:   vw.show(c.Detail),
		ByModel:  c.ByModel,
	}
	for _, step := range s.Steps {
		if step.ID == c.Step && c.Index >= 0 && c.Index < len(step.Expect) {
			cv.Cite = vw.show(step.Expect[c.Index].Cite)
		}
	}
	return cv
}

func checkLabel(c domain.CheckResult) string {
	if c.Source == domain.SourceFlow {
		return fmt.Sprintf("%s (flow check)", c.Step)
	}
	if c.Index < 0 {
		return fmt.Sprintf("%s (invariant)", c.Step)
	}
	return fmt.Sprintf("%s, check %d", c.Step, c.Index+1)
}

func (vw viewer) valueOrEmpty(s string) string {
	if s == "" {
		return "(empty)"
	}
	return vw.show(s)
}

func (vw viewer) ledgerViewOf(row activity.LedgerRow) ledgerView {
	status := "left in place"
	if row.CleanedUp {
		status = "cleaned up"
	}
	return ledgerView{
		Verb:     vw.show(string(row.Verb)),
		Object:   vw.show(row.Object.Type + " " + row.Object.ID),
		Scenario: vw.show(string(row.Use.Scenario)),
		Step:     vw.show(string(row.Use.Step)),
		Status:   status,
	}
}

func (vw viewer) builds(bs []domain.BuildRef) string {
	if len(bs) == 0 {
		return "none recorded"
	}
	parts := make([]string, len(bs))
	for i, b := range bs {
		if b.Declared {
			parts[i] = vw.show(declaredBuild(b))
		} else {
			parts[i] = vw.show(fmt.Sprintf("%s %s config %s", b.Service, b.Commit, b.ConfigHash))
		}
	}
	return strings.Join(parts, "; ")
}

func declaredBuild(b domain.BuildRef) string {
	if b.APK {
		return fmt.Sprintf("%s %s (the digest of the APK file of the service, not read from the service)", b.Service, b.Commit)
	}
	if b.Local {
		from := "the head commit of the repository"
		if strings.Contains(b.Commit, "+") {
			from = "the head commit of the repository and the changes to its tracked files"
		}
		return fmt.Sprintf("%s %s (%s, not read from the service)", b.Service, b.Commit, from)
	}
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
var screenshotPath = regexp.MustCompile(`^screenshots/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+\.(png|jpg)$`)

// evidenceFile is where the engine puts the crash report and the log of a device step. Like a screenshot
// path it is linked only when it has this form, because the activity log is read back from disk.
var evidenceFile = regexp.MustCompile(`^(crash|logcat)-[A-Za-z0-9._-]+\.txt$`)

func (vw viewer) browserSteps(acts []domain.Activity, surface domain.Surface) []browserStep {
	var out []browserStep
	for _, e := range acts {
		if e.Surface != surface {
			continue
		}
		b := browserStep{
			Scenario: vw.show(string(e.Use.Scenario)),
			Step:     vw.show(string(e.Use.Step)),
			Why:      vw.show(e.Why),
			Target:   vw.show(e.Target),
			Result:   vw.show(e.Result),
		}
		if screenshotPath.MatchString(e.Screenshot) {
			b.Screenshot = e.Screenshot
		}
		if evidenceFile.MatchString(e.CrashFile) {
			b.Crash = e.CrashFile
		}
		if evidenceFile.MatchString(e.LogcatFile) {
			b.Logcat = e.LogcatFile
		}
		out = append(out, b)
	}
	return out
}
