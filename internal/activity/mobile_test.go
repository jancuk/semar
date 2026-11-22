package activity_test

import (
	"testing"

	"github.com/jancuk/semar/internal/activity"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
)

func TestFromMobileAction(t *testing.T) {
	tap := domain.Action{
		Seq: 3, Surface: domain.SurfaceAndroid, Op: "tap", Why: "Sign in", By: domain.ActorReplay,
		Mobile: &domain.MobileRecord{Op: "tap", Screen: "SignIn", Screenshot: "screenshots/s/0003.jpg"},
	}
	got := activity.FromAction(tap, effects.Write, "exchanges/s/0003-a.json")
	if got.Verb != domain.VerbUpdate || got.Target != "tap SignIn" || got.Result != "done" || got.Screenshot != "screenshots/s/0003.jpg" || got.Why != "Sign in" || got.Object != nil || got.Surface != domain.SurfaceAndroid {
		t.Errorf("tap = %+v", got)
	}
	open := domain.Action{Surface: domain.SurfaceAndroid, Op: "open", Mobile: &domain.MobileRecord{Op: "open", Link: "shop://cart/7?token=abc#frag"}}
	if got := activity.FromAction(open, effects.Read, "p"); got.Verb != domain.VerbVisit || got.Target != "open shop://cart/7" {
		t.Errorf("open = %+v", got)
	}
	launch := domain.Action{Surface: domain.SurfaceAndroid, Op: "launch", Mobile: &domain.MobileRecord{Op: "launch", Package: "com.example.shop"}}
	if got := activity.FromAction(launch, effects.Read, "p"); got.Verb != domain.VerbVisit || got.Target != "launch com.example.shop" {
		t.Errorf("launch = %+v", got)
	}
	scroll := domain.Action{Surface: domain.SurfaceAndroid, Op: "scroll", Mobile: &domain.MobileRecord{Op: "scroll", Screen: "List"}}
	if got := activity.FromAction(scroll, effects.Read, "p"); got.Verb != domain.VerbRead {
		t.Errorf("scroll = %+v", got)
	}
	refused := domain.Action{Surface: domain.SurfaceAndroid, Op: "tap", Refused: "read_only: no", Mobile: &domain.MobileRecord{Op: "tap"}}
	if got := activity.FromAction(refused, effects.Write, "p"); got.Result != "refused: read_only: no" {
		t.Errorf("refused = %+v", got)
	}
	failed := domain.Action{Surface: domain.SurfaceAndroid, Op: "tap", Err: "boom", Mobile: &domain.MobileRecord{Op: "tap"}}
	if got := activity.FromAction(failed, effects.Write, "p"); got.Result != "error: boom" {
		t.Errorf("failed = %+v", got)
	}
}
