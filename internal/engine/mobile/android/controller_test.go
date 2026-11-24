package android_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

// wdExtra answers the endpoints that the fake server does not have (orientation, clipboard, window
// rectangle, location, screen recording), and passes everything else to the fake server.
type wdExtra struct {
	URL string

	mu          sync.Mutex
	orientation string
	clipboard   string
	calls       []string
	bodies      []string
}

func newWDExtra(t *testing.T, target string) *wdExtra {
	t.Helper()
	u, _ := url.Parse(target)
	proxy := httputil.NewSingleHostReverseProxy(u)
	w := &wdExtra{orientation: "PORTRAIT"}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		_, suffix, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/session/"), "/")
		key := r.Method + " /" + suffix
		reply := func(v any) {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(map[string]any{"value": v})
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		switch key {
		case "GET /orientation":
			w.record(key, "")
			reply(w.orientation)
		case "POST /orientation":
			var in struct{ Orientation string }
			_ = json.Unmarshal(body, &in)
			w.orientation = in.Orientation
			w.record(key, string(body))
			reply(nil)
		case "POST /appium/device/get_clipboard":
			w.record(key, string(body))
			reply(base64.StdEncoding.EncodeToString([]byte(w.clipboard)))
		case "POST /appium/device/set_clipboard":
			var in struct{ Content string }
			_ = json.Unmarshal(body, &in)
			b, _ := base64.StdEncoding.DecodeString(in.Content)
			w.clipboard = string(b)
			w.record(key, string(body))
			reply(nil)
		case "GET /window/rect":
			w.record(key, "")
			reply(map[string]any{"width": 1080, "height": 2400, "x": 0, "y": 0})
		case "POST /location", "POST /appium/start_recording_screen":
			w.record(key, string(body))
			reply(nil)
		case "POST /appium/stop_recording_screen":
			w.record(key, string(body))
			reply(base64.StdEncoding.EncodeToString([]byte("REMOTE-MP4")))
		default:
			w.mu.Unlock()
			proxy.ServeHTTP(rw, r)
			w.mu.Lock()
		}
	}))
	t.Cleanup(srv.Close)
	w.URL = srv.URL
	return w
}

func (w *wdExtra) record(key, body string) {
	w.calls = append(w.calls, key)
	w.bodies = append(w.bodies, body)
}

func (w *wdExtra) Calls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.calls)
}

func (w *wdExtra) Body(call string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := len(w.calls) - 1; i >= 0; i-- {
		if w.calls[i] == call {
			return w.bodies[i]
		}
	}
	return ""
}

type ctlRig struct {
	*rig
	wd *wdExtra
}

// newCtlRig is the rig of a local device whose server also answers the extra endpoints.
func newCtlRig(t *testing.T, mods ...rigOption) *ctlRig {
	t.Helper()
	r := newRig(t, mods...)
	wd := newWDExtra(t, r.server.URL)
	u, _ := url.Parse(wd.URL)
	r.adb.Set("port", u.Port())
	return &ctlRig{rig: r, wd: wd}
}

func (r *ctlRig) controller(p *android.Platform) *android.Controller {
	r.t.Helper()
	c, err := p.Controller(context.Background())
	if err != nil {
		r.t.Fatalf("Controller: %v", err)
	}
	return c
}

func (r *ctlRig) start(t *testing.T) (*android.Platform, *android.Controller) {
	t.Helper()
	p := r.platform()
	return p, r.controller(p)
}

// shellHas reports whether a shell command of the device was sent, as words.
func shellHas(r *ctlRig, words ...string) bool {
	for _, w := range r.adb.ShellWords() {
		if slices.Equal(w, words) {
			return true
		}
	}
	return false
}

func shellPrefixed(r *ctlRig, words ...string) bool {
	for _, w := range r.adb.ShellWords() {
		if len(w) >= len(words) && slices.Equal(w[:len(words)], words) {
			return true
		}
	}
	return false
}

func TestControllerStartsOnlyTheDeviceAndHoldsItsLease(t *testing.T) {
	r := newCtlRig(t)
	p, c := r.start(t)
	want := []string{"start-server", "devices -l", "shell 'getprop' 'sys.boot_completed'"}
	if got := r.adb.Log(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("adb log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if r.server.Sessions() != 0 {
		t.Error("the server was started by Controller")
	}
	if c.Serial() != emulator || !c.Emulator() || c.Remote() {
		t.Errorf("serial %q, emulator %v, remote %v", c.Serial(), c.Emulator(), c.Remote())
	}
	if again, err := p.Controller(context.Background()); err != nil || again != c {
		t.Errorf("a second call gave %v, %v", again, err)
	}
	// Another platform on the same device is refused while the controller holds the lease.
	other := android.New(r.opts)
	t.Cleanup(func() { _ = other.Close(context.Background()) })
	_, err := other.Controller(context.Background())
	if un := unavailable(t, err); !strings.Contains(un.Reason, "used by another semar run") {
		t.Errorf("reason = %q", un.Reason)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	third := android.New(r.opts)
	t.Cleanup(func() { _ = third.Close(context.Background()) })
	if _, err := third.Controller(context.Background()); err != nil {
		t.Errorf("after the lease was released: %v", err)
	}
	if _, err := p.Controller(context.Background()); err == nil {
		t.Error("a closed platform gave a controller")
	}
}

func TestControllerFailsLikeADriverWhenThereIsNoDevice(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) { cfg.Devices = nil })
	p := r.platform()
	_, err := p.Controller(context.Background())
	if un := unavailable(t, err); !strings.Contains(un.Reason, "no device is connected") {
		t.Errorf("reason = %q", un.Reason)
	}
}

func TestControllerDeviceStartsAgainAfterAFailure(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) { cfg.NotBooted = true })
	p := r.platform()
	_, err := p.Controller(context.Background())
	if un := unavailable(t, err); !strings.Contains(un.Reason, "has not finished starting") {
		t.Errorf("reason = %q", un.Reason)
	}
	r.adb.Unset("notbooted")
	if _, err := p.Controller(context.Background()); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if r.adb.Count("devices") != 1 {
		t.Errorf("the device was chosen twice: %v", r.adb.Log())
	}
}

