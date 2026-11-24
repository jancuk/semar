package mobilemcp_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mobilemcp"
)

var wantTools = []string{
	"mobile_list_available_devices", "mobile_use_device", "mobile_get_screen_size", "mobile_get_orientation",
	"mobile_set_orientation", "mobile_fold_device", "mobile_set_location", "mobile_clipboard",
	"mobile_list_apps", "mobile_get_foreground_app", "mobile_launch_app", "mobile_terminate_app",
	"mobile_install_app", "mobile_uninstall_app", "mobile_clear_app_data",
	"mobile_take_screenshot", "mobile_save_screenshot", "mobile_list_elements_on_screen",
	"mobile_click_on_screen_at_coordinates", "mobile_double_tap_on_screen", "mobile_long_press_on_screen_at_coordinates",
	"mobile_drag_on_screen", "mobile_swipe_on_screen", "mobile_type_keys", "mobile_press_button", "mobile_open_url",
	"mobile_start_screen_recording", "mobile_stop_screen_recording", "mobile_get_device_logs",
	"mobile_list_crashes", "mobile_get_crash", "mobile_batch_commands",
}

var readOnlyTools = map[string]bool{
	"mobile_list_available_devices": true, "mobile_get_screen_size": true, "mobile_get_orientation": true,
	"mobile_list_apps": true, "mobile_get_foreground_app": true, "mobile_take_screenshot": true,
	"mobile_save_screenshot": true, "mobile_list_elements_on_screen": true, "mobile_get_device_logs": true,
	"mobile_list_crashes": true, "mobile_get_crash": true, "mobile_clipboard": true,
}

func TestToolsAreTheExpectedSetAndPassTheSchemaCheck(t *testing.T) {
	f := newFixture(t)
	tools := f.m.Tools()
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	if !reflect.DeepEqual(names, wantTools) {
		t.Errorf("tools = %v", names)
	}
	if len(tools) != 32 {
		t.Errorf("%d tools", len(tools))
	}
	for _, tool := range tools {
		if err := mcp.ValidatePortableSchema(tool.Input); err != nil {
			t.Errorf("%s: %v", tool.Name, err)
		}
		if tool.Surface != "mobile" {
			t.Errorf("%s: surface %q", tool.Name, tool.Surface)
		}
		if tool.Annotations.ReadOnly == nil || tool.Write == *tool.Annotations.ReadOnly {
			t.Errorf("%s: Write=%v with readOnly=%v", tool.Name, tool.Write, tool.Annotations.ReadOnly)
		}
		if readOnlyTools[tool.Name] != !tool.Write {
			t.Errorf("%s: read-only is %v, want %v", tool.Name, !tool.Write, readOnlyTools[tool.Name])
		}
		if tool.Description == "" || tool.Title == "" || tool.Handler == nil {
			t.Errorf("%s: incomplete", tool.Name)
		}
	}
	// The server accepts them, and its own check passes.
	if _, err := mcp.NewServer(mcp.Info{Name: "t"}, tools, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mobile_uninstall_app", "mobile_clear_app_data", "mobile_install_app"} {
		if d := f.tools[name].Annotations.Destructive; d == nil || !*d {
			t.Errorf("%s is not marked destructive", name)
		}
	}
}

