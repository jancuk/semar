package mobilerec_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/mobilerec"
)

func startSignedIn(r *rig) {
	home := func() { r.d.Show(homeScreen()) }
	r.d.Show(homeScreen())
	r.d.OnLaunch, r.d.OnReset = home, func(string) { home() }
}

func TestREPLEditsTheTimeline(t *testing.T) {
	r := newRig(t)
	startSignedIn(r)
	s := r.session(mobilerec.KindScenario, "TASK-E")
	// Home: 1 heading, 2 greeting, 3 Sign out.
	out := runREPL(t, r, s, strings.Join([]string{
		"assert 2", "tap 3", "steps",
		"goto 1", "steps",
		"delete 2", "steps",
		"goto end", "delete 9", "goto x", "quit",
	}, "\n")+"\n")
	for _, want := range []string{
		"--- recording goes here ---",
		"--- recording goes here: the steps below are after the device",
		"The device is after step 1. A new step goes there.",
		"Deleted step 2.",
		"The device is after step 2.",
		"there is no step 9",
		`"x" is not a step number`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestREPLDeletingAnActionSaysTheDeviceIsOutOfSync(t *testing.T) {
	r := newRig(t)
	startSignedIn(r)
	s := r.session(mobilerec.KindScenario, "TASK-S")
	out := runREPL(t, r, s, "tap 3\ndelete 2\nquit\n")
	if !strings.Contains(out, "The device still shows what it did") || !s.Dirty() {
		t.Errorf("output:\n%s", out)
	}
}

func TestREPLPicksALocatorByNumber(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "pick")
	// Sign in: 1 heading, 2 email, 3 password, 4 the button, which a description, a role and a name, and an xpath find.
	out := runREPL(t, r, s, "loc 4\ntap 4.2\nlocator 2\nlocator 2 1\ntap 4.x\nquit\n")
	for _, want := range []string{"1  { desc: \"Sign in\" }", "1 match [", "Pick one by its number: tap 4.K", "(in use)", "Step 2 now uses locator 1", `"x" is not a locator number`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := s.Steps(); len(got) != 2 || !strings.Contains(got[1].Locator, "role") {
		t.Errorf("steps = %+v", got)
	}
}

func TestREPLListsAndRenamesScreensAndSharesAnElement(t *testing.T) {
	r := newRig(t)
	startSignedIn(r)
	s := r.session(mobilerec.KindFlow, "names")
	out := runREPL(t, r, s, "screen\nscreen Start\ntap 3\nrename-screen Start Landing\ncommon 2\ncommon 1\ncommon 2 off\nsteps\nquit\n")
	for _, want := range []string{"Screen names (the shown screen is Home)", "Renamed Start to Landing", "goes to the common section", "has nothing to share", "goes back to its screen"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestREPLAsksForASecretBeforeGoto(t *testing.T) {
	r := newRig(t)
	login := r.session(mobilerec.KindFlow, "login")
	recordLogin(t, r, login)
	sv, err := login.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadSaved(t, r, mobilerec.KindFlow, "login", sv.FlowPath)
	r.signed = false
	s := r.session(mobilerec.KindFlow, "login", func(o *mobilerec.Options) { o.Resume = loaded })
	out := runREPL(t, r, s, "goto end\n"+canary+"\nsteps\nquit\n")
	if !strings.Contains(out, "Value of the secret SHOP_PASSWORD") || !strings.Contains(out, "The device is after step 4.") {
		t.Errorf("output:\n%s", out)
	}
	if strings.Contains(out, canary) {
		t.Errorf("the output holds the secret:\n%s", out)
	}
	if strings.Contains(r.allFiles(), canary) {
		t.Error("the secret is in a file")
	}
}

func TestREPLRefusesAGotoOutOfRangeBeforeAskingForValues(t *testing.T) {
	r := newRig(t)
	startSignedIn(r)
	s := r.session(mobilerec.KindScenario, "TASK-Z")
	out := runREPL(t, r, s, "goto 0\ngoto -1\ngoto 7\nquit\n")
	if got := strings.Count(out, "There is no step"); got != 3 {
		t.Errorf("%d refusals, want 3:\n%s", got, out)
	}
	if strings.Contains(out, "Value of the") {
		t.Errorf("a value was asked for:\n%s", out)
	}
}
