package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/jancuk/semar/internal/apimcp"
	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/journey"
	"github.com/jancuk/semar/internal/journeymcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/webmcp"
)

func init() { registerMCPSurface("journey", buildMCPJourney) }

// buildMCPJourney makes the journey tools. They read what the api, web, and mobile tools of the same
// server did, so they are built after those groups and need at least one of them.
func buildMCPJourney(w *mcpWiring) (mcpSurfaceResult, error) {
	switch {
	case !w.RepoMode:
		return mcpSurfaceResult{}, mcpUnavailable("there is no repository")
	case w.EnvName == "" || w.Policy == nil:
		return mcpSurfaceResult{}, mcpUnavailable("no environment is bound: start semar mcp with --env <name>")
	}
	opts := journeymcp.Options{Guard: w.Guard, Scrub: w.Scrubber, Clock: w.Opts.Clock}
	if am := mcpAPIManagerOf(w); am != nil {
		opts.API = mcpJourneyAPI{am}
	}
	if wm, ok := mcpWebManager(w); ok && wm != nil {
		opts.Web = &mcpJourneyWeb{m: wm, w: w}
	}
	if w.Mobile != nil && hasAndroidService(w.Env) && slices.Contains(w.Surfaces, "mobile") {
		opts.Mobile = &mcpJourneyMobile{w: w}
	}
	if opts.API == nil && opts.Web == nil && opts.Mobile == nil {
		return mcpSurfaceResult{}, mcpUnavailable("none of the api, web, or mobile tools is served, so there is nothing to make a journey from")
	}
	opts.Screens = func(app string) domain.Screens {
		s, err := screens.Load(w.Dir, app)
		if err != nil {
			return domain.Screens{}
		}
		return s
	}
	opts.Backend = &mcpJourneyBackend{w: w}
	m := journeymcp.New(opts)
	w.ExtraMiddleware = append(w.ExtraMiddleware, m.AutoLegMiddleware())
	return mcpSurfaceResult{Tools: m.Tools(), Close: m.Close}, nil
}

// ---- taps ----

// mcpJourneyAPI reads the log of the api tools. The step type of apimcp is copied, field by field.
type mcpJourneyAPI struct{ m *apimcp.Manager }

func (a mcpJourneyAPI) Seq() int { return a.m.Seq() }
func (a mcpJourneyAPI) Since(i int) []journeymcp.APIStep {
	steps := a.m.Since(i)
	out := make([]journeymcp.APIStep, len(steps))
	for k, s := range steps {
		out[k] = journeymcp.APIStep{
			Service: s.Service, Request: s.Request, Captures: s.Captures, Expect: s.Expect, As: s.As,
			Status: s.Status, Write: s.Write, Action: s.Action, PathTemplate: s.PathTemplate,
		}
	}
	return out
}
func (a mcpJourneyAPI) DropLast() bool          { return a.m.DropLast() }
func (a mcpJourneyAPI) Vars() map[string]string { return a.m.Vars() }
func (a mcpJourneyAPI) SetVar(k, v string)      { a.m.SetVar(k, v) }

// mcpJourneyWeb reads the one window of the web tools.
type mcpJourneyWeb struct {
	m *webmcp.Manager
	w *mcpWiring
}

func (t *mcpJourneyWeb) Mark(ctx context.Context) (int, string, error) {
	mark, ok := t.m.Mark()
	if !ok {
		return 0, "", journeymcp.ErrNoWindow
	}
	url, _ := t.m.Location(ctx)
	return mark, url, nil
}
func (t *mcpJourneyWeb) EventsFrom(i int) []record.Event { return t.m.EventsFrom(i) }
func (t *mcpJourneyWeb) Vars() map[string]string         { return t.m.Vars() }
func (t *mcpJourneyWeb) Service() string                 { return t.m.Info().Service }

// DefaultService is the one service with a web page, which web_open takes when it is given no service.
func (t *mcpJourneyWeb) DefaultService() string {
	name := ""
	for _, s := range t.w.Env.Services {
		if s.BaseURL == "" {
			continue
		}
		if name != "" {
			return ""
		}
		name = s.Name
	}
	return name
}
func (t *mcpJourneyWeb) BaseURL(service string) string {
	for _, s := range t.w.Env.Services {
		if s.Name == service {
			return s.BaseURL
		}
	}
	return ""
}

// mcpJourneyMobile drives the recording of an android leg through the Manager of the mobile tools.
type mcpJourneyMobile struct{ w *mcpWiring }

