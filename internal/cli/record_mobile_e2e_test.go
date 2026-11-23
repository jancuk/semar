package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

// recordRig is an android rig whose app keeps its sign-in until its data is cleared, as a real app does.
func recordRig(t *testing.T) (*androidRig, *atomic.Bool) {
	t.Helper()
	r := newAndroidRig(t, androidOptions{env: "device: emulator-5554", scenarios: map[string]string{}})
	var signed atomic.Bool
	show := func() {
		if signed.Load() {
			r.server.SetSource(signedInScreen("Hi Ana"))
		} else {
			r.server.SetSource(signedOutScreen())
		}
	}
	show()
	r.server.OnTap(func(_, y int) {
		if y >= 900 && y <= 1020 {
			r.taps.Add(1)
			signed.Store(true)
			show()
		}
	})
	r.server.OnExecute(func(script string, _ map[string]any) (any, *mobiletest.Failure) {
		switch script {
		case "mobile: clearApp":
			signed.Store(false)
			show()
		case "mobile: activateApp":
			show()
		}
		return nil, nil
	})
	return r, &signed
}

func (r *androidRig) stdin(lines ...string) {
	r.opts.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
}

// The whole journey of the plan: record the flow login, verify it from an app that is signed in, record a
// scenario that starts with that flow and checks the greeting, and replay it as semar run does.
func TestRecordMobileFlowThenScenarioThatUsesIt(t *testing.T) {
	const password = "pw-Zk93-recorded-canary"
	r, signed := recordRig(t)

	// The flow. The elements of the sign-in screen are 1 email, 2 password, 3 the button.
	r.stdin("screen SignIn", "type 1 ana@example.com", "secret 2 SHOP_PASSWORD", password, "tap 3", "finish", "quit")
	res := r.must(t, cli.ExitOK, "record", "--flow", "login")
	contains(t, "stdout", res.stdout, "Saved:", ".semar/flows/login.yaml", "the flow login completed", "A flow has no check, so it completes and does not pass.")
	if strings.Contains(res.stdout, "login passed") || strings.Contains(res.stdout, "the flow login passed") {
		t.Errorf("a flow was reported as passed:\n%s", res.stdout)
	}
	flowText, err := os.ReadFile(r.path(".semar", "flows", "login.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the flow", string(flowText), "kind: Flow", "reset: clear", "secret: env:SHOP_PASSWORD", "value: ana@example.com")
	if !signed.Load() && r.taps.Load() < 2 {
		t.Errorf("the verify did not run the flow on the device: taps = %d", r.taps.Load())
	}
	if r.taps.Load() != 2 {
		t.Errorf("the sign-in was tapped %d times, want once by the person and once by the verify", r.taps.Load())
	}
	// The password reached the device twice, and nothing of Semar holds it.
	if typed := r.server.Typed(); len(typed) != 4 || typed[1] != password || typed[3] != password {
		t.Errorf("typed = %q", typed)
	}
	r.assertNoSecret(t, []string{password})
	for _, text := range append(append([]string{}, r.output...), string(flowText)) {
		if strings.Contains(text, password) {
			t.Fatalf("the password is in what Semar printed or wrote:\n%s", text)
		}
	}

	// The scenario starts with the flow, run live from a signed in app, and checks the greeting. The
	// password is exported now, as a person has it for semar run. Elements of the home screen: 1 hi, 2 button.
	t.Setenv("SHOP_PASSWORD", password)
	signed.Store(true)
	r.server.SetSource(signedInScreen("Hi Ana"))
	r.stdin("screen Home", "assert 1", "finish", "quit")
	res = r.must(t, cli.ExitOK, "record", "TASK-1", "--use", "login")
	contains(t, "stdout", res.stdout, "Saved:", "TASK-1.feature", "Replayed once: passed.")
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "TASK", "TASK-1.feature"))
	if err != nil {
		t.Fatalf("no feature: %v\n%s", err, res.stdout)
	}
	contains(t, "the scenario", string(feature), "@surface:android", `the element welcome equals "Hi Ana"`)
	stored, _ := filepath.Glob(r.path(".semar", "store", "scripts", "TASK-1", "*.yaml"))
	if len(stored) != 1 {
		t.Fatalf("stored scripts = %v", stored)
	}
	script, _ := os.ReadFile(stored[0])
	contains(t, "the script", string(script), "use: login", "do: wait")
	r.assertNoSecret(t, []string{password})

	// The recording replays with plain semar run too.
	res = r.must(t, cli.ExitOK, "run", "--scenario", "TASK-1")
	contains(t, "stdout", res.stdout, "TASK-1  passed")
}

