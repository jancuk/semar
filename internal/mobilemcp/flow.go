package mobilemcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/mcp"
)

const (
	maxFlowWith      = 32
	maxFlowWithValue = 1 << 10
	defaultFlowSec   = 300
	maxFlowSec       = 1800
	// maxCapturedShown bounds the text of one captured value in a result.
	maxCapturedShown = 200
)

// FlowRequest says which saved flow to run, and with what.
type FlowRequest struct {
	ID   string
	With map[string]string
	// KeepState runs the flow on what the device shows. When false the app is reset first, as a
	// standalone replay does.
	KeepState bool
	// Before is called before each step. A non-nil error stops the flow at that step boundary.
	Before func(ctx context.Context, step string) error
}

// FlowStep is what one step of a flow did.
type FlowStep struct {
	Name string `json:"name"`
	Op   string `json:"op,omitempty"`
	OK   bool   `json:"ok"`
	MS   int64  `json:"ms"`
	Note string `json:"note,omitempty"`
}

// FlowOutcome is how a run of a flow went. Status is completed, failed, or refused, and never passed:
// a flow has no expected result.
type FlowOutcome struct {
	Status string         `json:"status"`
	Flow   domain.FlowRef `json:"flow"`
	Steps  []FlowStep     `json:"steps,omitempty"`
	// Checked counts the checks of the flow that held.
	Checked int `json:"checked"`
	// Captured holds the values that the flow captured. The Manager scrubs them before they are shown.
	Captured map[string]string `json:"captured,omitempty"`
	Error    string            `json:"error,omitempty"`
}

// FlowBackend runs a saved flow. The command line builds it.
type FlowBackend interface {
	// Run runs a saved flow on the device that dev holds, through the same android.Platform: no new
	// UiAutomator2 server, and no lease handoff. Actions are recorded in the run of the server.
	Run(ctx context.Context, dev Device, r FlowRequest) (FlowOutcome, error)
}

// UseStep is a flow that a tool ran, as a journey leg or a scenario would use it.
type UseStep struct {
	Flow string
	With map[string]string
}

// flowState is what the Manager keeps for semar_run_flow.
type flowState struct {
	mu   sync.Mutex
	b    FlowBackend
	uses []UseStep
}

// SetFlowBackend attaches the backend of semar_run_flow. Without one FlowTools gives none.
func (m *Manager) SetFlowBackend(b FlowBackend) {
	m.fl.mu.Lock()
	defer m.fl.mu.Unlock()
	m.fl.b = b
}

func (m *Manager) flowBackend() FlowBackend {
	m.fl.mu.Lock()
	defer m.fl.mu.Unlock()
	return m.fl.b
}

// Uses gives the flows that completed through semar_run_flow after the first since of them, in order. A
// caller that records a journey counts them before and after a stretch of work, and turns what is new
// into use steps. It takes no lock of the tools, so a tool may call it.
func (m *Manager) Uses(since int) []UseStep {
	m.fl.mu.Lock()
	defer m.fl.mu.Unlock()
	if since < 0 {
		since = 0
	}
	if since >= len(m.fl.uses) {
		return nil
	}
	out := make([]UseStep, 0, len(m.fl.uses)-since)
	for _, u := range m.fl.uses[since:] {
		out = append(out, UseStep{Flow: u.Flow, With: maps.Clone(u.With)})
	}
	return out
}

func (m *Manager) noteUse(id string, with map[string]string) {
	clean := make(map[string]string, len(with))
	for k, v := range with {
		clean[k] = m.scrub.Text(v)
	}
	m.fl.mu.Lock()
	defer m.fl.mu.Unlock()
	m.fl.uses = append(m.fl.uses, UseStep{Flow: id, With: clean})
}

// FlowTools gives semar_run_flow, or none when no backend is attached.
func (m *Manager) FlowTools() []mcp.Tool {
	if m.flowBackend() == nil {
		return nil
	}
	return []mcp.Tool{m.toolFor(m.flowDef())}
}

func (m *Manager) flowDef() *toolDef {
	return &toolDef{
		name: "semar_run_flow", title: "Run a saved flow",
		desc: "Run a flow that is saved in the repository (for example login) on the device now, with no model call: the engine sends each step and waits for the screen. This is much faster than tapping the same steps one by one. It acts on the same device and keeps the app as it is by default. The refs of the last listing are no longer valid after it, so it ends with a new listing. While a recording goes on, the flow is run and recorded as one use step.",
		schema: mcp.Object(map[string]mcp.Schema{
			"id":         mcp.String("The ID of the flow, from semar_list."),
			"with":       mcp.NameValue("The params of the flow, as name and value pairs. At most 32, each value up to 1 KiB.").With("maxItems", maxFlowWith),
			"keepState":  mcp.Bool("Run on what the device shows now. True by default. False resets the app first, and is refused while recording.").With("default", true),
			"list":       mcp.Bool("End with the listing of the screen. True by default.").With("default", true),
			"timeoutSec": mcp.Int("How long the flow may run, in seconds.").With("minimum", 1).With("maximum", maxFlowSec).With("default", defaultFlowSec),
		}).Required("id"),
		run: m.runFlow,
	}
}

