package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/engine/mobile/webdriver"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/journey"
)

// The state of one line of the check of a machine.
const (
	stateOK      = "ok"
	stateMissing = "missing"
)

type checkItem struct{ state, text string }

// machineCheck is the list of what this machine has and lacks for the repository's semar.yaml.
type machineCheck struct{ items []checkItem }

func (m *machineCheck) add(state, format string, args ...any) {
	m.items = append(m.items, checkItem{state, oneLine(terminalSafe(fmt.Sprintf(format, args...)))})
}

func (m *machineCheck) missing() int {
	n := 0
	for _, it := range m.items {
		if it.state == stateMissing {
			n++
		}
	}
	return n
}

// checkMachine is semar init in a repository that is set up. It writes nothing. It prints one line
// for each item that a run needs from this machine, with the way to fix what is missing, and ends
// with exit code 2 when something is missing.
func checkMachine(ctx context.Context, o options, stdout io.Writer, dir string) error {
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s exists: checking this machine.\n\n", configFileName)
	var m machineCheck
	m.tokens(o, cfg)
	m.flowSecrets(o, dir)
	m.services(ctx, o, cfg)
	m.provider(ctx, o, dir, cfg)
	m.chrome(dir, cfg)
	m.android(ctx, o, dir, cfg)
	for _, it := range m.items {
		fmt.Fprintf(stdout, "  %-7s  %s\n", it.state, it.text)
	}
	fmt.Fprintln(stdout)
	if n := m.missing(); n > 0 {
		fmt.Fprintf(stdout, "%d missing. Next: fix them, then semar run\n", n)
		return exitError{ExitUndecided}
	}
	fmt.Fprintln(stdout, "Nothing is missing. Next: semar run")
	return nil
}

// tokens checks the environment variable of each secret of each auth profile, once each.
func (m *machineCheck) tokens(o options, cfg config.Config) {
	seen := map[string]bool{}
	names := slices.Sorted(maps.Keys(cfg.AuthProfiles))
	if i := slices.Index(names, "default"); i > 0 {
		names = slices.Concat([]string{"default"}, slices.Delete(names, i, i+1))
	}
	for _, name := range names {
		prof := cfg.AuthProfiles[name]
		for _, key := range slices.Sorted(maps.Keys(prof.Secrets)) {
			env, ok := strings.CutPrefix(prof.Secrets[key], "env:")
			if !ok || seen[env] {
				continue
			}
			seen[env] = true
			if v, set := o.LookupEnv(env); set && v != "" {
				m.add(stateOK, "%s is set", env)
				continue
			}
			m.add(stateMissing, "%s is not set: export it with the token of the account %q", env, name)
		}
		if prof.Web == nil {
			continue
		}
		env := prof.Web.PasswordEnv()
		if seen[env] {
			continue
		}
		seen[env] = true
		if v, set := o.LookupEnv(env); set && v != "" {
			m.add(stateOK, "%s is set", env)
			continue
		}
		m.add(stateMissing, "%s is not set: export it with the web password of the account %q", env, name)
	}
}

// flowSecrets checks the environment variable of each secret that a flow of .semar/flows types. A flow
// types its password on a device, so a run needs the variable, and a harness must never inherit it.
func (m *machineCheck) flowSecrets(o options, dir string) {
	for _, env := range flow.SecretEnvNames(dir) {
		if slices.ContainsFunc(m.items, func(it checkItem) bool { return strings.HasPrefix(it.text, env+" is ") }) {
			continue
		}
		if v, set := o.LookupEnv(env); set && v != "" {
			m.add(stateOK, "%s is set", env)
			continue
		}
		m.add(stateMissing, "%s is not set: a flow types it as a secret, so export it before a run", env)
	}
}

// services reads the build probe of each service of each test environment, and for a service with
// none, one GET to its address to see that it answers.
func (m *machineCheck) services(ctx context.Context, o options, cfg config.Config) {
	client := o.HTTP
	if client == nil {
		client = &http.Client{Timeout: probeTimeout}
	}
	for _, envName := range slices.Sorted(maps.Keys(cfg.Environments)) {
		env := cfg.Environments[envName]
		if env.Kind == config.KindProduction {
			continue
		}
		prober := build.NewHTTP(env.Services, client)
		for _, s := range env.Services {
			if s.Build != nil {
				b, err := prober.Probe(ctx, s.Name)
				if err != nil {
					m.add(stateMissing, "%s: %s did not answer its build probe at %s: %s: start the service, or correct the address", envName, s.Name, s.Build.URL, probeReason(err))
					continue
				}
				m.add(stateOK, "%s: %s answers, build %s", envName, s.Name, b.Commit[:min(7, len(b.Commit))])
				continue
			}
			if s.BaseURL == "" {
				// A standalone app on a device has no address to ask: its build is the APK, or the --build.
				if s.Android != nil && s.Android.APK != "" {
					m.add(stateOK, "%s: %s is an app on a device; its build comes from %s", envName, s.Name, s.Android.APK)
				} else {
					m.add(stateOK, "%s: %s is an app on a device with no address of its own; name its build with semar run --build <commit> or give it an apk", envName, s.Name)
				}
				continue
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL, nil)
			if err == nil {
				var resp *http.Response
				if resp, err = client.Do(req); err == nil {
					resp.Body.Close()
				}
			}
			if err != nil {
				m.add(stateMissing, "%s: %s did not answer at %s: start the service, or correct the address", envName, s.Name, s.BaseURL)
				continue
			}
			if build.IsLocal(s.BaseURL) {
				m.add(stateOK, "%s: %s answers; it has no build probe, so its build is the head commit of the repository", envName, s.Name)
				continue
			}
			m.add(stateOK, "%s: %s answers; it has no build probe, so name the build with semar run --build <commit>", envName, s.Name)
		}
	}
}

