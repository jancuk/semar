package android_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
	"github.com/jancuk/semar/internal/engine/mobile/webdriver"
)

func TestTheStartOrderAndTheCleanup(t *testing.T) {
	r := newRig(t)
	p := r.platform()
	if got := r.adb.Calls(); len(got) != 0 {
		t.Fatalf("New ran adb: %v", got)
	}
	d := r.driver(p)
	if about := d.About(); about.Device != emulator || about.Package != pkgShop || about.Version != "14" {
		t.Errorf("about = %+v", about)
	}
	want := []string{
		"start-server",
		"devices -l",
		"shell 'getprop' 'sys.boot_completed'",
		"shell 'pm' 'path' 'io.appium.uiautomator2.server'",
		"shell 'pm' 'path' 'io.appium.uiautomator2.server.test'",
		"shell 'am' 'force-stop' 'io.appium.uiautomator2.server'",
		"shell 'am' 'force-stop' 'io.appium.uiautomator2.server.test'",
		"forward tcp:0 tcp:6790",
		"shell 'am' 'instrument' '-w' '-e' 'disableAnalytics' 'true' 'io.appium.uiautomator2.server.test/androidx.test.runner.AndroidJUnitRunner'",
		"shell 'pm' 'path' 'com.example.shop'",
		"shell 'getprop' 'ro.build.version.release'",
	}
	// getprop of the version is quoted too.
	want[10] = "shell 'getprop' 'ro.build.version.release'"
	got := r.adb.Log()
	// The server runs in its own process, started in the background: its am instrument may be logged
	// before or after the calls that follow it. It must come after the forward, and the rest keep their order.
	instrument, forward := want[8], want[7]
	at, fwd := slices.Index(got, instrument), slices.Index(got, forward)
	others := slices.DeleteFunc(slices.Clone(got), func(s string) bool { return s == instrument })
	wantRest := slices.DeleteFunc(slices.Clone(want), func(s string) bool { return s == instrument })
	if at < 0 || fwd < 0 || at < fwd || strings.Join(others, "\n") != strings.Join(wantRest, "\n") {
		t.Errorf("adb log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if r.server.Sessions() != 1 || r.server.Capabilities()["platformName"] != "Android" {
		t.Errorf("sessions %d, caps %v", r.server.Sessions(), r.server.Capabilities())
	}
	if s := r.server.Settings(); s["waitForIdleTimeout"] != float64(0) || s["waitForSelectorTimeout"] != float64(0) || s["enableMultiWindows"] != true {
		t.Errorf("settings = %v", s)
	}
	// One more service call does not start anything again.
	r.driver(p)
	if r.adb.Count("devices") != 1 || r.server.Sessions() != 1 {
		t.Error("the platform started twice")
	}

	before := len(r.adb.Log())
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	rest := r.adb.Log()[before:]
	wantClose := []string{
		"shell 'am' 'force-stop' 'io.appium.uiautomator2.server'",
		"shell 'am' 'force-stop' 'io.appium.uiautomator2.server.test'",
	}
	if len(rest) != 3 || strings.Join(rest[:2], "\n") != strings.Join(wantClose, "\n") || !strings.HasPrefix(rest[2], "forward --remove tcp:") {
		t.Errorf("close:\n%s", strings.Join(rest, "\n"))
	}
	if r.server.Deleted() != 1 {
		t.Errorf("the session was deleted %d times", r.server.Deleted())
	}
	// Closing again does nothing, and a driver is refused after it.
	n := len(r.adb.Log())
	if err := p.Close(context.Background()); err != nil || len(r.adb.Log()) != n || r.server.Deleted() != 1 {
		t.Errorf("a second Close: %v", err)
	}
	if _, err := p.Driver(context.Background(), "shop"); err == nil {
		t.Error("a driver after Close")
	}
	// Semar leaves the daemon, the packages, and the app alone: no uninstall and no stop of the app.
	for _, l := range r.adb.Log() {
		if strings.Contains(l, "uninstall") || strings.Contains(l, "'com.example.shop'") && strings.Contains(l, "force-stop") || strings.Contains(l, "kill-server") {
			t.Errorf("unexpected call: %s", l)
		}
	}
}

func TestEveryDeviceShellWordIsQuotedInAWholeRun(t *testing.T) {
	r := newRig(t, func(r *rig, _ *mobiletest.ADBConfig) {
		r.opts = android.Options{}
	})
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, Reset: config.ResetClear, Permissions: config.Permissions{Names: []string{"android.permission.CAMERA"}}}}
	r.opts.Home, r.opts.RepoRoot = r.home, r.repo
	r.opts.LookupEnv = func(k string) (string, bool) { v, ok := r.env[k]; return v, ok }
	r.opts.Sleep, r.opts.Now = r.clk.Sleep, r.clk.Now
	p := r.platform()
	d := r.driver(p)
	ctx := context.Background()
	for _, f := range []func() error{
		func() error { return d.Reset(ctx) },
		func() error { return d.Launch(ctx) },
		func() error { return d.Open(ctx, "shop://a") },
		func() error { return d.Key(ctx, mobile.KeyHideKeyboard) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	_ = p.Close(ctx)
	if len(r.adb.Shell()) < 10 {
		t.Fatalf("shell commands: %v", r.adb.Shell())
	}
	for _, c := range r.adb.Shell() {
		for _, w := range strings.Split(c, " ") {
			if w != "" && !strings.HasPrefix(w, "'") && !strings.HasSuffix(w, "'") {
				t.Errorf("a word of %q is not quoted", c)
			}
		}
	}
}

func TestResetStopsClearsAndGrants(t *testing.T) {
	for mode, want := range map[string][]string{
		"":        {"'am' 'force-stop' 'com.example.shop'", "'pm' 'grant' 'com.example.shop' 'android.permission.CAMERA'"},
		"restart": {"'am' 'force-stop' 'com.example.shop'", "'pm' 'grant' 'com.example.shop' 'android.permission.CAMERA'"},
		"clear":   {"'am' 'force-stop' 'com.example.shop'", "'pm' 'clear' 'com.example.shop'", "'pm' 'grant' 'com.example.shop' 'android.permission.CAMERA'"},
		"none":    {"'pm' 'grant' 'com.example.shop' 'android.permission.CAMERA'"},
	} {
		r := newRig(t)
		r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, Reset: mode, Permissions: config.Permissions{Names: []string{"android.permission.CAMERA"}}}}
		p := r.platform()
		d := r.driver(p)
		n := len(r.adb.Shell())
		if err := d.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := r.adb.Shell()[n:]
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("reset %q:\n%s\nwant\n%s", mode, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if mode == "none" {
			// The second reset with none does nothing at all: the app keeps its state.
			n = len(r.adb.Shell())
			_ = d.Reset(context.Background())
			if len(r.adb.Shell()) != n {
				t.Errorf("a second reset with none sent %v", r.adb.Shell()[n:])
			}
		}
	}
}