// The recording releases the device before the verify, so that the replay can take the lease, and the
// replay reads the serial of the recording.
func TestRecordMobileVerifyUsesTheSameDeviceAndTheLeaseIsFree(t *testing.T) {
	r := newAndroidRig(t, androidOptions{local: true, scenarios: map[string]string{}})
	r.stdin("tap 3", "finish", "quit")
	res := r.must(t, cli.ExitOK, "record", "--flow", "tap-only")
	contains(t, "stdout", res.stdout, "the flow tap-only completed")
	if strings.Contains(res.stdout, "used by another semar run") {
		t.Errorf("the verify could not take the device:\n%s", res.stdout)
	}
	// The device was opened for the recording, for the verify, and again after it.
	starts := 0
	for _, l := range r.adb.Log() {
		if strings.HasPrefix(l, "start-server") {
			starts++
		}
	}
	if starts != 3 {
		t.Errorf("the device was opened %d times, want 3: the recording, the verify, and the return:\n%s", starts, adbLog(r))
	}
}

func TestRecordMobileFlags(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{}})
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"flow with a scenario id and use", []string{"record", "--flow", "login", "--use", "x"}, "does not use another flow"},
		{"ui with a flow", []string{"record", "--ui", "--flow", "login"}, "leave out --flow and --use"},
		{"port without ui", []string{"record", "--flow", "x", "--port", "8123"}, "belong to --ui"},
		{"bad port", []string{"record", "--ui", "--port", "70000"}, "is not a port"},
		{"bad surface", []string{"record", "X-1", "--surface", "ios"}, "must be web or android"},
		{"web surface with a flow", []string{"record", "--surface", "web", "--flow", "login"}, "android only"},
		{"a web flag", []string{"record", "X-1", "--surface", "android", "--open", "/x"}, "does not apply to an app on a device"},
		{"two arguments with a flow", []string{"record", "--flow", "login", "uat", "extra"}, "accepts at most 1 arg"},
		{"no scenario id", []string{"record", "--surface", "android"}, "accepts between 1 and 2 arg"},
		{"a flow name that is not one", []string{"record", "--flow", "../x"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := r.semar(t, t.Context(), c.args...)
			if res.code == cli.ExitOK {
				t.Fatalf("exit 0:\n%s", res.stdout)
			}
			if !strings.Contains(res.stderr, c.want) {
				t.Errorf("stderr = %q, want %q", res.stderr, c.want)
			}
		})
	}
}

func TestRecordMobileRefusesAProductionEnvironmentBeforeAnyDevice(t *testing.T) {
	r := newAndroidRig(t, androidOptions{production: true, scenarios: map[string]string{}})
	res := r.semar(t, t.Context(), "record", "--flow", "login")
	if res.code == cli.ExitOK || !strings.Contains(res.stderr, "production") {
		t.Errorf("exit %d, stderr = %q", res.code, res.stderr)
	}
}

func TestRecordMobileRefusesAFlowThatExists(t *testing.T) {
	r, _ := recordRig(t)
	r.write(t, ".semar/flows/login.yaml", androidLoginFlow)
	res := r.semar(t, t.Context(), "record", "--flow", "login")
	if res.code == cli.ExitOK || !strings.Contains(res.stderr, "exists already") {
		t.Errorf("exit %d, stderr = %q", res.code, res.stderr)
	}
}

// The web recorder is untouched: semar record with a scenario id and no device flag still looks for Chrome.
func TestRecordWithoutDeviceFlagsStaysTheWebRecorder(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{}})
	res := r.semar(t, t.Context(), "record", "WEB-1")
	if strings.Contains(res.stderr, "device") && !strings.Contains(res.stderr, "Chrome") && !strings.Contains(res.stderr, "chrome") {
		t.Errorf("the web path was not taken: %q", res.stderr)
	}
}

// A service that is only an app has no baseUrl. The build of its scenarios is its APK, and the run asks
// no address.
func TestAnAndroidScenarioRunsOnAStandaloneAppWithAnAPK(t *testing.T) {
	r := newAndroidRig(t, androidOptions{apk: "an apk, version one"})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "      - name: shop-app\n        baseUrl: "+r.ordersURL+"\n", "      - name: shop-app\n", 1)
	})
	res := r.must(t, cli.ExitOK, "run", "--scenario", "AND-1")
	contains(t, "stdout", res.stdout, "AND-1  passed", "build: shop-app "+hexSHA("an apk, version one")[:40])
}

// A service that is only an app, with no APK and no build, is not blocked by an address that it lacks:
// its build is not known, so the result is not decided.
func TestAnAndroidScenarioOnAStandaloneAppWithNoBuildSaysSoAndDoesNotCrash(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "      - name: shop-app\n        baseUrl: "+r.ordersURL+"\n", "      - name: shop-app\n", 1)
	})
	res := r.semar(t, t.Context(), "run", "--scenario", "AND-1")
	if strings.Contains(res.stderr, "panic") || strings.Contains(res.stdout, "panic") {
		t.Fatalf("panic:\n%s\n%s", res.stdout, res.stderr)
	}
	if res.code != cli.ExitOK && res.code != cli.ExitUndecided {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", res.code, res.stdout, res.stderr)
	}
}
