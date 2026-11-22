package android

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/webdriver"
)

// The packages and the program of the UiAutomator2 server of Appium. The server is two APKs: the
// server and the test package that holds the instrumentation.
const (
	serverPackage     = "io.appium.uiautomator2.server"
	serverTestPackage = "io.appium.uiautomator2.server.test"
	instrumentation   = serverTestPackage + "/androidx.test.runner.AndroidJUnitRunner"
	// devicePort is the port that the server listens on, on the device.
	devicePort = 6790

	// ServerAPKName and ServerTestAPKName are the files that Semar looks for in <semar home>/android/
	// when the configuration names none.
	ServerAPKName     = "appium-uiautomator2-server.apk"
	ServerTestAPKName = "appium-uiautomator2-server-test.apk"
)

// maxAPK bounds the file that Semar hashes and installs.
const maxAPK = 4 << 30

// signature clashes: the texts that adb and the package manager give when an installed package has
// another signing key than the file.
var clashTexts = []string{
	"INSTALL_FAILED_UPDATE_INCOMPATIBLE", "INSTALL_FAILED_SHARED_USER_INCOMPATIBLE",
	"INSTALL_PARSE_FAILED_INCONSISTENT_CERTIFICATES", "signatures do not match",
}

func isClash(text string) bool {
	for _, c := range clashTexts {
		if strings.Contains(text, c) {
			return true
		}
	}
	return false
}

// fileSHA256 hashes a regular file. A link, a directory, and a file over 4 GiB are refused.
func fileSHA256(path string) (string, error) {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		return "", fmt.Errorf("the file %s cannot be read: %w", path, errors.Unwrap(err))
	case !info.Mode().IsRegular():
		return "", fmt.Errorf("the file %s is not a regular file", path)
	case info.Size() > maxAPK:
		return "", fmt.Errorf("the file %s is larger than 4 GiB", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("the file %s cannot be read: %w", path, errors.Unwrap(err))
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("the file %s cannot be read: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// packagePaths lists the files of an installed package. A package that is not installed gives none, and
// one that is split into several APKs gives several.
func (a *adb) packagePaths(ctx context.Context, pkg string) ([]string, error) {
	out, err := a.shell(ctx, "pm", "path", pkg)
	var paths []string
	for _, l := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(l), "package:"); ok && p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 && err != nil && !isExit(err) {
		return nil, err
	}
	return paths, nil
}

// isExit is true for an error that says the command ran and gave a status, as pm path does for a package
// that is not there.
func isExit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "exit status")
}

// install puts the APK on the device unless the same file is installed already: a package with one
// file whose sha256 on the device is that of the local file is left as it is. An install that fails for
// a signing key that differs uninstalls the package and tries again only when reinstall is set, which
// is for the packages of the server and never for the app under test, whose data an uninstall would
// delete. It reports whether it installed.
func (p *Platform) install(ctx context.Context, a *adb, pkg, file string, reinstall bool) (bool, *mobile.Unavailable) {
	sum, err := fileSHA256(file)
	if err != nil {
		return false, &mobile.Unavailable{Reason: err.Error()}
	}
	paths, err := a.packagePaths(ctx, pkg)
	if err != nil {
		return false, &mobile.Unavailable{Reason: fmt.Sprintf("the packages of the device could not be read: %v", err)}
	}
	if len(paths) == 1 {
		out, err := a.shell(ctx, "sha256sum", paths[0])
		if f := strings.Fields(out); err == nil && len(f) > 0 && strings.EqualFold(f[0], sum) {
			return false, nil
		}
	}
	for attempt := 0; ; attempt++ {
		out, err := a.out(ctx, "install", "-r", "-t", "-d", file)
		text := out
		if err != nil {
			text += "\n" + err.Error()
		}
		switch {
		case err == nil && strings.Contains(out, "Success"):
			return true, nil
		case ctx.Err() != nil:
			return false, &mobile.Unavailable{Reason: "the run was interrupted while " + pkg + " was installed"}
		case isClash(text) && reinstall && attempt == 0:
			if _, uerr := a.out(ctx, "uninstall", pkg); uerr != nil {
				return false, &mobile.Unavailable{Reason: fmt.Sprintf("%s on the device is signed with another key, and it could not be removed: %v", pkg, uerr)}
			}
		case isClash(text) && reinstall:
			return false, &mobile.Unavailable{Reason: fmt.Sprintf("%s on the device is signed with another key and could not be replaced after uninstall: remove it with adb uninstall %s, then run again", pkg, pkg)}
		case isClash(text):
			return false, &mobile.Unavailable{Reason: fmt.Sprintf("%s on the device is signed with another key than the APK. Semar never uninstalls the app under test, because that deletes its data: uninstall it yourself with adb uninstall %s, then run again", pkg, pkg)}
		default:
			return false, &mobile.Unavailable{Reason: fmt.Sprintf("%s could not be installed: %s", pkg, installReason(text))}
		}
	}
}