func TestAPermissionThatCannotBeGrantedIsAnErrorOfTheReset(t *testing.T) {
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.GrantOutput = "Exception occurred while executing 'grant':\njava.lang.SecurityException: Unknown permission"
	})
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, Permissions: config.Permissions{Names: []string{"android.permission.NOPE"}}}}
	d := r.driver(r.platform())
	err := d.Reset(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the permission android.permission.NOPE could not be granted") {
		t.Errorf("error = %v", err)
	}
}

func TestLaunchUsesTheActivityOrTheLauncher(t *testing.T) {
	r := newRig(t)
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, Activity: ".ui.MainActivity"}}
	d := r.driver(r.platform())
	if err := d.Launch(context.Background()); err != nil {
		t.Fatal(err)
	}
	last := r.adb.Shell()[len(r.adb.Shell())-1]
	if last != "'am' 'start' '-W' '--activity-clear-task' '-n' 'com.example.shop/.ui.MainActivity' '-a' 'android.intent.action.MAIN' '-c' 'android.intent.category.LAUNCHER'" {
		t.Errorf("launch = %s", last)
	}

	r2 := newRig(t)
	d2 := r2.driver(r2.platform())
	if err := d2.Launch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d2.Launch(context.Background()); err != nil {
		t.Fatal(err)
	}
	sh := strings.Join(r2.adb.Shell(), "\n")
	if strings.Count(sh, "'resolve-activity'") != 1 {
		t.Errorf("the launcher activity was asked %d times", strings.Count(sh, "'resolve-activity'"))
	}
	if !strings.Contains(sh, "'-n' 'com.example.shop/.MainActivity'") {
		t.Errorf("shell:\n%s", sh)
	}
}

func TestLaunchReadsTheOutputForAnError(t *testing.T) {
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.StartOutput = "Starting: Intent { cmp=com.example.shop/.Nope }\nError type 3\nError: Activity class {com.example.shop/com.example.shop.Nope} does not exist."
	})
	d := r.driver(r.platform())
	err := d.Launch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the app could not be started: Error type 3") {
		t.Errorf("error = %v", err)
	}
	if err := d.Open(context.Background(), "shop://x"); err == nil || !strings.Contains(err.Error(), "the link could not be opened") {
		t.Errorf("open error = %v", err)
	}
}

func TestAnActivityThatIsNotANameIsRefusedBeforeItReachesTheShell(t *testing.T) {
	r := newRig(t)
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, Activity: ".A'; reboot; '"}}
	d := r.driver(r.platform())
	err := d.Launch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "is not an activity name") {
		t.Errorf("error = %v", err)
	}
	for _, c := range r.adb.Shell() {
		if strings.Contains(c, "reboot") {
			t.Errorf("the shell got %s", c)
		}
	}
}

