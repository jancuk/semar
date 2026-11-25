package mobilemcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mobilerec"
)

const (
	// swipeDuration is how long the finger takes to move in a swipe, as in the engine.
	swipeDuration = 300 * time.Millisecond
	// dragDuration is how long the finger takes to move in a drag, after the hold.
	dragDuration = 400 * time.Millisecond
	// swipeMargin is the share of each side where a swipe does not start or end, so that the gestures of
	// the system (back, home, the shade) do not fire.
	swipeMargin = 0.05
	// defaultSwipe is the share of the screen that a swipe covers when no distance is given.
	defaultSwipe = 0.30

	defaultLongPress = 500
	maxHold          = 10000
)

var (
	xSchema   = mcp.Number("Horizontal position in pixels from the left edge of the screen.").With("minimum", 0)
	ySchema   = mcp.Number("Vertical position in pixels from the top edge of the screen.").With("minimum", 0)
	refSchema = mcp.String("A ref such as e3 from mobile_list_elements_on_screen. It wins over x and y when both are given.")
)

func (m *Manager) inputTools() []*toolDef {
	pointProps := func(extra map[string]mcp.Schema) map[string]mcp.Schema {
		p := map[string]mcp.Schema{"x": xSchema, "y": ySchema, "ref": refSchema}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	return []*toolDef{
		{
			name: "mobile_click_on_screen_at_coordinates", title: "Tap",
			desc:   "Tap the screen at x and y, or at the center of the element that a ref names. When both are given, the ref wins.",
			schema: mcp.Object(pointProps(nil)),
			run:    m.click,
		},
		{
			name: "mobile_double_tap_on_screen", title: "Double tap",
			desc:   "Tap twice at x and y, or at the center of the element that a ref names. When both are given, the ref wins.",
			schema: mcp.Object(pointProps(nil)),
			run:    m.doubleTap,
		},
		{
			name: "mobile_long_press_on_screen_at_coordinates", title: "Long press",
			desc: "Press and hold at x and y, or at the center of the element that a ref names. duration is in milliseconds, 500 by default.",
			schema: mcp.Object(pointProps(map[string]mcp.Schema{
				"duration": mcp.Int("How long to hold, in milliseconds. 500 by default.").With("minimum", 1).With("maximum", maxHold),
			})),
			run: m.longPress,
		},
		{
			name: "mobile_drag_on_screen", title: "Drag",
			desc: "Drag from one point to another. Give x1, y1, x2, y2, or fromRef and toRef (a ref wins over its coordinates). hold is how long the finger rests on the start before it moves, in milliseconds, for a screen that picks an item up on a long press.",
			schema: mcp.Object(map[string]mcp.Schema{
				"x1":      xSchema,
				"y1":      ySchema,
				"x2":      xSchema,
				"y2":      ySchema,
				"fromRef": mcp.String("A ref for the start of the drag."),
				"toRef":   mcp.String("A ref for the end of the drag."),
				"hold":    mcp.Int("Milliseconds to hold before moving. 0 by default.").With("minimum", 0).With("maximum", maxHold),
			}),
			run: m.drag,
		},
		{
			name: "mobile_swipe_on_screen", title: "Swipe",
			desc: "Swipe the finger in a direction. It starts at x and y, or at the center of the screen, and covers distance pixels, or 30% of the screen side by default. The path stays 5% inside the edges so that system gestures do not fire. To scroll a list down, swipe up.",
			schema: mcp.Object(map[string]mcp.Schema{
				"direction": mcp.Enum("The direction that the finger moves.", "up", "down", "left", "right"),
				"x":         xSchema,
				"y":         ySchema,
				"distance":  mcp.Number("How far to swipe, in pixels.").With("minimum", 1),
			}).Required("direction"),
			run: m.swipe,
		},
		{
			name: "mobile_type_keys", title: "Type text",
			desc: "Type text in the field that has the focus, or tap the field that ref names first. replace clears the field first. submit presses Enter after the text. To type a secret, name an environment variable in secretEnv instead of text: its value is typed, and never shown. By default the text is set in the field. With via: keyboard each character is typed by tapping the matching key of the on-screen keyboard, to test the keyboard itself: it is slower, needs the keyboard to be shown, and is refused with secretEnv.",
			schema: mcp.Object(map[string]mcp.Schema{
				"text":      mcp.String("The text to type."),
				"submit":    mcp.Bool("Press Enter after the text."),
				"ref":       mcp.String("A ref of the field, from mobile_list_elements_on_screen. It is tapped first."),
				"secretEnv": mcp.String("The name of an environment variable whose value is typed. Give it instead of text."),
				"replace":   mcp.Bool("Clear the field before typing."),
				"via":       mcp.Enum("direct sets the text in the field (the default). keyboard taps the keys of the on-screen keyboard one by one.", "direct", "keyboard"),
			}),
			run: m.typeKeys,
		},
		{
			name: "mobile_press_button", title: "Press a button",
			desc: "Press a hardware or navigation key: HOME, BACK, ENTER, VOLUME_UP, VOLUME_DOWN, MENU, APP_SWITCH (or RECENTS), TAB, DEL (or BACKSPACE), FORWARD_DEL, SEARCH, ESCAPE, POWER, DPAD_UP, DPAD_DOWN, DPAD_LEFT, DPAD_RIGHT, DPAD_CENTER. Not case sensitive.",
			schema: mcp.Object(map[string]mcp.Schema{
				"button": mcp.String("The name of the key."),
			}).Required("button"),
			run: m.pressButton,
		},
		{
			name: "mobile_open_url", title: "Open a link", openWorld: true,
			desc: "Open a link on the device. http and https links are always allowed (and checked against the environment when the server has one). Other schemes need the server to allow them. file, content, intent, javascript, data, about, and vbscript are never opened.",
			schema: mcp.Object(map[string]mcp.Schema{
				"url": mcp.String("The link, up to 2048 characters, with no spaces."),
			}).Required("url"),
			run: m.openURL,
		},
	}
}

type pointArgs struct {
	X   *float64 `json:"x"`
	Y   *float64 `json:"y"`
	Ref string   `json:"ref"`
}

// note says in words where a tap went.
func (t Target) note() string {
	switch {
	case t.Ref == "":
		return fmt.Sprintf("(%d,%d)", t.Point.X, t.Point.Y)
	case t.Refound:
		return fmt.Sprintf("%s, found again at (%d,%d)", t.Ref, t.Point.X, t.Point.Y)
	default:
		return fmt.Sprintf("%s at (%d,%d)", t.Ref, t.Point.X, t.Point.Y)
	}
}

func (m *Manager) tapLike(ctx context.Context, call mcp.Call, taps int, hold time.Duration, verb string) (mcp.Result, error) {
	var a pointArgs
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	return m.tapAt(ctx, a, taps, hold, verb)
}

func (m *Manager) tapAt(ctx context.Context, a pointArgs, taps int, hold time.Duration, verb string) (mcp.Result, error) {
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	t, err := m.pointOf(ctx, dev, a.Ref, a.X, a.Y)
	if err != nil {
		return mcp.Result{}, err
	}
	if err := dev.Touch(ctx, mobile.Gesture{From: t.Point, To: t.Point, Hold: hold, Taps: taps}); err != nil {
		return mcp.Result{}, err
	}
	return mcp.TextResult(verb + " " + t.note()), nil
}

func (m *Manager) click(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	return m.tapLike(ctx, call, 0, 0, "tapped")
}

func (m *Manager) doubleTap(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	return m.tapLike(ctx, call, 2, 0, "double tapped")
}

func (m *Manager) longPress(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		pointArgs
		Duration int `json:"duration"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	d := a.Duration
	if d == 0 {
		d = defaultLongPress
	}
	if d < 1 || d > maxHold {
		return mcp.Result{}, fmt.Errorf("duration is 1 to %d milliseconds", maxHold)
	}
	return m.tapAt(ctx, a.pointArgs, 0, time.Duration(d)*time.Millisecond, fmt.Sprintf("held for %d ms at", d))
}

func (m *Manager) drag(ctx context.Context, call mcp.Call) (mcp.Result, error) {
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
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	from, err := m.pointOf(ctx, dev, a.FromRef, a.X1, a.Y1)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("the start of the drag: %w", err)
	}
	to, err := m.pointOf(ctx, dev, a.ToRef, a.X2, a.Y2)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("the end of the drag: %w", err)
	}
	g := mobile.Gesture{From: from.Point, To: to.Point, Hold: time.Duration(a.Hold) * time.Millisecond, Move: dragDuration}
	if err := dev.Touch(ctx, g); err != nil {
		return mcp.Result{}, err
	}
	return mcp.TextResult(fmt.Sprintf("dragged from %s to %s", from.note(), to.note())), nil
}

// SwipePath gives the start and the end of a swipe on a screen of w by h pixels. The finger starts at
// (x, y), or at the center, moves in the direction (the finger, not the content), and covers distance
// pixels, or 30% of the side that it moves along. Both points are kept 5% inside the edges. It fails when
// the direction is not known, or when there is no room to move.
func SwipePath(w, h int, direction string, x, y *float64, distance float64) (from, to mobile.Point, err error) {
	if w <= 0 || h <= 0 {
		return from, to, errors.New("the size of the screen is not known")
	}
	mx, my := int(float64(w)*swipeMargin+0.5), int(float64(h)*swipeMargin+0.5)
	clamp := func(v, lo, hi int) int { return max(lo, min(v, hi)) }
	cx, cy := w/2, h/2
	if x != nil {
		cx = int(*x + 0.5)
	}
	if y != nil {
		cy = int(*y + 0.5)
	}
	cx, cy = clamp(cx, mx, w-1-mx), clamp(cy, my, h-1-my)
	dx, dy := 0, 0
	dist := func(side int) int {
		if distance > 0 {
			return int(distance + 0.5)
		}
		return int(float64(side)*defaultSwipe + 0.5)
	}
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "up":
		dy = -dist(h)
	case "down":
		dy = dist(h)
	case "left":
		dx = -dist(w)
	case "right":
		dx = dist(w)
	default:
		return from, to, fmt.Errorf("the direction %q is not up, down, left, or right", truncateText(direction, 20))
	}
	from = mobile.Point{X: cx, Y: cy}
	to = mobile.Point{X: clamp(cx+dx, mx, w-1-mx), Y: clamp(cy+dy, my, h-1-my)}
	if from == to {
		return from, to, errors.New("there is no room to swipe that way from there: start nearer the middle of the screen")
	}
	return from, to, nil
}

// screenDims gives the size of the screen as it is turned now.
func (m *Manager) screenDims(ctx context.Context, dev Device) (int, int, error) {
	if t, err := dev.Source(ctx); err == nil && t.Width > 0 && t.Height > 0 {
		return t.Width, t.Height, nil
	}
	info, err := dev.ScreenSize(ctx)
	if err != nil {
		return 0, 0, err
	}
	info = m.turned(ctx, dev, info)
	return info.Width, info.Height, nil
}

func (m *Manager) swipe(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Direction string   `json:"direction"`
		X         *float64 `json:"x"`
		Y         *float64 `json:"y"`
		Distance  float64  `json:"distance"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if a.Distance < 0 {
		return mcp.Result{}, errors.New("distance must be above 0")
	}
	for _, v := range []*float64{a.X, a.Y} {
		if v != nil && (*v != *v || *v < 0) {
			return mcp.Result{}, errors.New("x and y must be 0 or more")
		}
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	w, h, err := m.screenDims(ctx, dev)
	if err != nil {
		return mcp.Result{}, err
	}
	from, to, err := SwipePath(w, h, a.Direction, a.X, a.Y, a.Distance)
	if err != nil {
		return mcp.Result{}, err
	}
	if err := dev.Touch(ctx, mobile.Gesture{From: from, To: to, Move: swipeDuration}); err != nil {
		return mcp.Result{}, err
	}
	return mcp.TextResult(fmt.Sprintf("swiped %s from (%d,%d) to (%d,%d)", strings.ToLower(a.Direction), from.X, from.Y, to.X, to.Y)), nil
}

func (m *Manager) typeKeys(ctx context.Context, call mcp.Call) (mcp.Result, error) {
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
		return m.typeViaKeyboard(ctx, keyboardArgs{Text: a.Text, Submit: a.Submit, Ref: a.Ref, SecretEnv: a.SecretEnv, Replace: a.Replace})
	}
	text, secret := "", ""
	switch {
	case a.Text != nil && a.SecretEnv != "":
		return mcp.Result{}, errors.New("give text or secretEnv, not both")
	case a.SecretEnv != "":
		if err := mobilerec.CheckSecretEnv(a.SecretEnv); err != nil {
			return mcp.Result{}, err
		}
		v, ok := m.env(a.SecretEnv)
		if !ok || v == "" {
			return mcp.Result{}, fmt.Errorf("the environment variable %s is not set", a.SecretEnv)
		}
		text, secret = v, a.SecretEnv
		m.scrub.Add(v)
	case a.Text != nil:
		text = *a.Text
	default:
		return mcp.Result{}, errors.New("give the text to type, or secretEnv")
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	where := "the focused field"
	if strings.TrimSpace(a.Ref) != "" {
		t, err := m.resolveRef(ctx, dev, a.Ref)
		if err != nil {
			return mcp.Result{}, err
		}
		if err := dev.Touch(ctx, mobile.Gesture{From: t.Point, To: t.Point}); err != nil {
			return mcp.Result{}, err
		}
		where = t.Ref
	}
	if err := dev.TypeFocused(ctx, text, a.Replace); err != nil {
		return mcp.Result{}, err
	}
	if a.Submit {
		code, _ := android.ButtonKeycode("ENTER")
		if err := dev.Key(ctx, code); err != nil {
			return mcp.Result{}, fmt.Errorf("the text was typed, but Enter failed: %w", err)
		}
	}
	msg := fmt.Sprintf("typed %d characters in %s", len([]rune(text)), where)
	if secret != "" {
		msg += " from " + secret
	}
	if a.Submit {
		msg += ", then pressed Enter"
	}
	return mcp.TextResult(msg), nil
}

func (m *Manager) pressButton(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Button string `json:"button"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	code, ok := android.ButtonKeycode(a.Button)
	if !ok {
		return mcp.Result{}, fmt.Errorf("the button %q is not known: use HOME, BACK, ENTER, VOLUME_UP, VOLUME_DOWN, MENU, APP_SWITCH, TAB, DEL, FORWARD_DEL, SEARCH, ESCAPE, POWER, or DPAD_UP, DPAD_DOWN, DPAD_LEFT, DPAD_RIGHT, DPAD_CENTER", truncateText(a.Button, 30))
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	// BACK with the keyboard up only closes the keyboard: the screen stays where it is.
	closesKeyboard := ""
	if strings.EqualFold(strings.TrimSpace(a.Button), "BACK") {
		if pkg, shown, err := dev.InputMethod(ctx); err == nil && shown {
			closesKeyboard = pkg
		}
	}
	if err := dev.Key(ctx, code); err != nil {
		return mcp.Result{}, err
	}
	if code == 3 || code == 187 || (code == 4 && closesKeyboard == "") { // HOME, APP_SWITCH / RECENTS, or BACK
		m.trackIntentionalNav()
	}
	msg := "pressed " + strings.ToUpper(strings.TrimSpace(a.Button))
	if closesKeyboard != "" {
		msg += ": BACK closed the keyboard " + closesKeyboard + " and did not go back. Press BACK again to go back"
	}
	return mcp.TextResult(msg), nil
}

func (m *Manager) openURL(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if err := m.urls.Check(a.URL); err != nil {
		return mcp.Result{}, fmt.Errorf("the link is refused: %w", err)
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if err := dev.OpenURL(ctx, a.URL); err != nil {
		return mcp.Result{}, err
	}
	m.trackIntentionalNav()
	return mcp.TextResult("opened the link"), nil
}
