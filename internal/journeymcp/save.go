package journeymcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/mcp"
)

func (m *Manager) saveDef() *toolDef {
	return &toolDef{
		name: "semar_journey_save", title: "Save the journey", write: true, openWorld: true,
		desc: "Close the open leg, write the journey, and replay it once with no model. The journey is kept only when that replay passes: on any other outcome the files that were written are removed, and you get the verdict that code decided. " +
			"The outputs of each leg are the names that its requests captured. The checks that you did not give are written as what the system answered, marked as written by a model, so a person can review them. Pass run: false to write without the replay.",
		schema: mcp.Object(map[string]mcp.Schema{
			"invariants": mcp.Array("Groups of values that must be equal across legs, such as [\"{{place.amount}}\", \"{{pay.amount}}\"]. Each group has 2 to 6 values.",
				mcp.Array("One group.", mcp.String("A value or a {{<leg>.<name>}} variable."))),
			"replace": mcp.Bool("Write over a saved journey of the same ID."),
			"run":     mcp.Bool("Replay the journey once and keep it only if it passes. True by default.").With("default", true),
		}),
		run: m.save,
	}
}

type saveArgs struct {
	Invariants [][]string `json:"invariants"`
	Replace    bool       `json:"replace"`
	Run        *bool      `json:"run"`
}

func (m *Manager) save(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a saveArgs
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	d, err := m.needDraft()
	if err != nil {
		return mcp.Result{}, err
	}
	if m.o.Backend == nil {
		return mcp.Result{}, errors.New("this server cannot write journeys: it needs a repository and an environment")
	}
	if len(a.Invariants) > MaxInvariants {
		return mcp.Result{}, fmt.Errorf("%d invariants: at most %d", len(a.Invariants), MaxInvariants)
	}
	for i, g := range a.Invariants {
		if len(g) < 2 || len(g) > MaxEqual {
			return mcp.Result{}, fmt.Errorf("invariants[%d] has %d values: 2 to %d", i, len(g), MaxEqual)
		}
	}
	replace := a.Replace || d.replace
	if !replace && m.o.Backend.Exists(d.id) {
		return mcp.Result{}, fmt.Errorf("the journey %s exists already: save with replace: true to write over it", d.id)
	}
	run := a.Run == nil || *a.Run
	progress(call, "closing the open leg")
	dropped, err := m.closeCurLocked(ctx, d)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("the open leg could not be closed, so nothing was written: %w", err)
	}
	if len(d.legs) == 0 {
		return mcp.Result{}, errors.New("the draft has no leg with a step: do something with the api_*, web_*, or mobile_* tools first")
	}
	j := assemble(d, d.legs, a.Invariants)
	p := pendingOf(d.legs)
	if ps := m.o.Backend.Validate(j, p); len(ps) > 0 {
		reopened := d.reopenLast()
		out := map[string]any{"saved": false, "problems": problemLines(ps), "note": "nothing was written"}
		if reopened {
			out["next"] = "the last leg is open again: undo or add steps, then save again"
		} else {
			out["next"] = "fix what the problems say with new steps or legs, or drop the draft with semar_journey_discard"
		}
		return m.refusal(out)
	}
	progress(call, "writing the journey")
	saved, err := m.o.Backend.Write(j, p, replace)
	if err != nil {
		d.reopenLast()
		return mcp.Result{}, fmt.Errorf("the journey was not written, and nothing was left: %w", err)
	}
	out := map[string]any{"journey": d.id, "files": saved.Files, "legs": len(j.Legs)}
	if dropped != "" {
		out["note"] = dropped
	}
	if !run {
		out["saved"], out["replayed"] = true, false
		out["next"] = "the journey was not replayed: run it with semar_journey_run before you trust it"
		m.d = nil
		return textJSON(out)
	}
	progress(call, "replaying the journey once, with no model")
	outcome, rerr := m.o.Backend.Run(ctx, d.id)
	if rerr != nil || outcome.Outcome != string(domain.JourneyPassed) {
		undone := true
		var uerr error
		if saved.Undo != nil {
			if uerr = saved.Undo(); uerr != nil {
				undone = false
			}
		}
		// The replay used the browser and the device of the tools, which it closed: a web or an android leg
		// has nothing left to go on with, so only an api leg is opened again.
		if n := len(d.legs); n > 0 && d.legs[n-1].leg.Surface == domain.SurfaceAPI {
			d.reopenLast()
		}
		res := map[string]any{"saved": false, "replayed": true}
		if rerr != nil {
			res["error"] = rerr.Error()
		} else {
			res["outcome"], res["firstFailing"], res["reasons"], res["legs"] = outcome.Outcome, outcome.FirstFailing, outcome.Reasons, outcome.Legs
			if outcome.Report != "" {
				res["report"] = outcome.Report
			}
		}
		if undone {
			res["note"] = "the replay did not pass, so the files that were written are removed: a wrong test costs more than none"
		} else {
			res["note"] = "the replay did not pass, and the files could not be removed: " + uerr.Error() + ". Remove " + strings.Join(saved.Files, ", ") + " by hand"
		}
		return m.refusal(res)
	}
	out["saved"], out["replayed"], out["outcome"] = true, true, outcome.Outcome
	if outcome.Report != "" {
		out["report"] = outcome.Report
	}
	m.d = nil
	return textJSON(out)
}

