package mobilerec_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/mobilerec"
)

// recordEmailSignIn types an email that is no secret and taps Sign in, so the recording leaves the app
// signed in and nothing makes the save reset with clear.
func recordEmailSignIn(t *testing.T, s *mobilerec.Session) {
	t.Helper()
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("email"))), Value: "ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign in")))}); err != nil {
		t.Fatal(err)
	}
}

func failedStepOf(res flow.RunResult) string {
	for _, st := range res.Steps {
		if st.Action.Err != "" {
			return st.Name
		}
	}
	return ""
}

func TestDiagnoseReplayFindsAReplayThatBeganSignedIn(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	recordEmailSignIn(t, s)
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.read(".semar/flows/login.yaml"), "reset: clear") {
		t.Fatal("a recording with no secret should not reset with clear")
	}

	res := verifyFlow(t, r, s, "login")
	step := failedStepOf(res)
	if res.Status != flow.StatusFailed || step == "" {
		t.Fatalf("the replay from a signed in app should fail at a step: %+v", res)
	}
	d := s.DiagnoseReplay(step)
	if d == nil {
		t.Fatal("no diagnosis")
	}
	if d.Kind != mobilerec.DiagnosisStaleStart || d.Actual != "Home" || d.Recorded != "SignIn" || d.NeedsConfirm {
		t.Errorf("diagnosis = %+v", d)
	}
	for _, want := range []string{"After the launch the app showed Home, and the step that failed was recorded on SignIn", "resets with restart", "Save with reset: clear on the first launch."} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message lacks %q: %s", want, d.Message)
		}
	}

	if err := s.ClearFirstLaunch(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(mobilerec.SaveOptions{Replace: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.read(".semar/flows/login.yaml"), "reset: clear") {
		t.Errorf("the fix did not reach the saved flow:\n%s", r.read(".semar/flows/login.yaml"))
	}
	if res = verifyFlow(t, r, s, "login"); res.Status != flow.StatusCompleted {
		t.Errorf("the replay after the fix = %+v", res)
	}
	if d := s.DiagnoseReplay(failedStepOf(res)); d != nil {
		t.Errorf("a launch that clears has no stale start: %+v", d)
	}
}

func TestDiagnoseReplayStaysSilentWhenTheStartIsNotTheCause(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	recordEmailSignIn(t, s)
	steps := s.Steps()
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}

	// The resume lands on Home: the app is signed in.
	r.signed = true
	if err := s.Suspend(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := s.DiagnoseReplay(""); d != nil {
		t.Errorf("no failed step, diagnosis = %+v", d)
	}
	if d := s.DiagnoseReplay("no-such-step"); d != nil {
		t.Errorf("a step that the recording lacks, diagnosis = %+v", d)
	}
	if d := s.DiagnoseReplay(steps[0].ID); d != nil {
		t.Errorf("the launch itself failed, diagnosis = %+v", d)
	}
	if d := s.DiagnoseReplay(steps[2].ID); d != nil {
		t.Errorf("the second step after the launch failed, diagnosis = %+v", d)
	}

	// The replay began where the recording did: the element is missing for another reason.
	r.signed = false
	if err := s.Suspend(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := s.DiagnoseReplay(steps[1].ID); d != nil {
		t.Errorf("the replay began on the recorded screen, diagnosis = %+v", d)
	}
}

func TestDiagnoseReplayStaysSilentWhenTheFirstLaunchClears(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login", func(o *mobilerec.Options) { o.Reset = "clear" })
	recordEmailSignIn(t, s)
	steps := s.Steps()
	r.signed = true
	if err := s.Suspend(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := s.DiagnoseReplay(steps[1].ID); d != nil {
		t.Errorf("diagnosis = %+v", d)
	}
}

func TestClearFirstLaunchOnAPhysicalDeviceNeedsConfirm(t *testing.T) {
	r := newRig(t)
	r.d.Info.Device = "R58M123ABC"
	s := r.session(mobilerec.KindFlow, "login")
	recordEmailSignIn(t, s)
	r.signed = true
	if err := s.Suspend(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := s.DiagnoseReplay(s.Steps()[1].ID)
	if d == nil || !d.NeedsConfirm {
		t.Fatalf("diagnosis = %+v, want one that needs a confirm", d)
	}
	if err := s.ClearFirstLaunch(); !errors.Is(err, mobilerec.ErrNeedsConfirm) {
		t.Fatalf("ClearFirstLaunch = %v, want ErrNeedsConfirm", err)
	}
	s.ConfirmClear()
	if err := s.ClearFirstLaunch(); err != nil {
		t.Fatal(err)
	}
}

func TestREPLFixResetsTheFirstLaunchAndVerifiesAgain(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	verified := 0
	out := runREPL(t, r, s, "fix\ntype 2 ana@example.com\ntap 4\nfinish\nfix\nquit\n", func(rp *mobilerec.REPL) {
		rp.Verify = func(ctx context.Context, sv *mobilerec.Saved) (mobilerec.VerifyOutcome, error) {
			verified++
			res := verifyFlow(t, r, s, "login")
			return mobilerec.VerifyOutcome{Message: "The flow login " + res.Status + ".", FailedStep: failedStepOf(res)}, nil
		}
	})
	if verified != 2 {
		t.Errorf("verified %d times, want 2:\n%s", verified, out)
	}
	for _, want := range []string{
		"There is nothing to fix",
		"After the launch the app showed Home, and the step that failed was recorded on SignIn",
		"Next: fix does that and verifies again",
		"The flow login completed.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(r.read(".semar/flows/login.yaml"), "reset: clear") {
		t.Errorf("the flow was not saved with the fix:\n%s", r.read(".semar/flows/login.yaml"))
	}
}
