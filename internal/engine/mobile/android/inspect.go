package android

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile"
)

// Inspector reads the devices and the app in front with adb. It is a mobile.AppInspector, and it needs no
// platform: it works before there is a config, a server on the device, or a driver.
type Inspector struct {
	bin string
	run Runner
}

var _ mobile.AppInspector = (*Inspector)(nil)

// NewInspector uses the adb program at bin. A nil runner starts the real program.
func NewInspector(bin string, run Runner) *Inspector {
	if run == nil {
		run = ExecRunner{}
	}
	return &Inspector{bin: bin, run: run}
}

// ListDevices lists the devices that adb sees, with the emulators tagged.
func (i *Inspector) ListDevices(ctx context.Context) ([]mobile.DeviceInfo, error) {
	a := &adb{bin: i.bin, run: i.run}
	out, err := a.out(ctx, "devices", "-l")
	if err != nil {
		return nil, fmt.Errorf("the devices could not be listed: %w", err)
	}
	var devices []mobile.DeviceInfo
	for _, d := range ParseDevices(out) {
		if !config.ValidAndroidSerial(d.Serial) {
			continue
		}
		devices = append(devices, mobile.DeviceInfo{Serial: d.Serial, Model: d.Model, State: d.State, Emulator: d.Emulator})
	}
	return devices, nil
}

// InspectForeground reads which app has the focus on the device. It asks dumpsys for the activities and
// then for the window, because devices of different versions write the focus in different places. When
// no app of a person is found, it gives the installed third-party apps and no guess.
func (i *Inspector) InspectForeground(ctx context.Context, serial string) (mobile.AppInfo, error) {
	if serial != "" && !config.ValidAndroidSerial(serial) {
		return mobile.AppInfo{}, fmt.Errorf("%q is not a device serial", serial)
	}
	a := &adb{bin: i.bin, run: i.run, serial: serial}
	for _, dump := range [][]string{{"dumpsys", "activity", "activities"}, {"dumpsys", "window"}} {
		out, err := a.shell(ctx, dump...)
		if err != nil {
			continue
		}
		if pkg, act, ok := ParseForeground(out); ok {
			return mobile.AppInfo{Package: pkg, Activity: act}, nil
		}
	}
	out, err := a.shell(ctx, "pm", "list", "packages", "-3")
	if err != nil {
		return mobile.AppInfo{}, fmt.Errorf("the apps of the device could not be listed: %w", err)
	}
	return mobile.AppInfo{Candidates: ParseThirdParty(out)}, nil
}

// The places where dumpsys writes the app that has the focus, in the order that they are trusted. Each
// has a component, package/activity, in the form that Android writes.
var focusLines = []*regexp.Regexp{
	regexp.MustCompile(`(?:topResumedActivity|mResumedActivity|ResumedActivity)[=:]\s*ActivityRecord\{[^}]*?\s([A-Za-z0-9_.]+)/([A-Za-z0-9_.$]*)[\s}]`),
	regexp.MustCompile(`mCurrentFocus=Window\{[^}]*?\s([A-Za-z0-9_.]+)/([A-Za-z0-9_.$]*)[\s}]`),
	regexp.MustCompile(`mFocusedApp=(?:Token\{[^}]*\s)?ActivityRecord\{[^}]*?\s([A-Za-z0-9_.]+)/([A-Za-z0-9_.$]*)[\s}]`),
	regexp.MustCompile(`mFocusedWindow=Window\{[^}]*?\s([A-Za-z0-9_.]+)/([A-Za-z0-9_.$]*)[\s}]`),
}

// systemPackages and the launchers are in front when no app of a person is: they are not an answer.
var systemPackages = []string{"android", "com.android.systemui", "com.android.launcher", "com.android.launcher3", "com.google.android.apps.nexuslauncher", "com.sec.android.app.launcher", "com.miui.home"}

// ParseForeground reads the package and the activity of the app in front from the output of dumpsys
// activity activities or dumpsys window. A relative activity such as .MainActivity is made whole with its
// package. It gives false when no line names an app of a person, or when a name is not a valid package.
func ParseForeground(out string) (pkg, activity string, ok bool) {
	for _, re := range focusLines {
		for _, m := range re.FindAllStringSubmatch(out, -1) {
			p, act := m[1], m[2]
			if !config.ValidAndroidPackage(p) || slices.Contains(systemPackages, p) {
				continue
			}
			if strings.HasPrefix(act, ".") {
				act = p + act
			}
			return p, act, true
		}
	}
	return "", "", false
}

// ParseThirdParty reads the packages of the output of pm list packages -3, sorted, and leaves out what is
// not a package name.
func ParseThirdParty(out string) []string {
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		p, ok := strings.CutPrefix(strings.TrimSpace(line), "package:")
		if !ok || !config.ValidAndroidPackage(p) || slices.Contains(pkgs, p) {
			continue
		}
		pkgs = append(pkgs, p)
	}
	slices.Sort(pkgs)
	return pkgs
}
