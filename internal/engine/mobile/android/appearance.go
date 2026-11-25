package android

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile"
)

const (
	ThemeLight   = "light"
	ThemeDark    = "dark"
	MinFontScale = 0.5
	MaxFontScale = 3.0
)

type savedFontScale struct {
	value   string
	wasNull bool
}

type savedAppLocale struct {
	pkg     string
	locales string // empty means omit --locales (system default)
}

func normalizeTheme(theme string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(theme))
	if t != ThemeLight && t != ThemeDark {
		return "", fmt.Errorf("the theme %q is not %s or %s", truncate(theme, 40), ThemeLight, ThemeDark)
	}
	return t, nil
}

func checkFontScale(scale float64) error {
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale < MinFontScale || scale > MaxFontScale {
		return fmt.Errorf("font scale must be from %.1f to %.1f, got %g", MinFontScale, MaxFontScale, scale)
	}
	return nil
}

func formatFontScale(scale float64) string {
	s := strconv.FormatFloat(scale, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

func isShellUnsupported(out string, err error) bool {
	combined := strings.ToLower(out)
	if err != nil {
		combined += "\n" + strings.ToLower(err.Error())
	}
	for _, marker := range []string{"unknown command", "can't find service", "inaccessible or not found", "no such command"} {
		if strings.Contains(combined, marker) {
			return true
		}
	}
	return false
}

// ParseNightMode parses the output of `cmd uimode night`, such as "Night mode: yes".
// raw is the argument suitable for restoring via `cmd uimode night <raw>` (yes, no, auto, etc.).
// theme is "dark", "light", or "auto".
func ParseNightMode(out string) (raw, theme string, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		idx := strings.Index(lower, "night mode:")
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+len("night mode:"):])
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		val := strings.ToLower(fields[0])
		switch val {
		case "yes":
			return "yes", ThemeDark, true
		case "no":
			return "no", ThemeLight, true
		case "auto":
			return "auto", "auto", true
		case "custom_schedule", "custom_bedtime":
			return val, "auto", true
		}
	}
	return "", "", false
}

func readTheme(ctx context.Context, a *adb) (raw, theme string, err error) {
	out, err := a.shell(ctx, "cmd", "uimode", "night")
	if isShellUnsupported(out, err) {
		return "", "", unsupportedf("dark mode toggle needs Android 10 (API 29) or later")
	}
	if err != nil {
		return "", "", fmt.Errorf("the theme could not be read: %w", err)
	}
	raw, theme, ok := ParseNightMode(out)
	if !ok {
		return "", "", fmt.Errorf("the theme could not be read: %s", firstLine(out))
	}
	return raw, theme, nil
}

func applyTheme(ctx context.Context, a *adb, theme string) error {
	arg := "no"
	if theme == ThemeDark {
		arg = "yes"
	}
	out, err := a.shell(ctx, "cmd", "uimode", "night", arg)
	if isShellUnsupported(out, err) {
		return unsupportedf("dark mode toggle needs Android 10 (API 29) or later")
	}
	if err != nil || containsFailure(out) {
		if err == nil {
			err = errors.New(firstLine(out))
		}
		return fmt.Errorf("the theme could not be set: %w", err)
	}
	return nil
}

func restoreTheme(ctx context.Context, a *adb, raw string) error {
	if raw == "" {
		return nil
	}
	out, err := a.shell(ctx, "cmd", "uimode", "night", raw)
	if err != nil || containsFailure(out) {
		if err == nil {
			err = errors.New(firstLine(out))
		}
		return fmt.Errorf("the theme could not be put back: %w", err)
	}
	return nil
}

func readFontScale(ctx context.Context, a *adb) (savedFontScale, float64, error) {
	out, err := a.shell(ctx, "settings", "get", "system", "font_scale")
	if err != nil {
		return savedFontScale{}, 0, fmt.Errorf("the font scale could not be read: %w", err)
	}
	v := strings.TrimSpace(firstLine(out))
	if v == "" || strings.EqualFold(v, "null") {
		return savedFontScale{wasNull: true}, 1.0, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return savedFontScale{}, 0, fmt.Errorf("the font scale %q is not a number", truncate(v, 40))
	}
	return savedFontScale{value: v}, f, nil
}

func applyFontScale(ctx context.Context, a *adb, scale float64) error {
	out, err := a.shell(ctx, "settings", "put", "system", "font_scale", formatFontScale(scale))
	if err != nil || containsFailure(out) {
		if err == nil {
			err = errors.New(firstLine(out))
		}
		return fmt.Errorf("the font scale could not be set: %w", err)
	}
	return nil
}