func TestSchemaGolden(t *testing.T) {
	f := newFixture(t)
	required := map[string][]string{
		"mobile_use_device": {"device"}, "mobile_set_orientation": {"orientation"}, "mobile_fold_device": {"posture"},
		"mobile_launch_app": {"packageName"}, "mobile_terminate_app": {"packageName"}, "mobile_install_app": {"path"},
		"mobile_uninstall_app": {"packageName"}, "mobile_clear_app_data": {"packageName"}, "mobile_save_screenshot": {"saveTo"},
		"mobile_swipe_on_screen": {"direction"}, "mobile_press_button": {"button"}, "mobile_open_url": {"url"},
		"mobile_get_crash": {"id"}, "mobile_batch_commands": {"steps"},
	}
	for name, tool := range f.tools {
		got, _ := tool.Input["required"].([]string)
		sort.Strings(got)
		want := required[name]
		if !reflect.DeepEqual(append([]string(nil), got...), append([]string(nil), want...)) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("%s: required = %v, want %v", name, got, want)
		}
	}
	props := func(name string) []string {
		p := f.tools[name].Input["properties"].(map[string]mcp.Schema)
		var keys []string
		for k := range p {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	for name, want := range map[string][]string{
		"mobile_type_keys":                           {"ref", "replace", "secretEnv", "submit", "text", "via"},
		"mobile_drag_on_screen":                      {"fromRef", "hold", "toRef", "x1", "x2", "y1", "y2"},
		"mobile_get_device_logs":                     {"filter", "limit", "recent", "saveTo"},
		"mobile_take_screenshot":                     {"clip", "maxSize", "scale"},
		"mobile_long_press_on_screen_at_coordinates": {"duration", "ref", "x", "y"},
		"mobile_batch_commands":                      {"listElementsAtEnd", "steps", "stopOnError"},
		"mobile_list_crashes":                        {"package", "since"},
		"mobile_start_screen_recording":              {"output", "timeLimit"},
	} {
		if got := props(name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: properties = %v, want %v", name, got, want)
		}
	}
}

func TestUnknownArgumentsAreRefused(t *testing.T) {
	f := newFixture(t)
	for name := range f.tools {
		res := f.call(name, `{"nope": 1}`)
		if !res.IsError || !strings.Contains(textOf(res), "unknown argument") {
			t.Errorf("%s: %q", name, textOf(res))
		}
	}
}

func TestDeviceToolsHappyPath(t *testing.T) {
	f := newFixture(t)
	d := f.dev

	var size map[string]int
	decode(t, f.ok("mobile_get_screen_size", ""), &size)
	if size["width"] != 1080 || size["height"] != 2400 || size["density"] != 420 {
		t.Errorf("size = %v", size)
	}

	var o map[string]string
	decode(t, f.ok("mobile_get_orientation", ""), &o)
	if o["orientation"] != "portrait" {
		t.Errorf("orientation = %v", o)
	}
	f.ok("mobile_set_orientation", `{"orientation":"Landscape"}`)
	if d.orient != "landscape" {
		t.Errorf("device orientation = %s", d.orient)
	}
	f.fails("mobile_set_orientation", `{"orientation":"sideways"}`, "portrait or landscape")

	f.ok("mobile_fold_device", `{"posture":"half-open"}`)
	if d.called("Fold half-open") != 1 {
		t.Errorf("calls = %v", d.calls)
	}
	f.fails("mobile_fold_device", `{"posture":"open","angle":90}`, "iOS only")

	f.ok("mobile_set_location", `{"latitude":-6.2,"longitude":106.8}`)
	if d.called("SetLocation -6.2 106.8") != 1 {
		t.Errorf("calls = %v", d.calls)
	}
	f.ok("mobile_set_location", `{}`)
	if d.called("ClearLocation") != 1 {
		t.Errorf("calls = %v", d.calls)
	}
	f.fails("mobile_set_location", `{"latitude":1}`, "both")
	f.fails("mobile_set_location", `{"latitude":91,"longitude":0}`, "latitude")

	f.ok("mobile_clipboard", `{"text":"hello"}`)
	var cb map[string]any
	decode(t, f.ok("mobile_clipboard", ""), &cb)
	if cb["text"] != "hello" {
		t.Errorf("clipboard = %v", cb)
	}

	var devs struct {
		Devices []struct {
			Serial   string `json:"serial"`
			Selected bool   `json:"selected"`
		} `json:"devices"`
	}
	decode(t, f.ok("mobile_list_available_devices", ""), &devs)
	if len(devs.Devices) != 1 || devs.Devices[0].Serial != "emulator-5554" || !devs.Devices[0].Selected {
		t.Errorf("devices = %+v", devs)
	}
}

type fakeInspector struct {
	list []mobile.DeviceInfo
	err  error
}

func (i fakeInspector) ListDevices(context.Context) ([]mobile.DeviceInfo, error) {
	return i.list, i.err
}
func (i fakeInspector) InspectForeground(context.Context, string) (mobile.AppInfo, error) {
	return mobile.AppInfo{}, nil
}

func TestListDevicesUsesTheInspector(t *testing.T) {
	f := newFixture(t, func(o *mobilemcp.Options, _ *fixture) {
		o.Inspector = fakeInspector{list: []mobile.DeviceInfo{
			{Serial: "emulator-5554", Model: "sdk_gphone", State: "device", Emulator: true},
			{Serial: "R58M", Model: "Pixel 8", State: "device"},
		}}
		o.Serial = "emulator-5554"
	})
	var devs struct {
		Devices []struct {
			Serial   string `json:"serial"`
			Model    string `json:"model"`
			State    string `json:"state"`
			Emulator bool   `json:"emulator"`
			Selected bool   `json:"selected"`
		} `json:"devices"`
	}
	decode(t, f.ok("mobile_list_available_devices", ""), &devs)
	if len(devs.Devices) != 2 || !devs.Devices[0].Selected || devs.Devices[1].Selected || devs.Devices[1].Model != "Pixel 8" || devs.Devices[1].Emulator {
		t.Errorf("devices = %+v", devs)
	}
	if f.opens.Load() != 0 {
		t.Error("listing devices opened the device")
	}
	f2 := newFixture(t, func(o *mobilemcp.Options, _ *fixture) { o.Inspector = fakeInspector{err: errBoom} })
	f2.fails("mobile_list_available_devices", "", "could not be listed")
}

func TestUseDeviceReleasesTheOldOneAndOpensTheNew(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_get_screen_size", "")
	if f.opens.Load() != 1 || f.closes.Load() != 0 {
		t.Fatalf("opens %d closes %d", f.opens.Load(), f.closes.Load())
	}
	res := f.ok("mobile_use_device", `{"device":"R58M","deviceType":"android"}`)
	if f.opens.Load() != 2 || f.closes.Load() != 1 {
		t.Errorf("opens %d closes %d", f.opens.Load(), f.closes.Load())
	}
	if !strings.Contains(textOf(res), "R58M") {
		t.Errorf("result = %s", textOf(res))
	}
	f.fails("mobile_use_device", `{"device":"  "}`, "serial")
	if err := f.m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.closes.Load() != 2 {
		t.Errorf("closes = %d", f.closes.Load())
	}
	if err := f.m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.closes.Load() != 2 {
		t.Errorf("a second Close released again: %d", f.closes.Load())
	}
}

func TestOpenerFailureReachesTheModelAndIsNotSticky(t *testing.T) {
	fail := true
	f := newFixture(t, func(o *mobilemcp.Options, fx *fixture) {
		inner := o.Opener
		o.Opener = func(ctx context.Context, serial string) (mobilemcp.Device, func(context.Context) error, error) {
			if fail {
				return nil, nil, errors.New("no emulator is running: start one or name a device")
			}
			return inner(ctx, serial)
		}
	})
	f.fails("mobile_get_screen_size", "", "no emulator is running")
	fail = false
	f.ok("mobile_get_screen_size", "")
}

func TestAppToolsHappyPath(t *testing.T) {
	f := newFixture(t)
	d := f.dev
	d.fgPkg, d.fgAct = "com.example.shop", ".MainActivity"

	var apps struct{ Apps []string }
	decode(t, f.ok("mobile_list_apps", `{"includeSystem":true}`), &apps)
	if len(apps.Apps) != 1 || d.called("ListApps true") != 1 {
		t.Errorf("apps = %v, calls = %v", apps, d.calls)
	}
	var fg map[string]string
	decode(t, f.ok("mobile_get_foreground_app", ""), &fg)
	if fg["package"] != "com.example.shop" || fg["activity"] != ".MainActivity" {
		t.Errorf("fg = %v", fg)
	}

	f.ok("mobile_launch_app", `{"packageName":"com.example.shop","locale":"en-US, fr-FR","arguments":["a=b"]}`)
	if d.called("LaunchApp com.example.shop [en-US fr-FR] [a=b]") != 1 {
		t.Errorf("calls = %v", d.calls)
	}
	f.fails("mobile_launch_app", `{"packageName":"not a package"}`, "package name")
	f.fails("mobile_launch_app", `{"packageName":"com.example.shop","locale":"x y z"}`, "locale")
	f.fails("mobile_launch_app", `{"packageName":"com.example.shop","arguments":["noequals"]}`, "")

	f.ok("mobile_terminate_app", `{"packageName":"com.example.shop"}`)
	if d.called("TerminateApp com.example.shop") != 1 {
		t.Errorf("calls = %v", d.calls)
	}

	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.apk"), []byte("PK"), 0o600); err != nil {
		t.Fatal(err)
	}
	f2 := newFixture(t, func(o *mobilemcp.Options, _ *fixture) { o.RepoRoot = repo })
	f2.ok("mobile_install_app", `{"path":"app.apk"}`)
	if f2.dev.called("InstallAPK app.apk") != 1 {
		t.Errorf("calls = %v", f2.dev.calls)
	}
	f2.fails("mobile_install_app", `{"path":"../app.apk"}`, "cannot be installed")
	f2.fails("mobile_install_app", `{"path":"missing.apk"}`, "does not exist")
	f2.fails("mobile_install_app", `{"path":"`+filepath.Join(repo, "app.apk")+`.txt"}`, ".apk")

	f.ok("mobile_uninstall_app", `{"packageName":"com.example.shop"}`)
	f.ok("mobile_clear_app_data", `{"packageName":"com.example.shop"}`)
	if d.called("UninstallApp") != 1 || d.called("ClearAppData") != 1 {
		t.Errorf("calls = %v", d.calls)
	}
}

