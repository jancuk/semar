package android_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

func TestParseNightModeAndAppLocales(t *testing.T) {
	for in, want := range map[string][2]string{
		"Night mode: yes\n":             {"yes", "dark"},
		"Night mode: no\n":              {"no", "light"},
		"Night mode: auto\n":            {"auto", "auto"},
		"Night mode: custom_schedule\n": {"custom_schedule", "auto"},
	} {
		raw, theme, ok := android.ParseNightMode(in)
		if !ok || raw != want[0] || theme != want[1] {
			t.Errorf("ParseNightMode(%q) = %q %q %v, want %v", in, raw, theme, ok, want)
		}
	}
	if _, _, ok := android.ParseNightMode("Unknown command"); ok {
		t.Error("ParseNightMode accepted Unknown command")
	}

	for in, want := range map[string]string{
		"Locales for com.example.shop for user 0 are [en-US, fr-FR]\n": "en-US,fr-FR",
		"Locales for com.example.shop for user 0 are []\n":             "",
		"no brackets": "",
	} {
		if got := android.ParseAppLocales(in); got != want {
			t.Errorf("ParseAppLocales(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestControllerThemeAndFontScaleRestoreOnClose(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'cmd' 'uimode' 'night' 'yes'", Out: ""},
			{Match: "'cmd' 'uimode' 'night' 'no'", Out: ""},
			{Match: "'cmd' 'uimode' 'night'", Out: "Night mode: no\n"},
			{Match: "'settings' 'get' 'system' 'font_scale'", Out: "1.15\n"},
		}
	})
	p, c := r.start(t)
	ctx := context.Background()

	if got, err := c.Theme(ctx); err != nil || got != "light" {
		t.Fatalf("Theme = %q, %v", got, err)
	}
	if err := c.SetTheme(ctx, "dark"); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "cmd", "uimode", "night", "yes") {
		t.Errorf("no uimode night yes: %v", r.adb.Shell())
	}
	// Second set does not re-read prior theme.
	if err := c.SetTheme(ctx, "light"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetTheme(ctx, "sepia"); err == nil {
		t.Error("invalid theme accepted")
	}

	if got, err := c.FontScale(ctx); err != nil || got != 1.15 {
		t.Fatalf("FontScale = %g, %v", got, err)
	}
	if err := c.SetFontScale(ctx, 1.5); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "settings", "put", "system", "font_scale", "1.5") {
		t.Errorf("no font_scale put: %v", r.adb.Shell())
	}
	if err := c.SetFontScale(ctx, 0.2); err == nil {
		t.Error("too small font scale accepted")
	}
	if err := c.SetFontScale(ctx, 4.0); err == nil {
		t.Error("too large font scale accepted")
	}

	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "cmd", "uimode", "night", "no") {
		t.Errorf("theme was not restored: %v", r.adb.Shell())
	}
	if !shellHas(r, "settings", "put", "system", "font_scale", "1.15") {
		t.Errorf("font_scale was not restored: %v", r.adb.Shell())
	}
	r.adb.AssertQuoted(t)
}

func TestControllerFontScaleNullRestoreWritesOnePointZeroThenDeletes(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'settings' 'get' 'system' 'font_scale'", Out: "null\n"},
		}
	})
	p, c := r.start(t)
	ctx := context.Background()

	if got, err := c.FontScale(ctx); err != nil || got != 1.0 {
		t.Fatalf("FontScale(null) = %g, %v", got, err)
	}
	if err := c.SetFontScale(ctx, 2.0); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	words := r.adb.ShellWords()
	putIdx, delIdx := -1, -1
	for i, w := range words {
		if slices.Equal(w, []string{"settings", "put", "system", "font_scale", "1.0"}) {
			putIdx = i
		}
		if slices.Equal(w, []string{"settings", "delete", "system", "font_scale"}) {
			delIdx = i
		}
	}
	if putIdx < 0 || delIdx < 0 || putIdx >= delIdx {
		t.Errorf("expected put 1.0 before delete, got %v", words)
	}
	r.adb.AssertQuoted(t)
}

