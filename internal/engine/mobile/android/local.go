package android

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

	// Local mode.
	adb     *adb
	device  Device
	release func()
	child   Child
	port    int
	session *webdriver.Session
	apps    map[string]bool // services whose app was checked and installed

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
		if un := p.startLocal(ctx); un != nil {
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

// version is the Android version of the device, or "".
func (p *Platform) version(ctx context.Context) string {
	out, err := p.adb.shell(ctx, "getprop", "ro.build.version.release")
	if err != nil {
		return ""
	}
	return firstLine(out)
}

// startLocal does what the whole run needs, once: it finds adb, picks the device, takes its lease,
// installs the server, and starts it.
func (p *Platform) startLocal(ctx context.Context) *mobile.Unavailable {
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
		release, ok, err := leases.TryLock("android", "device", dev.Serial)
		switch {
		case err != nil:
			return &mobile.Unavailable{Reason: fmt.Sprintf("the lease of the device could not be taken: %v", err)}
		case !ok:
			return &mobile.Unavailable{Reason: fmt.Sprintf("the device %s is used by another semar run: wait for it to end, or use another device", dev.Serial)}
		}
		p.release = release
	}
	p.adb, p.device = a, dev
	if un := p.bootCompleted(ctx, a); un != nil {
		return un
	}
	return p.startServer(ctx, a)
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
		return nil
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
	_, un := p.install(ctx, p.adb, app.Package, apkPath, false)
	return un
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
	if p.child != nil {
		p.child.Kill()
		p.child = nil
	}
	if p.adb != nil {
		for _, pkg := range []string{serverPackage, serverTestPackage} {
			sctx, cancel := step()
			if _, err := p.adb.shell(sctx, "am", "force-stop", pkg); err != nil {
				errs = append(errs, fmt.Errorf("%s could not be stopped: %w", pkg, err))
			}
			cancel()
		}
		if p.port != 0 {
			sctx, cancel := step()
			if _, err := p.adb.out(sctx, "forward", "--remove", fmt.Sprintf("tcp:%d", p.port)); err != nil {
				errs = append(errs, fmt.Errorf("the forward could not be removed: %w", err))
			}
			cancel()
			p.port = 0
		}
	}
	if p.release != nil {
		p.release()
		p.release = nil
	}
	return errors.Join(errs...)
}
