package mobilerec_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/mobilerec"
)

func ordersScreen() *mobile.Tree {
	return screen(heading("Orders"), label("ref", "Order #A12B", 300), field("note", "Note", 500, false))
}

// ordersRig is a device whose app shows one screen, the orders, whatever is done to it.
func ordersRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	show := func() { r.d.Show(ordersScreen()) }
	r.d.OnLaunch, r.d.OnTouch = show, nil
	r.d.OnReset = func(string) { show() }
	show()
	return r
}

func startOrders(t *testing.T, r *rig, kind mobilerec.Kind, id string, tune ...func(*mobilerec.Options)) (*mobilerec.Session, *mobilerec.Frame) {
	t.Helper()
	s := r.session(kind, id, tune...)
	f, err := s.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestCaptureAValueWithAGroupAndUseItInALaterText(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	s, f := startOrders(t, r, mobilerec.KindFlow, "order-ref")

	f, value, err := s.CaptureValue(ctx, mobilerec.CaptureReq{Frame: f.ID, Target: pick(t, f, byID("ref")), Name: "orderRef", Pattern: `Order #([A-Z0-9]+)`})
	if err != nil {
		t.Fatal(err)
	}
	if value != "A12B" || !strings.Contains(f.Notice, "orderRef") || !strings.Contains(f.Notice, "A12B") {
		t.Errorf("value %q, notice %q", value, f.Notice)
	}
	steps := s.Steps()
	if got := steps[len(steps)-1]; got.Kind != "capture" || !strings.Contains(got.Text, "orderRef") {
		t.Errorf("steps = %+v", steps)
	}

	// The device gets the value, and the file keeps the name.
	if f, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("note"))), Value: "ref {{orderRef}}"}); err != nil {
		t.Fatal(err)
	}
	if typed := r.d.Typed(); len(typed) != 1 || typed[0] != "ref A12B" {
		t.Errorf("typed = %q", typed)
	}
	if _, err := s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("note"))), Value: "x {{nope}}"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("an unknown variable = %v", err)
	}
	if len(r.d.Typed()) != 1 {
		t.Errorf("a refused text reached the device: %q", r.d.Typed())
	}

	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := r.read(sv.FlowPath)
	for _, want := range []string{"captures:", "name: orderRef", `pattern: 'Order #([A-Z0-9]+)'`, "{{orderRef}}"} {
		if !strings.Contains(text, want) {
			t.Errorf("the flow lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "A12B") || strings.Contains(r.read(".semar/screens/shop-app.yaml"), "A12B") {
		t.Errorf("the captured value is in a file:\n%s", text)
	}
	// The launch has no screen, so the capture has a wait step of its own, before the type step.
	entry, err := flow.Load(r.repo, "order-ref")
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Flow.Steps) != 3 || entry.Flow.Steps[1].Mobile.Do != domain.MobileWait || len(entry.Flow.Steps[1].Captures) != 1 {
		t.Errorf("steps = %+v", entry.Flow.Steps)
	}
}

func TestCaptureAttachesToTheStepBeforeOnTheSameScreen(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	s, f := startOrders(t, r, mobilerec.KindFlow, "typed")
	f, err := s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("note"))), Value: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CaptureValue(ctx, mobilerec.CaptureReq{Frame: f.ID, Target: pick(t, f, byID("ref")), Name: "all"}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Flow.Steps) != 2 || len(d.Flow.Steps[1].Captures) != 1 || d.Flow.Steps[1].Captures[0].Pattern != "" {
		t.Errorf("steps = %+v", d.Flow.Steps)
	}
}

