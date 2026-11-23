package mobile

import "context"

// DeviceInfo is a device that the platform sees, before any app runs on it.
type DeviceInfo struct {
	Serial string
	Model  string
	// State is the state that the platform gave: device, offline, unauthorized.
	State string
	// Emulator tells an emulator from a physical device, which Semar uses only when it is named.
	Emulator bool
}

// AppInfo is the app that a device shows in front.
type AppInfo struct {
	// Package and Activity name the app that has the focus. Both are empty when no app of a person has it.
	Package  string
	Activity string
	// Candidates are the installed apps that are not part of the system, for a person to choose from when
	// no app is in front. They are never chosen for the person.
	Candidates []string
}

// Found reports whether an app of a person is in front.
func (a AppInfo) Found() bool { return a.Package != "" }

// AppInspector tells a person which devices and which apps there are, so that a guided setup can ask for
// the right ones. It works before the platform has a driver: it needs no server on the device.
type AppInspector interface {
	ListDevices(ctx context.Context) ([]DeviceInfo, error)
	// InspectForeground reads the app in front on the device with the serial.
	InspectForeground(ctx context.Context, serial string) (AppInfo, error)
}
