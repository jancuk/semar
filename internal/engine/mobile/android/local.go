package android

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/webdriver"
	"github.com/jancuk/semar/internal/fixture"
)

// Options is what the Android platform needs. Nothing starts before the first mobile step.
type Options struct {
	// FindADB gives the path of adb. The default is FindADB with LookupEnv and RepoRoot.
	FindADB func() (string, error)
	// Run starts adb. The default is ExecRunner.
	Run Runner
	// Home is the Semar home. It holds the lease of the device and, in android/, the two server APKs.
	Home string
	Env  config.Android
	// Apps are the apps by the name of their service.
	Apps     map[string]App
	RepoRoot string
	// LookupEnv reads the environment. The default is os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// HTTP is the client for the server on the device or at serverUrl. The default never uses a proxy for
	// a server on this machine.
	HTTP   *http.Client
	Sleep  func(ctx context.Context, d time.Duration) error
	Now    func() time.Time
	Logger *slog.Logger
}

var (
	_ mobile.Recoverer     = (*Platform)(nil)
	_ mobile.DensityReader = (*Platform)(nil)
)

// Platform is the Android device of a run. It is a mobile.Platform.
type Platform struct {
	o Options

	mu     sync.Mutex
	closed bool
	// startErr is the reason that the start failed. A platform that failed to start fails the same way
	// for the rest of the run, and does not try again at each step. appErr remembers per-service failures
	// so one broken app does not retry on every step or poison another service.
	startErr *mobile.Unavailable
	appErr   map[string]*mobile.Unavailable
	started  bool
	// deviceReady is true once the device was chosen, leased, and has booted. A controller needs no more.
	deviceReady bool
	// ctl is the controller of the device, made on the first call of Controller, and ctlSess is its
	// session at serverUrl.
	ctl     *Controller
	ctlSess *webdriver.Session

	// Local mode.
	adb     *adb
	device  Device
	release func()
	child   Child
	port    int
	session *webdriver.Session
	// local is the client of the server on the device, which Healthy asks.
	local *webdriver.Client
	apps  map[string]bool // services whose app was checked and installed

	density     int
	densityDone bool

	savedTheme       *string
	themeApplied     bool
	savedFontScale   *savedFontScale
	fontScaleApplied bool
	savedLocales     []savedAppLocale

	// serverUrl mode.
	client *webdriver.Client
	remote map[string]*webdriver.Session

	drivers map[string]*driver
}

