package mobilerec_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/mobilerec"
)

func TestRecordALoginFlowAndUndoIt(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	recordLogin(t, r, s)

	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sv.FlowPath != ".semar/flows/login.yaml" {
		t.Errorf("flow path = %s", sv.FlowPath)
	}
	entry, err := flow.Load(r.repo, "login")
	if err != nil {
		t.Fatal(err)
	}
	var secretStep domain.ScriptStep
	for _, st := range entry.Flow.Steps {
		if st.Mobile.Secret != "" {
			secretStep = st
		}
	}
	if secretStep.Mobile == nil || secretStep.Mobile.Secret != "env:SHOP_PASSWORD" {
		t.Errorf("no step types the secret: %+v", entry.Flow.Steps)
	}
	first := entry.Flow.Steps[0].Mobile
	if first.Do != domain.MobileLaunch || first.Reset != "clear" {
		t.Errorf("the flow must start with reset: clear, because a secret was typed: %+v", first)
	}
	files := r.read(".semar/flows/login.yaml")
	screensFile := r.read(".semar/screens/shop-app.yaml")
	for _, want := range []string{"apiVersion: semar/v1", "kind: Screens", "app: shop-app", "Signin:", "email"} {
		if want == "Signin:" {
			want = "SignIn:"
		}
		if !strings.Contains(screensFile, want) {
			t.Errorf("screens file lacks %q:\n%s", want, screensFile)
		}
	}
	if strings.Contains(r.allFiles(), canary) || strings.Contains(r.sink.text(), canary) {
		t.Error("the secret is in a file or in the evidence")
	}
	_ = files

	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if r.exists(".semar/flows/login.yaml") || r.exists(".semar/screens/shop-app.yaml") {
		t.Error("undo left files behind")
	}
	if err := s.Undo(); !errors.Is(err, mobilerec.ErrNothingToUndo) {
		t.Errorf("a second undo = %v, want ErrNothingToUndo", err)
	}
}

func TestScenarioUsesALoginFlowLiveAndUndoRestoresTheScreens(t *testing.T) {
	r := newRig(t)
	login := r.session(mobilerec.KindFlow, "login")
	recordLogin(t, r, login)
	loginSave, err := login.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	screensAfterLogin := r.read(".semar/screens/shop-app.yaml")
	loginRun := r.openHandles()
	_ = loginRun

	// The scenario starts with the flow, run live, and checks the greeting.
	ctx := context.Background()
	r.signed = false
	exported := func(name string) (string, bool) { return canary, name == "SHOP_PASSWORD" }
	task := r.session(mobilerec.KindScenario, "TASK-1", func(o *mobilerec.Options) { o.Use = "login"; o.Lookup = exported })
	f, err := task.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f.Screen != "Home" {
		t.Errorf("the screen after the flow is %q, want Home from its heading", f.Screen)
	}
	steps := task.Steps()
	if len(steps) != 1 || steps[0].Kind != "use" {
		t.Fatalf("steps = %+v", steps)
	}
	if _, err := task.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("welcome")), Kind: mobilerec.AssertEquals}); err != nil {
		t.Fatal(err)
	}
	sv, err := task.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	feature := r.read(sv.ScenarioPath)
	if !strings.Contains(feature, "Hi Ana") || !strings.Contains(feature, "@surface:android") {
		t.Errorf("feature:\n%s", feature)
	}
	if !strings.Contains(strings.Join(sv.Files, " "), ".semar/store/scripts/TASK-1/") {
		t.Errorf("the stored script is not among the files: %v", sv.Files)
	}
	script := r.read(sv.Files[len(sv.Files)-1])
	if !strings.Contains(script, "use: login") || !strings.Contains(script, "kind: Script") {
		t.Errorf("script:\n%s", script)
	}
	if !strings.Contains(r.read(".semar/screens/shop-app.yaml"), "welcome") {
		t.Error("the screens file lacks the element of the check")
	}

	// An older save cannot be undone once a later one changed a file that it wrote.
	if err := loginSave.Undo(); err == nil || !strings.Contains(err.Error(), "changed since it was saved") {
		t.Errorf("undo of the older save = %v, want a refusal", err)
	}
	if !r.exists(".semar/flows/login.yaml") {
		t.Error("a refused undo removed the flow")
	}
	if err := task.Undo(); err != nil {
		t.Fatal(err)
	}
	if got := r.read(".semar/screens/shop-app.yaml"); got != screensAfterLogin {
		t.Errorf("undo did not restore the screens file:\n%s", got)
	}
	if r.exists(sv.ScenarioPath) || r.exists(sv.Files[len(sv.Files)-1]) {
		t.Error("undo left the feature or the stored script")
	}
}

