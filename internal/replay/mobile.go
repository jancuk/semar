package replay

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/script"
)

// MobileDoer is the engine of the app on a device.
type MobileDoer interface {
	Do(ctx context.Context, c mobile.Call) (mobile.Result, error)
	// NewScenario makes the next action start with the app of its service reset.
	NewScenario()
}

const reasonNoDevice = "the scenario has steps on a device, and this run has no device"

// prepareMobile checks the device steps of a script before any step is sent, as prepareWeb does for a
// browser: the screens file of each app, its screens and its elements, and the service of each step.
// It returns nil for a scenario with no device step.
func (r *Runner) prepareMobile(sc domain.Scenario, s domain.Script, where []string, run *replayed) (mr *webRun, proceed bool) {
	var apps []string
	uses := map[string][]screens.Use{}
	stepOf := map[domain.StepID]domain.Step{}
	for _, st := range sc.Steps {
		stepOf[st.ID] = st
	}
	for i, ss := range s.Steps {
		if ss.Mobile == nil {
			continue
		}
		label := "step " + string(ss.Step)
		if i < len(where) {
			label = where[i]
		}
		app := ss.Mobile.App
		if app == "" {
			run.blocked = append(run.blocked, fmt.Sprintf("the script does not fit the scenario: step %s: a mobile step needs the app of its screens file", ss.Step))
			continue
		}
		if r.d.MobileServices != nil && !slices.Contains(r.d.MobileServices, ss.Mobile.Service) {
			text := fmt.Sprintf("service %s has no android app in this environment: add android.appPackage to it in semar.yaml", ss.Mobile.Service)
			if !slices.Contains(run.blocked, text) {
				run.blocked = append(run.blocked, text)
			}
			continue
		}
		if !slices.Contains(apps, app) {
			apps = append(apps, app)
		}
		uses[app] = append(uses[app], screens.MobileUse(label, *ss.Mobile, stepElements(stepOf[ss.Step], ss)))
	}
	if len(apps) == 0 && len(run.blocked) == 0 {
		return nil, true
	}
	if len(run.blocked) > 0 {
		return nil, false
	}
	if r.d.Mobile == nil || r.d.Screens == nil {
		run.blocked = append(run.blocked, reasonNoDevice)
		return nil, false
	}
	return r.loadScreens(run, apps, uses)
}

// mobileCall builds the call of one device step, as webCall does for a browser step.
func (wr *webRun) mobileCall(sc domain.Scenario, step domain.Step, ss domain.ScriptStep, vars map[string]string, pass domain.Pass) (mobile.Call, error) {
	m, err := script.RenderMobile(*ss.Mobile, vars)
	if err != nil {
		return mobile.Call{}, err
	}
	scr := wr.screens[m.App]
	names := slices.Concat(m.Elements(), stepElements(step, ss))
	locs := map[string]domain.Locator{}
	for _, name := range screens.Needed(scr, m.Screen, names) {
		raw, _ := scr.Locator(m.Screen, name)
		loc, err := script.RenderLocator(raw, vars)
		if err != nil {
			return mobile.Call{}, fmt.Errorf("the locator of element %s: %w", name, err)
		}
		locs[name] = loc
	}
	if step.Setup {
		pass = domain.PassSetup
	}
	return mobile.Call{
		Use:      domain.UseCase{Pass: pass, Scenario: sc.ID, Step: step.ID},
		By:       domain.ActorReplay,
		Action:   m,
		Locators: locs,
		Watch:    watched(step, ss),
		Until:    until(step, vars),
		Captures: ss.Captures,
		Why:      step.Action,
	}, nil
}

// blockedByDevice gives the reason when an error means that no device can be used: no adb, no device,
// a server that is missing. A person fixes it, and the scenario is blocked and not failed.
func blockedByDevice(err error) (string, bool) {
	var un *mobile.Unavailable
	if errors.As(err, &un) {
		return un.Reason, true
	}
	return "", false
}
