package cli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mobilemcp"
	"github.com/jancuk/semar/internal/studio"
	"github.com/jancuk/semar/internal/takeover"
)

func init() { registerMCPStudio(buildMCPStudio) }

// handBackLimit bounds what runs when control returns to the agent: the refs are dropped and a recording
// reads the screen again.
const handBackLimit = 30 * time.Second

// buildMCPStudio starts what --studio asks for: the baton that every surface shares, the live studio on
// the loopback address, and the three control tools. The token of the studio is its own: it is made
// here, is never the token of the HTTP server of the tools, and goes to standard error with the address
// and nowhere else.
func buildMCPStudio(w *mcpWiring) (mcpStudioResult, error) {
	if w.Baton == nil || w.Mobile == nil {
		return mcpStudioResult{}, errors.New("the studio needs the baton and the mobile tools")
	}
	baton := w.Baton
	host := &liveHost{
		w: w, b: baton, m: w.Mobile, mobile: slices.Contains(w.Surfaces, "mobile"), humanSteps: map[string]bool{},
	}

	baton.SetMode(mobilemcp.ModeLive)
	// Every tool goes through the baton: a person who holds control keeps the agent's writes out. The
	// baton goes first, so it runs outside every surface middleware (the journey's AutoLegMiddleware
	// opens a leg and starts the app before its handler runs): audit, guard, baton, surfaces, handler.
	w.ExtraMiddleware = append([]mcp.Middleware{takeover.Middleware(baton)}, w.ExtraMiddleware...)
	// When control returns, what the agent knew about the screen is stale.
	baton.OnHandBack(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handBackLimit)
		defer cancel()
		if err := w.Mobile.OnHandBack(ctx); err != nil {
			fmt.Fprintf(w.Stderr, "semar mcp: the screen could not be read after the hand-back: %s\n", w.Scrubber.Text(err.Error()))
		}
		if wm, ok := mcpWebManager(w); ok {
			wm.InvalidateList()
		}
	})

	srv, err := newStudioServer(w, host)
	if err != nil {
		return mcpStudioResult{}, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(w.Ctx, "tcp", w.StudioAddr)
	if err != nil {
		return mcpStudioResult{}, fmt.Errorf("listen on %s: %w", w.StudioAddr, err)
	}
	// The scrubber knows the token, so that no tool result ever shows it.
	w.AddSecrets(srv.Token())
	sctx, cancel := context.WithCancel(w.Ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(sctx, ln); err != nil && sctx.Err() == nil {
			fmt.Fprintf(w.Stderr, "semar mcp: the studio stopped: %s\n", w.Scrubber.Text(err.Error()))
		}
	}()
	addr := fmt.Sprintf("http://%s/#token=%s", ln.Addr(), srv.Token())
	return mcpStudioResult{
		Tools: w.Mobile.ControlTools(baton),
		Addr:  addr,
		Close: func(ctx context.Context) error {
			cancel()
			select {
			case <-done:
			case <-ctx.Done():
			}
			return nil
		},
	}, nil
}

// newStudioServer makes the live studio. Its token is made by the studio from the same source as the token
// of the HTTP server, and is made again in the one case that they could be the same: a person set the token
// of the HTTP server by hand.
func newStudioServer(w *mcpWiring, host studio.LiveHost) (*studio.Server, error) {
	fixed, _ := w.LookupEnv(mcpTokenEnv)
	for range 4 {
		srv, err := studio.New(studio.Deps{Live: host})
		if err != nil {
			return nil, err
		}
		if fixed == "" || srv.Token() != fixed {
			return srv, nil
		}
	}
	return nil, errors.New("the studio could not make a token of its own")
}

// liveHost is the studio's way to the baton and the device. It implements studio.LiveHost.
type liveHost struct {
	w      *mcpWiring
	b      *takeover.Baton
	m      *mobilemcp.Manager
	mobile bool

	mu         sync.Mutex // a leaf: it is never held while the baton, the Manager, or a session is
	view       mobilemcp.LiveView
	seen       uint64
	have       bool
	seenAt     time.Time      // when the cached view was read
	seenOwner  takeover.Owner // who held the baton at that reading
	humanSteps map[string]bool
}

var _ studio.LiveHost = (*liveHost)(nil)

// peek reads the baton without the heartbeat check, which may hand control back and so run the hand-back
// callbacks, and those take the lock of the Manager. Code that holds that lock uses peek.
func (h *liveHost) peek(ctx context.Context) takeover.State {
	st, _ := h.b.Watch(ctx, math.MaxUint64, 0)
	return st
}

func (h *liveHost) scrub(s string) string { return h.w.Scrubber.Text(s) }

func (h *liveHost) State(ctx context.Context, since uint64, wait time.Duration) (studio.LiveState, error) {
	_, evs := h.b.Watch(ctx, since, wait)
	// State also checks the heartbeat of a person who left, outside every lock.
	st := h.b.State()
	h.read(ctx, st, false)
	return h.compose(st, evs), nil
}