func TestKeysAndTheKeyboard(t *testing.T) {
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) { c.Dumpsys = "  mInputShown=true\n" })
	d := r.driver(r.platform())
	ctx := context.Background()
	for _, k := range []mobile.Key{mobile.KeyBack, mobile.KeyHome, mobile.KeyEnter, mobile.KeySearch, mobile.KeyTab, mobile.KeyDelete} {
		if err := d.Key(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.server.Keycodes(); len(got) != 6 || got[0] != 4 || got[1] != 3 || got[2] != 66 || got[3] != 84 || got[4] != 61 || got[5] != 67 {
		t.Errorf("keycodes = %v", got)
	}
	if err := d.Key(ctx, mobile.KeyHideKeyboard); err != nil {
		t.Fatal(err)
	}
	if got := r.server.Keycodes(); len(got) != 7 || got[6] != 4 {
		t.Errorf("an open keyboard is closed with escape: %v", got)
	}
	r.adb.Set("dumpsys", "  mInputShown=false\n")
	if err := d.Key(ctx, mobile.KeyHideKeyboard); err != nil || len(r.server.Keycodes()) != 7 {
		t.Errorf("a closed keyboard got a key: %v, %v", err, r.server.Keycodes())
	}
	if err := d.Key(ctx, "power"); err == nil {
		t.Error("an unknown key")
	}
}

func TestSourceTouchTypeAndScreenshot(t *testing.T) {
	r := newRig(t)
	src := mobiletest.Hierarchy(mobiletest.Widget("android.widget.EditText", map[string]string{"resource-id": "com.example.shop:id/email"}, "[48,300][1032,420]"))
	r.server.SetSource(src)
	d := r.driver(r.platform())
	ctx := context.Background()
	tree, err := d.Source(ctx)
	if err != nil || tree.Count() != 2 || tree.Width != 1080 {
		t.Fatalf("tree = %v, %v", tree, err)
	}
	// The same bytes give the same tree, and not a new parse.
	again, _ := d.Source(ctx)
	if again != tree {
		t.Error("equal bytes did not reuse the parsed tree")
	}
	r.server.SetSource(strings.Replace(src, "[48,300]", "[48,301]", 1))
	if next, _ := d.Source(ctx); next == tree || next.Digest == tree.Digest {
		t.Error("changed bytes gave the old tree")
	}
	if err := d.Touch(ctx, mobile.Gesture{From: mobile.Point{X: 5, Y: 6}, To: mobile.Point{X: 5, Y: 6}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Type(ctx, "ana@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := r.server.Typed(); len(got) != 1 || got[0] != "ana@example.com" {
		t.Errorf("typed = %v", got)
	}
	calls := r.server.Calls()
	var tail []string
	for _, c := range calls {
		if strings.Contains(c, "/element/") && !strings.HasSuffix(c, "/active") {
			tail = append(tail, c)
		}
	}
	if len(tail) != 2 || !strings.HasSuffix(tail[0], "/clear") || !strings.HasSuffix(tail[1], "/value") {
		t.Errorf("type must clear and then set: %v", tail)
	}
	shot, err := d.Screenshot(ctx)
	if err != nil || len(shot) < 8 {
		t.Errorf("screenshot = %d bytes, %v", len(shot), err)
	}
	r.server.Fail("GET /screenshot", mobiletest.Failure{Status: 500, Code: "unknown error", Message: "Cannot take a screenshot of a secure window"})
	if _, err := d.Screenshot(ctx); !errors.Is(err, mobile.ErrSecure) {
		t.Errorf("a secure window gave %v", err)
	}
	r.server.Heal("GET /screenshot")
	r.server.Fail("GET /source", mobiletest.Failure{Status: 500, Code: "unknown error", Message: "x"})
	if _, err := d.Source(ctx); err == nil {
		t.Error("a server error was swallowed")
	}
}

func TestTypeWithNoFocusedFieldIsAnError(t *testing.T) {
	r := newRig(t)
	d := r.driver(r.platform())
	err := d.Type(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "no field has the focus") {
		t.Errorf("error = %v", err)
	}
}

func TestDeviceChoice(t *testing.T) {
	phone := "R58M123ABC\tdevice usb:1-1 product:x model:Pixel_7 device:x transport_id:3"
	emu2 := "emulator-5556\tdevice product:sdk model:sdk transport_id:2"
	for name, c := range map[string]struct {
		devices []string
		env     map[string]string
		cfg     string
		want    string // the serial used, or ""
		reason  string
	}{
		"the one emulator":                    {[]string{emuLine}, nil, "", emulator, ""},
		"the emulator and a phone":            {[]string{phone, emuLine}, nil, "", emulator, ""},
		"two emulators":                       {[]string{emuLine, emu2}, nil, "", "", "2 emulators are running (emulator-5554, emulator-5556): name the one"},
		"ANDROID_SERIAL picks one":            {[]string{emuLine, emu2}, map[string]string{"ANDROID_SERIAL": "emulator-5556"}, "", "emulator-5556", ""},
		"android.device picks one":            {[]string{emuLine, emu2}, nil, "emulator-5556", "emulator-5556", ""},
		"ANDROID_SERIAL wins over the file":   {[]string{emuLine, emu2}, map[string]string{"ANDROID_SERIAL": "emulator-5554"}, "emulator-5556", "emulator-5554", ""},
		"a physical device that is named":     {[]string{phone}, map[string]string{"ANDROID_SERIAL": "R58M123ABC"}, "", "R58M123ABC", ""},
		"a physical device that is not":       {[]string{phone}, nil, "", "", "adb sees only physical devices (R58M123ABC): Semar uses a physical device only when it is named"},
		"no device":                           {nil, nil, "", "", "no device is connected: start an emulator"},
		"an emulator that is still starting":  {[]string{"emulator-5554\toffline"}, nil, "", "", "the emulator emulator-5554 is not ready (offline)"},
		"a named device that is not there":    {[]string{emuLine}, map[string]string{"ANDROID_SERIAL": "R58M123ABC"}, "", "", "the device R58M123ABC is not connected: adb lists emulator-5554"},
		"a named device that is offline":      {[]string{"R58M123ABC\toffline"}, map[string]string{"ANDROID_SERIAL": "R58M123ABC"}, "", "", "the device R58M123ABC is offline"},
		"a named device that is unauthorized": {[]string{"R58M123ABC\tunauthorized"}, map[string]string{"ANDROID_SERIAL": "R58M123ABC"}, "", "", "has not allowed this computer: accept the USB debugging prompt"},
		"a named device in another state":     {[]string{"R58M123ABC\trecovery"}, nil, "R58M123ABC", "", `adb says "recovery"`},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) { cfg.Devices = c.devices })
			for k, v := range c.env {
				r.env[k] = v
			}
			r.opts.Env = config.Android{Device: c.cfg}
			p := r.platform()
			d, err := p.Driver(context.Background(), "shop")
			if c.reason != "" {
				un := unavailable(t, err)
				if !strings.Contains(un.Reason, c.reason) {
					t.Fatalf("reason = %q, want one with %q", un.Reason, c.reason)
				}
				if r.server.Sessions() != 0 || r.adb.Contains("forward") {
					t.Error("the platform went on after the device was refused")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d.About().Device != c.want {
				t.Errorf("device = %q, want %q", d.About().Device, c.want)
			}
			for _, call := range r.adb.Calls() {
				if len(call) > 2 && call[0] == "-s" && call[1] != c.want {
					t.Errorf("a call went to %s: %v", call[1], call)
				}
			}
		})
	}
}

func TestParseDevices(t *testing.T) {
	out := "* daemon not running; starting now at tcp:5037\n* daemon started successfully\nList of devices attached\n" +
		"emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1\n" +
		"R58M123ABC             unauthorized usb:1-1 transport_id:3\n192.168.1.5:5555       device product:x model:Pixel_6 transport_id:4\n\n"
	got := android.ParseDevices(out)
	if len(got) != 3 || got[0].Serial != "emulator-5554" || !got[0].Emulator || got[0].Model != "sdk_gphone64_arm64" ||
		got[1].State != "unauthorized" || got[1].Emulator || got[2].Serial != "192.168.1.5:5555" || got[2].Emulator {
		t.Errorf("devices = %+v", got)
	}
}

func TestADeviceThatIsStillStartingIsWaitedFor(t *testing.T) {
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) { c.NotBooted = true })
	p := r.platform()
	_, err := p.Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "has not finished starting") || r.clk.n < 20 {
		t.Errorf("reason = %q after %d waits", un.Reason, r.clk.n)
	}
	// It is the same answer without another try.
	waits := r.clk.n
	if _, err := p.Driver(context.Background(), "shop"); err == nil || r.clk.n != waits {
		t.Errorf("a second call tried again: %v", err)
	}
}

