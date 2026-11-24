package android_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile/android"
)

func testdata(t testing.TB, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section reads the parts of a capture that are separated by lines of ### name.
func section(t testing.TB, text string) map[string]string {
	t.Helper()
	out := map[string]string{}
	name := ""
	for _, l := range strings.Split(text, "\n") {
		if n, ok := strings.CutPrefix(l, "### "); ok {
			name = n
			continue
		}
		out[name] += l + "\n"
	}
	return out
}

func TestParseInputMethod(t *testing.T) {
	for _, name := range []string{"ime_12.txt", "ime_14.txt", "ime_16.txt"} {
		t.Run(name, func(t *testing.T) {
			s := section(t, testdata(t, name))
			pkg, shown := android.ParseInputMethod(s["settings"], s["dumpsys"])
			want := strings.TrimSpace(s["want"])
			got := "pkg=" + pkg + " shown=" + map[bool]string{true: "true", false: "false"}[shown]
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
	for _, c := range []struct {
		name, setting, dump, pkg string
		shown                    bool
	}{
		{"no keyboard set", "null\n", "", "", false},
		{"empty", "", "", "", false},
		{"not a package", "no slash\n", "mInputShown=true", "", true},
		{"a bad package", "../../x/.Svc\n", "", "", false},
		{"hidden android 12", "a.b/.C\n", "mInputShown=false\nmImeWindowVis=0\n", "a.b", false},
		{"visible bit only", "a.b/.C\n", "mImeWindowVis=0x2 foo", "a.b", true},
		{"the back disposition bit is not a keyboard", "a.b/.C\n", "mImeWindowVis=0x4", "a.b", false},
		{"decimal", "a.b/.C\n", "mImeWindowVis=3", "a.b", true},
		{"a name that holds the key", "a.b/.C\n", "xmInputShown=true", "a.b", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			pkg, shown := android.ParseInputMethod(c.setting, c.dump)
			if pkg != c.pkg || shown != c.shown {
				t.Errorf("got %q %v, want %q %v", pkg, shown, c.pkg, c.shown)
			}
		})
	}
}

func FuzzParseInputMethod(f *testing.F) {
	for _, n := range []string{"ime_12.txt", "ime_14.txt", "ime_16.txt"} {
		s := section(f, testdata(f, n))
		f.Add(s["settings"], s["dumpsys"])
	}
	f.Add("", "")
	f.Add("a/b", "mImeWindowVis=0xffffffffffffffffff")
	f.Fuzz(func(t *testing.T, setting, dump string) {
		pkg, _ := android.ParseInputMethod(setting, dump)
		if pkg != "" && strings.ContainsAny(pkg, "/ \n'") {
			t.Fatalf("package %q", pkg)
		}
	})
}

func TestParseLogcatLine(t *testing.T) {
	for _, c := range []struct {
		line string
		want android.LogEntry
		ok   bool
	}{
		{"11-23 10:15:29.002  5678  5678 E ShopApp : login failed: HTTP 500",
			android.LogEntry{Time: "11-23 10:15:29.002", PID: 5678, TID: 5678, Level: "E", Tag: "ShopApp", Message: "login failed: HTTP 500"}, true},
		{"11-23 10:15:28.101  1234  1234 I ActivityManager: Start proc 5678:com.example.shop/u0a123",
			android.LogEntry{Time: "11-23 10:15:28.101", PID: 1234, TID: 1234, Level: "I", Tag: "ActivityManager", Message: "Start proc 5678:com.example.shop/u0a123"}, true},
		{"01-02 03:04:05.006 1 2 D T:",
			android.LogEntry{Time: "01-02 03:04:05.006", PID: 1, TID: 2, Level: "D", Tag: "T"}, true},
		{"11-23 10:15:30.500  5678  5678 F libc    : Fatal signal 11\r",
			android.LogEntry{Time: "11-23 10:15:30.500", PID: 5678, TID: 5678, Level: "F", Tag: "libc", Message: "Fatal signal 11"}, true},
		{"--------- beginning of main", android.LogEntry{}, false},
		{"", android.LogEntry{}, false},
		{"11-23 10:15:30.500  5678  5678 X Tag: bad level", android.LogEntry{}, false},
		{"11-23 10:15:30.500  5678  5678 I no colon here", android.LogEntry{}, false},
		{"11-23 10:15:30  5678  5678 I Tag: no millis", android.LogEntry{}, false},
	} {
		got, ok := android.ParseLogcatLine(c.line)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%q = %+v, %v; want %+v, %v", c.line, got, ok, c.want, c.ok)
		}
	}
	n := 0
	for _, l := range strings.Split(testdata(t, "logcat_threadtime.txt"), "\n") {
		if _, ok := android.ParseLogcatLine(l); ok {
			n++
		}
	}
	if n != 7 {
		t.Errorf("parsed %d lines of the capture, want 7 (the two banners are skipped)", n)
	}
}

func FuzzParseLogcatLine(f *testing.F) {
	for _, l := range strings.Split(testdata(f, "logcat_threadtime.txt"), "\n") {
		f.Add(l)
	}
	f.Add("11-23 10:15:30.500 99999999999999999999 1 I T: x")
	f.Add(strings.Repeat("a", 40000))
	f.Fuzz(func(t *testing.T, line string) {
		e, ok := android.ParseLogcatLine(line)
		if ok && (e.Time == "" || len(e.Level) != 1 || len(e.Message) > 16<<10) {
			t.Fatalf("a bad entry %+v", e)
		}
	})
}

func TestParseLogFilters(t *testing.T) {
	got, err := android.ParseLogFilters([]string{"tag=ShopApp", "level!=d", "message=a=b", "pid=12", "process=com.example.shop", "subsystem=x"})
	if err != nil {
		t.Fatal(err)
	}
	want := []android.LogFilter{
		{Key: "tag", Value: "ShopApp"}, {Key: "level", Value: "D", Not: true}, {Key: "message", Value: "a=b"},
		{Key: "pid", Value: "12"}, {Key: "process", Value: "com.example.shop"}, {Key: "subsystem", Value: "x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filters = %+v", got)
	}
	for _, bad := range []string{"tag", "colour=red", "pid=abc", "level=Z", "level=ID", "process=a b", "process=$(id)", "tag=", "message=\x00"} {
		if _, err := android.ParseLogFilters([]string{bad}); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	// The errors teach the format.
	_, err = android.ParseLogFilters([]string{"flutter"})
	for _, want := range []string{"pid, process, tag, level", "message", "tag=flutter"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error for a filter with no = is %v, want %q in it", err, want)
		}
	}
	if _, err = android.ParseLogFilters([]string{"colour=red"}); err == nil || !strings.Contains(err.Error(), "such as tag=flutter") {
		t.Errorf("error for an unknown key = %v", err)
	}
}

func TestParseDropbox(t *testing.T) {
	got := android.ParseDropbox(testdata(t, "dropbox_crash.txt"), "data_app_crash")
	if len(got) != 3 {
		t.Fatalf("%d crashes: %+v", len(got), got)
	}
	c := got[0]
	wantTime := time.Date(2026, 11, 23, 10, 15, 30, 0, time.UTC)
	if c.Tag != "data_app_crash" || !c.Time.Equal(wantTime) || c.Process != "com.example.shop" || c.Package != "com.example.shop" {
		t.Errorf("first = %+v", c)
	}
	if c.ID != "data_app_crash@"+strconv.FormatInt(wantTime.UnixMilli(), 10) {
		t.Errorf("id = %s", c.ID)
	}
	if c.Summary != "java.lang.IllegalStateException: Cart is empty" {
		t.Errorf("summary = %q", c.Summary)
	}
	if !strings.Contains(c.Body, "at com.example.shop.CartActivity.checkout") || !strings.Contains(c.Body, "Caused by: java.lang.NullPointerException") || strings.Contains(c.Body, "=====") {
		t.Errorf("body = %q", c.Body)
	}
	if got[1].Package != "com.other.app" || got[1].ID == got[0].ID {
		t.Errorf("second = %+v", got[1])
	}
	if got[2].Summary != "java.lang.OutOfMemoryError: Failed to allocate a 33554448 byte allocation" {
		t.Errorf("third = %+v", got[2])
	}
	// The same output gives the same ids.
	again := android.ParseDropbox(testdata(t, "dropbox_crash.txt"), "data_app_crash")
	for i := range got {
		if got[i].ID != again[i].ID {
			t.Errorf("id %d changed: %s then %s", i, got[i].ID, again[i].ID)
		}
	}

	anr := android.ParseDropbox(testdata(t, "dropbox_anr.txt"), "data_app_anr")
	if len(anr) != 1 || !strings.HasPrefix(anr[0].Summary, "Input dispatching timed out") || anr[0].Package != "com.example.shop" || anr[0].Tag != "data_app_anr" {
		t.Errorf("anr = %+v", anr)
	}
	if got := android.ParseDropbox("Drop box contents: 0 entries\n", "data_app_crash"); len(got) != 0 {
		t.Errorf("an empty box gave %+v", got)
	}
	// Two entries in the same second do not share an id.
	twin := "2026-11-23 10:00:00 data_app_crash (text, 1 bytes)\nProcess: a.b\n\nboom\n2026-11-23 10:00:00 data_app_crash (text, 1 bytes)\nProcess: a.c\n\nboom\n"
	if got := android.ParseDropbox(twin, "data_app_crash"); len(got) != 2 || got[0].ID == got[1].ID {
		t.Errorf("twins = %+v", got)
	}
}

func FuzzParseDropbox(f *testing.F) {
	f.Add(testdata(f, "dropbox_crash.txt"), "data_app_crash")
	f.Add(testdata(f, "dropbox_anr.txt"), "data_app_anr")
	f.Add("2026-13-45 99:99:99 x (a)\n", "t")
	f.Add("2026-11-23 10:00:00 t (x)\n"+strings.Repeat("a\n", 50000), "t")
	f.Fuzz(func(t *testing.T, out, tag string) {
		seen := map[string]bool{}
		for _, c := range android.ParseDropbox(out, tag) {
			if c.ID == "" || seen[c.ID] || len(c.Body) > 16<<10+16 {
				t.Fatalf("a bad crash %q (%d)", c.ID, len(c.Body))
			}
			seen[c.ID] = true
		}
	})
}

func TestParseCrashBuffer(t *testing.T) {
	got := android.ParseCrashBuffer(testdata(t, "crash_buffer.txt"), 2026)
	if len(got) != 2 {
		t.Fatalf("%d crashes: %+v", len(got), got)
	}
	c := got[0]
	if c.Tag != "crash" || c.Process != "com.example.shop" || c.Package != "com.example.shop" || c.Summary != "java.lang.IllegalStateException: Cart is empty" {
		t.Errorf("first = %+v", c)
	}
	if !c.Time.Equal(time.Date(2026, 11, 23, 10, 15, 30, 120_000_000, time.UTC)) {
		t.Errorf("time = %v", c.Time)
	}
	if strings.Contains(c.Body, "Force finishing") {
		t.Errorf("a line of another process is in the body: %q", c.Body)
	}
	if got[1].Package != "com.third.app" || !strings.Contains(got[1].Summary, "divide by zero") {
		t.Errorf("second = %+v", got[1])
	}
	if got := android.ParseCrashBuffer("--------- beginning of crash\n", 2026); len(got) != 0 {
		t.Errorf("no crash gave %+v", got)
	}
}

func FuzzParseCrashBuffer(f *testing.F) {
	f.Add(testdata(f, "crash_buffer.txt"), 2026)
	f.Add("11-31 25:61:61.000 1 1 E AndroidRuntime: FATAL EXCEPTION: main", 0)
	f.Fuzz(func(t *testing.T, out string, year int) {
		for _, c := range android.ParseCrashBuffer(out, year) {
			if c.ID == "" || c.Tag != "crash" {
				t.Fatalf("a bad crash %+v", c)
			}
		}
	})
}

func TestParseWM(t *testing.T) {
	w, h, ok := android.ParseWMSize(testdata(t, "wm_size_override.txt"))
	if !ok || w != 1080 || h != 1920 {
		t.Errorf("override size = %d x %d, %v", w, h, ok)
	}
	w, h, ok = android.ParseWMSize("Physical size: 720x1280\n")
	if !ok || w != 720 || h != 1280 {
		t.Errorf("physical size = %d x %d, %v", w, h, ok)
	}
	for _, bad := range []string{"", "Physical size: 0x0", "Physical size: axb", "size 1080x1920", "Override size: 1080x"} {
		if _, _, ok := android.ParseWMSize(bad); ok {
			t.Errorf("%q was read as a size", bad)
		}
	}
	if d, ok := android.ParseWMDensity(testdata(t, "wm_density.txt")); !ok || d != 320 {
		t.Errorf("density = %d, %v", d, ok)
	}
	if d, ok := android.ParseWMDensity("Physical density: 420\n"); !ok || d != 420 {
		t.Errorf("physical density = %d, %v", d, ok)
	}
}

func FuzzParseWMSize(f *testing.F) {
	f.Add(testdata(f, "wm_size_override.txt"))
	f.Add("Physical size: 99999x99999")
	f.Fuzz(func(t *testing.T, out string) {
		if w, h, ok := android.ParseWMSize(out); ok && (w <= 0 || h <= 0) {
			t.Fatalf("size %d x %d", w, h)
		}
		if d, ok := android.ParseWMDensity(out); ok && d <= 0 {
			t.Fatalf("density %d", d)
		}
	})
}

func TestParseDeviceStatesAndPostures(t *testing.T) {
	s14 := android.ParseDeviceStates(testdata(t, "device_state_14.txt"))
	want14 := []android.DeviceState{{0, "CLOSED"}, {1, "HALF_OPENED"}, {2, "OPENED"}, {3, "REAR_DISPLAY_MODE"}}
	if !reflect.DeepEqual(s14, want14) {
		t.Errorf("states 14 = %+v", s14)
	}
	s16 := android.ParseDeviceStates(testdata(t, "device_state_16.txt"))
	want16 := []android.DeviceState{{1, "FOLDED"}, {2, "HALF_FOLDED"}, {3, "UNFOLDED"}}
	if !reflect.DeepEqual(s16, want16) {
		t.Errorf("states 16 = %+v", s16)
	}
	for _, c := range []struct {
		states  []android.DeviceState
		posture string
		id      int
	}{{s14, "folded", 0}, {s14, "half-open", 1}, {s14, "open", 2}, {s16, "folded", 1}, {s16, "half-open", 2}, {s16, "open", 3}} {
		got, ok := android.StateFor(c.states, c.posture)
		if !ok || got.ID != c.id {
			t.Errorf("%s = %+v, %v; want id %d", c.posture, got, ok, c.id)
		}
	}
	if _, ok := android.StateFor(nil, "open"); ok {
		t.Error("a state of no states")
	}
	if got := android.ParseDeviceStates("Supported states: []\n"); len(got) != 0 {
		t.Errorf("a phone gave %+v", got)
	}
}

func FuzzParseDeviceStates(f *testing.F) {
	f.Add(testdata(f, "device_state_14.txt"))
	f.Add(testdata(f, "device_state_16.txt"))
	f.Add("identifier=99999999999999999999, name='")
	f.Fuzz(func(t *testing.T, out string) {
		seen := map[int]bool{}
		for _, s := range android.ParseDeviceStates(out) {
			if seen[s.ID] || s.Name == "" {
				t.Fatalf("bad state %+v", s)
			}
			seen[s.ID] = true
		}
	})
}

func TestADBVersion(t *testing.T) {
	if got := android.ParseADBVersion(testdata(t, "adb_version.txt")); got != "36.0.0" {
		t.Errorf("version = %q", got)
	}
	if got := android.ParseADBVersion("Android Debug Bridge version 1.0.41\n"); got != "1.0.41" {
		t.Errorf("protocol version = %q", got)
	}
	if got := android.ParseADBVersion("nonsense"); got != "" {
		t.Errorf("version = %q", got)
	}
}

func TestButtonKeycode(t *testing.T) {
	for name, want := range map[string]int{
		"HOME": 3, "back": 4, "Enter": 66, "VOLUME_UP": 24, "volume-down": 25, "MENU": 82, "APP_SWITCH": 187, "recents": 187,
		"TAB": 61, "DEL": 67, "backspace": 67, "FORWARD_DEL": 112, "SEARCH": 84, "ESCAPE": 111, "POWER": 26,
		"DPAD_UP": 19, "DPAD_DOWN": 20, "DPAD_LEFT": 21, "DPAD_RIGHT": 22, "DPAD_CENTER": 23, " keycode_home ": 3,
	} {
		if got, ok := android.ButtonKeycode(name); !ok || got != want {
			t.Errorf("%q = %d, %v; want %d", name, got, ok, want)
		}
	}
	for _, bad := range []string{"", "NOPE", "HOME;reboot", "12", "KEYCODE_"} {
		if _, ok := android.ButtonKeycode(bad); ok {
			t.Errorf("%q is a button", bad)
		}
	}
}

func TestLaunchArgumentsAndLocales(t *testing.T) {
	k, v, err := android.ParseLaunchArgument("com.x.key_1=hello world=1")
	if err != nil || k != "com.x.key_1" || v != "hello world=1" {
		t.Errorf("argument = %q %q %v", k, v, err)
	}
	for _, bad := range []string{"novalue", "=v", "bad key=v", "a;b=v", "k=\x00", "k=" + strings.Repeat("x", 1025), strings.Repeat("k", 129) + "=v"} {
		if _, _, err := android.ParseLaunchArgument(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	for _, ok := range []string{"en", "en-US", "id-ID", "zh-Hans-CN", "sr-Latn-RS"} {
		if !android.ValidLocale(ok) {
			t.Errorf("%q was refused", ok)
		}
	}
	for _, bad := range []string{"", "e", "en_US", "en-", "en US", "en;reboot", "en-US-aaaaaaaaa", "$(id)"} {
		if android.ValidLocale(bad) {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func FuzzParseLaunchArgument(f *testing.F) {
	f.Add("a=b")
	f.Add("a.b_c=" + strings.Repeat("v", 2000))
	f.Fuzz(func(t *testing.T, s string) {
		k, v, err := android.ParseLaunchArgument(s)
		if err == nil && (k == "" || len(k) > 128 || len(v) > 1024 || strings.ContainsRune(v, 0)) {
			t.Fatalf("accepted %q %q", k, v)
		}
	})
}