func restoreFontScale(ctx context.Context, a *adb, saved savedFontScale) error {
	if saved.wasNull {
		// Write 1.0 first so running apps receive a live configuration change, then delete the override.
		_, err1 := a.shell(ctx, "settings", "put", "system", "font_scale", "1.0")
		_, err2 := a.shell(ctx, "settings", "delete", "system", "font_scale")
		if err := errors.Join(err1, err2); err != nil {
			return fmt.Errorf("the font scale could not be put back: %w", err)
		}
		return nil
	}
	if saved.value == "" {
		return nil
	}
	out, err := a.shell(ctx, "settings", "put", "system", "font_scale", saved.value)
	if err != nil || containsFailure(out) {
		if err == nil {
			err = errors.New(firstLine(out))
		}
		return fmt.Errorf("the font scale could not be put back: %w", err)
	}
	return nil
}

func apiLevelOf(ctx context.Context, a *adb) (int, error) {
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

// ParseAppLocales reads the output of `cmd locale get-app-locales <pkg>`, such as
// "Locales for com.example.shop for user 0 are [en-US,fr-FR]" or "... are []".
func ParseAppLocales(out string) string {
	l := strings.IndexByte(out, '[')
	r := strings.LastIndexByte(out, ']')
	if l < 0 || r <= l {
		return ""
	}
	inner := strings.TrimSpace(out[l+1 : r])
	if inner == "" {
		return ""
	}
	parts := strings.Split(inner, ",")
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); ValidLocale(p) {
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, ",")
}

func readAppLocale(ctx context.Context, a *adb, pkg string) (savedAppLocale, error) {
	if !config.ValidAndroidPackage(pkg) {
		return savedAppLocale{}, fmt.Errorf("%q is not a package name", truncate(pkg, 80))
	}
	api, err := apiLevelOf(ctx, a)
	if err != nil {
		return savedAppLocale{}, err
	}
	if api < 33 {
		return savedAppLocale{}, unsupportedf("the language of an app needs Android 13 (API 33), and the device has API %d", api)
	}
	curOut, err := a.shell(ctx, "cmd", "locale", "get-app-locales", pkg)
	if isShellUnsupported(curOut, err) {
		return savedAppLocale{}, unsupportedf("the language of an app needs Android 13 (API 33)")
	}
	prev := ""
	if err == nil {
		prev = ParseAppLocales(curOut)
	}
	return savedAppLocale{pkg: pkg, locales: prev}, nil
}

func setAppLocale(ctx context.Context, a *adb, pkg, locale string) error {
	out, err := a.shell(ctx, "cmd", "locale", "set-app-locales", pkg, "--user", "current", "--locales", locale)
	if isShellUnsupported(out, err) {
		return unsupportedf("the language of an app needs Android 13 (API 33)")
	}
	if err != nil || containsFailure(out) {
		if err == nil {
			err = errors.New(firstLine(out))
		}
		return fmt.Errorf("the language of the app could not be set: %w", err)
	}
	return nil
}

func restoreAppLocale(ctx context.Context, a *adb, saved savedAppLocale) error {
	args := []string{"cmd", "locale", "set-app-locales", saved.pkg, "--user", "current"}
	if saved.locales != "" {
		args = append(args, "--locales", saved.locales)
	}
	out, err := a.shell(ctx, args...)
	if err != nil || containsFailure(out) {
		if err == nil {
			err = errors.New(firstLine(out))
		}
		return fmt.Errorf("the language of %s could not be put back: %w", saved.pkg, err)
	}
	return nil
}

// Theme reads the system dark/light theme: "dark", "light", or "auto".
func (c *Controller) Theme(ctx context.Context) (string, error) {
	a, err := c.adb()
	if err != nil {
		return "", err
	}
	_, theme, err := readTheme(ctx, a)
	return theme, err
}

