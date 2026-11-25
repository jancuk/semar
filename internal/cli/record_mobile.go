package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/screens"
)

// recordingBudget is how many device actions a recording session may take: a person acts slowly, and a
// session is not a run, but it has a limit.
const recordingBudget = 2000

// recordingSettle is the interactive settle budget for recording mode: snappier than replay's 3s default
// while still letting short transitions finish.
const recordingSettle = 1200 * time.Millisecond

// recordingPoll is the fixed delay between the screen reads of a settle while recording. A read costs
// about 25 ms, so a still screen is seen about 100 ms after it stops moving.
const recordingPoll = 80 * time.Millisecond

// mobileRecording says whether semar record records an app on a device: with --surface android, or with a
// flag that only a device has. It is decided before Chrome is looked for.
func mobileRecording(f recordTestFlags) bool {
	return f.surface == "android" || f.flow != "" || f.use != "" || f.ui || f.resume
}

// checkMobileFlags refuses a flag that does not go with recording on a device, in plain words.
func checkMobileFlags(f recordTestFlags, id string) error {
	switch f.surface {
	case "", "web", "android":
	default:
		return fmt.Errorf("--surface %q must be web or android", f.surface)
	}
	if f.surface == "web" {
		return errors.New("--surface web records in Chrome: --flow, --use, --resume, and --ui record an app on a device, and a flow is android only")
	}
	switch {
	case f.at != 0 && !f.resume:
		return errors.New("--at says how many steps --resume replays: add --resume")
	case f.at < 0:
		return fmt.Errorf("--at %d is not a step: it counts from 1", f.at)
	case f.ui && f.at != 0:
		return errors.New("--at is for the terminal: the studio asks for the step to replay to in its page, when you open a saved recording")
	case f.resume && f.use != "":
		return errors.New("--resume reopens the steps that the recording has: leave out --use")
	case f.ui && (f.flow != "" || f.use != ""):
		return errors.New("--ui lets you choose in the studio what to record and which flow to start with: leave out --flow and --use")
	case f.flow != "" && id != "":
		return errors.New("--flow takes a name and no scenario ID: its only argument is the environment")
	case f.flow == "" && !f.ui && id == "":
		return errors.New("name the scenario to record, as semar record <scenario id>, or record a flow with --flow <name>")
	case !f.ui && (f.port != 0 || f.noOpen):
		return errors.New("--port and --no-open belong to --ui, the local studio")
	case f.port < 0 || f.port > 65535:
		return fmt.Errorf("--port %d is not a port", f.port)
	}
	for flag, set := range map[string]bool{"--as": f.as != "", "--open": f.open != "", "--given": len(f.given) > 0, "--covers": f.covers != "", "--secret-env": f.secretEnv != ""} {
		if set {
			return fmt.Errorf("%s records in Chrome and does not apply to an app on a device: a secret is typed with the secret command and saved as env:NAME", flag)
		}
	}
	return nil
}

// recordAndroidService is the service of the environment that has an app on a device, and that the
// recording is for.
func recordAndroidService(env config.Environment, name string) (config.Service, error) {
	var apps []config.Service
	for _, s := range env.Services {
		if s.Android != nil {
			apps = append(apps, s)
		}
	}
	switch {
	case name != "":
		i := slices.IndexFunc(apps, func(s config.Service) bool { return s.Name == name })
		if i < 0 {
			return config.Service{}, fmt.Errorf("the service %s has no app on a device: add android.appPackage to it in semar.yaml", name)
		}
		return apps[i], nil
	case len(apps) == 1:
		return apps[0], nil
	case len(apps) == 0:
		return config.Service{}, errors.New("no service of the environment has an app on a device: add android.appPackage to one in semar.yaml")
	}
	names := make([]string, len(apps))
	for i, s := range apps {
		names[i] = s.Name
	}
	return config.Service{}, fmt.Errorf("choose the app to record with --service: %s", strings.Join(names, ", "))
}

