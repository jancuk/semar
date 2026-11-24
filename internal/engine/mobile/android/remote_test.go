package android_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

type remoteRig struct {
	t      *testing.T
	server *mobiletest.Server
	opts   android.Options
	env    map[string]string
	clk    *clock
}

func newRemote(t *testing.T, so mobiletest.Options, app android.App, env config.Android) *remoteRig {
	t.Helper()
	r := &remoteRig{t: t, env: map[string]string{}, clk: &clock{}}
	r.server = mobiletest.NewServer(t, so)
	env.ServerURL = r.server.URL
	r.opts = android.Options{
		Env: env, Apps: map[string]android.App{"shop": app}, RepoRoot: t.TempDir(),
		LookupEnv: func(k string) (string, bool) { v, ok := r.env[k]; return v, ok },
		Sleep:     r.clk.Sleep, Now: r.clk.Now,
		// Any program that adb would be is not reachable: serverUrl mode must not use adb.
		FindADB: func() (string, error) { t.Error("adb was looked for in serverUrl mode"); return "", nil },
		Run:     refuseRunner{t},
	}
	return r
}

type refuseRunner struct{ t *testing.T }

func (r refuseRunner) Output(context.Context, string, ...string) ([]byte, error) {
	r.t.Error("adb was run in serverUrl mode")
	return nil, nil
}
func (r refuseRunner) Quiet(context.Context, string, ...string) error {
	r.t.Error("adb was run in serverUrl mode")
	return nil
}
func (r refuseRunner) Start(context.Context, string, ...string) (android.Child, error) {
	r.t.Error("adb was started in serverUrl mode")
	return nil, nil
}

func (r *remoteRig) driver(p *android.Platform) mobile.Driver {
	r.t.Helper()
	d, err := p.Driver(context.Background(), "shop")
	if err != nil {
		r.t.Fatalf("Driver: %v", err)
	}
	return d
}

func TestRemoteCapabilities(t *testing.T) {
	apk, sum := writeFile(t, t.TempDir(), "shop.apk", "shop bytes")
	env := config.Android{Device: "pixel-8-farm", Capabilities: map[string]any{
		"appium:deviceName": "Pixel 8", "platformName": "iOS", "appium:appPackage": "evil.pkg", "appium:app": "bs://evil", "appium:noReset": false,
		"appium:options": map[string]any{"a": 1},
	}}
	r := newRemote(t, mobiletest.Options{}, android.App{Package: pkgShop, Activity: ".Main", APK: apk, APKDigest: sum[:40], Permissions: []string{"android.permission.CAMERA"}}, env)
	p := android.New(r.opts)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	d := r.driver(p)
	caps := r.server.Capabilities()
	want := map[string]any{
		"platformName": "Android", "appium:automationName": "UiAutomator2", "appium:appPackage": pkgShop, "appium:appActivity": ".Main",
		"appium:noReset": true, "appium:autoLaunch": false, "appium:newCommandTimeout": float64(300), "appium:udid": "pixel-8-farm",
		"appium:app": apk, "appium:enforceAppInstall": true, "appium:autoGrantPermissions": true, "appium:deviceName": "Pixel 8",
	}
	for k, v := range want {
		if caps[k] != v {
			t.Errorf("capability %s = %v, want %v", k, caps[k], v)
		}
	}
	if m, ok := caps["appium:options"].(map[string]any); !ok || m["a"] != float64(1) {
		t.Errorf("a capability that is a mapping: %v", caps["appium:options"])
	}
	if d.About().Device != "pixel-8-farm" || d.About().Package != pkgShop {
		t.Errorf("about = %+v", d.About())
	}
	if s := r.server.Settings(); s["waitForIdleTimeout"] != float64(0) || s["enableMultiWindows"] != true {
		t.Errorf("settings = %v", s)
	}
}

func TestRemoteCapabilitiesOfAnAppRef(t *testing.T) {
	r := newRemote(t, mobiletest.Options{}, android.App{Package: pkgShop, Ref: "bs://abc123"}, config.Android{})
	r.env["ANDROID_SERIAL"] = "from-env"
	p := android.New(r.opts)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	r.driver(p)
	caps := r.server.Capabilities()
	if caps["appium:app"] != "bs://abc123" || caps["appium:udid"] != "from-env" {
		t.Errorf("caps = %v", caps)
	}
	if _, has := caps["appium:enforceAppInstall"]; has {
		t.Error("enforceAppInstall without an apk")
	}
	if _, has := caps["appium:appActivity"]; has {
		t.Error("an empty activity was sent")
	}
	if _, has := caps["appium:autoGrantPermissions"]; has {
		t.Error("autoGrantPermissions without permissions")
	}
}