func TestControllerStartsTheServerOnTheFirstScreenCall(t *testing.T) {
	r := newCtlRig(t)
	_, c := r.start(t)
	ctx := context.Background()
	tree, err := c.Source(ctx)
	if err != nil || tree.Root == nil {
		t.Fatalf("Source: %v", err)
	}
	if r.server.Sessions() != 1 || !r.adb.Contains("forward", "tcp:0", "tcp:6790") {
		t.Errorf("sessions %d, adb %v", r.server.Sessions(), r.adb.Log())
	}
	caps := r.server.Capabilities()
	for _, k := range []string{"appium:appPackage", "appium:app"} {
		if _, has := caps[k]; has {
			t.Errorf("the session names %s", k)
		}
	}
	again, err := c.Source(ctx)
	if err != nil || again != tree {
		t.Errorf("a source with the same bytes gave another tree (%v)", err)
	}
	if r.server.Sessions() != 1 {
		t.Error("a second session was opened")
	}
	shot, err := c.Screenshot(ctx)
	if err != nil || len(shot) == 0 {
		t.Errorf("Screenshot: %d bytes, %v", len(shot), err)
	}
	g := mobile.Gesture{From: mobile.Point{X: 10, Y: 20}, To: mobile.Point{X: 10, Y: 20}}
	if err := c.Touch(ctx, g); err != nil {
		t.Fatal(err)
	}
	if taps := r.server.Taps(); len(taps) != 1 || taps[0] != [2]int{10, 20} {
		t.Errorf("taps = %v", taps)
	}
}

func TestControllerTouchDoubleTapSendsTwoTaps(t *testing.T) {
	r := newCtlRig(t)
	_, c := r.start(t)
	p := mobile.Point{X: 30, Y: 40}
	if err := c.Touch(context.Background(), mobile.Gesture{From: p, To: p, Taps: 2}); err != nil {
		t.Fatal(err)
	}
	taps := r.server.Taps()
	if len(taps) == 0 {
		t.Fatal("the server saw no tap")
	}
	for _, tap := range taps {
		if tap != [2]int{30, 40} {
			t.Errorf("taps = %v", taps)
		}
	}
	// A double tap with a hold is not a double tap: it is one long press.
	before := len(taps)
	if err := c.Touch(context.Background(), mobile.Gesture{From: p, To: p, Taps: 2, Hold: 500 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if got := len(r.server.Taps()); got != before+1 {
		t.Errorf("a held gesture made %d taps", got-before)
	}
}

func TestControllerSecureScreen(t *testing.T) {
	r := newCtlRig(t)
	_, c := r.start(t)
	r.server.Fail("GET /screenshot", mobiletest.Failure{Status: 500, Code: "unknown error", Message: "Screenshot failed: the window is secure"})
	if _, err := c.Screenshot(context.Background()); !errors.Is(err, mobile.ErrSecure) {
		t.Errorf("err = %v", err)
	}
}

func TestControllerServerThatDoesNotStartIsStoppedAndTriedAgain(t *testing.T) {
	r := newCtlRig(t, func(r *rig, cfg *mobiletest.ADBConfig) {
		cfg.InstrumentExits = true
		r.serverOpts.NotReady = true
	})
	_, c := r.start(t)
	_, err := c.Source(context.Background())
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "stopped as it started") {
		t.Errorf("reason = %q", un.Reason)
	}
	if !r.adb.Contains("forward", "--remove", "tcp:"+strconv.Itoa(portOf(t, r.wd.URL))) {
		t.Errorf("the forward was left: %v", r.adb.Log())
	}
	r.adb.Unset("instrument_exits")
	r.server.SetReady(true)
	if _, err := c.Source(context.Background()); err != nil {
		t.Fatalf("second try: %v", err)
	}
}

func portOf(t *testing.T, raw string) int {
	t.Helper()
	u, _ := url.Parse(raw)
	n, _ := strconv.Atoi(u.Port())
	return n
}

func TestControllerTypeUsesTheServerThenAdb(t *testing.T) {
	r := newCtlRig(t)
	_, c := r.start(t)
	ctx := context.Background()
	// No field has the focus yet, so the text goes through adb input, which has no %s for a space.
	if err := c.TypeFocused(ctx, "a b", true); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "input", "text", "a%sb") {
		t.Errorf("no input text: %v", r.adb.Shell())
	}
	if !shellPrefixed(r, "input", "keyevent", "123", "67") {
		t.Errorf("no replace by keys: %v", r.adb.Shell())
	}
	// A tap gives a field the focus, and then the server types.
	if err := c.Touch(ctx, mobile.Gesture{From: mobile.Point{X: 1, Y: 2}, To: mobile.Point{X: 1, Y: 2}}); err != nil {
		t.Fatal(err)
	}
	before := len(r.adb.Shell())
	if err := c.TypeFocused(ctx, "hello", true); err != nil {
		t.Fatal(err)
	}
	if typed := r.server.Typed(); len(typed) != 1 || typed[0] != "hello" {
		t.Errorf("typed = %v", typed)
	}
	if r.server.Count("POST /element/e1/clear") != 1 {
		t.Errorf("not cleared: %v", r.server.Calls())
	}
	if len(r.adb.Shell()) != before {
		t.Error("adb was used though the server could type")
	}
	// Without replace the field is not cleared.
	if err := c.TypeFocused(ctx, "!", false); err != nil {
		t.Fatal(err)
	}
	if r.server.Count("POST /element/e1/clear") != 1 {
		t.Error("a type without replace cleared the field")
	}
	r.adb.AssertQuoted(t)
}

func TestControllerTypeFallbackRefusesWhatAdbCannotType(t *testing.T) {
	r := newCtlRig(t)
	_, c := r.start(t)
	for _, text := range []string{"héllo", "100%s", "line\nbreak"} {
		err := c.TypeFocused(context.Background(), text, false)
		if err == nil || !strings.Contains(err.Error(), "no field has the focus") {
			t.Errorf("%q: err = %v", text, err)
		}
	}
	if shellPrefixed(r, "input", "text") {
		t.Error("a text that adb cannot type was sent")
	}
}

