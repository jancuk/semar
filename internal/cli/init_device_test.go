package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

// initRig is an android repository that init checks. Its service is served for real, and the
// offline client that the other tests use is left out so that its probe answers.
func initRig(t *testing.T, o androidOptions) *androidRig {
	t.Helper()
	r := newAndroidRig(t, o)
	r.opts.HTTP = nil
	return r
}

// An app on a device is checked as soon as semar.yaml lists it, before the first scenario is written: that
// is when a person needs to know that the device is ready to record on.
func TestInitChecksTheDeviceOfAnAndroidServiceWithNoScenarioYet(t *testing.T) {
	r := initRig(t, androidOptions{local: true, scenarios: map[string]string{}})
	res := r.must(t, cli.ExitOK, "init")
	contains(t, "stdout", res.stdout,
		"  ok       adb is at "+r.adb.Path+"\n",
		"  ok       local: the device emulator-5554 is ready\n",
	)
}

// A service that is only an app has no address to ask. init reports it, and checks adb and the device.
func TestInitDoesNotProbeTheAddressOfAStandaloneAndroidService(t *testing.T) {
	r := initRig(t, androidOptions{local: true, scenarios: map[string]string{}})
	r.write(t, ".semar/semar.yaml", `version: 1
environments:
  local:
    kind: test
    methods: [GET]
    effects: {mode: test_data}
    services:
      - name: shop-app
        android:
          appPackage: com.example.shop
authProfiles:
  default: {}
`)
	res := r.must(t, cli.ExitOK, "init")
	contains(t, "stdout", res.stdout,
		"  ok       local: shop-app is an app on a device with no address of its own",
		"  ok       adb is at "+r.adb.Path+"\n",
		"  ok       local: the device emulator-5554 is ready\n",
	)
	if strings.Contains(res.stdout, "did not answer") || strings.Contains(res.stdout, "shop-app answers") {
		t.Errorf("init probed an address that the service does not have:\n%s", res.stdout)
	}
}

func TestInitChecksAdbTheDeviceAndTheServerOfAnAndroidScenario(t *testing.T) {
	r := initRig(t, androidOptions{local: true})
	res := r.must(t, cli.ExitOK, "init")
	contains(t, "stdout", res.stdout,
		"  ok       adb is at "+r.adb.Path+"\n",
		"  ok       local: the device emulator-5554 is ready\n",
		"  ok       local: the UiAutomator2 server is already on the device emulator-5554\n",
	)
	// The check reads the device and installs nothing.
	for _, l := range r.adb.Log() {
		if strings.HasPrefix(l, "install") || strings.Contains(l, "instrument") {
			t.Errorf("init changed the device: %s", l)
		}
	}
}

func TestInitFindsTheServerAPKsInTheSemarHome(t *testing.T) {
	r := initRig(t, androidOptions{local: true, adb: mobiletest.ADBConfig{Installed: map[string]string{}}})
	res := r.must(t, cli.ExitUndecided, "init")
	contains(t, "stdout", res.stdout,
		"  missing  local: the UiAutomator2 server APKs were not found: put appium-uiautomator2-server.apk and appium-uiautomator2-server-test.apk in the Semar home under android/",
		"Semar downloads nothing")
	dir := filepath.Join(r.opts.Home, "android")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"appium-uiautomator2-server.apk", "appium-uiautomator2-server-test.apk"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("apk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res = r.must(t, cli.ExitOK, "init")
	contains(t, "stdout", res.stdout, "  ok       local: the UiAutomator2 server APKs are at "+dir+"\n")
}

func TestInitSaysThatNoDeviceIsThere(t *testing.T) {
	r := initRig(t, androidOptions{local: true, adb: mobiletest.ADBConfig{Devices: []string{" "}}})
	res := r.must(t, cli.ExitUndecided, "init")
	contains(t, "stdout", res.stdout, "  missing  local: ", "no device")
}

func TestInitSaysThatAdbIsNotFound(t *testing.T) {
	r := initRig(t, androidOptions{local: true})
	t.Setenv("SEMAR_ADB_BIN", "")
	t.Setenv("PATH", "")
	t.Setenv("HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	res := r.must(t, cli.ExitUndecided, "init")
	contains(t, "stdout", res.stdout, "  missing  adb was not found: ", "SEMAR_ADB_BIN")
	if calls := r.adb.Calls(); len(calls) != 0 {
		t.Errorf("an adb that was not named was run: %v", calls)
	}
}

func TestInitAsksTheServerAtServerUrl(t *testing.T) {
	r := initRig(t, androidOptions{})
	res := r.must(t, cli.ExitOK, "init")
	contains(t, "stdout", res.stdout, "  ok       local: the device server at "+r.server.URL+" answers\n")
	if strings.Contains(res.stdout, "adb") {
		t.Errorf("init looked for adb although the environment has a server:\n%s", res.stdout)
	}

	down := initRig(t, androidOptions{server: mobiletest.Options{NotReady: true}})
	res = down.must(t, cli.ExitUndecided, "init")
	contains(t, "stdout", res.stdout, "  missing  local: the device server at "+down.server.URL+" is not ready\n")
}
