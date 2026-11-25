package android

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/webdriver"
)

// ErrUnsupported is what a controller method wraps when the device or the mode cannot do it: adb is not
// used at serverUrl, a physical device has no mock location, and an old Android has no per-app locale.
// A caller tests it with errors.Is.
var ErrUnsupported = errors.New("not supported")

func unsupportedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, args...))
}

// maxClipboard bounds the text that SetClipboard sends.
const maxClipboard = 64 << 10

// Controller is the device of a Platform with no app: what a tool server needs. It owns the changes that
// it made to the device (the rotation, a fold, a screen recording), and Platform.Close undoes them. The
// methods are safe to call from several goroutines.
type Controller struct {
	p *Platform

	mu        sync.Mutex
	drv       *driver
	drvSess   *webdriver.Session
	rotation  *savedRotation
	theme     *string
	fontScale *savedFontScale
	folded    bool
	rec       *recording
	ime       imeCache
}

// savedRotation is what the rotation settings were before the first SetOrientation.
type savedRotation struct{ auto, user string }

// Serial is the serial of the device: the one that adb chose, or the one that was named for a server at
// serverUrl ("" when none was).
func (c *Controller) Serial() string {
	if c.p.o.Env.Remote() {
		return c.p.serial()
	}
	return c.p.device.Serial
}

// Emulator is true for an emulator. A server at serverUrl never says it, so it is false there.
func (c *Controller) Emulator() bool {
	return !c.p.o.Env.Remote() && c.p.device.Emulator
}

// Remote is true when the device is behind a server at serverUrl. The methods that need adb then give
// ErrUnsupported.
func (c *Controller) Remote() bool { return c.p.o.Env.Remote() }

// adb gives the adb of the device, or ErrUnsupported at serverUrl.
func (c *Controller) adb() (*adb, error) {
	if c.p.o.Env.Remote() {
		return nil, unsupportedf("this needs adb, and the device is behind a server at serverUrl")
	}
	c.p.mu.Lock()
	closed := c.p.closed
	c.p.mu.Unlock()
	if closed {
		return nil, &mobile.Unavailable{Reason: "the device was released at the end of the run"}
	}
	return c.p.adb, nil
}

// uiConn is a session of the server and the client that made it.
type uiConn struct {
	sess   *webdriver.Session
	client *webdriver.Client
}

// ui gives the session of the server, and starts the server on the first call. At serverUrl it opens a
// session with no app. A server that does not start is stopped again, and the next call tries again.
func (c *Controller) ui(ctx context.Context) (uiConn, error) {
	p := c.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return uiConn{}, &mobile.Unavailable{Reason: "the device was released at the end of the run"}
	}
	if p.o.Env.Remote() {
		return p.controllerRemote(ctx)
	}
	if p.session != nil && p.local != nil {
		return uiConn{p.session, p.local}, nil
	}
	if p.startErr != nil {
		return uiConn{}, p.startErr
	}
	if un := p.ensureServer(ctx); un != nil {
		p.stopServer(context.WithoutCancel(ctx))
		return uiConn{}, un
	}
	p.started = true
	return uiConn{p.session, p.local}, nil
}