// SetTheme switches the system dark/light theme to light or dark. The first call saves the prior
// setting, and Platform.Close puts it back.
func (c *Controller) SetTheme(ctx context.Context, theme string) error {
	norm, err := normalizeTheme(theme)
	if err != nil {
		return err
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	firstSave := c.theme == nil
	if firstSave {
		raw, _, err := readTheme(ctx, a)
		if err != nil {
			return err
		}
		c.theme = &raw
	}
	if err := applyTheme(ctx, a, norm); err != nil {
		if firstSave && errors.Is(err, ErrUnsupported) {
			c.theme = nil
		}
		return err
	}
	return nil
}

// FontScale reads the system font_scale setting (1.0 when unset).
func (c *Controller) FontScale(ctx context.Context) (float64, error) {
	a, err := c.adb()
	if err != nil {
		return 0, err
	}
	_, scale, err := readFontScale(ctx, a)
	return scale, err
}

// SetFontScale sets the system font_scale setting (0.5 to 3.0). The first call saves the prior
// setting, and Platform.Close puts it back.
func (c *Controller) SetFontScale(ctx context.Context, scale float64) error {
	if err := checkFontScale(scale); err != nil {
		return err
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	firstSave := c.fontScale == nil
	if firstSave {
		prev, _, err := readFontScale(ctx, a)
		if err != nil {
			return err
		}
		c.fontScale = &prev
	}
	if err := applyFontScale(ctx, a, scale); err != nil {
		if firstSave && errors.Is(err, ErrUnsupported) {
			c.fontScale = nil
		}
		return err
	}
	return nil
}

// applyDisplaySettings applies run-level theme and fontScale overrides on a local device. Must be
// called with p.mu held after startDevice.
func (p *Platform) applyDisplaySettings(ctx context.Context) *mobile.Unavailable {
	if p.o.Env.Theme != "" && !p.themeApplied {
		norm, err := normalizeTheme(p.o.Env.Theme)
		if err != nil {
			return &mobile.Unavailable{Reason: err.Error()}
		}
		firstSave := p.savedTheme == nil
		if firstSave {
			raw, _, err := readTheme(ctx, p.adb)
			if err != nil {
				return &mobile.Unavailable{Reason: err.Error()}
			}
			p.savedTheme = &raw
		}
		if err := applyTheme(ctx, p.adb, norm); err != nil {
			if firstSave && errors.Is(err, ErrUnsupported) {
				p.savedTheme = nil
			}
			return &mobile.Unavailable{Reason: err.Error()}
		}
		p.themeApplied = true
	}
	if p.o.Env.FontScale != 0 && !p.fontScaleApplied {
		if err := checkFontScale(p.o.Env.FontScale); err != nil {
			return &mobile.Unavailable{Reason: err.Error()}
		}
		firstSave := p.savedFontScale == nil
		if firstSave {
			prev, _, err := readFontScale(ctx, p.adb)
			if err != nil {
				return &mobile.Unavailable{Reason: err.Error()}
			}
			p.savedFontScale = &prev
		}
		if err := applyFontScale(ctx, p.adb, p.o.Env.FontScale); err != nil {
			if firstSave && errors.Is(err, ErrUnsupported) {
				p.savedFontScale = nil
			}
			return &mobile.Unavailable{Reason: err.Error()}
		}
		p.fontScaleApplied = true
	}
	return nil
}

// applyLocaleForApp applies a run-level per-app locale override after the app is prepared. Must be
// called with p.mu held.
func (p *Platform) applyLocaleForApp(ctx context.Context, pkg string) *mobile.Unavailable {
	if p.o.Env.Locale == "" {
		return nil
	}
	for _, s := range p.savedLocales {
		if s.pkg == pkg {
			return nil
		}
	}
	if !ValidLocale(p.o.Env.Locale) {
		return &mobile.Unavailable{Reason: fmt.Sprintf("%q is not a BCP-47 locale such as en-US", truncate(p.o.Env.Locale, 40))}
	}
	saved, err := readAppLocale(ctx, p.adb, pkg)
	if err != nil {
		return &mobile.Unavailable{Reason: err.Error()}
	}
	p.savedLocales = append(p.savedLocales, saved)
	if err := setAppLocale(ctx, p.adb, pkg, p.o.Env.Locale); err != nil {
		if errors.Is(err, ErrUnsupported) {
			p.savedLocales = p.savedLocales[:len(p.savedLocales)-1]
		}
		return &mobile.Unavailable{Reason: err.Error()}
	}
	return nil
}

// undoDisplay restores any run-level display/locale overrides made by Platform. Must be called with
// p.mu held.
func (p *Platform) undoDisplay(ctx context.Context) []error {
	p.themeApplied, p.fontScaleApplied = false, false
	if p.adb == nil {
		p.savedTheme, p.savedFontScale, p.savedLocales = nil, nil, nil
		return nil
	}
	step := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 3*time.Second) }
	var errs []error
	for i := len(p.savedLocales) - 1; i >= 0; i-- {
		sctx, cancel := step()
		if err := restoreAppLocale(sctx, p.adb, p.savedLocales[i]); err != nil {
			errs = append(errs, err)
		}
		cancel()
	}
	p.savedLocales = nil
	if p.savedFontScale != nil {
		sctx, cancel := step()
		if err := restoreFontScale(sctx, p.adb, *p.savedFontScale); err != nil {
			errs = append(errs, err)
		}
		cancel()
		p.savedFontScale = nil
	}
	if p.savedTheme != nil {
		sctx, cancel := step()
		if err := restoreTheme(sctx, p.adb, *p.savedTheme); err != nil {
			errs = append(errs, err)
		}
		cancel()
		p.savedTheme = nil
	}
	return errs
}
