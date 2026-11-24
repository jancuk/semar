package mobilemcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/mobilerec"
)

// RecordRequest says what an agent wants to record. The backend turns it into a session that is not yet
// started: the repository, the environment, and the way to open the device are the command's to know.
type RecordRequest struct {
	Kind    mobilerec.Kind
	ID      string
	Ticket  string
	Title   string
	Service string
	// Reset is restart, clear, none, or "" for the one that the service has.
	Reset string
	// Serial is the device that the live tools used, or "".
	Serial string
	// Use is a flow that a scenario starts with, run live, with With as its params.
	Use          string
	With         map[string]string
	Replace      bool
	ConfirmClear bool
	// Author is who records: the agent, over MCP, by the name that its client gave (see mcpkit.AgentAuthor).
	// The backend gives a default when it is the zero value.
	Author domain.ProviderRef
}

// VerifyStep is one line of a verify.
type VerifyStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// VerifyResult is how a saved recording did on a clean device session. The code decides it, not the model.
type VerifyResult struct {
	Status  string       `json:"status"`
	Message string       `json:"message"`
	Steps   []VerifyStep `json:"steps,omitempty"`
	Report  string       `json:"reportPath,omitempty"`
}

// RunResult is the verdict of a replay of a saved scenario.
type RunResult struct {
	Outcome string   `json:"outcome"`
	Reasons []string `json:"reasons,omitempty"`
	Report  string   `json:"reportPath,omitempty"`
}

// CatalogItem is a scenario or a flow that the repository has.
type CatalogItem struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// Catalog is what semar_list gives: the apps that can be recorded, and what is recorded already.
type Catalog struct {
	Environment string        `json:"environment,omitempty"`
	Services    []string      `json:"services"`
	Scenarios   []CatalogItem `json:"scenarios"`
	Flows       []CatalogItem `json:"flows"`
}

// RecordBackend is what the record tools need of the command. The command line builds it from the studio
// backend and the runner of semar run; tests replace it.
type RecordBackend interface {
	// Start makes a session that has not started. The Manager starts it after it freed the device.
	Start(ctx context.Context, r RecordRequest) (*mobilerec.Session, error)
	// Verify runs what was saved on a clean device session. The session has given the device away.
	Verify(ctx context.Context, s *mobilerec.Session, saved *mobilerec.Saved) (VerifyResult, error)
	// RunScenario replays a saved scenario as semar run does, with no model.
	RunScenario(ctx context.Context, id string) (RunResult, error)
	// List says what can be recorded and what is.
	List(ctx context.Context) (Catalog, error)
}

// recording is the session that owns the device while a recording goes on.
type recording struct {
	sess   *mobilerec.Session
	kind   mobilerec.Kind
	id     string
	ticket string
	title  string
	// replace is what semar_record_start asked for: a save may write over a recording of the same ID.
	replace bool
	// frame is the latest frame that a tool gave: the refs e1..eN are its elements.
	frame *mobilerec.Frame
	// unsaved says that a step was added or removed since the last save.
	unsaved bool
}

// SetRecordBackend attaches the backend of the record tools. Without one RecordTools gives none.
func (m *Manager) SetRecordBackend(b RecordBackend) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rb = b
}

// Recording says whether a recording session owns the device now.
func (m *Manager) Recording() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rec != nil
}

// ActiveSession is the session of the recording that goes on, or nil. A tool of another group that acts
// on it must hold no lock of the Manager while it does.
func (m *Manager) ActiveSession() *mobilerec.Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rec == nil {
		return nil
	}
	return m.rec.sess
}

// InvalidateRefs forgets the refs of the last listing, as a tool that changed the screen outside the
// Manager must. The next listing makes new ones.
func (m *Manager) InvalidateRefs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refs.Reset("", nil)
}

// NoteRecorded tells the Manager that a tool of another group did something in the recording: the frame is
// the new screen, and the recording has something to save. A nil frame only marks the change.
func (m *Manager) NoteRecorded(f *mobilerec.Frame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rec == nil {
		return
	}
	if f != nil {
		m.rec.frame = f
	}
	m.rec.unsaved = true
}