func (t *mcpJourneyMobile) StartLegFlow(ctx context.Context, flowID, service, ticket string) error {
	return t.w.Mobile.StartLegFlow(ctx, flowID, service, ticket)
}

func (t *mcpJourneyMobile) EndLegFlow(ctx context.Context) (journeymcp.LegFlow, error) {
	lf, err := t.w.Mobile.EndLegFlow(ctx)
	if err != nil {
		return journeymcp.LegFlow{}, err
	}
	out := journeymcp.LegFlow{Flow: lf.Flow, App: lf.App, Added: lf.Added, Steps: lf.Steps, Notes: lf.Notes, Captures: lf.Captures}
	for _, c := range lf.Checks {
		out.Checks = append(out.Checks, journeymcp.LegCheck{ID: c.ID, Action: c.Action, Screen: c.Screen, Element: c.Element, Expect: c.Expect})
	}
	return out, nil
}

func (t *mcpJourneyMobile) DiscardLegFlow(ctx context.Context) error {
	return t.w.Mobile.DiscardLegFlow(ctx)
}
func (t *mcpJourneyMobile) UndoLegStep(ctx context.Context) error { return t.w.Mobile.UndoLegStep(ctx) }
func (t *mcpJourneyMobile) LegFlowSteps() int                     { return t.w.Mobile.LegFlowSteps() }

// LegScreen lists the screen that the recording of the leg is on.
func (t *mcpJourneyMobile) LegScreen(ctx context.Context) (string, error) {
	return t.w.Mobile.LegScreen(ctx)
}

// ForegroundService names the service whose app is on the screen of the device.
func (t *mcpJourneyMobile) ForegroundService(ctx context.Context) (string, error) {
	pkg, err := t.w.Mobile.ForegroundPackage(ctx)
	if err != nil {
		return "", err
	}
	for _, s := range t.w.Env.Services {
		if s.Android != nil && s.Android.AppPackage == pkg {
			return s.Name, nil
		}
	}
	return "", fmt.Errorf("the app %s on the screen is not an app of a service of the environment", pkg)
}

// ---- backend ----

// mcpJourneyBackend checks, writes, and replays journeys with the code that semar journey run uses.
type mcpJourneyBackend struct {
	w *mcpWiring

	// buildMu guards buildNotes, what BuildProblem answered for each service. The configuration is read once
	// at start, so the answer of a service does not change while the server runs.
	buildMu    sync.Mutex
	buildNotes map[string]string
}

func (b *mcpJourneyBackend) Exists(id domain.JourneyID) bool {
	for _, p := range []string{journey.Path(id), strings.TrimSuffix(journey.Path(id), ".yaml") + ".yml"} {
		if _, err := os.Lstat(b.w.Dir + "/" + p); err == nil {
			return true
		}
	}
	return false
}

// BuildProblem says when the replay of a journey with a leg of the service could not read its build: the
// service has no build probe and no declared build (an apk is one). The agent hears it when the leg opens,
// before it does the work of a journey that the save would not keep.
func (b *mcpJourneyBackend) BuildProblem(ctx context.Context, service string) string {
	b.buildMu.Lock()
	defer b.buildMu.Unlock()
	if note, ok := b.buildNotes[service]; ok {
		return note
	}
	note := b.buildProblem(ctx, service)
	if ctx.Err() == nil {
		if b.buildNotes == nil {
			b.buildNotes = map[string]string{}
		}
		b.buildNotes[service] = note
	}
	return note
}

func (b *mcpJourneyBackend) buildProblem(ctx context.Context, service string) string {
	var svc *config.Service
	for i := range b.w.Env.Services {
		if b.w.Env.Services[i].Name == service {
			svc = &b.w.Env.Services[i]
			break
		}
	}
	if svc == nil || svc.Build != nil {
		return ""
	}
	declared, err := readDeclaredBuilds(ctx, b.w.Dir, nil, b.w.Env.Services)
	if err != nil || declared.has(service) {
		return ""
	}
	give := "a build probe"
	if svc.Android != nil {
		give = "android.apk or a build probe"
	}
	return fmt.Sprintf("the service %s has no build that the replay of semar_journey_save can read, so the save would end blocked and keep nothing. Add %s to %s in .semar/semar.yaml and start semar mcp again (the configuration is read at start), or save with run: false and run it later with semar journey run --build %s=<commit>", service, give, service, service)
}

