package android

import (
	"strings"

	"github.com/jancuk/semar/internal/engine/mobile"
)

// Android key codes of the keys that a step can press.
const (
	keycodeHome   = 3
	keycodeBack   = 4
	keycodeTab    = 61
	keycodeEnter  = 66
	keycodeDelete = 67
	keycodeSearch = 84
)

// keycodes maps the keys of a mobile step to Android key codes. hide-keyboard sends back only when a
// keyboard is shown.
var keycodes = map[mobile.Key]int{
	mobile.KeyBack:   keycodeBack,
	mobile.KeyHome:   keycodeHome,
	mobile.KeyEnter:  keycodeEnter,
	mobile.KeySearch: keycodeSearch,
	mobile.KeyTab:    keycodeTab,
	mobile.KeyDelete: keycodeDelete,
}

// buttonKeycodes are the keys that a person or an agent may name: the Android names without the
// KEYCODE_ prefix, and the aliases that people use.
var buttonKeycodes = map[string]int{
	"HOME":        keycodeHome,
	"BACK":        keycodeBack,
	"ENTER":       keycodeEnter,
	"VOLUME_UP":   24,
	"VOLUME_DOWN": 25,
	"MENU":        82,
	"APP_SWITCH":  187,
	"RECENTS":     187,
	"TAB":         keycodeTab,
	"DEL":         keycodeDelete,
	"BACKSPACE":   keycodeDelete,
	"FORWARD_DEL": 112,
	"SEARCH":      keycodeSearch,
	"ESCAPE":      111,
	"POWER":       26,
	"DPAD_UP":     19,
	"DPAD_DOWN":   20,
	"DPAD_LEFT":   21,
	"DPAD_RIGHT":  22,
	"DPAD_CENTER": 23,
}

// ButtonKeycode gives the Android key code of a named button. The name is not case sensitive, a dash
// counts as an underscore, and a KEYCODE_ prefix is allowed.
func ButtonKeycode(name string) (int, bool) {
	n := strings.ToUpper(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "-", "_")
	n = strings.TrimPrefix(n, "KEYCODE_")
	code, ok := buttonKeycodes[n]
	return code, ok
}
