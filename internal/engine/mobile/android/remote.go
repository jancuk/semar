package android

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/webdriver"
)

// The scripts that the lifecycle of an app uses with a server at serverUrl. A server that lacks one
// gives an error that names it.
const (
	scriptDeepLink     = "mobile: deepLink"
	scriptPressKey     = "mobile: pressKey"
	scriptActivateApp  = "mobile: activateApp"
	scriptTerminateApp = "mobile: terminateApp"
	scriptClearApp     = "mobile: clearApp"
	scriptHideKeyboard = "mobile: hideKeyboard"
)

// remoteCaps is the session request for the app of a service. The platform and the app are set last, so
// that the capabilities of the configuration cannot change them: a farm would then run another app.
func (p *Platform) remoteCaps(app App) map[string]any {
	caps := map[string]any{}
	maps.Copy(caps, p.o.Env.Capabilities)
	caps["platformName"] = "Android"
	caps["appium:automationName"] = "UiAutomator2"
	caps["appium:appPackage"] = app.Package
	delete(caps, "appium:appActivity")
	if app.Activity != "" {
		caps["appium:appActivity"] = app.Activity
	}
	caps["appium:noReset"] = true
	caps["appium:autoLaunch"] = false
	caps["appium:newCommandTimeout"] = 300
	if serial := p.serial(); serial != "" {
		caps["appium:udid"] = serial
	}
	delete(caps, "appium:app")
	delete(caps, "appium:enforceAppInstall")
	switch {
	case app.APK != "":
		caps["appium:app"] = app.APK
		caps["appium:enforceAppInstall"] = true
	case app.Ref != "":
		caps["appium:app"] = app.Ref
	}
	if len(app.Permissions) > 0 {
		caps["appium:autoGrantPermissions"] = true
	}
	return caps
}

// remoteClient makes the client of the server once, with the credentials that the configuration names.
func (p *Platform) remoteClient() (*webdriver.Client, *mobile.Unavailable) {
	if p.client != nil {
		return p.client, nil
	}
	c := webdriver.New(p.o.Env.ServerURL)
	if p.o.HTTP != nil {
		c.HTTP = p.o.HTTP
	}
	if ref := p.o.Env.ServerUser; ref != "" {
		var un *mobile.Unavailable
		if c.User, un = p.secretOf("serverUser", ref); un != nil {
			return nil, un
		}
		if c.Key, un = p.secretOf("serverKey", p.o.Env.ServerKey); un != nil {
			return nil, un
		}
	}
	p.client = c
	return c, nil
}

// secretOf reads an env:NAME reference. The value is never put in a message.
func (p *Platform) secretOf(field, ref string) (string, *mobile.Unavailable) {
	name, ok := strings.CutPrefix(ref, "env:")
	if !ok || name == "" {
		return "", &mobile.Unavailable{Reason: fmt.Sprintf("%s must be a reference such as env:FARM_USER", field)}
	}
	v, ok := p.o.LookupEnv(name)
	if !ok || v == "" {
		return "", &mobile.Unavailable{Reason: fmt.Sprintf("%s names the variable %s, which is not set", field, name)}
	}
	return v, nil
}

// remoteDriver starts a session at serverUrl for the app of a service. No adb is used: the server
// installs the app, starts it, and stops it.
func (p *Platform) remoteDriver(ctx context.Context, service string, app App) (*driver, *mobile.Unavailable) {
	if app.APK != "" {
		apkPath, err := p.resolvePath(app.APK)
		if err != nil {
			return nil, &mobile.Unavailable{Reason: err.Error()}
		}
		app.APK = apkPath
	}
	c, un := p.remoteClient()
	if un != nil {
		return nil, un
	}
	host := hostOf(p.o.Env.ServerURL)
	if !p.started {
		ready, err := c.Status(ctx)
		switch {
		case err != nil:
			return nil, &mobile.Unavailable{Reason: fmt.Sprintf("the server at %s cannot be used: %v", host, err)}
		case !ready.Ready:
			reason := fmt.Sprintf("the server at %s is not ready", host)
			if ready.Message != "" {
				reason += ": " + ready.Message
			}
			return nil, &mobile.Unavailable{Reason: reason}
		}
		p.started = true
	}
	if app.APK != "" && app.APKDigest != "" {
		sum, err := fileSHA256(app.APK)
		if err != nil {
			return nil, &mobile.Unavailable{Reason: err.Error()}
		}
		if !strings.HasPrefix(sum, strings.ToLower(app.APKDigest)) {
			return nil, &mobile.Unavailable{Reason: fmt.Sprintf("the APK %s changed after the build of this run was read: its result would belong to another build. Run again", baseName(app.APK))}
		}
	}
	sess, err := c.NewSession(ctx, p.remoteCaps(app))
	if err != nil {
		return nil, &mobile.Unavailable{Reason: fmt.Sprintf("the server at %s did not start a session: %v", host, err)}
	}
	p.remote[service] = sess
	if err := sess.Settings(ctx, map[string]any{"waitForIdleTimeout": 0, "waitForSelectorTimeout": 0}); err != nil {
		p.o.Logger.DebugContext(ctx, "the server settings were not changed", "error", err)
	}
	return &driver{
		sess:  sess,
		life:  &remoteLife{sess: sess, app: app},
		about: mobile.About{Device: p.serial(), Package: app.Package},
	}, nil
}

// hostOf is the host and port of an address, for a message.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "the server"
	}
	return u.Host
}

// remoteLife controls the app with the scripts of the server.
type remoteLife struct {
	sess *webdriver.Session
	app  App
}

func (l *remoteLife) exec(ctx context.Context, script string, args map[string]any) error {
	_, err := l.sess.Execute(ctx, script, args)
	if err == nil {
		return nil
	}
	var we *webdriver.Error
	if errors.As(err, &we) && unsupported(we) {
		return fmt.Errorf("the server does not support %s", script)
	}
	return err
}

// unsupported is true for the answer of a server that does not know a command.
func unsupported(e *webdriver.Error) bool {
	code := strings.ToLower(e.Code)
	msg := strings.ToLower(e.Message)
	return code == "unknown command" || code == "unknown method" || code == "unsupported operation" || e.Status == 404 || e.Status == 405 || e.Status == 501 ||
		strings.Contains(msg, "unknown mobile command") || strings.Contains(msg, "not implemented")
}

func (l *remoteLife) reset(ctx context.Context) error {
	mode := l.app.resetMode()
	if mode == config.ResetNone {
		return nil
	}
	args := map[string]any{"appId": l.app.Package}
	if err := l.exec(ctx, scriptTerminateApp, args); err != nil {
		return err
	}
	if mode == config.ResetClear {
		return l.exec(ctx, scriptClearApp, args)
	}
	return nil
}

func (l *remoteLife) launch(ctx context.Context) error {
	return l.exec(ctx, scriptActivateApp, map[string]any{"appId": l.app.Package})
}

func (l *remoteLife) open(ctx context.Context, link string) error {
	return l.exec(ctx, scriptDeepLink, map[string]any{"url": link, "package": l.app.Package})
}

func (l *remoteLife) key(ctx context.Context, code int) error {
	return l.exec(ctx, scriptPressKey, map[string]any{"keycode": code})
}

func (l *remoteLife) hideKeyboard(ctx context.Context) error {
	return l.exec(ctx, scriptHideKeyboard, nil)
}
