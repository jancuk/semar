package report

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	_ "embed"

	"github.com/jancuk/semar/internal/domain"
)

//go:embed report.html.tmpl
var htmlSource string

var htmlTemplate = template.Must(template.New("report.html.tmpl").Option("missingkey=error").Parse(htmlSource))

const (
	// maxShotBytes is the largest screenshot that the report embeds. A larger one is left out.
	maxShotBytes = 2 << 20
	// maxBodyShown is how many characters of a request or a response body the report shows.
	maxBodyShown = 4000
	noTicket     = "No ticket"
)

type exchangeView struct {
	N                 int
	Request, Response string
}

type webView struct {
	Why, Target, Result string
	Shot                *shotEmbed
}

// shotEmbed is a screenshot of a web step. Src is empty when the report leaves it out, and Path says where it is.
type shotEmbed struct {
	Src  template.URL
	Path string
}

type stepView struct {
	ID, Action string
	// Open is true for a step with a check that did not hold.
	Open      bool
	Checks    []checkView
	Exchanges []exchangeView
	Web       []webView
	Docs      []docView
}

type htmlScenario struct {
	scenarioView
	Open  bool
	Steps []stepView
}

type ticketGroup struct {
	Ticket    string
	Scenarios []htmlScenario
}

type countRow struct {
	Word  string
	N     int
	Class string
}

type htmlPage struct {
	markdownView
	Title         string
	Exit          int
	ExitMeaning   string
	CountRows     []countRow
	Attention     []ticketGroup
	AttentionN    int
	Passed        []ticketGroup
	PassedN       int
	ShotsLeftOut  int
	ShotsDir      string
	ScenarioCount int
}

// HTML writes the report as one file that asks for nothing from the network: its style is in the file and
// each screenshot is a data URI. It reads the same view as Markdown. A scenario that needs attention is
// open and listed first, and a passed one is closed. The screenshots of a scenario that failed, was
// inconclusive or is flaky are embedded, and of a passed web scenario only the last one.
func HTML(w io.Writer, d Data) error {
	v := rawViewer.buildView(d)
	p := htmlPage{markdownView: v, Title: "Semar report " + string(d.Run.ID)}
	p.Exit, p.ExitMeaning = exitMeaning(d.Run, v.Interrupted)
	p.CountRows = countRows(d.Run.Results)
	p.ScenarioCount = len(d.Run.Results)

	embed := embedder{d: d}
	var attention, passed []htmlScenario
	for _, sv := range append(slices.Clone(v.Attention), v.Passed...) {
		hs := htmlScenario{scenarioView: sv}
		res, _ := resultOf(d.Run, sv.ID)
		needs := sv.Class != domain.OutcomePassed || sv.Flaky
		hs.Open = needs
		hs.Steps = embed.steps(rawViewer, d, res, needs)
		if needs {
			attention = append(attention, hs)
		} else {
			passed = append(passed, hs)
		}
	}
	p.Attention, p.AttentionN = groupByTicket(attention), len(attention)
	p.Passed, p.PassedN = groupByTicket(passed), len(passed)
	p.ShotsLeftOut, p.ShotsDir = embed.leftOut, d.RunDir
	if err := htmlTemplate.Execute(w, p); err != nil {
		return fmt.Errorf("render html report %s: %w", d.Run.ID, err)
	}
	return nil
}

func resultOf(run domain.Run, id string) (domain.ScenarioResult, bool) {
	for _, r := range run.Results {
		if string(r.Scenario) == id {
			return r, true
		}
	}
	return domain.ScenarioResult{}, false
}

// exitMeaning is the exit code that the run ended with, and what it says. It is the code that
// semar run returns, from the same functions.
func exitMeaning(run domain.Run, interrupted bool) (int, string) {
	code := ExitCode(run.Results)
	if len(run.Journeys) > 0 {
		code = JourneyExitCode(run.Journeys)
	}
	if interrupted {
		code = max(code, 2)
	}
	switch code {
	case 0:
		return 0, "Everything that ran passed. A flaky scenario counts as passed."
	case 1:
		return 1, "At least one scenario failed."
	}
	return 2, "Nothing failed, but at least one scenario did not pass: it was blocked, inconclusive, stale or not run, or the run did not finish."
}

func countRows(results []domain.ScenarioResult) []countRow {
	var rows []countRow
	for _, o := range knownOutcomes {
		n := 0
		for _, r := range results {
			if r.Outcome == o {
				n++
			}
		}
		if n > 0 {
			rows = append(rows, countRow{Word: strings.ReplaceAll(string(o), "_", " "), N: n, Class: string(o)})
		}
	}
	if n := flakyCount(results); n > 0 {
		rows = append(rows, countRow{Word: "flaky (counted as passed)", N: n, Class: "flaky"})
	}
	return rows
}

// groupByTicket keeps the order of the scenarios and the order in which each ticket first appears.
func groupByTicket(list []htmlScenario) []ticketGroup {
	var out []ticketGroup
	at := map[string]int{}
	for _, s := range list {
		t := s.Ticket
		if t == "" {
			t = noTicket
		}
		i, ok := at[t]
		if !ok {
			i = len(out)
			at[t] = i
			out = append(out, ticketGroup{Ticket: t})
		}
		out[i].Scenarios = append(out[i].Scenarios, s)
	}
	return out
}

