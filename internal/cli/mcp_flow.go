package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/mobilemcp"
)

func init() {
	registerMCPMobileExtra(func(w *mcpWiring) (mcpSurfaceResult, error) {
		// A flow is a file of the repository and runs on an app of the environment, so it needs both and an
		// Android service. Standalone mode has only the live tools.
		if !w.RepoMode || w.EnvName == "" || !hasAndroidService(w.Env) {
			return mcpSurfaceResult{}, nil
		}
		w.Mobile.SetFlowBackend(&mcpFlowBackend{w: w})
		return mcpSurfaceResult{Tools: w.Mobile.FlowTools()}, nil
	})
}

func hasAndroidService(env config.Environment) bool {
	return slices.ContainsFunc(env.Services, func(s config.Service) bool { return s.Android != nil })
}

// mcpFlowBackend is the mobilemcp.FlowBackend of the command. It runs a saved flow through the engine on
// the same android.Platform that the live tools hold, so that no second UiAutomator2 server starts and the
// lease on the device stays where it is. The actions are recorded in the run of the tool server.
type mcpFlowBackend struct {
	w *mcpWiring

	mu   sync.Mutex
	eng  *mobile.Engine
	plat *android.Platform // the platform that eng acts on
}

var _ mobilemcp.FlowBackend = (*mcpFlowBackend)(nil)

// engine gives the engine on the platform of the live device, and makes it the first time. It is made
// again when the live device was closed and opened anew, because that is another platform.
func (b *mcpFlowBackend) engine() (*mobile.Engine, domain.RunID, error) {
	w := b.w
	plat := w.MobilePlatform()
	if plat == nil {
		return nil, "", errors.New("the device is not open: use a mobile_* tool first, or end the recording")
	}
	id, rec, err := w.EnsureRun()
	if err != nil {
		return nil, "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.eng != nil && b.plat == plat {
		return b.eng, id, nil
	}
	services := map[string]mobile.Service{}
	for _, s := range w.Env.Services {
		if s.Android != nil {
			services[s.Name] = mobile.Service{Schemes: slices.Clone(s.Android.Schemes)}
		}
	}
	b.eng = mobile.New(mobile.Options{
		Policy:   w.Policy,
		Recorder: rec,
		Files:    w.RunFiles(),
		Secrets:  w.Secrets,
		Platform: plat,
		Services: services,
		Kind:     w.Env.Kind,
		Run:      id,
		// The engine of a tool server lives as long as the process, and every flow draws on its budget.
		Budget:          maxMobileBudget,
		CheckTimeout:    w.Env.Android.CheckTimeout(),
		Clock:           w.Opts.Clock,
		LogcatOnFailure: w.Env.Android.LogcatOnFailure,
		Sleep:           w.Opts.Sleep,
	})
	b.plat = plat
	return b.eng, id, nil
}

// before is the hook of the run that stops a flow at the boundary of a step: when a person asked for a
// pause or holds control, the next step does not start.
func (b *mcpFlowBackend) before(next func(context.Context, string) error) func(context.Context, string) error {
	return func(ctx context.Context, step string) error {
		if next != nil {
			if err := next(ctx, step); err != nil {
				return err
			}
		}
		if baton := b.w.Baton; baton != nil && baton.Pending() {
			return fmt.Errorf("paused by a person before step %s", step)
		}
		return nil
	}
}

func (b *mcpFlowBackend) Run(ctx context.Context, _ mobilemcp.Device, r mobilemcp.FlowRequest) (mobilemcp.FlowOutcome, error) {
	w := b.w
	loader := flow.Loader(w.Dir)
	f, digest, err := loader.Flow(r.ID)
	if err != nil {
		return mobilemcp.FlowOutcome{}, err
	}
	eng, runID, err := b.engine()
	if err != nil {
		return mobilemcp.FlowOutcome{}, err
	}
	scenario := domain.ScenarioID("mcp-flow-" + r.ID)
	res, err := flow.RunWith(ctx, eng, screenSource{repo: w.Dir}, f, r.With, map[string]string{"run.id": string(runID)}, flow.Options{
		KeepState: r.KeepState,
		By:        domain.ActorReplay,
		Use: func(step string) domain.UseCase {
			return domain.UseCase{Pass: domain.PassExplore, Scenario: scenario, Step: domain.StepID(step)}
		},
		Flows:  loader.Flow,
		Before: b.before(r.Before),
	})
	// A secret that the flow resolved is now known to the scrubber, so that nothing shows it.
	w.AddSecrets(w.Secrets.Loaded()...)
	if err != nil {
		return mobilemcp.FlowOutcome{}, err
	}
	out := mobilemcp.FlowOutcome{
		Status: res.Status, Flow: domain.FlowRef{ID: f.ID, Digest: digest}, Checked: res.Checked,
		Captured: res.Captured, Error: res.Error,
	}
	for _, s := range res.Steps {
		a := s.Action
		st := mobilemcp.FlowStep{Name: s.Name, Op: a.Op, OK: a.Err == "" && a.Refused == "", MS: a.DurationMS}
		switch {
		case a.Refused != "":
			st.Note = "refused: " + a.Refused
		case a.Err != "":
			st.Note = a.Err
		}
		out.Steps = append(out.Steps, st)
	}
	return out, nil
}