// recordMobile records a flow or a scenario of an app on a device, in the terminal.
func recordMobile(ctx context.Context, cmd *cobra.Command, o options, f recordTestFlags, id, envArg string) error {
	out := cmd.OutOrStdout()
	if err := checkMobileFlags(f, id); err != nil {
		return err
	}
	kind, name := mobilerec.KindScenario, id
	ticket := f.ticket
	if f.flow != "" {
		kind, name = mobilerec.KindFlow, f.flow
		if err := flow.CheckID(name); err != nil {
			return err
		}
	} else if id != "" {
		if err := idcheck.Scenario(id); err != nil {
			return err
		}
		if ticket == "" {
			ticket = groupOf(id)
		}
		if err := idcheck.Check("ticket", ticket); err != nil {
			return err
		}
	}
	if f.use != "" {
		if err := flow.CheckID(f.use); err != nil {
			return err
		}
	}
	if f.ui {
		// The studio reads the configuration itself, each time: a repository with none gets a guided setup.
		return recordStudio(ctx, cmd, o, f, envArg)
	}
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	envName, env, err := cfg.Environment(envArg)
	if err != nil {
		return err
	}
	switch {
	case env.Kind == config.KindProduction:
		return fmt.Errorf("the environment %q is a production environment, and a recording taps and types, so semar record does not run there", envName)
	case env.Effects.Mode == "read_only":
		return fmt.Errorf("the environment %q is read only, and a recording taps and types, so it needs an environment that allows test data", envName)
	}
	env = withServiceAuth(cfg, env)
	svc, err := recordAndroidService(env, f.service)
	if err != nil {
		return err
	}
	app := f.app
	if app == "" {
		app = svc.Name
	}
	if err := idcheck.Check("app name", app); err != nil {
		return err
	}
	var resume *mobilerec.Loaded
	switch {
	case f.resume:
		if resume, err = loadResume(f.dir, kind, name, app); err != nil {
			return err
		}
	case kind == mobilerec.KindScenario:
		if _, err := existingScenario(f.dir, id, f.replace); err != nil {
			return err
		}
	case !f.replace:
		if _, err := flow.Read(f.dir, name); err == nil {
			return fmt.Errorf("the flow %s exists already: %s; use --replace to record it again", name, flow.Path(name))
		}
	}
	if f.use != "" {
		if _, err := flow.Load(f.dir, f.use); err != nil {
			return fmt.Errorf("--use %s: %w", f.use, err)
		}
	}

	if resume != nil && kind == mobilerec.KindScenario && f.ticket == "" && resume.Scenario.Ticket != "" {
		// A reopened scenario keeps the ticket of its file, unless --ticket says another.
		ticket = resume.Scenario.Ticket
	}
	overlay := mobilerec.NewOverlay(o.LookupEnv)
	open := recordingFactory(o, f.dir, cfg, envName, env, recordingSettle)
	verifyOpen := recordingFactory(o, f.dir, cfg, envName, env, 0)
	sess, err := mobilerec.New(mobilerec.Options{
		Repo: f.dir, Kind: kind, ID: name, Ticket: ticket, Service: svc.Name, App: app,
		Open: open, Lookup: o.LookupEnv, Overlay: overlay, Reset: svc.Android.ResetMode(), Use: f.use,
		Resume: resume, ResumeAt: f.at, Settle: recordingSettle, SettlePoll: recordingPoll,
		Clock: o.Clock, Sleep: o.Sleep, Now: o.Clock,
	})
	if err != nil {
		return err
	}
	defer sess.Wipe()
	defer func() { _ = sess.Close(ctx) }()

	what := "the flow " + name
	if kind == mobilerec.KindScenario {
		what = "the scenario " + id
	}
	if resume != nil {
		upTo := f.at
		if n := len(sess.Steps()); upTo == 0 || upTo > n {
			upTo = n
		}
		fmt.Fprintf(out, "Reopening %s: its %s, and the first %d are replayed on the device. New steps go after them.\n", what, plural(len(sess.Steps()), "step"), upTo)
	} else {
		fmt.Fprintf(out, "Recording %s on a device. The app is reset first, so the recording starts where a replay will.\n", what)
	}
	in := o.Stdin
	_, err = sess.Start(ctx)
	if errors.Is(err, mobilerec.ErrNeedsConfirm) && resume != nil {
		// A reopened recording that resets with clear on a physical device asks first, on the line that the
		// recorder reads next, so that no input is lost between the two.
		br := bufio.NewReader(o.Stdin)
		in = br
		fmt.Fprintf(out, "Replaying these steps resets with clear, which deletes the data of the app on %s.\nType yes to allow it: ", sess.Device())
		answer, _ := br.ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(answer), "yes") {
			return errors.New("nothing was reopened: the data of the app was not cleared")
		}
		sess.ConfirmClear()
		_, err = sess.Start(ctx)
	}
	if err != nil {
		var un *mobile.Unavailable
		if errors.As(err, &un) {
			return fmt.Errorf("nothing was recorded: %s", terminalSafe(oneLine(un.Reason)))
		}
		return fmt.Errorf("nothing was recorded: %w", err)
	}
	repl := &mobilerec.REPL{Session: sess, In: in, Out: out, Replace: f.replace || f.resume}
	if !f.noReplay {
		repl.Verify = func(ctx context.Context, sv *mobilerec.Saved) (string, error) {
			return verifyRecording(ctx, o, f, sess, overlay, verifyOpen, envName, kind, name, app)
		}
	}
	return repl.Run(ctx)
}

