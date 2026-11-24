package mobilerec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/mobilerec"
)

func TestRecordADoubleTapAndADragThenReopenThem(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	s := r.session(mobilerec.KindFlow, "gestures")
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	email, password := pick(t, f, byID("email")), pick(t, f, byID("password"))
	if f, err = s.Act(ctx, mobilerec.Act{Do: mobilerec.DoDoubleTap, Frame: f.ID, Target: &email}); err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: mobilerec.DoDrag, Frame: f.ID, Target: &email, To: &password, Value: "800ms"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Act(ctx, mobilerec.Act{Do: mobilerec.DoDrag, Frame: f.ID, Target: &email}); err == nil || !strings.Contains(err.Error(), "drop on") {
		t.Errorf("a drag with no target: %v", err)
	}
	if _, err = s.Act(ctx, mobilerec.Act{Do: mobilerec.DoTap, Frame: f.ID, Target: &email, To: &password}); err == nil || !strings.Contains(err.Error(), "takes no element to drop on") {
		t.Errorf("a tap with a drop target: %v", err)
	}

	touches := r.d.Touches()
	if len(touches) != 2 || touches[0].Taps != 2 || touches[0].From != touches[0].To {
		t.Fatalf("touches = %+v", touches)
	}
	drag := touches[1]
	if drag.Hold.Milliseconds() != 800 || drag.Move == 0 || drag.From == drag.To || drag.From.Y >= drag.To.Y {
		t.Errorf("drag = %+v", drag)
	}
	steps := s.Steps()
	if len(steps) != 3 || steps[1].Kind != "doubleTap" || steps[2].Kind != "drag" || !strings.HasPrefix(steps[1].Text, "Double-tap ") || !strings.HasPrefix(steps[2].Text, "Drag ") || !strings.Contains(steps[2].Text, " to ") {
		t.Errorf("steps = %+v", steps)
	}

	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	flowFile := r.read(sv.FlowPath)
	for _, want := range []string{"do: doubleTap", "do: drag", "to: ", "value: 800ms"} {
		if !strings.Contains(flowFile, want) {
			t.Errorf("the flow lacks %q:\n%s", want, flowFile)
		}
	}
	loaded := loadSaved(t, r, mobilerec.KindFlow, "gestures", sv.FlowPath)
	var dragStep domain.MobileAction
	for _, st := range loaded.Flow.Steps {
		if st.Mobile.Do == domain.MobileDrag {
			dragStep = *st.Mobile
		}
	}
	if dragStep.To == "" || dragStep.Element == "" || dragStep.To == dragStep.Element || dragStep.Value != "800ms" {
		t.Fatalf("the saved drag = %+v", dragStep)
	}
	if ps := dragStep.Problems(); len(ps) > 0 {
		t.Errorf("the saved drag is not well formed: %v", ps)
	}

	// Reopen: the steps replay on the device, and a save that changes nothing writes the same flow.
	before := len(r.d.Touches())
	re := r.session(mobilerec.KindFlow, "gestures", func(o *mobilerec.Options) { o.Resume = loaded })
	if _, err = re.Start(ctx); err != nil {
		t.Fatal(err)
	}
	replayed := r.d.Touches()[before:]
	if len(replayed) != 2 || replayed[0].Taps != 2 || replayed[1].Hold.Milliseconds() != 800 || replayed[1].From.Y >= replayed[1].To.Y {
		t.Errorf("replayed = %+v", replayed)
	}
	if got := re.Steps(); len(got) != 3 || !strings.Contains(got[2].Text, "Drag ") || !strings.Contains(got[2].Text, " to ") {
		t.Errorf("reopened steps = %+v", got)
	}
	d, err := re.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Added) != 0 {
		t.Errorf("an unchanged recording adds elements: %+v", d.Added)
	}
	if g := d.Flow.Steps[2].Mobile; *g != dragStep {
		t.Errorf("the rebuilt drag = %+v, was %+v", g, dragStep)
	}
}