func TestCaptureRefusals(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	s, f := startOrders(t, r, mobilerec.KindFlow, "refusals")
	ref := pick(t, f, byID("ref"))
	for _, c := range []struct {
		name string
		req  mobilerec.CaptureReq
		want string
	}{
		{"no match", mobilerec.CaptureReq{Name: "x", Pattern: `Invoice (\d+)`}, "does not match"},
		{"bad pattern", mobilerec.CaptureReq{Name: "x", Pattern: `(`}, "regular expression"},
		{"bad name", mobilerec.CaptureReq{Name: "1x"}, "must start with a letter"},
		{"no name", mobilerec.CaptureReq{}, "must start with a letter"},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.req.Frame, c.req.Target = f.ID, ref
			if _, _, err := s.CaptureValue(ctx, c.req); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want %q", err, c.want)
			}
		})
	}
	if len(s.Steps()) != 1 {
		t.Errorf("a refused capture was recorded: %+v", s.Steps())
	}
	// A name that is taken.
	if _, _, err := s.CaptureValue(ctx, mobilerec.CaptureReq{Frame: f.ID, Target: ref, Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CaptureValue(ctx, mobilerec.CaptureReq{Frame: s.Frame().ID, Target: ref, Name: "x"}); err == nil || !strings.Contains(err.Error(), "used already") {
		t.Errorf("a name used twice = %v", err)
	}
}

func TestCaptureOfASecretIsRefused(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	// The app shows what is typed, as a screen that echoes a password does.
	r.d.OnType = func(v string) {
		r.d.Show(screen(heading("Orders"), label("echo", v, 300), field("note", "Note", 500, false)))
	}
	s, f := startOrders(t, r, mobilerec.KindFlow, "echo")
	f, err := s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("note"))), Value: canary, Secret: "SHOP_NOTE"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Secret {
		t.Fatalf("the frame does not know that a secret is shown: %+v", f)
	}
	_, _, err = s.CaptureValue(ctx, mobilerec.CaptureReq{Frame: f.ID, Target: pick(t, f, byID("echo")), Name: "leak"})
	if !errors.Is(err, mobilerec.ErrSecretOnScreen) {
		t.Errorf("capture of a secret = %v", err)
	}
	if strings.Contains(r.allFiles(), canary) {
		t.Error("the secret is in a file")
	}
}

func TestMakeAParamOfATypedText(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	s, f := startOrders(t, r, mobilerec.KindFlow, "note-flow")
	note := ptr(pick(t, f, byID("note")))
	var err error
	for _, v := range []string{"hello", "hello"} {
		if f, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: note, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	notice, err := s.MakeParam(2, "greeting", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice, "3") {
		t.Errorf("the notice does not name the other step: %q", notice)
	}
	if _, err := s.MakeParam(2, "greeting", false); err == nil {
		t.Error("a param was made twice from one step")
	}
	if _, err := s.MakeParam(3, "greeting", false); err == nil || !strings.Contains(err.Error(), "used already") {
		t.Errorf("a name used twice = %v", err)
	}
	if _, err := s.MakeParam(1, "start", false); err == nil || !strings.Contains(err.Error(), "does not type") {
		t.Errorf("a launch step = %v", err)
	}
	if _, err := s.MakeParam(3, "who", true); err != nil {
		t.Fatal(err)
	}
	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := r.read(sv.FlowPath)
	if !strings.Contains(text, "value: '{{greeting}}'") && !strings.Contains(text, "value: \"{{greeting}}\"") && !strings.Contains(text, "value: {{greeting}}") {
		t.Errorf("the step does not type the param:\n%s", text)
	}
	entry, err := flow.Load(r.repo, "note-flow")
	if err != nil {
		t.Fatal(err)
	}
	g, ok := entry.Flow.Param("greeting")
	w, ok2 := entry.Flow.Param("who")
	if !ok || g.Default == nil || *g.Default != "hello" || g.Required() {
		t.Errorf("greeting = %+v", g)
	}
	if !ok2 || w.Default != nil || !w.Required() {
		t.Errorf("who = %+v", w)
	}
}

func TestAParamIsMadeOnlyInAFlowAndNeverOfASecret(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	sc, f := startOrders(t, r, mobilerec.KindScenario, "TASK-P")
	if _, err := sc.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("note"))), Value: "hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.MakeParam(2, "greeting", false); err == nil || !strings.Contains(err.Error(), "scenario has no params") {
		t.Errorf("a param in a scenario = %v", err)
	}

	fl, g := startOrders(t, r, mobilerec.KindFlow, "secret-flow")
	if _, err := fl.Act(ctx, mobilerec.Act{Do: "type", Frame: g.ID, Target: ptr(pick(t, g, byID("note"))), Value: canary, Secret: "SHOP_NOTE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fl.MakeParam(2, "pw", false); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Errorf("a param of a secret = %v", err)
	}
}