// recordingFactory opens a device session for a recording: a run of the purpose recording, an engine that
// acts through the policy and the redactor of the environment, and the Android platform under it. Every
// call opens a new one, with the environment that the recording reads: its secrets and its serial.
func recordingFactory(o options, dir string, cfg config.Config, envName string, env config.Environment, settle time.Duration) mobilerec.PlatformFactory {
	return func(ctx context.Context, lookup mobilerec.LookupEnv) (mobilerec.Handle, error) {
		oo := o
		oo.LookupEnv = lookup
		rg, err := newEnvRig(ctx, oo, dir, cfg, env, declaredBuilds{}, map[string]string{})
		if err != nil {
			return mobilerec.Handle{}, err
		}
		eng, platform := rg.mobileEngineOn(oo, dir, env, declaredBuilds{}, recordingBudget, settle)
		if eng == nil {
			return mobilerec.Handle{}, errors.New("no service of the environment has an app on a device")
		}
		closed := false
		return mobilerec.Handle{Engine: eng, Platform: platform, Close: func(ctx context.Context) error {
			if closed {
				return nil
			}
			closed = true
			err := eng.Close(ctx)
			now := o.Clock()
			return errors.Join(err, rg.run.Close(domain.Run{ID: rg.id, Environment: envName, EnvironmentKind: env.Kind, Purpose: domain.PurposeRecording, StartedAt: now, EndedAt: now}))
		}}, nil
	}
}

// verifyRecording gives the device away, runs what was saved on a clean device session, and takes the
// device back. A flow runs through flow.Run and is reported as completed, never as passed: it has no
// check. A scenario is replayed by the runner of semar run, with the same environment: the secrets that
// were typed and the serial of the device.
func verifyRecording(ctx context.Context, o options, f recordTestFlags, sess *mobilerec.Session, overlay *mobilerec.Overlay, open mobilerec.PlatformFactory, envName string, kind mobilerec.Kind, name, app string) (string, error) {
	if err := sess.Suspend(ctx); err != nil {
		return "", err
	}
	var msg string
	var err error
	if kind == mobilerec.KindFlow {
		msg, err = verifyFlow(ctx, f.dir, overlay, open, name)
	} else {
		var buf bytes.Buffer
		oo := o
		oo.LookupEnv = overlay.Lookup
		oo.Stdin = strings.NewReader("")
		replayOnceWithBuilds(ctx, &buf, oo, f, envName, app, name, recordVerifyBuilds(f.dir, envName))
		msg = strings.TrimRight(buf.String(), "\n")
	}
	if _, rerr := sess.Resume(ctx); rerr != nil {
		err = errors.Join(err, fmt.Errorf("the device could not be taken back: %w", rerr))
	}
	return msg, err
}

