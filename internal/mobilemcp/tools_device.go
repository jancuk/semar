package mobilemcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/takeover"
)

// maxClipboardText bounds the clipboard text that a tool gives back.
const maxClipboardText = 64 << 10

func (m *Manager) deviceTools() []*toolDef {
	return []*toolDef{
		{
			name: "mobile_list_available_devices", title: "List devices", readOnly: true,
			desc:   "List the Android devices and emulators that adb sees, with their serial, model, state, and whether each is an emulator. selected marks the one in use.",
			schema: mcp.Object(nil),
			run:    m.listDevices,
		},
		{
			name: "mobile_use_device", title: "Use a device",
			desc: "Choose the device that the other mobile tools act on, by its serial from mobile_list_available_devices. The device in use is released first. Naming a physical device is how you allow its use.",
			schema: mcp.Object(map[string]mcp.Schema{
				"device":     mcp.String("The serial of the device, such as emulator-5554."),
				"deviceType": mcp.String("Ignored. It exists for clients that send it."),
			}).Required("device"),
			run: m.useDevice,
		},
		{
			name: "mobile_get_screen_size", title: "Get the screen size", readOnly: true,
			desc:   "Get the size of the screen in pixels as it is turned now, and its density in dots per inch when known.",
			schema: mcp.Object(nil),
			run:    m.screenSize,
		},
		{
			name: "mobile_get_orientation", title: "Get the orientation", readOnly: true,
			desc:   "Get the orientation of the screen: portrait or landscape.",
			schema: mcp.Object(nil),
			run:    m.getOrientation,
		},
		{
			name: "mobile_set_orientation", title: "Set the orientation",
			desc: "Turn the screen to portrait or landscape. The rotation settings are put back when the server ends.",
			schema: mcp.Object(map[string]mcp.Schema{
				"orientation": mcp.Enum("The orientation to set.", "portrait", "landscape"),
			}).Required("orientation"),
			run: m.setOrientation,
		},
		{
			name: "mobile_set_theme", title: "Set the theme",
			desc: "Switch the device between light and dark theme. The prior theme is put back when the server ends.",
			schema: mcp.Object(map[string]mcp.Schema{
				"theme": mcp.Enum("The theme to set.", android.ThemeLight, android.ThemeDark),
			}).Required("theme"),
			run: m.setTheme,
		},
		{
			name: "mobile_set_font_scale", title: "Set the font scale",
			desc: "Set the system font scale between 0.5 and 3.0 to test large or small text. The prior font scale is put back when the server ends.",
			schema: mcp.Object(map[string]mcp.Schema{
				"scale": mcp.Number("The font scale factor, from 0.5 to 3.0.").With("minimum", android.MinFontScale).With("maximum", android.MaxFontScale),
			}).Required("scale"),
			run: m.setFontScale,
		},
		{
			name: "mobile_fold_device", title: "Fold the device",
			desc: "Put a foldable device or emulator in a posture. The state is reset when the server ends.",
			schema: mcp.Object(map[string]mcp.Schema{
				"posture": mcp.Enum("The posture to set.", "folded", "half-open", "open"),
			}).Required("posture"),
			run: m.foldDevice,
		},
		{
			name: "mobile_set_location", title: "Set the location",
			desc: "Put the device at a latitude and longitude. An emulator takes it; a physical device needs a mock location app, which this cannot set up. Give neither value to clear it.",
			schema: mcp.Object(map[string]mcp.Schema{
				"latitude":  mcp.Number("Latitude in degrees, -90 to 90.").With("minimum", -90).With("maximum", 90),
				"longitude": mcp.Number("Longitude in degrees, -180 to 180.").With("minimum", -180).With("maximum", 180),
			}),
			run: m.setLocation,
		},
		{
			name: "mobile_clipboard", title: "Read or set the clipboard", readOnly: true,
			desc: "Read the text on the clipboard of the device, or set it when text is given. What is read is scrubbed of secrets and cut at 64 KiB. Reading works while the server only observes, while a person drives, and during a recording. Setting is a write: it is refused in those three.",
			schema: mcp.Object(map[string]mcp.Schema{
				"text": mcp.String("The text to put on the clipboard. Leave it out to read the clipboard."),
			}),
			run: m.clipboard,
		},
	}
}

type deviceRow struct {
	Serial   string `json:"serial"`
	Model    string `json:"model,omitempty"`
	State    string `json:"state"`
	Emulator bool   `json:"emulator"`
	Selected bool   `json:"selected,omitempty"`
}