// controllerRemote opens the session of a controller at serverUrl: the capabilities of the configuration
// and the device, and no app.
func (p *Platform) controllerRemote(ctx context.Context) (uiConn, error) {
	c, un := p.remoteClient()
	if un != nil {
		return uiConn{}, un
	}
	if p.ctlSess != nil {
		return uiConn{p.ctlSess, c}, nil
	}
	host := hostOf(p.o.Env.ServerURL)
	ready, err := c.Status(ctx)
	switch {
	case err != nil:
		return uiConn{}, &mobile.Unavailable{Reason: fmt.Sprintf("the server at %s cannot be used: %v", host, err)}
	case !ready.Ready:
		reason := fmt.Sprintf("the server at %s is not ready", host)
		if ready.Message != "" {
			reason += ": " + ready.Message
		}
		return uiConn{}, &mobile.Unavailable{Reason: reason}
	}
	caps := p.remoteCaps(App{})
	for _, k := range []string{"appium:appPackage", "appium:appActivity", "appium:app", "appium:enforceAppInstall", "appium:autoGrantPermissions"} {
		delete(caps, k)
	}
	sess, err := c.NewSession(ctx, caps)
	if err != nil {
		return uiConn{}, &mobile.Unavailable{Reason: fmt.Sprintf("the server at %s did not start a session: %v", host, err)}
	}
	p.ctlSess = sess
	if err := sess.Settings(ctx, map[string]any{"waitForIdleTimeout": 0, "waitForSelectorTimeout": 0, "enableMultiWindows": true}); err != nil {
		p.o.Logger.DebugContext(ctx, "the server settings were not changed", "error", err)
	}
	return uiConn{sess, c}, nil
}

// driverOf is the driver that reads the screen on a session, the same one for as long as the session
// lives, so that a source with the same bytes gives the tree that was parsed first.
func (c *Controller) driverOf(s *webdriver.Session) *driver {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.drv == nil || c.drvSess != s {
		c.drv, c.drvSess = &driver{sess: s}, s
	}
	return c.drv
}

// Source reads the tree of the screen, as the driver of a run does.
func (c *Controller) Source(ctx context.Context) (*mobile.Tree, error) {
	u, err := c.ui(ctx)
	if err != nil {
		return nil, err
	}
	return c.driverOf(u.sess).Source(ctx)
}

// Screenshot gives a PNG of the screen. A screen that the app protects gives mobile.ErrSecure.
func (c *Controller) Screenshot(ctx context.Context) ([]byte, error) {
	u, err := c.ui(ctx)
	if err != nil {
		return nil, err
	}
	return c.driverOf(u.sess).Screenshot(ctx)
}

// Touch sends one gesture.
func (c *Controller) Touch(ctx context.Context, g mobile.Gesture) error {
	u, err := c.ui(ctx)
	if err != nil {
		return err
	}
	if g.Taps > 1 && g.From == g.To && g.Hold == 0 {
		return u.sess.Tap(ctx, webdriver.Point(g.From), g.Taps)
	}
	return u.sess.Touch(ctx, webdriver.Point(g.From), webdriver.Point(g.To), g.Hold, g.Move)
}

// TypeFocused types in the field that has the focus, and replaces its text when replace is set. When the
// server cannot do it and the device is local, it falls back to adb input, which takes printable ASCII
// only.
func (c *Controller) TypeFocused(ctx context.Context, text string, replace bool) error {
	u, err := c.ui(ctx)
	if err != nil {
		return err
	}
	err = typeWithServer(ctx, u.sess, text, replace)
	if err == nil || ctx.Err() != nil || c.Remote() {
		return err
	}
	a, aerr := c.adb()
	if aerr != nil {
		return err
	}
	if ferr := typeWithADB(ctx, a, text, replace); ferr != nil {
		return fmt.Errorf("%w (adb input did not do it either: %v)", err, ferr)
	}
	return nil
}

func typeWithServer(ctx context.Context, s *webdriver.Session, text string, replace bool) error {
	id, err := s.Active(ctx)
	if err != nil {
		return fmt.Errorf("no field has the focus: %w", err)
	}
	if replace {
		if err := s.Clear(ctx, id); err != nil {
			return err
		}
	}
	return s.SetValue(ctx, id, text)
}

const (
	keycodeMoveEnd = 123
	// replaceDeletes is how many characters the adb fallback deletes to replace a text.
	replaceDeletes = 128
	maxADBText     = 1000
)

