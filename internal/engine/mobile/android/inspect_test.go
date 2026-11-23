package android_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/engine/mobile/android"
)

// scriptRunner answers adb calls from a table keyed by the words of the call after -s <serial>.
type scriptRunner struct {
	answers map[string]string
	calls   []string
}

func (s *scriptRunner) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "-s" {
		args = args[2:]
	}
	key := strings.Join(args, " ")
	s.calls = append(s.calls, key)
	out, ok := s.answers[key]
	if !ok {
		return nil, errors.New("adb failed: not scripted: " + key)
	}
	return []byte(out), nil
}
func (s *scriptRunner) Quiet(context.Context, string, ...string) error { return nil }
func (s *scriptRunner) Start(context.Context, string, ...string) (android.Child, error) {
	return nil, errors.New("not used")
}

func shellKey(words ...string) string { return "shell " + android.ShellCommand(words...) }

func TestListDevicesTagsEmulatorsAndPhysicalDevices(t *testing.T) {
	r := &scriptRunner{answers: map[string]string{"devices -l": "List of devices attached\n" +
		"emulator-5554\tdevice product:sdk model:sdk_gphone64 device:emu64a transport_id:1\n" +
		"R58M123ABC\tdevice usb:1-1 product:beyond model:SM_G973F device:beyond1 transport_id:2\n" +
		"bad;serial\toffline\n"}}
	devs, err := android.NewInspector("adb", r).ListDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 || !devs[0].Emulator || devs[0].Serial != "emulator-5554" || devs[1].Emulator || devs[1].Model != "SM_G973F" {
		t.Errorf("devices = %+v", devs)
	}
}

func TestInspectForegroundReadsEachShapeOfDumpsys(t *testing.T) {
	activities := shellKey("dumpsys", "activity", "activities")
	window := shellKey("dumpsys", "window")
	cases := []struct {
		name    string
		answers map[string]string
		pkg     string
		act     string
	}{
		{"resumed activity, android 10", map[string]string{activities: "  mResumedActivity: ActivityRecord{1a2b3c u0 com.example.shop/.MainActivity t42}\n"}, "com.example.shop", "com.example.shop.MainActivity"},
		{"top resumed, android 12", map[string]string{activities: "  topResumedActivity=ActivityRecord{9f u0 com.example.shop/com.example.shop.ui.HomeActivity t7 f}\n"}, "com.example.shop", "com.example.shop.ui.HomeActivity"},
		{"the window when the activities say nothing", map[string]string{activities: "ACTIVITY MANAGER ACTIVITIES\n", window: "  mCurrentFocus=Window{c0ffee u0 com.example.shop/com.example.shop.MainActivity}\n"}, "com.example.shop", "com.example.shop.MainActivity"},
		{"the focused app of the window", map[string]string{activities: "x", window: "  mFocusedApp=Token{abc ActivityRecord{def u0 com.shop.app/.Main t3}}\n"}, "com.shop.app", "com.shop.app.Main"},
		{"the launcher is not an answer, the next line is", map[string]string{activities: "  mResumedActivity: ActivityRecord{1 u0 com.google.android.apps.nexuslauncher/.NexusLauncherActivity t1}\n  mResumedActivity: ActivityRecord{2 u0 com.example.shop/.MainActivity t2}\n"}, "com.example.shop", "com.example.shop.MainActivity"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info, err := android.NewInspector("adb", &scriptRunner{answers: c.answers}).InspectForeground(context.Background(), "emulator-5554")
			if err != nil {
				t.Fatal(err)
			}
			if info.Package != c.pkg || info.Activity != c.act || !info.Found() {
				t.Errorf("info = %+v, want %s %s", info, c.pkg, c.act)
			}
		})
	}
}

func TestInspectForegroundFallsBackToThePackageListAndNeverGuesses(t *testing.T) {
	r := &scriptRunner{answers: map[string]string{
		shellKey("dumpsys", "activity", "activities"): "  mResumedActivity: ActivityRecord{1 u0 com.android.systemui/.Home t1}\n",
		shellKey("dumpsys", "window"):                 "nothing here\n",
		shellKey("pm", "list", "packages", "-3"):      "package:com.example.shop\npackage:com.aura.keyboard\npackage:not a package\n\n",
	}}
	info, err := android.NewInspector("adb", r).InspectForeground(context.Background(), "emulator-5554")
	if err != nil {
		t.Fatal(err)
	}
	if info.Found() || strings.Join(info.Candidates, ",") != "com.aura.keyboard,com.example.shop" {
		t.Errorf("info = %+v: with no app in front the candidates are listed, and none is picked", info)
	}
	// Every word of every shell call went through quoting.
	for _, c := range r.calls {
		if strings.HasPrefix(c, "shell ") && !strings.HasPrefix(c, "shell '") {
			t.Errorf("an unquoted shell call: %s", c)
		}
	}
}

func TestInspectRefusesASerialThatIsNotOne(t *testing.T) {
	r := &scriptRunner{}
	if _, err := android.NewInspector("adb", r).InspectForeground(context.Background(), "x; reboot"); err == nil {
		t.Error("a serial with a semicolon was used")
	}
	if len(r.calls) != 0 {
		t.Errorf("adb was called: %v", r.calls)
	}
}

func TestParseForegroundRefusesAnInvalidPackage(t *testing.T) {
	if _, _, ok := android.ParseForeground("mResumedActivity: ActivityRecord{1 u0 bad;pkg/.Main t1}"); ok {
		t.Error("an invalid package was taken")
	}
}
