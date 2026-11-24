package mobilemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
)

const (
	maxBatchSteps = 50
	maxDelay      = 10000
	delayStep     = "delay"
	batchName     = "mobile_batch_commands"
)

func (m *Manager) batchTools() []*toolDef {
	return []*toolDef{{
		name: batchName, title: "Run several commands",
		desc: "Run mobile tools one after another in a single call, 1 to 50 steps. Each step is {name, arguments} with the name of a mobile_* tool and its arguments. The extra step name delay takes {milliseconds} of 1 to 10000. A batch cannot hold a batch. With stopOnError (the default) it stops at the first step that fails. listElementsAtEnd adds the element list of the screen at the end. The text of the steps comes first in order, then the images.",
		schema: mcp.Object(map[string]mcp.Schema{
			"steps": mcp.Array("The steps to run in order.", mcp.Object(map[string]mcp.Schema{
				"name":      mcp.String("The tool to run, such as mobile_click_on_screen_at_coordinates, or delay."),
				"arguments": mcp.Object(nil).With("description", "The arguments of that tool."),
			}).Required("name")).With("minItems", 1).With("maxItems", maxBatchSteps),
			"stopOnError":       mcp.Bool("Stop at the first step that fails. True by default."),
			"listElementsAtEnd": mcp.Bool("List the elements on the screen after the last step."),
		}).Required("steps"),
		run: m.batch,
	}}
}

type batchStep struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// runStep runs one step with the lock held: the delay, or a tool through the guard.
func (m *Manager) runStep(ctx context.Context, s batchStep) (mcp.Result, error) {
	name := strings.TrimSpace(s.Name)
	switch {
	case name == "":
		return mcp.Result{}, errors.New("a step needs a name")
	case name == delayStep:
		var a struct {
			Milliseconds int `json:"milliseconds"`
		}
		if err := mcp.DecodeArgs(s.Arguments, &a); err != nil {
			return mcp.Result{}, err
		}
		if a.Milliseconds < 1 || a.Milliseconds > maxDelay {
			return mcp.Result{}, fmt.Errorf("a delay is 1 to %d milliseconds", maxDelay)
		}
		timer := time.NewTimer(time.Duration(a.Milliseconds) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return mcp.Result{}, ctx.Err()
		}
		return mcp.TextResult(fmt.Sprintf("waited %d ms", a.Milliseconds)), nil
	case name == batchName:
		return mcp.Result{}, errors.New("a batch cannot hold a batch")
	case strings.HasPrefix(name, "semar_"):
		return mcp.Result{}, errors.New("a batch runs mobile tools only, not " + truncateText(name, 40))
	}
	d, ok := m.byName[name]
	if !ok {
		return mcp.Result{}, fmt.Errorf("the tool %q is not a mobile tool", truncateText(name, 60))
	}
	if err := m.guard.Allow(mcpkit.SurfaceMobile, !d.readOnly); err != nil {
		return mcp.Result{}, err
	}
	return m.run(ctx, d, mcp.Call{Name: name, Args: s.Arguments, Progress: func(float64, float64, string) {}})
}

func (m *Manager) batch(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Steps             []batchStep `json:"steps"`
		StopOnError       *bool       `json:"stopOnError"`
		ListElementsAtEnd bool        `json:"listElementsAtEnd"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if len(a.Steps) < 1 || len(a.Steps) > maxBatchSteps {
		return mcp.Result{}, fmt.Errorf("a batch has 1 to %d steps, not %d", maxBatchSteps, len(a.Steps))
	}
	// A batch that cannot start is refused whole, before any step runs: a nested batch or a name that
	// is not a mobile tool is a mistake of the caller, not a result of the device.
	for i, s := range a.Steps {
		name := strings.TrimSpace(s.Name)
		if name == batchName {
			return mcp.Result{}, fmt.Errorf("step %d: a batch cannot hold a batch", i+1)
		}
		if strings.HasPrefix(name, "semar_") {
			return mcp.Result{}, fmt.Errorf("step %d: a batch runs mobile tools only, not %s", i+1, truncateText(name, 40))
		}
	}
	stop := a.StopOnError == nil || *a.StopOnError
	var texts []string
	var images []mcp.Content
	failed := false
	for i, s := range a.Steps {
		if err := ctx.Err(); err != nil {
			return mcp.Result{}, err
		}
		if call.Progress != nil {
			call.Progress(float64(i), float64(len(a.Steps)), strings.TrimSpace(s.Name))
		}
		res, err := m.runStep(ctx, s)
		label := fmt.Sprintf("step %d %s", i+1, strings.TrimSpace(s.Name))
		if err == nil && res.IsError {
			err = errors.New(joinText(res.Content))
		}
		if err != nil {
			if ctx.Err() != nil {
				return mcp.Result{}, ctx.Err()
			}
			failed = true
			texts = append(texts, label+" failed: "+m.scrub.Text(err.Error()))
			if stop {
				texts = append(texts, fmt.Sprintf("stopped: %d steps were not run", len(a.Steps)-i-1))
				break
			}
			continue
		}
		body := joinText(res.Content)
		for _, c := range res.Content {
			if c.Type == "image" {
				images = append(images, c)
				body = strings.TrimSpace(body + " [image " + fmt.Sprint(len(images)) + " below]")
			}
		}
		texts = append(texts, label+": "+body)
	}
	if a.ListElementsAtEnd {
		var err error
		if m.mode == modeRecord {
			var text string
			if text, err = m.recFrameText(ctx); err == nil {
				texts = append(texts, "elements on the screen:\n"+text)
			}
		} else {
			var dev Device
			if dev, err = m.device(ctx); err == nil {
				var l *listing
				if l, err = m.list(ctx, dev); err == nil {
					texts = append(texts, "elements on the screen:\n"+l.text(m))
				}
			}
		}
		if err != nil {
			failed = true
			texts = append(texts, "the elements could not be listed: "+m.scrub.Text(err.Error()))
		}
	}
	out := mcp.Result{IsError: failed}
	for _, t := range texts {
		out.Content = append(out.Content, mcp.Content{Type: "text", Text: t})
	}
	out.Content = append(out.Content, images...)
	return out, nil
}

// joinText is the text blocks of a result, one per line.
func joinText(cs []mcp.Content) string {
	var parts []string
	for _, c := range cs {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}