// typeWithADB is input text: printable ASCII only, and a space is written %s. The text %s itself cannot
// be told apart from a space, so it is refused.
func typeWithADB(ctx context.Context, a *adb, text string, replace bool) error {
	if len(text) > maxADBText {
		return fmt.Errorf("the text is longer than %d characters", maxADBText)
	}
	for _, r := range text {
		if r < 0x20 || r > 0x7e {
			return errors.New("adb input takes printable ASCII only")
		}
	}
	if strings.Contains(text, "%s") {
		return errors.New("adb input cannot type the text %s")
	}
	if replace {
		words := []string{"input", "keyevent", fmt.Sprint(keycodeMoveEnd)}
		for range replaceDeletes {
			words = append(words, fmt.Sprint(keycodeDelete))
		}
		if _, err := a.shell(ctx, words...); err != nil {
			return err
		}
	}
	if text == "" {
		return nil
	}
	_, err := a.shell(ctx, "input", "text", strings.ReplaceAll(text, " ", "%s"))
	return err
}

// Key presses a key by its Android key code. When the server cannot, a local device gets input keyevent.
func (c *Controller) Key(ctx context.Context, code int) error {
	if code < 0 || code > 999 {
		return fmt.Errorf("%d is not a key code", code)
	}
	u, err := c.ui(ctx)
	if err != nil {
		return err
	}
	c.InvalidateInputMethod()
	err = u.sess.PressKeycode(ctx, code)
	if err == nil || ctx.Err() != nil || c.Remote() {
		return err
	}
	a, aerr := c.adb()
	if aerr != nil {
		return err
	}
	if _, ferr := a.shell(ctx, "input", "keyevent", fmt.Sprint(code)); ferr != nil {
		return fmt.Errorf("%w (adb input did not do it either: %v)", err, ferr)
	}
	return nil
}

// Orientation is portrait or landscape.
func (c *Controller) Orientation(ctx context.Context) (string, error) {
	u, err := c.ui(ctx)
	if err != nil {
		return "", err
	}
	var v string
	if err := u.call(ctx, http.MethodGet, "/orientation", nil, &v, false); err != nil {
		return "", err
	}
	return strings.ToLower(v), nil
}

// SetOrientation turns the screen to portrait or landscape. The first call on a local device saves the
// rotation settings, and Platform.Close puts them back.
func (c *Controller) SetOrientation(ctx context.Context, orientation string) error {
	o := strings.ToLower(strings.TrimSpace(orientation))
	if o != "portrait" && o != "landscape" {
		return fmt.Errorf("the orientation %q is not portrait or landscape", orientation)
	}
	u, err := c.ui(ctx)
	if err != nil {
		return err
	}
	if !c.Remote() {
		if err := c.saveRotation(ctx); err != nil {
			return err
		}
	}
	return u.call(ctx, http.MethodPost, "/orientation", map[string]any{"orientation": strings.ToUpper(o)}, nil, false)
}

func (c *Controller) saveRotation(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rotation != nil {
		return nil
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	read := func(key string) (string, error) {
		out, err := a.shell(ctx, "settings", "get", "system", key)
		if err != nil {
			return "", fmt.Errorf("the rotation could not be read: %w", err)
		}
		v := strings.TrimSpace(firstLine(out))
		if len(v) != 1 || v[0] < '0' || v[0] > '9' {
			return "", nil // null: there is nothing to put back
		}
		return v, nil
	}
	auto, err := read("accelerometer_rotation")
	if err != nil {
		return err
	}
	user, err := read("user_rotation")
	if err != nil {
		return err
	}
	c.rotation = &savedRotation{auto: auto, user: user}
	return nil
}

// Clipboard reads the text on the clipboard of the device.
func (c *Controller) Clipboard(ctx context.Context) (string, error) {
	u, err := c.ui(ctx)
	if err != nil {
		return "", err
	}
	var b64 string
	if err := u.call(ctx, http.MethodPost, "/appium/device/get_clipboard", map[string]any{"contentType": "plaintext"}, &b64, true); err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", errors.New("the clipboard is not base64")
	}
	return string(data), nil
}