func TestControllerThemeUnsupportedOnOldAndroidAndRemote(t *testing.T) {
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'cmd' 'uimode' 'night'", RC: 1, Err: "Unknown command: night"},
		}
	})
	_, c := r.start(t)
	if _, err := c.Theme(context.Background()); !errors.Is(err, android.ErrUnsupported) {
		t.Errorf("Theme err = %v, want ErrUnsupported", err)
	}
	if err := c.SetTheme(context.Background(), "dark"); !errors.Is(err, android.ErrUnsupported) {
		t.Errorf("SetTheme err = %v, want ErrUnsupported", err)
	}

	_, rc, _, _ := newRemoteCtl(t)
	ctx := context.Background()
	for name, err := range map[string]error{
		"Theme":        func() error { _, e := rc.Theme(ctx); return e }(),
		"SetTheme":     rc.SetTheme(ctx, "dark"),
		"FontScale":    func() error { _, e := rc.FontScale(ctx); return e }(),
		"SetFontScale": rc.SetFontScale(ctx, 1.5),
	} {
		if !errors.Is(err, android.ErrUnsupported) {
			t.Errorf("remote %s = %v, want ErrUnsupported", name, err)
		}
	}
}

func TestPlatformRunLevelDisplayAndLocaleApplyAndRestore(t *testing.T) {
	r := newRig(t, func(r *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'cmd' 'uimode' 'night' 'yes'", Out: ""},
			{Match: "'cmd' 'uimode' 'night' 'no'", Out: ""},
			{Match: "'cmd' 'uimode' 'night'", Out: "Night mode: no\n"},
			{Match: "'settings' 'get' 'system' 'font_scale'", Out: "1.0\n"},
			{Match: "'cmd' 'locale' 'get-app-locales' 'com.example.shop'", Out: "Locales for com.example.shop for user 0 are []\n"},
			{Match: "'wm' 'density'", Out: "Physical density: 420\n"},
		}
	})
	r.opts.Env.Theme = "dark"
	r.opts.Env.FontScale = 1.5
	r.opts.Env.Locale = "id-ID"

	p := r.platform()
	ctx := context.Background()
	r.driver(p)

	// Density is cached across calls.
	var dr mobile.DensityReader = p
	if got := dr.ScreenDensity(ctx); got != 420 {
		t.Errorf("ScreenDensity = %d, want 420", got)
	}
	if got := dr.ScreenDensity(ctx); got != 420 {
		t.Errorf("cached ScreenDensity = %d, want 420", got)
	}
	wmCount := 0
	for _, w := range r.adb.ShellWords() {
		if slices.Equal(w, []string{"wm", "density"}) {
			wmCount++
		}
	}
	if wmCount != 1 {
		t.Errorf("wm density called %d times, want 1", wmCount)
	}

	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}

	words := r.adb.ShellWords()
	has := func(want ...string) bool {
		for _, w := range words {
			if slices.Equal(w, want) {
				return true
			}
		}
		return false
	}
	for _, want := range [][]string{
		{"cmd", "uimode", "night", "yes"},
		{"settings", "put", "system", "font_scale", "1.5"},
		{"cmd", "locale", "set-app-locales", pkgShop, "--user", "current", "--locales", "id-ID"},
		{"cmd", "locale", "set-app-locales", pkgShop, "--user", "current"},
		{"settings", "put", "system", "font_scale", "1.0"},
		{"cmd", "uimode", "night", "no"},
	} {
		if !has(want...) {
			t.Errorf("missing adb shell %v in %v", want, words)
		}
	}
	r.adb.AssertQuoted(t)
}

func TestPlatformPartialStartupFailureStillRestoresSavedSettings(t *testing.T) {
	r := newRig(t, func(r *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'cmd' 'uimode' 'night' 'yes'", Out: ""},
			{Match: "'cmd' 'uimode' 'night' 'no'", Out: ""},
			{Match: "'cmd' 'uimode' 'night'", Out: "Night mode: no\n"},
			{Match: "'settings' 'get' 'system' 'font_scale'", RC: 1, Err: "permission denied"},
		}
	})
	r.opts.Env.Theme = "dark"
	r.opts.Env.FontScale = 1.5

	p := r.platform()
	_, err := p.Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "font scale") {
		t.Errorf("reason = %q", un.Reason)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range r.adb.ShellWords() {
		if slices.Equal(w, []string{"cmd", "uimode", "night", "no"}) {
			found = true
		}
	}
	if !found {
		t.Errorf("theme was not restored after partial failure: %v", r.adb.Shell())
	}
	r.adb.AssertQuoted(t)
}

