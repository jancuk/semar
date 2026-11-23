package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/studio"
)

// recordStudio serves the recorder studio on the loopback address until Ctrl+C. The studio takes the same
// session, and the same way to open a device, as the terminal.
func recordStudio(ctx context.Context, cmd *cobra.Command, o options, f recordTestFlags, envArg string) error {
	repo, err := filepath.Abs(f.dir)
	if err != nil {
		return err
	}
	f.dir = repo
	b := &studioBackend{o: o, f: f, envArg: envArg}
	srv, err := studio.New(studio.Deps{
		Repo: repo, Status: b.status, NewSession: b.newSession, Verify: b.verify,
		Inspector: &lazyInspector{o: o, repo: repo}, TicketOf: groupOf,
	})
	if err != nil {
		return err
	}
	// A kill or a closed terminal stops the studio as Ctrl+C does: the device is given away, and the
	// secrets of the session are dropped, on every way out.
	ctx, stop := signal.NotifyContext(ctx, StopSignals()...)
	defer stop()
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.port)))
	if err != nil {
		return fmt.Errorf("listen on 127.0.0.1 port %d: %w", f.port, err)
	}
	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
	address := fmt.Sprintf("http://%s/#token=%s", ln.Addr(), srv.Token())
	fmt.Fprintln(out, address)
	fmt.Fprintln(errw, "semar: the recorder studio runs until you press Ctrl+C. Anyone who has the address above can act on your device.")
	if !f.noOpen {
		if err := o.OpenBrowser(address); err != nil {
			fmt.Fprintf(errw, "semar: no browser was opened (%v); open the address above yourself\n", err)
		}
	}
	return srv.Serve(ctx, ln)
}

// lazyInspector reads the devices with adb when the studio asks, so that a repository with no
// configuration, and a machine with no adb until later, still get a studio.
type lazyInspector struct {
	o    options
	repo string
}

func (l *lazyInspector) inspector() (mobile.AppInspector, error) {
	bin, err := android.FindADB(l.o.LookupEnv, l.repo)
	if err != nil {
		return nil, err
	}
	return android.NewInspector(bin, nil), nil
}

func (l *lazyInspector) ListDevices(ctx context.Context) ([]mobile.DeviceInfo, error) {
	i, err := l.inspector()
	if err != nil {
		return nil, err
	}
	return i.ListDevices(ctx)
}

func (l *lazyInspector) InspectForeground(ctx context.Context, serial string) (mobile.AppInfo, error) {
	i, err := l.inspector()
	if err != nil {
		return mobile.AppInfo{}, err
	}
	return i.InspectForeground(ctx, serial)
}

// studioBackend is what the studio asks of the command: the repository status, a new session, and a
// verify. It reads the configuration each time, so that the guided setup is seen at once.
type studioBackend struct {
	o      options
	f      recordTestFlags
	envArg string

	mu  sync.Mutex
	cur *studioCurrent
}

// studioCurrent is what the verify of the latest session needs.
type studioCurrent struct {
	envName string
	app     string
	open    mobilerec.PlatformFactory
}

type studioSetup struct {
	cfg     config.Config
	envName string
	env     config.Environment
	svc     config.Service
	names   []string
}

// load reads the configuration and chooses the environment and the app. The error is in plain words.
func (b *studioBackend) load(service string) (studioSetup, error) {
	var s studioSetup
	cfg, err := config.Load(b.f.dir)
	if err != nil {
		return s, err
	}
	envName, env, err := cfg.Environment(b.envArg)
	if err != nil {
		return s, err
	}
	switch {
	case env.Kind == config.KindProduction:
		return s, fmt.Errorf("the environment %q is a production environment, and a recording taps and types, so semar record does not run there", envName)
	case env.Effects.Mode == "read_only":
		return s, fmt.Errorf("the environment %q is read only, and a recording taps and types, so it needs an environment that allows test data", envName)
	}
	env = withServiceAuth(cfg, env)
	for _, sv := range env.Services {
		if sv.Android != nil {
			s.names = append(s.names, sv.Name)
		}
	}
	s.cfg, s.envName, s.env = cfg, envName, env
	if service == "" {
		service = b.f.service
	}
	svc, err := recordAndroidService(env, service)
	if err != nil {
		return s, err
	}
	s.svc = svc
	return s, nil
}

func (b *studioBackend) app(svc config.Service) string {
	if b.f.app != "" {
		return b.f.app
	}
	return svc.Name
}

func (b *studioBackend) status() (studio.Status, error) {
	s, err := b.load("")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return studio.Status{}, nil
	case err != nil && len(s.names) > 1 && b.f.service == "":
		// Several apps exist: the studio lets the person pick one instead of refusing to start.
		return studio.Status{HasConfig: true, Environment: s.envName, Services: s.names}, nil
	case err != nil:
		return studio.Status{HasConfig: true, Environment: s.envName, Services: s.names, Problem: oneLine(terminalSafe(err.Error()))}, nil
	}
	return studio.Status{HasConfig: true, Environment: s.envName, Service: s.svc.Name, App: b.app(s.svc), Services: s.names}, nil
}

