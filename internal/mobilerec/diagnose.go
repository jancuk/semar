package mobilerec

import (
	"errors"
	"fmt"

	"github.com/jancuk/semar/internal/domain"
)

// DiagnosisKind names a reason that a replay failed which is not in the steps.
type DiagnosisKind string

// DiagnosisStaleStart says that the first step after the launch failed on another screen than it was
// recorded on, and the first launch keeps the data of the app. The app may show a splash screen first, so
// the screen after the launch is evidence, not proof, and the message says so.
const DiagnosisStaleStart DiagnosisKind = "stale-start"

// Diagnosis explains a failed verify. The studio and the terminal read Kind to offer the fix, and show
// Message as it is.
type Diagnosis struct {
	Kind DiagnosisKind `json:"kind"`
	// Recorded is the screen that the failed step was recorded on, and Actual the screen that the replay
	// showed after the launch. Reset is the reset that the first launch has.
	Recorded string `json:"recorded"`
	Actual   string `json:"actual"`
	Reset    string `json:"reset"`
	Message  string `json:"message"`
	// NeedsConfirm is true when the fix deletes the data of a physical device, which a person must allow.
	NeedsConfirm bool `json:"needsConfirm,omitempty"`
}

// DiagnoseReplay says why the replay of the saved steps failed at the step failedStep, when the cause is the
// start and not the step. Call it after the verify has given the device back: the frame is then the start of
// the app with the reset of the service, which is where the replay began. It gives nil for any other failure.
func (s *Session) DiagnoseReplay(failedStep string) *Diagnosis {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frame == nil {
		return nil
	}
	d := diagnoseStart(s.events, s.o.Reset, failedStep, s.frame.Screen)
	if d == nil {
		return nil
	}
	d.NeedsConfirm = s.needsConfirmLocked()
	d.Message = s.ov.Scrub(startMessage(d))
	return d
}

// diagnoseStart decides the diagnosis from the steps. A replay that fails at the first step after the first
// launch, on another screen than the one that step was recorded on, started from data that the launch did
// not reset. A launch that clears cannot, so it is never the cause.
func diagnoseStart(events []*event, serviceReset, failedStep, actual string) *Diagnosis {
	if failedStep == "" || actual == "" || len(events) < 2 || events[0].do != domain.MobileLaunch {
		return nil
	}
	reset := events[0].reset
	if reset == "" {
		reset = serviceReset
	}
	if reset == "" {
		reset = "restart"
	}
	if reset == "clear" {
		return nil
	}
	for _, ev := range events[1:] {
		if ev.isRead() {
			continue
		}
		if ev.id != failedStep || ev.screen == "" || ev.screen == actual {
			return nil
		}
		return &Diagnosis{Kind: DiagnosisStaleStart, Recorded: ev.screen, Actual: actual, Reset: reset}
	}
	return nil
}

func startMessage(d *Diagnosis) string {
	return fmt.Sprintf("After the launch the app showed %s, and the step that failed was recorded on %s. The first launch resets with %s, which keeps the data of the app: when the recording changed it (a sign-in, say), a replay starts past the screen that the recording began on. Save with reset: clear on the first launch.", d.Actual, d.Recorded, d.Reset)
}

// ClearFirstLaunch sets the reset of the first launch to clear, so that the next save writes it. A physical
// device gives ErrNeedsConfirm until ConfirmClear: a clear deletes the data of the app there.
func (s *Session) ClearFirstLaunch() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 || s.events[0].do != domain.MobileLaunch {
		return errors.New("the recording does not start with the launch of the app, so there is no reset to set")
	}
	if s.needsConfirmLocked() {
		return ErrNeedsConfirm
	}
	s.events[0].reset = "clear"
	return nil
}

func (s *Session) needsConfirmLocked() bool {
	return s.device.Device != "" && !s.emulatorLocked() && !s.o.ConfirmClear
}
