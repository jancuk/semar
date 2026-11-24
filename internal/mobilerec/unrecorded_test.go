package mobilerec_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mobilerec"
)

func TestUnrecordedActsOnTheDeviceAddsNoStepAndOpensAGap(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Gap(); ok {
		t.Fatal("a new recording has a gap")
	}
	before := len(s.Steps())
	touches := len(r.d.Touches())
	f, err = s.Unrecorded(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign in")))})
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		t.Fatal("no frame after an unrecorded tap")
	}
	if got := len(r.d.Touches()); got != touches+1 {
		t.Errorf("the device got %d touches, want %d", got, touches+1)
	}
	if got := len(s.Steps()); got != before {
		t.Errorf("steps = %d, want %d: an unrecorded action added a step", got, before)
	}
	gap, at := s.Gap()
	if !gap || at != before {
		t.Errorf("Gap() = %v, %d; want true, %d", gap, at, before)
	}

	_, err = s.Save(mobilerec.SaveOptions{})
	if err == nil || !errors.Is(err, mobilerec.ErrUnrecorded) || !strings.Contains(err.Error(), "were not recorded: undo past them or record them") {
		t.Fatalf("Save with a gap = %v", err)
	}
	if r.exists(".semar/flows/login.yaml") {
		t.Error("a save with a gap wrote the flow")
	}
}

func TestAGapEndsWhenTheDeviceIsTakenThroughTheStepsAgain(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// One recorded step, then one by hand.
	if f, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("email"))), Value: "ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Unrecorded(ctx, mobilerec.Act{Do: "press", Frame: f.ID, Value: "back"}); err != nil {
		t.Fatal(err)
	}
	if ok, at := s.Gap(); !ok || at != 2 {
		t.Fatalf("Gap() = %v, %d", ok, at)
	}
	if _, err := s.Goto(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Gap(); ok {
		t.Error("a goto replays from the reset, so the gap must be closed")
	}
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Errorf("Save after the gap closed: %v", err)
	}
}

func TestAGapEndsWhenTheStepsAreDeletedBackPastIt(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("email"))), Value: "ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Unrecorded(ctx, mobilerec.Act{Do: "press", Frame: f.ID, Value: "back"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteLastStep(); err != nil { // 2 steps -> 1: fewer than the 2 there were
		t.Fatal(err)
	}
	if ok, _ := s.Gap(); ok {
		t.Error("the step before the gap was deleted, so the gap is gone")
	}
}

func TestAnUnrecordedActionThatFailsLeavesNoGap(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A stale frame does not touch the device.
	_, err = s.Unrecorded(ctx, mobilerec.Act{Do: "tap", Frame: f.ID + 99, Target: ptr(pick(t, f, byDesc("Sign in")))})
	if err == nil {
		t.Fatal("a stale unrecorded action went through")
	}
	if ok, _ := s.Gap(); ok {
		t.Error("an action that never reached the device opened a gap")
	}
}

func TestTheKeyboardWindowIsToldFromTheAppInARecording(t *testing.T) {
	r := newRig(t)
	appWin := &mobile.Node{Class: "android.widget.FrameLayout", Package: "com.example.shop", Visible: true, Enabled: true,
		Bounds: mobile.Rect{Right: 1080, Bottom: 1500}, Children: []*mobile.Node{heading("Sign in"), field("email", "Email", 300, false)}}
	key := &mobile.Node{Class: "android.widget.Button", Package: "com.aura.keyboard.debug", Desc: "q", Role: "button", Visible: true, Enabled: true,
		Bounds: mobile.Rect{Left: 10, Top: 1520, Right: 100, Bottom: 1620}, Attrs: map[string]string{"clickable": "true", "content-desc": "q"}}
	kb := &mobile.Node{Class: "android.widget.FrameLayout", Package: "com.aura.keyboard.debug", Visible: true, Enabled: true,
		Bounds: mobile.Rect{Left: 0, Top: 1500, Right: 1080, Bottom: 2200}, Children: []*mobile.Node{key}}
	app := screen(appWin, kb)
	r.d.Show(app)
	r.d.OnLaunch = func() { r.d.Show(app) }
	r.d.OnReset = func(string) { r.d.Show(app) }
	s := r.session(mobilerec.KindFlow, "kb")
	f, err := s.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, e := range f.Elements {
		if e.Desc == "q" {
			got = e.Window
		}
	}
	if got != string(mobile.WindowKeyboard) {
		t.Fatalf("the key is in the window %q, want keyboard (guessed from the package): %+v", got, f.Elements)
	}
	s.SetIME("com.example.other")
	f, err = s.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.Elements {
		if e.Desc == "q" && e.Window == string(mobile.WindowKeyboard) {
			t.Error("an explicit IME hint that names another package must win over the guess")
		}
	}
}
