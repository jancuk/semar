package mobilemcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/screens"
)

// LegFlow is what the recording of a journey leg made, before anything is written: the flow that holds the
// actions, and the checks, which a leg holds on its own steps after the flow, because a check that a model
// wrote is not allowed in a flow.
type LegFlow struct {
	Flow domain.Flow
	// App is the screens file that the flow names.
	App string
	// Added are the elements that the screens file does not have yet.
	Added []screens.Addition
	// Checks are the checks that the recording made, in order. Each one waits for an element of a screen and
	// holds the expectations on it.
	Checks []LegCheck
	// Steps is how many steps the recording has after the launch that every recording starts with.
	Steps int
	Notes []string
	// Captures are the names that the steps of the flow capture, in order, once each.
	Captures []string
}

// LegCheck is a check of a leg: a wait for an element of a screen, with what the element must show.
type LegCheck struct {
	ID     string
	Action string
	Screen string
	// Element is the name of the element in the screens file.
	Element string
	Expect  []domain.Expectation
}

// StartLegFlow begins to record the steps of an android leg as the flow flowID of a service, with the app
// restarted: a journey replays the flow from the launch. The recording is a scenario in the session, so that
// a check that the agent records is a check of the leg, and EndLegFlow splits the two. The device moves from
// the live tools to the recording. The journey tools call it when the first action of an android leg comes.
func (m *Manager) StartLegFlow(ctx context.Context, flowID, service, ticket string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.rb == nil:
		return errors.New("this server cannot record: it needs a repository and an environment")
	case m.rec != nil:
		return fmt.Errorf("the recording of %s %s is going on: end it with semar_record_stop first", m.rec.kind, m.rec.id)
	case m.recOut != "":
		return errors.New("a screen recording is going on: stop it with mobile_stop_screen_recording first")
	}
	req := RecordRequest{
		Kind: mobilerec.KindScenario, ID: strings.TrimSpace(flowID), Ticket: ticket, Service: service, Reset: "restart",
		Serial: m.currentSerial(), Replace: true, Author: mcpkit.AgentAuthor(m.client),
	}
	sess, err := m.rb.Start(ctx, req)
	if err != nil {
		return err
	}
	if err := m.closeLocked(ctx); err != nil {
		sess.Wipe()
		return fmt.Errorf("the device in use could not be released: %w", err)
	}
	f, err := sess.Start(ctx)
	if err != nil {
		_ = sess.Close(ctx)
		sess.Wipe()
		return m.hint(err)
	}
	m.rec = &recording{sess: sess, kind: mobilerec.KindScenario, id: req.ID, ticket: sess.Ticket(), title: sess.Title(), replace: true, frame: f}
	m.mode = modeRecord
	m.refs.Reset("", nil)
	return nil
}

// EndLegFlow builds the flow of the recording that StartLegFlow began, writes nothing, and ends the
// recording: the device goes back to the live tools. A recording with no step after the launch gives a
// LegFlow with Steps 0 and no flow.
func (m *Manager) EndLegFlow(ctx context.Context) (LegFlow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rec
	if r == nil {
		return LegFlow{}, errors.New("there is no leg recording")
	}
	steps := max(len(r.sess.Steps())-1, 0)
	if steps == 0 {
		return LegFlow{}, m.endRecordingLocked(ctx)
	}
	d, err := r.sess.Build()
	if errors.Is(err, mobilerec.ErrNoCheck) {
		return LegFlow{}, errors.New("an android leg needs a check, or the journey would prove nothing of the device: record one with semar_record_assert on an element that shows the result, then close the leg again")
	}
	if err != nil {
		return LegFlow{}, m.hint(err)
	}
	out, err := legFlowOf(d)
	if err != nil {
		return LegFlow{}, err
	}
	out.Steps = steps
	if err := m.endRecordingLocked(ctx); err != nil {
		return LegFlow{}, fmt.Errorf("the recording ended, but the device was not released cleanly: %w", err)
	}
	return out, nil
}

