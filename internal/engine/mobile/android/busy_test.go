package android_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/fixture"
)

func TestBusyDeviceNamesTheHolder(t *testing.T) {
	since := time.Date(2026, 11, 25, 11, 47, 0, 0, time.Local)
	for name, c := range map[string]struct {
		holder *fixture.Holder
		want   string
	}{
		"a holder": {
			&fixture.Holder{PID: 20938, Command: "semar record --ui", Since: since},
			"the device emulator-5554 is used by semar record --ui (pid 20938, since 11.47): close it, wait for it to end, or use another device",
		},
		"no holder":         {nil, "the device emulator-5554 is used by another semar run: wait for it to end, or use another device"},
		"an empty command":  {&fixture.Holder{PID: 1, Command: "\x1b\n"}, "the device emulator-5554 is used by another semar run: wait for it to end, or use another device"},
		"a hostile command": {&fixture.Holder{PID: 1, Command: "semar\x1b[2J\nrun", Since: since}, "the device emulator-5554 is used by semar[2Jrun (pid 1, since 11.47)"},
	} {
		if got := android.BusyDevice("emulator-5554", c.holder); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}