func (b *studioBackend) newSession(req studio.SessionRequest) (*mobilerec.Session, error) {
	s, err := b.load(req.Service)
	if err != nil {
		return nil, errors.New(oneLine(terminalSafe(err.Error())))
	}
	app := b.app(s.svc)
	if err := idcheck.Check("app name", app); err != nil {
		return nil, err
	}
	if req.Use != "" {
		if _, err := flow.Load(b.f.dir, req.Use); err != nil {
			return nil, fmt.Errorf("the flow %s: %s", req.Use, oneLine(terminalSafe(err.Error())))
		}
	}
	overlay := mobilerec.NewOverlay(b.o.LookupEnv)
	if req.Serial != "" {
		overlay.Pin(mobilerec.SerialEnv, req.Serial)
	}
	open := recordingFactory(b.o, b.f.dir, s.cfg, s.envName, s.env)
	sess, err := mobilerec.New(mobilerec.Options{
		Repo: b.f.dir, Kind: req.Kind, ID: req.ID, Ticket: req.Ticket, Title: req.Title, Service: s.svc.Name, App: app,
		Open: open, Lookup: b.o.LookupEnv, Overlay: overlay, Reset: s.svc.Android.ResetMode(), Use: req.Use, UseWith: req.With, ConfirmClear: req.ConfirmClear,
		Clock: b.o.Clock, Sleep: b.o.Sleep, Now: b.o.Clock,
	})
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.cur = &studioCurrent{envName: s.envName, app: app, open: open}
	b.mu.Unlock()
	return sess, nil
}

// verify runs what was saved on a clean device session. The studio has given the device away, and takes
// it back. A flow runs through flow.Run and completes: it has no check. A scenario is replayed by the
// runner of semar run, with the secrets that were typed and the serial of the device.
func (b *studioBackend) verify(ctx context.Context, sess *mobilerec.Session, _ *mobilerec.Saved) (studio.VerifyResult, error) {
	b.mu.Lock()
	cur := b.cur
	b.mu.Unlock()
	if cur == nil {
		return studio.VerifyResult{}, errors.New("there is no recording to verify")
	}
	ov := sess.Overlay()
	clean := func(s string) string { return terminalSafe(oneLine(ov.Scrub(s))) }
	if sess.Kind() == mobilerec.KindFlow {
		entry, err := flow.Load(b.f.dir, sess.ID())
		if err != nil {
			return studio.VerifyResult{}, err
		}
		h, err := cur.open(ctx, ov.Lookup)
		if err != nil {
			return studio.VerifyResult{}, err
		}
		defer func() {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), androidCloseLimit)
			defer cancel()
			_ = h.Close(cctx)
		}()
		res, err := flow.Run(ctx, h.Engine, screenSource{repo: b.f.dir}, entry.Flow, nil, map[string]string{"run.id": "verify-" + sess.ID()})
		if err != nil {
			return studio.VerifyResult{}, fmt.Errorf("the flow %s could not be run: %s", sess.ID(), clean(err.Error()))
		}
		out := studio.VerifyResult{Status: res.Status, Message: fmt.Sprintf("Verified on a clean device session: the flow %s %s, in %s.", sess.ID(), res.Status, plural(len(res.Steps), "step"))}
		for _, st := range res.Steps {
			row := studio.VerifyStep{Name: st.Name, Status: "done"}
			switch {
			case st.Action.Refused != "":
				row.Status, row.Detail = "refused", clean(st.Action.Refused)
			case st.Action.Err != "":
				row.Status, row.Detail = "failed", clean(st.Action.Err)
			}
			out.Steps = append(out.Steps, row)
		}
		if res.Error != "" {
			out.Message += " " + clean(res.Error)
		}
		if res.Status == flow.StatusCompleted {
			out.Message += " A flow has no check, so it completes and does not pass."
		}
		return out, nil
	}
	oo := b.o
	oo.LookupEnv = ov.Lookup
	r := replayScenario(ctx, oo, b.f.dir, cur.envName, sess.ID(), recordVerifyBuilds(b.f.dir, cur.envName))
	var exit exitError
	switch {
	case r.result == nil && r.err != nil && !errors.As(r.err, &exit):
		return studio.VerifyResult{Status: "error", Message: "The scenario could not run: " + clean(r.err.Error())}, nil
	case r.result == nil:
		return studio.VerifyResult{Status: "error", Message: "The scenario did not finish. " + clean(r.stderr)}, nil
	}
	out := studio.VerifyResult{Status: string(r.result.Outcome), Report: r.report}
	if r.result.Outcome == domain.OutcomePassed {
		out.Message = "Replayed once: passed."
		return out, nil
	}
	out.Message = fmt.Sprintf("Replayed once: %s. The files are written: fix the step in the file, or record it again.", r.result.Outcome)
	for _, rs := range r.result.Reasons {
		name := string(rs.Step)
		if name == "" {
			name = "scenario"
		}
		out.Steps = append(out.Steps, studio.VerifyStep{Name: name, Status: string(rs.Rule), Detail: clean(rs.Text)})
	}
	return out, nil
}
