package cli_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

const androidPackage = "com.example.shop"

const androidScreens = `apiVersion: semar/v1
kind: Screens
app: shop-app
screens:
  SignIn:
    email: { id: email }
    password: { id: password }
    submit: { desc: Sign in }
    welcome: { id: welcome }
`

const androidScenarioFile = `Feature: Signing in

  @id:AND-1 @ticket:SHOP-1 @surface:android @auth:support-agent
  Scenario: A customer signs in
    The app greets the customer

    Given Start the app
    # step: start
    When Tap sign in
    # step: sign-in
    Then the element welcome equals "Hi Ana"
    # person
`

const androidScriptFile = `apiVersion: semar/v1
kind: Script
scenario: AND-1
authoredBy: { instance: person, model: "", harness: file }
steps:
  - step: start
    mobile: { service: shop-app, app: shop-app, do: launch }
  - step: sign-in
    mobile: { service: shop-app, app: shop-app, do: tap, screen: SignIn, element: submit }
`

const androidSecretScenario = `Feature: Signing in with a password

  @id:AND-2 @ticket:SHOP-1 @surface:android @auth:support-agent
  Scenario: A customer signs in with a password
    The app greets the customer

    Given Start the app
    # step: start
    When Type the password
    # step: password
    And Tap sign in
    # step: sign-in
    Then the element welcome equals "Hi Ana"
    # person
`

const androidSecretScript = `apiVersion: semar/v1
kind: Script
scenario: AND-2
authoredBy: { instance: person, model: "", harness: file }
steps:
  - step: start
    mobile: { service: shop-app, app: shop-app, do: launch }
  - step: password
    mobile: { service: shop-app, app: shop-app, do: type, screen: SignIn, element: password, secret: env:SHOP_PASSWORD }
  - step: sign-in
    mobile: { service: shop-app, app: shop-app, do: tap, screen: SignIn, element: submit }
`

const androidOpenScenario = `Feature: Deep link

  @id:AND-3 @ticket:SHOP-1 @surface:android @auth:support-agent
  Scenario: A link opens the app
    The app opens at the link

    Given Start the app
    # step: start
    When Open the order link
    # step: link
    Then the element welcome exists
    # person
`

const androidOpenScript = `apiVersion: semar/v1
kind: Script
scenario: AND-3
authoredBy: { instance: person, model: "", harness: file }
steps:
  - step: start
    mobile: { service: shop-app, app: shop-app, do: launch }
  - step: link
    mobile: { service: shop-app, app: shop-app, do: open, link: "shop://orders/1", screen: SignIn }
`

// The screens that the fake device shows: the sign-in form, and what the app shows after the tap.
func signedOutScreen() string {
	return mobiletest.Hierarchy(mobiletest.Widget("android.widget.FrameLayout", nil, "[0,0][1080,2400]",
		mobiletest.Widget("android.widget.EditText", map[string]string{"resource-id": androidPackage + ":id/email"}, "[100,300][980,420]"),
		mobiletest.Widget("android.widget.EditText", map[string]string{"resource-id": androidPackage + ":id/password", "password": "true"}, "[100,500][980,620]"),
		mobiletest.Widget("android.widget.Button", map[string]string{"content-desc": "Sign in", "clickable": "true"}, "[100,900][980,1020]")))
}

func signedInScreen(greeting string) string {
	return mobiletest.Hierarchy(mobiletest.Widget("android.widget.FrameLayout", nil, "[0,0][1080,2400]",
		mobiletest.Widget("android.widget.TextView", map[string]string{"resource-id": androidPackage + ":id/welcome", "text": greeting}, "[100,300][980,420]"),
		mobiletest.Widget("android.widget.Button", map[string]string{"content-desc": "Sign in", "clickable": "true"}, "[100,900][980,1020]")))
}

// androidRig is a repository with an android service, a fake W3C server for the device, and, for a
// run through adb, a fake adb. Nothing of it reaches a real device.
type androidRig struct {
	*repo
	server *mobiletest.Server
	adb    *mobiletest.ADB
	apk    string
	// taps counts the taps of the sign-in button.
	taps atomic.Int64
}

type androidOptions struct {
	// local runs through the fake adb. The default is serverUrl.
	local bool
	adb   mobiletest.ADBConfig
	// apk is the content of the app file. Empty lists none.
	apk string
	// service is extra YAML under the android key of the service, such as "schemes: [shop]".
	service string
	// env is extra YAML under android: of the environment.
	env string
	// greeting is what the app says after the tap. The default is the one that the scenario expects.
	greeting string
	server   mobiletest.Options
	// production and readOnly change the environment.
	production, readOnly bool
	scenarios            map[string]string
}

