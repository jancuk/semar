package mobilemcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/mobilerec"
)

// run runs a tool with the lock held. While a recording goes on the mobile_* tools go through its session,
// so that an action is a step and the session keeps the one lease on the device.
func (m *Manager) run(ctx context.Context, d *toolDef, call mcp.Call) (mcp.Result, error) {
	if m.mode != modeRecord || m.rec == nil {
		return d.run(ctx, call)
	}
	if strings.HasPrefix(d.name, "semar_") {
		return d.run(ctx, call)
	}
	return m.runRecording(ctx, d, call)
}

// recordKeys are the keys that a recording stores, by the name of the button.
var recordKeys = map[string]string{
	"BACK": "back", "HOME": "home", "ENTER": "enter", "SEARCH": "search", "TAB": "tab", "DEL": "delete", "BACKSPACE": "delete",
}

// refused says that a tool does not work while a recording goes on, and what to do.
func refused(name, why string) error {
	return fmt.Errorf("%s is not available while recording: %s. End the recording with semar_record_stop to use it", name, why)
}

func (m *Manager) runRecording(ctx context.Context, d *toolDef, call mcp.Call) (mcp.Result, error) {
	switch d.name {
	case "mobile_list_available_devices", batchName:
		// A batch runs each of its steps through this function again.
		return d.run(ctx, call)
	case "mobile_list_elements_on_screen":
		return m.recList(ctx, call)
	case "mobile_get_screen_size":
		if err := noArgs(call); err != nil {
			return mcp.Result{}, err
		}
		return textJSON(map[string]any{"width": m.rec.frame.Width, "height": m.rec.frame.Height})
	case "mobile_get_orientation":
		if err := noArgs(call); err != nil {
			return mcp.Result{}, err
		}
		o := "portrait"
		if m.rec.frame.Width > m.rec.frame.Height {
			o = "landscape"
		}
		return textJSON(map[string]any{"orientation": o})
	case "mobile_click_on_screen_at_coordinates":
		return m.recPoint(ctx, call, mobilerec.DoTap)
	case "mobile_double_tap_on_screen":
		return m.recPoint(ctx, call, mobilerec.DoDoubleTap)
	case "mobile_long_press_on_screen_at_coordinates":
		return m.recLongPress(ctx, call)
	case "mobile_drag_on_screen":
		return m.recDrag(ctx, call)
	case "mobile_swipe_on_screen":
		return m.recSwipe(ctx, call)
	case "mobile_type_keys":
		return m.recType(ctx, call)
	case "mobile_press_button":
		return m.recPress(ctx, call)
	case "mobile_open_url":
		return m.recOpen(ctx, call)
	case "mobile_launch_app":
		return mcp.Result{}, refused(d.name, "the recording started the app already, and a step that starts it again is not recorded. To start over from the reset, save, end the recording, and start another")
	case "mobile_take_screenshot":
		return m.recShot(ctx, call, false)
	case "mobile_save_screenshot":
		return m.recShot(ctx, call, true)
	case "mobile_use_device":
		return mcp.Result{}, refused(d.name, "the recording holds one device")
	case "mobile_clipboard":
		return m.recClipboard(ctx, call)
	case "mobile_set_orientation", "mobile_set_theme", "mobile_set_font_scale", "mobile_fold_device", "mobile_set_location",
		"mobile_terminate_app", "mobile_install_app", "mobile_uninstall_app":
		return mcp.Result{}, refused(d.name, "a replay cannot repeat it, so it would make a recording that does not replay")
	case "mobile_clear_app_data":
		return mcp.Result{}, refused(d.name, "use reset: clear in semar_record_start, which a replay repeats")
	}
	// The rest (the logs, the crashes, the screen recording, the list of apps, the app in front) read the
	// device through a second session, which the recording's lease does not allow.
	return mcp.Result{}, refused(d.name, "the recording holds the device, and a second session on it is not possible")
}

