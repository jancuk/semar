package takeover

import (
	"context"
	"time"

	"github.com/jancuk/semar/internal/mcp"
)

// Middleware gates every tool call through the baton. Control tools (Surface "control") always run, so
// the agent can still ask for or wait on control. A write is refused with a structured human_in_control
// error while a person drives; reads keep working. The call feed gets the tool name, surface, outcome
// and duration, never the arguments.
func Middleware(b *Baton) mcp.Middleware {
	return func(t mcp.Tool, next mcp.Handler) mcp.Handler {
		if t.Surface == "control" {
			return next
		}
		// web_open and web_close stay Write=false so observe mode can read pages and close the
		// browser, but under human takeover they must be gated here before AutoLegMiddleware can
		// mutate an open journey draft or close a running recording.
		write := t.Write || t.Name == "web_open" || t.Name == "web_close"
		return func(ctx context.Context, call mcp.Call) (res mcp.Result, err error) {
			ctx2, leave, enterErr := b.Enter(ctx, Agent, write)
			if enterErr != nil {
				if he, ok := enterErr.(*ErrHumanInControl); ok {
					b.RecordEvent(Event{Who: Agent, Tool: t.Name, Surface: t.Surface, OK: false, Summary: "refused: human in control"})
					return HumanInControl(he), nil
				}
				return mcp.Result{}, enterErr
			}
			started := b.clock()
			ok := false
			defer func() {
				leave(ok, "")
				b.RecordEvent(Event{
					Who: Agent, Tool: t.Name, Surface: t.Surface, OK: ok,
					MS: b.clock().Sub(started).Milliseconds(),
				})
			}()
			res, err = next(ctx2, call)
			ok = err == nil && !res.IsError
			// A call that failed keeps the notice for the next one: the agent did not see this result's text.
			if err != nil || res.IsError {
				return res, err
			}
			if n := b.TakeNotice(); n != "" {
				res.Content = append([]mcp.Content{{Type: "text", Text: "Note: " + n + "."}}, res.Content...)
			}
			return res, err
		}
	}
}

// HumanInControl is the structured error result that an agent write gets while a person drives. A tool
// that gates itself with Baton.Enter, because its Write flag must stay false, answers with it.
func HumanInControl(e *ErrHumanInControl) mcp.Result {
	text := "A person is in control of the device, so this action was not run."
	if e.Reason != "" {
		text += " Reason: " + e.Reason + "."
	}
	text += " Reads still work. Call semar_wait_for_control to wait for the hand-back."
	return mcp.Result{
		Content: []mcp.Content{{Type: "text", Text: text}},
		IsError: true,
		Structured: map[string]any{
			"status": "human_in_control",
			"reason": e.Reason,
			"since":  e.Since.UTC().Format(time.RFC3339),
			"next":   "semar_wait_for_control",
		},
	}
}
