package android

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/webdriver"
)

// App is the app of one service.
type App struct {
	Package string
	// Activity is the activity to start. Empty means the launcher activity.
	Activity string
	// APK is the absolute path of the file to install, or "".
	APK string
	// APKDigest is the digest that the build recorded for the APK: the first 40 hexadecimal digits of its
	// sha256. The file is checked against it before it is installed, so the app that runs is the build
	// that the result belongs to. Empty skips the check.
	APKDigest string
	// Ref is what a server that is not on this machine installs, such as bs://abc.
	Ref string
	// Reset is restart (the default), clear, or none.
	Reset string
	// Permissions are granted after each reset.
	Permissions config.Permissions
}

func (a App) resetMode() string {
	if a.Reset == "" {
		return config.ResetRestart
	}
	return a.Reset
}

// lifecycle is what differs between an app that adb controls and one that a server controls.
type lifecycle interface {
	// reset puts the app in its starting state with the mode, or with the mode of the service for "".
	reset(ctx context.Context, mode string) error
	launch(ctx context.Context) error
	open(ctx context.Context, link string) error
	key(ctx context.Context, code int) error
	hideKeyboard(ctx context.Context) error
}

// driver is the app of one service on one device. The part that reads and touches the screen is the
// same for both modes, so it is one type, and a lifecycle does the rest.
type driver struct {
	sess  *webdriver.Session
	life  lifecycle
	about mobile.About

	mu      sync.Mutex
	lastSrc string
	last    *mobile.Tree
}

func (d *driver) Reset(ctx context.Context) error { return d.life.reset(ctx, "") }

// ResetWith resets the app with a mode in place of the one of its service: restart, clear, or none.
func (d *driver) ResetWith(ctx context.Context, mode string) error { return d.life.reset(ctx, mode) }
func (d *driver) Launch(ctx context.Context) error                 { return d.life.launch(ctx) }
func (d *driver) Open(ctx context.Context, link string) error      { return d.life.open(ctx, link) }
func (d *driver) About() mobile.About                              { return d.about }

// Source reads the tree. Two reads with the same bytes give the tree that was parsed the first time.
func (d *driver) Source(ctx context.Context) (*mobile.Tree, error) {
	src, err := d.sess.Source(ctx)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.last != nil && src == d.lastSrc {
		return d.last, nil
	}
	tree, err := ParseSource([]byte(src))
	if err != nil {
		return nil, err
	}
	d.lastSrc, d.last = src, tree
	return tree, nil
}

func (d *driver) Touch(ctx context.Context, g mobile.Gesture) error {
	if g.Taps > 1 && g.From == g.To && g.Hold == 0 {
		return d.sess.Tap(ctx, webdriver.Point(g.From), g.Taps)
	}
	return d.sess.Touch(ctx, webdriver.Point(g.From), webdriver.Point(g.To), g.Hold, g.Move)
}

// Type replaces the text of the field that has the focus. The server appends the text it is given, so the
// field is cleared first.
func (d *driver) Type(ctx context.Context, text string) error {
	id, err := d.sess.Active(ctx)
	if err != nil {
		return fmt.Errorf("no field has the focus: %w", err)
	}
	if err := d.sess.Clear(ctx, id); err != nil {
		return err
	}
	return d.sess.SetValue(ctx, id, text)
}

func (d *driver) Key(ctx context.Context, k mobile.Key) error {
	if k == mobile.KeyHideKeyboard {
		return d.life.hideKeyboard(ctx)
	}
	code, ok := keycodes[k]
	if !ok {
		return fmt.Errorf("the key %s is not known", k)
	}
	return d.life.key(ctx, code)
}

func (d *driver) Screenshot(ctx context.Context) ([]byte, error) {
	data, err := d.sess.Screenshot(ctx)
	if err != nil {
		if msg := strings.ToLower(err.Error()); strings.Contains(msg, "secure") {
			return nil, mobile.ErrSecure
		}
		return nil, err
	}
	return data, nil
}

// adbLife controls the app with adb.
type adbLife struct {
	a    *adb
	sess *webdriver.Session
	app  App
	// component is the activity that launch starts, found once.
	component string
	granted   bool
}

var componentPattern = regexp.MustCompile(`^[A-Za-z0-9_.]+/[A-Za-z0-9_.$]+$`)

