package mobilemcp_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
)

func healthTree(pkg string, text string) *mobile.Tree {
	full := mobile.Rect{Right: 1080, Bottom: 2400}
	btn := &mobile.Node{
		Class: "android.widget.TextView", Role: "text", Package: pkg, Text: text, Enabled: true,
		Bounds: mobile.Rect{Left: 100, Top: 200, Right: 900, Bottom: 360}, Attrs: map[string]string{"text": text},
	}
	win := &mobile.Node{Class: "android.widget.FrameLayout", Package: pkg, Enabled: true, Bounds: full, Children: []*mobile.Node{btn}}
	t := &mobile.Tree{Root: &mobile.Node{Class: "hierarchy", Bounds: full, Children: []*mobile.Node{win}}, Width: 1080, Height: 2400}
	t.Finish()
	return t
}

func TestHealthBaselineAndProactiveCrashAndLogNotes(t *testing.T) {
	f := newFixture(t)
	f.dev.setTree(healthTree("com.example.shop", "Home"))
	// Pre-existing crash and RN error before first read must be baselined and NOT emitted.
	f.dev.crashes = []android.Crash{
		{ID: "data_app_crash@1000", Package: "com.example.shop", Summary: "old NPE"},
	}
	f.dev.logs = []android.LogEntry{
		{Time: "11-23 12:00:00.000", Level: "E", Tag: "ReactNativeJS", Message: "old JS error"},
		{Time: "11-23 12:00:00.100", Level: "I", Tag: "flutter", Message: "normal info log"},
	}

	first := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(first, "crash detected") || strings.Contains(first, "old JS error") {
		t.Errorf("first read emitted baseline noise:\n%s", first)
	}
	if len(f.dev.logQueries) != 2 {
		t.Fatalf("baseline logQueries = %+v, want 2 targeted queries", f.dev.logQueries)
	}
	for _, q := range f.dev.logQueries {
		if q.Follow {
			t.Errorf("health probe used Follow=true: %+v", q)
		}
		if len(q.Filters) == 0 {
			t.Errorf("health probe used unfiltered log query: %+v", q)
		}
	}

	// Within 2s cooldown, new crashes/logs are throttled (no extra probe).
	f.dev.crashes = append(f.dev.crashes, android.Crash{ID: "data_app_crash@2000", Time: f.now.Add(1 * time.Second), Package: "com.example.shop", Summary: "java.lang.IllegalStateException: boom secret-pw"})
	f.dev.logs = append(f.dev.logs,
		android.LogEntry{Time: "11-23 12:00:01.000", Level: "E", Tag: "ReactNativeJS", Message: "TypeError: undefined is not a function"},
		android.LogEntry{Time: "11-23 12:00:01.100", Level: "I", Tag: "flutter", Message: "EXCEPTION CAUGHT BY WIDGETS LIBRARY"},
	)
	f.scrub.Add("secret-pw")
	f.now = f.now.Add(1 * time.Second)
	throttled := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(throttled, "crash detected") {
		t.Errorf("probe ran within cooldown:\n%s", throttled)
	}

	// After 2s cooldown, new crash + RN + Flutter notes appear and are scrubbed.
	f.now = f.now.Add(2 * time.Second)
	second := textOf(f.ok("mobile_list_elements_on_screen", ""))
	for _, want := range []string{
		`note: crash detected: com.example.shop (java.lang.IllegalStateException: boom`,
		`mobile_get_crash with id "data_app_crash@2000"`,
		"note: ReactNativeJS error: TypeError: undefined is not a function",
		"note: Flutter error: EXCEPTION CAUGHT BY WIDGETS LIBRARY",
	} {
		if !strings.Contains(second, want) {
			t.Errorf("missing %q in:\n%s", want, second)
		}
	}
	if strings.Contains(second, "secret-pw") {
		t.Errorf("secret leaked in health note:\n%s", second)
	}

	// Subsequent read after cooldown must NOT repeat already-seen crash/logs.
	f.now = f.now.Add(5 * time.Second)
	third := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(third, "data_app_crash@2000") || strings.Contains(third, "TypeError") || strings.Contains(third, "EXCEPTION CAUGHT") {
		t.Errorf("seen crash/log repeated:\n%s", third)
	}

	// A repeated identical error message at a NEW timestamp IS reported again after cooldown.
	f.dev.logs = append(f.dev.logs, android.LogEntry{Time: "11-23 12:00:10.000", Level: "E", Tag: "ReactNativeJS", Message: "TypeError: undefined is not a function"})
	f.now = f.now.Add(3 * time.Second)
	fourth := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(fourth, "note: ReactNativeJS error: TypeError: undefined is not a function") {
		t.Errorf("repeated error at new timestamp was suppressed:\n%s", fourth)
	}
}

