package mobilerec_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/mobilerec"
)

func runREPL(t *testing.T, r *rig, s *mobilerec.Session, input string, tune ...func(*mobilerec.REPL)) string {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	repl := &mobilerec.REPL{Session: s, In: strings.NewReader(input), Out: &out}
	for _, f := range tune {
		f(repl)
	}
	if err := repl.Run(ctx); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestREPLRecordsALoginFlow(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	// Elements of the sign-in screen: 1 heading, 2 email, 3 password, 4 sign in.
	out := runREPL(t, r, s, strings.Join([]string{
		"type 2 ana@example.com",
		"secret 3 SHOP_PASSWORD",
		canary,
		"tap 4",
		"steps",
		"finish",
		"quit",
	}, "\n")+"\n")
	for _, want := range []string{"Screen SignIn", "Saved:", ".semar/flows/login.yaml", "Type the secret env:SHOP_PASSWORD"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, canary) && strings.Count(out, canary) > 0 {
		// The prompt does not echo the value, and nothing prints it.
		t.Errorf("the output holds the secret:\n%s", out)
	}
	if !r.exists(".semar/flows/login.yaml") {
		t.Error("the flow was not written")
	}
}

func TestREPLShowsTheNewScreenWhenTheOldOneIsStale(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	var out string
	out = runREPL(t, r, s, "refresh\nquit\n")
	if !strings.Contains(out, "frame 2") {
		t.Errorf("output:\n%s", out)
	}
	// A tap that names a number the screen does not have is refused before the device is touched.
	out = runREPL2(t, r, "tap 99\nquit\n")
	if !strings.Contains(out, "pick one from 1 to") || len(r.d.Touches()) != 0 {
		t.Errorf("output:\n%s", out)
	}
}

func runREPL2(t *testing.T, r *rig, input string) string {
	t.Helper()
	var out bytes.Buffer
	s := r.session(mobilerec.KindFlow, "x")
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := (&mobilerec.REPL{Session: s, In: strings.NewReader(input), Out: &out}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestREPLAssertsAndUndoes(t *testing.T) {
	r := newRig(t)
	r.signed = true
	s := r.session(mobilerec.KindScenario, "TASK-9")
	out := runREPL(t, r, s, "assert 2\nfinish\nundo\nundo\nquit\n")
	for _, want := range []string{"Check recorded: welcome equals \"Hi Ana\"", "Saved:", "Undone", "there is no save to undo"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if r.exists(".semar/scenarios/SHOP-1/TASK-9.feature") {
		t.Error("undo left the feature")
	}
}

func TestREPLAsksBeforeClearingAPhysicalDevice(t *testing.T) {
	r := newRig(t)
	r.d.Info.Device = "R58M123ABC"
	s := r.session(mobilerec.KindFlow, "login")
	out := runREPL(t, r, s, "type 2 ana@example.com\nsecret 3 SHOP_PASSWORD\n"+canary+"\ntap 4\nfinish\nno\nfinish\nyes\nquit\n")
	if strings.Count(out, "deletes the data of the app on R58M123ABC") != 2 {
		t.Errorf("the REPL must ask on each finish:\n%s", out)
	}
	if !r.exists(".semar/flows/login.yaml") {
		t.Errorf("a confirmed finish wrote nothing:\n%s", out)
	}
}

func TestREPLVerifyHook(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	out := runREPL(t, r, s, "type 2 ana@example.com\nsecret 3 SHOP_PASSWORD\n"+canary+"\ntap 4\nfinish\nquit\n", func(rp *mobilerec.REPL) {
		rp.Verify = func(ctx context.Context, sv *mobilerec.Saved) (string, error) {
			res := verifyFlow(t, r, s, "login")
			return "The flow login " + res.Status + ".", nil
		}
	})
	if !strings.Contains(out, "The flow login completed.") {
		t.Errorf("output:\n%s", out)
	}
}