// frameHead writes the first lines of a frame: the size, the count of elements, and the name of the screen,
// then the secret line and the notice when there are any.
func (m *Manager) frameHead(f *mobilerec.Frame) string {
	var b strings.Builder
	name := f.Screen
	if name == "" {
		name = "unnamed"
	}
	fmt.Fprintf(&b, "screen %dx%d, %d elements, called %s\n", f.Width, f.Height, len(f.Elements), name)
	if f.Secret {
		b.WriteString("the screen shows a secret value: its text is hidden, and a screenshot is refused until it is gone\n")
	}
	if f.Notice != "" {
		b.WriteString("notice: " + f.Notice + "\n")
	}
	return b.String()
}

// frameText writes a frame as the model reads it: the refs e1..eN are the elements of this frame.
func (m *Manager) frameText(f *mobilerec.Frame) string {
	var b strings.Builder
	b.WriteString(m.frameHead(f))
	lastWin := ""
	for _, e := range f.Elements {
		if w := e.Window + " " + e.Package; w != lastWin {
			fmt.Fprintf(&b, "window %s %s\n", nonEmpty(e.Window, "app"), nonEmpty(e.Package, "unknown"))
			lastWin = w
		}
		b.WriteString(m.elementLine(e))
		if e.Warning != "" {
			b.WriteString("  ! " + e.Warning)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// recFrameText reads the screen again and writes it.
func (m *Manager) recFrameText(ctx context.Context) (string, error) {
	r := m.rec
	f, err := r.sess.Refresh(ctx)
	if err != nil {
		return "", err
	}
	r.frame = f
	return m.frameText(f), nil
}

func (m *Manager) recList(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Format string `json:"format"`
		Raw    bool   `json:"raw"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if a.Raw {
		return mcp.Result{}, errors.New("the raw tree is not available while recording: the list shows what a recording can act on")
	}
	format := strings.ToLower(strings.TrimSpace(a.Format))
	if format != "" && format != "text" && format != "json" {
		return mcp.Result{}, errors.New("format must be text or json")
	}
	r := m.rec
	f, err := r.sess.Refresh(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	// A WebView that was just opened lists as one empty document: read it again while it fills.
	for i := 0; i < flutterRetries && emptyDocument(f.Elements, f.Width, f.Height); i++ {
		if err := m.sleep(ctx, flutterInterval); err != nil {
			return mcp.Result{}, err
		}
		if f, err = r.sess.Refresh(ctx); err != nil {
			return mcp.Result{}, err
		}
	}
	r.frame = f
	if format != "json" {
		return mcp.TextResult(m.frameText(f)), nil
	}
	out := struct {
		Width    int           `json:"width"`
		Height   int           `json:"height"`
		Screen   string        `json:"screen"`
		Elements []jsonElement `json:"elements"`
	}{Width: f.Width, Height: f.Height, Screen: f.Screen, Elements: make([]jsonElement, 0, len(f.Elements))}
	for _, e := range f.Elements {
		je := jsonElement{Ref: fmt.Sprintf("e%d", e.Index+1), Element: e}
		c := e.Bounds.Center()
		je.Center.X, je.Center.Y = c.X, c.Y
		out.Elements = append(out.Elements, je)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return mcp.Result{}, err
	}
	return mcp.TextResult(string(b)), nil
}

// recTarget turns a ref or a pair of coordinates into the target of the frame that the model saw last.
func (m *Manager) recTarget(ref string, x, y *float64) (*mobilerec.Target, error) {
	f := m.rec.frame
	if strings.TrimSpace(ref) != "" {
		n, err := ParseRef(ref)
		if err != nil {
			return nil, err
		}
		if n > len(f.Elements) {
			return nil, fmt.Errorf("ref e%d does not exist: the screen has e1 to e%d. List it again with mobile_list_elements_on_screen", n, len(f.Elements))
		}
		return &mobilerec.Target{Index: n - 1}, nil
	}
	if x == nil || y == nil {
		return nil, errors.New("give a ref from mobile_list_elements_on_screen, or x and y")
	}
	p, err := coordinate(*x, *y)
	if err != nil {
		return nil, err
	}
	// A point is the smallest element of the frame that holds it, and of two of the same size the later one,
	// which is drawn on top. A point on no element, or on the whole screen, has nothing a replay could find.
	best, area := -1, 0
	for _, e := range f.Elements {
		b := e.Bounds
		if p.X < b.Left || p.X >= b.Right || p.Y < b.Top || p.Y >= b.Bottom {
			continue
		}
		if a := b.Width() * b.Height(); best < 0 || a <= area {
			best, area = e.Index, a
		}
	}
	if best < 0 {
		return nil, fmt.Errorf("(%d,%d) is on no element that can be recorded: %w", p.X, p.Y, m.hint(mobilerec.ErrNoLocator))
	}
	return &mobilerec.Target{Index: best}, nil
}

// afterRecord makes the result of an action or a check of the recording: what was recorded, and the
// screen now. A stale action comes back with the screen as it is, whose refs are valid.
func (m *Manager) afterRecord(r *recording, before int, f *mobilerec.Frame, err error) (mcp.Result, error) {
	if err != nil {
		var se *mobilerec.StaleError
		if errors.As(err, &se) && se.Frame != nil {
			r.frame = se.Frame
			return mcp.Result{}, fmt.Errorf("the screen changed, so nothing was recorded. The screen now:\n%s", m.frameText(se.Frame))
		}
		return mcp.Result{}, m.hint(err)
	}
	b := m.recordedSteps(r, before, f)
	b.WriteString(m.frameText(f))
	return mcp.TextResult(b.String()), nil
}

// recordedSteps takes the frame as the current one and writes the steps that were recorded since before.
func (m *Manager) recordedSteps(r *recording, before int, f *mobilerec.Frame) *strings.Builder {
	r.frame = f
	r.unsaved = true
	var b strings.Builder
	if steps := r.sess.Steps(); len(steps) > before {
		for _, s := range steps[before:] {
			fmt.Fprintf(&b, "recorded step %d (%s): %s\n", s.N, s.Kind, s.Text)
		}
	}
	return &b
}

// afterCheck is afterRecord for a check, a capture, or a screen name. They change nothing on the device, so
// the refs of the last listing stay valid and the answer does not repeat the listing. An error is answered
// as afterRecord does, so that a stale ref still shows the new screen.
func (m *Manager) afterCheck(r *recording, before int, f *mobilerec.Frame, err error) (mcp.Result, error) {
	if err != nil {
		return m.afterRecord(r, before, f, err)
	}
	b := m.recordedSteps(r, before, f)
	b.WriteString(m.frameHead(f))
	b.WriteString("the refs of the last listing stay valid")
	return mcp.TextResult(b.String()), nil
}

func (m *Manager) recAct(ctx context.Context, a mobilerec.Act) (mcp.Result, error) {
	r := m.rec
	a.Frame = r.frame.ID
	before := len(r.sess.Steps())
	kb := m.keyboardOf(r.frame, a.Target)
	f, err := r.sess.Act(ctx, a)
	if err == nil && kb != "" && f != nil {
		// The locator is a normal one, and the engine looks in every window. A replay on a device whose
		// keyboard is another one finds no key, so the person is told.
		nf := *f
		note := "this key belongs to the keyboard " + kb + ": replay needs it as the default input method"
		if nf.Notice != "" {
			note = nf.Notice + "; " + note
		}
		nf.Notice = note
		f = &nf
	}
	return m.afterRecord(r, before, f, err)
}

// keyboardOf gives the package of the keyboard that the target of an action is a key of, or "".
func (m *Manager) keyboardOf(f *mobilerec.Frame, t *mobilerec.Target) string {
	if f == nil || t == nil || t.Index < 0 || t.Index >= len(f.Elements) {
		return ""
	}
	if e := f.Elements[t.Index]; e.Window == string(mobile.WindowKeyboard) {
		return nonEmpty(e.Package, "of the keyboard")
	}
	return ""
}

func (m *Manager) recPoint(ctx context.Context, call mcp.Call, do string) (mcp.Result, error) {
	var a pointArgs
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	t, err := m.recTarget(a.Ref, a.X, a.Y)
	if err != nil {
		return mcp.Result{}, err
	}
	return m.recAct(ctx, mobilerec.Act{Do: do, Target: t})
}

func (m *Manager) recLongPress(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		pointArgs
		Duration int `json:"duration"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if a.Duration < 0 || a.Duration > maxHold {
		return mcp.Result{}, fmt.Errorf("duration is 1 to %d milliseconds", maxHold)
	}
	t, err := m.recTarget(a.Ref, a.X, a.Y)
	if err != nil {
		return mcp.Result{}, err
	}
	res, err := m.recAct(ctx, mobilerec.Act{Do: mobilerec.DoLongPress, Target: t})
	if err == nil {
		res.Content = append([]mcp.Content{{Type: "text", Text: "the hold time is not stored: a replay holds for 700 ms"}}, res.Content...)
	}
	return res, err
}

func (m *Manager) recDrag(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		X1      *float64 `json:"x1"`
		Y1      *float64 `json:"y1"`
		X2      *float64 `json:"x2"`
		Y2      *float64 `json:"y2"`
		FromRef string   `json:"fromRef"`
		ToRef   string   `json:"toRef"`
		Hold    int      `json:"hold"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if a.Hold < 0 || a.Hold > maxHold {
		return mcp.Result{}, fmt.Errorf("hold is 0 to %d milliseconds", maxHold)
	}
	from, err := m.recTarget(a.FromRef, a.X1, a.Y1)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("the start of the drag: %w", err)
	}
	to, err := m.recTarget(a.ToRef, a.X2, a.Y2)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("the end of the drag: %w", err)
	}
	act := mobilerec.Act{Do: mobilerec.DoDrag, Target: from, To: to}
	if a.Hold > 0 {
		act.Value = fmt.Sprintf("%dms", a.Hold)
	}
	return m.recAct(ctx, act)
}

func (m *Manager) recSwipe(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Direction string   `json:"direction"`
		X         *float64 `json:"x"`
		Y         *float64 `json:"y"`
		Distance  float64  `json:"distance"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	dir := strings.ToLower(strings.TrimSpace(a.Direction))
	switch dir {
	case "up", "down", "left", "right":
	default:
		return mcp.Result{}, fmt.Errorf("the direction %q is not up, down, left, or right", truncateText(a.Direction, 20))
	}
	act := mobilerec.Act{Do: mobilerec.DoSwipe, Value: dir}
	if (a.X == nil) != (a.Y == nil) {
		return mcp.Result{}, errors.New("give both x and y, or neither")
	}
	if a.X != nil {
		t, err := m.recTarget("", a.X, a.Y)
		if err != nil {
			return mcp.Result{}, err
		}
		act.Target = t
	}
	res, err := m.recAct(ctx, act)
	if err == nil {
		res.Content = append([]mcp.Content{{Type: "text", Text: "the distance is not stored: a replay swipes the default distance"}}, res.Content...)
	}
	return res, err
}

// focusedField is the element of the frame that has the focus and takes text.
func (m *Manager) focusedField() (*mobilerec.Target, error) {
	for _, e := range m.rec.frame.Elements {
		if e.Focused && e.Editable {
			return &mobilerec.Target{Index: e.Index}, nil
		}
	}
	return nil, errors.New("no field has the focus: tap the field first, or give its ref")
}

func (m *Manager) recType(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Text      *string `json:"text"`
		Submit    bool    `json:"submit"`
		Ref       string  `json:"ref"`
		SecretEnv string  `json:"secretEnv"`
		Replace   bool    `json:"replace"`
		Via       string  `json:"via"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	via, err := typeVia(a.Via)
	if err != nil {
		return mcp.Result{}, err
	}
	if via == viaKeyboard {
		return mcp.Result{}, errors.New("via: keyboard is not available while recording: tap the keys by their refs, each tap is a step, and the replay needs that keyboard as the default input method. Or type with the default via, which a replay repeats on any keyboard")
	}
	r := m.rec
	act := mobilerec.Act{Do: mobilerec.DoType}
	switch {
	case a.Text != nil && a.SecretEnv != "":
		return mcp.Result{}, errors.New("give text or secretEnv, not both")
	case a.SecretEnv != "":
		if err := mobilerec.CheckSecretEnv(a.SecretEnv); err != nil {
			return mcp.Result{}, err
		}
		// The value comes from the environment of the server, through the layer of the session, and is
		// never given to the model.
		v, ok := r.sess.Overlay().Lookup(a.SecretEnv)
		if !ok || v == "" {
			return mcp.Result{}, fmt.Errorf("the environment variable %s is not set", a.SecretEnv)
		}
		m.scrub.Add(v)
		act.Value, act.Secret = v, a.SecretEnv
	case a.Text != nil:
		act.Value = *a.Text
	default:
		return mcp.Result{}, errors.New("give the text to type, or secretEnv")
	}
	var t *mobilerec.Target
	if strings.TrimSpace(a.Ref) != "" {
		t, err = m.recTarget(a.Ref, nil, nil)
	} else {
		t, err = m.focusedField()
	}
	if err != nil {
		return mcp.Result{}, err
	}
	act.Target = t
	res, err := m.recAct(ctx, act)
	if err != nil || !a.Submit {
		return res, err
	}
	enter, perr := m.recAct(ctx, mobilerec.Act{Do: mobilerec.DoPress, Value: "enter"})
	if perr != nil {
		return mcp.Result{}, fmt.Errorf("the text was typed and recorded, but Enter failed: %w", perr)
	}
	return enter, nil
}

func (m *Manager) recPress(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Button string `json:"button"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	key, ok := recordKeys[strings.ToUpper(strings.TrimSpace(a.Button))]
	if !ok {
		return mcp.Result{}, fmt.Errorf("the button %q cannot be recorded: a recording takes BACK, HOME, ENTER, SEARCH, TAB, and DEL", truncateText(a.Button, 30))
	}
	return m.recAct(ctx, mobilerec.Act{Do: mobilerec.DoPress, Value: key})
}

func (m *Manager) recOpen(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if err := m.urls.Check(a.URL); err != nil {
		return mcp.Result{}, fmt.Errorf("the link is refused: %w", err)
	}
	return m.recAct(ctx, mobilerec.Act{Do: mobilerec.DoOpen, Value: a.URL})
}

// recShot takes a screenshot through the session, which refuses a screen that shows a secret.
func (m *Manager) recShot(ctx context.Context, call mcp.Call, save bool) (mcp.Result, error) {
	var a struct {
		SaveTo  string    `json:"saveTo"`
		MaxSize int       `json:"maxSize"`
		Scale   float64   `json:"scale"`
		Clip    *clipArgs `json:"clip"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	req := shotRequest{MaxSize: a.MaxSize, Scale: a.Scale, Clip: a.Clip}
	var path string
	exts := []string{".png", ".jpg", ".jpeg"}
	if save {
		if m.out == nil {
			return mcp.Result{}, errors.New("this server has no output folder, so it cannot save files")
		}
		p, err := m.out.Resolve(a.SaveTo, exts)
		if err != nil {
			return mcp.Result{}, fmt.Errorf("saveTo is refused: %w", err)
		}
		path = p
		req.PNG = strings.HasSuffix(strings.ToLower(p), ".png")
		if req.MaxSize == 0 {
			req.MaxSize = maxMaxSize
		}
	}
	clip, err := req.check()
	if err != nil {
		return mcp.Result{}, err
	}
	data, err := m.rec.sess.Capture(ctx)
	if err != nil {
		return mcp.Result{}, m.hint(err)
	}
	max := req.MaxSize
	if max == 0 {
		max = defaultMaxSize
	}
	shot, err := mobile.Render(data, mobile.ShotOptions{MaxSide: max, Scale: req.Scale, Clip: clip, PNG: req.PNG})
	if err != nil {
		return mcp.Result{}, err
	}
	if !save {
		return mcp.ImageResult(base64.StdEncoding.EncodeToString(shot.Data), "image/jpeg", caption(shot)), nil
	}
	saved, err := m.out.Write(path, exts, shot.Data)
	if err != nil {
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{
		"path": saved, "bytes": len(shot.Data), "width": shot.Width, "height": shot.Height,
		"screenWidth": shot.ScreenWidth, "screenHeight": shot.ScreenHeight,
	})
}

// recClipboard reads the clipboard through the recording, which holds the device. A read is no step. A
// write is refused: a replay cannot repeat it, so it would make a recording that does not replay.
func (m *Manager) recClipboard(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Text *string `json:"text"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if a.Text != nil {
		if err := m.guard.Allow(mcpkit.SurfaceMobile, true); err != nil {
			return mcp.Result{}, err
		}
		return mcp.Result{}, refused("mobile_clipboard", "a replay cannot repeat setting it, so it would make a recording that does not replay. Reading the clipboard works")
	}
	text, err := m.rec.sess.Clipboard(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	return m.clipboardRead(text)
}
