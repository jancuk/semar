package android

import (
	"context"
	"fmt"
	"strings"
)

// launcherOf is the component that the package manager gives for the launcher of the package, or "" when
// there is none.
func (a *adb) launcherOf(ctx context.Context, pkg string) (string, error) {
	out, err := a.shell(ctx, "cmd", "package", "resolve-activity", "--brief", "-c", "android.intent.category.LAUNCHER", pkg)
	if err != nil {
		return "", fmt.Errorf("the launcher activity of %s could not be found: %w", pkg, err)
	}
	return componentLine(out), nil
}

// componentLine is the component in the output of resolve-activity --brief, or "" for "No activity found".
func componentLine(out string) string {
	comp := ""
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); strings.Contains(line, "/") && !strings.Contains(line, " ") {
			comp = line
		}
	}
	return comp
}

// ActivityCheck is what a device says about an app and the activity that appActivity names.
type ActivityCheck struct {
	// Installed is false when the device has no package of that name.
	Installed bool
	// Launcher is the activity that the launcher starts, "" when the package has none.
	Launcher string
	// Component is the activity that appActivity names, as the device resolves it. It is "" when the
	// package has no such activity. With no appActivity it is the Launcher.
	Component string
}

// CheckActivity asks the device, and starts nothing. A wrong appActivity otherwise shows only when the
// first scenario starts the app.
func CheckActivity(ctx context.Context, run Runner, bin, serial, pkg, activity string) (ActivityCheck, error) {
	a := &adb{bin: bin, run: run, serial: serial}
	paths, err := a.packagePaths(ctx, pkg)
	if err != nil || len(paths) == 0 {
		return ActivityCheck{}, err
	}
	res := ActivityCheck{Installed: true}
	if res.Launcher, err = a.launcherOf(ctx, pkg); err != nil {
		return res, err
	}
	res.Component = res.Launcher
	if activity == "" {
		return res, nil
	}
	out, err := a.shell(ctx, "cmd", "package", "resolve-activity", "--brief", "-n", pkg+"/"+activity)
	if err != nil {
		return res, fmt.Errorf("the activity %s could not be looked up: %w", activity, err)
	}
	res.Component = componentLine(out)
	return res, nil
}