type flowArgs struct {
	ID         string        `json:"id"`
	With       mcp.StringMap `json:"with"`
	KeepState  *bool         `json:"keepState"`
	List       *bool         `json:"list"`
	TimeoutSec *int          `json:"timeoutSec"`
}

func (m *Manager) runFlow(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a flowArgs
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if err := flow.CheckID(a.ID); err != nil {
		return mcp.Result{}, err
	}
	if len(a.With) > maxFlowWith {
		return mcp.Result{}, fmt.Errorf("with has %d items: at most %d", len(a.With), maxFlowWith)
	}
	for _, k := range slices.Sorted(maps.Keys(a.With)) {
		if len(a.With[k]) > maxFlowWithValue {
			return mcp.Result{}, fmt.Errorf("the value of %s is longer than %d bytes", k, maxFlowWithValue)
		}
	}
	sec := defaultFlowSec
	if a.TimeoutSec != nil {
		sec = *a.TimeoutSec
	}
	if sec < 1 || sec > maxFlowSec {
		return mcp.Result{}, fmt.Errorf("timeoutSec is 1 to %d seconds", maxFlowSec)
	}
	keep := a.KeepState == nil || *a.KeepState
	list := a.List == nil || *a.List
	b := m.flowBackend()
	if b == nil && m.mode != modeRecord {
		return mcp.Result{}, errors.New("this server cannot run flows: it needs a repository and an environment")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(sec)*time.Second)
	defer cancel()
	with := map[string]string(a.With)
	if m.mode == modeRecord && m.rec != nil {
		return m.recRunFlow(ctx, a.ID, with, keep)
	}
	return m.liveRunFlow(ctx, b, FlowRequest{ID: a.ID, With: with, KeepState: keep}, list)
}

// recRunFlow runs the flow through the session of the recording, which keeps the state of the app and
// inserts the use step.
func (m *Manager) recRunFlow(ctx context.Context, id string, with map[string]string, keep bool) (mcp.Result, error) {
	if !keep {
		return mcp.Result{}, errors.New("keepState: false is refused while recording: a recording cannot reset the app half way")
	}
	r := m.rec
	before := len(r.sess.Steps())
	f, res, err := r.sess.UseFlow(ctx, id, with)
	out, err := m.afterRecord(r, before, f, err)
	if err != nil {
		return mcp.Result{}, err
	}
	m.noteUse(id, with)
	if len(res.Captured) > 0 {
		names := slices.Sorted(maps.Keys(res.Captured))
		out.Content = append([]mcp.Content{{Type: "text", Text: "captured (values stay in the recording): " + strings.Join(names, ", ")}}, out.Content...)
	}
	return out, nil
}

// liveRunFlow runs the flow on the live device, then forgets the refs and lists the screen.
func (m *Manager) liveRunFlow(ctx context.Context, b FlowBackend, req FlowRequest, list bool) (mcp.Result, error) {
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	out, runErr := b.Run(ctx, dev, req)
	// Whatever the flow did, the screen is not the one that the refs named.
	m.refs.Invalidate()
	if runErr != nil && out.Status == "" {
		return mcp.Result{}, fmt.Errorf("the flow %s could not run: %w", req.ID, runErr)
	}
	for k, v := range out.Captured {
		out.Captured[k] = truncateText(m.scrub.Text(v), maxCapturedShown)
	}
	out.Error = m.scrub.Text(out.Error)
	for i := range out.Steps {
		out.Steps[i].Note = m.scrub.Text(out.Steps[i].Note)
	}
	if runErr != nil && out.Error == "" {
		out.Error = m.scrub.Text(runErr.Error())
	}
	if out.Status == flow.StatusCompleted {
		m.noteUse(req.ID, req.With)
	}
	body, err := textJSON(out)
	if err != nil {
		return mcp.Result{}, err
	}
	// One block: the outcome as JSON on the first line, then the screen as the listing tool writes it.
	text := body.Content[0].Text
	if list {
		// A listing that fails is no reason to lose what the flow did.
		if l, lerr := m.list(ctx, dev); lerr == nil {
			text += "\n\nthe screen now:\n" + l.text(m)
		} else {
			text += "\n\nthe screen could not be listed: " + lerr.Error()
		}
	}
	res := mcp.TextResult(text)
	res.IsError = out.Status != flow.StatusCompleted
	return res, nil
}