// probeReason is the part of a probe error that a person can act on.
func probeReason(err error) string {
	return strings.TrimPrefix(err.Error(), "build probe for service ")
}

// provider checks that the model provider that would plan is installed.
func (m *machineCheck) provider(ctx context.Context, o options, dir string, cfg config.Config) {
	src, err := loadSources(o, dir, cfg)
	if err != nil {
		m.add(stateMissing, "your providers file cannot be read: %v", err)
		return
	}
	pc, _, err := src.choose("")
	if err != nil {
		m.add(stateMissing, "no model provider can plan: %v", err)
		return
	}
	o.drop.set(append(secretEnvNamesIn(cfg, dir), keyEnvNames(src.file)...))
	inst, err := o.Providers(dir).New(pc)
	if err != nil {
		m.add(stateMissing, "the provider %s cannot plan: %v", pc.ID, err)
		return
	}
	if h := inst.Health(ctx); !h.Installed {
		m.add(stateMissing, "the provider %s cannot plan: %s", inst.ID(), h.Detail)
		return
	}
	m.add(stateOK, "the provider %s would plan", inst.ID())
}

// chrome is checked only when a scenario or a leg of a journey has a web step.
func (m *machineCheck) chrome(dir string, cfg config.Config) {
	scs, _ := loadAllScenarios(dir)
	n := 0
	for _, sc := range scs {
		if sc.Surface == domain.SurfaceWeb {
			n++
		}
	}
	js, _, _ := journey.Load(dir)
	for _, j := range js {
		if slices.ContainsFunc(j.Legs, func(l domain.Leg) bool { return l.Surface == domain.SurfaceWeb }) {
			n++
		}
	}
	if n == 0 {
		return
	}
	configured := ""
	if _, env, err := cfg.Environment(""); err == nil {
		configured = env.Web.Chrome
	}
	if path, err := web.FindChrome(configured); err != nil {
		m.add(stateMissing, "Chrome was not found: %s", webStepsWords(n))
	} else {
		m.add(stateOK, "Chrome is at %s", path)
	}
}

func webStepsWords(n int) string {
	if n == 1 {
		return "1 scenario has web steps"
	}
	return fmt.Sprintf("%d scenarios have web steps", n)
}

// android is checked only when a scenario or a leg of a journey runs on a device: adb, the device, and
// the two server APKs, or for an environment with a serverUrl, that the server answers.
func (m *machineCheck) android(ctx context.Context, o options, dir string, cfg config.Config) {
	scs, _ := loadAllScenarios(dir)
	n := 0
	for _, sc := range scs {
		if sc.Surface == domain.SurfaceAndroid {
			n++
		}
	}
	js, _, _ := journey.Load(dir)
	for _, j := range js {
		if slices.ContainsFunc(j.Legs, func(l domain.Leg) bool { return l.Surface == domain.SurfaceAndroid }) {
			n++
		}
	}
	if n == 0 && !anyAndroidService(cfg) {
		return
	}
	named := false
	adbSaid := false
	for _, envName := range slices.Sorted(maps.Keys(cfg.Environments)) {
		env := cfg.Environments[envName]
		if env.Kind == config.KindProduction || len(androidServices(env)) == 0 {
			continue
		}
		named = true
		if env.Android.Remote() {
			m.androidServer(ctx, o, envName, env)
			continue
		}
		bin, err := android.FindADB(o.LookupEnv, dir)
		if err != nil {
			if !adbSaid {
				m.add(stateMissing, "adb was not found: %s", strings.TrimPrefix(err.Error(), "adb was not found: "))
				adbSaid = true
			}
			continue
		}
		if !adbSaid {
			m.add(stateOK, "adb is at %s", bin)
			adbSaid = true
		}
		serial := m.androidDevice(ctx, o, envName, env, bin)
		m.androidServerAPKs(ctx, o, dir, envName, env, bin, serial)
	}
	if !named {
		m.add(stateMissing, "%s on a device, and no test environment has a service with an android app: add android.appPackage to a service in semar.yaml", androidScenariosWords(n))
	}
}

