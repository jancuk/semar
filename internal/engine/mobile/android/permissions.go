package android

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const backgroundLocation = "android.permission.ACCESS_BACKGROUND_LOCATION"

// ParseRuntimePermissions reads the permissions that permissions: all grants from the output of dumpsys
// package <pkg>: the entries of each "runtime permissions:" block, then the requested permissions that
// are not install permissions, which a device may list in no runtime block until the app asks for them.
// The background location permission comes last, because the device refuses it before the foreground ones.
func ParseRuntimePermissions(dump string) []string {
	var runtime, requested, install []string
	var kind string
	indent := 0
	for _, line := range strings.Split(dump, "\n") {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		depth := len(line) - len(strings.TrimLeft(line, " "))
		if kind != "" && depth <= indent {
			kind = ""
		}
		if kind == "" {
			switch text {
			case "runtime permissions:":
				kind = "runtime"
			case "requested permissions:":
				kind = "requested"
			case "install permissions:":
				kind = "install"
			}
			indent = depth
			continue
		}
		name, _, _ := strings.Cut(text, ":")
		if !permissionName(name) {
			continue
		}
		switch kind {
		case "runtime":
			runtime = append(runtime, name)
		case "requested":
			requested = append(requested, name)
		case "install":
			install = append(install, name)
		}
	}
	var out []string
	for _, name := range append(runtime, requested...) {
		if !slices.Contains(out, name) && !slices.Contains(install, name) {
			out = append(out, name)
		}
	}
	slices.SortStableFunc(out, func(a, b string) int {
		return boolInt(a == backgroundLocation) - boolInt(b == backgroundLocation)
	})
	return out
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// permissionName is true for a name such as android.permission.CAMERA. It keeps a word of the dumpsys
// prose out of the list, and a name that a shell would read as more than a word.
func permissionName(s string) bool {
	if !strings.Contains(s, ".") {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// notGrantable are the texts of pm grant for a permission that the app asks for and that no grant gives:
// an install or signature permission, an app op, or one that this device does not define.
var notGrantable = []string{"not a changeable permission type", "has not requested permission", "Unknown permission", "is not a runtime permission"}

func isNotGrantable(text string) bool {
	for _, t := range notGrantable {
		if strings.Contains(text, t) {
			return true
		}
	}
	return false
}

// requestedPermissions lists what permissions: all grants for the package on the device.
func (a *adb) requestedPermissions(ctx context.Context, pkg string) ([]string, error) {
	out, err := a.shell(ctx, "dumpsys", "package", pkg)
	if err != nil {
		return nil, fmt.Errorf("the permissions of %s could not be read: %w", pkg, err)
	}
	return ParseRuntimePermissions(out), nil
}

// grantPermissions grants each permission. With lenient, one that no grant gives is passed over, as
// permissions: all asks.
func (a *adb) grantPermissions(ctx context.Context, pkg string, perms []string, lenient bool) error {
	for _, perm := range perms {
		out, err := a.shell(ctx, "pm", "grant", pkg, perm)
		if err == nil && !containsFailure(out) {
			continue
		}
		// pm writes its refusal to stderr, which reaches here only inside err.
		if lenient && (isNotGrantable(out) || err != nil && isNotGrantable(err.Error())) {
			continue
		}
		if err == nil {
			err = errors.New(firstLine(out))
		}
		return fmt.Errorf("the permission %s could not be granted: %w", perm, err)
	}
	return nil
}