func TestAFlowRecordingHasChecksAndUsesAFlow(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	base, f := startOrders(t, r, mobilerec.KindFlow, "base")
	if _, err := base.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("note"))), Value: "hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}

	// A flow that starts with the flow base, and then checks the screen.
	outer, f := startOrders(t, r, mobilerec.KindFlow, "outer", func(o *mobilerec.Options) { o.Use = "base" })
	if steps := outer.Steps(); len(steps) != 1 || steps[0].Kind != "use" {
		t.Fatalf("steps = %+v", steps)
	}
	if _, err := outer.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("ref")), Kind: mobilerec.AssertContains, Value: "Order"}); err != nil {
		t.Fatal(err)
	}
	sv, err := outer.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := r.read(sv.FlowPath)
	for _, want := range []string{"use: base", "expect:", "source: person", "contains: Order"} {
		if !strings.Contains(text, want) {
			t.Errorf("the flow lacks %q:\n%s", want, text)
		}
	}
	if _, err := flow.Load(r.repo, "outer"); err != nil {
		t.Fatal(err)
	}
	// The check sits on a wait step of its own: the use step takes none.
	entry, _ := flow.Load(r.repo, "outer")
	if len(entry.Flow.Steps) != 2 || entry.Flow.Steps[1].Mobile == nil || len(entry.Flow.Steps[1].Expect) != 1 {
		t.Errorf("steps = %+v", entry.Flow.Steps)
	}
	if d := outer.Dirty(); d {
		t.Error("the session is dirty")
	}

	// The flow outer reopens with its check and its use step, and saves the same file.
	loaded := loadSaved(t, r, mobilerec.KindFlow, "outer", sv.FlowPath)
	re := r.session(mobilerec.KindFlow, "outer", func(o *mobilerec.Options) { o.Resume = loaded })
	if _, err := re.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := re.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := r.read(sv.FlowPath); got != text {
		t.Errorf("the flow changed:\n%s\nwas\n%s", got, text)
	}
}