func TestControllerKeyFallsBackToAdb(t *testing.T) {
	r := newCtlRig(t)
	_, c := r.start(t)
	ctx := context.Background()
	if err := c.Key(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if kc := r.server.Keycodes(); len(kc) != 1 || kc[0] != 4 {
		t.Errorf("keycodes = %v", kc)
	}
	r.server.Fail("POST /appium/device/press_keycode", mobiletest.Failure{Status: 500, Code: "unknown error", Message: "no"})
	if err := c.Key(ctx, 66); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "input", "keyevent", "66") {
		t.Errorf("no keyevent: %v", r.adb.Shell())
	}
	if err := c.Key(ctx, -1); err == nil {
		t.Error("a negative key code")
	}
}

func TestControllerOrientationIsPutBackOnClose(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'settings' 'get' 'system' 'accelerometer_rotation'", Out: "1\n"},
			{Match: "'settings' 'get' 'system' 'user_rotation'", Out: "null\n"},
		}
	})
	p, c := r.start(t)
	ctx := context.Background()
	if got, err := c.Orientation(ctx); err != nil || got != "portrait" {
		t.Fatalf("Orientation = %q, %v", got, err)
	}
	if err := c.SetOrientation(ctx, "Landscape"); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Orientation(ctx); got != "landscape" {
		t.Errorf("after set: %q", got)
	}
	if body := r.wd.Body("POST /orientation"); !strings.Contains(body, `"LANDSCAPE"`) {
		t.Errorf("body = %s", body)
	}
	if err := c.SetOrientation(ctx, "upside-down"); err == nil {
		t.Error("an orientation that is not one")
	}
	// A second set does not read the settings again.
	if err := c.SetOrientation(ctx, "portrait"); err != nil {
		t.Fatal(err)
	}
	if n := countShell(r, "settings", "get"); n != 2 {
		t.Errorf("%d reads of the settings, want 2", n)
	}
	if shellPrefixed(r, "settings", "put") {
		t.Fatal("the settings were written before Close")
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "settings", "put", "system", "accelerometer_rotation", "1") {
		t.Errorf("the rotation was not put back: %v", r.adb.Shell())
	}
	if shellPrefixed(r, "settings", "put", "system", "user_rotation") {
		t.Error("a setting that was null was written")
	}
	r.adb.AssertQuoted(t)
}

func countShell(r *ctlRig, prefix ...string) int {
	n := 0
	for _, w := range r.adb.ShellWords() {
		if len(w) >= len(prefix) && slices.Equal(w[:len(prefix)], prefix) {
			n++
		}
	}
	return n
}

func TestControllerClipboard(t *testing.T) {
	r := newCtlRig(t)
	_, c := r.start(t)
	ctx := context.Background()
	if err := c.SetClipboard(ctx, "pa$$ word 'x'"); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Clipboard(ctx); err != nil || got != "pa$$ word 'x'" {
		t.Errorf("Clipboard = %q, %v", got, err)
	}
	if err := c.SetClipboard(ctx, strings.Repeat("x", 70<<10)); err == nil {
		t.Error("a huge clipboard was sent")
	}
}

func TestControllerApps(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'pm' 'list' 'packages' '-3'", Out: "package:com.example.shop\npackage:com.a.b\n"},
			{Match: "'pm' 'list' 'packages'", Out: "package:android\npackage:com.example.shop\npackage:com.google.android.gms\n"},
			{Match: "'dumpsys' 'activity' 'activities'", Out: "  topResumedActivity=ActivityRecord{1a2b3c u0 com.example.shop/.MainActivity t12}\n"},
		}
	})
	_, c := r.start(t)
	ctx := context.Background()
	apps, err := c.ListApps(ctx, false)
	if err != nil || !slices.Equal(apps, []string{"com.a.b", "com.example.shop"}) {
		t.Errorf("third party = %v, %v", apps, err)
	}
	all, err := c.ListApps(ctx, true)
	if err != nil || len(all) != 2 || !slices.Contains(all, "com.google.android.gms") {
		t.Errorf("all = %v, %v", all, err)
	}
	pkg, act, err := c.Foreground(ctx)
	if err != nil || pkg != pkgShop || act != "com.example.shop.MainActivity" {
		t.Errorf("Foreground = %q %q %v", pkg, act, err)
	}
	if err := c.TerminateApp(ctx, pkgShop); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "am", "force-stop", pkgShop) {
		t.Errorf("no force-stop: %v", r.adb.Shell())
	}
	if err := c.ClearAppData(ctx, pkgShop); err != nil || !shellHas(r, "pm", "clear", pkgShop) {
		t.Errorf("ClearAppData: %v", err)
	}
	if err := c.UninstallApp(ctx, pkgShop); err != nil || !r.adb.Contains("uninstall", pkgShop) {
		t.Errorf("UninstallApp: %v", err)
	}
	for _, pkg := range []string{serverPkg, testPkg, "io.appium.uiautomator2.server.other"} {
		for name, fn := range map[string]func(context.Context, string) error{"uninstall": c.UninstallApp, "clear": c.ClearAppData, "terminate": c.TerminateApp} {
			if err := fn(ctx, pkg); err == nil || !strings.Contains(err.Error(), "not touched") {
				t.Errorf("%s %s: err = %v", name, pkg, err)
			}
		}
	}
	for _, bad := range []string{"", "x", "a b.c", "a.b;reboot", "../x"} {
		if err := c.UninstallApp(ctx, bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	r.adb.AssertQuoted(t)
}

func TestControllerForegroundOfALauncherIsEmpty(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{{Match: "'dumpsys' 'activity' 'activities'", Out: "  topResumedActivity=ActivityRecord{1 u0 com.google.android.apps.nexuslauncher/.NexusLauncherActivity t1}\n"}}
	})
	_, c := r.start(t)
	pkg, act, err := c.Foreground(context.Background())
	if err != nil || pkg != "" || act != "" {
		t.Errorf("Foreground = %q %q %v", pkg, act, err)
	}
}