func TestRemoteLifecycleUsesTheSixMethods(t *testing.T) {
	r := newRemote(t, mobiletest.Options{}, android.App{Package: pkgShop, Reset: config.ResetClear}, config.Android{})
	p := android.New(r.opts)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	d := r.driver(p)
	ctx := context.Background()
	if err := d.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.Launch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.Open(ctx, "shop://cart/7"); err != nil {
		t.Fatal(err)
	}
	for _, k := range []mobile.Key{mobile.KeyBack, mobile.KeyEnter, mobile.KeyHideKeyboard} {
		if err := d.Key(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	got := r.server.ExecutedScripts()
	wantScripts := []string{"mobile: terminateApp", "mobile: clearApp", "mobile: activateApp", "mobile: deepLink", "mobile: pressKey", "mobile: pressKey", "mobile: hideKeyboard"}
	if len(got) != len(wantScripts) {
		t.Fatalf("scripts = %+v", got)
	}
	for i, w := range wantScripts {
		if got[i].Script != w {
			t.Errorf("script %d = %s, want %s", i, got[i].Script, w)
		}
	}
	if got[0].Args["appId"] != pkgShop || got[1].Args["appId"] != pkgShop || got[2].Args["appId"] != pkgShop {
		t.Errorf("args = %v %v %v", got[0].Args, got[1].Args, got[2].Args)
	}
	if got[3].Args["url"] != "shop://cart/7" || got[3].Args["package"] != pkgShop {
		t.Errorf("deepLink args = %v", got[3].Args)
	}
	if got[4].Args["keycode"] != float64(4) || got[5].Args["keycode"] != float64(66) {
		t.Errorf("pressKey args = %v %v", got[4].Args, got[5].Args)
	}
	// The bare endpoint of the UiAutomator2 server is not used.
	if r.server.Count("POST /appium/device/press_keycode") != 0 {
		t.Error("press_keycode was used with a server that has execute")
	}
}

func TestRemoteResetRestartTerminatesOnlyAndNoneDoesNothing(t *testing.T) {
	for mode, want := range map[string][]string{"": {"mobile: terminateApp"}, "restart": {"mobile: terminateApp"}, "none": nil} {
		r := newRemote(t, mobiletest.Options{}, android.App{Package: pkgShop, Reset: mode}, config.Android{})
		p := android.New(r.opts)
		d := r.driver(p)
		if err := d.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range r.server.ExecutedScripts() {
			got = append(got, e.Script)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("reset %q ran %v, want %v", mode, got, want)
		}
		_ = p.Close(context.Background())
	}
}

func TestAServerWithoutAMethodSaysSo(t *testing.T) {
	r := newRemote(t, mobiletest.Options{NoExecute: true}, android.App{Package: pkgShop}, config.Android{})
	p := android.New(r.opts)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	d := r.driver(p)
	ctx := context.Background()
	for name, f := range map[string]func() error{
		"mobile: terminateApp": func() error { return d.Reset(ctx) },
		"mobile: activateApp":  func() error { return d.Launch(ctx) },
		"mobile: deepLink":     func() error { return d.Open(ctx, "shop://x") },
		"mobile: pressKey":     func() error { return d.Key(ctx, mobile.KeyBack) },
		"mobile: hideKeyboard": func() error { return d.Key(ctx, mobile.KeyHideKeyboard) },
	} {
		if err := f(); err == nil || err.Error() != "the server does not support "+name {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	// A server that knows the method and fails it gives its own error.
	r2 := newRemote(t, mobiletest.Options{}, android.App{Package: pkgShop}, config.Android{})
	r2.server.OnExecute(func(string, map[string]any) (any, *mobiletest.Failure) {
		return nil, &mobiletest.Failure{Status: 500, Code: "unknown error", Message: "The app is not installed"}
	})
	p2 := android.New(r2.opts)
	t.Cleanup(func() { _ = p2.Close(context.Background()) })
	err := r2.driver(p2).Launch(ctx)
	if err == nil || !strings.Contains(err.Error(), "unknown error: The app is not installed") || strings.Contains(err.Error(), "does not support") {
		t.Errorf("error = %v", err)
	}
}

func TestRemoteSessionsPerServiceAndCleanup(t *testing.T) {
	r := newRemote(t, mobiletest.Options{}, android.App{Package: pkgShop}, config.Android{})
	r.opts.Apps["admin"] = android.App{Package: "com.example.admin"}
	p := android.New(r.opts)
	d1 := r.driver(p)
	if again, _ := p.Driver(context.Background(), "shop"); again != d1 {
		t.Error("a second call made a new driver")
	}
	if _, err := p.Driver(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	if r.server.Sessions() != 2 {
		t.Errorf("sessions = %d", r.server.Sessions())
	}
	if r.server.Count("GET /status") != 1 {
		t.Errorf("status was asked %d times", r.server.Count("GET /status"))
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The fake has one session id, so both deletes reach it.
	if r.server.Deleted() != 2 {
		t.Errorf("deleted = %d", r.server.Deleted())
	}
	if err := p.Close(context.Background()); err != nil || r.server.Deleted() != 2 {
		t.Errorf("a second Close: %v, %d", err, r.server.Deleted())
	}
}

func TestRemoteCredentialsComeFromTheEnvironmentAndNeverFromTheMessage(t *testing.T) {
	const key = "farm-key-very-secret"
	r := newRemote(t, mobiletest.Options{User: "farm-user", Key: key}, android.App{Package: pkgShop}, config.Android{ServerUser: "env:FARM_USER", ServerKey: "env:FARM_KEY"})
	// Not set: the platform says which variable.
	p := android.New(r.opts)
	_, err := p.Driver(context.Background(), "shop")
	if un := unavailable(t, err); !strings.Contains(un.Reason, "serverUser names the variable FARM_USER, which is not set") {
		t.Errorf("reason = %q", un.Reason)
	}
	r.env["FARM_USER"] = "farm-user"
	r.env["FARM_KEY"] = "not-the-key-xyz"
	p = android.New(r.opts)
	_, err = p.Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if strings.Contains(un.Reason, "not-the-key-xyz") || strings.Contains(un.Reason, key) || !strings.Contains(un.Reason, "401") {
		t.Errorf("reason = %q", un.Reason)
	}
	r.env["FARM_KEY"] = key
	p = android.New(r.opts)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	if _, err := p.Driver(context.Background(), "shop"); err != nil {
		t.Fatal(err)
	}
	for _, req := range r.server.Requests() {
		if strings.Contains(req.Body, key) || strings.Contains(req.Path, key) {
			t.Errorf("the key is in a request: %v", req)
		}
	}
	// A reference that is not env:NAME.
	r.opts.Env.ServerUser = "plain"
	if _, err := android.New(r.opts).Driver(context.Background(), "shop"); err == nil || !strings.Contains(err.Error(), "must be a reference such as env:FARM_USER") {
		t.Errorf("error = %v", err)
	}
}

func TestARemoteServerThatIsNotReadyOrNotThere(t *testing.T) {
	r := newRemote(t, mobiletest.Options{NotReady: true}, android.App{Package: pkgShop}, config.Android{})
	_, err := android.New(r.opts).Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "is not ready") || !strings.Contains(un.Reason, "fake server") || strings.Contains(un.Reason, "http://") {
		t.Errorf("reason = %q", un.Reason)
	}
	r2 := newRemote(t, mobiletest.Options{}, android.App{Package: pkgShop}, config.Android{})
	r2.server.Close()
	_, err = android.New(r2.opts).Driver(context.Background(), "shop")
	un = unavailable(t, err)
	if !strings.Contains(un.Reason, "cannot be used") || !strings.Contains(un.Reason, "127.0.0.1:") {
		t.Errorf("reason = %q", un.Reason)
	}
	// A failed session start is Unavailable, and is not tried again at the next step.
	r3 := newRemote(t, mobiletest.Options{}, android.App{Package: pkgShop}, config.Android{})
	r3.server.Fail("POST /session", mobiletest.Failure{Status: 500, Code: "session not created", Message: "no device is free"})
	p := android.New(r3.opts)
	_, err = p.Driver(context.Background(), "shop")
	un = unavailable(t, err)
	if !strings.Contains(un.Reason, "did not start a session") || !strings.Contains(un.Reason, "no device is free") {
		t.Errorf("reason = %q", un.Reason)
	}
	n := r3.server.Count("POST /session")
	if _, err := p.Driver(context.Background(), "shop"); err == nil || r3.server.Count("POST /session") != n {
		t.Error("the session was asked for again")
	}
}

func TestARemoteAPKThatChangedIsRefused(t *testing.T) {
	apk, sum := writeFile(t, t.TempDir(), "shop.apk", "shop bytes")
	r := newRemote(t, mobiletest.Options{}, android.App{Package: pkgShop, APK: apk, APKDigest: strings.Repeat("0", 40)}, config.Android{})
	_ = sum
	_, err := android.New(r.opts).Driver(context.Background(), "shop")
	if un := unavailable(t, err); !strings.Contains(un.Reason, "changed after the build of this run was read") {
		t.Errorf("reason = %q", un.Reason)
	}
	if r.server.Sessions() != 0 {
		t.Error("a session was started for a file that is not the build")
	}
}

func TestAWebdriverServerThatSpeaksTheLegacyDialectWorks(t *testing.T) {
	r := newRemote(t, mobiletest.Options{Legacy: true}, android.App{Package: pkgShop}, config.Android{})
	p := android.New(r.opts)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	d := r.driver(p)
	r.server.SetSource(mobiletest.Hierarchy(mobiletest.Widget("android.widget.EditText", nil, "[0,0][100,100]")))
	if _, err := d.Source(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Touch(context.Background(), mobile.Gesture{From: mobile.Point{X: 1, Y: 1}, To: mobile.Point{X: 1, Y: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Type(context.Background(), "x"); err != nil {
		t.Errorf("type with an ELEMENT id: %v", err)
	}
}

func (r refuseRunner) Stream(context.Context, string, ...string) (io.ReadCloser, func() error, error) {
	r.t.Error("adb was streamed in serverUrl mode")
	return nil, nil, nil
}