func TestUndoRefusesWhenAFileChangedOutside(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	recordLogin(t, r, s)
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	flowText := r.read(".semar/flows/login.yaml")
	r.write(".semar/flows/login.yaml", flowText+"# edited by hand\n")
	err := s.Undo()
	if err == nil || !strings.Contains(err.Error(), "changed since it was saved") {
		t.Fatalf("undo = %v, want a refusal", err)
	}
	if !r.exists(".semar/screens/shop-app.yaml") {
		t.Error("a refused undo changed the screens file")
	}
	if got := r.read(".semar/flows/login.yaml"); !strings.HasSuffix(got, "# edited by hand\n") {
		t.Error("a refused undo overwrote the edit")
	}
}

func TestSaveRefusesToReplaceWithoutTheFlag(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	recordLogin(t, r, s)
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	again := r.session(mobilerec.KindFlow, "login")
	r.signed = false
	recordLogin(t, r, again)
	if _, err := again.Save(mobilerec.SaveOptions{}); err == nil || !strings.Contains(err.Error(), "exists already") {
		t.Errorf("save over a flow = %v", err)
	}
	if _, err := again.Save(mobilerec.SaveOptions{Replace: true}); err != nil {
		t.Errorf("save with replace = %v", err)
	}
}

func TestScreensMergeReusesAndSuffixes(t *testing.T) {
	r := newRig(t)
	r.write(".semar/screens/shop-app.yaml", `apiVersion: semar/v1
kind: Screens
app: shop-app
screens:
  SignIn:
    email: { id: email }
    password: { desc: not the real one }
`)
	s := r.session(mobilerec.KindFlow, "login")
	recordLogin(t, r, s)
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, a := range d.Added {
		for _, e := range a.Elements {
			added = append(added, a.Screen+"."+e.Name)
		}
	}
	// email is reused, password clashes with another locator and gets -2, the button is new.
	if strings.Join(added, ",") != "SignIn.password-2,SignIn.sign-in" && strings.Join(added, ",") != "SignIn.password-2,SignIn.sign-in-2" {
		t.Errorf("added = %v", added)
	}
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatalf("save into an existing screens file: %v", err)
	}
	merged := r.read(".semar/screens/shop-app.yaml")
	if !strings.Contains(merged, "password: { desc: not the real one }") || !strings.Contains(merged, "password-2") {
		t.Errorf("merged:\n%s", merged)
	}
}

func TestAnActionOnAnOldFrameIsStale(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := f.ID
	if _, err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: old, Target: ptr(pick(t, f, byDesc("Sign in")))})
	var stale *mobilerec.StaleError
	if !errors.Is(err, mobilerec.ErrStaleFrame) || !errors.As(err, &stale) {
		t.Fatalf("err = %v, want a stale frame", err)
	}
	if stale.Frame == nil || stale.Frame.ID <= old {
		t.Errorf("the stale error has no newer frame: %+v", stale.Frame)
	}
	if len(r.d.Touches()) != 0 {
		t.Error("a stale action touched the device")
	}
}

func TestTheAppChangingUnderTheFrameIsStale(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := pick(t, f, byDesc("Sign in"))
	// The app moves the button after the frame was read: the person's frame is current, the screen is not.
	moved := button("Sign in", 100, 1400)
	r.d.Show(screen(heading("Sign in"), moved))
	_, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: &target})
	var stale *mobilerec.StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want a stale frame", err)
	}
	if stale.Frame.ID == f.ID {
		t.Error("the refreshed frame has the old number")
	}
	if len(r.d.Touches()) != 0 {
		t.Errorf("the tap went to the device: %+v", r.d.Touches())
	}
}

