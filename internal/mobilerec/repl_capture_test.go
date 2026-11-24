package mobilerec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/mobilerec"
)

func TestREPLCapturesAValueAndUsesIt(t *testing.T) {
	r := ordersRig(t)
	s := r.session(mobilerec.KindFlow, "cap")
	// Orders: 1 heading, 2 the order, 3 the note field.
	out := runREPL(t, r, s, strings.Join([]string{
		"capture 2 orderRef Order #([A-Z0-9]+)",
		"capture 2 orderRef",
		"capture 2",
		"capture 2 bad (",
		"type 3 ref {{orderRef}}",
		"type 3 {{nope}}",
		"type 3 hello",
		"param 4 note",
		"steps",
		"quit",
	}, "\n")+"\n")
	for _, want := range []string{
		`Captured orderRef = "A12B". Write {{orderRef}} in a later text or check.`,
		"used already",
		"Usage: capture N[.K] NAME [REGEX]",
		"regular expression",
		"{{nope}}",
		"Step 4 now types {{note}}.",
		"capture  Remember",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if typed := r.d.Typed(); len(typed) != 2 || typed[0] != "ref A12B" {
		t.Errorf("typed = %q", typed)
	}
}

func TestREPLScreenshotsHaveTheirOwnCommandAndTheOldOneStillWorks(t *testing.T) {
	r := ordersRig(t)
	r.d.Shot = []byte("png-bytes")
	dir := t.TempDir()
	one, two := filepath.Join(dir, "one.png"), filepath.Join(dir, "two.png")
	s := r.session(mobilerec.KindFlow, "shots")
	out := runREPL(t, r, s, "shot "+one+"\ncapture "+two+"\nquit\n")
	if strings.Count(out, "Saved a screenshot") != 2 {
		t.Errorf("output:\n%s", out)
	}
	for _, p := range []string{one, two} {
		if b, err := os.ReadFile(p); err != nil || string(b) != "png-bytes" {
			t.Errorf("%s: %q, %v", p, b, err)
		}
	}
}

func TestREPLParamNeedsAFlowAndAName(t *testing.T) {
	r := ordersRig(t)
	s := r.session(mobilerec.KindScenario, "TASK-R")
	out := runREPL(t, r, s, "type 3 hello\nparam 2 greeting\nparam 2\nparam x y\nparam 2 y maybe\nquit\n")
	for _, want := range []string{"a scenario has no params", "Usage: param S NAME [required]", `"x" is not a step number`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