func newAndroidRig(t *testing.T, o androidOptions) *androidRig {
	t.Helper()
	r := &androidRig{repo: newRepo(t, repoOptions{})}
	r.opts.Sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	if o.greeting == "" {
		o.greeting = "Hi Ana"
	}
	r.server = mobiletest.NewServer(t, o.server)
	r.server.SetSource(signedOutScreen())
	r.server.OnTap(func(_, y int) {
		if y >= 900 && y <= 1020 {
			r.taps.Add(1)
			r.server.SetSource(signedInScreen(o.greeting))
		}
	})
	u, err := url.Parse(r.server.URL)
	if err != nil {
		t.Fatal(err)
	}

	envAndroid := "      " + strings.ReplaceAll(strings.TrimSpace(o.env), "\n", "\n      ")
	if o.local {
		port, _ := strconv.Atoi(u.Port())
		cfg := o.adb
		cfg.Port = port
		if cfg.Devices == nil {
			cfg.Devices = []string{"emulator-5554\tdevice product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1"}
		}
		if cfg.Installed == nil {
			cfg.Installed = map[string]string{
				"io.appium.uiautomator2.server":      "/data/app/s/base.apk",
				"io.appium.uiautomator2.server.test": "/data/app/t/base.apk",
				androidPackage:                       "/data/app/shop/base.apk",
			}
		}
		r.adb = mobiletest.NewADB(t, cfg)
		t.Setenv("SEMAR_ADB_BIN", r.adb.Path)
	} else {
		envAndroid = "      serverUrl: " + r.server.URL + "\n" + strings.TrimRight(envAndroid, " ")
	}
	envAndroid = strings.TrimRight(envAndroid, " \n")

	service := "          appPackage: " + androidPackage + "\n"
	if o.apk != "" {
		r.apk = filepath.Join(r.dir, "app", "app-debug.apk")
		r.write(t, "app/app-debug.apk", o.apk)
		service += "          apk: app/app-debug.apk\n"
	}
	if o.service != "" {
		service += "          " + strings.ReplaceAll(strings.TrimSpace(o.service), "\n", "\n          ") + "\n"
	}
	host := strings.TrimPrefix(r.ordersURL, "http://")
	r.rewriteConfig(t, func(s string) string {
		s = strings.Replace(s, `hosts: ["`+host+`"]`, `hosts: ["`+host+`", "`+u.Host+`"]`, 1)
		if o.production {
			s = strings.Replace(s, "kind: test", "kind: production", 1)
		}
		if o.readOnly || o.production {
			s = strings.Replace(s, "mode: test_data", "mode: read_only", 1)
		}
		block := "    android:\n" + envAndroid + "\n"
		s = strings.Replace(s, "    effects:", block+"    effects:", 1)
		return strings.Replace(s, "authProfiles:", "      - name: shop-app\n        baseUrl: "+r.ordersURL+"\n        android:\n"+service+"authProfiles:", 1)
	})
	r.write(t, ".semar/screens/shop-app.yaml", androidScreens)
	scenarios := o.scenarios
	if scenarios == nil {
		scenarios = map[string]string{"AND-1": androidScenarioFile + "\x00" + androidScriptFile}
	}
	for id, both := range scenarios {
		feature, script, _ := strings.Cut(both, "\x00")
		r.write(t, ".semar/scenarios/"+id+".feature", feature)
		r.write(t, "scripts/"+id+".yaml", script)
		r.must(t, cli.ExitOK, "script", "add", r.path("scripts", id+".yaml"))
	}
	return r
}

func (r *androidRig) write(t *testing.T, rel, text string) {
	t.Helper()
	r.writeRel(t, filepath.ToSlash(rel), text)
}