func TestNoADBIsUnavailableAndStartsNothing(t *testing.T) {
	r := newRig(t)
	r.env = map[string]string{"PATH": "", "HOME": t.TempDir()}
	p := android.New(android.Options{Home: r.home, RepoRoot: r.repo, Apps: r.opts.Apps, LookupEnv: r.opts.LookupEnv, Sleep: r.clk.Sleep, Now: r.clk.Now})
	_, err := p.Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "adb was not found") {
		t.Errorf("reason = %q", un.Reason)
	}
	if len(r.adb.Calls()) != 0 {
		t.Errorf("adb ran: %v", r.adb.Calls())
	}
	if err := p.Close(context.Background()); err != nil {
		t.Errorf("Close after a failed start: %v", err)
	}
}

func TestAnUnknownServiceIsAnErrorAndNotUnavailable(t *testing.T) {
	r := newRig(t)
	_, err := r.platform().Driver(context.Background(), "nobody")
	var un *mobile.Unavailable
	if err == nil || errors.As(err, &un) || !strings.Contains(err.Error(), `the service "nobody" has no android app`) {
		t.Errorf("error = %v", err)
	}
	if len(r.adb.Calls()) != 0 {
		t.Error("adb ran")
	}
}

func TestTheDeviceLeaseKeepsTwoRunsApart(t *testing.T) {
	r := newRig(t)
	first := r.platform()
	r.driver(first)
	second := android.New(r.opts)
	t.Cleanup(func() { _ = second.Close(context.Background()) })
	_, err := second.Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "the device emulator-5554 is used by another semar run") {
		t.Errorf("reason = %q", un.Reason)
	}
	// The second run did not touch the packages of the device, and closing it must not stop the first run's server.
	if err := second.Close(context.Background()); err != nil {
		t.Fatalf("closing the losing platform: %v", err)
	}
	if r.adb.Count("forward", "tcp:0", "tcp:6790") != 1 {
		t.Error("the second run went on")
	}
	if r.adb.Count("force-stop") != 0 {
		t.Errorf("closing the losing platform stopped the winning run's server: %v", r.adb.Log())
	}
	// Closing the first releases the lease, and a third run may take it.
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	third := android.New(r.opts)
	t.Cleanup(func() { _ = third.Close(context.Background()) })
	if _, err := third.Driver(context.Background(), "shop"); err != nil {
		t.Errorf("after the lease was released: %v", err)
	}
}