func TestControllerLaunchApp(t *testing.T) {
	r := newCtlRig(t)
	_, c := r.start(t)
	ctx := context.Background()
	err := c.LaunchApp(ctx, android.LaunchOptions{
		Package: pkgShop, Locales: []string{"id-ID", "en-US"},
		Arguments: []string{"promo=it's $(reboot)", "debug.flag=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "cmd", "locale", "set-app-locales", pkgShop, "--user", "current", "--locales", "id-ID,en-US") {
		t.Errorf("no locale: %v", r.adb.Shell())
	}
	want := []string{"am", "start", "-W", "-n", "com.example.shop/.MainActivity", "--es", "promo", "it's $(reboot)", "--es", "debug.flag", "1"}
	if !shellHas(r, want...) {
		t.Errorf("no am start of %v in %v", want, r.adb.Shell())
	}
	// The value with a quote travelled as one word.
	for _, c := range r.adb.Shell() {
		if strings.Contains(c, "promo") && !strings.Contains(c, `'it'\''s $(reboot)'`) {
			t.Errorf("the value was not quoted: %s", c)
		}
	}
	r.adb.AssertQuoted(t)

	for name, o := range map[string]android.LaunchOptions{
		"package":  {Package: "bad;pkg"},
		"locale":   {Package: pkgShop, Locales: []string{"en_US"}},
		"key":      {Package: pkgShop, Arguments: []string{"a b=c"}},
		"no equal": {Package: pkgShop, Arguments: []string{"abc"}},
		"too many": {Package: pkgShop, Locales: make([]string, 11)},
	} {
		n := len(r.adb.Shell())
		if err := c.LaunchApp(ctx, o); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if len(r.adb.Shell()) != n {
			t.Errorf("%s: a command was sent for a bad request", name)
		}
	}
}

func TestControllerLaunchAppLocaleNeedsAndroid13(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) { cfg.SDK = "32" })
	_, c := r.start(t)
	err := c.LaunchApp(context.Background(), android.LaunchOptions{Package: pkgShop, Locales: []string{"en-US"}})
	if !errors.Is(err, android.ErrUnsupported) {
		t.Errorf("err = %v", err)
	}
	if shellPrefixed(r, "am", "start") {
		t.Error("the app was started")
	}
	// Without a locale the old device launches.
	if err := c.LaunchApp(context.Background(), android.LaunchOptions{Package: pkgShop}); err != nil {
		t.Errorf("no locale: %v", err)
	}
}

func TestControllerLaunchAppReportsWhatAmSays(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.StartOutput = "Error type 3\nError: Activity class {com.example.shop/.MainActivity} does not exist.\n"
	})
	_, c := r.start(t)
	err := c.LaunchApp(context.Background(), android.LaunchOptions{Package: pkgShop})
	if err == nil || !strings.Contains(err.Error(), "could not be started") {
		t.Errorf("err = %v", err)
	}
}

func TestControllerInstallAPK(t *testing.T) {
	dir := t.TempDir()
	good, _ := writeFile(t, dir, "shop.apk", "PK\x03\x04 apk bytes")
	notZip, _ := writeFile(t, dir, "bad.apk", "not an apk")
	txt, _ := writeFile(t, dir, "shop.txt", "PK\x03\x04")
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.InstallSeq = map[string][]string{"clash.apk": {"1|Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE: signatures do not match]"}, "full.apk": {"1|Failure [INSTALL_FAILED_INSUFFICIENT_STORAGE]"}}
	})
	_, c := r.start(t)
	ctx := context.Background()
	if err := c.InstallAPK(ctx, good); err != nil {
		t.Fatal(err)
	}
	if !r.adb.Contains("install", "-r", "-t", good) {
		t.Errorf("adb log: %v", r.adb.Log())
	}
	for _, f := range []string{"-d"} {
		for _, l := range r.adb.Log() {
			if strings.Contains(l, " "+f+" ") {
				t.Errorf("install used %s: %s", f, l)
			}
		}
	}
	if err := c.InstallAPK(ctx, notZip); err == nil || !strings.Contains(err.Error(), "not an APK") {
		t.Errorf("not a zip: %v", err)
	}
	if err := c.InstallAPK(ctx, txt); err == nil || !strings.Contains(err.Error(), "not an .apk") {
		t.Errorf("not .apk: %v", err)
	}
	if err := c.InstallAPK(ctx, filepath.Join(dir, "missing.apk")); err == nil {
		t.Error("a file that is not there")
	}
	if err := c.InstallAPK(ctx, dir); err == nil {
		t.Error("a directory")
	}
	if err := c.InstallAPK(ctx, "../escape.apk"); err == nil {
		t.Error("a relative path out of the repository")
	}
	link := filepath.Join(dir, "link.apk")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	if err := c.InstallAPK(ctx, link); err == nil {
		t.Error("a link")
	}
	for name, want := range map[string]string{"clash.apk": "signed with another key", "full.apk": "INSUFFICIENT_STORAGE"} {
		p, _ := writeFile(t, dir, name, "PK\x03\x04")
		err := c.InstallAPK(ctx, p)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if r.adb.Count("uninstall") != 0 {
		t.Error("an install that clashed uninstalled the app")
	}
}

