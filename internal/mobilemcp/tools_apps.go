package mobilemcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
)

var pkgSchema = mcp.String("The package name of the app, such as com.example.shop.")

func (m *Manager) appTools() []*toolDef {
	return []*toolDef{
		{
			name: "mobile_list_apps", title: "List apps", readOnly: true,
			desc: "List the packages installed on the device. By default only apps that a person installed.",
			schema: mcp.Object(map[string]mcp.Schema{
				"includeSystem": mcp.Bool("Also list system packages. Off by default."),
			}),
			run: m.listApps,
		},
		{
			name: "mobile_get_foreground_app", title: "Get the app in front", readOnly: true,
			desc:   "Get the package and activity of the app in front. Both are empty at the launcher, the lock screen, or a system screen.",
			schema: mcp.Object(nil),
			run:    m.foregroundApp,
		},
		{
			name: "mobile_launch_app", title: "Launch an app",
			desc: "Start an app by its package name. locale sets the language of the app (Android 13 or later). arguments are key=value strings that become string extras of the launch intent.",
			schema: mcp.Object(map[string]mcp.Schema{
				"packageName": pkgSchema,
				"locale":      mcp.String("BCP-47 tags separated by commas, such as en-US or fr-FR,de-DE."),
				"arguments":   mcp.Array("Launch arguments as key=value strings.", mcp.String("One key=value pair.")),
			}).Required("packageName"),
			run: m.launchApp,
		},
		{
			name: "mobile_terminate_app", title: "Stop an app",
			desc: "Force-stop an app by its package name.",
			schema: mcp.Object(map[string]mcp.Schema{
				"packageName": pkgSchema,
			}).Required("packageName"),
			run: m.terminateApp,
		},
		{
			name: "mobile_install_app", title: "Install an app", destructive: true,
			desc: "Install an .apk file, replacing the app of the same package and keeping its data. The path is absolute or relative to the repository. The file must be a regular .apk of at most 4 GiB.",
			schema: mcp.Object(map[string]mcp.Schema{
				"path": mcp.String("Path to the .apk file."),
			}).Required("path"),
			run: m.installApp,
		},
		{
			name: "mobile_uninstall_app", title: "Uninstall an app", destructive: true,
			desc: "Uninstall an app and delete its data. On a physical device it needs confirm: true.",
			schema: mcp.Object(map[string]mcp.Schema{
				"packageName": pkgSchema,
				"confirm":     mcp.Bool("Set to true to allow this on a physical device."),
			}).Required("packageName"),
			run: m.uninstallApp,
		},
		{
			name: "mobile_clear_app_data", title: "Clear app data", destructive: true,
			desc: "Delete all data of an app, as a fresh install would have it. On a physical device it needs confirm: true.",
			schema: mcp.Object(map[string]mcp.Schema{
				"packageName": pkgSchema,
				"confirm":     mcp.Bool("Set to true to allow this on a physical device."),
			}).Required("packageName"),
			run: m.clearAppData,
		},
	}
}

func checkPackage(p string) (string, error) {
	p = strings.TrimSpace(p)
	if !config.ValidAndroidPackage(p) {
		return "", fmt.Errorf("%q is not a package name such as com.example.shop", truncateText(p, 80))
	}
	return p, nil
}

func (m *Manager) listApps(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		IncludeSystem bool `json:"includeSystem"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	apps, err := dev.ListApps(ctx, a.IncludeSystem)
	if err != nil {
		return mcp.Result{}, err
	}
	if apps == nil {
		apps = []string{}
	}
	return textJSON(map[string]any{"apps": apps})
}

func (m *Manager) foregroundApp(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	if err := noArgs(call); err != nil {
		return mcp.Result{}, err
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	pkg, act, err := dev.Foreground(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{"package": pkg, "activity": act})
}

func (m *Manager) launchApp(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		PackageName string   `json:"packageName"`
		Locale      string   `json:"locale"`
		Arguments   []string `json:"arguments"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	pkg, err := checkPackage(a.PackageName)
	if err != nil {
		return mcp.Result{}, err
	}
	o := android.LaunchOptions{Package: pkg, Arguments: a.Arguments}
	for _, l := range strings.Split(a.Locale, ",") {
		if l = strings.TrimSpace(l); l != "" {
			o.Locales = append(o.Locales, l)
		}
	}
	for _, l := range o.Locales {
		if !android.ValidLocale(l) {
			return mcp.Result{}, fmt.Errorf("%q is not a locale such as en-US", truncateText(l, 40))
		}
	}
	for _, arg := range o.Arguments {
		if _, _, err := android.ParseLaunchArgument(arg); err != nil {
			return mcp.Result{}, err
		}
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	m.baselineHealth(ctx, dev)
	if err := dev.LaunchApp(ctx, o); err != nil {
		return mcp.Result{}, err
	}
	m.trackLaunch(pkg)
	// A Flutter app builds its semantics late, and a debug build draws its first frame slowly.
	if waited := m.warmAfterLaunch(ctx, dev); waited > 0 {
		return textJSON(map[string]any{"launched": pkg, "waitedMs": waited.Milliseconds(), "note": "waited for the Flutter semantics tree to be built"})
	}
	return textJSON(map[string]any{"launched": pkg})
}

func (m *Manager) terminateApp(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		PackageName string `json:"packageName"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	pkg, err := checkPackage(a.PackageName)
	if err != nil {
		return mcp.Result{}, err
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if err := dev.TerminateApp(ctx, pkg); err != nil {
		return mcp.Result{}, err
	}
	m.trackExit(pkg, false)
	return textJSON(map[string]any{"terminated": pkg})
}

func (m *Manager) installApp(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	path, err := mcpkit.ReadAPK(a.Path, m.repoRoot)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("the app cannot be installed: %w", err)
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if err := dev.InstallAPK(ctx, path); err != nil {
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{"installed": path})
}

type destructiveArgs struct {
	PackageName string `json:"packageName"`
	Confirm     bool   `json:"confirm"`
}

func (m *Manager) destructive(ctx context.Context, call mcp.Call, what string, do func(Device, string) error) (mcp.Result, error) {
	var a destructiveArgs
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	pkg, err := checkPackage(a.PackageName)
	if err != nil {
		return mcp.Result{}, err
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if err := needsConfirm(dev, what+" "+pkg, a.Confirm); err != nil {
		return mcp.Result{}, err
	}
	if err := do(dev, pkg); err != nil {
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{"done": what, "package": pkg})
}

func (m *Manager) uninstallApp(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	return m.destructive(ctx, call, "uninstalling", func(d Device, pkg string) error {
		if err := d.UninstallApp(ctx, pkg); err != nil {
			return err
		}
		m.trackExit(pkg, true)
		return nil
	})
}

func (m *Manager) clearAppData(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	return m.destructive(ctx, call, "clearing the data of", func(d Device, pkg string) error {
		if err := d.ClearAppData(ctx, pkg); err != nil {
			return err
		}
		m.trackExit(pkg, false)
		return nil
	})
}