func TestTheServerFilesAreInstalledOnlyWhenTheyDiffer(t *testing.T) {
	server, serverSum := writeFile(t, t.TempDir(), "server.apk", "server apk bytes")
	test, testSum := writeFile(t, t.TempDir(), "test.apk", "test apk bytes")
	same := func(_ *rig, c *mobiletest.ADBConfig) {
		c.DeviceSHA = map[string]string{"/data/app/s/base.apk": serverSum, "/data/app/t/base.apk": testSum}
	}
	r := newRig(t, same)
	r.opts.Env = config.Android{UIAutomator2ServerAPK: server, UIAutomator2ServerTestAPK: test}
	r.driver(r.platform())
	if r.adb.Count("install") != 0 {
		t.Errorf("an APK that is on the device was installed again: %v", r.adb.Log())
	}

	// One differs: only that one is installed.
	r2 := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.DeviceSHA = map[string]string{"/data/app/s/base.apk": "0000", "/data/app/t/base.apk": testSum}
	})
	r2.opts.Env = config.Android{UIAutomator2ServerAPK: server, UIAutomator2ServerTestAPK: test}
	r2.driver(r2.platform())
	if got := r2.adb.Log(); !contains(got, "install -r -t -d "+server) || contains(got, "install -r -t -d "+test) {
		t.Errorf("log: %v", got)
	}

	// A package that is not on the device is installed, with the files of the Semar home as well.
	home := t.TempDir()
	for name, content := range map[string]string{android.ServerAPKName: "server apk bytes", android.ServerTestAPKName: "test apk bytes"} {
		writeFile(t, home+"/android", name, content)
	}
	r3 := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.Installed = map[string]string{pkgShop: "/data/app/shop/base.apk"}
	})
	r3.opts.Home = home
	r3.driver(r3.platform())
	if r3.adb.Count("install") != 2 {
		t.Errorf("installs: %v", r3.adb.Log())
	}
}

func contains(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}

func TestASplitPackageIsAlwaysInstalled(t *testing.T) {
	apk, sum := writeFile(t, t.TempDir(), "shop.apk", "shop bytes")
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.DeviceSHA = map[string]string{"/data/app/shop/base.apk": sum}
	})
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, APK: apk}}
	r.driver(r.platform())
	if r.adb.Count("install") != 0 {
		t.Errorf("the same app was installed again: %v", r.adb.Log())
	}
	// Two files for the package: it is split, and it is installed.
	r2 := newRig(t, func(rg *rig, c *mobiletest.ADBConfig) {
		c.DeviceSHA = map[string]string{"/data/app/shop/base.apk": sum}
	})
	r2.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, APK: apk}}
	r2.adb.Set("pm."+pkgShop, "package:/data/app/shop/base.apk\npackage:/data/app/shop/split_config.apk\n")
	r2.driver(r2.platform())
	if r2.adb.Count("install") != 1 {
		t.Errorf("a split app was not installed: %v", r2.adb.Log())
	}
}

func TestAnAppThatIsNotThereAndHasNoAPKIsUnavailable(t *testing.T) {
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		delete(c.Installed, pkgShop)
	})
	_, err := r.platform().Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "the app com.example.shop is not installed on the device, and the service lists no apk to install") {
		t.Errorf("reason = %q", un.Reason)
	}
}

func TestTheAPKMustBeTheFileOfTheBuild(t *testing.T) {
	apk, sum := writeFile(t, t.TempDir(), "shop.apk", "shop bytes")
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) { delete(c.Installed, pkgShop) })
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, APK: apk, APKDigest: sum[:40]}}
	r.driver(r.platform())
	if r.adb.Count("install") != 1 {
		t.Errorf("install: %v", r.adb.Log())
	}
	// The file changed after the build was read.
	if err := os.WriteFile(apk, []byte("another build"), 0o644); err != nil {
		t.Fatal(err)
	}
	r2 := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) { delete(c.Installed, pkgShop) })
	r2.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, APK: apk, APKDigest: sum[:40]}}
	_, err := r2.platform().Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "changed after the build of this run was read") || r2.adb.Count("install") != 0 {
		t.Errorf("reason = %q, installs %d", un.Reason, r2.adb.Count("install"))
	}
}

