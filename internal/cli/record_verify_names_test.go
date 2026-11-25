package cli

import (
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/mobilerec"
)

func TestActionNamesUseTheStepTextOfTheTimeline(t *testing.T) {
	dir := t.TempDir()
	store, err := evidence.Open(semarDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	const id = domain.RunID("r-20261125T000000Z-abcd")
	run, err := store.NewRun(id)
	if err != nil {
		t.Fatal(err)
	}
	for i, step := range []domain.StepID{"start", "tap-play", "swipe-up"} {
		if err := run.AppendActivity(domain.Activity{Seq: i + 1, At: time.Unix(0, 0), Use: domain.UseCase{Pass: domain.PassScenario, Scenario: "S-1", Step: step}}); err != nil {
			t.Fatal(err)
		}
	}
	steps := []mobilerec.Step{{ID: "start", Text: "Start the app"}, {ID: "tap-play", Text: "Tap Play"}}
	got := actionNames(dir, id, []domain.ActionID{"a-0001", "a-0002", "a-0003", "a-0009"}, steps)
	want := map[domain.ActionID]string{
		"a-0001": "Start the app",
		"a-0002": "Tap Play",
		"a-0003": "swipe-up", // a step that is not in the timeline keeps its ID
		"a-0009": "a-0009",   // an action with no activity keeps its own ID
	}
	for a, w := range want {
		if got[a] != w {
			t.Errorf("%s = %q, want %q", a, got[a], w)
		}
	}
	// No run, no store: every action keeps its ID, and nothing fails.
	if got := actionNames(t.TempDir(), "", []domain.ActionID{"a-0001"}, steps); got["a-0001"] != "a-0001" {
		t.Errorf("no run: %q", got["a-0001"])
	}
}
