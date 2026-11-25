package mobilerec_test

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/jancuk/semar/internal/mobilerec"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 32))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRecordedTapAndSwipeSettleOnceAndCacheShot(t *testing.T) {
	r := newRig(t)
	r.d.Shot = tinyPNG(t)
	s := r.session(mobilerec.KindScenario, "PERF-1")
	ctx := context.Background()

	// Start: 1 settle (2 Source reads) and 1 Screenshot; cached JPEG is ready for the frame.
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.d.SourceCalls(); got != 2 {
		t.Errorf("Start SourceCalls = %d, want 2 (single settle)", got)
	}
	if got := r.d.ScreenshotCalls(); got != 1 {
		t.Errorf("Start ScreenshotCalls = %d, want 1", got)
	}
	jpg := s.LastShotJPEG(f.ID)
	if len(jpg) == 0 {
		t.Fatal("Start did not cache LastShotJPEG")
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(jpg)); err != nil {
		t.Fatalf("LastShotJPEG is not valid JPEG: %v", err)
	}

	// Tap: 1 resolve + 1 settle (3 Source reads total, skipping waitTarget Source via InitialTree), 1 Screenshot.
	srcBefore, shotBefore := r.d.SourceCalls(), r.d.ScreenshotCalls()
	f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign in")))})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.d.SourceCalls() - srcBefore; got != 3 {
		t.Errorf("tap SourceCalls = %d, want 3 (1 resolve + 1 settle)", got)
	}
	if got := r.d.ScreenshotCalls() - shotBefore; got != 1 {
		t.Errorf("tap ScreenshotCalls = %d, want 1", got)
	}
	if len(s.LastShotJPEG(f.ID)) == 0 {
		t.Error("tap did not cache LastShotJPEG")
	}

	// Plain swipe: 0 pre-action Source (uses InitialTree) + 1 settle (2 Source reads total), 1 Screenshot.
	srcBefore, shotBefore = r.d.SourceCalls(), r.d.ScreenshotCalls()
	f, err = s.Act(ctx, mobilerec.Act{Do: "swipe", Frame: f.ID, Value: "up"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.d.SourceCalls() - srcBefore; got != 2 {
		t.Errorf("swipe SourceCalls = %d, want 2 (single settle only)", got)
	}
	if got := r.d.ScreenshotCalls() - shotBefore; got != 1 {
		t.Errorf("swipe ScreenshotCalls = %d, want 1", got)
	}
	if len(s.LastShotJPEG(f.ID)) == 0 {
		t.Error("swipe did not cache LastShotJPEG")
	}

	// Direct Refresh clears cached JPEG.
	f, err = s.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if jpg := s.LastShotJPEG(f.ID); jpg != nil {
		t.Errorf("Refresh kept cached LastShotJPEG (%d bytes)", len(jpg))
	}

	// Close clears cached JPEG.
	f, err = s.Act(ctx, mobilerec.Act{Do: "swipe", Frame: f.ID, Value: "down"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.LastShotJPEG(f.ID)) == 0 {
		t.Fatal("expected cached shot before Close")
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if jpg := s.LastShotJPEG(f.ID); jpg != nil {
		t.Errorf("Close kept cached LastShotJPEG (%d bytes)", len(jpg))
	}
}

func TestSecretOnScreenNeverCachesShot(t *testing.T) {
	r := showPasswordRig(t)
	r.d.Shot = tinyPNG(t)
	s := r.session(mobilerec.KindFlow, "login")
	ctx := context.Background()

	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("password"))), Value: canary, Secret: "SHOP_PASSWORD"})
	if err != nil {
		t.Fatal(err)
	}
	f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Show password")))})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Secret {
		t.Fatal("expected frame to be marked secret")
	}
	if jpg := s.LastShotJPEG(f.ID); jpg != nil {
		t.Errorf("cached JPEG returned on secret screen (%d bytes)", len(jpg))
	}
}