func (m *Manager) listDevices(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	if err := noArgs(call); err != nil {
		return mcp.Result{}, err
	}
	rows := []deviceRow{}
	if m.inspector != nil {
		infos, err := m.inspector.ListDevices(ctx)
		if err != nil {
			return mcp.Result{}, fmt.Errorf("the devices could not be listed: %w", err)
		}
		for _, d := range infos {
			rows = append(rows, deviceRow{Serial: d.Serial, Model: d.Model, State: d.State, Emulator: d.Emulator, Selected: d.Serial != "" && d.Serial == m.currentSerial()})
		}
	} else if m.dev != nil {
		rows = append(rows, deviceRow{Serial: m.dev.Serial(), State: "device", Emulator: m.dev.Emulator(), Selected: true})
	} else if m.serial != "" {
		rows = append(rows, deviceRow{Serial: m.serial, State: "configured"})
	}
	return textJSON(map[string]any{"devices": rows})
}

// currentSerial is the serial of the device in use, or the one that was asked for.
func (m *Manager) currentSerial() string {
	if m.dev != nil && m.dev.Serial() != "" {
		return m.dev.Serial()
	}
	return m.serial
}

func (m *Manager) useDevice(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Device     string `json:"device"`
		DeviceType string `json:"deviceType"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	serial := strings.TrimSpace(a.Device)
	if serial == "" {
		return mcp.Result{}, errors.New("device needs a serial, from mobile_list_available_devices")
	}
	if m.mode != modeLive {
		return mcp.Result{}, errors.New("a recording is going on: end it before you change the device")
	}
	if m.recOut != "" {
		return mcp.Result{}, errors.New("a screen recording is going on: stop it with mobile_stop_screen_recording before you change the device")
	}
	oldSerial := m.serial
	if err := m.closeLocked(ctx); err != nil {
		return mcp.Result{}, fmt.Errorf("the device in use could not be released: %w", err)
	}
	m.serial = serial
	dev, err := m.device(ctx)
	if err != nil {
		m.serial = oldSerial
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{"serial": dev.Serial(), "emulator": dev.Emulator(), "remote": dev.Remote()})
}

func (m *Manager) screenSize(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	if err := noArgs(call); err != nil {
		return mcp.Result{}, err
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	info, err := dev.ScreenSize(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	info = m.turned(ctx, dev, info)
	return textJSON(map[string]any{"width": info.Width, "height": info.Height, "density": info.Density})
}

// turned gives the size as the screen is turned now: wm size reports the natural size, so when the screen
// is turned the width and the height are swapped. The shape of the screen that the device lists says how it
// is turned, which is also right on a tablet that is wide by nature. With no listing, the orientation that
// the device reports decides, and a device that reports none keeps the size as it is.
func (m *Manager) turned(ctx context.Context, dev Device, info android.ScreenInfo) android.ScreenInfo {
	wide := false
	if t, err := dev.Source(ctx); err == nil && t != nil && t.Width > 0 && t.Height > 0 {
		wide = t.Width > t.Height
	} else if o, err := dev.Orientation(ctx); err == nil {
		wide = o == "landscape"
	} else {
		return info
	}
	if wide != (info.Width > info.Height) {
		info.Width, info.Height = info.Height, info.Width
	}
	return info
}

func (m *Manager) getOrientation(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	if err := noArgs(call); err != nil {
		return mcp.Result{}, err
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	o, err := dev.Orientation(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{"orientation": o})
}

func (m *Manager) setOrientation(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Orientation string `json:"orientation"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	o := strings.ToLower(strings.TrimSpace(a.Orientation))
	if o != "portrait" && o != "landscape" {
		return mcp.Result{}, errors.New("orientation must be portrait or landscape")
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if err := dev.SetOrientation(ctx, o); err != nil {
		return mcp.Result{}, err
	}
	m.refs.Reset("", nil)
	return textJSON(map[string]any{"orientation": o})
}

func (m *Manager) setTheme(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Theme string `json:"theme"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	theme := strings.ToLower(strings.TrimSpace(a.Theme))
	if theme != android.ThemeLight && theme != android.ThemeDark {
		return mcp.Result{}, fmt.Errorf("theme must be %s or %s", android.ThemeLight, android.ThemeDark)
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	prev, _ := dev.Theme(ctx)
	if err := dev.SetTheme(ctx, theme); err != nil {
		return mcp.Result{}, err
	}
	m.refs.Reset("", nil)
	out := map[string]any{"theme": theme}
	if prev != "" {
		out["previous"] = prev
	}
	return textJSON(out)
}

func (m *Manager) setFontScale(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Scale *float64 `json:"scale"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if a.Scale == nil {
		return mcp.Result{}, fmt.Errorf("scale is required and must be from %.1f to %.1f", android.MinFontScale, android.MaxFontScale)
	}
	scale := *a.Scale
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale < android.MinFontScale || scale > android.MaxFontScale {
		return mcp.Result{}, fmt.Errorf("scale must be from %.1f to %.1f", android.MinFontScale, android.MaxFontScale)
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	prev, prevErr := dev.FontScale(ctx)
	if err := dev.SetFontScale(ctx, scale); err != nil {
		return mcp.Result{}, err
	}
	m.refs.Reset("", nil)
	out := map[string]any{"fontScale": scale}
	if prevErr == nil {
		out["previous"] = prev
	}
	return textJSON(out)
}

func (m *Manager) foldDevice(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Posture string   `json:"posture"`
		Angle   *float64 `json:"angle"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if a.Angle != nil {
		return mcp.Result{}, errors.New("angle is for iOS only: on Android give a posture of folded, half-open, or open")
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if err := dev.Fold(ctx, a.Posture); err != nil {
		return mcp.Result{}, err
	}
	m.refs.Reset("", nil)
	return textJSON(map[string]any{"posture": strings.ToLower(strings.TrimSpace(a.Posture))})
}

func (m *Manager) setLocation(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if (a.Latitude == nil) != (a.Longitude == nil) {
		return mcp.Result{}, errors.New("give both latitude and longitude, or neither to clear the location")
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if a.Latitude == nil {
		if err := dev.ClearLocation(ctx); err != nil {
			return mcp.Result{}, err
		}
		return textJSON(map[string]any{"location": "cleared"})
	}
	lat, lon := *a.Latitude, *a.Longitude
	if lat != lat || lon != lon || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return mcp.Result{}, errors.New("latitude is -90 to 90 and longitude is -180 to 180")
	}
	if err := dev.SetLocation(ctx, lat, lon); err != nil {
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{"latitude": lat, "longitude": lon})
}

func (m *Manager) clipboard(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Text *string `json:"text"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if a.Text != nil {
		// The tool is read-only for the guard and the baton, so that a read passes them; a write is checked
		// here, where the arguments are known.
		var leave func()
		var refusal *mcp.Result
		var err error
		ctx, leave, refusal, err = m.clipboardWriteGate(ctx)
		if err != nil || refusal != nil {
			if refusal != nil {
				return *refusal, nil
			}
			return mcp.Result{}, err
		}
		defer leave()
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if a.Text != nil {
		if err := dev.SetClipboard(ctx, *a.Text); err != nil {
			return mcp.Result{}, err
		}
		return textJSON(map[string]any{"set": true, "characters": len([]rune(*a.Text))})
	}
	text, err := dev.Clipboard(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	return m.clipboardRead(text)
}

// emptyClipboardNote says why a read that gave nothing may still be wrong.
const emptyClipboardNote = "the clipboard read back empty. On Android 10 (API 29) and later the device gives the clipboard only to the app in focus and the keyboard, so this read can be empty although the clipboard holds text. To see what was copied, paste it into a field and list the screen."

// clipboardRead is the result of a read: scrubbed, and cut at maxClipboardText.
func (m *Manager) clipboardRead(text string) (mcp.Result, error) {
	cut := false
	if len(text) > maxClipboardText {
		text, cut = strings.ToValidUTF8(text[:maxClipboardText], ""), true
	}
	out := map[string]any{"text": m.scrub.Text(text), "truncated": cut}
	if text == "" {
		out["note"] = emptyClipboardNote
	}
	return textJSON(out)
}

// clipboardWriteGate lets a write of the clipboard through only when the guard allows a write and a person
// does not hold the baton. leave must be called when the write is over.
func (m *Manager) clipboardWriteGate(ctx context.Context) (ctx2 context.Context, leave func(), refusal *mcp.Result, err error) {
	if err := m.guard.Allow(mcpkit.SurfaceMobile, true); err != nil {
		return ctx, nil, nil, err
	}
	if m.baton == nil {
		return ctx, func() {}, nil, nil
	}
	ctx2, done, enterErr := m.baton.Enter(ctx, takeover.Agent, true)
	if enterErr != nil {
		var he *takeover.ErrHumanInControl
		if errors.As(enterErr, &he) {
			res := takeover.HumanInControl(he)
			return ctx, nil, &res, nil
		}
		return ctx, nil, nil, enterErr
	}
	return ctx2, func() { done(true, "") }, nil, nil
}
