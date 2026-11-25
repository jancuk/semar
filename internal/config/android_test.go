package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
)

const androidHead = "version: 1\nenvironments:\n  uat:\n    kind: test\n    hosts: [shop.uat.example.com, \"127.0.0.1:4723\", farm.example.com]\n    methods: [GET]\n    effects: {mode: test_data}\n"

// androidText builds a configuration with the given android block of the environment, indented by
// the caller, and the given android block of the service.
func androidText(env, svc string) string {
	s := androidHead
	if env != "" {
		s += "    android:\n" + env
	}
	s += "    services:\n      - name: shop-app\n        baseUrl: https://shop.uat.example.com\n"
	if svc != "" {
		s += "        android:\n" + svc
	}
	return s
}

func TestAndroidKeysLoad(t *testing.T) {
	env := `      device: emulator-5554
      timeout: 7s
      startTimeout: 90s
      uiautomator2ServerApk: tools/uia2-server.apk
      uiautomator2ServerTestApk: /opt/uia2-server-test.apk
`
	svc := `          appPackage: com.example.shop
          appActivity: .MainActivity
          apk: app/build/outputs/apk/debug/app-debug.apk
          reset: clear
          schemes: [shop, shop-beta]
          permissions: [android.permission.CAMERA]
`
	cfg, err := loadText(t, androidText(env, svc))
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.Environments["uat"]
	if e.Android.Device != "emulator-5554" || e.Android.CheckTimeout() != 7*time.Second || e.Android.StartLimit() != 90*time.Second {
		t.Errorf("android = %+v", e.Android)
	}
	a := e.Services[0].Android
	if a == nil || a.AppPackage != "com.example.shop" || a.ResetMode() != config.ResetClear || len(a.Schemes) != 2 || a.Permissions[0] != "android.permission.CAMERA" {
		t.Fatalf("service android = %+v", a)
	}
}

func TestAndroidDefaultsStayZeroInTheFile(t *testing.T) {
	cfg, err := loadText(t, androidText("", "          appPackage: com.example.shop\n"))
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.Environments["uat"]
	if e.Android.Timeout != 0 || e.Android.StartTimeout != 0 {
		t.Errorf("the defaults must stay zero: %+v", e.Android)
	}
	if e.Android.CheckTimeout() != config.DefaultAndroidTimeout || e.Android.StartLimit() != config.DefaultAndroidStartTimeout {
		t.Errorf("the defaults are %s and %s", e.Android.CheckTimeout(), e.Android.StartLimit())
	}
	if e.Services[0].Android.ResetMode() != config.ResetRestart {
		t.Errorf("reset defaults to %q", e.Services[0].Android.ResetMode())
	}
	var none config.Service
	if none.Android != nil {
		t.Error("a service with no android block has one")
	}
}

func TestATopLevelAndroidKeyStaysAcceptedAndIgnored(t *testing.T) {
	if _, err := loadText(t, androidText("", "")+"android: {anything: 1}\n"); err != nil {
		t.Fatal(err)
	}
}