// legFlowOf splits the draft of the recording of a leg. The flow holds every step, without the checks. The
// checks are the steps of the leg after the flow, each of which waits for the element that it checks, so
// they come last: a check that was recorded before a later action is refused, because the screen of the
// check is gone by the end of the flow.
func legFlowOf(d mobilerec.Draft) (LegFlow, error) {
	sc, sr := d.Scenario, d.Script
	if len(sc.Steps) != len(sr.Steps) {
		return LegFlow{}, fmt.Errorf("the recording has %d steps and %d actions", len(sc.Steps), len(sr.Steps))
	}
	f := domain.Flow{
		APIVersion: "semar/v1", Kind: "Flow", ID: d.ID, Surface: domain.SurfaceAndroid,
		Description: mobilerec.AgentDescription, App: d.App,
	}
	out := LegFlow{App: d.App, Added: d.Added, Notes: d.Notes}
	firstCheck := -1
	ids := map[string]int{}
	for i, st := range sc.Steps {
		ss := sr.Steps[i]
		if firstCheck >= 0 && (ss.Use != "" || ss.Mobile != nil && ss.Mobile.Do != domain.MobileWait) {
			return LegFlow{}, fmt.Errorf("the check on %s was recorded before the step %s, and a check of a leg is judged at the end of its steps: record the checks last. Delete the step with semar_record_delete_step, or the check, and record them in order", out.Checks[0].Element, st.ID)
		}
		if ss.Mobile != nil {
			mob := *ss.Mobile
			if f.Service == "" {
				f.Service = mob.Service
			}
			mob.Service, mob.App = "", ""
			ss.Mobile = &mob
		}
		for _, c := range ss.Captures {
			if !slices.Contains(out.Captures, c.Name) {
				out.Captures = append(out.Captures, c.Name)
			}
		}
		if len(st.Expect) > 0 {
			if firstCheck < 0 {
				firstCheck = i
			}
			if ss.Mobile == nil || ss.Mobile.Screen == "" {
				return LegFlow{}, fmt.Errorf("the check of the step %s has no screen", st.ID)
			}
			for _, ex := range st.Expect {
				base := "check-" + ex.Check.Element
				ids[base]++
				id := base
				if n := ids[base]; n > 1 {
					id = fmt.Sprintf("%s-%d", base, n)
				}
				out.Checks = append(out.Checks, LegCheck{
					ID: id, Action: "Check " + ex.Check.Element, Screen: ss.Mobile.Screen, Element: ex.Check.Element,
					Expect: []domain.Expectation{ex},
				})
			}
		}
		ss.Expect = nil
		f.Steps = append(f.Steps, ss)
	}
	if len(out.Checks) == 0 {
		return LegFlow{}, mobilerec.ErrNoCheck
	}
	out.Flow = f
	return out, nil
}

// DiscardLegFlow drops the recording of a leg, if one goes on.
func (m *Manager) DiscardLegFlow(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rec == nil {
		return nil
	}
	return m.endRecordingLocked(ctx)
}

// UndoLegStep takes the last step off the recording of a leg. The launch at its start stays.
func (m *Manager) UndoLegStep(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rec == nil {
		return errors.New("there is no leg recording")
	}
	if len(m.rec.sess.Steps()) <= 1 {
		return errors.New("the leg has no step to undo")
	}
	if err := m.rec.sess.DeleteLastStep(); err != nil {
		return err
	}
	m.rec.unsaved = true
	return nil
}

// LegFlowSteps is how many steps the recording has after its launch, or 0 without a recording.
func (m *Manager) LegFlowSteps() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rec == nil {
		return 0
	}
	return max(len(m.rec.sess.Steps())-1, 0)
}

// LegScreen lists the screen that the recording of the leg is on now. The refs of the list are the ones that
// the next action of the recording resolves.
func (m *Manager) LegScreen(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rec == nil {
		return "", errors.New("there is no leg recording")
	}
	return m.recFrameText(ctx)
}

// ForegroundPackage is the package of the app that the device shows. It opens the device when no tool
// has yet, and it is an error while a recording holds the device.
func (m *Manager) ForegroundPackage(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dev, err := m.device(ctx)
	if err != nil {
		return "", err
	}
	pkg, _, err := dev.Foreground(ctx)
	return pkg, err
}
