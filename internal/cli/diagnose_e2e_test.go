package cli_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
)

// brokenSubmit makes the screens file name a button that the screen does not have, the way a product
// change does: the button is now a link, which the fake device does not show.
func brokenSubmit(t *testing.T, r *androidRig, locator string) {
	t.Helper()
	r.edit(t, ".semar/screens/shop-app.yaml", func(s string) string {
		return strings.Replace(s, "submit: { desc: Sign in }", "submit: "+locator, 1)
	})
}

func TestAFailingRunNamesTheClosestElementsAndTheRepairCommand(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	brokenSubmit(t, r, "{ role: link, name: Sign in }")
	res := r.must(t, cli.ExitUndecided, "run", "--scenario", "AND-1")
	id := filepath.Base(r.latestRun(t))
	contains(t, "stdout", res.stdout,
		"elements not found: 2",
		"AND-1 step sign-in: SignIn.submit was not found (looked for { role: link, name: \"Sign in\" })",
		"closest on the screen:",
		"semar screens repair "+id+" --apply SignIn.submit=1")

	md := r.lastReport(t)
	contains(t, "report.md", md, "## Elements not found", "SignIn.submit", "Closest:", "semar screens repair "+id+" --apply SignIn.submit=1")
	html, err := os.ReadFile(filepath.Join(r.latestRun(t), "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "report.html", string(html), "<h2>Elements not found</h2>", "SignIn.submit")
}

func TestAMissWithNoCandidateStillShowsTheScreenAndScreensRepairListsTheClosest(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	brokenSubmit(t, r, "{ desc: Totally different }")
	res := r.must(t, cli.ExitUndecided, "run", "--scenario", "AND-1")
	id := filepath.Base(r.latestRun(t))
	contains(t, "stdout", res.stdout, "elements not found: 2", "closest on the screen:", "to see the screen: semar screens repair "+id)
	if strings.Contains(res.stdout, "--apply") {
		t.Errorf("a miss with no candidate offers --apply:\n%s", res.stdout)
	}
	contains(t, "report.md", r.lastReport(t), "Run `semar screens repair "+id+"`")

	repair := r.must(t, cli.ExitOK, "screens", "repair", "latest")
	contains(t, "screens repair", repair.stdout, "SignIn.submit was not found", "no candidate that finds one element", "The closest elements on the screen were:", `button "Sign in"`)
}

func TestAPassingRunPrintsNoMisses(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	res := r.must(t, cli.ExitOK, "run", "--scenario", "AND-1")
	if strings.Contains(res.stdout, "elements not found") || strings.Contains(r.lastReport(t), "Elements not found") {
		t.Errorf("a run that found everything reports a miss:\n%s", res.stdout)
	}
}

func TestPauseOnFailIsRefusedWithCIAndWithNoTerminal(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	r.opts.Terminal = func() bool { return true }
	res := r.semar(t, t.Context(), "run", "--scenario", "AND-1", "--pause-on-fail", "--ci")
	if res.code == cli.ExitOK || !strings.Contains(res.stderr, "--pause-on-fail") || !strings.Contains(res.stderr, "--ci") {
		t.Errorf("--pause-on-fail --ci: exit %d, stderr %q", res.code, res.stderr)
	}
	r.opts.Terminal = func() bool { return false }
	res = r.semar(t, t.Context(), "run", "--scenario", "AND-1", "--pause-on-fail")
	if res.code == cli.ExitOK || !strings.Contains(res.stderr, "needs a terminal") {
		t.Errorf("--pause-on-fail with no terminal: exit %d, stderr %q", res.code, res.stderr)
	}
	if len(r.runDirsOrNone()) != 0 {
		t.Error("a refused run left a run directory")
	}
}

func (r *repo) runDirsOrNone() []string {
	entries, err := os.ReadDir(r.path(".semar", "runs"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// pausedRun starts a run that fails on a device step and reads its answers from a pipe, so that a test
// decides when the person presses Enter.
func pausedRun(t *testing.T, ctx context.Context) (*androidRig, *io.PipeWriter, <-chan result) {
	t.Helper()
	r := newAndroidRig(t, androidOptions{})
	brokenSubmit(t, r, "{ role: link, name: Sign in }")
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	r.opts.Stdin = pr
	r.opts.Terminal = func() bool { return true }
	done := make(chan result, 1)
	go func() { done <- r.semar(t, ctx, "run", "--scenario", "AND-1", "--pause-on-fail") }()
	return r, pw, done
}

func TestPauseOnFailWaitsForEnterThenGoesOn(t *testing.T) {
	_, pw, done := pausedRun(t, t.Context())
	select {
	case res := <-done:
		t.Fatalf("the run did not wait for Enter:\n%s", res.stdout)
	case <-time.After(500 * time.Millisecond):
	}
	if _, err := pw.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		if res.code != cli.ExitUndecided {
			t.Errorf("exit %d, want %d: the pause must not change the outcome\n%s", res.code, cli.ExitUndecided, res.stderr)
		}
		contains(t, "stdout", res.stdout,
			"paused at scenario AND-1 step sign-in",
			"the step did not finish: an element was not found",
			"closest elements on the screen:",
			"The device stays on this screen. Press Enter to go on.",
			"AND-1  inconclusive")
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not go on after Enter")
	}
}

func TestPauseOnFailEndsOnCtrlC(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, _, done := pausedRun(t, ctx)
	select {
	case <-done:
		t.Fatal("the run did not wait for Enter")
	case <-time.After(500 * time.Millisecond):
	}
	cancel()
	select {
	case res := <-done:
		if res.code == cli.ExitOK {
			t.Errorf("an interrupted run exits 0:\n%s", res.stdout)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Ctrl+C did not end a paused run")
	}
}

func TestAnAndroidScenarioThatPassesNeverPauses(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	r.opts.Stdin = strings.NewReader("")
	r.opts.Terminal = func() bool { return true }
	res := r.must(t, cli.ExitOK, "run", "--scenario", "AND-1", "--pause-on-fail")
	if strings.Contains(res.stdout, "paused at") {
		t.Errorf("a passing run paused:\n%s", res.stdout)
	}
}
