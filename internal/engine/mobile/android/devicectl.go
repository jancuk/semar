package android

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// ScreenInfo is the size of the screen in pixels, and its density in dots per inch (0 when unknown).
type ScreenInfo struct {
	Width, Height, Density int
}

var (
	wmSizeLine    = regexp.MustCompile(`^(Physical|Override) size:\s*(\d{1,5})x(\d{1,5})\s*$`)
	wmDensityLine = regexp.MustCompile(`^(Physical|Override) density:\s*(\d{1,5})\s*$`)
)

// ParseWMSize reads the output of wm size. An override, which a person or a test set, wins over the
// physical size.
func ParseWMSize(out string) (w, h int, ok bool) {
	for _, l := range strings.Split(out, "\n") {
		m := wmSizeLine.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		pw, _ := strconv.Atoi(m[2])
		ph, _ := strconv.Atoi(m[3])
		if pw <= 0 || ph <= 0 {
			continue
		}
		if m[1] == "Override" {
			return pw, ph, true
		}
		w, h, ok = pw, ph, true
	}
	return w, h, ok
}

// ParseWMDensity reads the output of wm density, an override first.
func ParseWMDensity(out string) (dpi int, ok bool) {
	for _, l := range strings.Split(out, "\n") {
		m := wmDensityLine.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		d, _ := strconv.Atoi(m[2])
		if d <= 0 {
			continue
		}
		if m[1] == "Override" {
			return d, true
		}
		dpi, ok = d, true
	}
	return dpi, ok
}

// ScreenSize reads the size and the density of the screen. A local device is asked with wm, and when wm
// does not answer, the size of the tree of the screen is used. A server at serverUrl gives the window
// rectangle.
func (c *Controller) ScreenSize(ctx context.Context) (ScreenInfo, error) {
	if c.Remote() {
		u, err := c.ui(ctx)
		if err != nil {
			return ScreenInfo{}, err
		}
		var rect struct {
			Width  float64 `json:"width"`
			Height float64 `json:"height"`
		}
		if err := u.call(ctx, http.MethodGet, "/window/rect", nil, &rect, false); err != nil {
			return ScreenInfo{}, err
		}
		if rect.Width <= 0 || rect.Height <= 0 {
			return ScreenInfo{}, errors.New("the server gave no size")
		}
		return ScreenInfo{Width: int(rect.Width), Height: int(rect.Height)}, nil
	}
	a, err := c.adb()
	if err != nil {
		return ScreenInfo{}, err
	}
	var info ScreenInfo
	if out, err := a.shell(ctx, "wm", "size"); err == nil {
		info.Width, info.Height, _ = ParseWMSize(out)
	}
	if out, err := a.shell(ctx, "wm", "density"); err == nil {
		info.Density, _ = ParseWMDensity(out)
	}
	if info.Width == 0 {
		tree, err := c.Source(ctx)
		if err != nil {
			return ScreenInfo{}, fmt.Errorf("the size of the screen could not be read: %w", err)
		}
		info.Width, info.Height = tree.Width, tree.Height
	}
	return info, nil
}

// DeviceState is one state that a foldable device lists.
type DeviceState struct {
	ID   int
	Name string
}

var deviceStateExpr = regexp.MustCompile(`identifier=(\d{1,6}),\s*name='?([A-Za-z0-9_ -]{1,40}?)'?\s*(?:,|\}|$)`)

// ParseDeviceStates reads the output of cmd device_state print-states, which lists the states with an
// identifier and a name, such as DeviceState{identifier=1, name='HALF_OPENED', ...}. It gives none for a
// device that has no states.
func ParseDeviceStates(out string) []DeviceState {
	var states []DeviceState
	seen := map[int]bool{}
	for _, l := range strings.Split(out, "\n") {
		for _, m := range deviceStateExpr.FindAllStringSubmatch(l, -1) {
			id, err := strconv.Atoi(m[1])
			name := strings.TrimSpace(m[2])
			if err != nil || name == "" || seen[id] {
				continue
			}
			seen[id] = true
			states = append(states, DeviceState{ID: id, Name: name})
		}
	}
	return states
}

// The postures of Fold, and the names that devices give them.
var postureNames = map[string][]string{
	"folded":    {"CLOSED", "FOLDED"},
	"half-open": {"HALF_OPENED", "HALF_OPEN", "HALF_FOLDED", "CONCURRENT_INNER_DEFAULT"},
	"open":      {"OPENED", "OPEN", "UNFOLDED"},
}

