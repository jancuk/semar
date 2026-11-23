package mobilerec_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/screens"
)

func TestOverlay(t *testing.T) {
	base := func(name string) (string, bool) { return "from-env-" + name, name != "MISSING" }
	o := mobilerec.NewOverlay(base)
	if v, _ := o.Lookup("A"); v != "from-env-A" {
		t.Errorf("base lookup = %q", v)
	}
	o.SetSecret("A", "short-secret")
	o.SetSecret("B", "short-secret-and-longer")
	o.Pin(mobilerec.SerialEnv, "emulator-5554")
	if v, _ := o.Lookup("A"); v != "short-secret" {
		t.Errorf("overlay lookup = %q", v)
	}
	if v, _ := o.Lookup(mobilerec.SerialEnv); v != "emulator-5554" {
		t.Errorf("pinned = %q", v)
	}
	if _, ok := o.Lookup("MISSING"); ok {
		t.Error("a missing variable was found")
	}
	if got := o.Scrub("x short-secret-and-longer y short-secret z"); got != "x *** y *** z" {
		t.Errorf("scrub = %q: the longer secret must go whole", got)
	}
	if o.Leaks("emulator-5554") {
		t.Error("a pin is no secret")
	}
	o.Clear()
	if o.Leaks("short-secret") || o.HasSecret("A") {
		t.Error("Clear kept a secret")
	}
	if v, _ := o.Lookup(mobilerec.SerialEnv); v != "from-env-"+mobilerec.SerialEnv {
		t.Errorf("Clear kept a pin: %q", v)
	}
}

// showPasswordRig has a button that shows the password in plain text, as an app does.
func showPasswordRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	shown := false
	build := func() *mobile.Tree {
		pw := field("password", "Password", 500, !shown)
		if shown {
			pw.Text, pw.Attrs["text"] = canary, canary
		}
		return screen(heading("Sign in"), field("email", "Email", 300, false), pw, button("Show password", 100, 700), button("Sign in", 100, 900))
	}
	r.d.Show(build())
	r.d.OnLaunch = func() { shown = false; r.d.Show(build()) }
	r.d.OnReset = func(string) { shown = false; r.d.Show(build()) }
	r.d.OnTouch = func(g mobile.Gesture) {
		if g.From.Y >= 700 && g.From.Y < 800 {
			shown = !shown
			r.d.Show(build())
		}
	}
	return r
}

func TestSecretCanaryIsAbsentEverywhereEvenAfterShowPassword(t *testing.T) {
	r := showPasswordRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("password"))), Value: canary, Secret: "SHOP_PASSWORD"}); err != nil {
		t.Fatal(err)
	}
	// The person taps "show password": the value is on the screen now.
	if f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Show password")))}); err != nil {
		t.Fatal(err)
	}
	if !f.Secret {
		t.Error("the frame does not say that a secret is on the screen")
	}
	frameJSON, _ := json.Marshal(f)
	if strings.Contains(string(frameJSON), canary) {
		t.Errorf("the frame shows the secret: %s", frameJSON)
	}
	for _, e := range f.Elements {
		if strings.Contains(e.Text, canary) || (e.Locator != nil && e.ID != "" && strings.Contains(e.Locator.Text+e.Locator.Name+e.Locator.XPath, canary)) {
			t.Errorf("element %d holds the secret: %+v", e.Index, e)
		}
	}
	pw := pick(t, f, byID("password"))
	if _, err := s.Capture(ctx); !errors.Is(err, mobilerec.ErrSecretOnScreen) {
		t.Errorf("capture of a screen that shows a secret = %v", err)
	}
	if _, err := s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: &pw}); !errors.Is(err, mobilerec.ErrSecretOnScreen) {
		t.Errorf("tap of the element that shows it = %v", err)
	}

	// An error of the device that holds the secret is scrubbed.
	r.d.TouchErr = fmt.Errorf("the device refused the touch with %s", canary)
	_, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign in")))})
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Errorf("error = %v", err)
	}
	r.d.TouchErr = nil

	// Hide it again and finish.
	if f, err = s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Show password")))}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign in")))}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	timeline, _ := json.Marshal(s.Steps())
	for what, text := range map[string]string{
		"the timeline": string(timeline), "the files": r.allFiles(), "the evidence": r.sink.text(),
	} {
		if strings.Contains(text, canary) {
			t.Errorf("the canary is in %s:\n%.400s", what, text)
		}
	}
	if !strings.Contains(r.allFiles(), "env:SHOP_PASSWORD") {
		t.Error("the flow does not name the secret by its reference")
	}
}