func hexSHA(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// adbLogIs says whether the calls of adb, in order, are those given. A word of a call that is not
// worth naming is left out by naming only the prefix.
func adbLog(r *androidRig) string { return strings.Join(r.adb.Log(), "\n") }

func TestAndroidPassesThroughAServerUrl(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	res := r.must(t, cli.ExitOK, "run", "--scenario", "AND-1")
	contains(t, "stdout", res.stdout, "AND-1  passed")
	if strings.Contains(res.stdout, "Gaps") {
		t.Errorf("a scenario that can run has a gap:\n%s", res.stdout)
	}
	report := r.lastReport(t)
	contains(t, "the report", report, "## Device steps", "Tap sign in (tap SignIn)", "[Screenshot](screenshots/AND-1/")
	if _, err := os.Stat(filepath.Join(r.latestRun(t), "screenshots", "AND-1")); err != nil {
		t.Errorf("no screenshots in the run: %v", err)
	}
	if r.taps.Load() != 1 {
		t.Errorf("the button was tapped %d times, want 1", r.taps.Load())
	}
	executed := r.server.ExecutedScripts()
	if len(executed) < 2 || executed[0].Script != "mobile: terminateApp" || executed[len(executed)-1].Script != "mobile: activateApp" {
		t.Errorf("the lifecycle of the app through the server: %+v", executed)
	}
	// The run is closed: the session is deleted.
	if r.server.Deleted() != 1 {
		t.Errorf("the session was deleted %d times, want 1", r.server.Deleted())
	}
}

func TestAndroidFailingCheckExitsOne(t *testing.T) {
	r := newAndroidRig(t, androidOptions{greeting: "Hi Bob"})
	res := r.must(t, cli.ExitFailed, "run", "--scenario", "AND-1")
	contains(t, "stdout", res.stdout, "AND-1  failed")
	contains(t, "the report", r.lastReport(t), `Hi Bob`)
}

func TestAndroidMissingElementIsInconclusiveAndScreensRepairFixesIt(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	r.edit(t, ".semar/screens/shop-app.yaml", func(s string) string {
		return strings.Replace(s, "submit: { desc: Sign in }", "submit: { role: link, name: Sign in }", 1)
	})
	r.must(t, cli.ExitUndecided, "run", "--scenario", "AND-1")
	contains(t, "the report", r.lastReport(t), "an element was not found: either the product changed or the script is out of date")

	res := r.must(t, cli.ExitOK, "screens", "repair", "latest")
	contains(t, "the proposal", res.stdout, "shop-app SignIn.submit was not found", `old: { role: link, name: "Sign in" }`, `1. { role: button, name: "Sign in" }`)
	if r.taps.Load() != 0 {
		t.Errorf("a step that found nothing tapped %d times", r.taps.Load())
	}
	res = r.must(t, cli.ExitOK, "screens", "repair", "latest", "--apply", "SignIn.submit=1")
	contains(t, "the repair", res.stdout, "SignIn.submit in .semar/screens/shop-app.yaml")
	data, err := os.ReadFile(r.path(".semar", "screens", "shop-app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "role: link") {
		t.Errorf("the screens file was not repaired:\n%s", data)
	}
	r.must(t, cli.ExitOK, "run", "--scenario", "AND-1")
}

func localRun(t *testing.T, o androidOptions) *androidRig {
	t.Helper()
	o.local = true
	return newAndroidRig(t, o)
}

func TestAndroidLocalModeStartsInOrderAndCleansUp(t *testing.T) {
	r := localRun(t, androidOptions{})
	r.must(t, cli.ExitOK, "run", "--scenario", "AND-1")
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
		"shell 'am' 'force-stop' 'com.example.shop'",
	}
	got := r.adb.Log()
	if len(got) < len(want)+3 {
		t.Fatalf("adb log:\n%s", strings.Join(got, "\n"))
	}
	if strings.Join(got[:len(want)], "\n") != strings.Join(want, "\n") {
		t.Errorf("adb log:\n%s\nwant it to start with:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !r.adb.Contains("shell", "'am' 'start' '-W' '--activity-clear-task' '-n' 'com.example.shop/.MainActivity' '-a' 'android.intent.action.MAIN' '-c' 'android.intent.category.LAUNCHER'") &&
		!r.adb.Contains("shell", "'am' 'start' '-W' '--activity-clear-task' '-n' 'com.example.shop/.MainActivity'") {
		// The launcher activity is read from the device, and then started.
		if r.adb.Count("shell") < len(want) {
			t.Errorf("the app was not started:\n%s", adbLog(r))
		}
	}
	// The end of the run undoes what the run made: both server packages are stopped, the forward is
	// removed, and the session is deleted. Nothing is uninstalled, and the daemon stays.
	tail := got[len(got)-3:]
	if tail[0] != "shell 'am' 'force-stop' 'io.appium.uiautomator2.server'" || tail[1] != "shell 'am' 'force-stop' 'io.appium.uiautomator2.server.test'" || !strings.HasPrefix(tail[2], "forward --remove tcp:") {
		t.Errorf("the cleanup:\n%s", strings.Join(tail, "\n"))
	}
	if r.server.Deleted() != 1 {
		t.Errorf("the session was deleted %d times, want 1", r.server.Deleted())
	}
	for _, l := range got {
		if strings.Contains(l, "uninstall") || strings.Contains(l, "kill-server") {
			t.Errorf("unexpected call: %s", l)
		}
	}
}

func TestAndroidSecondRunSkipsBothInstalls(t *testing.T) {
	server, test, app := "/data/app/s/base.apk", "/data/app/t/base.apk", "/data/app/shop/base.apk"
	r := localRun(t, androidOptions{apk: "an apk", adb: mobiletest.ADBConfig{Installed: map[string]string{}}})
	home := r.opts.Home
	serverAPK, testAPK := "the server", "the test package"
	for name, content := range map[string]string{"appium-uiautomator2-server.apk": serverAPK, "appium-uiautomator2-server-test.apk": testAPK} {
		p := filepath.Join(home, "android", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.must(t, cli.ExitOK, "run", "--scenario", "AND-1")
	first := r.adb.Count("install")
	if first != 3 {
		t.Fatalf("the first run made %d installs, want 3 (the server, its test package, and the app):\n%s", first, adbLog(r))
	}
	// The device now has what the first run installed, with the same hashes.
	for pkg, path := range map[string]string{"io.appium.uiautomator2.server": server, "io.appium.uiautomator2.server.test": test, androidPackage: app} {
		r.adb.Set("pm."+pkg, "package:"+path)
	}
	for path, content := range map[string]string{server: serverAPK, test: testAPK, app: "an apk"} {
		r.adb.Set("sha."+strings.ReplaceAll(path, "/", "_"), hexSHA(content))
	}
	r.must(t, cli.ExitOK, "run", "--scenario", "AND-1")
	if n := r.adb.Count("install"); n != first {
		t.Errorf("the second run installed again: %d installs after %d:\n%s", n, first, adbLog(r))
	}
}

// blockedRun runs the scenario and says that it was blocked with the reason, and that no step reached a
// device.
func (r *androidRig) blockedRun(t *testing.T, reason string) result {
	t.Helper()
	res := r.must(t, cli.ExitUndecided, "run", "--scenario", "AND-1")
	contains(t, "stdout", res.stdout, "AND-1  blocked")
	contains(t, "the report", r.lastReport(t), reason)
	if reqs := r.server.Requests(); len(reqs) != 0 {
		t.Errorf("the device server got %d requests, want none: %+v", len(reqs), reqs)
	}
	if r.taps.Load() != 0 {
		t.Errorf("a blocked scenario tapped %d times", r.taps.Load())
	}
	return res
}

func TestAndroidWithNoAdbIsBlocked(t *testing.T) {
	r := localRun(t, androidOptions{})
	// The machine's own SDK must not be found: no program on PATH, and no home to look in.
	t.Setenv("SEMAR_ADB_BIN", "")
	t.Setenv("PATH", "")
	t.Setenv("HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	res := r.blockedRun(t, "adb was not found")
	contains(t, "the header", res.stdout, "Gaps      AND-1 is an Android scenario, which cannot run here: adb was not found")
	if n := len(r.adb.Calls()); n != 0 {
		t.Errorf("a fake adb that was not named was run %d times", n)
	}
}

func TestAndroidWithNoDeviceIsBlocked(t *testing.T) {
	r := localRun(t, androidOptions{adb: mobiletest.ADBConfig{Devices: []string{" "}}})
	res := r.blockedRun(t, "no device")
	// A missing device is known only when the device is asked, so the header names no gap.
	if strings.Contains(res.stdout, "Gaps") {
		t.Errorf("the header names a gap for a device that adb was never asked about:\n%s", res.stdout)
	}
	if r.adb.Count("shell") != 0 {
		t.Errorf("a command was sent to a device that is not there:\n%s", adbLog(r))
	}
}

func TestAndroidWithTwoEmulatorsIsBlocked(t *testing.T) {
	r := localRun(t, androidOptions{adb: mobiletest.ADBConfig{Devices: []string{
		"emulator-5554\tdevice product:sdk model:sdk device:emu transport_id:1",
		"emulator-5556\tdevice product:sdk model:sdk device:emu transport_id:2",
	}}})
	r.blockedRun(t, "emulator-5554")
}

func TestAndroidUsesAPhysicalDeviceOnlyWhenNamed(t *testing.T) {
	phone := "R58M123ABC\tdevice product:flame model:Pixel_4 device:flame transport_id:3"
	r := localRun(t, androidOptions{adb: mobiletest.ADBConfig{Devices: []string{phone}}})
	r.blockedRun(t, "R58M123ABC")
	if r.adb.Count("shell") != 0 {
		t.Errorf("a command was sent to a phone that was not named:\n%s", adbLog(r))
	}

	named := localRun(t, androidOptions{adb: mobiletest.ADBConfig{Devices: []string{phone}}, env: "device: R58M123ABC"})
	named.must(t, cli.ExitOK, "run", "--scenario", "AND-1")
	if !named.adb.Contains("-s", "R58M123ABC") && named.adb.Count("shell") == 0 {
		t.Errorf("the named phone was not used:\n%s", adbLog(named))
	}
}

func TestAndroidUnauthorizedDeviceIsBlocked(t *testing.T) {
	r := localRun(t, androidOptions{adb: mobiletest.ADBConfig{Devices: []string{"emulator-5554\tunauthorized transport_id:1"}}})
	r.blockedRun(t, "unauthorized")
	if r.adb.Count("shell") != 0 {
		t.Errorf("a command was sent to a device that is unauthorized:\n%s", adbLog(r))
	}
}

func TestAndroidRefusesAReadOnlyAndAProductionEnvironment(t *testing.T) {
	for name, o := range map[string]androidOptions{
		"read only":  {readOnly: true},
		"production": {production: true},
	} {
		t.Run(name, func(t *testing.T) {
			r := newAndroidRig(t, o)
			args := []string{"run", "--scenario", "AND-1"}
			if o.production {
				args = append(args, "--env", "local")
			}
			res := r.must(t, cli.ExitUndecided, args...)
			contains(t, "stdout", res.stdout, "AND-1  ", "Gaps      AND-1 is an Android scenario, which cannot run here: the environment is "+name+", and the app on a device makes requests that Semar cannot see")
			report := r.lastReport(t)
			contains(t, "the report", report, "makes requests that Semar cannot see")
			if reqs := r.server.Requests(); len(reqs) != 0 {
				t.Errorf("the device server got %d requests", len(reqs))
			}
		})
	}
}

func TestAndroidRefusesACustomSchemeThatIsNotListed(t *testing.T) {
	scenarios := map[string]string{"AND-3": androidOpenScenario + "\x00" + androidOpenScript}
	r := newAndroidRig(t, androidOptions{scenarios: scenarios})
	r.must(t, cli.ExitUndecided, "run", "--scenario", "AND-3")
	contains(t, "the report", r.lastReport(t), "the scheme shop is not in the schemes of service shop-app")
	for _, e := range r.server.ExecutedScripts() {
		if e.Script == "mobile: deepLink" {
			t.Errorf("a link of a scheme that is not listed was opened: %+v", e)
		}
	}

	listed := newAndroidRig(t, androidOptions{scenarios: scenarios, service: "schemes: [shop]"})
	listed.server.SetSource(signedInScreen("Hi Ana"))
	listed.must(t, cli.ExitOK, "run", "--scenario", "AND-3")
	opened := false
	for _, e := range listed.server.ExecutedScripts() {
		if e.Script == "mobile: deepLink" && e.Args["url"] == "shop://orders/1" {
			opened = true
		}
	}
	if !opened {
		t.Errorf("the listed scheme was not opened: %+v", listed.server.ExecutedScripts())
	}
}

func TestAndroidSecretIsNotInEvidenceReportOrAdbLog(t *testing.T) {
	const password = "pw-Zk93-never-written"
	t.Setenv("SHOP_PASSWORD", password)
	scenarios := map[string]string{"AND-2": androidSecretScenario + "\x00" + androidSecretScript}
	r := localRun(t, androidOptions{scenarios: scenarios})
	r.must(t, cli.ExitOK, "run", "--scenario", "AND-2")
	// The device did get the password: it is the point of the step.
	typed := r.server.Typed()
	if len(typed) != 1 || typed[0] != password {
		t.Fatalf("typed = %q, want the password once", typed)
	}
	r.assertNoSecret(t, []string{password})
	for _, l := range r.adb.Log() {
		for _, n := range forms(password) {
			if strings.Contains(l, n) {
				t.Errorf("the adb log holds the secret: %s", l)
			}
		}
	}
	contains(t, "the report", r.lastReport(t), "## Device steps")
}

func TestAndroidRetryResetsTheApp(t *testing.T) {
	r := localRun(t, androidOptions{})
	var tapped atomic.Int64
	r.server.OnTap(func(_, y int) {
		if y < 900 || y > 1020 {
			return
		}
		greeting := "Hi Bob"
		if tapped.Add(1) > 1 {
			greeting = "Hi Ana"
		}
		r.server.SetSource(signedInScreen(greeting))
	})
	res := r.must(t, cli.ExitOK, "run", "--scenario", "AND-1", "--retries", "1")
	contains(t, "stdout", res.stdout, "AND-1  passed")
	if n := r.adb.Count("shell", "'am' 'force-stop' 'com.example.shop'"); n != 2 {
		t.Errorf("the app was reset %d times, want 2 (once for each attempt):\n%s", n, adbLog(r))
	}
}

func TestAndroidApkChangeMakesTheOldResultStaleWithNoDevice(t *testing.T) {
	r := newAndroidRig(t, androidOptions{apk: "an apk, version one"})
	r.ledgerFile(t, obligations("SHOP-1", entry("SHOP-1/sign-in", "A customer signs in", "covers: [AND-1]")))
	res := r.must(t, cli.ExitOK, "run", "--scenario", "AND-1")
	contains(t, "stdout", res.stdout, "build: shop-app "+hexSHA("an apk, version one")[:40]+" (the digest of the APK file of the service, not read from the service)")
	run, err := os.ReadFile(filepath.Join(r.latestRun(t), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "run.json", string(run), `"apk": true`, hexSHA("an apk, version one")[:40])
	before := len(r.server.Requests())

	r.write(t, "app/app-debug.apk", "an apk, version two")
	status := r.must(t, cli.ExitUndecided, "status", "local")
	contains(t, "the status", squash(status.stdout), "stale 1 → semar run", "AND-1 ran on shop-app "+hexSHA("an apk, version one")[:7]+", and the environment runs "+hexSHA("an apk, version two")[:7])
	if after := len(r.server.Requests()); after != before {
		t.Errorf("status sent %d requests to the device server", after-before)
	}
}

func TestAnAPIRunInTheSameRepositoryStartsNoAdb(t *testing.T) {
	r := localRun(t, androidOptions{})
	r.addScript(t)
	r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	if calls := r.adb.Calls(); len(calls) != 0 {
		t.Errorf("an API run called adb: %v", calls)
	}
	if reqs := r.server.Requests(); len(reqs) != 0 {
		t.Errorf("an API run reached the device server: %v", reqs)
	}
}

func TestAndroidInterruptCleansUp(t *testing.T) {
	r := localRun(t, androidOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.server.OnTap(func(_, y int) {
		if y >= 900 && y <= 1020 {
			cancel()
		}
	})
	res := r.semar(t, ctx, "run", "--scenario", "AND-1")
	if res.code != cli.ExitUndecided {
		t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", res.code, cli.ExitUndecided, res.stdout, res.stderr)
	}
	contains(t, "the output", res.stdout+res.stderr, "interrupted")
	log := r.adb.Log()
	if len(log) < 3 {
		t.Fatalf("adb log:\n%s", strings.Join(log, "\n"))
	}
	tail := log[len(log)-3:]
	if tail[0] != "shell 'am' 'force-stop' 'io.appium.uiautomator2.server'" || tail[1] != "shell 'am' 'force-stop' 'io.appium.uiautomator2.server.test'" || !strings.HasPrefix(tail[2], "forward --remove tcp:") {
		t.Errorf("an interrupted run did not clean up:\n%s", strings.Join(log, "\n"))
	}
	if r.server.Deleted() != 1 {
		t.Errorf("the session was deleted %d times, want 1", r.server.Deleted())
	}
}

func TestAnAndroidScenarioWithNoAndroidServiceHasAGapAndIsBlocked(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	r.rewriteConfig(t, func(s string) string {
		i := strings.Index(s, "      - name: shop-app")
		j := strings.Index(s, "authProfiles:")
		return s[:i] + s[j:]
	})
	res := r.must(t, cli.ExitUndecided, "run", "--scenario", "AND-1")
	contains(t, "stdout", res.stdout, "AND-1 is an Android scenario, which cannot run here: no service of the environment has an android app", "AND-1  blocked")
	contains(t, "the report", r.lastReport(t), "service shop-app has no android app in this environment")
}
