package mobilerec_test

import (
	"context"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/mobilerec"
)

func TestPeekGivesANewFrameOnlyWhenTheScreenChangedOnItsOwn(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindScenario, "PEEK-1")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	steps := len(s.Steps())

	if nf, changed, _, err := s.Peek(ctx, f.ID, time.Second); err != nil || changed || nf != nil {
		t.Fatalf("a still screen: frame %v, changed %v, err %v", nf, changed, err)
	}
	// The app goes on by itself after the action was done.
	r.d.Show(homeScreen())
	if nf, changed, _, err := s.Peek(ctx, f.ID-1, time.Second); err != nil || changed || nf != nil {
		t.Fatalf("an old frame: frame %v, changed %v, err %v", nf, changed, err)
	}
	nf, changed, _, err := s.Peek(ctx, f.ID, time.Second)
	if err != nil || !changed || nf == nil || nf.ID == f.ID {
		t.Fatalf("a changed screen: frame %v, changed %v, err %v", nf, changed, err)
	}
	if got := len(s.Steps()); got != steps {
		t.Errorf("a peek added a step: %d steps, want %d", got, steps)
	}
}

func TestPeekGivesUpOnAScreenThatIsSlowToRead(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindScenario, "PEEK-2")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.d.Show(homeScreen())
	r.d.SourceDelay = time.Minute
	began := time.Now()
	nf, changed, slow, err := s.Peek(ctx, f.ID, 50*time.Millisecond)
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("the peek held the device for %s", took)
	}
	if err != nil || changed || !slow || nf != nil {
		t.Fatalf("a slow screen: frame %v, changed %v, slow %v, err %v", nf, changed, slow, err)
	}
	r.d.SourceDelay = 0
	if _, err := s.Act(ctx, mobilerec.Act{Do: "swipe", Frame: f.ID, Value: "up"}); err != nil {
		t.Errorf("the frame went stale after a slow peek: %v", err)
	}
}