func TestEveryActionGoesThroughTheEngine(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	recordLogin(t, r, s)
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	if len(r.sink.actions) != 4 {
		t.Fatalf("the engine stored %d actions, want launch, 2 types, tap", len(r.sink.actions))
	}
	for _, a := range r.sink.actions {
		if a.Use.Pass != domain.PassScenario || a.By != domain.ActorPilot || a.Surface != domain.SurfaceAndroid {
			t.Errorf("action %s is not a recorded one: %+v", a.Op, a.Use)
		}
	}
}

func TestAPasswordFieldNeedsASecretAndAProductionEngineRefusals(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("password"))), Value: "hunter2"})
	if !errors.Is(err, mobilerec.ErrNeedsSecret) {
		t.Errorf("a literal in a password field = %v", err)
	}
	if len(r.d.Typed()) != 0 {
		t.Error("the text of a password field went to the device")
	}
	_, err = s.Act(ctx, mobilerec.Act{Do: "open", Frame: f.ID, Value: "https://evil.example.com/x"})
	var ae *mobilerec.ActError
	if !errors.As(err, &ae) || !strings.Contains(ae.Msg, "refused") {
		t.Errorf("a link to a host outside the environment = %v, want the engine's refusal", err)
	}
	if len(r.d.Links()) != 0 {
		t.Error("the link was opened")
	}
}

func TestDeleteLastStepAndEdit(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("email"))), Value: "ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if got := len(s.Steps()); got != 2 {
		t.Fatalf("steps = %d", got)
	}
	// The literal becomes a secret: the old text is gone from the steps.
	if err := s.EditStep(2, mobilerec.Edit{Secret: "SHOP_EMAIL", ID: "email-step"}); err != nil {
		t.Fatal(err)
	}
	steps := s.Steps()
	if strings.Contains(steps[1].Text, "ana@example.com") || steps[1].ID != "email-step" {
		t.Errorf("step = %+v", steps[1])
	}
	if v, ok := s.Overlay().Lookup("SHOP_EMAIL"); !ok || v != "ana@example.com" {
		t.Error("the secret is not in the overlay")
	}
	if err := s.EditStep(1, mobilerec.Edit{Secret: "X"}); err == nil {
		t.Error("a launch became a secret")
	}
	if err := s.DeleteLastStep(); err != nil {
		t.Fatal(err)
	}
	if got := len(s.Steps()); got != 1 {
		t.Errorf("steps after delete = %d", got)
	}
}

func TestTheFirstLaunchUsesTheResetOfTheSession(t *testing.T) {
	r := newRig(t)
	r.signed = true
	s := r.session(mobilerec.KindFlow, "x", func(o *mobilerec.Options) { o.Reset = "clear" })
	f, err := s.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.d.ResetModes(), ","); got != "clear" {
		t.Errorf("reset modes = %q", got)
	}
	if f.Screen != "SignIn" {
		t.Errorf("the screen after a clear is %q, want the sign-in", f.Screen)
	}
	if steps := s.Steps(); len(steps) != 1 || steps[0].Kind != "launch" {
		t.Errorf("steps = %+v", steps)
	}
}

func TestAPhysicalDeviceNeedsConfirmationForClear(t *testing.T) {
	r := newRig(t)
	r.d.Info.Device = "R58M123ABC"
	s := r.session(mobilerec.KindFlow, "login")
	recordLogin(t, r, s)
	if _, err := s.Save(mobilerec.SaveOptions{}); !errors.Is(err, mobilerec.ErrNeedsConfirm) {
		t.Fatalf("save on a physical device = %v, want ErrNeedsConfirm", err)
	}
	if r.exists(".semar/flows/login.yaml") {
		t.Error("an unconfirmed save wrote the flow")
	}
	r2 := newRig(t)
	r2.d.Info.Device = "R58M123ABC"
	s2 := r2.session(mobilerec.KindFlow, "login", func(o *mobilerec.Options) { o.ConfirmClear = true })
	recordLogin(t, r2, s2)
	if _, err := s2.Save(mobilerec.SaveOptions{}); err != nil {
		t.Errorf("confirmed save = %v", err)
	}
}