// endRecordingLocked drops the recording: the device is given away and the secrets are wiped. What was
// saved stays on disk.
func (m *Manager) endRecordingLocked(ctx context.Context) error {
	r := m.rec
	if r == nil {
		return nil
	}
	m.rec, m.mode = nil, modeLive
	m.refs.Reset("", nil)
	err := r.sess.Close(ctx)
	r.sess.Wipe()
	return err
}

// RecordTools gives the semar_* tools of the recording, or none when no backend is attached.
func (m *Manager) RecordTools() []mcp.Tool {
	m.mu.Lock()
	has := m.rb != nil
	m.mu.Unlock()
	if !has {
		return nil
	}
	defs := m.recordDefs()
	tools := make([]mcp.Tool, 0, len(defs))
	for _, d := range defs {
		tools = append(tools, m.toolFor(d))
	}
	return tools
}

var (
	idSchema   = mcp.String("The ID of the scenario or the flow.")
	nameSchema = mcp.String("The name of the screen: a letter or digit first, then letters, digits, - and _.")
)

var assertKinds = []string{"equals", "contains", "exists", "not_exists", "visible", "hidden", "enabled", "disabled", "checked", "unchecked", "count", "attribute"}

func (m *Manager) recordDefs() []*toolDef {
	pointProps := map[string]mcp.Schema{"ref": refSchema, "x": xSchema, "y": ySchema}
	with := func(extra map[string]mcp.Schema) mcp.Schema {
		p := map[string]mcp.Schema{}
		for k, v := range pointProps {
			p[k] = v
		}
		for k, v := range extra {
			p[k] = v
		}
		return mcp.Object(p)
	}
	return []*toolDef{
		{
			name: "semar_list", title: "List what can be recorded", readOnly: true,
			desc:   "List the apps that can be recorded, and the scenarios and flows that the repository has already.",
			schema: mcp.Object(nil),
			run:    m.semarList,
		},
		{
			name: "semar_record_start", title: "Start a recording",
			desc: "Start recording a scenario or a flow on the device. The live device is released, the app is reset and started, and from now on the mobile_* tools also record: each tap, text, key, swipe, and link becomes a step, found again by a locator that a replay can use. Passwords are typed with mobile_type_keys secretEnv, and never saved. Add checks with semar_record_assert. A scenario needs at least one check.",
			schema: mcp.Object(map[string]mcp.Schema{
				"kind":         mcp.Enum("scenario is a test with checks. flow is a reusable sequence of steps.", "scenario", "flow"),
				"id":           idSchema,
				"service":      mcp.String("The app to record, from semar_list. Needed when the repository has several."),
				"ticket":       mcp.String("The ticket of a scenario, such as SHOP-482."),
				"title":        mcp.String("The title of the scenario, or the description of the flow."),
				"reset":        mcp.Enum("How the first launch resets the app. clear deletes its data (an emulator, or with confirmClear).", "restart", "clear", "none"),
				"use":          mcp.String("A flow that a scenario starts with. It runs now, and is recorded as one step."),
				"with":         mcp.NameValue("The params of that flow, as name and value pairs."),
				"replace":      mcp.Bool("Let the save write over a recording of the same ID."),
				"confirmClear": mcp.Bool("Agree that reset: clear deletes the data of the app on a physical device."),
			}).Required("kind", "id"),
			run: m.recBegin,
		},
		{
			name: "semar_record_assert", title: "Record a check",
			desc: "Record a check of the screen as it is now: it changes nothing on the device, so the answer is a short confirmation and the refs of the last listing stay valid. Name the element with ref (or x and y). kind is equals (the default), contains, exists, not_exists, visible, hidden, enabled, disabled, checked, unchecked, count, or attribute. value is the expected text (it defaults to what the element shows now), the number for count, or the value of the attribute.",
			schema: with(map[string]mcp.Schema{
				"kind":      mcp.Enum("How the element is checked.", assertKinds...),
				"value":     mcp.String("The expected text, count, or attribute value."),
				"attribute": mcp.String("The attribute that kind attribute reads."),
			}),
			run: m.recAssert,
		},
		{
			name: "semar_record_capture", title: "Capture a value",
			desc: "Take the text of an element into a variable for the steps after it: write {{name}} in a text that you type or in the value of a check. Only a flow or a scenario's later steps use it. pattern is a regular expression: the value is its first group.",
			schema: mcp.Object(map[string]mcp.Schema{
				"ref":     refSchema,
				"x":       xSchema,
				"y":       ySchema,
				"name":    mcp.String("The name of the variable: a letter first, then letters, digits, and _."),
				"pattern": mcp.String("An optional regular expression."),
			}).Required("name"),
			run: m.recCapture,
		},
		{
			name: "semar_record_screen", title: "Name the screen",
			desc:   "Name the screen that is in front. The name is used in the screens file of the app, for this and the next elements of the screen.",
			schema: mcp.Object(map[string]mcp.Schema{"name": nameSchema}).Required("name"),
			run:    m.recScreen,
		},
		{
			name: "semar_record_steps", title: "List the recorded steps", readOnly: true,
			desc:   "List the steps recorded so far, with the locator of each element.",
			schema: mcp.Object(nil),
			run:    m.recSteps,
		},
		{
			name: "semar_record_delete_step", title: "Delete a step",
			desc: "Delete the nth recorded step (from 1). The device is not changed, so the steps may no longer fit the screen: replay the recording with semar_record_save and verify.",
			schema: mcp.Object(map[string]mcp.Schema{
				"n": mcp.Int("The number of the step, from semar_record_steps.").With("minimum", 1),
			}).Required("n"),
			run: m.recDelete,
		},
		{
			name: "semar_record_save", title: "Save the recording",
			desc: "Write the recording: a flow, or a scenario with its script, and the elements it names in the screens file of the app. With verify (the default) the device is given back, the saved scenario or flow is run once on a clean session, and the verdict of the code is returned with its report. The recording stays open for more steps.",
			schema: mcp.Object(map[string]mcp.Schema{
				"replace":      mcp.Bool("Write over a recording of the same ID."),
				"verify":       mcp.Bool("Run it once after saving. True by default."),
				"confirmClear": mcp.Bool("Agree that reset: clear deletes the data of the app on a physical device."),
			}),
			run: m.recSave,
		},
		{
			name: "semar_record_undo", title: "Undo the save",
			desc:   "Put the repository back as it was before the latest save: the files that it wrote are removed. It refuses when one of them was changed since.",
			schema: mcp.Object(nil),
			run:    m.recUndo,
		},
		{
			name: "semar_record_stop", title: "Stop the recording",
			desc: "End the recording and give the device back to the live tools. Steps that were not saved are lost, so it refuses until you save them or pass discard: true. What was saved stays.",
			schema: mcp.Object(map[string]mcp.Schema{
				"discard": mcp.Bool("Drop the steps that were not saved."),
			}),
			run: m.recStop,
		},
		{
			name: "semar_run_scenario", title: "Run a scenario",
			desc: "Run a saved scenario on the device as semar run does, with no model: the code decides the outcome. It needs no recording to be going on, and it releases the live device first. It gives the outcome, the reasons, and the path of the report.",
			schema: mcp.Object(map[string]mcp.Schema{
				"id": mcp.String("The ID of the scenario."),
			}).Required("id"),
			run: m.runScenario,
		},
	}
}