func TestControllerScreenSizeFoldAndURL(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'wm' 'size'", Out: "Physical size: 1080x2400\nOverride size: 1080x1920\n"},
			{Match: "'wm' 'density'", Out: "Physical density: 420\n"},
			{Match: "'cmd' 'device_state' 'print-states'", Out: "Supported states: [\n  DeviceState{identifier=0, name='CLOSED', app_accessible=true},\n  DeviceState{identifier=1, name='HALF_OPENED', app_accessible=true},\n  DeviceState{identifier=2, name='OPENED', app_accessible=true},\n]\n"},
		}
	})
	p, c := r.start(t)
	ctx := context.Background()
	got, err := c.ScreenSize(ctx)
	if err != nil || got != (android.ScreenInfo{Width: 1080, Height: 1920, Density: 420}) {
		t.Errorf("ScreenSize = %+v, %v", got, err)
	}
	if err := c.Fold(ctx, "half-open"); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "cmd", "device_state", "state", "1") {
		t.Errorf("no state: %v", r.adb.Shell())
	}
	if err := c.Fold(ctx, "sideways"); err == nil {
		t.Error("a posture that is not one")
	}
	if err := c.OpenURL(ctx, "https://example.com/a?b=1&c='2'"); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "am", "start", "-W", "-a", "android.intent.action.VIEW", "-d", "https://example.com/a?b=1&c='2'") {
		t.Errorf("no VIEW: %v", r.adb.Shell())
	}
	for _, bad := range []string{"", "example.com", "https://x\ny", "javascript", strings.Repeat("a", 3000)} {
		if err := c.OpenURL(ctx, bad); err == nil {
			t.Errorf("%q was opened", bad)
		}
	}
	if shellPrefixed(r, "cmd", "device_state", "state", "reset") {
		t.Fatal("the state was reset before Close")
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "cmd", "device_state", "state", "reset") {
		t.Errorf("the fold was not reset on Close: %v", r.adb.Shell())
	}
	r.adb.AssertQuoted(t)
}

func TestControllerFoldOnAPhoneIsUnsupported(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{{Match: "'cmd' 'device_state' 'print-states'", Out: "Supported states: []\n"}}
	})
	p, c := r.start(t)
	if err := c.Fold(context.Background(), "folded"); !errors.Is(err, android.ErrUnsupported) {
		t.Errorf("err = %v", err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if shellPrefixed(r, "cmd", "device_state", "state", "reset") {
		t.Error("a state that was never set was reset")
	}
}

func TestControllerScreenSizeFallsBackToTheTree(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{{Match: "'wm' ", RC: 1, Err: "cmd: Can't find service: window"}}
	})
	_, c := r.start(t)
	r.server.SetSource(`<hierarchy width="720" height="1280"><android.widget.FrameLayout bounds="[0,0][720,1280]"/></hierarchy>`)
	got, err := c.ScreenSize(context.Background())
	if err != nil || got.Width != 720 || got.Height != 1280 || got.Density != 0 {
		t.Errorf("ScreenSize = %+v, %v", got, err)
	}
}

func TestControllerLocation(t *testing.T) {
	r := newCtlRig(t)
	_, c := r.start(t)
	ctx := context.Background()
	if err := c.SetLocation(ctx, -6.2, 106.816666); err != nil {
		t.Fatal(err)
	}
	// geo fix takes the longitude first.
	if !r.adb.Contains("emu", "geo", "fix", "106.816666", "-6.200000") {
		t.Errorf("adb log: %v", r.adb.Log())
	}
	for _, bad := range [][2]float64{{91, 0}, {0, 181}, {-91, 0}} {
		if err := c.SetLocation(ctx, bad[0], bad[1]); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	if err := c.ClearLocation(ctx); !errors.Is(err, android.ErrUnsupported) {
		t.Errorf("ClearLocation = %v", err)
	}

	phone := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Devices = []string{"R58M123ABC\tdevice usb:1-1 product:x model:Pixel_8 device:y transport_id:2"}
	})
	phone.env["ANDROID_SERIAL"] = "R58M123ABC"
	_, pc := phone.start(t)
	if pc.Emulator() {
		t.Error("a phone is an emulator")
	}
	if err := pc.SetLocation(ctx, 1, 2); !errors.Is(err, android.ErrUnsupported) {
		t.Errorf("a phone: %v", err)
	}
	if phone.adb.Count("emu") != 0 {
		t.Error("emu was sent to a phone")
	}
}

func TestControllerLogsDump(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'pidof' 'com.example.shop'", Out: "5678\n"},
			{Match: "'logcat' '-d'", Out: testdata(t, "logcat_threadtime.txt")},
		}
	})
	_, c := r.start(t)
	ctx := context.Background()
	all, err := c.Logs(ctx, android.LogQuery{})
	if err != nil || len(all) != 7 {
		t.Fatalf("all = %d, %v", len(all), err)
	}
	if !shellHas(r, "logcat", "-d", "-v", "threadtime", "-t", "100") {
		t.Errorf("no dump of 100: %v", r.adb.Shell())
	}
	last, err := c.Logs(ctx, android.LogQuery{Limit: 2})
	if err != nil || len(last) != 2 || last[1].Level != "F" {
		t.Errorf("last = %+v, %v", last, err)
	}
	for _, tc := range []struct {
		filters []string
		want    int
	}{
		{[]string{"tag=ShopApp"}, 2},
		{[]string{"tag!=ShopApp"}, 5},
		{[]string{"level=W"}, 3},
		{[]string{"level=e"}, 2},
		{[]string{"level!=W"}, 4},
		{[]string{"message=HTTP"}, 1},
		{[]string{"pid=1234"}, 2},
		{[]string{"process=com.example.shop"}, 5},
		{[]string{"process!=com.example.shop"}, 2},
		{[]string{"process=com.example.shop", "level=E"}, 2},
		{[]string{"process=com.example.shop", "tag=ShopApp", "level=E"}, 1},
		{[]string{"subsystem=x", "category!=y"}, 7},
		{[]string{"tag=Nope"}, 0},
	} {
		fs, err := android.ParseLogFilters(tc.filters)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Logs(ctx, android.LogQuery{Filters: fs})
		if err != nil || len(got) != tc.want {
			t.Errorf("%v: %d entries, want %d (%v)", tc.filters, len(got), tc.want, err)
		}
	}
	// With filters the device is asked for more than the limit.
	fs, _ := android.ParseLogFilters([]string{"tag=ShopApp"})
	if _, err := c.Logs(ctx, android.LogQuery{Filters: fs, Limit: 5}); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "logcat", "-d", "-v", "threadtime", "-t", "100") {
		t.Errorf("no widened dump: %v", r.adb.Shell())
	}
	r.adb.AssertQuoted(t)
}

