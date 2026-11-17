package activity_test

import (
	"testing"

	"github.com/jancuk/semar/internal/activity"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
)

func TestFromWebAction(t *testing.T) {
	click := domain.Action{
		Seq: 3, Surface: domain.SurfaceWeb, Op: "click", Why: "Move the card", By: domain.ActorReplay,
		Web: &domain.WebRecord{Op: "click", Screen: "Board", URL: "http://x.test/b", Screenshot: "screenshots/s/0003.png"},
	}
	got := activity.FromAction(click, effects.Write, "exchanges/s/0003-a.json")
	if got.Verb != domain.VerbUpdate || got.Target != "click Board" || got.Result != "done" || got.Screenshot != "screenshots/s/0003.png" || got.Why != "Move the card" || got.Object != nil {
		t.Errorf("click = %+v", got)
	}
	open := domain.Action{Surface: domain.SurfaceWeb, Op: "open", Web: &domain.WebRecord{Op: "open", URL: "http://x.test/boards/7?x=1"}}
	if got := activity.FromAction(open, effects.Read, "p"); got.Verb != domain.VerbVisit || got.Target != "open /boards/7" {
		t.Errorf("open = %+v", got)
	}
	refused := domain.Action{Surface: domain.SurfaceWeb, Op: "click", Refused: "read_only: no", Web: &domain.WebRecord{Op: "click"}}
	if got := activity.FromAction(refused, effects.Write, "p"); got.Result != "refused: read_only: no" {
		t.Errorf("refused = %+v", got)
	}
}

func TestAnOpenThatSignedInSaysAsWhom(t *testing.T) {
	open := domain.Action{Surface: domain.SurfaceWeb, Op: "open", Web: &domain.WebRecord{
		Op: "open", URL: "http://x.test/desk", SignIn: &domain.WebSignIn{Account: "support", User: "ana@example.com"},
	}}
	if got := activity.FromAction(open, effects.Read, "p"); got.Result != "done, signed in as support" {
		t.Errorf("result = %q", got.Result)
	}
}