func (l *adbLife) reset(ctx context.Context, override string) error {
	mode := l.app.resetMode()
	if override != "" {
		mode = override
	}
	if mode != config.ResetNone {
		if _, err := l.a.shell(ctx, "am", "force-stop", l.app.Package); err != nil {
			return err
		}
	}
	if mode == config.ResetClear {
		out, err := l.a.shell(ctx, "pm", "clear", l.app.Package)
		if err != nil || !strings.Contains(out, "Success") {
			if err == nil {
				err = errors.New(firstLine(out))
			}
			return fmt.Errorf("the data of the app could not be cleared: %w", err)
		}
	}
	// A clear takes the permissions back, so they are granted after each reset. With none the app keeps
	// its state, and the permissions are granted once.
	if mode == config.ResetNone && l.granted {
		return nil
	}
	l.granted = true
	perms := l.app.Permissions.Names
	if l.app.Permissions.All {
		var err error
		if perms, err = l.a.requestedPermissions(ctx, l.app.Package); err != nil {
			return err
		}
	}
	return l.a.grantPermissions(ctx, l.app.Package, perms, l.app.Permissions.All)
}

// containsFailure is true for the output of am and pm that says it failed, which they do with an exit
// status of 0 at times.
func containsFailure(out string) bool {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "Error") || strings.HasPrefix(l, "Exception") || strings.Contains(l, "Exception:") ||
			strings.HasPrefix(l, "Failure") || strings.Contains(l, "Unknown permission") || strings.Contains(l, "SecurityException") {
			return true
		}
	}
	return false
}

func (l *adbLife) launch(ctx context.Context) error {
	comp, err := l.launcher(ctx)
	if err != nil {
		return err
	}
	out, err := l.a.shell(ctx, "am", "start", "-W", "--activity-clear-task", "-n", comp, "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER")
	return withActivityHint(startResult("the app could not be started", out, err), l.app)
}

// launcher is the component to start: the activity of the service, or the launcher activity that the
// package manager gives for the package.
func (l *adbLife) launcher(ctx context.Context) (string, error) {
	if l.component != "" {
		return l.component, nil
	}
	comp := ""
	if act := l.app.Activity; act != "" {
		comp = l.app.Package + "/" + act
	} else {
		var err error
		if comp, err = l.a.launcherOf(ctx, l.app.Package); err != nil {
			return "", err
		}
		if comp == "" {
			return "", fmt.Errorf("the app %s has no launcher activity: name one with appActivity", l.app.Package)
		}
	}
	if !componentPattern.MatchString(comp) {
		return "", fmt.Errorf("the activity %q is not an activity name", comp)
	}
	l.component = comp
	return comp, nil
}

func (l *adbLife) open(ctx context.Context, link string) error {
	out, err := l.a.shell(ctx, "am", "start", "-W", "-a", "android.intent.action.VIEW", "-d", link, "-p", l.app.Package)
	return startResult("the link could not be opened", out, err)
}

// startResult reads the output of am start, which says Error and still exits with 0 at times.
func startResult(what, out string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if isFailureLine(strings.TrimSpace(line)) {
			return fmt.Errorf("%s: %s", what, failureLine(out))
		}
	}
	return nil
}

// withActivityHint adds to a failed start of a missing class what a name that starts with a dot hides:
// it resolves against the package of the app, and a build with an applicationIdSuffix has classes in
// another package.
func withActivityHint(err error, app App) error {
	if err == nil || !strings.HasPrefix(app.Activity, ".") || !activityMissing.MatchString(err.Error()) {
		return err
	}
	return fmt.Errorf("%w: appActivity %s is relative, so it resolves against the package %s, and the class may be in another package when the build has an applicationIdSuffix: name the full class, such as com.example.ui.MainActivity, or leave appActivity empty", err, app.Activity, app.Package)
}

var activityMissing = regexp.MustCompile(`Activity class \{[^}]*\} does not exist`)

func (l *adbLife) key(ctx context.Context, code int) error { return l.sess.PressKeycode(ctx, code) }

// hideKeyboard closes the keyboard when it is open, and does nothing when it is not: the back key
// would otherwise leave the screen of the app.
func (l *adbLife) hideKeyboard(ctx context.Context) error {
	out, err := l.a.shell(ctx, "dumpsys", "input_method")
	if err != nil {
		return fmt.Errorf("the keyboard could not be asked: %w", err)
	}
	if !strings.Contains(out, "mInputShown=true") {
		return nil
	}
	return l.sess.PressKeycode(ctx, keycodeBack)
}

// Clipboard reads the clipboard of the device through the session of the app. A recording holds that
// session, so it is how a read gets to the device while a recording goes on.
func (d *driver) Clipboard(ctx context.Context) (string, error) { return d.sess.Clipboard(ctx) }