func TestControllerLogsFollow(t *testing.T) {
	three := "11-23 10:00:00.001  10  10 I T: one\n11-23 10:00:00.002  10  10 I T: two\n11-23 10:00:00.003  10  10 W T: three\n11-23 10:00:00.004  10  10 I T: four\n"
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{{Match: "'logcat' '-v' 'threadtime' '-T' '1'", Out: three, Hold: true}}
	})
	_, c := r.start(t)
	ctx := context.Background()
	start := time.Now()
	got, err := c.Logs(ctx, android.LogQuery{Follow: true, Limit: 3})
	if err != nil || len(got) != 3 || got[2].Message != "three" {
		t.Fatalf("limit: %+v, %v", got, err)
	}
	// The command stays on the device, so the stream ended because of the limit, and quickly.
	if time.Since(start) > 10*time.Second {
		t.Error("following did not stop at the limit")
	}
	fs, _ := android.ParseLogFilters([]string{"level=W"})
	start = time.Now()
	got, err = c.Logs(ctx, android.LogQuery{Follow: true, Filters: fs, Limit: 50, Silence: 300 * time.Millisecond, Cap: 5 * time.Second})
	if err != nil || len(got) != 1 || got[0].Message != "three" {
		t.Errorf("silence: %+v, %v", got, err)
	}
	if time.Since(start) > 4*time.Second {
		t.Errorf("silence took %v", time.Since(start))
	}
	got, err = c.Logs(ctx, android.LogQuery{Follow: true, Limit: 50, Silence: time.Minute, Cap: 400 * time.Millisecond})
	if err != nil || len(got) != 4 {
		t.Errorf("cap: %+v, %v", got, err)
	}
	cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	got, err = c.Logs(cctx, android.LogQuery{Follow: true, Limit: 50, Silence: time.Minute, Cap: time.Minute})
	if !errors.Is(err, context.DeadlineExceeded) || len(got) != 4 {
		t.Errorf("cancel: %d entries, %v", len(got), err)
	}
	r.adb.AssertQuoted(t)
}

func TestControllerCrashes(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'--print' 'data_app_crash'", Out: testdata(t, "dropbox_crash.txt")},
			{Match: "'--print' 'data_app_anr'", Out: testdata(t, "dropbox_anr.txt")},
			{Match: "'--print' 'data_app_native_crash'", RC: 1, Err: "Can't find service: dropbox"},
			{Match: "'logcat' '-b' 'crash'", Out: testdata(t, "crash_buffer.txt")},
		}
	})
	_, c := r.start(t)
	ctx := context.Background()
	list, err := c.ListCrashes(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	// 3 + 1 of dropbox, and the buffer adds only the crash that dropbox lacks: com.third.app. The
	// crash of the shop at 10:15:30 is in dropbox as well. The clock of the rig is 2026, as the logs are.
	if len(list) != 5 {
		t.Fatalf("%d crashes: %+v", len(list), list)
	}
	for i := 1; i < len(list); i++ {
		if list[i].Time.After(list[i-1].Time) {
			t.Error("not newest first")
		}
	}
	for _, cr := range list {
		if cr.Body != "" {
			t.Errorf("a list holds a body: %s", cr.ID)
		}
	}
	if list[0].Tag != "data_app_anr" {
		t.Errorf("newest = %+v", list[0])
	}
	shop, err := c.ListCrashes(ctx, pkgShop, 2)
	if err != nil || len(shop) != 2 || shop[0].Package != pkgShop {
		t.Errorf("shop = %+v, %v", shop, err)
	}
	if _, err := c.ListCrashes(ctx, "bad pkg", 0); err == nil {
		t.Error("a bad package")
	}
	full, err := c.GetCrash(ctx, list[len(list)-1].ID)
	if err != nil || full.Body == "" || full.ID != list[len(list)-1].ID {
		t.Errorf("GetCrash = %+v, %v", full, err)
	}
	for _, cr := range list {
		got, err := c.GetCrash(ctx, cr.ID)
		if err != nil || got.ID != cr.ID || got.Body == "" {
			t.Errorf("GetCrash(%s) = %+v, %v", cr.ID, got, err)
		}
	}
	if _, err := c.GetCrash(ctx, "data_app_crash@1"); !errors.Is(err, android.ErrCrashNotFound) {
		t.Errorf("a missing crash: %v", err)
	}
	for _, bad := range []string{"", "x", "data_app_crash", "evil@1", "data_app_crash@1; reboot", "../x@1"} {
		if _, err := c.GetCrash(ctx, bad); err == nil || errors.Is(err, android.ErrCrashNotFound) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	r.adb.AssertQuoted(t)
}

func TestControllerImeIsCachedForTenSeconds(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Dumpsys = "mInputShown=true\n"
		cfg.Canned = []mobiletest.Canned{{Match: "'settings' 'get' 'secure' 'default_input_method'", Out: "com.aura.keyboard.debug/.Ime\n"}}
	})
	_, c := r.start(t)
	ctx := context.Background()
	pkg, shown, err := c.InputMethod(ctx)
	if err != nil || pkg != "com.aura.keyboard.debug" || !shown {
		t.Fatalf("InputMethod = %q %v %v", pkg, shown, err)
	}
	r.adb.Set("dumpsys", "mInputShown=false\n")
	if _, shown, _ := c.InputMethod(ctx); !shown {
		t.Error("the answer was not cached")
	}
	if n := countShell(r, "dumpsys", "input_method"); n != 1 {
		t.Errorf("%d dumps", n)
	}
	// Ten seconds later it asks again.
	r.clk.Sleep(ctx, 11*time.Second)
	if _, shown, _ := c.InputMethod(ctx); shown {
		t.Error("an old answer after ten seconds")
	}
	// A launch, a press, and the helper drop it.
	r.adb.Set("dumpsys", "mInputShown=true\n")
	c.InvalidateInputMethod()
	if _, shown, _ := c.InputMethod(ctx); !shown {
		t.Error("InvalidateInputMethod kept the answer")
	}
	r.adb.Set("dumpsys", "mInputShown=false\n")
	if err := c.LaunchApp(ctx, android.LaunchOptions{Package: pkgShop}); err != nil {
		t.Fatal(err)
	}
	if _, shown, _ := c.InputMethod(ctx); shown {
		t.Error("LaunchApp kept the answer")
	}
	r.adb.Set("dumpsys", "mInputShown=true\n")
	if err := c.Key(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if _, shown, _ := c.InputMethod(ctx); !shown {
		t.Error("Key kept the answer")
	}
}

