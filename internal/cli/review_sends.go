package cli

import (
	"fmt"
	"io"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/sends"
)

const (
	// maxListedText bounds a line of the list view of a scenario.
	maxListedText = 600
	// maxExchangeText bounds a request or a response body of the details view.
	maxExchangeText = 2000
)

// scenarioView is what a review reads besides the scenario: the script that Semar stored for its
// expected results, and the last run of those expected results.
type scenarioView struct {
	store   *evidence.Store
	script  *domain.Script
	last    *evidence.ScenarioRun
	problem string // why the script or the run could not be read; empty when they could
}

func (v scenarioView) checkOf(step domain.StepID, index int) (domain.CheckResult, bool) {
	if v.last == nil {
		return domain.CheckResult{}, false
	}
	for _, c := range v.last.Result.Checks {
		if c.Step == step && c.Index == index {
			return c, true
		}
	}
	return domain.CheckResult{}, false
}

func (v scenarioView) scriptStep(id domain.StepID) (domain.ScriptStep, bool) {
	if v.script == nil {
		return domain.ScriptStep{}, false
	}
	for _, st := range v.script.Steps {
		if st.Step == id {
			return st, true
		}
	}
	return domain.ScriptStep{}, false
}

// scenarioViewer reads the store of the repository at dir. The store is opened on the first
// scenario that is shown.
func scenarioViewer(dir string) func(domain.Scenario) scenarioView {
	var store *evidence.Store
	var openErr error
	return func(sc domain.Scenario) scenarioView {
		if store == nil && openErr == nil {
			store, openErr = evidence.Open(semarDir(dir))
		}
		if openErr != nil {
			return scenarioView{problem: "the store cannot be read: " + openErr.Error()}
		}
		return loadScenarioView(store, sc)
	}
}

func loadScenarioView(store *evidence.Store, sc domain.Scenario) scenarioView {
	v := scenarioView{store: store}
	h := domain.ExpectHash(sc)
	script, ok, err := store.Script(sc.ID, h)
	switch {
	case err != nil:
		v.problem = "the stored script cannot be read: " + err.Error()
	case ok:
		v.script = &script
	}
	last, ok, err := store.LastRunOf(sc.ID, h)
	switch {
	case err != nil && v.problem == "":
		v.problem = "the runs cannot be listed: " + err.Error()
	case ok:
		v.last = &last
	}
	return v
}

// printSends prints what the step sends, under its line, each line cut and made safe.
func (v scenarioView) printSends(w io.Writer, indent string, step domain.StepID) {
	st, ok := v.scriptStep(step)
	if !ok {
		return
	}
	for _, l := range sends.Lines(st) {
		fmt.Fprintf(w, "%s%s\n", indent, listed(l))
	}
}

// printObserved prints what the last run observed for one expected result.
func (v scenarioView) printObserved(w io.Writer, indent string, step domain.StepID, index int) {
	if v.last == nil {
		return
	}
	run := terminalSafe(oneLine(string(v.last.Run)))
	c, ok := v.checkOf(step, index)
	switch {
	case !ok:
		fmt.Fprintf(w, "%slast run %s: not evaluated: the run stored no result for this check\n", indent, run)
	case c.State == domain.CheckTrue:
		fmt.Fprintf(w, "%slast run %s: observed %s, met\n", indent, run, listed(c.Observed))
	case c.State == domain.CheckFalse:
		fmt.Fprintf(w, "%slast run %s: observed %s, not met\n", indent, run, listed(c.Observed))
	default:
		fmt.Fprintf(w, "%slast run %s: not evaluated: %s\n", indent, run, listed(c.Detail))
	}
}

// printNotes says what is missing: the script that review would show, and the run that it would
// compare with.
func (v scenarioView) printNotes(w io.Writer, indent string, id domain.ScenarioID) {
	switch {
	case v.script != nil:
	case v.problem != "":
		fmt.Fprintf(w, "%s%s\n", indent, listed(v.problem))
	default:
		fmt.Fprintf(w, "%sno script is stored for this scenario\n", indent)
	}
	if v.last == nil {
		fmt.Fprintf(w, "%snot run yet: semar run --scenario %s sends it and stores what the service answers\n", indent, id)
	}
}

// printExchanges prints, for each step of the scenario, the exchange that the last run stored.
func (v scenarioView) printExchanges(w io.Writer, sc domain.Scenario) {
	if v.last == nil || v.store == nil {
		return
	}
	exchanges, err := v.store.Exchanges(v.last.Run, sc.ID)
	if err != nil {
		fmt.Fprintf(w, "Last run %s: the stored exchanges cannot be read: %s\n", terminalSafe(oneLine(string(v.last.Run))), exchangeText(err.Error()))
		return
	}
	for _, st := range sc.Steps {
		a, ok := exchanges[st.ID]
		if !ok {
			continue
		}
		fmt.Fprintf(w, "Last run %s, step %s:\n", terminalSafe(oneLine(string(v.last.Run))), terminalSafe(oneLine(string(st.ID))))
		switch {
		case a.Request != nil:
			fmt.Fprintf(w, "  request: %s %s\n", exchangeText(a.Request.Method), exchangeText(a.Request.URL))
			if len(a.Request.Body) > 0 {
				fmt.Fprintf(w, "  request body: %s\n", exchangeText(string(a.Request.Body)))
			}
		case a.Web != nil:
			fmt.Fprintf(w, "  web action: %s %s\n", exchangeText(a.Web.Op), exchangeText(a.Web.URL))
		}
		switch {
		case a.Refused != "":
			fmt.Fprintf(w, "  refused, nothing was sent: %s\n", exchangeText(a.Refused))
		case a.Err != "":
			fmt.Fprintf(w, "  no answer: %s\n", exchangeText(a.Err))
		case a.Response != nil:
			fmt.Fprintf(w, "  status: %d\n", a.Response.Status)
			if len(a.Response.Body) > 0 {
				fmt.Fprintf(w, "  response body: %s\n", exchangeText(string(a.Response.Body)))
			}
		}
	}
}

func listed(s string) string       { return terminalSafe(oneLine(cutText(s, maxListedText))) }
func exchangeText(s string) string { return terminalSafe(oneLine(cutText(s, maxExchangeText))) }

// cutText keeps the first limit characters of s and says how many it left out.
func cutText(s string, limit int) string {
	rs := []rune(s)
	if len(rs) <= limit {
		return s
	}
	return string(rs[:limit]) + fmt.Sprintf("… [cut: %d more characters]", len(rs)-limit)
}