// StateFor picks the state of a posture, folded, half-open, or open, by the names that devices use.
func StateFor(states []DeviceState, posture string) (DeviceState, bool) {
	for _, want := range postureNames[posture] {
		for _, s := range states {
			if strings.EqualFold(strings.ReplaceAll(s.Name, " ", "_"), want) {
				return s, true
			}
		}
	}
	return DeviceState{}, false
}

// Fold puts a foldable device in a posture: folded, half-open, or open. A device with no fold states gives
// ErrUnsupported. Platform.Close resets the state.
func (c *Controller) Fold(ctx context.Context, posture string) error {
	posture = strings.ToLower(strings.TrimSpace(posture))
	if _, ok := postureNames[posture]; !ok {
		return fmt.Errorf("the posture %q is not folded, half-open, or open", truncate(posture, 40))
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	out, err := a.shell(ctx, "cmd", "device_state", "print-states")
	states := ParseDeviceStates(out)
	if len(states) == 0 {
		if err != nil && !isExit(err) {
			return fmt.Errorf("the states of the device could not be read: %w", err)
		}
		return unsupportedf("the device lists no fold states")
	}
	state, ok := StateFor(states, posture)
	if !ok {
		return unsupportedf("the device has no state for %s (it has %s)", posture, stateNames(states))
	}
	c.mu.Lock()
	c.folded = true
	c.mu.Unlock()
	out, err = a.shell(ctx, "cmd", "device_state", "state", strconv.Itoa(state.ID))
	if err != nil || containsFailure(out) {
		if err == nil {
			err = errors.New(firstLine(out))
		}
		return fmt.Errorf("the posture could not be set: %w", err)
	}
	return nil
}

func stateNames(states []DeviceState) string {
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

// SetLocation puts the device at a place. An emulator takes adb emu geo fix. A physical device needs an
// app that is set as the mock location provider, which adb cannot do, so it gives ErrUnsupported. A
// server at serverUrl takes the location command of WebDriver.
func (c *Controller) SetLocation(ctx context.Context, lat, lon float64) error {
	if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return errors.New("the location is outside latitude -90..90 and longitude -180..180")
	}
	if c.Remote() {
		u, err := c.ui(ctx)
		if err != nil {
			return err
		}
		return u.call(ctx, http.MethodPost, "/location", map[string]any{"location": map[string]any{"latitude": lat, "longitude": lon, "altitude": 0}}, nil, false)
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	if !c.Emulator() {
		return unsupportedf("a physical device needs an app that is its mock location provider: adb cannot set the location")
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }
	if out, err := a.out(ctx, "emu", "geo", "fix", f(lon), f(lat)); err != nil {
		return fmt.Errorf("the location could not be set: %w", err)
	} else if strings.Contains(strings.ToLower(out), "ko:") {
		return fmt.Errorf("the emulator refused the location: %s", firstLine(out))
	}
	return nil
}

// ClearLocation takes back a location. The emulator cannot give it up through adb: set another place,
// or restart the emulator. So it gives ErrUnsupported on a local device, and clears nothing.
func (c *Controller) ClearLocation(ctx context.Context) error {
	if c.Remote() {
		return unsupportedf("the server at serverUrl has no way to clear a location")
	}
	if _, err := c.adb(); err != nil {
		return err
	}
	return unsupportedf("adb cannot clear a location override: set another place with SetLocation, or restart the emulator")
}

// maxURL bounds a link that OpenURL sends.
const maxURL = 2048

var schemeExpr = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// OpenURL opens a link with whatever handles it on the device: the browser, or an app. It sets no
// package.
func (c *Controller) OpenURL(ctx context.Context, link string) error {
	switch {
	case link == "" || len(link) > maxURL:
		return fmt.Errorf("a link is 1 to %d characters", maxURL)
	case !schemeExpr.MatchString(link):
		return errors.New("the link has no scheme, such as https:")
	}
	for _, r := range link {
		if r < 0x20 || r == 0x7f {
			return errors.New("the link holds a control character")
		}
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	c.InvalidateInputMethod()
	out, err := a.shell(ctx, "am", "start", "-W", "-a", "android.intent.action.VIEW", "-d", link)
	return startResult("the link could not be opened", out, err)
}