func TestAFlowThatWouldUseItselfIsRefused(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	a, af := startOrders(t, r, mobilerec.KindFlow, "a")
	if _, err := a.Act(ctx, mobilerec.Act{Do: "type", Frame: af.ID, Target: ptr(pick(t, af, byID("note"))), Value: "hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	b, _ := startOrders(t, r, mobilerec.KindFlow, "b", func(o *mobilerec.Options) { o.Use = "a" })
	if _, err := b.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}

	// Recording the flow a again, it cannot use b, which uses a.
	again, _ := startOrders(t, r, mobilerec.KindFlow, "a")
	_, _, err := again.UseFlow(ctx, "b", nil)
	if err == nil || !strings.Contains(err.Error(), "cannot use itself") {
		t.Errorf("a cycle = %v", err)
	}
	if len(again.Steps()) != 1 {
		t.Errorf("a refused use was recorded: %+v", again.Steps())
	}
	if _, _, err := again.UseFlow(ctx, "a", nil); err == nil {
		t.Error("a flow used itself")
	}
	// Retarget checks the same: a scenario that uses b cannot become the flow a.
	sc, _ := startOrders(t, r, mobilerec.KindScenario, "TASK-C", func(o *mobilerec.Options) { o.Use = "b" })
	if err := sc.Retarget(mobilerec.Next{Kind: mobilerec.KindFlow, ID: "a"}); err == nil || !strings.Contains(err.Error(), "cannot use itself") {
		t.Errorf("retarget into a cycle = %v", err)
	}
	if err := sc.Retarget(mobilerec.Next{Kind: mobilerec.KindFlow, ID: "c"}); err != nil {
		t.Errorf("retarget = %v", err)
	}
}

func TestACheckThatExpectsACapturedValueIsOnAStepAfterIt(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	s, f := startOrders(t, r, mobilerec.KindScenario, "TASK-V")
	f, _, err := s.CaptureValue(ctx, mobilerec.CaptureReq{Frame: f.ID, Target: pick(t, f, byID("ref")), Name: "orderRef", Pattern: `#(\w+)`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("ref")), Kind: mobilerec.AssertContains, Value: "{{orderRef}}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Assert(ctx, mobilerec.Assert{Frame: s.Frame().ID, Target: pick(t, f, byID("ref")), Kind: mobilerec.AssertContains, Value: "{{missing}}"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("an unknown variable in a check = %v", err)
	}
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	// The value is read by the step that captures it, so the check that expects it cannot be on that step.
	var carrier, checked int
	for i, st := range d.Script.Steps {
		if len(st.Captures) > 0 {
			carrier = i
		}
		if len(d.Scenario.Steps[i].Expect) > 0 {
			checked = i
		}
	}
	if checked <= carrier {
		t.Errorf("the check is on step %d, and the capture on %d", checked, carrier)
	}
	if got := d.Scenario.Steps[checked].Expect[0].Check.Contains; got != "{{orderRef}}" {
		t.Errorf("the check expects %v", got)
	}
}

func TestGotoReadsACaptureAgain(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	s, f := startOrders(t, r, mobilerec.KindFlow, "again")
	f, _, err := s.CaptureValue(ctx, mobilerec.CaptureReq{Frame: f.ID, Target: pick(t, f, byID("ref")), Name: "orderRef", Pattern: `#(\w+)`})
	if err != nil {
		t.Fatal(err)
	}
	// The app shows another order after a restart.
	other := func() {
		r.d.Show(screen(heading("Orders"), label("ref", "Order #ZZ99", 300), field("note", "Note", 500, false)))
	}
	r.d.OnLaunch, r.d.OnReset = other, func(string) { other() }
	f, err = s.Goto(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	// The value is what the screen shows now.
	if _, err := s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("note"))), Value: "{{orderRef}}"}); err != nil {
		t.Fatal(err)
	}
	if typed := r.d.Typed(); !slices.Equal(typed, []string{"ZZ99"}) {
		t.Errorf("typed = %q", typed)
	}
}

func TestCaptureOfATextWithNoIDKeepsTheValueOutOfTheFiles(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	const code = "Reset code 884213"
	show := func() {
		r.d.Show(screen(heading("Reset"), label("", code, 300), label("", "Enter the code", 450), label("ref", "Order #A12B", 700)))
	}
	r.d.OnLaunch, r.d.OnTouch = show, nil
	r.d.OnReset = func(string) { show() }
	show()
	s, f := startOrders(t, r, mobilerec.KindFlow, "reset-code")
	target := pick(t, f, func(e mobilerec.Element) bool { return e.Text == code })

	// Every locator of the label is made of its text, so each is refused, by default and by number.
	for _, k := range []int{0, 1, 2, 3} {
		_, _, err := s.CaptureValue(ctx, mobilerec.CaptureReq{Frame: f.ID, Target: target, Name: "code", Pattern: `(\d+)$`, Candidate: k})
		if err == nil || !strings.Contains(err.Error(), "holds the text") {
			t.Errorf("capture with locator %d = %v", k, err)
		}
		if k == 0 && !errors.Is(err, mobilerec.ErrCaptureHoldsText) {
			t.Errorf("capture with the default locator = %v, want ErrCaptureHoldsText", err)
		}
	}
	if len(s.Steps()) != 1 {
		t.Errorf("a refused capture was recorded: %+v", s.Steps())
	}
	if _, _, err := s.CaptureValue(ctx, mobilerec.CaptureReq{Frame: f.ID, Target: pick(t, f, byID("ref")), Name: "ref"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if all := r.allFiles(); strings.Contains(all, "884213") || strings.Contains(all, "A12B") || strings.Contains(all, "Reset code") {
		t.Errorf("the value is in a file:\n%s", all)
	}
}
