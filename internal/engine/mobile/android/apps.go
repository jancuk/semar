package android

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/config"
)

// ListApps lists the packages of the apps that a person installed, sorted. With includeSystem it lists
// every package.
func (c *Controller) ListApps(ctx context.Context, includeSystem bool) ([]string, error) {
	a, err := c.adb()
	if err != nil {
		return nil, err
	}
	words := []string{"pm", "list", "packages"}
	if !includeSystem {
		words = append(words, "-3")
	}
	out, err := a.shell(ctx, words...)
	if err != nil {
		return nil, fmt.Errorf("the apps of the device could not be listed: %w", err)
	}
	return ParseThirdParty(out), nil
}

// Foreground gives the package and the activity of the app in front. Both are "" when no app of a
// person is in front: the launcher, the lock screen, or the system.
func (c *Controller) Foreground(ctx context.Context) (pkg, activity string, err error) {
	a, err := c.adb()
	if err != nil {
		return "", "", err
	}
	var last error
	for _, dump := range [][]string{{"dumpsys", "activity", "activities"}, {"dumpsys", "window"}} {
		out, err := a.shell(ctx, dump...)
		if err != nil {
			last = err
			continue
		}
		if p, act, ok := ParseForeground(out); ok {
			return p, act, nil
		}
		last = nil
	}
	if last != nil {
		return "", "", fmt.Errorf("the app in front could not be read: %w", last)
	}
	return "", "", nil
}

// LaunchOptions say how LaunchApp starts an app.
type LaunchOptions struct {
	Package string
	// Locales are BCP-47 tags, such as en-US, and they set the language of the app. They need Android 13.
	Locales []string
	// Arguments are key=value strings that become string extras of the intent.
	Arguments []string
}

// The limits of what is sent to am and to the locale service.
const (
	maxLocales       = 10
	maxArguments     = 32
	maxArgumentValue = 1 << 10
)

