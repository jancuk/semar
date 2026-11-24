package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

// A saved scenario is reopened: its steps replay on the device up to --at, a new recording goes after
// them, and a save that changes nothing writes the same files.
func TestRecordMobileResumeAScenarioAtAStep(t *testing.T) {
	r, signed := recordRig(t)
	// The signed out screen: 1 email, 2 password, 3 the button. After it, the home screen: 1 the greeting.
	r.stdin("tap 3", "assert 1", "finish", "quit")
	res := r.must(t, cli.ExitOK, "record", "TASK-2", "--surface", "android")
	contains(t, "stdout", res.stdout, "Saved:", "TASK-2.feature", "Replayed once: passed.")
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "TASK", "TASK-2.feature"))
	if err != nil {
		t.Fatal(err)
	}
	screensBefore, err := os.ReadFile(r.path(".semar", "screens", "shop-app.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// Reopen to step 1, the launch: the device is signed out, and the steps after it are pending.
	signed.Store(false)
	r.server.SetSource(signedOutScreen())
	r.stdin("steps", "quit")
	res = r.must(t, cli.ExitOK, "record", "TASK-2", "--resume", "--at", "1")
	contains(t, "stdout", res.stdout, "Reopening the scenario TASK-2", "its 4 steps, and the first 1 are replayed",
		"--- recording goes here: the steps below are after the device")
	if strings.Contains(res.stdout, "Saved:") {
		t.Errorf("a reopen that saves nothing wrote:\n%s", res.stdout)
	}

	// Reopen all of it and save again with no change: the same files.
	signed.Store(false)
	r.server.SetSource(signedOutScreen())
	r.stdin("finish", "quit")
	res = r.must(t, cli.ExitOK, "record", "TASK-2", "--resume")
	contains(t, "stdout", res.stdout, "Saved:", "Replayed once: passed.")
	after, _ := os.ReadFile(r.path(".semar", "scenarios", "TASK", "TASK-2.feature"))
	if string(after) != string(feature) {
		t.Errorf("the feature changed:\n%s\nwas\n%s", after, feature)
	}
	screensAfter, _ := os.ReadFile(r.path(".semar", "screens", "shop-app.yaml"))
	if string(screensAfter) != string(screensBefore) {
		t.Errorf("the screens file changed:\n%s", screensAfter)
	}
	if strings.Contains(res.stdout, "exists already") {
		t.Errorf("a reopened save was refused:\n%s", res.stdout)
	}
}

func TestRecordMobileResumeWithNothingSavedFailsClearly(t *testing.T) {
	r, _ := recordRig(t)
	res := r.semar(t, t.Context(), "record", "NOPE-1", "--resume")
	if res.code == cli.ExitOK || !strings.Contains(res.stderr, "the scenario NOPE-1 is not saved") {
		t.Errorf("exit %d, stderr = %q", res.code, res.stderr)
	}
	res = r.semar(t, t.Context(), "record", "--flow", "nope", "--resume")
	if res.code == cli.ExitOK || !strings.Contains(res.stderr, "the flow nope is not saved") {
		t.Errorf("exit %d, stderr = %q", res.code, res.stderr)
	}
}

func TestRecordMobileResumeFlags(t *testing.T) {
	r, _ := recordRig(t)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"at without resume", []string{"record", "X-1", "--surface", "android", "--at", "2"}, "add --resume"},
		{"negative at", []string{"record", "X-1", "--resume", "--at", "-1"}, "counts from 1"},
		{"at with ui", []string{"record", "--ui", "--resume", "--at", "2"}, "the studio asks for the step"},
		{"resume with use", []string{"record", "X-1", "--resume", "--use", "login"}, "leave out --use"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := r.semar(t, t.Context(), c.args...)
			if res.code == cli.ExitOK || !strings.Contains(res.stderr, c.want) {
				t.Errorf("exit %d, stderr = %q, want %q", res.code, res.stderr, c.want)
			}
		})
	}
}