// New makes the platform. It starts nothing.
func New(o Options) *Platform {
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	if o.FindADB == nil {
		lookup, root := o.LookupEnv, o.RepoRoot
		o.FindADB = func() (string, error) { return FindADB(lookup, root) }
	}
	if o.Run == nil {
		o.Run = ExecRunner{}
	}
	if o.Sleep == nil {
		o.Sleep = sleepFor
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Platform{o: o, appErr: map[string]*mobile.Unavailable{}, apps: map[string]bool{}, remote: map[string]*webdriver.Session{}, drivers: map[string]*driver{}}
}

func sleepFor(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Surface is android.
func (p *Platform) Surface() domain.Surface { return domain.SurfaceAndroid }

// Driver gives the driver of the app of a service, and starts the platform on the first call. A failure
// that a person can fix is a *mobile.Unavailable, which the same call gives again until the run ends.
func (p *Platform) Driver(ctx context.Context, service string) (mobile.Driver, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, &mobile.Unavailable{Reason: "the device was released at the end of the run"}
	}
	app, ok := p.o.Apps[service]
	if !ok {
		return nil, fmt.Errorf("the service %q has no android app", service)
	}
	if d, ok := p.drivers[service]; ok {
		return d, nil
	}
	if p.startErr != nil {
		return nil, p.startErr
	}
	if un, ok := p.appErr[service]; ok {
		return nil, un
	}
	var d *driver
	var un *mobile.Unavailable
	if p.o.Env.Remote() {
		d, un = p.remoteDriver(ctx, service, app)
		if un != nil {
			if ctx.Err() == nil {
				p.appErr[service] = un
			}
			return nil, un
		}
	} else {
		d, un = p.localDriver(ctx, service, app)
		if un != nil {
			if ctx.Err() == nil && p.startErr == nil {
				p.appErr[service] = un
			}
			return nil, un
		}
	}
	p.drivers[service] = d
	return d, nil
}

// serial is the serial that a person named: ANDROID_SERIAL wins over android.device.
func (p *Platform) serial() string {
	if v, ok := p.o.LookupEnv("ANDROID_SERIAL"); ok && v != "" {
		return v
	}
	return p.o.Env.Device
}

func (p *Platform) localDriver(ctx context.Context, service string, app App) (*driver, *mobile.Unavailable) {
	if !p.started {
		un := p.startDevice(ctx)
		if un == nil {
			un = p.applyDisplaySettings(ctx)
		}
		if un == nil {
			un = p.ensureServer(ctx)
		}
		if un != nil {
			if ctx.Err() == nil {
				p.startErr = un
			}
			return nil, un
		}
		p.started = true
	}
	if !p.apps[service] {
		if un := p.prepareApp(ctx, app); un != nil {
			return nil, un
		}
		p.apps[service] = true
	}
	return &driver{
		sess:  p.session,
		life:  &adbLife{a: p.adb, sess: p.session, app: app},
		about: mobile.About{Device: p.device.Serial, Package: app.Package, Version: p.version(ctx)},
	}, nil
}

// ScreenDensity returns the cached screen density of the device in dots per inch, or 0 when remote
// or unknown.
func (p *Platform) ScreenDensity(ctx context.Context) int {
	p.mu.Lock()
	if p.densityDone {
		d := p.density
		p.mu.Unlock()
		return d
	}
	if p.closed || p.o.Env.Remote() || p.adb == nil {
		p.mu.Unlock()
		return 0
	}
	a := p.adb
	p.mu.Unlock()

	dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := a.shell(dctx, "wm", "density")
	if err != nil || ctx.Err() != nil {
		return 0
	}
	dpi := 0
	if v, ok := ParseWMDensity(out); ok {
		dpi = v
	}

	p.mu.Lock()
	if !p.closed {
		p.density = dpi
		p.densityDone = true
	}
	p.mu.Unlock()
	return dpi
}

// version is the Android version of the device, or "".
func (p *Platform) version(ctx context.Context) string {
	out, err := p.adb.shell(ctx, "getprop", "ro.build.version.release")
	if err != nil {
		return ""
	}
	return firstLine(out)
}

// startDevice does what a device needs, once: it finds adb, picks the device, takes its lease, and waits
// until the device has booted. It is what a controller needs, and a run goes on with ensureServer. A call
// after a start that failed continues where that one stopped.
func (p *Platform) startDevice(ctx context.Context) *mobile.Unavailable {
	if p.deviceReady {
		return nil
	}
	if p.adb == nil {
		bin, err := p.o.FindADB()
		if err != nil {
			return &mobile.Unavailable{Reason: err.Error()}
		}
		a := &adb{bin: bin, run: p.o.Run}
		// The daemon inherits the pipes of the program that starts it, so its output goes to the null device.
		if err := p.o.Run.Quiet(ctx, bin, "start-server"); err != nil {
			return &mobile.Unavailable{Reason: fmt.Sprintf("the adb server could not be started: %v", err)}
		}
		out, err := a.out(ctx, "devices", "-l")
		if err != nil {
			return &mobile.Unavailable{Reason: fmt.Sprintf("adb could not list the devices: %v", err)}
		}
		dev, un := ChooseDevice(ParseDevices(out), p.serial())
		if un != nil {
			return un
		}
		a.serial = dev.Serial

		if p.o.Home != "" {
			leases, err := fixture.OpenLeases(p.o.Home)
			if err != nil {
				return &mobile.Unavailable{Reason: fmt.Sprintf("the lease of the device could not be taken: %v", err)}
			}
			release, ok, holder, err := leases.TryLockHolder("android", "device", dev.Serial)
			switch {
			case err != nil:
				return &mobile.Unavailable{Reason: fmt.Sprintf("the lease of the device could not be taken: %v", err)}
			case !ok:
				return &mobile.Unavailable{Reason: busyDevice(dev.Serial, holder)}
			}
			p.release = release
		}
		p.adb, p.device = a, dev
	}
	if un := p.bootCompleted(ctx, p.adb); un != nil {
		return un
	}
	p.deviceReady = true
	return nil
}

// ensureServer installs the server, starts it, and opens the session, unless that was done.
func (p *Platform) ensureServer(ctx context.Context) *mobile.Unavailable {
	if p.session != nil {
		return nil
	}
	return p.startServer(ctx, p.adb)
}

// prepareApp installs the APK of a service, or checks that the app is on the device. The file must be the
// one that the build was read from.
func (p *Platform) prepareApp(ctx context.Context, app App) *mobile.Unavailable {
	if app.APK == "" {
		paths, err := p.adb.packagePaths(ctx, app.Package)
		if err != nil {
			return &mobile.Unavailable{Reason: fmt.Sprintf("the packages of the device could not be read: %v", err)}
		}
		if len(paths) == 0 {
			return &mobile.Unavailable{Reason: fmt.Sprintf("the app %s is not installed on the device, and the service lists no apk to install", app.Package)}
		}
		return p.applyLocaleForApp(ctx, app.Package)
	}
	apkPath, err := p.resolvePath(app.APK)
	if err != nil {
		return &mobile.Unavailable{Reason: err.Error()}
	}
	if app.APKDigest != "" {
		sum, err := fileSHA256(apkPath)
		if err != nil {
			return &mobile.Unavailable{Reason: err.Error()}
		}
		if !strings.HasPrefix(sum, strings.ToLower(app.APKDigest)) {
			return &mobile.Unavailable{Reason: fmt.Sprintf("the APK %s changed after the build of this run was read: its result would belong to another build. Run again", baseName(apkPath))}
		}
	}
	if _, un := p.install(ctx, p.adb, app.Package, apkPath, false); un != nil {
		return un
	}
	return p.applyLocaleForApp(ctx, app.Package)
}

func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// Close ends what the platform started: the session, the instrumentation, the server packages, the
// forward, and the lease. It never uses the context of the run, which may be cancelled already, and it is
// given ten seconds. It is safe to call twice, and after a start that failed. The adb daemon, the server
// packages on the device, and the app stay.
func (p *Platform) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	base := context.WithoutCancel(ctx)
	step := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(base, 3*time.Second)
	}
	var errs []error
	// What a controller or the run changed on the device is undone first, while the device still answers.
	if p.ctl != nil {
		errs = append(errs, p.ctl.undo(base)...)
	}
	errs = append(errs, p.undoDisplay(base)...)
	p.density, p.densityDone = 0, false
	if p.ctlSess != nil {
		sctx, cancel := step()
		if err := p.ctlSess.Delete(sctx); err != nil {
			errs = append(errs, fmt.Errorf("the session of the controller could not be ended: %w", err))
		}
		cancel()
		p.ctlSess = nil
	}
	if p.session != nil {
		sctx, cancel := step()
		if err := p.session.Delete(sctx); err != nil {
			errs = append(errs, fmt.Errorf("the session could not be ended: %w", err))
		}
		cancel()
		p.session = nil
	}
	for name, s := range p.remote {
		sctx, cancel := step()
		if err := s.Delete(sctx); err != nil {
			errs = append(errs, fmt.Errorf("the session of %s could not be ended: %w", name, err))
		}
		cancel()
	}
	p.remote = map[string]*webdriver.Session{}
	// Stop the server packages while the instrumentation child is still attached so AndroidJUnitRunner
	// does not hit a dead binder watcher and pop up a system "keeps stopping" crash dialog on Android 17.
	if p.adb != nil {
		for _, pkg := range []string{serverPackage, serverTestPackage} {
			sctx, cancel := step()
			if _, err := p.adb.shell(sctx, "am", "force-stop", pkg); err != nil {
				errs = append(errs, fmt.Errorf("%s could not be stopped: %w", pkg, err))
			}
			cancel()
		}
	}
	if p.child != nil {
		t := time.NewTimer(500 * time.Millisecond)
		select {
		case <-p.child.Done():
			t.Stop()
		case <-t.C:
			p.child.Kill()
		}
		p.child = nil
	}
	if p.adb != nil && p.port != 0 {
		sctx, cancel := step()
		if _, err := p.adb.out(sctx, "forward", "--remove", fmt.Sprintf("tcp:%d", p.port)); err != nil {
			errs = append(errs, fmt.Errorf("the forward could not be removed: %w", err))
		}
		cancel()
		p.port = 0
	}
	if p.release != nil {
		p.release()
		p.release = nil
	}
	return errors.Join(errs...)
}