func TestDestructiveToolsNeedConfirmOnAPhysicalDevice(t *testing.T) {
	f := newFixture(t)
	f.dev.emulator, f.dev.serial = false, "R58M"
	for _, name := range []string{"mobile_uninstall_app", "mobile_clear_app_data"} {
		f.fails(name, `{"packageName":"com.example.shop"}`, "confirm")
		f.fails(name, `{"packageName":"com.example.shop","confirm":false}`, "R58M")
	}
	if f.dev.called("UninstallApp")+f.dev.called("ClearAppData") != 0 {
		t.Errorf("a refused call reached the device: %v", f.dev.calls)
	}
	f.ok("mobile_uninstall_app", `{"packageName":"com.example.shop","confirm":true}`)
	f.ok("mobile_clear_app_data", `{"packageName":"com.example.shop","confirm":true}`)
	if f.dev.called("UninstallApp")+f.dev.called("ClearAppData") != 2 {
		t.Errorf("calls = %v", f.dev.calls)
	}
	// A server at serverUrl never says it is an emulator, so it needs the confirm too.
	g := newFixture(t)
	g.dev.emulator, g.dev.remote = false, true
	g.fails("mobile_clear_app_data", `{"packageName":"com.example.shop"}`, "confirm")
}

func TestDeviceErrorsReachTheModel(t *testing.T) {
	f := newFixture(t)
	f.dev.err["LaunchApp"] = android.ErrUnsupported
	res := f.call("mobile_launch_app", `{"packageName":"com.example.shop"}`)
	if !res.IsError || !strings.Contains(textOf(res), "not supported") {
		t.Errorf("result = %s", textOf(res))
	}
}