func TestAssertionsAndLocatorsNeverHoldASecretThatIsOnScreen(t *testing.T) {
	r := showPasswordRig(t)
	s := r.session(mobilerec.KindScenario, "TASK-3")
	ctx := context.Background()
	f, _ := s.Start(ctx)
	f, err := s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("password"))), Value: canary, Secret: "SHOP_PASSWORD"})
	if err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Show password")))}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("password")), Kind: mobilerec.AssertEquals})
	if !errors.Is(err, mobilerec.ErrSecretOnScreen) {
		t.Errorf("an assertion on a field that shows a secret = %v", err)
	}
	for _, st := range s.Steps() {
		if strings.Contains(st.Text, canary) {
			t.Errorf("step %+v holds the secret", st)
		}
	}
}

func TestTurningALiteralIntoASecretErasesItEverywhere(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindScenario, "TASK-4")
	ctx := context.Background()
	f, _ := s.Start(ctx)
	_, err := s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("email"))), Value: "ana@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EditStep(2, mobilerec.Edit{Secret: "SHOP_EMAIL"}); err != nil {
		t.Fatal(err)
	}
	steps, _ := json.Marshal(s.Steps())
	if strings.Contains(string(steps), "ana@example.com") {
		t.Errorf("the old value is in the steps: %s", steps)
	}
	if got := s.Overlay().Scrub("mail ana@example.com"); got != "mail ***" {
		t.Errorf("the value is not scrubbed from now on: %q", got)
	}
}

func TestVerifyFromASignedInAppNeedsResetClearAndTheDeviceIsFree(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	recordLogin(t, r, s)
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if !r.signed {
		t.Fatal("the recording should leave the app signed in")
	}
	ctx := context.Background()

	res := verifyFlow(t, r, s, "login")
	if res.Status != flow.StatusCompleted {
		t.Fatalf("verify = %+v", res)
	}
	if got := strings.Join(r.d.ResetModes(), ","); !strings.HasSuffix(got, "clear") {
		t.Errorf("reset modes = %q, the verify must clear the app", got)
	}
	// After the verify the session has the device again, reset.
	if r.openHandles() != 1 {
		t.Errorf("open handles after resume = %d", r.openHandles())
	}
	if _, err := s.Refresh(ctx); err != nil {
		t.Errorf("the session cannot read after the verify: %v", err)
	}

	// The same flow with no reset: clear starts on the home screen of a signed in app, and fails.
	text := r.read(".semar/flows/login.yaml")
	if !strings.Contains(text, "reset: clear") {
		t.Fatalf("flow:\n%s", text)
	}
	r.write(".semar/flows/login.yaml", strings.Replace(text, "reset: clear", "reset: restart", 1))
	r.signed = true
	res = verifyFlow(t, r, s, "login")
	if res.Status != flow.StatusFailed {
		t.Errorf("verify with restart from a signed in app = %q, want failed", res.Status)
	}
}

// verifyFlow does what the command line does: it gives the device away, runs the flow with a fresh
// engine that reads the same environment, and takes the device back.
func verifyFlow(t *testing.T, r *rig, s *mobilerec.Session, id string) flow.RunResult {
	t.Helper()
	ctx := context.Background()
	if err := s.Suspend(ctx); err != nil {
		t.Fatal(err)
	}
	if n := r.openHandles(); n != 0 {
		t.Fatalf("%d device sessions are open during the verify: the lease is not free", n)
	}
	h, err := r.factory()(ctx, s.Overlay().Lookup)
	if err != nil {
		t.Fatal(err)
	}
	if serial, _ := s.Overlay().Lookup(mobilerec.SerialEnv); serial != "emulator-5554" {
		t.Errorf("the verify would use the device %q, want the one of the recording", serial)
	}
	entry, err := flow.Load(r.repo, id)
	if err != nil {
		t.Fatal(err)
	}
	res, err := flow.Run(ctx, h.Engine, repoScreens{r.repo}, entry.Flow, nil, map[string]string{"run.id": "verify"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	return res
}

type repoScreens struct{ repo string }

func (s repoScreens) Load(app string) (domain.Screens, error) { return screens.Load(s.repo, app) }