var (
	localePattern   = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,4}$`)
	argumentKeyExpr = regexp.MustCompile(`^[A-Za-z0-9_.]{1,128}$`)
)

// ValidLocale reports whether s is a BCP-47 language tag of the simple kind that Android takes.
func ValidLocale(s string) bool { return len(s) <= 35 && localePattern.MatchString(s) }

// ParseLaunchArgument splits key=value, and checks both: the key is made of letters, digits, dots and
// underscores, and the value is at most 1 KiB with no NUL.
func ParseLaunchArgument(s string) (key, value string, err error) {
	key, value, ok := strings.Cut(s, "=")
	switch {
	case !ok:
		return "", "", errors.New("an argument is key=value")
	case !argumentKeyExpr.MatchString(key):
		return "", "", fmt.Errorf("the argument key %q is not letters, digits, dots and underscores", truncate(key, 40))
	case len(value) > maxArgumentValue:
		return "", "", fmt.Errorf("the value of the argument %s is longer than 1 KiB", key)
	case strings.ContainsRune(value, 0):
		return "", "", fmt.Errorf("the value of the argument %s holds a NUL", key)
	}
	return key, value, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
}

// LaunchApp starts an app, and stops with an error when am says it could not. It never clears the task,
// so an app that runs keeps its screen.
func (c *Controller) LaunchApp(ctx context.Context, o LaunchOptions) error {
	if !config.ValidAndroidPackage(o.Package) {
		return fmt.Errorf("%q is not a package name", truncate(o.Package, 80))
	}
	if len(o.Locales) > maxLocales {
		return fmt.Errorf("at most %d locales", maxLocales)
	}
	for _, l := range o.Locales {
		if !ValidLocale(l) {
			return fmt.Errorf("%q is not a BCP-47 locale such as en-US", truncate(l, 40))
		}
	}
	if len(o.Arguments) > maxArguments {
		return fmt.Errorf("at most %d arguments", maxArguments)
	}
	extras := make([]string, 0, 3*len(o.Arguments))
	for _, arg := range o.Arguments {
		k, v, err := ParseLaunchArgument(arg)
		if err != nil {
			return err
		}
		extras = append(extras, "--es", k, v)
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	if len(o.Locales) > 0 {
		if api, err := c.apiLevel(ctx, a); err != nil {
			return err
		} else if api < 33 {
			return unsupportedf("the language of an app needs Android 13 (API 33), and the device has API %d", api)
		}
		out, err := a.shell(ctx, "cmd", "locale", "set-app-locales", o.Package, "--user", "current", "--locales", strings.Join(o.Locales, ","))
		if err != nil || containsFailure(out) {
			if err == nil {
				err = errors.New(firstLine(out))
			}
			return fmt.Errorf("the language of the app could not be set: %w", err)
		}
	}
	comp, err := (&adbLife{a: a, app: App{Package: o.Package}}).launcher(ctx)
	if err != nil {
		return err
	}
	c.InvalidateInputMethod()
	words := append([]string{"am", "start", "-W", "-n", comp}, extras...)
	out, err := a.shell(ctx, words...)
	return startResult("the app could not be started", out, err)
}

// apiLevel is the API level of the device.
func (c *Controller) apiLevel(ctx context.Context, a *adb) (int, error) {
	out, err := a.shell(ctx, "getprop", "ro.build.version.sdk")
	if err != nil {
		return 0, fmt.Errorf("the Android version could not be read: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(firstLine(out)))
	if err != nil {
		return 0, errors.New("the Android version is not a number")
	}
	return n, nil
}

// serverPackages are the packages of the UiAutomator2 server, which Semar needs to talk to the device.
func isServerPackage(pkg string) bool {
	return pkg == serverPackage || strings.HasPrefix(pkg, serverPackage+".")
}

// appPackage checks a package that a destructive call acts on. The packages of the server are refused:
// without them nothing on the device can be read.
func appPackage(pkg string) error {
	switch {
	case !config.ValidAndroidPackage(pkg):
		return fmt.Errorf("%q is not a package name", truncate(pkg, 80))
	case isServerPackage(pkg):
		return fmt.Errorf("%s is the server that Semar reads the device with: it is not touched", pkg)
	}
	return nil
}

// TerminateApp stops an app.
func (c *Controller) TerminateApp(ctx context.Context, pkg string) error {
	if err := appPackage(pkg); err != nil {
		return err
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	if _, err := a.shell(ctx, "am", "force-stop", pkg); err != nil {
		return fmt.Errorf("the app could not be stopped: %w", err)
	}
	return nil
}

// InstallAPK installs a file on the device with adb install -r -t. It replaces the app of the same
// package and never uninstalls it: that would delete its data. A signing key that differs is an error
// with a way out. The path must be an absolute path to a regular .apk file, and a caller that takes the
// path from a person checks it first against its own roots.
func (c *Controller) InstallAPK(ctx context.Context, path string) error {
	a, err := c.adb()
	if err != nil {
		return err
	}
	file, err := c.p.resolvePath(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(file)
	switch {
	case err != nil:
		return fmt.Errorf("the file %s cannot be read: %w", filepath.Base(file), errors.Unwrap(err))
	case !info.Mode().IsRegular():
		return fmt.Errorf("the file %s is not a regular file", filepath.Base(file))
	case !strings.EqualFold(filepath.Ext(file), ".apk"):
		return fmt.Errorf("the file %s is not an .apk", filepath.Base(file))
	case info.Size() > maxAPK:
		return fmt.Errorf("the file %s is larger than 4 GiB", filepath.Base(file))
	}
	if err := checkZip(file); err != nil {
		return err
	}
	out, err := a.out(ctx, "install", "-r", "-t", file)
	text := out
	if err != nil {
		text += "\n" + err.Error()
	}
	switch {
	case err == nil && strings.Contains(out, "Success"):
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case isClash(text):
		return errors.New("the app on the device is signed with another key than the APK. Semar never uninstalls an app on its own, because that deletes its data: uninstall it first if you accept that")
	}
	return fmt.Errorf("the APK could not be installed: %s", installReason(text))
}

// checkZip reads the first bytes of a file for the signature of a zip, which an APK is.
func checkZip(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("the file %s cannot be read: %w", filepath.Base(file), errors.Unwrap(err))
	}
	defer f.Close()
	var head [4]byte
	if _, err := f.Read(head[:]); err != nil || string(head[:]) != "PK\x03\x04" {
		return fmt.Errorf("the file %s is not an APK", filepath.Base(file))
	}
	return nil
}

// UninstallApp removes an app with its data. The server of UiAutomator2 is refused.
func (c *Controller) UninstallApp(ctx context.Context, pkg string) error {
	if err := appPackage(pkg); err != nil {
		return err
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	out, err := a.out(ctx, "uninstall", pkg)
	if err != nil || !strings.Contains(out, "Success") {
		if err == nil {
			err = errors.New(firstLine(out))
		}
		return fmt.Errorf("the app could not be uninstalled: %w", err)
	}
	return nil
}

// ClearAppData deletes the data of an app, as a fresh install has it.
func (c *Controller) ClearAppData(ctx context.Context, pkg string) error {
	if err := appPackage(pkg); err != nil {
		return err
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	out, err := a.shell(ctx, "pm", "clear", pkg)
	if err != nil || !strings.Contains(out, "Success") {
		if err == nil {
			err = errors.New(firstLine(out))
		}
		return fmt.Errorf("the data of the app could not be cleared: %w", err)
	}
	return nil
}