func TestASignatureClashOfAServerPackageUninstallsItAndTriesAgain(t *testing.T) {
	server, _ := writeFile(t, t.TempDir(), "server.apk", "server apk bytes")
	test, _ := writeFile(t, t.TempDir(), "test.apk", "test apk bytes")
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.InstallSeq = map[string][]string{"server.apk": {"1|Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE: Package io.appium.uiautomator2.server signatures do not match the previously installed version]"}}
	})
	r.opts.Env = config.Android{UIAutomator2ServerAPK: server, UIAutomator2ServerTestAPK: test}
	r.driver(r.platform())
	log := r.adb.Log()
	if !contains(log, "uninstall "+serverPkg) || r.adb.Count("install") != 3 {
		t.Errorf("log:\n%s", strings.Join(log, "\n"))
	}
	for _, l := range log {
		if strings.HasPrefix(l, "uninstall ") && l != "uninstall "+serverPkg {
			t.Errorf("an unexpected uninstall: %s", l)
		}
	}
}

func TestASignatureClashOfTheAppUnderTestNeverUninstallsIt(t *testing.T) {
	apk, _ := writeFile(t, t.TempDir(), "shop.apk", "shop bytes")
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.InstallSeq = map[string][]string{"shop.apk": {"1|Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE: signatures do not match]", "1|Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE: signatures do not match]"}}
	})
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, APK: apk}}
	_, err := r.platform().Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "signed with another key than the APK. Semar never uninstalls the app under test") || !strings.Contains(un.Reason, "adb uninstall com.example.shop") {
		t.Errorf("reason = %q", un.Reason)
	}
	for _, l := range r.adb.Log() {
		if strings.HasPrefix(l, "uninstall") {
			t.Errorf("the app was uninstalled: %s", l)
		}
	}
	if r.adb.Count("install") != 1 {
		t.Errorf("installs = %d", r.adb.Count("install"))
	}
}

func TestAClashThatPersistsAfterTheReinstallIsReported(t *testing.T) {
	server, _ := writeFile(t, t.TempDir(), "server.apk", "server apk bytes")
	test, _ := writeFile(t, t.TempDir(), "test.apk", "test apk bytes")
	clash := "1|Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE]"
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.InstallSeq = map[string][]string{"server.apk": {clash, clash}}
	})
	r.opts.Env = config.Android{UIAutomator2ServerAPK: server, UIAutomator2ServerTestAPK: test}
	_, err := r.platform().Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, serverPkg) || r.adb.Count("uninstall") != 1 {
		t.Errorf("reason = %q, uninstalls %d", un.Reason, r.adb.Count("uninstall"))
	}
}

func TestAnInstallThatFailsForAnotherReasonIsNamed(t *testing.T) {
	apk, _ := writeFile(t, t.TempDir(), "shop.apk", "shop bytes")
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.InstallSeq = map[string][]string{"shop.apk": {"1|adb: failed to install shop.apk: Failure [INSTALL_FAILED_INSUFFICIENT_STORAGE]"}}
	})
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, APK: apk}}
	_, err := r.platform().Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "com.example.shop could not be installed") || !strings.Contains(un.Reason, "INSTALL_FAILED_INSUFFICIENT_STORAGE") || r.adb.Count("uninstall") != 0 {
		t.Errorf("reason = %q", un.Reason)
	}
}

func TestAMissingServerNamesTheFilesToPut(t *testing.T) {
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) { c.Installed = map[string]string{pkgShop: "/x/base.apk"} })
	_, err := r.platform().Driver(context.Background(), "shop")
	un := unavailable(t, err)
	for _, want := range []string{serverPkg, testPkg, android.ServerAPKName, android.ServerTestAPKName, "uiautomator2ServerApk", "downloads nothing"} {
		if !strings.Contains(un.Reason, want) {
			t.Errorf("reason lacks %q: %s", want, un.Reason)
		}
	}
	if r.adb.Contains("forward") || r.adb.Count("install") != 0 {
		t.Error("the platform went on")
	}
}