// installReason is the line of the output of adb install that says why, which starts with Failure or
// adb:.
func installReason(text string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if strings.Contains(l, "Failure") || strings.HasPrefix(l, "adb:") || strings.Contains(l, "INSTALL_") {
			return firstLine(l)
		}
	}
	return firstLine(text)
}

// serverAPKs gives the two files of the server: those that the configuration names, else those in the
// directory <semar home>/android. Both are "" when the person gave none, and then the packages that the
// device has are used.
func (p *Platform) serverAPKs() (server, test string, err error) {
	env := p.o.Env
	if env.UIAutomator2ServerAPK != "" {
		server, err = p.resolvePath(env.UIAutomator2ServerAPK)
		if err != nil {
			return "", "", err
		}
		test, err = p.resolvePath(env.UIAutomator2ServerTestAPK)
		return server, test, err
	}
	if p.o.Home == "" {
		return "", "", nil
	}
	dir := filepath.Join(p.o.Home, "android")
	server, test = filepath.Join(dir, ServerAPKName), filepath.Join(dir, ServerTestAPKName)
	for _, f := range []string{server, test} {
		if info, err := os.Lstat(f); err != nil || !info.Mode().IsRegular() {
			return "", "", nil
		}
	}
	return server, test, nil
}

// resolvePath is an absolute path as it is, or a path in the repository: it must stay in it, also through
// links.
func (p *Platform) resolvePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	if !filepath.IsLocal(path) || p.o.RepoRoot == "" {
		return "", fmt.Errorf("the path %q is not inside the repository", path)
	}
	root, err := filepath.EvalSymlinks(p.o.RepoRoot)
	if err != nil {
		return "", fmt.Errorf("the repository cannot be resolved: %w", errors.Unwrap(err))
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, path))
	if err != nil {
		return "", fmt.Errorf("the file %s cannot be read: %w", path, errors.Unwrap(err))
	}
	if rel, err := filepath.Rel(root, full); err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("the file %s leaves the repository through a link", path)
	}
	return full, nil
}

// missingServer is the reason when the device has no server and the person gave no files.
func missingServer(missing []string) *mobile.Unavailable {
	return &mobile.Unavailable{Reason: fmt.Sprintf("the UiAutomator2 server is not on the device (%s is missing). Semar downloads nothing: put %s and %s in the Semar home under android/, "+
		"or name the two files with uiautomator2ServerApk and uiautomator2ServerTestApk under environments.<name>.android. They come from the Appium UiAutomator2 driver releases on GitHub",
		strings.Join(missing, ", "), ServerAPKName, ServerTestAPKName)}
}