func TestHealthLaunchTimeErrorsAndPackageScoping(t *testing.T) {
	f := newFixture(t)
	f.dev.setTree(healthTree("com.example.shop", "Home"))
	// Simulate a device whose wall clock is UTC-5 (5 hours behind host f.now):
	// pre-launch crash is 5h05m behind host clock; launch-time crash is 5h behind host clock.
	devNow := f.now.Add(-5 * time.Hour)
	f.dev.crashes = []android.Crash{
		{ID: "data_app_crash@old", Time: devNow.Add(-5 * time.Minute), Package: "com.example.shop", Summary: "pre-launch NPE"},
	}
	f.dev.logs = []android.LogEntry{
		{Time: "11-23 11:59:00.000", Level: "E", Tag: "ReactNativeJS", Message: "pre-launch JS error"},
	}

	// Launch baselines before running LaunchApp.
	f.ok("mobile_launch_app", `{"packageName":"com.example.shop"}`)

	// During launch: an older pre-baseline crash (rotated dropbox), an unrelated background package
	// crash, a real launch crash (on device UTC-5 wall clock), and a launch-time Flutter error appear.
	f.dev.crashes = append(f.dev.crashes,
		android.Crash{ID: "data_app_crash@rotated", Time: devNow.Add(-10 * time.Minute), Package: "com.example.shop", Summary: "rotated old crash"},
		android.Crash{ID: "data_app_crash@gms", Time: devNow, Package: "com.google.android.gms", Summary: "unrelated GMS crash"},
		android.Crash{ID: "data_app_crash@launch", Time: devNow, Package: "com.example.shop", Summary: "StartupException"},
	)
	f.dev.logs = append(f.dev.logs,
		android.LogEntry{Time: "11-23 12:00:00.200", Level: "E", Tag: "flutter", Message: "Unhandled Exception: init failed"},
	)

	// Very first screen read after launch (no time advance!) MUST report launch-time errors
	// even when device wall clock is 5 hours west of UTC, and suppress pre-launch, rotated-old,
	// and unrelated package crashes.
	first := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(first, `crash detected: com.example.shop (StartupException)`) {
		t.Errorf("launch-time crash missing on first read:\n%s", first)
	}
	if !strings.Contains(first, "note: Flutter error: Unhandled Exception: init failed") {
		t.Errorf("launch-time Flutter error missing on first read:\n%s", first)
	}
	for _, unwanted := range []string{"pre-launch", "rotated old crash", "com.google.android.gms"} {
		if strings.Contains(first, unwanted) {
			t.Errorf("unexpected %q in first read:\n%s", unwanted, first)
		}
	}
}