func TestAndroidEnvironmentRules(t *testing.T) {
	for name, c := range map[string]struct{ env, want string }{
		"a device with a space":           {"      device: \"a b\"\n", "android.device"},
		"a device with a quote":           {"      device: \"a'b\"\n", "android.device"},
		"a relative serverUrl":            {"      serverUrl: /wd/hub\n", "android.serverUrl must be an absolute"},
		"a serverUrl with a user":         {"      serverUrl: http://u:p@127.0.0.1:4723\n", "no user name"},
		"a serverUrl with a query":        {"      serverUrl: http://127.0.0.1:4723/?k=v\n", "no query"},
		"a serverUrl host not in hosts":   {"      serverUrl: https://other.example.com\n", "not in hosts"},
		"a serverUrl on another scheme":   {"      serverUrl: ftp://127.0.0.1:4723\n", "android.serverUrl must be an absolute"},
		"a user without a key":            {"      serverUrl: https://farm.example.com\n      serverUser: env:U\n", "both serverUser and serverKey"},
		"a key that is the secret itself": {"      serverUrl: https://farm.example.com\n      serverUser: env:U\n      serverKey: hunter2\n", "serverKey must be a reference"},
		"a user without a serverUrl":      {"      serverUser: env:U\n      serverKey: env:K\n", "serverUser needs serverUrl"},
		"basic auth over http":            {"      serverUrl: http://farm.example.com\n      serverUser: env:U\n      serverKey: env:K\n", "https"},
		"capabilities without serverUrl":  {"      capabilities: {\"appium:deviceName\": Pixel}\n", "capabilities needs serverUrl"},
		"a reserved capability":           {"      serverUrl: http://127.0.0.1:4723\n      capabilities: {platformName: iOS}\n", "set by Semar"},
		"a reserved package capability":   {"      serverUrl: http://127.0.0.1:4723\n      capabilities: {\"appium:appPackage\": evil.pkg}\n", "set by Semar"},
		"a list capability":               {"      serverUrl: http://127.0.0.1:4723\n      capabilities: {\"appium:x\": [1]}\n", "must be a string"},
		"one server apk":                  {"      uiautomator2ServerApk: a.apk\n", "both uiautomator2ServerApk and uiautomator2ServerTestApk"},
		"a server apk that leaves":        {"      uiautomator2ServerApk: ../a.apk\n      uiautomator2ServerTestApk: b.apk\n", "uiautomator2ServerApk must be a path inside"},
		"a server apk with serverUrl":     {"      serverUrl: http://127.0.0.1:4723\n      uiautomator2ServerApk: a.apk\n      uiautomator2ServerTestApk: b.apk\n", "does not use adb"},
		"a negative timeout":              {"      timeout: -1s\n", "android.timeout"},
		"a timeout over a minute":         {"      timeout: 2m\n", "android.timeout"},
		"a start timeout over five":       {"      startTimeout: 6m\n", "android.startTimeout"},
		"an adb path, which has no place": {"      adb: /usr/bin/adb\n", "adb"},
		"an unknown key":                  {"      colour: red\n", "colour"},
	} {
		_, err := loadText(t, androidText(c.env, ""))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error with %q, got %v", name, c.want, err)
		}
	}
	good := "      serverUrl: https://farm.example.com\n      serverUser: env:FARM_USER\n      serverKey: env:FARM_KEY\n      capabilities:\n        \"appium:deviceName\": Pixel 8\n        \"appium:options\": {a: 1, b: true, c: x}\n"
	cfg, err := loadText(t, androidText(good, ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Environments["uat"].Android.SecretEnvNames(); strings.Join(got, ",") != "FARM_USER,FARM_KEY" {
		t.Errorf("secret names = %v", got)
	}
	if _, err := loadText(t, androidText("      serverUrl: http://127.0.0.1:4723\n      serverUser: env:U\n      serverKey: env:K\n", "")); err != nil {
		t.Errorf("basic auth over http to this machine: %v", err)
	}
}

func TestAndroidServiceRules(t *testing.T) {
	farm := "      serverUrl: https://farm.example.com\n"
	for name, c := range map[string]struct{ env, svc, want string }{
		"no package":                  {"", "          appActivity: .Main\n", "appPackage is required"},
		"a one-word package":          {"", "          appPackage: shop\n", "appPackage is required"},
		"a package with a quote":      {"", "          appPackage: \"com.x'y\"\n", "appPackage"},
		"an activity with a space":    {"", "          appPackage: com.a.b\n          appActivity: \"a b\"\n", "appActivity"},
		"an apk that leaves":          {"", "          appPackage: com.a.b\n          apk: ../x.apk\n", "apk must be a path inside"},
		"an apk with a far server":    {farm, "          appPackage: com.a.b\n          apk: x.apk\n", "apk cannot be used with a serverUrl"},
		"an app without a far server": {"", "          appPackage: com.a.b\n          app: bs://abc\n", "app is used only with a serverUrl"},
		"an app with a near server":   {"      serverUrl: http://127.0.0.1:4723\n", "          appPackage: com.a.b\n          app: bs://abc\n", "app is used only with a serverUrl"},
		"an app with an apk":          {farm, "          appPackage: com.a.b\n          app: bs://abc\n          apk: x.apk\n", "apk or app, and not both"},
		"an app with a space":         {farm, "          appPackage: com.a.b\n          app: \"bs://a b\"\n", "app \"bs://a b\""},
		"a bad reset":                 {"", "          appPackage: com.a.b\n          reset: wipe\n", "reset must be"},
		"an upper-case scheme":        {"", "          appPackage: com.a.b\n          schemes: [Shop]\n", "schemes[0]"},
		"the http scheme":             {"", "          appPackage: com.a.b\n          schemes: [http]\n", "cannot be listed"},
		"the intent scheme":           {"", "          appPackage: com.a.b\n          schemes: [intent]\n", "cannot be listed"},
		"the javascript scheme":       {"", "          appPackage: com.a.b\n          schemes: [javascript]\n", "cannot be listed"},
		"a scheme twice":              {"", "          appPackage: com.a.b\n          schemes: [shop, shop]\n", "listed twice"},
		"a permission with a space":   {"", "          appPackage: com.a.b\n          permissions: [\"a b\"]\n", "permissions[0]"},
		"a permission with a quote":   {"", "          appPackage: com.a.b\n          permissions: [\"a'; rm -rf /\"]\n", "permissions[0]"},
		"an unknown key":              {"", "          appPackage: com.a.b\n          colour: red\n", "colour"},
	} {
		_, err := loadText(t, androidText(c.env, c.svc))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error with %q, got %v", name, c.want, err)
		}
	}
	if _, err := loadText(t, androidText(farm, "          appPackage: com.a.b\n          app: bs://abc\n")); err != nil {
		t.Errorf("an app with a far server: %v", err)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "127.5.5.5": true, "::1": true, "[::1]": true, "LOCALHOST": true, "a.localhost": true,
		"example.com": false, "10.0.0.1": false, "0.0.0.0": false, "": false, "127.0.0.1.example.com": false,
	} {
		if got := config.IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestAndroidLogcatOnFailure(t *testing.T) {
	cfg, err := loadText(t, androidText("      logcatOnFailure: true\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Environments["uat"].Android.LogcatOnFailure {
		t.Error("logcatOnFailure: true was not read")
	}
	cfg, err = loadText(t, androidText("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environments["uat"].Android.LogcatOnFailure {
		t.Error("logcatOnFailure must default to false")
	}
	if _, err := loadText(t, androidText("      logcatOnFailure: maybe\n", "")); err == nil {
		t.Error("a logcatOnFailure that is not a boolean must be refused")
	}
}

func TestAndroidDisplayAndAccessibilityCheckRules(t *testing.T) {
	good := "      theme: dark\n      fontScale: 1.5\n      locale: id-ID\n"
	cfg, err := loadText(t, androidText(good, ""))
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.Environments["uat"]
	if e.Android.Theme != "dark" || e.Android.FontScale != 1.5 || e.Android.Locale != "id-ID" {
		t.Errorf("android = %+v", e.Android)
	}
	if e.Checks.AccessibilityMode() != "warn" {
		t.Errorf("default accessibility mode = %q, want warn", e.Checks.AccessibilityMode())
	}

	for name, c := range map[string]struct{ env, want string }{
		"bad theme":             {"      theme: neon\n", "android.theme must be light or dark"},
		"fontScale too low":     {"      fontScale: 0.2\n", "android.fontScale must be from 0.5 to 3.0"},
		"fontScale too high":    {"      fontScale: 3.5\n", "android.fontScale must be from 0.5 to 3.0"},
		"bad locale":            {"      locale: en_US\n", "android.locale"},
		"theme with serverUrl":  {"      serverUrl: http://127.0.0.1:4723\n      theme: dark\n", "does not use adb"},
		"scale with serverUrl":  {"      serverUrl: http://127.0.0.1:4723\n      fontScale: 1.5\n", "does not use adb"},
		"locale with serverUrl": {"      serverUrl: http://127.0.0.1:4723\n      locale: en-US\n", "does not use adb"},
	} {
		_, err := loadText(t, androidText(c.env, ""))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error with %q, got %v", name, c.want, err)
		}
	}

	offCfg, err := loadText(t, androidHead+"    checks:\n      accessibility: off\n    services:\n      - name: shop-app\n        baseUrl: https://shop.uat.example.com\n")
	if err != nil {
		t.Fatal(err)
	}
	if offCfg.Environments["uat"].Checks.AccessibilityMode() != "off" {
		t.Errorf("accessibility mode = %q, want off", offCfg.Environments["uat"].Checks.AccessibilityMode())
	}
	if _, err := loadText(t, androidHead+"    checks:\n      accessibility: fail\n    services:\n      - name: shop-app\n        baseUrl: https://shop.uat.example.com\n"); err == nil || !strings.Contains(err.Error(), "checks.accessibility") {
		t.Errorf("checks.accessibility=fail: err = %v", err)
	}
}
