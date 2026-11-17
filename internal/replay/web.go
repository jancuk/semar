package replay

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/script"
)

// WebDoer is the browser engine.
type WebDoer interface {
	Do(ctx context.Context, c web.Call) (web.Result, error)
}

// ScreenSource gives the screens file of an app, and says whether a person approved it as it is now.
type ScreenSource interface {
	Load(app string) (domain.Screens, error)
	Approved(app string, h domain.Hash) (bool, error)
}

// webRun holds what the web steps of one scenario share: the screens of each app that they name.
type webRun struct {
	screens map[string]domain.Screens
}

const reasonNoBrowser = "the scenario has web steps, and this run has no browser"

// prepareWeb checks the web steps of a script before any step is sent. A screens file that is not
// there, a screen or an element that it lacks, and a step with no app are reasons to block the
// scenario, because the script does not fit what is on disk. A screens file that no person approved
// is a reason to stop with no result (TRD rule 8): the steps are not sent, and each ends inconclusive.
// It returns nil for a scenario with no web step.
func (r *Runner) prepareWeb(sc domain.Scenario, s domain.Script, run *replayed) (wr *webRun, proceed bool) {
	var apps []string
	uses := map[string][]screens.Use{}
	stepOf := map[domain.StepID]domain.Step{}
	for _, st := range sc.Steps {
		stepOf[st.ID] = st
	}
	var first domain.StepID
	for _, ss := range s.Steps {
		if ss.Web == nil {
			continue
		}
		if first == "" {
			first = ss.Step
		}
		app := ss.Web.App
		if app == "" {
			run.blocked = append(run.blocked, fmt.Sprintf("the script does not fit the scenario: step %s: a web step needs the app of its screens file", ss.Step))
			continue
		}
		if !slices.Contains(apps, app) {
			apps = append(apps, app)
		}
		uses[app] = append(uses[app], screens.Use{
			Where:    "step " + string(ss.Step),
			Action:   *ss.Web,
			Elements: stepElements(stepOf[ss.Step], ss),
		})
	}
	if len(apps) == 0 && len(run.blocked) == 0 {
		return nil, true
	}
	if len(run.blocked) > 0 {
		return nil, false
	}
	if r.d.Web == nil || r.d.Screens == nil {
		run.blocked = append(run.blocked, reasonNoBrowser)
		return nil, false
	}
	wr = &webRun{screens: map[string]domain.Screens{}}
	for _, app := range apps {
		scr, err := r.d.Screens.Load(app)
		switch {
		case errors.Is(err, screens.ErrNotFound):
			run.blocked = append(run.blocked, fmt.Sprintf("the screens of %s were not found: %s", app, screens.Path(app)))
			continue
		case err != nil:
			run.blocked = append(run.blocked, fmt.Sprintf("the screens of %s could not be used: %v", app, err))
			continue
		}
		wr.screens[app] = scr
		for _, msg := range screens.Check(scr, uses[app]) {
			run.blocked = append(run.blocked, "the script does not fit the screens of "+app+": "+msg)
		}
	}
	if len(run.blocked) > 0 {
		return nil, false
	}
	for _, app := range apps {
		approved, err := r.d.Screens.Approved(app, domain.ScreensHash(wr.screens[app]))
		switch {
		case err != nil:
			run.notes = append(run.notes, domain.Reason{Rule: domain.OutcomeInconclusive, Step: first,
				Text: fmt.Sprintf("the approval of the screens of %s could not be read: %v", app, err)})
			return nil, false
		case !approved:
			run.notes = append(run.notes, domain.Reason{Rule: domain.OutcomeInconclusive, Step: first,
				Text: fmt.Sprintf("the screens of %s are not approved: semar review %s", app, app)})
			return nil, false
		}
	}
	return wr, true
}

// stepElements are the elements that the checks and the captures of a step name.
func stepElements(st domain.Step, ss domain.ScriptStep) []string {
	var out []string
	add := func(name string) {
		if name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	for _, e := range st.Expect {
		add(e.Check.Element)
	}
	for _, c := range ss.Captures {
		add(c.Element)
	}
	return out
}

// webCall builds the call of one web step: the action with its variables filled in, the locator of
// every element that the step reads, and what the checks need of each.
func (wr *webRun) webCall(sc domain.Scenario, step domain.Step, ss domain.ScriptStep, vars map[string]string, pass domain.Pass) (web.Call, error) {
	w, err := script.RenderWeb(*ss.Web, vars)
	if err != nil {
		return web.Call{}, err
	}
	scr := wr.screens[w.App]
	names := slices.Concat(w.Elements(), stepElements(step, ss))
	locs := map[string]domain.Locator{}
	for _, name := range screens.Needed(scr, w.Screen, names) {
		raw, _ := scr.Locator(w.Screen, name)
		loc, err := script.RenderLocator(raw, vars)
		if err != nil {
			return web.Call{}, fmt.Errorf("the locator of element %s: %w", name, err)
		}
		locs[name] = loc
	}
	if step.Setup {
		pass = domain.PassSetup
	}
	profile := sc.AuthProfile
	if step.As != "" {
		profile = step.As
	}
	return web.Call{
		Use:         domain.UseCase{Pass: pass, Scenario: sc.ID, Step: step.ID},
		By:          domain.ActorReplay,
		Action:      w,
		Locators:    locs,
		Watch:       watched(step, ss),
		Captures:    ss.Captures,
		AuthProfile: profile,
		Why:         step.Action,
	}, nil
}

// watched lists the elements that the engine reads after the action. An element that a check asks
// only for its existence or its number, and that no capture and no text check reads, is read for
// presence only: its absence is no reason for a repair.
func watched(step domain.Step, ss domain.ScriptStep) []web.Watched {
	var order []string
	presence := map[string]bool{}
	use := func(name string, onlyPresence bool) {
		if name == "" {
			return
		}
		if _, seen := presence[name]; !seen {
			order = append(order, name)
			presence[name] = onlyPresence
			return
		}
		presence[name] = presence[name] && onlyPresence
	}
	for _, e := range step.Expect {
		c := e.Check
		use(c.Element, c.Equals == nil && c.NotEquals == nil && c.Contains == nil)
	}
	for _, c := range ss.Captures {
		use(c.Element, false)
	}
	out := make([]web.Watched, len(order))
	for i, name := range order {
		out[i] = web.Watched{Element: name, Presence: presence[name]}
	}
	return out
}

// blockedByBrowser gives the reason when an error means that no browser can be started.
func blockedByBrowser(err error) (string, bool) {
	text, ok := web.ChromeNotFoundText(err)
	if ok {
		return text, true
	}
	return "", false
}
