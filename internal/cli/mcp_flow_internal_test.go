package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/takeover"
)

func TestFlowBeforeStopsAtTheNextStepWhenAPersonPausedOrHoldsControl(t *testing.T) {
	baton := takeover.New(time.Now)
	b := &mcpFlowBackend{w: &mcpWiring{Baton: baton}}
	var seen []string
	before := b.before(func(_ context.Context, step string) error {
		seen = append(seen, step)
		return nil
	})
	if err := before(t.Context(), "login.start"); err != nil {
		t.Fatalf("the agent drives: %v", err)
	}
	baton.Yield("a person looks")
	err := before(t.Context(), "login.submit")
	if err == nil || err.Error() != "paused by a person before step login.submit" {
		t.Fatalf("err = %v", err)
	}
	if len(seen) != 2 {
		t.Errorf("the caller's hook saw %v", seen)
	}
	baton.Resume("")
	if err := before(t.Context(), "login.submit"); err != nil {
		t.Errorf("after the hand back: %v", err)
	}
}

func TestFlowBeforeKeepsTheCallersRefusalAndWorksWithoutABaton(t *testing.T) {
	b := &mcpFlowBackend{w: &mcpWiring{}}
	if err := b.before(nil)(t.Context(), "x.y"); err != nil {
		t.Errorf("no baton, no hook: %v", err)
	}
	want := errors.New("stop here")
	if err := b.before(func(context.Context, string) error { return want })(t.Context(), "x.y"); !errors.Is(err, want) || strings.Contains(err.Error(), "paused") {
		t.Errorf("err = %v", err)
	}
}