// screensFor is the screens file of an app as it will be once the additions are written.
func (b *mcpJourneyBackend) screensFor(app string, adds []record.ScreenAdd) (domain.Screens, error) {
	data, err := screens.Read(b.w.Dir, app)
	missing := errors.Is(err, screens.ErrNotFound)
	if err != nil && !missing {
		return domain.Screens{}, err
	}
	switch {
	case missing && len(adds) == 0:
		return domain.Screens{}, err
	case missing:
		data, err = screens.New(app, adds)
	case len(adds) > 0:
		data, err = screens.Add(data, app, adds)
	}
	if err != nil {
		return domain.Screens{}, err
	}
	return screens.Parse(data, screens.Path(app), app)
}

func (b *mcpJourneyBackend) Validate(j domain.Journey, p journeymcp.Pending) []journey.Problem {
	adds := map[string][]record.ScreenAdd{}
	for _, f := range p.Screens {
		adds[f.App] = append(adds[f.App], f.Adds...)
	}
	screensOf := func(app string) (domain.Screens, error) { return b.screensFor(app, adds[app]) }
	loader := flow.Loader(b.w.Dir).Resolver()
	flows := script.FlowResolver(func(id string) (domain.Flow, error) {
		for _, f := range p.Flows {
			if f.ID == id {
				return f, nil
			}
		}
		return loader(id)
	})
	file := journey.Path(j.ID)
	ps := journey.ValidateWithFlows(j, nil, screensOf, flows)
	for i := range ps {
		ps[i].File = file
	}
	// The file must read back as it was made.
	if _, err := journey.Marshal(j); err != nil {
		ps = append(ps, journey.Problem{File: file, Message: err.Error()})
	}
	return ps
}

// txn remembers how to put back what a write changed.
type txn struct{ undos []func() error }

func (t *txn) add(f func() error) { t.undos = append(t.undos, f) }

// undo puts everything back, last write first, and reports what could not be put back.
func (t *txn) undo() error {
	var errs []error
	for i := len(t.undos) - 1; i >= 0; i-- {
		if err := t.undos[i](); err != nil {
			errs = append(errs, err)
		}
	}
	t.undos = nil
	return errors.Join(errs...)
}

func (b *mcpJourneyBackend) Write(j domain.Journey, p journeymcp.Pending, replace bool) (*journeymcp.Saved, error) {
	repo := b.w.Dir
	tx := &txn{}
	var files []string
	fail := func(err error) (*journeymcp.Saved, error) {
		if uerr := tx.undo(); uerr != nil {
			err = errors.Join(err, fmt.Errorf("and the files could not be put back: %w", uerr))
		}
		return nil, err
	}
	for _, f := range p.Screens {
		if len(f.Adds) == 0 {
			continue
		}
		rel, undo, err := writeScreensAdds(repo, f.App, f.Adds)
		if err != nil {
			return fail(err)
		}
		tx.add(undo)
		files = append(files, rel)
	}
	for _, f := range p.Flows {
		if !replace {
			if _, err := os.Lstat(repo + "/" + flow.Path(f.ID)); err == nil {
				return fail(fmt.Errorf("the flow %s exists already: save with replace: true to write over it", f.ID))
			}
		}
		sv, err := flow.Write(repo, f)
		if err != nil {
			return fail(err)
		}
		tx.add(sv.Undo)
		files = append(files, sv.Path)
	}
	sv, err := journey.Write(repo, j, replace)
	if err != nil {
		return fail(err)
	}
	tx.add(sv.Undo)
	files = append(files, sv.Path)
	return &journeymcp.Saved{Files: files, Undo: tx.undo}, nil
}

// writeScreensAdds adds elements to the screens file of an app, atomically and inside the repository, and
// gives the way to put the file back.
func writeScreensAdds(repo, app string, adds []record.ScreenAdd) (string, func() error, error) {
	rel := screens.Path(app)
	current, err := screens.Read(repo, app)
	missing := errors.Is(err, screens.ErrNotFound)
	if err != nil && !missing {
		return "", nil, err
	}
	var merged []byte
	if missing {
		merged, err = screens.New(app, adds)
	} else {
		merged, err = screens.Add(current, app, adds)
	}
	if err != nil {
		return "", nil, err
	}
	if _, err := screens.Parse(merged, rel, app); err != nil {
		return "", nil, err
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	if _, err := atomicfile.MkdirIn(root, screens.RelDir); err != nil {
		return "", nil, err
	}
	if err := atomicfile.WriteIn(root, rel, merged, 0o644); err != nil {
		return "", nil, err
	}
	digest := flow.Digest(merged)
	undo := func() error {
		again, err := os.OpenRoot(repo)
		if err != nil {
			return err
		}
		defer again.Close()
		now, err := readRootFile(again, rel)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("undo %s: the file is gone", rel)
		case err != nil:
			return err
		case flow.Digest(now) != digest:
			return fmt.Errorf("undo %s: the file changed since it was written, so it is left as it is", rel)
		}
		if missing {
			return again.Remove(rel)
		}
		return atomicfile.WriteIn(again, rel, current, 0o644)
	}
	return rel, undo, nil
}