func verifyFlow(ctx context.Context, dir string, overlay *mobilerec.Overlay, open mobilerec.PlatformFactory, id string) (string, error) {
	entry, err := flow.Load(dir, id)
	if err != nil {
		return "", err
	}
	h, err := open(ctx, overlay.Lookup)
	if err != nil {
		return "", err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), androidCloseLimit)
		defer cancel()
		_ = h.Close(cctx)
	}()
	res, err := flow.RunWith(ctx, h.Engine, screenSource{repo: dir}, entry.Flow, nil, map[string]string{"run.id": "verify-" + id}, flow.Options{Flows: flow.Loader(dir).Flow})
	if err != nil {
		return "", fmt.Errorf("the flow %s could not be run: %s", id, terminalSafe(oneLine(overlay.Scrub(err.Error()))))
	}
	text := fmt.Sprintf("Verified on a clean device session: the flow %s %s, in %s.", id, res.Status, plural(len(res.Steps), "step"))
	if res.Error != "" {
		text += "\n  " + terminalSafe(oneLine(overlay.Scrub(res.Error)))
	}
	switch {
	case res.Status == flow.StatusCompleted && res.Checked > 0:
		text += fmt.Sprintf("\n  %s of the flow held. A flow completes and does not pass: a scenario still needs its own check.", plural(res.Checked, "check"))
	case res.Status == flow.StatusCompleted:
		text += "\n  A flow has no check, so it completes and does not pass."
	}
	return text, nil
}

// recordVerifyBuilds supplies a synthetic "recording" build for standalone Android services that have
// neither a build probe, an apk file, nor a local baseUrl, so verifying a freshly recorded scenario on
// an already-installed app reports passed/failed instead of inconclusive.
func recordVerifyBuilds(dir, envName string) []string {
	cfg, err := config.Load(dir)
	if err != nil {
		return nil
	}
	_, env, err := cfg.Environment(envName)
	if err != nil {
		return nil
	}
	var out []string
	for _, s := range env.Services {
		if s.Android != nil && s.Build == nil && s.Android.APK == "" && s.BaseURL == "" {
			out = append(out, s.Name+"=0000000")
		}
	}
	return out
}

// loadResume reads the files of a saved scenario or flow, to reopen it. A scenario needs its stored
// script: the one of its expectation hash, else the newest one of the same shape, which is what semar run
// uses for a scenario that was edited after it was recorded. The screens file is the one of the app. Path
// is relative to the repository.
func loadResume(dir string, kind mobilerec.Kind, id, app string) (*mobilerec.Loaded, error) {
	scr, err := screens.Load(dir, app)
	if err != nil {
		return nil, fmt.Errorf("the screens file of the app %s: %w", app, err)
	}
	l := &mobilerec.Loaded{Screens: scr}
	if kind == mobilerec.KindFlow {
		entry, err := flow.Load(dir, id)
		if err != nil {
			if errors.Is(err, flow.ErrNotFound) {
				return nil, fmt.Errorf("--resume: the flow %s is not saved: nothing to reopen (%s)", id, flow.Path(id))
			}
			return nil, err
		}
		l.Flow, l.Path = entry.Flow, flow.Path(id)
		return l, nil
	}
	files, err := scenarioFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(files[domain.ScenarioID(id)]) == 0 {
		return nil, fmt.Errorf("--resume: the scenario %s is not saved: nothing to reopen", id)
	}
	sc, err := loadScenario(dir, domain.ScenarioID(id))
	if err != nil {
		return nil, err
	}
	if sc.Surface != domain.SurfaceAndroid {
		return nil, fmt.Errorf("--resume: the scenario %s is for %q, and only an Android scenario is reopened", id, sc.Surface)
	}
	store, err := evidence.Open(filepath.Join(dir, ".semar"))
	if err != nil {
		return nil, err
	}
	sc2, ok, err := store.Script(sc.ID, domain.ExpectHash(sc))
	if err != nil {
		return nil, err
	}
	if !ok {
		all, err := store.ScriptsOf(sc.ID)
		if err != nil {
			return nil, err
		}
		shape := domain.ShapeHash(sc)
		for _, c := range all {
			if c.ShapeHash == shape && (!ok || c.StoredAt.After(sc2.StoredAt)) {
				sc2, ok = c, true
			}
		}
	}
	if !ok {
		return nil, fmt.Errorf("--resume: no stored script fits the scenario %s: record it again with --replace", id)
	}
	rel, err := filepath.Rel(dir, files[sc.ID][0])
	if err != nil {
		return nil, err
	}
	l.Scenario, l.Script, l.Path = sc, sc2, filepath.ToSlash(rel)
	return l, nil
}
