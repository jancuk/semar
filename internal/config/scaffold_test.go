package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
)

func loadScaffold(t *testing.T, text string) (config.Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".semar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".semar", "semar.yaml"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.Load(dir)
}

func TestAStandaloneAndroidServiceNeedsNoBaseURLAndNoHosts(t *testing.T) {
	cfg, err := loadScaffold(t, `version: 1
environments:
  uat:
    kind: test
    methods: [GET]
    effects: {mode: test_data}
    services:
      - name: sinau-mobile
        android:
          appPackage: id.sinau.mobile
authProfiles:
  default: {}
`)
	if err != nil {
		t.Fatalf("a service with an app and no baseUrl: %v", err)
	}
	svc := cfg.Environments["uat"].Services[0]
	if svc.BaseURL != "" || svc.Android == nil {
		t.Errorf("service = %+v", svc)
	}
}

func TestAServiceWithNoAppStillNeedsABaseURL(t *testing.T) {
	_, err := loadScaffold(t, `version: 1
environments:
  uat:
    kind: test
    hosts: [shop.example.com]
    methods: [GET]
    effects: {mode: test_data}
    services:
      - name: shop
authProfiles:
  default: {}
`)
	if err == nil || !strings.Contains(err.Error(), "baseUrl must be an absolute http or https URL") {
		t.Errorf("err = %v", err)
	}
}

func TestHostsAreStillNeededWhenOneServiceHasAnAddress(t *testing.T) {
	_, err := loadScaffold(t, `version: 1
environments:
  uat:
    kind: test
    methods: [GET]
    effects: {mode: test_data}
    services:
      - name: sinau-mobile
        android: {appPackage: id.sinau.mobile}
      - name: api
        baseUrl: https://api.example.com
authProfiles:
  default: {}
`)
	if err == nil || !strings.Contains(err.Error(), "hosts needs at least one entry") {
		t.Errorf("err = %v", err)
	}
}

func TestHostsAreStillNeededWhenAServerIsNamed(t *testing.T) {
	_, err := loadScaffold(t, `version: 1
environments:
  uat:
    kind: test
    methods: [GET]
    effects: {mode: test_data}
    android:
      serverUrl: http://127.0.0.1:4723
    services:
      - name: sinau-mobile
        android: {appPackage: id.sinau.mobile}
authProfiles:
  default: {}
`)
	if err == nil || !strings.Contains(err.Error(), "hosts needs at least one entry") {
		t.Errorf("err = %v", err)
	}
}

func TestAddAndroidServiceWritesANewConfig(t *testing.T) {
	out, err := config.AddAndroidService(nil, config.AndroidService{Name: "aura-keyboard", Package: "com.aura.keyboard", APK: "app/build/outputs/apk/debug/app-debug.apk", Reset: "clear"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"version: 1", "uat:", "kind: test", "appPackage: com.aura.keyboard", "apk: app/build/outputs/apk/debug/app-debug.apk", "reset: clear"} {
		if !strings.Contains(text, want) {
			t.Errorf("config lacks %q:\n%s", want, text)
		}
	}
	for _, never := range []string{"appActivity", "adb", "password", "token", "baseUrl", "hosts"} {
		if strings.Contains(text, never) {
			t.Errorf("config holds %q:\n%s", never, text)
		}
	}
	if _, err := loadScaffold(t, text); err != nil {
		t.Errorf("the new file does not load: %v", err)
	}
}

func TestAddAndroidServiceKeepsEveryOtherLine(t *testing.T) {
	old := `# the shop
version: 1
environments:
  uat:
    kind: test
    hosts: ["shop.example.com"]   # only this one
    methods: [GET, POST]
    effects:
      mode: test_data
    services:
      # the backend
      - name: shop
        baseUrl: https://shop.example.com
authProfiles:
  default: {}
`
	out, err := config.AddAndroidService([]byte(old), config.AndroidService{Name: "shop-app", Package: "com.example.shop", BaseURL: "https://app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, line := range strings.Split(old, "\n") {
		if strings.Contains(line, "hosts:") {
			continue // the host of the base URL is added to this line
		}
		if line != "" && !strings.Contains(text, line) {
			t.Errorf("the line %q was lost", line)
		}
	}
	cfg, err := loadScaffold(t, text)
	if err != nil {
		t.Fatalf("%v:\n%s", err, text)
	}
	svcs := cfg.Environments["uat"].Services
	if len(svcs) != 2 || svcs[1].Name != "shop-app" || svcs[1].Android.AppPackage != "com.example.shop" || svcs[1].Android.AppActivity != "" {
		t.Errorf("services = %+v", svcs)
	}
	if !strings.Contains(strings.Join(cfg.Environments["uat"].Hosts, ","), "app.example.com") {
		t.Errorf("hosts = %v: the host of the base URL must be listed", cfg.Environments["uat"].Hosts)
	}
}

func TestAddAndroidServiceRefusals(t *testing.T) {
	envText := func(extra string) []byte {
		return []byte("version: 1\nenvironments:\n  uat:\n    kind: test\n    hosts: [a.example.com]\n    methods: [GET]\n    effects: {mode: test_data}\n    services:\n      - name: api\n        baseUrl: https://a.example.com\n" + extra + "authProfiles:\n  default: {}\n")
	}
	app := config.AndroidService{Name: "app", Package: "com.example.app"}
	cases := map[string]struct {
		data []byte
		svc  config.AndroidService
		want string
	}{
		"production":  {[]byte(strings.Replace(string(envText("")), "kind: test", "kind: production", 1)), app, "production"},
		"read only":   {[]byte(strings.Replace(string(envText("")), "mode: test_data", "mode: read_only", 1)), app, "read only"},
		"same name":   {envText(""), config.AndroidService{Name: "api", Package: "com.example.app"}, "has a service api already"},
		"bad package": {envText(""), config.AndroidService{Name: "app", Package: "not a package"}, "not a package name"},
		"bad reset":   {envText(""), config.AndroidService{Name: "app", Package: "com.example.app", Reset: "wipe"}, "reset must be"},
		"flow style":  {[]byte("version: 1\nenvironments:\n  uat: {kind: test, hosts: [a.example.com], methods: [GET], effects: {mode: test_data}, services: [{name: api, baseUrl: \"https://a.example.com\"}]}\nauthProfiles:\n  default: {}\n"), app, "by hand"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := config.AddAndroidService(c.data, c.svc)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestWriteAndroidServiceCreatesTheFileAndTheGitignoreOnce(t *testing.T) {
	dir := t.TempDir()
	path, created, err := config.WriteAndroidService(dir, config.AndroidService{Name: "app", Package: "com.example.app"})
	if err != nil || !created {
		t.Fatalf("created = %v, err = %v", created, err)
	}
	if path != filepath.Join(dir, ".semar", "semar.yaml") {
		t.Errorf("path = %s", path)
	}
	ignore := filepath.Join(dir, ".semar", ".gitignore")
	if _, err := os.Stat(ignore); err != nil {
		t.Errorf("no .gitignore: %v", err)
	}
	if err := os.WriteFile(ignore, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, created, err = config.WriteAndroidService(dir, config.AndroidService{Name: "second", Package: "com.example.second"}); err != nil || created {
		t.Fatalf("second: created = %v, err = %v", created, err)
	}
	if b, _ := os.ReadFile(ignore); string(b) != "mine\n" {
		t.Errorf("an existing .gitignore was changed: %q", b)
	}
	cfg, err := config.Load(dir)
	if err != nil || len(cfg.Environments["uat"].Services) != 2 {
		t.Errorf("config = %+v, %v", cfg, err)
	}
}