// healthLimit is how long Healthy waits for the server.
const healthLimit = 2 * time.Second

// Healthy says whether the server on the device answers and still knows its session. It reads /status and
// asks the session for the focused element, with two seconds for both. It is conservative: only a server
// that cannot be reached, that does not answer in time, that is not ready, or that says its session is gone
// is unhealthy. An answer that cannot be read, or that holds no element, is no sign of a dead server. A
// server at serverUrl is not Semar's to restart, so it is always healthy here.
func (p *Platform) Healthy(ctx context.Context) bool {
	p.mu.Lock()
	client, sess := p.local, p.session
	p.mu.Unlock()
	if client == nil || sess == nil {
		return true
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, healthLimit)
	defer cancel()
	ready, err := client.Status(ctx)
	if err != nil {
		return parent.Err() != nil || !serverGone(err)
	}
	if !ready.Ready {
		return false
	}
	if _, err := sess.Active(ctx); err != nil {
		return parent.Err() != nil || !serverGone(err)
	}
	return true
}

// serverGone says that an error of a call shows a server that is dead or hung, or that lost its session:
// the call did not reach it, did not end in time, or was answered with a code of a lost session.
func serverGone(err error) bool {
	var we *webdriver.Error
	if errors.As(err, &we) {
		switch strings.ToLower(we.Code) {
		case "invalid session id", "no such session", "session not created":
			return true
		}
		return false
	}
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// recoverCleanup is how long each step that undoes the old server may take.
const recoverCleanup = 3 * time.Second

// Recover starts the UiAutomator2 server again, on a device that Semar started it on: it ends the old
// session, stops the server packages and the instrumentation, forwards the port again, starts the
// instrumentation, and opens a new session with the same settings. The apps that were prepared stay as
// they are, so nothing is installed again, and the drivers that were handed out are dropped: they point
// at a session that no longer exists. A server at serverUrl is not restarted. A restart that fails
// leaves the platform unavailable for the rest of the run.
func (p *Platform) Recover(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.closed:
		return errors.New("the device was released at the end of the run")
	case p.o.Env.Remote():
		return errors.New("restarting the server is not supported for a server at serverUrl")
	case !p.started || p.adb == nil:
		return errors.New("the server was not started, so there is nothing to restart")
	}
	ctx, cancel := context.WithTimeout(ctx, p.o.Env.StartLimit()+5*recoverCleanup)
	defer cancel()
	p.stopServer(ctx)
	p.drivers = map[string]*driver{}
	if un := p.launchServer(ctx, p.adb); un != nil {
		p.startErr = un
		return un
	}
	return nil
}