func readRootFile(root *os.Root, rel string) ([]byte, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 4<<20))
}

// scrubWriter keeps secrets out of what a replay prints on standard error.
type scrubWriter struct {
	mu sync.Mutex
	s  *mcpkit.Scrubber
	w  io.Writer
}

func (s *scrubWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := io.WriteString(s.w, s.s.Text(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Run replays the saved journey once, as semar journey run does: no model, and a verdict that code
// decides. The device of the mobile tools is given back first, because the replay opens its own.
func (b *mcpJourneyBackend) Run(ctx context.Context, id domain.JourneyID) (journeymcp.RunOutcome, error) {
	w := b.w
	env := withServiceAuth(w.Cfg, w.Env)
	chosen, _, err := selectJourneys(io.Discard, w.Dir, []string{string(id)}, false)
	if err != nil {
		return journeymcp.RunOutcome{}, err
	}
	declared, err := readDeclaredBuilds(ctx, w.Dir, nil, env.Services)
	if err != nil {
		return journeymcp.RunOutcome{}, err
	}
	hasLeg := func(surface domain.Surface) bool {
		return slices.ContainsFunc(chosen, func(j domain.Journey) bool {
			return slices.ContainsFunc(j.Legs, func(l domain.Leg) bool { return l.Surface == surface })
		})
	}
	// The replay opens its own browser on the profile of the account, which the window of the web tools
	// holds, so the window is closed first.
	if hasLeg(domain.SurfaceWeb) {
		if wm, ok := mcpWebManager(w); ok && wm != nil {
			if err := wm.Close(ctx); err != nil {
				return journeymcp.RunOutcome{}, fmt.Errorf("the browser window could not be closed for the replay: %w", err)
			}
		}
	}
	if hasLeg(domain.SurfaceAndroid) && w.Mobile != nil {
		if err := w.Mobile.Close(ctx); err != nil {
			return journeymcp.RunOutcome{}, fmt.Errorf("the device could not be released for the replay: %w", err)
		}
	}
	var errw io.Writer = io.Discard
	if w.Stderr != nil {
		errw = &scrubWriter{s: w.Scrubber, w: w.Stderr}
	}
	fin, err := executeJourneys(ctx, errw, w.Opts, w.Dir, w.Cfg, w.EnvName, env, declared, chosen, w.Headed, func() {})
	if err != nil {
		return journeymcp.RunOutcome{}, err
	}
	out := journeymcp.RunOutcome{Outcome: string(domain.JourneyNotRun), Report: fin.reportPath}
	if rel, err := relToDir(w.Dir, fin.reportPath); err == nil {
		out.Report = rel
	}
	if fin.interrupted {
		out.Reasons = []string{"the replay was interrupted"}
		return out, nil
	}
	for _, jr := range fin.record.Journeys {
		if jr.Journey != id {
			continue
		}
		out.Outcome, out.FirstFailing = string(jr.Outcome), string(jr.FirstFailing)
		for _, r := range jr.Reasons {
			out.Reasons = append(out.Reasons, r.Text)
		}
		for _, l := range jr.Legs {
			lo := journeymcp.LegOutcome{Leg: string(l.Leg), Outcome: string(l.Outcome)}
			for _, r := range l.Reasons {
				lo.Reasons = append(lo.Reasons, r.Text)
			}
			out.Legs = append(out.Legs, lo)
		}
		for _, inv := range jr.Invariants {
			if inv.State != domain.CheckTrue && inv.Detail != "" {
				out.Reasons = append(out.Reasons, fmt.Sprintf("invariant %d: %s", inv.Index+1, inv.Detail))
			}
		}
	}
	return out, nil
}

func relToDir(dir, path string) (string, error) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("not inside the repository")
	}
	return filepath.ToSlash(rel), nil
}
