package cli_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

func TestInitSaysWhichActivityTheAppStarts(t *testing.T) {
	for name, service := range map[string]string{
		"no appActivity":  "",
		"a good activity": "appActivity: .MainActivity",
	} {
		r := initRig(t, androidOptions{local: true, service: service})
		res := r.must(t, cli.ExitOK, "init")
		contains(t, name+": stdout", res.stdout, "  ok       local: shop-app starts com.example.shop/.MainActivity\n")
	}
}

func TestInitCatchesAnActivityThatIsNotInThePackage(t *testing.T) {
	r := initRig(t, androidOptions{
		local:   true,
		service: "appActivity: .ui.Nope",
		adb: mobiletest.ADBConfig{Canned: []mobiletest.Canned{
			{Match: "'resolve-activity' '--brief' '-n' 'com.example.shop/.ui.Nope'", Out: "No activity found\n"},
		}},
	})
	res := r.must(t, cli.ExitUndecided, "init")
	contains(t, "stdout", res.stdout,
		"  missing  local: the activity .ui.Nope is not in com.example.shop: the launcher is com.example.shop/.MainActivity; set appActivity to .MainActivity or leave it empty\n")
	for _, l := range r.adb.Log() {
		if strings.Contains(l, "am\t") || strings.Contains(l, "'am' 'start'") {
			t.Errorf("init started the app: %s", l)
		}
	}
}

func TestInitNotesAnAppThatIsNotOnTheDevice(t *testing.T) {
	server := map[string]string{
		"io.appium.uiautomator2.server":      "/data/app/s/base.apk",
		"io.appium.uiautomator2.server.test": "/data/app/t/base.apk",
	}
	withAPK := initRig(t, androidOptions{local: true, apk: "PK", adb: mobiletest.ADBConfig{Installed: server}})
	res := withAPK.must(t, cli.ExitOK, "init")
	contains(t, "stdout", res.stdout, "  info     local: shop-app (com.example.shop) is not on the device yet: Semar installs ")

	noAPK := initRig(t, androidOptions{local: true, adb: mobiletest.ADBConfig{Installed: server}})
	res = noAPK.must(t, cli.ExitOK, "init")
	contains(t, "stdout", res.stdout, "  info     local: shop-app (com.example.shop) is not on the device, so its activity was not checked: install it, or name an apk\n")
}