func TestRecordingStartAndStop(t *testing.T) {
	f := newFixture(t)
	f.dev.recPath = filepath.Join(f.realOut, "clip.mp4")
	if err := os.WriteFile(f.dev.recPath, make([]byte, 1234), 0o600); err != nil {
		t.Fatal(err)
	}
	var start map[string]any
	decode(t, f.ok("mobile_start_screen_recording", `{"output":"clip.mp4","timeLimit":60}`), &start)
	if f.dev.recOpts.Output != f.dev.recPath || f.dev.recOpts.TimeLimit != 60*time.Second {
		t.Errorf("options = %+v", f.dev.recOpts)
	}
	f.fails("mobile_start_screen_recording", `{}`, "already going on")
	f.fails("mobile_use_device", `{"device":"x"}`, "recording")
	f.now = f.now.Add(12*time.Second + 400*time.Millisecond)
	var stop struct {
		Path    string  `json:"path"`
		Bytes   int64   `json:"bytes"`
		Seconds float64 `json:"seconds"`
	}
	decode(t, f.ok("mobile_stop_screen_recording", ""), &stop)
	if stop.Path != f.dev.recPath || stop.Bytes != 1234 || stop.Seconds != 12.4 {
		t.Errorf("stop = %+v", stop)
	}
	// A name is made when none is given, and the path guard applies.
	f.ok("mobile_start_screen_recording", `{}`)
	if !strings.HasPrefix(filepath.Base(f.dev.recOpts.Output), "recording-20261123-120012") {
		t.Errorf("output = %s", f.dev.recOpts.Output)
	}
	f.ok("mobile_stop_screen_recording", "")
	f.fails("mobile_start_screen_recording", `{"output":"../x.mp4"}`, "refused")
	f.fails("mobile_start_screen_recording", `{"output":"x.mov"}`, "extension")
	f.fails("mobile_start_screen_recording", `{"timeLimit":181}`, "timeLimit")
	g := newFixture(t, func(o *mobilemcp.Options, _ *fixture) { o.Outputs = nil })
	g.fails("mobile_start_screen_recording", `{}`, "no output folder")
}