// liveMaxAge is how old the cached view may be while the agent drives: every call of the agent moves the
// version, and each reading is a dump of the UI tree, so a burst of calls is not allowed a dump apiece.
const liveMaxAge = 500 * time.Millisecond

func (h *liveHost) now() time.Time {
	if h.w != nil && h.w.Opts.Clock != nil {
		return h.w.Opts.Clock()
	}
	return time.Now()
}

// read takes a new reading of the screen when the version moved, or force asks. While the agent drives,
// a reading younger than liveMaxAge is kept, unless the owner changed since (a hand-back invalidates it).
// A reading that finds the agent busy keeps the last one, so that the page does not blink.
func (h *liveHost) read(ctx context.Context, st takeover.State, force bool) {
	h.mu.Lock()
	need := force || !h.have || h.seen != st.Version
	if need && !force && h.have && st.Owner == takeover.Agent && h.seenOwner == takeover.Agent && h.now().Sub(h.seenAt) < liveMaxAge {
		need = false
	}
	h.mu.Unlock()
	if !need {
		return
	}
	if !h.mobile {
		h.mu.Lock()
		h.view, h.have, h.seen = mobilemcp.LiveView{Mode: mobilemcp.ModeLive, NoDevice: true, Windows: []mobilemcp.LiveWindow{}, Steps: []mobilemcp.LiveStep{}}, true, st.Version
		h.seenAt, h.seenOwner = h.now(), st.Owner
		h.mu.Unlock()
		return
	}
	h.mu.Lock()
	observe := h.view.Observe
	h.mu.Unlock()
	refresh := st.Owner == takeover.Human && !st.Pending && !observe
	v, err := h.m.LiveSnapshot(ctx, refresh)
	h.mu.Lock()
	switch {
	case err != nil:
		// The device could not be read: show what was seen last, and try again at the next change.
		if h.have {
			h.view.Busy = false
		}
	case v.Busy && h.have:
		h.view.Busy = true
	default:
		h.view, h.have, h.seen = v, true, st.Version
		h.seenAt, h.seenOwner = h.now(), st.Owner
	}
	// Everything of h.view that is used below is copied while the lock is held.
	mode, gap := h.view.Mode, h.view.Gap
	h.mu.Unlock()
	h.b.SetMode(mode)
	if mode == mobilemcp.ModeRecord || gap {
		h.b.SetGap(gap)
	}
}

// compose builds what the page draws, with every text scrubbed.
func (h *liveHost) compose(st takeover.State, evs []takeover.Event) studio.LiveState {
	h.mu.Lock()
	v := h.view
	steps := make([]studio.LiveStep, 0, len(v.Steps))
	for _, s := range v.Steps {
		who := ""
		if h.humanSteps[s.ID] {
			who = "human"
		}
		steps = append(steps, studio.LiveStep{N: s.N, ID: s.ID, Kind: s.Kind, Text: h.scrub(s.Text), Who: who, Pending: s.Pending})
	}
	h.mu.Unlock()
	out := studio.LiveState{
		Version: st.Version, Owner: st.Owner.String(), Pending: st.Pending, Asked: h.scrub(st.Asked), Reason: h.scrub(st.Reason),
		Since: st.Since, Mode: nonEmptyStr(v.Mode, mobilemcp.ModeLive), RecordHuman: st.RecordHuman, Gap: v.Gap, GapAt: v.GapAt,
		Observe: v.Observe, ObserveReason: v.ObserveReason, Width: v.Width, Height: v.Height, Secret: v.Secret,
		NoDevice: v.NoDevice, Busy: v.Busy, Steps: steps, Events: []studio.LiveEvent{},
	}
	if st.Owner == takeover.Agent {
		out.Note = h.scrub(st.Note)
	}
	for _, w := range v.Windows {
		lw := studio.LiveWindow{Kind: w.Kind, Package: h.scrub(w.Package), Elements: make([]studio.LiveElement, 0, len(w.Elements))}
		for _, e := range w.Elements {
			lw.Elements = append(lw.Elements, studio.LiveElement(e))
		}
		out.Windows = append(out.Windows, lw)
	}
	if out.Windows == nil {
		out.Windows = []studio.LiveWindow{}
	}
	for _, e := range evs {
		out.Events = append(out.Events, studio.LiveEvent{
			Seq: e.Seq, At: e.At, Who: e.Who.String(), Tool: h.scrub(e.Tool), Surface: e.Surface, OK: e.OK, MS: e.MS, Summary: h.scrub(e.Summary),
		})
	}
	return out
}

func nonEmptyStr(s, other string) string {
	if s == "" {
		return other
	}
	return s
}

