package mobilemcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/takeover"
)

// The control tools exist only when the server runs with --studio. They are how the agent asks a person
// for help and waits for the baton to come back. They have the surface "control", so the baton middleware
// lets them through while a person drives, and none of them changes the device.

const (
	maxReasonRunes     = 200
	defaultControlWait = 300
	maxControlWait     = 1800
)

// progressEvery is how often a blocked control tool tells a stdio client that it is still waiting.
var progressEvery = 5 * time.Second

var timeoutSchema = mcp.Int("How many seconds to wait for the person. 300 by default, at most 1800.").With("minimum", 1).With("maximum", maxControlWait)

// ControlTools gives semar_control_status, semar_request_human, and semar_wait_for_control. The caller adds
// them only when a studio is served and the baton is shared by every surface.
func (m *Manager) ControlTools(b *takeover.Baton) []mcp.Tool {
	ro := mcp.Annotations{ReadOnly: mcp.BoolPtr(true), OpenWorld: mcp.BoolPtr(false)}
	tool := func(name, title, desc string, schema mcp.Schema, h mcp.Handler) mcp.Tool {
		a := ro
		a.Title = title
		return mcp.Tool{Name: name, Title: title, Description: desc, Input: schema, Annotations: a, Surface: string(mcpkit.SurfaceControl), Write: false, Handler: h}
	}
	return []mcp.Tool{
		tool("semar_control_status", "Who has control",
			"Say who holds control of the device now (the agent or a person), whether a person asked for or was asked for control, why, since when, whether the tools act live or through a recording, whether the person's actions are recorded, and whether steps done by hand are missing from the recording.",
			mcp.Object(nil),
			func(ctx context.Context, call mcp.Call) (mcp.Result, error) {
				if err := noArgs(call); err != nil {
					return mcp.Result{}, err
				}
				return m.controlStatus(ctx, b)
			}),
		tool("semar_request_human", "Ask a person to take over",
			"Hand control of the device to the person at the studio and wait until they hand it back. Use it when you cannot go on yourself: a captcha, a one-time code, a permission dialog, a login that needs a person. Say what they should do in reason. It waits up to timeoutSec seconds, and gives back the person's note and a fresh listing of the screen. While you wait, other tools that change something are refused.",
			mcp.Object(map[string]mcp.Schema{
				"reason":     mcp.String("What the person should do, in at most 200 characters.").With("maxLength", maxReasonRunes),
				"timeoutSec": timeoutSchema,
			}).Required("reason"),
			func(ctx context.Context, call mcp.Call) (mcp.Result, error) { return m.requestHuman(ctx, b, call) }),
		tool("semar_wait_for_control", "Wait for control to come back",
			"Wait until a person hands control back to you, when a tool answered human_in_control. It gives back the person's note and a fresh listing of the screen. It returns at once when you already have control.",
			mcp.Object(map[string]mcp.Schema{"timeoutSec": timeoutSchema}),
			func(ctx context.Context, call mcp.Call) (mcp.Result, error) { return m.waitForControl(ctx, b, call) }),
	}
}

// controlState is the state of the baton as the tools say it. Every text that a person wrote is scrubbed.
func (m *Manager) controlState(ctx context.Context, b *takeover.Baton, st takeover.State) map[string]any {
	mode, gap, at, busy := m.statusBrief(ctx)
	if !busy {
		b.SetGap(gap)
	}
	out := map[string]any{
		"owner":       st.Owner.String(),
		"pending":     st.Pending,
		"reason":      m.scrub.Text(st.Reason),
		"since":       st.Since.UTC().Format(time.RFC3339),
		"mode":        mode,
		"recordHuman": st.RecordHuman,
		"gap":         gap,
	}
	if busy {
		out["busy"] = true // a call of the agent holds the device: the mode and the gap are not known now
	}
	if gap {
		out["gapAt"] = at
	}
	if st.Note != "" && st.Owner == takeover.Agent {
		out["note"] = m.scrub.Text(st.Note)
	}
	return out
}