type embedder struct {
	d       Data
	leftOut int
}

// steps lists the steps of a scenario with what each one did. all says whether every screenshot is
// embedded or only the last one of the scenario.
func (e *embedder) steps(vw viewer, d Data, res domain.ScenarioResult, all bool) []stepView {
	sc := d.Scenarios[res.Scenario]
	var order []domain.StepID
	actions := map[domain.StepID]string{}
	for _, s := range sc.Steps {
		order = append(order, s.ID)
		actions[s.ID] = s.Action
	}
	for _, c := range res.Checks {
		if !slices.Contains(order, c.Step) {
			order = append(order, c.Step)
		}
	}
	lastShot := -1
	for i, a := range d.Activity {
		if a.Use.Scenario == res.Scenario && a.Surface == domain.SurfaceWeb && screenshotPath.MatchString(a.Screenshot) {
			lastShot = i
		}
	}
	findings := DocFindings(d.Activity)
	var out []stepView
	for _, id := range order {
		sv := stepView{ID: vw.show(string(id)), Action: vw.show(actions[id])}
		for _, c := range res.Checks {
			if c.Step == id {
				cv := vw.checkViewOf(c, sc)
				sv.Checks = append(sv.Checks, cv)
				sv.Open = sv.Open || c.State != domain.CheckTrue
			}
		}
		seen := map[string]bool{}
		for i, a := range d.Activity {
			if a.Use.Scenario != res.Scenario || a.Use.Step != id {
				continue
			}
			switch a.Surface {
			case domain.SurfaceWeb:
				wv := webView{Why: vw.show(a.Why), Target: vw.show(a.Target), Result: vw.show(a.Result)}
				if screenshotPath.MatchString(a.Screenshot) {
					wv.Shot = e.shot(a.Screenshot, all || i == lastShot)
				}
				sv.Web = append(sv.Web, wv)
			default:
				if a.Evidence == "" || seen[a.Evidence] || d.Files == nil {
					continue
				}
				seen[a.Evidence] = true
				if x, ok := e.exchange(d.Files, a.Evidence); ok {
					x.N = len(sv.Exchanges) + 1
					sv.Exchanges = append(sv.Exchanges, x)
				}
			}
		}
		for _, f := range findings {
			if f.Scenario == string(res.Scenario) && f.Step == string(id) {
				effect := "warning, no outcome changed"
				if f.Failed {
					effect = "failed the step"
				}
				sv.Docs = append(sv.Docs, docView{Operation: vw.show(f.Operation), Text: vw.show(f.Text), Effect: effect})
			}
		}
		out = append(out, sv)
	}
	return out
}

// shot embeds a screenshot when embed is true and the file is a PNG or a JPEG that is not too large. It
// counts each one that it leaves out.
func (e *embedder) shot(rel string, embed bool) *shotEmbed {
	s := &shotEmbed{Path: rel}
	if !embed || e.d.Files == nil {
		e.leftOut++
		return s
	}
	data, err := e.d.Files.File(rel, maxShotBytes)
	kind := ""
	if err == nil {
		kind = http.DetectContentType(data)
	}
	if kind != "image/jpeg" && kind != "image/png" {
		e.leftOut++
		return s
	}
	// The prefix is fixed and the rest is base64, so the value holds nothing that can end the attribute.
	s.Src = template.URL("data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(data))
	return s
}

func (e *embedder) exchange(f Files, rel string) (exchangeView, bool) {
	a, err := f.Action(rel)
	if err != nil || a.Request == nil {
		return exchangeView{}, false
	}
	x := exchangeView{Request: a.Request.Method + " " + a.Request.URL + headerText(a.Request.Headers) + bodyText(a.Request.Body)}
	switch {
	case a.Response != nil:
		x.Response = fmt.Sprintf("HTTP %d%s%s", a.Response.Status, headerText(a.Response.Headers), bodyText(a.Response.Body))
		if a.Response.Truncated {
			x.Response += "\n[the stored body was cut when it was recorded]"
		}
	case a.Refused != "":
		x.Response = "not sent: " + a.Refused
	case a.Err != "":
		x.Response = "no response: " + a.Err
	default:
		x.Response = "no response"
	}
	return x, true
}

func headerText(h map[string][]string) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			b.WriteString("\n" + k + ": " + v)
		}
	}
	return b.String()
}

// bodyText is a stored body as text, cut to maxBodyShown characters. A body that is not text is named by its size.
func bodyText(b domain.Body) string {
	if len(b) == 0 {
		return ""
	}
	if !utf8.Valid(b) {
		return fmt.Sprintf("\n\n(binary body, %d bytes)", len(b))
	}
	head, more := cut(string(b), maxBodyShown)
	if more > 0 {
		head += fmt.Sprintf("\n[cut: %d more characters]", more)
	}
	return "\n\n" + head
}