func TestAScenarioNeedsACheck(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindScenario, "TASK-2")
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(mobilerec.SaveOptions{}); !errors.Is(err, mobilerec.ErrNoCheck) {
		t.Errorf("save with no check = %v", err)
	}
	flowSession := r.session(mobilerec.KindFlow, "plain")
	f, _ := flowSession.Start(context.Background())
	if _, err := flowSession.Assert(context.Background(), mobilerec.Assert{Frame: f.ID, Target: mobilerec.Target{Index: 0}}); err == nil {
		t.Error("a flow took a check")
	}
}

func TestReservedSecretNamesAreRefusedAndPinsWin(t *testing.T) {
	ov := mobilerec.NewOverlay(nil)
	ov.Pin(mobilerec.SerialEnv, "emulator-5554")
	ov.SetSecret(mobilerec.SerialEnv, "hijack")
	if got, ok := ov.Lookup(mobilerec.SerialEnv); !ok || got != "emulator-5554" {
		t.Fatalf("pin lookup = %q, %v; want pinned emulator-5554", got, ok)
	}
	for _, bad := range []string{"ANDROID_SERIAL", "PATH", "HOME", "SEMAR_ADB_BIN", "CLAUDE_API_KEY"} {
		if err := mobilerec.CheckSecretEnv(bad); err == nil {
			t.Errorf("CheckSecretEnv(%q) = nil, want refusal", bad)
		}
	}
	if err := mobilerec.CheckSecretEnv("SHOP_PASSWORD"); err != nil {
		t.Errorf("CheckSecretEnv(SHOP_PASSWORD) = %v, want nil", err)
	}
}

func TestPhysicalDeviceClearResetRefusesStartBeforeReset(t *testing.T) {
	r := newRig(t)
	r.d.Info.Device = "R58M123ABC"
	s := r.session(mobilerec.KindFlow, "login", func(o *mobilerec.Options) { o.Reset = "clear" })
	if _, err := s.Start(context.Background()); !errors.Is(err, mobilerec.ErrNeedsConfirm) {
		t.Fatalf("Start on physical device with reset:clear = %v, want ErrNeedsConfirm", err)
	}
	if calls := r.d.Calls(); len(calls) != 0 {
		t.Errorf("driver calls = %v, want none before confirmation", calls)
	}
}

func TestAssertsAfterScreenTransitionStayInChronologicalOrder(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindScenario, "NAV-1")
	f, err := s.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Tap Sign in (on SignIn screen -> navigates to Home screen).
	f, err = s.Act(context.Background(), mobilerec.Act{Frame: f.ID, Do: mobilerec.DoTap, Target: ptr(pick(t, f, byDesc("Sign in")))})
	if err != nil {
		t.Fatal(err)
	}
	// Assert on Home screen right after navigating to it.
	f, err = s.Assert(context.Background(), mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("welcome")), Kind: mobilerec.AssertVisible})
	if err != nil {
		t.Fatal(err)
	}
	// Tap Sign out (on Home screen -> navigates back to SignIn screen).
	if _, err = s.Act(context.Background(), mobilerec.Act{Frame: f.ID, Do: mobilerec.DoTap, Target: ptr(pick(t, f, byDesc("Sign out")))}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	// The synthetic wait-welcome step MUST come BEFORE tap-sign-out, not deferred to the end.
	waitIdx, signOutIdx := -1, -1
	for i, st := range d.Script.Steps {
		if st.Mobile != nil && st.Mobile.Do == "wait" {
			waitIdx = i
		}
		if st.Step == "tap-sign-out" {
			signOutIdx = i
		}
	}
	if waitIdx < 0 || signOutIdx < 0 || waitIdx >= signOutIdx {
		t.Fatalf("step order wrong: waitIdx=%d signOutIdx=%d steps=%+v", waitIdx, signOutIdx, d.Script.Steps)
	}
}