func (m *Manager) controlStatus(ctx context.Context, b *takeover.Baton) (mcp.Result, error) {
	return textJSON(m.controlState(ctx, b, b.State()))
}

type waitArgs struct {
	Reason     string `json:"reason"`
	TimeoutSec int    `json:"timeoutSec"`
}

func waitTimeout(sec int) (time.Duration, error) {
	if sec == 0 {
		sec = defaultControlWait
	}
	if sec < 1 || sec > maxControlWait {
		return 0, fmt.Errorf("timeoutSec is 1 to %d", maxControlWait)
	}
	return time.Duration(sec) * time.Second, nil
}

func (m *Manager) requestHuman(ctx context.Context, b *takeover.Baton, call mcp.Call) (mcp.Result, error) {
	var a waitArgs
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	reason := strings.TrimSpace(a.Reason)
	if reason == "" {
		return mcp.Result{}, errors.New("reason is needed: say what the person should do")
	}
	if n := len([]rune(reason)); n > maxReasonRunes {
		return mcp.Result{}, fmt.Errorf("reason is %d characters: keep it to %d", n, maxReasonRunes)
	}
	timeout, err := waitTimeout(a.TimeoutSec)
	if err != nil {
		return mcp.Result{}, err
	}
	reason = m.scrub.Text(reason)
	if b.State().Owner != takeover.Human {
		b.Yield(reason)
	}
	b.RecordEvent(takeover.Event{Who: takeover.Agent, Tool: call.Name, Surface: string(mcpkit.SurfaceControl), OK: true, Summary: "asked a person for help: " + reason})
	return m.awaitAgent(ctx, b, call, timeout)
}

func (m *Manager) waitForControl(ctx context.Context, b *takeover.Baton, call mcp.Call) (mcp.Result, error) {
	var a waitArgs
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	timeout, err := waitTimeout(a.TimeoutSec)
	if err != nil {
		return mcp.Result{}, err
	}
	return m.awaitAgent(ctx, b, call, timeout)
}

// awaitAgent blocks until the agent holds control again, the timeout passes, or the call is cancelled. It
// sends a progress note every few seconds, which a stdio client sees as the call being alive.
func (m *Manager) awaitAgent(ctx context.Context, b *takeover.Baton, call mcp.Call, timeout time.Duration) (mcp.Result, error) {
	started := time.Now()
	deadline := started.Add(timeout)
	var st takeover.State
	for {
		wait := min(progressEvery, time.Until(deadline))
		if wait <= 0 {
			break
		}
		step, cancel := context.WithTimeout(ctx, wait)
		var err error
		st, err = b.WaitAgent(step)
		cancel()
		if err == nil {
			return m.handedBack(ctx, b, st)
		}
		if ctx.Err() != nil {
			return mcp.Result{}, ctx.Err()
		}
		if call.Progress != nil {
			call.Progress(time.Since(started).Seconds(), timeout.Seconds(), "waiting for the person to hand control back")
		}
	}
	st = b.State()
	if st.Owner == takeover.Agent {
		return m.handedBack(ctx, b, st)
	}
	out := m.controlState(ctx, b, st)
	out["status"] = "waiting"
	out["next"] = "semar_wait_for_control"
	out["message"] = fmt.Sprintf("the person has not handed control back after %d seconds: call semar_wait_for_control to wait longer", int(timeout.Seconds()))
	return textJSON(out)
}

// handedBack is the result once the agent holds control: the person's note, the notice that the next call
// would have got, and a fresh listing, whose refs are valid.
func (m *Manager) handedBack(ctx context.Context, b *takeover.Baton, st takeover.State) (mcp.Result, error) {
	// The control tools do not pass through the baton middleware, so the notice is taken here, once, and not
	// repeated on the next call.
	notice := b.TakeNotice()
	out := m.controlState(ctx, b, st)
	out["status"] = "control_returned"
	if notice != "" {
		out["notice"] = m.scrub.Text(notice)
	}
	res, err := textJSON(out)
	if err != nil {
		return res, err
	}
	res.Content = append(res.Content, mcp.Content{Type: "text", Text: m.FreshListing(ctx)})
	return res, nil
}