func (h *liveHost) Screen(ctx context.Context) ([]byte, error) {
	if !h.mobile {
		return nil, &studio.LiveError{Status: 404, Msg: "this server serves no device, so there is no screen to show"}
	}
	data, err := h.m.LiveScreen(ctx)
	switch {
	case err == nil:
		return data, nil
	case errors.Is(err, mobilemcp.ErrSecretScreen):
		return nil, &studio.LiveError{Status: 403, Msg: err.Error()}
	case errors.Is(err, mobilemcp.ErrBusy):
		return nil, &studio.LiveError{Status: 503, Msg: err.Error()}
	}
	return nil, &studio.LiveError{Status: 503, Msg: h.scrub(oneLine(err.Error()))}
}

func (h *liveHost) Pause(ctx context.Context, reason string) error {
	err := h.b.Pause(ctx, h.scrub(reason), takeover.DefaultGrace)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, takeover.ErrPauseAborted):
		return &studio.LiveError{Status: 409, Msg: err.Error()}
	}
	return err
}

func (h *liveHost) Resume(_ context.Context, note string) error {
	h.b.Resume(h.scrub(note))
	return nil
}

func (h *liveHost) SetRecordHuman(on bool) error {
	h.b.SetRecordHuman(on)
	return nil
}

// Act does one thing by hand. The baton says whether the person holds control, and the Manager asks it
// again under its own lock, so that a hand-back that came in between stops the action.
func (h *liveHost) Act(ctx context.Context, a studio.LiveAct) (studio.LiveState, error) {
	if !h.mobile {
		return studio.LiveState{}, &studio.LiveError{Status: 400, Msg: "this server serves no device: act in the browser window instead"}
	}
	ctx2, leave, err := h.b.Enter(ctx, takeover.Human, true)
	if err != nil {
		if errors.Is(err, takeover.ErrAgentInControl) {
			return studio.LiveState{}, studio.ErrAgentOwns
		}
		return studio.LiveState{}, err
	}
	defer leave(true, "")
	req, err := h.request(a)
	if err != nil {
		return studio.LiveState{}, err
	}
	req.Still = func() error {
		if h.peek(ctx2).Owner != takeover.Human {
			return studio.ErrAgentOwns
		}
		return nil
	}
	h.mu.Lock()
	before := map[string]bool{}
	for _, s := range h.view.Steps {
		before[s.ID] = true
	}
	h.mu.Unlock()

	res, err := h.m.HumanAct(ctx2, req, h.b.State().RecordHuman)
	summary := res.Summary
	if err != nil || summary == "" {
		summary = humanSummary(a) + " failed"
	}
	h.b.RecordEvent(takeover.Event{Who: takeover.Human, Tool: "human " + strings.ToLower(a.Do), Surface: "mobile", OK: err == nil, Summary: summary})
	if err == nil || res.Gap {
		h.b.SetGap(res.Gap)
	}
	if err != nil {
		if errors.Is(err, mobilemcp.ErrDeviceBusy) {
			return studio.LiveState{}, &studio.LiveError{Status: 503, Msg: err.Error()}
		}
		var refused *mobilemcp.ErrHumanRefused
		if errors.As(err, &refused) {
			return studio.LiveState{}, &studio.LiveError{Status: 403, Msg: refused.Reason}
		}
		var le *studio.LiveError
		if errors.As(err, &le) {
			return studio.LiveState{}, err
		}
		return studio.LiveState{}, errors.New(h.scrub(oneLine(err.Error())))
	}
	st := h.b.State()
	h.read(ctx, st, true)
	if res.Recorded {
		h.mu.Lock()
		for _, s := range h.view.Steps {
			if !before[s.ID] {
				h.humanSteps[s.ID] = true
			}
		}
		h.mu.Unlock()
	}
	return h.State(ctx, 0, 0)
}

// humanSummary is what the feed says about an action that failed: the kind, and for a text only how long.
func humanSummary(a studio.LiveAct) string {
	do := strings.ToLower(strings.TrimSpace(a.Do))
	switch {
	case do == "type" && a.Text != nil:
		return fmt.Sprintf("[typed %d chars]", len([]rune(*a.Text)))
	case do == "type":
		return "[typed from a secret]"
	}
	return do
}

// request turns what the page sent into what the Manager does. A ref names an element of the last state.
func (h *liveHost) request(a studio.LiveAct) (mobilemcp.HumanActRequest, error) {
	req := mobilemcp.HumanActRequest{
		Do: a.Do, X: a.X, Y: a.Y, X2: a.X2, Y2: a.Y2, Direction: a.Direction, Hold: a.Hold, Text: a.Text,
		SecretEnv: a.SecretEnv, Replace: a.Replace, Submit: a.Submit, Key: a.Key,
	}
	if ref := strings.TrimSpace(a.Ref); ref != "" {
		n, err := mobilemcp.ParseRef(ref)
		if err != nil {
			return req, err
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, w := range h.view.Windows {
			for _, e := range w.Elements {
				if e.Index == n-1 {
					x, y := float64((e.Left+e.Right)/2), float64((e.Top+e.Bottom)/2)
					req.X, req.Y = &x, &y
					return req, nil
				}
			}
		}
		return req, fmt.Errorf("there is no element %s on the last screen: look at it again", ref)
	}
	return req, nil
}