// anyAndroidService reports whether a test environment has a service with an app on a device. The
// device is checked for it even before the first scenario is written, because that is when a person
// needs to know that the device is ready to record on.
func anyAndroidService(cfg config.Config) bool {
	for _, env := range cfg.Environments {
		if env.Kind != config.KindProduction && len(androidServices(env)) > 0 {
			return true
		}
	}
	return false
}

func androidScenariosWords(n int) string {
	if n == 1 {
		return "1 scenario or journey leg runs"
	}
	return fmt.Sprintf("%d scenarios or journey legs run", n)
}

// androidServer checks that the server at serverUrl answers.
func (m *machineCheck) androidServer(ctx context.Context, o options, envName string, env config.Environment) {
	c := webdriver.New(env.Android.ServerURL)
	if o.HTTP != nil {
		c.HTTP = o.HTTP
	}
	for _, f := range []struct {
		ref string
		to  *string
	}{{env.Android.ServerUser, &c.User}, {env.Android.ServerKey, &c.Key}} {
		if name, ok := strings.CutPrefix(f.ref, "env:"); ok {
			v, set := o.LookupEnv(name)
			if !set || v == "" {
				m.add(stateMissing, "%s: %s is not set: export it with the credential of the device server", envName, name)
				return
			}
			*f.to = v
		}
	}
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	switch st, err := c.Status(cctx); {
	case err != nil:
		m.add(stateMissing, "%s: the device server at %s did not answer: %s", envName, env.Android.ServerURL, oneLine(err.Error()))
	case !st.Ready:
		m.add(stateMissing, "%s: the device server at %s is not ready", envName, env.Android.ServerURL)
	default:
		m.add(stateOK, "%s: the device server at %s answers", envName, env.Android.ServerURL)
	}
}

// androidDevice checks that the device that a run would use is there and ready. It gives the serial, or
// "" when there is none.
func (m *machineCheck) androidDevice(ctx context.Context, o options, envName string, env config.Environment, bin string) string {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	run := android.ExecRunner{}
	if err := run.Quiet(cctx, bin, "start-server"); err != nil {
		m.add(stateMissing, "%s: the adb server could not be started: %s", envName, oneLine(err.Error()))
		return ""
	}
	out, err := run.Output(cctx, bin, "devices", "-l")
	if err != nil {
		m.add(stateMissing, "%s: adb could not list the devices: %s", envName, oneLine(err.Error()))
		return ""
	}
	want := env.Android.Device
	if v, ok := o.LookupEnv("ANDROID_SERIAL"); ok && v != "" {
		want = v
	}
	dev, un := android.ChooseDevice(android.ParseDevices(string(out)), want)
	if un != nil {
		m.add(stateMissing, "%s: %s", envName, un.Reason)
		return ""
	}
	m.add(stateOK, "%s: the device %s is ready", envName, dev.Serial)
	return dev.Serial
}

// androidServerAPKs checks that Semar has the two UiAutomator2 server APKs, or that the device has the
// packages already.
func (m *machineCheck) androidServerAPKs(ctx context.Context, o options, dir, envName string, env config.Environment, bin, serial string) {
	files := []string{env.Android.UIAutomator2ServerAPK, env.Android.UIAutomator2ServerTestAPK}
	if files[0] == "" {
		home, err := o.home()
		if err == nil {
			files = []string{filepath.Join(home, "android", android.ServerAPKName), filepath.Join(home, "android", android.ServerTestAPKName)}
		}
	} else {
		for i, f := range files {
			files[i] = build.APKPath(dir, f)
		}
	}
	have := len(files) == 2
	for _, f := range files {
		if info, err := os.Stat(f); err != nil || !info.Mode().IsRegular() {
			have = false
		}
	}
	if have {
		m.add(stateOK, "%s: the UiAutomator2 server APKs are at %s", envName, filepath.Dir(files[0]))
		return
	}
	if serial != "" {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		present := true
		for _, pkg := range []string{"io.appium.uiautomator2.server", "io.appium.uiautomator2.server.test"} {
			// The package names are constants, and the device shell takes the command as one quoted line.
			out, err := android.ExecRunner{}.Output(cctx, bin, "-s", serial, "shell", "'pm' 'path' '"+pkg+"'")
			if err != nil || !strings.Contains(string(out), "package:") {
				present = false
			}
		}
		if present {
			m.add(stateOK, "%s: the UiAutomator2 server is already on the device %s", envName, serial)
			return
		}
	}
	m.add(stateMissing, "%s: the UiAutomator2 server APKs were not found: put %s and %s in the Semar home under android/, or name them with uiautomator2ServerApk and uiautomator2ServerTestApk (Semar downloads nothing)",
		envName, android.ServerAPKName, android.ServerTestAPKName)
}