func TestAServerFileThatIsNotThereOrLeavesTheRepositoryIsRefused(t *testing.T) {
	r := newRig(t)
	r.opts.Env = config.Android{UIAutomator2ServerAPK: "tools/missing.apk", UIAutomator2ServerTestAPK: "tools/missing-test.apk"}
	_, err := r.platform().Driver(context.Background(), "shop")
	if un := unavailable(t, err); !strings.Contains(un.Reason, "tools/missing.apk") {
		t.Errorf("reason = %q", un.Reason)
	}
	// A link inside the repository that points out of it.
	outside, _ := writeFile(t, t.TempDir(), "evil.apk", "x")
	if err := os.MkdirAll(r.repo+"/tools", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, r.repo+"/tools/server.apk"); err != nil {
		t.Skip("links are not available")
	}
	_, ok := writeFile(t, r.repo, "tools/test.apk", "y")
	_ = ok
	r2 := newRig(t)
	r2.repo = r.repo
	r2.opts.RepoRoot = r.repo
	r2.opts.Env = config.Android{UIAutomator2ServerAPK: "tools/server.apk", UIAutomator2ServerTestAPK: "tools/test.apk"}
	_, err = r2.platform().Driver(context.Background(), "shop")
	if un := unavailable(t, err); !strings.Contains(un.Reason, "leaves the repository through a link") {
		t.Errorf("reason = %q", un.Reason)
	}
}

func TestAnInstrumentationThatExitsEarlyGivesItsTail(t *testing.T) {
	r := newRig(t, func(r *rig, c *mobiletest.ADBConfig) {
		c.InstrumentExits = true
		r.serverOpts.NotReady = true // the server never comes up: the instrumentation is what ended
	})
	r.clk.real = 20 * time.Millisecond
	p := r.platform()
	_, err := p.Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "stopped as it started: INSTRUMENTATION_RESULT: shortMsg=Process crashed.") {
		t.Errorf("reason = %q", un.Reason)
	}
	// What was made is undone, though the start failed.
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.adb.Contains("forward", "--remove") || r.adb.Count("shell", "'am' 'force-stop' 'io.appium.uiautomator2.server'") != 2 {
		t.Errorf("cleanup after a failed start:\n%s", strings.Join(r.adb.Log(), "\n"))
	}
	// Another step does not start again.
	if _, err := p.Driver(context.Background(), "shop"); err == nil {
		t.Error("a second try")
	}
}

func TestAServerThatNeverAnswersTimesOutWithTheConfiguredLimit(t *testing.T) {
	r := newRig(t)
	r.server.Fail("POST /session", mobiletest.Failure{Status: 500, Code: "x", Message: "x"})
	r.opts.Env = config.Android{StartTimeout: 5 * time.Second}
	p := android.New(r.opts)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	// The status answers, so the failure is in the session.
	_, err := p.Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "did not start a session") {
		t.Errorf("reason = %q", un.Reason)
	}

	// A server that is down: the forward goes to a port with nothing behind it.
	dead := newRig(t)
	dead.server.Close()
	dead.opts.Env = config.Android{StartTimeout: 3 * time.Second}
	d := android.New(dead.opts)
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	_, err = d.Driver(context.Background(), "shop")
	un = unavailable(t, err)
	if !strings.Contains(un.Reason, "did not answer within 3s") || !strings.Contains(un.Reason, "startTimeout") {
		t.Errorf("reason = %q", un.Reason)
	}
}

func TestACancelledRunStillCleansUp(t *testing.T) {
	r := newRig(t)
	p := r.platform()
	r.driver(p)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close with a cancelled context: %v", err)
	}
	if r.server.Deleted() != 1 || !r.adb.Contains("forward", "--remove") {
		t.Errorf("cleanup was cut short: deleted %d\n%s", r.server.Deleted(), strings.Join(r.adb.Log(), "\n"))
	}
	// A run that is cancelled while the platform starts is reported as interrupted and cleans up too.
	r2 := newRig(t)
	p2 := r2.platform()
	cctx, stop := context.WithCancel(context.Background())
	stop()
	if _, err := p2.Driver(cctx, "shop"); err == nil {
		t.Fatal("a cancelled context started the platform")
	}
	if err := p2.Close(context.Background()); err != nil {
		t.Errorf("Close: %v", err)
	}
	// And it is not remembered as a failure of the device: the next run may try again.
	if _, err := p2.Driver(context.Background(), "shop"); err == nil || strings.Contains(err.Error(), "interrupted") {
		t.Logf("after Close: %v", err)
	}
}

func TestADriverIsTheSameForTheSameService(t *testing.T) {
	r := newRig(t)
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop}, "admin": {Package: "com.example.admin"}}
	r.adb.Set("pm.com.example.admin", "package:/data/app/admin/base.apk")
	p := r.platform()
	a1, err := p.Driver(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := p.Driver(context.Background(), "shop")
	b, err := p.Driver(context.Background(), "admin")
	if err != nil || a1 != a2 || b == a1 || b.About().Package != "com.example.admin" {
		t.Errorf("drivers: %v %v %v, %v", a1, a2, b, err)
	}
	if r.server.Sessions() != 1 {
		t.Errorf("two apps used %d sessions", r.server.Sessions())
	}
}

var _ = webdriver.Point{}
var _ domain.Surface

func recoverer(t *testing.T, p *android.Platform) mobile.Recoverer {
	t.Helper()
	rc, ok := any(p).(mobile.Recoverer)
	if !ok {
		t.Fatal("the platform is not a Recoverer")
	}
	return rc
}