func TestHealthUnexpectedExitBypassesCooldownAndRespectsIntentionalExits(t *testing.T) {
	f := newFixture(t)
	f.dev.setTree(healthTree("com.example.shop", "Home"))
	f.ok("mobile_launch_app", `{"packageName":"com.example.shop"}`)
	f.ok("mobile_list_elements_on_screen", "") // baseline

	// Permission dialog overlay while app window is still present must NOT flag exit.
	full := mobile.Rect{Right: 1080, Bottom: 2400}
	appWin := &mobile.Node{Class: "android.widget.FrameLayout", Package: "com.example.shop", Enabled: true, Bounds: full,
		Children: []*mobile.Node{{Class: "android.widget.TextView", Package: "com.example.shop", Text: "Home", Enabled: true, Bounds: mobile.Rect{Left: 10, Top: 10, Right: 200, Bottom: 100}}}}
	permWin := &mobile.Node{Class: "android.widget.FrameLayout", Package: "com.google.android.permissioncontroller", Enabled: true, Bounds: mobile.Rect{Left: 100, Top: 800, Right: 980, Bottom: 1400},
		Children: []*mobile.Node{{Class: "android.widget.Button", Role: "button", Package: "com.google.android.permissioncontroller", Text: "Allow", Enabled: true, Bounds: mobile.Rect{Left: 200, Top: 1100, Right: 500, Bottom: 1260}, Attrs: map[string]string{"clickable": "true"}}}}
	overlayTree := &mobile.Tree{Root: &mobile.Node{Class: "hierarchy", Bounds: full, Children: []*mobile.Node{appWin, permWin}}, Width: 1080, Height: 2400}
	overlayTree.Finish()
	f.dev.setTree(overlayTree)
	permText := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(permText, "no longer on screen") {
		t.Errorf("permission overlay falsely flagged exit:\n%s", permText)
	}

	// Pulling notification shade / system UI must NOT falsely flag exit or trip reportedExit.
	f.dev.setTree(healthTree("com.android.systemui", "Notifications"))
	shadeText := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(shadeText, "no longer on screen") {
		t.Errorf("notification shade falsely flagged exit:\n%s", shadeText)
	}

	// App vanishes to launcher within cooldown (no time advance): unexpected exit triggers immediately and bypasses cooldown.
	f.dev.setTree(healthTree("com.google.android.apps.nexuslauncher", "Search"))
	exitText := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(exitText, "note: app com.example.shop is no longer on screen (screen returned to launcher)") {
		t.Errorf("expected unexpected exit note:\n%s", exitText)
	}

	// Relaunch + intentional HOME does NOT flag unexpected exit.
	f.ok("mobile_launch_app", `{"packageName":"com.example.shop"}`)
	f.dev.setTree(healthTree("com.example.shop", "Home"))
	f.ok("mobile_list_elements_on_screen", "")
	f.ok("mobile_press_button", `{"button":"HOME"}`)
	f.dev.setTree(healthTree("com.google.android.apps.nexuslauncher", "Search"))
	homeText := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(homeText, "no longer on screen") {
		t.Errorf("intentional HOME falsely flagged exit:\n%s", homeText)
	}

	// Returning to the app without mobile_launch_app resets intentionalExit so later crashes are caught.
	f.dev.setTree(healthTree("com.example.shop", "Home"))
	f.ok("mobile_list_elements_on_screen", "")
	f.dev.setTree(healthTree("com.google.android.apps.nexuslauncher", "Search"))
	reenterExit := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(reenterExit, "screen returned to launcher") {
		t.Errorf("expected unexpected exit after re-entering app:\n%s", reenterExit)
	}

	// Intentional BACK exiting to launcher does NOT falsely flag exit.
	f.ok("mobile_launch_app", `{"packageName":"com.example.shop"}`)
	f.dev.setTree(healthTree("com.example.shop", "Home"))
	f.ok("mobile_list_elements_on_screen", "")
	f.dev.imePkg = "" // no keyboard shown
	f.ok("mobile_press_button", `{"button":"BACK"}`)
	f.dev.setTree(healthTree("com.google.android.apps.nexuslauncher", "Search"))
	backText := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(backText, "no longer on screen") {
		t.Errorf("intentional BACK falsely flagged exit:\n%s", backText)
	}

	// Relaunch + crash dialog triggers unexpected exit immediately.
	f.ok("mobile_launch_app", `{"packageName":"com.example.shop"}`)
	f.dev.setTree(healthTree("android", "Shop keeps stopping"))
	dialogText := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(dialogText, "crash dialog shown") {
		t.Errorf("expected crash dialog exit note:\n%s", dialogText)
	}
}

func TestHealthSwallowsProbeErrorsAndSkipsRemote(t *testing.T) {
	f := newFixture(t)
	f.dev.setTree(healthTree("com.example.shop", "Home"))
	f.dev.err["ListCrashes"] = errors.New("dropbox unavailable")
	f.dev.err["Logs"] = errors.New("logcat failed")
	f.ok("mobile_list_elements_on_screen", "")
	f.now = f.now.Add(5 * time.Second)
	f.ok("mobile_list_elements_on_screen", "")

	// Remote device skips ADB probes altogether.
	g := newFixture(t)
	g.dev.remote = true
	g.dev.setTree(healthTree("com.example.shop", "Home"))
	g.ok("mobile_list_elements_on_screen", "")
	g.now = g.now.Add(5 * time.Second)
	g.ok("mobile_list_elements_on_screen", "")
	if g.dev.called("ListCrashes") != 0 || g.dev.called("Logs") != 0 {
		t.Errorf("remote device called ADB probes: %v", g.dev.calls)
	}
}