// startServer installs what is needed, forwards a port, starts the instrumentation, and waits until the
// server answers. It records each thing it made on the platform before it goes on, so that Close can undo it.
func (p *Platform) startServer(ctx context.Context, a *adb) *mobile.Unavailable {
	serverAPK, testAPK, err := p.serverAPKs()
	if err != nil {
		return &mobile.Unavailable{Reason: err.Error()}
	}
	if serverAPK != "" {
		for _, f := range []struct{ pkg, file string }{{serverPackage, serverAPK}, {serverTestPackage, testAPK}} {
			if _, un := p.install(ctx, a, f.pkg, f.file, true); un != nil {
				return un
			}
		}
	} else {
		var missing []string
		for _, pkg := range []string{serverPackage, serverTestPackage} {
			paths, err := a.packagePaths(ctx, pkg)
			if err != nil {
				return &mobile.Unavailable{Reason: fmt.Sprintf("the packages of the device could not be read: %v", err)}
			}
			if len(paths) == 0 {
				missing = append(missing, pkg)
			}
		}
		if len(missing) > 0 {
			return missingServer(missing)
		}
	}

	// A server that is still running from another run, or from Appium, holds the port and the session.
	for _, pkg := range []string{serverPackage, serverTestPackage} {
		if _, err := a.shell(ctx, "am", "force-stop", pkg); err != nil && ctx.Err() == nil {
			p.o.Logger.DebugContext(ctx, "force-stop of a server package failed", "package", pkg, "error", err)
		}
	}
	out, err := a.out(ctx, "forward", "tcp:0", "tcp:"+strconv.Itoa(devicePort))
	if err != nil {
		return &mobile.Unavailable{Reason: fmt.Sprintf("the port of the server could not be forwarded: %v", err)}
	}
	port, perr := strconv.Atoi(strings.TrimSpace(out))
	if perr != nil || port <= 0 || port > 65535 {
		return &mobile.Unavailable{Reason: fmt.Sprintf("adb forward gave no port: %q", firstLine(out))}
	}
	p.port = port

	child, err := p.o.Run.Start(ctx, a.bin, a.args("shell", shellCommand("am", "instrument", "-w", "-e", "disableAnalytics", "true", instrumentation))...)
	if err != nil {
		return &mobile.Unavailable{Reason: fmt.Sprintf("the server could not be started: %v", err)}
	}
	p.child = child

	client := webdriver.New("http://127.0.0.1:" + strconv.Itoa(port))
	if p.o.HTTP != nil {
		client.HTTP = p.o.HTTP
	}
	if un := p.waitForServer(ctx, client, child); un != nil {
		return un
	}
	sess, err := client.NewSession(ctx, map[string]any{"platformName": "Android"})
	if err != nil {
		return &mobile.Unavailable{Reason: fmt.Sprintf("the server on the device did not start a session: %v", err)}
	}
	p.session = sess
	// A screen that animates, or that shows a spinner, would make the server wait for it to be idle before
	// it answers /source: for ten seconds at times. Semar settles the screen itself.
	if err := sess.Settings(ctx, map[string]any{"waitForIdleTimeout": 0, "waitForSelectorTimeout": 0}); err != nil {
		p.o.Logger.DebugContext(ctx, "the server settings were not changed", "error", err)
	}
	return nil
}

// waitForServer polls /status from 250 ms to 1 s until the server is ready, the instrumentation exits, or
// the start timeout passes.
func (p *Platform) waitForServer(ctx context.Context, client *webdriver.Client, child Child) *mobile.Unavailable {
	limit := p.o.Env.StartLimit()
	deadline := p.o.Now().Add(limit)
	delay := 250 * time.Millisecond
	last := ""
	for {
		select {
		case <-child.Done():
			reason := "the UiAutomator2 server on the device stopped as it started"
			if tail := lastLine(child.Tail()); tail != "" {
				reason += ": " + tail
			}
			return &mobile.Unavailable{Reason: reason}
		default:
		}
		pollCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ready, err := client.Status(pollCtx)
		cancel()
		switch {
		case err == nil && ready.Ready:
			return nil
		case err != nil:
			last = err.Error()
		default:
			last = "the server is not ready"
		}
		if ctx.Err() != nil {
			return &mobile.Unavailable{Reason: "the run was interrupted while the server started"}
		}
		if !p.o.Now().Before(deadline) {
			return &mobile.Unavailable{Reason: fmt.Sprintf("the UiAutomator2 server did not answer within %s (%s): raise startTimeout under environments.<name>.android, or check the device", limit, last)}
		}
		if err := p.o.Sleep(ctx, delay); err != nil {
			return &mobile.Unavailable{Reason: "the run was interrupted while the server started"}
		}
		delay = min(delay*2, time.Second)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return firstLine(lines[len(lines)-1])
}
