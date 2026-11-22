package android

import "github.com/jancuk/semar/internal/engine/mobile"

// Android key codes of the keys that a step can press.
const (
	keycodeHome   = 3
	keycodeBack   = 4
	keycodeTab    = 61
	keycodeEnter  = 66
	keycodeDelete = 67
	keycodeSearch = 84
	// keycodeEscape closes a keyboard that is open.
	keycodeEscape = 111
)

// keycodes maps the keys of a mobile step to Android key codes. hide-keyboard has none: it is the escape
// key, and only when a keyboard is shown.
var keycodes = map[mobile.Key]int{
	mobile.KeyBack:   keycodeBack,
	mobile.KeyHome:   keycodeHome,
	mobile.KeyEnter:  keycodeEnter,
	mobile.KeySearch: keycodeSearch,
	mobile.KeyTab:    keycodeTab,
	mobile.KeyDelete: keycodeDelete,
}
