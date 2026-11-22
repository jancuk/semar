package android

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
)

// Device is one line of adb devices -l.
type Device struct {
	Serial string
	// State is device, offline, unauthorized, or another word that adb prints.
	State string
	// Emulator is true for a serial that starts with emulator-.
	Emulator bool
	// Model is the model that adb knows, such as sdk_gphone64_arm64.
	Model string
}

// ParseDevices reads the output of adb devices -l. The lines that are not a device (the title, the
// notices of the daemon that starts) are skipped.
func ParseDevices(out string) []Device {
	var devices []Device
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "List" || strings.HasPrefix(f[0], "*") {
			continue
		}
		d := Device{Serial: f[0], State: f[1], Emulator: strings.HasPrefix(f[0], "emulator-")}
		for _, kv := range f[2:] {
			if v, ok := strings.CutPrefix(kv, "model:"); ok {
				d.Model = v
			}
		}
		devices = append(devices, d)
	}
	return devices
}

// ChooseDevice picks the device to run on. A serial that a person gave wins: ANDROID_SERIAL, else
// android.device. With none, it takes the one emulator that runs, and it never takes a physical device:
// a reset can delete the data of an app, and Semar must not do that on a phone that somebody uses. The
// reason of an *mobile.Unavailable says what to do.
func ChooseDevice(devices []Device, want string) (Device, *mobile.Unavailable) {
	if want != "" {
		for _, d := range devices {
			if d.Serial != want {
				continue
			}
			if reason := stateProblem(d); reason != "" {
				return Device{}, &mobile.Unavailable{Reason: reason}
			}
			return d, nil
		}
		return Device{}, &mobile.Unavailable{Reason: fmt.Sprintf("the device %s is not connected: adb lists %s", want, listSerials(devices))}
	}
	var emulators, physical []Device
	for _, d := range devices {
		if d.Emulator {
			emulators = append(emulators, d)
		} else {
			physical = append(physical, d)
		}
	}
	var running []Device
	for _, d := range emulators {
		if d.State == "device" {
			running = append(running, d)
		}
	}
	switch {
	case len(running) == 1:
		return running[0], nil
	case len(running) > 1:
		return Device{}, &mobile.Unavailable{Reason: fmt.Sprintf("%d emulators are running (%s): name the one to use with ANDROID_SERIAL or environments.<name>.android.device", len(running), listSerials(running))}
	case len(emulators) > 0:
		return Device{}, &mobile.Unavailable{Reason: fmt.Sprintf("the emulator %s is not ready (%s): wait until it has started", emulators[0].Serial, emulators[0].State)}
	case len(physical) > 0:
		return Device{}, &mobile.Unavailable{Reason: fmt.Sprintf("no emulator is running, and adb sees only physical devices (%s): Semar uses a physical device only when it is named, so a reset cannot delete the data of a phone by chance. Set ANDROID_SERIAL or environments.<name>.android.device, or start an emulator", listSerials(physical))}
	}
	return Device{}, &mobile.Unavailable{Reason: "no device is connected: start an emulator, or connect a device and name it with ANDROID_SERIAL"}
}

func stateProblem(d Device) string {
	switch d.State {
	case "device":
		return ""
	case "unauthorized":
		return fmt.Sprintf("the device %s has not allowed this computer: accept the USB debugging prompt on the device, then run again", d.Serial)
	case "offline":
		return fmt.Sprintf("the device %s is offline: reconnect it, or restart it, and run again", d.Serial)
	}
	return fmt.Sprintf("the device %s is not ready: adb says %q", d.Serial, d.State)
}

func listSerials(devices []Device) string {
	if len(devices) == 0 {
		return "no device"
	}
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Serial
	}
	return strings.Join(names, ", ")
}

// bootWait is how long a device that has not finished starting is waited for.
const bootWait = 30 * time.Second

// bootCompleted waits until the device says that it has started.
func (p *Platform) bootCompleted(ctx context.Context, a *adb) *mobile.Unavailable {
	deadline := p.o.Now().Add(bootWait)
	for {
		out, err := a.shell(ctx, "getprop", "sys.boot_completed")
		if err == nil && strings.TrimSpace(out) == "1" {
			return nil
		}
		if ctx.Err() != nil {
			return &mobile.Unavailable{Reason: "the run was interrupted while the device was started"}
		}
		if !p.o.Now().Before(deadline) {
			return &mobile.Unavailable{Reason: fmt.Sprintf("the device %s has not finished starting", a.serial)}
		}
		if err := p.o.Sleep(ctx, time.Second); err != nil {
			return &mobile.Unavailable{Reason: "the run was interrupted while the device was started"}
		}
	}
}
