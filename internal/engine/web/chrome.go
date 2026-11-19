// Package web is the one place that drives a browser for a test environment. Every action is admitted
// by the effects policy first, runs in a Chrome that has a profile of Semar's own, and is recorded
// afterwards, whatever its outcome. The engine has no assertion logic: checks read the recorded state.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/idcheck"
)

// ErrChromeNotFound is the error of a run on a machine where no Chrome can be started.
var ErrChromeNotFound = errors.New("Chrome was not found: install Chrome or set web.chrome in semar.yaml")

// chromeError is a Chrome that was not found, with the text for the person who set web.chrome.
type chromeError struct{ msg string }

func (e chromeError) Error() string        { return e.msg }
func (e chromeError) Is(target error) bool { return target == ErrChromeNotFound }

// ChromeNotFoundText is the message to show for an error that says that Chrome was not found, and
// false for any other error. The error may be wrapped.
func ChromeNotFoundText(err error) (string, bool) {
	if !errors.Is(err, ErrChromeNotFound) {
		return "", false
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		if ce, ok := e.(chromeError); ok {
			return ce.msg, true
		}
		if e == ErrChromeNotFound {
			return e.Error(), true
		}
	}
	return ErrChromeNotFound.Error(), true
}

// FindChrome gives the program to start. A configured path is used as it is and never replaced by
// another Chrome, so a person who names one gets that one or an error.
func FindChrome(configured string) (string, error) {
	if configured != "" {
		info, err := os.Stat(configured)
		if err != nil || info.IsDir() {
			return "", chromeError{fmt.Sprintf("Chrome was not found at the path of web.chrome, %s: install Chrome or correct web.chrome in semar.yaml", configured)}
		}
		return configured, nil
	}
	for _, c := range chromeCandidates(runtime.GOOS) {
		if filepath.IsAbs(c) {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				return c, nil
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", ErrChromeNotFound
}

func chromeCandidates(goos string) []string {
	switch goos {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		}
	case "windows":
		var out []string
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if dir := os.Getenv(env); dir != "" {
				out = append(out, filepath.Join(dir, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
		return out
	}
	return []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"}
}

// ProfileDir is the Chrome profile of one account in one environment, below the Semar home. It is
// never the profile of the person's own browser.
func ProfileDir(home, environment, account string) (string, error) {
	if home == "" {
		return "", errors.New("the Semar home is not known, so the browser has no profile directory")
	}
	if err := idcheck.Check("environment name", environment); err != nil {
		return "", err
	}
	if account == "" {
		account = "default"
	}
	if err := idcheck.Check("account name", account); err != nil {
		return "", err
	}
	return filepath.Join(home, "browser", environment, account), nil
}

// allocator makes the context that starts Chrome on a profile directory.
func allocator(chrome, profile string, headed bool) (context.Context, context.CancelFunc) {
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.ExecPath(chrome),
		chromedp.UserDataDir(profile),
		chromedp.WindowSize(1280, 900),
	)
	if headed {
		opts = append(opts, chromedp.VisibleWindow)
	}
	if rootOnLinux(runtime.GOOS, os.Geteuid()) {
		opts = append(opts, chromedp.NoSandbox)
	}
	return chromedp.NewExecAllocator(context.Background(), opts...)
}

// rootOnLinux says whether Chrome must start with no sandbox. Chrome refuses to start as root on
// Linux with its sandbox, and a job of a pipeline in a container runs as root.
func rootOnLinux(goos string, euid int) bool { return goos == "linux" && euid == 0 }

func ensureProfile(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("the browser profile %s could not be made: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return turnOffSavedSignIns(dir)
}

// turnOffSavedSignIns stops Chrome from saving and filling in passwords and addresses on the
// profile. A step types what its script says, and a saved sign-in typed into the same field
// would change what the step sent. Chrome reads the file when it starts.
func turnOffSavedSignIns(dir string) error {
	path := filepath.Join(dir, "Default", "Preferences")
	prefs := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &prefs); err != nil {
			prefs = map[string]any{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("the settings of the browser profile %s could not be read: %w", dir, err)
	}
	prefs["credentials_enable_service"] = false
	setIn(prefs, "profile", "password_manager_enabled", false)
	setIn(prefs, "autofill", "profile_enabled", false)
	setIn(prefs, "autofill", "credit_card_enabled", false)
	raw, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("the settings of the browser profile %s could not be written: %w", dir, err)
	}
	return nil
}

func setIn(m map[string]any, section, key string, v any) {
	inner, ok := m[section].(map[string]any)
	if !ok {
		inner = map[string]any{}
		m[section] = inner
	}
	inner[key] = v
}