// stopServer ends the session, stops the server packages and the instrumentation, and removes the
// forward, so that the server can be started again. Each step has its own time, and none of them fails
// the call: it logs.
func (p *Platform) stopServer(ctx context.Context) {
	step := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(ctx, recoverCleanup)
	}
	if p.session != nil {
		sctx, c := step()
		if err := p.session.Delete(sctx); err != nil {
			p.o.Logger.DebugContext(ctx, "the old session could not be ended", "error", err)
		}
		c()
		p.session = nil
	}
	for _, pkg := range []string{serverPackage, serverTestPackage} {
		sctx, c := step()
		if _, err := p.adb.shell(sctx, "am", "force-stop", pkg); err != nil {
			p.o.Logger.DebugContext(ctx, "a server package could not be stopped", "package", pkg, "error", err)
		}
		c()
	}
	if p.child != nil {
		t := time.NewTimer(500 * time.Millisecond)
		select {
		case <-p.child.Done():
			t.Stop()
		case <-t.C:
			p.child.Kill()
		}
		p.child = nil
	}
	if p.port != 0 {
		sctx, c := step()
		if _, err := p.adb.out(sctx, "forward", "--remove", fmt.Sprintf("tcp:%d", p.port)); err != nil {
			p.o.Logger.DebugContext(ctx, "the old forward could not be removed", "error", err)
		}
		c()
		p.port = 0
	}
	p.local = nil
}

// Controller gives the controller of the device: the device with no app. It does what a start needs up to
// the boot (adb, the device, its lease), and nothing on the device changes before a method of the
// controller asks for it: the server of UiAutomator2 starts with the first call that reads or touches
// the screen. The same controller is given on every call. A failure that a person can fix is a
// *mobile.Unavailable, and a device that was not there is looked for again at the next call.
func (p *Platform) Controller(ctx context.Context) (*Controller, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, &mobile.Unavailable{Reason: "the device was released at the end of the run"}
	}
	if p.ctl != nil {
		return p.ctl, nil
	}
	if !p.o.Env.Remote() {
		if p.startErr != nil {
			return nil, p.startErr
		}
		if un := p.startDevice(ctx); un != nil {
			return nil, un
		}
	}
	p.ctl = &Controller{p: p}
	return p.ctl, nil
}