// SetClipboard puts text on the clipboard of the device.
func (c *Controller) SetClipboard(ctx context.Context, text string) error {
	if len(text) > maxClipboard {
		return fmt.Errorf("the text is longer than %d KiB", maxClipboard>>10)
	}
	u, err := c.ui(ctx)
	if err != nil {
		return err
	}
	return u.call(ctx, http.MethodPost, "/appium/device/set_clipboard",
		map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(text)), "contentType": "plaintext"}, nil, true)
}

// maxCall bounds the answer of a call that is not a screenshot.
const maxCall = 1 << 20

// call sends one request of the session that the webdriver package has no method for, and reads the
// value of the answer into out. hush says that the body carries text that a person typed, so the message
// of an error is left out.
func (u uiConn) call(ctx context.Context, method, rest string, body, out any, hush bool) error {
	return u.callMax(ctx, maxCall, method, rest, body, out, hush)
}

// callMax is call with the most that the answer may hold.
func (u uiConn) callMax(ctx context.Context, limit int64, method, rest string, body, out any, hush bool) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("the request could not be encoded: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.client.Base+"/session/"+u.sess.ID+rest, rd)
	if err != nil {
		return errors.New("the request could not be made")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if u.client.User != "" {
		req.SetBasicAuth(u.client.User, u.client.Key)
	}
	hc := u.client.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("the server could not be reached: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("the answer could not be read: %w", err)
	}
	if int64(len(data)) > limit {
		return errors.New("the answer of the server is too large")
	}
	var env struct {
		Value json.RawMessage `json:"value"`
	}
	_ = json.Unmarshal(data, &env)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var f struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(env.Value, &f)
		e := &webdriver.Error{Status: resp.StatusCode, Code: f.Error}
		if !hush {
			e.Message = firstLine(f.Message)
		}
		return e
	}
	if out != nil && len(env.Value) > 0 && string(env.Value) != "null" {
		if err := json.Unmarshal(env.Value, out); err != nil {
			return errors.New("the answer of the server has the wrong form")
		}
	}
	return nil
}

// undo puts back what the controller changed on the device: the rotation settings, a fold, and a screen
// recording that is still going. Each step has three seconds. It is called by Platform.Close.
func (c *Controller) undo(ctx context.Context) []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.p.o.Env.Remote() {
		return c.undoRemote(ctx)
	}
	a := c.p.adb
	step := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 3*time.Second) }
	var errs []error
	if c.rec != nil {
		sctx, cancel := step()
		if err := c.abortRecording(sctx, a); err != nil {
			errs = append(errs, fmt.Errorf("the screen recording could not be cleaned up: %w", err))
		}
		cancel()
	}
	if c.rotation != nil {
		for _, kv := range [][2]string{{"accelerometer_rotation", c.rotation.auto}, {"user_rotation", c.rotation.user}} {
			if kv[1] == "" {
				continue
			}
			sctx, cancel := step()
			if _, err := a.shell(sctx, "settings", "put", "system", kv[0], kv[1]); err != nil {
				errs = append(errs, fmt.Errorf("the rotation could not be put back: %w", err))
			}
			cancel()
		}
		c.rotation = nil
	}
	if c.folded {
		sctx, cancel := step()
		if _, err := a.shell(sctx, "cmd", "device_state", "state", "reset"); err != nil {
			errs = append(errs, fmt.Errorf("the device state could not be reset: %w", err))
		}
		cancel()
		c.folded = false
	}
	if c.fontScale != nil {
		sctx, cancel := step()
		if err := restoreFontScale(sctx, a, *c.fontScale); err != nil {
			errs = append(errs, err)
		}
		cancel()
		c.fontScale = nil
	}
	if c.theme != nil {
		sctx, cancel := step()
		if err := restoreTheme(sctx, a, *c.theme); err != nil {
			errs = append(errs, err)
		}
		cancel()
		c.theme = nil
	}
	return errs
}