func TestLogsHappyPathAndSaveTo(t *testing.T) {
	f := newFixture(t)
	f.dev.logs = []android.LogEntry{
		{Time: "11-23 12:00:00.000", PID: 1, TID: 2, Level: "E", Tag: "OkHttp", Message: "failed"},
		{Time: "11-23 12:00:01.000", PID: 1, TID: 2, Level: "I", Tag: "App", Message: "ok"},
	}
	var got struct {
		Count   int `json:"count"`
		Entries []struct {
			Level, Tag, Message string
		} `json:"entries"`
	}
	decode(t, f.ok("mobile_get_device_logs", `{"limit":50,"filter":["tag=OkHttp"],"recent":true}`), &got)
	if got.Count != 2 || got.Entries[0].Tag != "OkHttp" {
		t.Errorf("got = %+v", got)
	}
	q := f.dev.logQuery
	if q.Limit != 50 || q.Follow || len(q.Filters) != 1 || q.Filters[0].Key != "tag" {
		t.Errorf("query = %+v", q)
	}
	f.ok("mobile_get_device_logs", "")
	if q := f.dev.logQuery; q.Limit != 100 || !q.Follow {
		t.Errorf("default query = %+v", q)
	}
	var saved struct {
		Count int
		Path  string
	}
	decode(t, f.ok("mobile_get_device_logs", `{"saveTo":"logs/run.jsonl","recent":true}`), &saved)
	b, err := os.ReadFile(saved.Path)
	if err != nil || strings.Count(string(b), "\n") != 2 {
		t.Errorf("file = %q, %v", b, err)
	}
	f.fails("mobile_get_device_logs", `{"saveTo":"run.exe"}`, "extension")
	f.fails("mobile_get_device_logs", `{"saveTo":"../run.log"}`, "refused")
	f.fails("mobile_get_device_logs", `{"limit":10001}`, "limit")
	f.fails("mobile_get_device_logs", `{"filter":["bogus"]}`, "")
}

func TestCrashesHappyPath(t *testing.T) {
	f := newFixture(t)
	now := f.now
	f.dev.crashes = []android.Crash{
		{ID: "data_app_crash@2", Tag: "data_app_crash", Time: now.Add(-10 * time.Minute), Process: "com.example.shop", Package: "com.example.shop", Summary: "java.lang.NullPointerException", Body: "stack trace"},
		{ID: "data_app_anr@1", Tag: "data_app_anr", Time: now.Add(-5 * time.Hour), Process: "com.example.shop", Package: "com.example.shop", Summary: "ANR"},
	}
	var list struct {
		Crashes []struct{ ID, Summary string }
	}
	decode(t, f.ok("mobile_list_crashes", `{"package":"com.example.shop"}`), &list)
	if len(list.Crashes) != 2 || f.dev.called("ListCrashes com.example.shop") != 1 {
		t.Errorf("list = %+v, calls = %v", list, f.dev.calls)
	}
	if strings.Contains(textOf(f.ok("mobile_list_crashes", "")), "stack trace") {
		t.Error("a list holds a body")
	}
	decode(t, f.ok("mobile_list_crashes", `{"since":"1h"}`), &list)
	if len(list.Crashes) != 1 || list.Crashes[0].ID != "data_app_crash@2" {
		t.Errorf("since 1h = %+v", list)
	}
	decode(t, f.ok("mobile_list_crashes", `{"since":"`+now.Add(-6*time.Hour).Format(time.RFC3339)+`"}`), &list)
	if len(list.Crashes) != 2 {
		t.Errorf("since RFC3339 = %+v", list)
	}
	f.fails("mobile_list_crashes", `{"since":"yesterday"}`, "since")
	f.fails("mobile_list_crashes", `{"package":"bad pkg"}`, "package name")

	var crash struct{ Report, Summary string }
	decode(t, f.ok("mobile_get_crash", `{"id":"data_app_crash@2"}`), &crash)
	if crash.Report != "stack trace" || crash.Summary == "" {
		t.Errorf("crash = %+v", crash)
	}
	f.fails("mobile_get_crash", `{"id":"nope@1"}`, "no crash has that id")
	f.fails("mobile_get_crash", `{"id":""}`, "id")
}