func TestControllerRecording(t *testing.T) {
	r := newCtlRig(t)
	p, c := r.start(t)
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "run.mp4")
	for name, o := range map[string]android.RecOptions{
		"relative":    {Output: "run.mp4"},
		"not mp4":     {Output: filepath.Join(t.TempDir(), "run.avi")},
		"no folder":   {Output: filepath.Join(t.TempDir(), "nope", "run.mp4")},
		"too long":    {Output: out, TimeLimit: 181 * time.Second},
		"bad size":    {Output: out, Size: "720;reboot"},
		"bad bitrate": {Output: out, BitRate: 5},
	} {
		if err := c.StartRecording(ctx, o); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := c.StopRecording(ctx); err == nil {
		t.Error("stopped a recording that never started")
	}
	// A file that was there already, with a wider mode, is the person's alone once the video is in it.
	if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.StartRecording(ctx, android.RecOptions{Output: out, TimeLimit: 90 * time.Second, Size: "720x1280", BitRate: 4_000_000}); err != nil {
		t.Fatal(err)
	}
	if !r.adb.Recording() {
		t.Error("the fake screenrecord is not running")
	}
	if err := c.StartRecording(ctx, android.RecOptions{Output: out}); err == nil {
		t.Error("two recordings at once")
	}
	got, err := c.StopRecording(ctx)
	if err != nil || got != out {
		t.Fatalf("StopRecording = %q, %v", got, err)
	}
	if data, err := os.ReadFile(out); err != nil || string(data) != "FAKE-MP4" {
		t.Errorf("file = %q, %v", data, err)
	}
	if info, err := os.Stat(out); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the video has the mode %v (%v), want 0600", info.Mode().Perm(), err)
	}
	var started, remote []string
	for _, w := range r.adb.ShellWords() {
		if w[0] == "screenrecord" {
			started = w
			remote = w[len(w)-1:]
		}
	}
	wantPrefix := []string{"screenrecord", "--time-limit", "90", "--size", "720x1280", "--bit-rate", "4000000"}
	if len(started) != 8 || !slices.Equal(started[:7], wantPrefix) || !strings.HasPrefix(remote[0], "/data/local/tmp/semar-rec-") || !strings.HasSuffix(remote[0], ".mp4") {
		t.Errorf("screenrecord = %v", started)
	}
	if !shellHas(r, "pkill", "-INT", "screenrecord") {
		t.Errorf("no SIGINT: %v", r.adb.Shell())
	}
	if !r.adb.Contains("pull", remote[0], out) {
		t.Errorf("no pull: %v", r.adb.Log())
	}
	if !shellHas(r, "rm", "-f", remote[0]) {
		t.Errorf("the device file was not deleted: %v", r.adb.Shell())
	}
	// The order is stop, pull, delete.
	order := func(prefix string) int {
		for i, l := range r.adb.Log() {
			if strings.Contains(l, prefix) {
				return i
			}
		}
		return -1
	}
	if !(order("'pkill'") < order("pull ") && order("pull ") < order("'rm'")) {
		t.Errorf("order: %v", r.adb.Log())
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	r.adb.AssertQuoted(t)
}

func TestControllerRecordingIsCleanedUpOnClose(t *testing.T) {
	r := newCtlRig(t)
	p, c := r.start(t)
	out := filepath.Join(t.TempDir(), "run.mp4")
	if err := c.StartRecording(context.Background(), android.RecOptions{Output: out}); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.adb.Recording() {
		t.Error("the recording is still running after Close")
	}
	if !shellHas(r, "pkill", "-INT", "screenrecord") {
		t.Errorf("no SIGINT: %v", r.adb.Shell())
	}
	var device string
	for _, w := range r.adb.ShellWords() {
		if w[0] == "screenrecord" {
			device = w[len(w)-1]
		}
	}
	if !shellHas(r, "rm", "-f", device) {
		t.Errorf("the device file was left: %v", r.adb.Shell())
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("Close wrote a file nobody asked for")
	}
	if _, err := c.StopRecording(context.Background()); err == nil {
		t.Error("a recording that Close ended can be stopped")
	}
}

func TestControllerRecordingThatDiesAtOnceIsAnError(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{{Match: "'screenrecord'", RC: 1, Err: "ERROR: unable to configure codec"}}
	})
	_, c := r.start(t)
	err := c.StartRecording(context.Background(), android.RecOptions{Output: filepath.Join(t.TempDir(), "x.mp4")})
	if err == nil || !strings.Contains(err.Error(), "unable to configure codec") {
		t.Errorf("err = %v", err)
	}
}

func TestEveryShellCommandOfAControllerSessionIsQuoted(t *testing.T) {
	r := newCtlRig(t)
	p, c := r.start(t)
	ctx := context.Background()
	_, _ = c.Source(ctx)
	_ = c.LaunchApp(ctx, android.LaunchOptions{Package: pkgShop, Arguments: []string{"k=v'; reboot #"}})
	_ = c.OpenURL(ctx, "https://x.test/?q=';reboot")
	_ = c.TypeFocused(ctx, "a b", true)
	_ = c.SetOrientation(ctx, "landscape")
	_, _ = c.ListApps(ctx, true)
	_, _ = c.Logs(ctx, android.LogQuery{})
	_, _ = c.ListCrashes(ctx, "", 0)
	_ = p.Close(ctx)
	r.adb.AssertQuoted(t)
	for _, w := range r.adb.ShellWords() {
		for _, word := range w {
			if word == "reboot" || word == "reboot #" {
				t.Errorf("an injected word became a word of its own: %v", w)
			}
		}
	}
}