// refusal is a result that says that nothing was kept.
func (m *Manager) refusal(v any) (mcp.Result, error) {
	r, err := textJSON(v)
	r.IsError = true
	return r, err
}

// assemble makes the journey of the closed legs.
func assemble(d *draft, legs []*closedLeg, invariants [][]string) domain.Journey {
	j := domain.Journey{
		APIVersion: "semar/v1", Kind: "Journey", ID: d.id, Title: d.title, Owner: d.owner,
		Criticality: d.criticality, AuthProfile: d.authProfile,
	}
	for _, l := range legs {
		j.Legs = append(j.Legs, l.leg)
	}
	for _, g := range invariants {
		j.Invariants = append(j.Invariants, domain.Invariant{Equal: slices.Clone(g)})
	}
	return j
}

// pendingOf is what the legs would write besides the journey.
func pendingOf(legs []*closedLeg) Pending {
	var p Pending
	for _, l := range legs {
		p.Flows = append(p.Flows, l.flows...)
		for _, f := range l.files {
			i := slices.IndexFunc(p.Screens, func(s ScreensFile) bool { return s.App == f.App })
			if i < 0 {
				p.Screens = append(p.Screens, ScreensFile{App: f.App, Adds: slices.Clone(f.Adds)})
				continue
			}
			p.Screens[i].Adds = append(p.Screens[i].Adds, f.Adds...)
		}
	}
	return p
}

// ---- run ----

func (m *Manager) runDef() *toolDef {
	return &toolDef{
		name: "semar_journey_run", title: "Run a saved journey", write: true, openWorld: true,
		desc: "Replay a saved journey once, leg by leg, with no model call. The outcome, the first failing leg, and the reasons are decided by code. It is refused while the server only observes: a run against production stays a command line action.",
		schema: mcp.Object(map[string]mcp.Schema{
			"id": mcp.String("The ID of a saved journey."),
		}).Required("id"),
		run: m.runTool,
	}
}

func (m *Manager) runTool(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if err := idcheck.Check("journey ID", a.ID); err != nil {
		return mcp.Result{}, err
	}
	if m.o.Backend == nil {
		return mcp.Result{}, errors.New("this server cannot run journeys: it needs a repository and an environment")
	}
	if m.d != nil && m.d.cur != nil && m.d.cur.flowStarted {
		return mcp.Result{}, fmt.Errorf("the android leg %s is recording and holds the device: close the leg with semar_journey_add_leg or semar_journey_save first", m.d.cur.id)
	}
	id := domain.JourneyID(a.ID)
	if !m.o.Backend.Exists(id) {
		return mcp.Result{}, fmt.Errorf("the journey %s is not saved", a.ID)
	}
	progress(call, "replaying the journey with no model")
	out, err := m.o.Backend.Run(ctx, id)
	if err != nil {
		return mcp.Result{}, err
	}
	res := map[string]any{"journey": a.ID, "outcome": out.Outcome, "legs": out.Legs}
	if out.FirstFailing != "" {
		res["firstFailing"] = out.FirstFailing
	}
	if len(out.Reasons) > 0 {
		res["reasons"] = out.Reasons
	}
	if out.Report != "" {
		res["report"] = out.Report
	}
	r, err := textJSON(res)
	r.IsError = out.Outcome != string(domain.JourneyPassed)
	return r, err
}

// progress tells the client what the tool is doing. A call made without a transport has no way to.
func progress(call mcp.Call, msg string) {
	if call.Progress != nil {
		call.Progress(0, 0, msg)
	}
}