func TestOpenURLHappyPath(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_open_url", `{"url":"https://example.com/a?b=1"}`)
	if f.dev.called("OpenURL https://example.com/a?b=1") != 1 {
		t.Errorf("calls = %v", f.dev.calls)
	}
	f.fails("mobile_open_url", `{"url":"file:///etc/passwd"}`, "never")
	if f.dev.called("OpenURL") != 1 {
		t.Error("a refused link reached the device")
	}
}

func TestPressButton(t *testing.T) {
	f := newFixture(t)
	for button, code := range map[string]int{"home": 3, "BACK": 4, "enter": 66, "VOLUME_UP": 24, "dpad-center": 23, "KEYCODE_TAB": 61, "recents": 187} {
		f.ok("mobile_press_button", `{"button":"`+button+`"}`)
		if got := f.dev.keys[len(f.dev.keys)-1]; got != code {
			t.Errorf("%s pressed %d, want %d", button, got, code)
		}
	}
	f.fails("mobile_press_button", `{"button":"WARP"}`, "not known")
}

func TestCloseSavesAScreenRecordingThatIsStillGoingBeforeItReleasesTheDevice(t *testing.T) {
	f := newFixture(t)
	f.ok("mobile_start_screen_recording", "")
	if f.dev.called("StartRecording") != 1 {
		t.Fatalf("the recording did not start: %v", f.dev.calls)
	}
	if err := f.m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.dev.called("StopRecording") != 1 {
		t.Errorf("the recording was not pulled on close: %v", f.dev.calls)
	}
	if f.closes.Load() != 1 {
		t.Errorf("the device was released %d times", f.closes.Load())
	}
	// With no recording, close does not stop one.
	g := newFixture(t)
	g.ok("mobile_get_screen_size", "")
	_ = g.m.Close(context.Background())
	if g.dev.called("StopRecording") != 0 {
		t.Errorf("close stopped a recording that never started: %v", g.dev.calls)
	}
}

func TestScreenSizeFollowsTheOrientation(t *testing.T) {
	f := newFixture(t)
	turn := func(w, h int) {
		f.dev.mu.Lock()
		defer f.dev.mu.Unlock()
		turned := *f.dev.tree
		turned.Width, turned.Height = w, h
		f.dev.tree = &turned
	}
	turn(2400, 1017)
	if got := textOf(f.ok("mobile_get_screen_size", "")); got != `{"density":420,"height":1080,"width":2400}` {
		t.Errorf("landscape size = %s", got)
	}
	turn(1080, 2337)
	if got := textOf(f.ok("mobile_get_screen_size", "")); got != `{"density":420,"height":2400,"width":1080}` {
		t.Errorf("portrait size = %s", got)
	}
	// A tablet that is wide by nature reports portrait at its natural rotation: the size must not be swapped.
	f.dev.screen = android.ScreenInfo{Width: 2560, Height: 1600, Density: 320}
	f.dev.orient = "portrait"
	turn(2560, 1500)
	if got := textOf(f.ok("mobile_get_screen_size", "")); got != `{"density":320,"height":1600,"width":2560}` {
		t.Errorf("wide tablet size = %s", got)
	}
	// With no listing of the screen, the orientation that the device reports decides.
	f.dev.screen = android.ScreenInfo{Width: 1080, Height: 2400, Density: 420}
	f.dev.mu.Lock()
	f.dev.treeErr = errors.New("no source")
	f.dev.mu.Unlock()
	f.ok("mobile_set_orientation", `{"orientation":"landscape"}`)
	if got := textOf(f.ok("mobile_get_screen_size", "")); got != `{"density":420,"height":1080,"width":2400}` {
		t.Errorf("landscape size with no listing = %s", got)
	}
}

func TestAnEmptyClipboardReadSaysWhy(t *testing.T) {
	f := newFixture(t)
	got := textOf(f.ok("mobile_clipboard", ""))
	if !strings.Contains(got, `"note"`) || !strings.Contains(got, "Android 10") {
		t.Errorf("an empty read = %s", got)
	}
	f.ok("mobile_clipboard", `{"text":"hello"}`)
	if got := textOf(f.ok("mobile_clipboard", "")); strings.Contains(got, `"note"`) {
		t.Errorf("a read with text has a note: %s", got)
	}
}

func TestLogFilterGivenAsAStringTeachesTheFormat(t *testing.T) {
	f := newFixture(t)
	f.fails("mobile_get_device_logs", `{"filter":"flutter"}`, "tag=flutter")
}