func TestPlatformRemoteRejectsDisplayOverrides(t *testing.T) {
	srv := mobiletest.NewServer(t, mobiletest.Options{NoExecute: true})
	p := android.New(android.Options{
		Env:      config.Android{ServerURL: srv.URL, Theme: "dark"},
		Apps:     map[string]android.App{"shop": {Package: pkgShop}},
		RepoRoot: t.TempDir(),
	})
	defer p.Close(context.Background())

	_, err := p.Driver(context.Background(), "shop")
	un := unavailable(t, err)
	if !strings.Contains(un.Reason, "serverUrl") {
		t.Errorf("reason = %q", un.Reason)
	}
	if got := p.ScreenDensity(context.Background()); got != 0 {
		t.Errorf("remote ScreenDensity = %d, want 0", got)
	}
}

func TestPlatformScreenDensityRetriesAfterTransientError(t *testing.T) {
	r := newRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'wm' 'density'", RC: 1, Err: "device offline"},
		}
	})
	p := r.platform()
	ctx := context.Background()
	r.driver(p)

	if got := p.ScreenDensity(ctx); got != 0 {
		t.Fatalf("first ScreenDensity = %d, want 0 on transient error", got)
	}
	// Replace transient error with a real reply; next call must retry and cache 420.
	r.adb.Unset("canned.0000.key")
	r.adb.Can(mobiletest.Canned{Match: "'wm' 'density'", Out: "Physical density: 420\n"})
	if got := p.ScreenDensity(ctx); got != 420 {
		t.Fatalf("second ScreenDensity = %d, want 420", got)
	}
	if got := p.ScreenDensity(ctx); got != 420 {
		t.Fatalf("cached ScreenDensity = %d, want 420", got)
	}
	wmCount := 0
	for _, w := range r.adb.ShellWords() {
		if slices.Equal(w, []string{"wm", "density"}) {
			wmCount++
		}
	}
	if wmCount != 2 {
		t.Errorf("wm density called %d times, want 2 (initial failure + cached success)", wmCount)
	}
}

func TestDisplayStateSavedBeforeMutatingCommandFailureRestoresOnClose(t *testing.T) {
	// Controller: put/set fails after get succeeds -> Close still restores prior theme and font scale.
	r := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'cmd' 'uimode' 'night' 'yes'", RC: 1, Err: "timed out"},
			{Match: "'cmd' 'uimode' 'night' 'no'", Out: ""},
			{Match: "'cmd' 'uimode' 'night'", Out: "Night mode: no\n"},
			{Match: "'settings' 'get' 'system' 'font_scale'", Out: "1.2\n"},
			{Match: "'settings' 'put' 'system' 'font_scale' '1.8'", RC: 1, Err: "timed out"},
		}
	})
	p, c := r.start(t)
	ctx := context.Background()

	if err := c.SetTheme(ctx, "dark"); err == nil {
		t.Fatal("SetTheme succeeded unexpectedly")
	}
	if err := c.SetFontScale(ctx, 1.8); err == nil {
		t.Fatal("SetFontScale succeeded unexpectedly")
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !shellHas(r, "cmd", "uimode", "night", "no") {
		t.Errorf("theme was not restored after failed apply: %v", r.adb.Shell())
	}
	if !shellHas(r, "settings", "put", "system", "font_scale", "1.2") {
		t.Errorf("font_scale was not restored after failed apply: %v", r.adb.Shell())
	}

	// Platform run-level locale: generic "package ... not found" is NOT misreported as Android 13 unsupported,
	// and prior locale is still restored on Close.
	r2 := newRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'cmd' 'locale' 'get-app-locales' 'com.example.shop'", Out: "Locales for com.example.shop for user 0 are [fr-FR]\n"},
			{Match: "'cmd' 'locale' 'set-app-locales' 'com.example.shop' '--user' 'current' '--locales' 'id-ID'", RC: 1, Err: "package com.example.shop not found"},
		}
	})
	r2.opts.Env.Locale = "id-ID"
	p2 := r2.platform()
	_, err := p2.Driver(ctx, "shop")
	un := unavailable(t, err)
	if strings.Contains(un.Reason, "Android 13") {
		t.Errorf("package not found misreported as Android 13 unsupported: %q", un.Reason)
	}
	if err := p2.Close(ctx); err != nil {
		t.Fatal(err)
	}
	foundRestore := false
	for _, w := range r2.adb.ShellWords() {
		if slices.Equal(w, []string{"cmd", "locale", "set-app-locales", pkgShop, "--user", "current", "--locales", "fr-FR"}) {
			foundRestore = true
		}
	}
	if !foundRestore {
		t.Errorf("prior locale fr-FR was not restored after failed apply: %v", r2.adb.Shell())
	}
}