// hint says what to do next for the errors of the recording that the model can fix.
func (m *Manager) hint(err error) error {
	switch {
	case errors.Is(err, mobilerec.ErrNeedsConfirm):
		return errors.New(err.Error() + ". Pass confirmClear: true to agree that the data of the app on this device is deleted")
	case errors.Is(err, mobilerec.ErrNoLocator):
		return errors.New("no locator finds that element alone, so a replay could not find it. Act on another element: list the screen with mobile_list_elements_on_screen and use a ref of an element that has a locator (an element with a warning has none)")
	case errors.Is(err, mobilerec.ErrNeedsSecret):
		return errors.New("that field takes a password: type it with secretEnv (the name of an environment variable that holds it) and no text, so that it is never saved")
	case errors.Is(err, mobilerec.ErrCaptureHoldsText):
		return errors.New("every locator of that element holds the text that the capture reads, so a saved file would hold the value. Capture an element whose id or label is not the value (in a Flutter app, ask its developers for Semantics(identifier: ...) on it), or check the text with semar_record_assert instead of capturing it")
	case errors.Is(err, mobilerec.ErrNoCheck):
		return errors.New("a scenario needs at least one check: record one with semar_record_assert")
	}
	return err
}

func (m *Manager) needRecording() (*recording, error) {
	if m.rec == nil {
		return nil, errors.New("there is no recording: start one with semar_record_start")
	}
	return m.rec, nil
}

