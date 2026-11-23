// Package mobilerec records what a person does in an app on a device, through the engine that a replay
// uses, and writes it as a flow or as a scenario. A Session is the one API that the terminal and a
// local studio share. The package knows no platform: it imports internal/engine/mobile, and never adb.
package mobilerec

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/jancuk/semar/internal/record"
)

// CheckSecretEnv checks that name is a valid secret environment variable name and does not shadow a
// host, device, or tool variable such as ANDROID_SERIAL, PATH, or HOME.
func CheckSecretEnv(name string) error {
	if err := record.CheckSecretEnv(name); err != nil {
		return err
	}
	upper := strings.ToUpper(name)
	for _, exact := range []string{"PATH", "HOME", "SHELL", "USER", "LOGNAME", "TMPDIR", "TEMP", "TMP", "JAVA_HOME", "GOPATH", "GOROOT", "GOCACHE", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"} {
		if upper == exact {
			return fmt.Errorf("%q is a reserved environment variable and cannot hold a recorded secret", name)
		}
	}
	for _, prefix := range []string{"ANDROID_", "SEMAR_", "XDG_", "LD_", "DYLD_", "CLAUDE_", "ANTHROPIC_", "OPENAI_"} {
		if strings.HasPrefix(upper, prefix) {
			return fmt.Errorf("%q is a reserved environment variable and cannot hold a recorded secret", name)
		}
	}
	return nil
}

// Redacted is what a secret value is replaced with in anything that a person or a file may see.
const Redacted = "***"

// SerialEnv is the variable that names the device that a run uses. The overlay pins it, so that a verify
// uses the device that the recording used.
const SerialEnv = "ANDROID_SERIAL"

// LookupEnv reads one environment variable, as os.LookupEnv does.
type LookupEnv func(name string) (string, bool)

// Overlay is the one in-memory layer over the environment of a recording session. It holds the secrets
// that the person typed, and the serial of the device, and it is passed to the engine, the redactor and
// the verify, so that env:NAME resolves in all of them. It is never written anywhere. Go strings are
// immutable, so the overlay cannot wipe a value from memory: its promise is that the value is in no
// file, no log, no frame, and no error message, and that Clear drops every reference to it.
type Overlay struct {
	mu      sync.Mutex
	base    LookupEnv
	secrets map[string]string
	pins    map[string]string
}

// NewOverlay layers over base. A nil base reads nothing.
func NewOverlay(base LookupEnv) *Overlay {
	return &Overlay{base: base, secrets: map[string]string{}, pins: map[string]string{}}
}

// Lookup reads a pin first, then a session secret, and then from the environment under it. Pins win so a
// secret can never override a pinned device serial.
func (o *Overlay) Lookup(name string) (string, bool) {
	o.mu.Lock()
	if v, ok := o.pins[name]; ok {
		o.mu.Unlock()
		return v, true
	}
	if v, ok := o.secrets[name]; ok {
		o.mu.Unlock()
		return v, true
	}
	base := o.base
	o.mu.Unlock()
	if base == nil {
		return "", false
	}
	return base(name)
}

// SetSecret makes name hold a secret value for this session.
func (o *Overlay) SetSecret(name, value string) {
	o.mu.Lock()
	o.secrets[name] = value
	o.mu.Unlock()
}

// Pin makes name read a value that is no secret, such as the serial of the device.
func (o *Overlay) Pin(name, value string) {
	o.mu.Lock()
	o.pins[name] = value
	o.mu.Unlock()
}

// HasSecret reports whether name holds a secret of this session.
func (o *Overlay) HasSecret(name string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.secrets[name]
	return ok
}

// Secrets lists the secret values of the session, the longest first, so that scrubbing a value that
// holds another removes the longer one whole.
func (o *Overlay) Secrets() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []string
	for _, v := range o.secrets {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return out
}

// Scrub replaces every secret value in s with Redacted.
func (o *Overlay) Scrub(s string) string {
	if s == "" {
		return s
	}
	for _, v := range o.Secrets() {
		s = strings.ReplaceAll(s, v, Redacted)
	}
	return s
}

// Leaks reports whether s holds a secret value.
func (o *Overlay) Leaks(s string) bool {
	if s == "" {
		return false
	}
	for _, v := range o.Secrets() {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}

// Clear drops every secret and every pin.
func (o *Overlay) Clear() {
	o.mu.Lock()
	o.secrets = map[string]string{}
	o.pins = map[string]string{}
	o.mu.Unlock()
}
