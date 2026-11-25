package android

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/jancuk/semar/internal/fixture"
)

// busyDevice is the reason when another process holds the lease of the device. It names the holder when
// the lease file does, which tells a person that the holder is their own semar record --ui.
func busyDevice(serial string, h *fixture.Holder) string {
	generic := fmt.Sprintf("the device %s is used by another semar run: wait for it to end, or use another device", serial)
	if h == nil {
		return generic
	}
	// Anyone can write the lease file, so what it says is cleaned before it is shown.
	cmd := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, h.Command)
	if cmd == "" {
		return generic
	}
	return fmt.Sprintf("the device %s is used by %s (pid %d, since %s): close it, wait for it to end, or use another device",
		serial, truncate(cmd, 60), h.PID, h.Since.Local().Format("15.04"))
}