func (m *Manager) semarList(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	if err := noArgs(call); err != nil {
		return mcp.Result{}, err
	}
	cat, err := m.rb.List(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if cat.Services == nil {
		cat.Services = []string{}
	}
	if cat.Scenarios == nil {
		cat.Scenarios = []CatalogItem{}
	}
	if cat.Flows == nil {
		cat.Flows = []CatalogItem{}
	}
	return textJSON(map[string]any{"catalog": cat, "recording": m.rec != nil})
}

func (m *Manager) recBegin(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Kind         string        `json:"kind"`
		ID           string        `json:"id"`
		Service      string        `json:"service"`
		Ticket       string        `json:"ticket"`
		Title        string        `json:"title"`
		Reset        string        `json:"reset"`
		Use          string        `json:"use"`
		With         mcp.StringMap `json:"with"`
		Replace      bool          `json:"replace"`
		ConfirmClear bool          `json:"confirmClear"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if m.rec != nil {
		return mcp.Result{}, fmt.Errorf("the recording of %s %s is going on: save it and end it with semar_record_stop first", m.rec.kind, m.rec.id)
	}
	kind := mobilerec.Kind(strings.ToLower(strings.TrimSpace(a.Kind)))
	if kind != mobilerec.KindScenario && kind != mobilerec.KindFlow {
		return mcp.Result{}, errors.New("kind must be scenario or flow")
	}
	if strings.TrimSpace(a.ID) == "" {
		return mcp.Result{}, errors.New("id is needed: the ID of the scenario or the flow")
	}
	switch a.Reset {
	case "", "restart", "clear", "none":
	default:
		return mcp.Result{}, errors.New("reset must be restart, clear, or none")
	}
	if len(a.With) > 0 && a.Use == "" {
		return mcp.Result{}, errors.New("with gives the params of a flow: name the flow in use")
	}
	if m.recOut != "" {
		return mcp.Result{}, errors.New("a screen recording is going on: stop it with mobile_stop_screen_recording before you start a recording")
	}
	req := RecordRequest{
		Kind: kind, ID: strings.TrimSpace(a.ID), Ticket: a.Ticket, Title: a.Title, Service: a.Service, Reset: a.Reset,
		Serial: m.currentSerial(), Use: a.Use, With: a.With, Replace: a.Replace, ConfirmClear: a.ConfirmClear,
		Author: mcpkit.AgentAuthor(call.Client.Name),
	}
	sess, err := m.rb.Start(ctx, req)
	if err != nil {
		return mcp.Result{}, err
	}
	// The live device and the recording take the same lease: the device moves from one to the other by
	// closing and opening. A recording that cannot start leaves the live tools to open the device again.
	if err := m.closeLocked(ctx); err != nil {
		sess.Wipe()
		return mcp.Result{}, fmt.Errorf("the device in use could not be released: %w", err)
	}
	f, err := sess.Start(ctx)
	if err != nil {
		_ = sess.Close(ctx)
		sess.Wipe()
		return mcp.Result{}, m.hint(err)
	}
	m.rec = &recording{sess: sess, kind: kind, id: req.ID, ticket: sess.Ticket(), title: sess.Title(), replace: a.Replace, frame: f}
	m.mode = modeRecord
	m.refs.Reset("", nil)
	return mcp.TextResult(fmt.Sprintf("recording the %s %s: the app is started. Each mobile_* action now also records a step.\n%s", kind, req.ID, m.frameText(f))), nil
}

func (m *Manager) recAssert(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		pointArgs
		Kind      string `json:"kind"`
		Value     string `json:"value"`
		Attribute string `json:"attribute"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	r, err := m.needRecording()
	if err != nil {
		return mcp.Result{}, err
	}
	t, err := m.recTarget(a.Ref, a.X, a.Y)
	if err != nil {
		return mcp.Result{}, err
	}
	before := len(r.sess.Steps())
	f, err := r.sess.Assert(ctx, mobilerec.Assert{
		Frame: r.frame.ID, Target: *t, Kind: mobilerec.AssertKind(strings.ToLower(strings.TrimSpace(a.Kind))),
		Value: a.Value, Attribute: a.Attribute,
	})
	return m.afterCheck(r, before, f, err)
}

func (m *Manager) recCapture(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		pointArgs
		Name    string `json:"name"`
		Pattern string `json:"pattern"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	r, err := m.needRecording()
	if err != nil {
		return mcp.Result{}, err
	}
	t, err := m.recTarget(a.Ref, a.X, a.Y)
	if err != nil {
		return mcp.Result{}, err
	}
	before := len(r.sess.Steps())
	f, value, err := r.sess.CaptureValue(ctx, mobilerec.CaptureReq{Frame: r.frame.ID, Target: *t, Name: a.Name, Pattern: a.Pattern})
	res, err := m.afterCheck(r, before, f, err)
	if err == nil {
		res.Content = append([]mcp.Content{{Type: "text", Text: fmt.Sprintf("{{%s}} is %q now", a.Name, value)}}, res.Content...)
	}
	return res, err
}

func (m *Manager) recScreen(_ context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	r, err := m.needRecording()
	if err != nil {
		return mcp.Result{}, err
	}
	f, err := r.sess.SetScreen(a.Name)
	if err != nil {
		return mcp.Result{}, err
	}
	r.frame = f
	return mcp.TextResult("the screen is called " + a.Name + "\n" + m.frameHead(f) + "the refs of the last listing stay valid"), nil
}

func (m *Manager) recSteps(_ context.Context, call mcp.Call) (mcp.Result, error) {
	if err := noArgs(call); err != nil {
		return mcp.Result{}, err
	}
	r, err := m.needRecording()
	if err != nil {
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{"kind": r.kind, "id": r.id, "steps": r.sess.Steps(), "unsaved": r.unsaved})
}

func (m *Manager) recDelete(_ context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		N int `json:"n"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	r, err := m.needRecording()
	if err != nil {
		return mcp.Result{}, err
	}
	if err := r.sess.DeleteStep(a.N); err != nil {
		return mcp.Result{}, err
	}
	r.unsaved = true
	out := map[string]any{"deleted": a.N, "steps": r.sess.Steps()}
	if r.sess.Dirty() {
		out["note"] = "the device had done that step, so it is not where the steps say: save with verify to replay the recording before you trust it"
	}
	return textJSON(out)
}

func (m *Manager) recSave(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Replace      bool  `json:"replace"`
		Verify       *bool `json:"verify"`
		ConfirmClear bool  `json:"confirmClear"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	r, err := m.needRecording()
	if err != nil {
		return mcp.Result{}, err
	}
	if a.ConfirmClear {
		r.sess.ConfirmClear()
	}
	if err := r.sess.Retarget(mobilerec.Next{Kind: r.kind, ID: r.id, Ticket: r.ticket, Title: r.title}); err != nil {
		return mcp.Result{}, err
	}
	sv, err := r.sess.Save(mobilerec.SaveOptions{Replace: a.Replace || r.replace})
	if err != nil {
		return mcp.Result{}, m.hint(err)
	}
	r.unsaved = false
	out := map[string]any{
		"kind": r.kind, "id": r.id, "files": sv.Files, "actions": sv.Draft.Actions, "checks": sv.Draft.Checks,
	}
	if len(sv.Draft.Notes) > 0 {
		out["notes"] = sv.Draft.Notes
	}
	if a.Verify != nil && !*a.Verify {
		return textJSON(out)
	}
	if err := r.sess.Suspend(ctx); err != nil {
		out["verify"] = map[string]any{"status": "error", "message": "the device could not be given away for the verify: " + err.Error()}
		return textJSON(out)
	}
	vr, verr := m.rb.Verify(ctx, r.sess, sv)
	if verr != nil {
		vr = VerifyResult{Status: "error", Message: "the verify could not run: " + verr.Error()}
	}
	out["verify"] = vr
	f, rerr := r.sess.Resume(ctx)
	if rerr != nil {
		out["note"] = "the device could not be taken back, so the recording cannot go on: end it with semar_record_stop. " + rerr.Error()
		return textJSON(out)
	}
	// The verify left the app at the start. Take the device through the steps again, so that new steps go
	// after the ones that are recorded.
	if n := len(r.sess.Steps()); n > 0 {
		if gf, gerr := r.sess.Goto(ctx, n); gerr == nil {
			f = gf
		} else {
			out["note"] = "the device is at the start, and the steps could not be replayed to the end: " + gerr.Error()
			if gf != nil {
				f = gf
			}
		}
	}
	r.frame = f
	out["screen"] = m.frameText(f)
	return textJSON(out)
}

func (m *Manager) recUndo(_ context.Context, call mcp.Call) (mcp.Result, error) {
	if err := noArgs(call); err != nil {
		return mcp.Result{}, err
	}
	r, err := m.needRecording()
	if err != nil {
		return mcp.Result{}, err
	}
	if err := r.sess.Undo(); err != nil {
		return mcp.Result{}, err
	}
	r.unsaved = true
	return mcp.TextResult("the save is undone: the files that it wrote are removed. The recording is still open."), nil
}

func (m *Manager) recStop(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Discard bool `json:"discard"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	r, err := m.needRecording()
	if err != nil {
		return mcp.Result{}, err
	}
	if r.unsaved && !a.Discard {
		return mcp.Result{}, errors.New("the recording has steps that were not saved: save them with semar_record_save, or pass discard: true to drop them")
	}
	saved := r.sess.Last() != nil
	if err := m.endRecordingLocked(ctx); err != nil {
		return mcp.Result{}, fmt.Errorf("the recording ended, but the device was not released cleanly: %w", err)
	}
	msg := "the recording ended and the device is back for the live tools."
	if !saved {
		msg += " Nothing was saved."
	}
	return mcp.TextResult(msg), nil
}

func (m *Manager) runScenario(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if m.rec != nil {
		return mcp.Result{}, errors.New("a recording is going on: save it and end it with semar_record_stop before you run a scenario")
	}
	if strings.TrimSpace(a.ID) == "" {
		return mcp.Result{}, errors.New("id is needed: the ID of a saved scenario")
	}
	if m.recOut != "" {
		return mcp.Result{}, errors.New("a screen recording is going on: stop it with mobile_stop_screen_recording before you run a scenario")
	}
	if err := m.closeLocked(ctx); err != nil {
		return mcp.Result{}, fmt.Errorf("the device in use could not be released: %w", err)
	}
	res, err := m.rb.RunScenario(ctx, strings.TrimSpace(a.ID))
	if err != nil {
		return mcp.Result{}, err
	}
	if res.Reasons == nil {
		res.Reasons = []string{}
	}
	return textJSON(res)
}