func TestQuotedWords(t *testing.T) {
	for in, want := range map[string][]string{
		`'a' 'b c'`:    {"a", "b c"},
		`'it'\''s'`:    {"it's"},
		`'x' ''`:       {"x", ""},
		`'a'\'''\''b'`: {"a''b"},
	} {
		got, ok := mobiletest.QuotedWords(in)
		if !ok || !slices.Equal(got, want) {
			t.Errorf("%s = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{``, `a`, `'a' b`, `'a`, `'a'b`, `'a' `, `'a'  'b'`} {
		if _, ok := mobiletest.QuotedWords(bad); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// remoteController starts a controller at serverUrl.
func newRemoteCtl(t *testing.T) (*android.Platform, *android.Controller, *mobiletest.Server, *wdExtra) {
	t.Helper()
	server := mobiletest.NewServer(t, mobiletest.Options{NoExecute: true})
	wd := newWDExtra(t, server.URL)
	clk := &clock{}
	opts := android.Options{
		Env: config.Android{ServerURL: wd.URL, Device: "pixel-farm-1"}, RepoRoot: t.TempDir(),
		LookupEnv: func(string) (string, bool) { return "", false },
		Sleep:     clk.Sleep, Now: clk.Now,
		FindADB: func() (string, error) { t.Error("adb was looked for in serverUrl mode"); return "", nil },
		Run:     refuseRunner{t},
	}
	p := android.New(opts)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	c, err := p.Controller(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p, c, server, wd
}

func TestRemoteController(t *testing.T) {
	p, c, server, wd := newRemoteCtl(t)
	ctx := context.Background()
	if !c.Remote() || c.Emulator() || c.Serial() != "pixel-farm-1" {
		t.Errorf("remote %v, emulator %v, serial %q", c.Remote(), c.Emulator(), c.Serial())
	}
	if server.Sessions() != 0 {
		t.Error("a session was opened before it was needed")
	}
	if _, err := c.Source(ctx); err != nil {
		t.Fatal(err)
	}
	caps := server.Capabilities()
	if caps["platformName"] != "Android" || caps["appium:udid"] != "pixel-farm-1" {
		t.Errorf("caps = %v", caps)
	}
	for _, k := range []string{"appium:appPackage", "appium:app", "appium:appActivity"} {
		if _, has := caps[k]; has {
			t.Errorf("the session names %s", k)
		}
	}
	if err := c.Touch(ctx, mobile.Gesture{From: mobile.Point{X: 1, Y: 1}, To: mobile.Point{X: 1, Y: 1}}); err != nil {
		t.Fatal(err)
	}
	if got, err := c.ScreenSize(ctx); err != nil || got.Width != 1080 || got.Height != 2400 {
		t.Errorf("ScreenSize = %+v, %v", got, err)
	}
	if err := c.SetLocation(ctx, -6.2, 106.8); err != nil || !strings.Contains(wd.Body("POST /location"), `"latitude":-6.2`) {
		t.Errorf("SetLocation: %v %s", err, wd.Body("POST /location"))
	}
	if err := c.SetOrientation(ctx, "landscape"); err != nil {
		t.Errorf("SetOrientation: %v", err)
	}
	// Everything that needs adb says so.
	unsupported := map[string]error{
		"ListApps":      func() error { _, err := c.ListApps(ctx, false); return err }(),
		"Foreground":    func() error { _, _, err := c.Foreground(ctx); return err }(),
		"LaunchApp":     c.LaunchApp(ctx, android.LaunchOptions{Package: pkgShop}),
		"TerminateApp":  c.TerminateApp(ctx, pkgShop),
		"InstallAPK":    c.InstallAPK(ctx, "/x.apk"),
		"UninstallApp":  c.UninstallApp(ctx, pkgShop),
		"ClearAppData":  c.ClearAppData(ctx, pkgShop),
		"Fold":          c.Fold(ctx, "open"),
		"ClearLocation": c.ClearLocation(ctx),
		"OpenURL":       c.OpenURL(ctx, "https://x.test"),
		"Logs":          func() error { _, err := c.Logs(ctx, android.LogQuery{}); return err }(),
		"ListCrashes":   func() error { _, err := c.ListCrashes(ctx, "", 0); return err }(),
		"GetCrash":      func() error { _, err := c.GetCrash(ctx, "data_app_crash@1"); return err }(),
		"InputMethod":   func() error { _, _, err := c.InputMethod(ctx); return err }(),
	}
	for name, err := range unsupported {
		if !errors.Is(err, android.ErrUnsupported) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	out := filepath.Join(t.TempDir(), "r.mp4")
	if err := c.StartRecording(ctx, android.RecOptions{Output: out, TimeLimit: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wd.Body("POST /appium/start_recording_screen"), `"timeLimit":30`) {
		t.Errorf("start body = %s", wd.Body("POST /appium/start_recording_screen"))
	}
	if got, err := c.StopRecording(ctx); err != nil || got != out {
		t.Fatalf("StopRecording = %q, %v", got, err)
	}
	if data, _ := os.ReadFile(out); string(data) != "REMOTE-MP4" {
		t.Errorf("video = %q", data)
	}
	// A recording that is left is stopped on Close.
	if err := c.StartRecording(ctx, android.RecOptions{Output: out}); err != nil {
		t.Fatal(err)
	}
	before := len(wd.Calls())
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(wd.Calls()[before:], "POST /appium/stop_recording_screen") {
		t.Errorf("Close left the recording: %v", wd.Calls()[before:])
	}
	if server.Deleted() != 1 {
		t.Errorf("sessions ended: %d", server.Deleted())
	}
}

func TestControllerAfterCloseIsUnavailable(t *testing.T) {
	r := newCtlRig(t)
	p, c := r.start(t)
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Source(context.Background()); err == nil {
		t.Error("Source on a closed platform")
	}
	if _, err := c.ListApps(context.Background(), false); err == nil {
		t.Error("ListApps on a closed platform")
	}
	_ = fmt.Sprint
}