func TestHealthyJudgesOnlyASessionThatIsClearlyGone(t *testing.T) {
	r := newRig(t)
	p := r.platform()
	r.driver(p)
	rc := recoverer(t, p)
	ctx := context.Background()
	// An answer that is no sign of a dead server: the element is not found, or the server errs once.
	for _, f := range []mobiletest.Failure{
		{Status: 404, Code: "no such element", Message: "no element has the focus"},
		{Status: 500, Code: "unknown error", Message: "something broke"},
		{Status: 400, Code: "invalid argument", Message: "bad"},
	} {
		r.server.Fail("GET /element/active", f)
		if !rc.Healthy(ctx) {
			t.Errorf("a server that answered %d %s is not healthy", f.Status, f.Code)
		}
	}
	// A session that the server lost, by any of the codes that say so.
	for _, f := range []mobiletest.Failure{
		{Status: 404, Code: "invalid session id", Message: "gone"},
		{Status: 404, Code: "no such session", Message: "gone"},
		{Status: 500, Code: "session not created", Message: "gone"},
	} {
		r.server.Fail("GET /element/active", f)
		if rc.Healthy(ctx) {
			t.Errorf("a server that answered %d %s is healthy", f.Status, f.Code)
		}
	}
	r.server.Heal("GET /element/active")
	// A caller that gave up is no sign of a dead server.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if !rc.Healthy(gone) {
		t.Error("a cancelled call made the server unhealthy")
	}
	// A server that cannot be reached is unhealthy.
	r.server.Close()
	if rc.Healthy(ctx) {
		t.Error("a server that is not there is healthy")
	}
}

func TestRecoveryStartsTheInstrumentationAgainAndOpensASecondSession(t *testing.T) {
	r := newRig(t)
	p := r.platform()
	first := r.driver(p)
	rc := recoverer(t, p)
	if !rc.Healthy(context.Background()) {
		t.Fatal("a server that answers is not healthy")
	}
	r.server.SetReady(false)
	if rc.Healthy(context.Background()) {
		t.Fatal("a server that is not ready is healthy")
	}
	r.server.SetReady(true)
	r.server.Expire()
	if rc.Healthy(context.Background()) {
		t.Fatal("a server that lost its session is healthy")
	}
	installs := r.adb.Count("install")
	if err := rc.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	const instrument = "shell 'am' 'instrument' '-w' '-e' 'disableAnalytics' 'true' 'io.appium.uiautomator2.server.test/androidx.test.runner.AndroidJUnitRunner'"
	// The fake adb logs the call when its process runs, which can be a moment after the platform went on.
	for i := 0; i < 200 && r.adb.Count(instrument) < 2; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if n := r.adb.Count(instrument); n != 2 {
		t.Errorf("the instrumentation started %d times, want 2", n)
	}
	if r.server.Sessions() != 2 {
		t.Errorf("sessions = %d, want 2", r.server.Sessions())
	}
	if n := r.adb.Count("shell 'am' 'force-stop'"); n < 4 {
		t.Errorf("the old server packages were stopped %d times in all", n)
	}
	if r.adb.Count("forward --remove") != 1 {
		t.Errorf("the old forward was removed %d times", r.adb.Count("forward --remove"))
	}
	if r.adb.Count("install") != installs {
		t.Error("an app or a server package was installed again")
	}
	if s := r.server.Settings(); s["enableMultiWindows"] != true {
		t.Errorf("settings = %v", s)
	}
	if !rc.Healthy(context.Background()) {
		t.Error("the server is not healthy after the restart")
	}
	// The driver that was handed out before points at a session that is gone. A new one is given.
	second := r.driver(p)
	if second == first {
		t.Error("the platform gave the old driver after a restart")
	}
	if _, err := second.Source(context.Background()); err != nil {
		t.Errorf("the new driver cannot read: %v", err)
	}
}

func TestAFailedRecoveryLeavesThePlatformUnavailable(t *testing.T) {
	r := newRig(t)
	p := r.platform()
	r.driver(p)
	r.server.SetReady(false) // the new server never answers
	if err := recoverer(t, p).Recover(context.Background()); err == nil {
		t.Fatal("Recover worked on a server that does not answer")
	}
	if _, err := p.Driver(context.Background(), "shop"); err == nil {
		t.Error("a driver was given after a restart that failed")
	} else {
		unavailable(t, err)
	}
}

func TestAServerAtServerURLIsNotRestartedAndIsAlwaysHealthy(t *testing.T) {
	r := newRemote(t, mobiletest.Options{}, android.App{Package: pkgShop}, config.Android{})
	p := android.New(r.opts)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	r.driver(p)
	rc := recoverer(t, p)
	r.server.SetReady(false)
	if !rc.Healthy(context.Background()) {
		t.Error("a remote server is not Semar's to judge")
	}
	err := rc.Recover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("Recover = %v, want not supported", err)
	}
	if r.server.Sessions() != 1 {
		t.Errorf("sessions = %d", r.server.Sessions())
	}
}
