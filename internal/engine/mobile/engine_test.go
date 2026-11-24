package mobile_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
)

var (
	locSubmit = map[string]domain.Locator{"submit": {Desc: "Sign in"}}
	pay       = func() *mobile.Node { b := button("Pay", 400, 800); b.Desc = "Sign in"; return b }
)

func TestLaunchResetsOnceThenStarts(t *testing.T) {
	r := newRig(t, rigOptions{})
	res := r.do(call("start", domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileLaunch}, nil))
	if res.Action.Err != "" || res.Action.Refused != "" {
		t.Fatalf("action = %+v", res.Action)
	}
	if got := strings.Join(r.d.log[:2], ","); got != "reset,launch" {
		t.Errorf("log = %v", r.d.log)
	}
	m := res.Action.Mobile
	if m.Op != "launch" || m.Package != "com.example.shop" || m.Device != "emulator-5554" || m.App != "shop-app" || res.Action.Surface != domain.SurfaceAndroid {
		t.Errorf("record = %+v", m)
	}
	if res.Action.EffectUnknown {
		t.Error("a launch has no unknown effect")
	}
}

func TestTheAppIsResetOncePerScenarioAndService(t *testing.T) {
	r := newRig(t, rigOptions{})
	launch := domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileLaunch}
	r.do(call("a", launch, nil))
	r.do(call("b", launch, nil))
	r.do(call("c", launch, nil))
	if n := count(r.d.log, "reset"); n != 1 {
		t.Fatalf("resets in one scenario = %d, log %v", n, r.d.log)
	}
	r.e.NewScenario()
	if n := count(r.d.log, "reset"); n != 1 {
		t.Errorf("NewScenario reset at once: %v", r.d.log)
	}
	r.do(call("a", launch, nil))
	if n := count(r.d.log, "reset"); n != 2 {
		t.Errorf("resets after a new scenario = %d, log %v", n, r.d.log)
	}
	if r.p.drivers < 4 {
		t.Errorf("the engine did not ask the platform for the driver each time: %d", r.p.drivers)
	}
}

func count(log []string, what string) int {
	n := 0
	for _, l := range log {
		if l == what {
			n++
		}
	}
	return n
}

func TestAFailedResetIsRetriedAndNamed(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.resetErr = errors.New("am force-stop failed")
	res := r.do(call("a", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if !strings.Contains(res.Action.Err, "the app could not be reset: am force-stop failed") {
		t.Errorf("err = %q", res.Action.Err)
	}
	if count(r.d.log, "launch") != 0 {
		t.Error("the app was launched after a failed reset")
	}
	r.d.resetErr = nil
	r.do(call("b", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if count(r.d.log, "reset") != 2 || count(r.d.log, "launch") != 1 {
		t.Errorf("log = %v", r.d.log)
	}
}

func TestTapTouchesTheCenterOfTheElement(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.screen = screen(pay())
	res := r.do(call("go", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	if res.Action.Err != "" {
		t.Fatalf("err = %q", res.Action.Err)
	}
	if len(r.d.touches) != 1 || r.d.touches[0].From != (mobile.Point{X: 450, Y: 850}) || r.d.touches[0].To != r.d.touches[0].From || r.d.touches[0].Hold != 0 {
		t.Errorf("touches = %+v", r.d.touches)
	}
	if res.Action.Mobile.Target == nil || res.Action.Mobile.Target.Desc != "Sign in" {
		t.Errorf("target = %+v", res.Action.Mobile.Target)
	}
	if res.Action.EffectUnknown {
		t.Error("a tap that worked has no unknown effect")
	}
}

func TestLongPressHolds(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.screen = screen(pay())
	r.do(call("go", act(domain.MobileLongTap, "SignIn", "submit"), locSubmit))
	if len(r.d.touches) != 1 || r.d.touches[0].Hold < 500*time.Millisecond {
		t.Errorf("touches = %+v", r.d.touches)
	}
}

func TestDoubleTapSendsOneGestureOfTwoTaps(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.screen = screen(pay())
	res := r.do(call("go", act(domain.MobileDoubleTap, "SignIn", "submit"), locSubmit))
	if res.Action.Err != "" {
		t.Fatalf("err = %q", res.Action.Err)
	}
	if len(r.d.touches) != 1 || r.d.touches[0].Taps != 2 || r.d.touches[0].From != (mobile.Point{X: 450, Y: 850}) || r.d.touches[0].To != r.d.touches[0].From || r.d.touches[0].Hold != 0 {
		t.Errorf("touches = %+v", r.d.touches)
	}
	if res.Action.Mobile.Op != "doubleTap" {
		t.Errorf("record = %+v", res.Action.Mobile)
	}
}

func dragRig(t *testing.T) (*rig, map[string]domain.Locator) {
	r := newRig(t, rigOptions{})
	slot := button("Slot", 700, 1200)
	slot.Desc = "Slot"
	r.d.screen = screen(pay(), slot)
	return r, map[string]domain.Locator{"submit": {Desc: "Sign in"}, "slot": {Desc: "Slot"}}
}

func TestDragHoldsMovesFromTheElementToTheTarget(t *testing.T) {
	r, locs := dragRig(t)
	a := act(domain.MobileDrag, "SignIn", "submit")
	a.To = "slot"
	res := r.do(call("go", a, locs))
	if res.Action.Err != "" {
		t.Fatalf("err = %q", res.Action.Err)
	}
	if len(r.d.touches) != 1 {
		t.Fatalf("touches = %+v", r.d.touches)
	}
	g := r.d.touches[0]
	if g.From != (mobile.Point{X: 450, Y: 850}) || g.To != (mobile.Point{X: 750, Y: 1250}) || g.Hold != 600*time.Millisecond || g.Move != 500*time.Millisecond || g.Taps != 0 {
		t.Errorf("gesture = %+v", g)
	}
	// A hold in the value replaces the default.
	r, locs = dragRig(t)
	a.Value = "800ms"
	if res = r.do(call("go", a, locs)); res.Action.Err != "" || r.d.touches[0].Hold != 800*time.Millisecond {
		t.Errorf("err = %q, touches = %+v", res.Action.Err, r.d.touches)
	}
	r, locs = dragRig(t)
	a.Value = "forever"
	if _, err := r.e.Do(context.Background(), call("go", a, locs)); err == nil || !strings.Contains(err.Error(), "must be a hold") || len(r.d.touches) != 0 {
		t.Errorf("a bad hold: err = %v, touches = %+v", err, r.d.touches)
	}
}

func TestADragWhoseTargetIsMissingIsAMissWithRepair(t *testing.T) {
	r := newRig(t, rigOptions{})
	slot := button("Slot", 700, 1200)
	slot.Desc = "Slot"
	r.d.screen = screen(pay(), slot)
	a := act(domain.MobileDrag, "SignIn", "submit")
	a.To = "slot"
	res := r.do(call("go", a, map[string]domain.Locator{"submit": {Desc: "Sign in"}, "slot": {Role: "button", Name: "Slott"}}))
	if !strings.HasPrefix(res.Action.Err, check.NotFound) || len(r.d.touches) != 0 || res.Action.EffectUnknown {
		t.Fatalf("err = %q, touches %d, effectUnknown %v", res.Action.Err, len(r.d.touches), res.Action.EffectUnknown)
	}
	if st, ok := res.Action.Mobile.Elements["slot"]; !ok || st.Count != 0 {
		t.Errorf("the miss is not in the record: %+v", res.Action.Mobile.Elements)
	}
	found := false
	for _, c := range res.Action.Mobile.Repair {
		found = found || c.Element == "slot"
	}
	if !found {
		t.Errorf("repair = %+v", res.Action.Mobile.Repair)
	}
}

func TestTypeTapsThenTypesTheValue(t *testing.T) {
	r := newRig(t, rigOptions{})
	field := n("android.widget.EditText", "", 100, 300)
	field.ID = "com.shop:id/email"
	r.d.screen = screen(field)
	a := act(domain.MobileType, "SignIn", "email")
	a.Value = "ana@example.com"
	res := r.do(call("email", a, map[string]domain.Locator{"email": {ID: "email"}}))
	if res.Action.Err != "" {
		t.Fatalf("err = %q", res.Action.Err)
	}
	if got := strings.Join(r.d.log, ","); got != "reset,touch,type" {
		t.Errorf("log = %v", got)
	}
	if len(r.d.typed) != 1 || r.d.typed[0] != "ana@example.com" {
		t.Errorf("typed = %v", r.d.typed)
	}
	if r.clk.sleeps[0] != 100*time.Millisecond {
		t.Errorf("the field was not given a moment to take the focus: %v", r.clk.sleeps)
	}
}

func TestATypedSecretIsReadFromTheSourceAndNeverStored(t *testing.T) {
	const secret = "hunter2-very-secret"
	r := newRig(t, rigOptions{secrets: []string{secret}, resolver: &fakeSecrets{values: map[string]string{"env:SHOP_PASSWORD": secret}}})
	field := n("android.widget.EditText", "", 100, 300)
	field.ID = "com.shop:id/pw"
	r.d.screen = screen(field)
	r.d.onType = func(text string) {
		// The app shows what was typed, as a field that is not a password field does.
		f := n("android.widget.EditText", text, 100, 300)
		f.ID = "com.shop:id/pw"
		r.d.screen = screen(f)
	}
	a := act(domain.MobileType, "SignIn", "password")
	a.Secret = "env:SHOP_PASSWORD"
	c := call("pw", a, map[string]domain.Locator{"password": {ID: "pw"}})
	c.Watch = []domain.Watched{{Element: "password"}}
	res := r.do(c)
	if res.Action.Err != "" {
		t.Fatalf("err = %q", res.Action.Err)
	}
	if r.d.typed[0] != secret {
		t.Errorf("typed = %v", r.d.typed)
	}
	blob := toJSON(t, r.sink.actions) + toJSON(t, res.Action)
	if strings.Contains(blob, secret) {
		t.Errorf("the secret is in what was stored: %s", blob)
	}
}

func TestASecretThatCannotBeReadIsAnErrorAndSendsNothing(t *testing.T) {
	r := newRig(t, rigOptions{resolver: &fakeSecrets{}})
	r.d.screen = screen(n("android.widget.EditText", "", 100, 300))
	a := act(domain.MobileType, "S", "pw")
	a.Secret = "env:NOPE"
	res := r.do(call("pw", a, map[string]domain.Locator{"pw": {Class: "android.widget.EditText"}}))
	if !strings.HasPrefix(res.Action.Err, "the secret could not be read") || len(r.d.touches) != 0 || len(r.d.log) != 0 {
		t.Errorf("err = %q, log = %v", res.Action.Err, r.d.log)
	}
	r2 := newRig(t, rigOptions{})
	res = r2.do(call("pw", a, map[string]domain.Locator{"pw": {Class: "x"}}))
	if !strings.Contains(res.Action.Err, "no secret source") {
		t.Errorf("err = %q", res.Action.Err)
	}
}

func TestPressSendsTheKey(t *testing.T) {
	r := newRig(t, rigOptions{})
	for _, k := range domain.MobileKeys {
		r.do(call("k", domain.MobileAction{Service: "shop-app", Do: domain.MobilePress, Value: k}, nil))
	}
	if len(r.d.keys) != len(domain.MobileKeys) || r.d.keys[0] != mobile.KeyBack || r.d.keys[len(r.d.keys)-1] != mobile.KeyHideKeyboard {
		t.Errorf("keys = %v", r.d.keys)
	}
}

func TestSwipeOnTheScreenAndOnAnElement(t *testing.T) {
	r := newRig(t, rigOptions{})
	card := n("android.widget.FrameLayout", "Card", 100, 500)
	card.Desc = "card"
	card.Bounds = mobile.Rect{Left: 100, Top: 500, Right: 900, Bottom: 700}
	r.d.screen = screen(card)
	r.do(call("s", domain.MobileAction{Service: "shop-app", Do: domain.MobileSwipe, Value: "up"}, nil))
	g := r.d.touches[0]
	if g.From.Y <= g.To.Y || g.From.X != g.To.X || g.From.X != screenW/2 || g.Move == 0 {
		t.Errorf("a swipe up is a finger that goes up: %+v", g)
	}
	r.do(call("s", domain.MobileAction{Service: "shop-app", Do: domain.MobileSwipe, Value: "left", Screen: "Home", Element: "card"}, map[string]domain.Locator{"card": {Desc: "card"}}))
	g = r.d.touches[1]
	if g.From.X <= g.To.X || g.From.Y != 600 || g.From.X > 900 || g.To.X < 100 {
		t.Errorf("a swipe left on the card stays on the card: %+v", g)
	}
	// Each direction.
	for dir, ok := range map[string]func(g mobile.Gesture) bool{
		"up":    func(g mobile.Gesture) bool { return g.To.Y < g.From.Y },
		"down":  func(g mobile.Gesture) bool { return g.To.Y > g.From.Y },
		"left":  func(g mobile.Gesture) bool { return g.To.X < g.From.X },
		"right": func(g mobile.Gesture) bool { return g.To.X > g.From.X },
	} {
		r.do(call("s", domain.MobileAction{Service: "shop-app", Do: domain.MobileSwipe, Value: dir}, nil))
		if g := r.d.touches[len(r.d.touches)-1]; !ok(g) {
			t.Errorf("%s: %+v", dir, g)
		}
	}
}

func TestWaitForAnElementStopsWhenItAppears(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.sources = []*mobile.Tree{screen(), screen(), screen(pay())}
	a := act(domain.MobileWait, "SignIn", "submit")
	res := r.do(call("w", a, locSubmit))
	if res.Action.Err != "" || res.Action.EffectUnknown || len(r.d.touches) != 0 {
		t.Errorf("action = %+v", res.Action)
	}
}

func TestWaitForAnElementThatNeverComesIsNotFound(t *testing.T) {
	r := newRig(t, rigOptions{})
	a := act(domain.MobileWait, "SignIn", "submit")
	res := r.do(call("w", a, locSubmit))
	if !strings.HasPrefix(res.Action.Err, check.NotFound) || !strings.Contains(res.Action.Err, "element submit matched 0") {
		t.Errorf("err = %q", res.Action.Err)
	}
	var waited time.Duration
	for _, s := range r.clk.sleeps {
		waited += s
	}
	if waited < 10*time.Second || waited > 20*time.Second {
		t.Errorf("a wait lasts about 10 seconds, and this one waited %s", waited)
	}
	st := res.Action.Mobile.Elements["submit"]
	if st.Count != 0 || st.Locator == nil {
		t.Errorf("the state of the miss = %+v", st)
	}
}

func TestWaitWithNoElementOnlyLetsTheScreenSettle(t *testing.T) {
	r := newRig(t, rigOptions{})
	res := r.do(call("w", domain.MobileAction{Service: "shop-app", Do: domain.MobileWait}, nil))
	if res.Action.Err != "" || len(r.d.touches) != 0 {
		t.Errorf("action = %+v", res.Action)
	}
}

func TestOpenAHTTPLinkIsAdmittedByThePolicy(t *testing.T) {
	r := newRig(t, rigOptions{})
	open := domain.MobileAction{Service: "shop-app", Do: domain.MobileOpenLink, Link: "https://shop.example.com/cart/7?x=1"}
	res := r.do(call("o", open, nil))
	if res.Action.Refused != "" || res.Action.Err != "" || len(r.d.links) != 1 || r.d.links[0] != open.Link {
		t.Errorf("action = %+v, links %v", res.Action, r.d.links)
	}
	for name, link := range map[string]string{
		"a host that is not listed": "https://evil.example.org/x",
		"an address with a port":    "https://shop.example.com:8443/x",
	} {
		r2 := newRig(t, rigOptions{})
		res := r2.do(call("o", domain.MobileAction{Service: "shop-app", Do: domain.MobileOpenLink, Link: link}, nil))
		if !strings.HasPrefix(res.Action.Refused, "host_not_allowed") || len(r2.d.links) != 0 || len(r2.d.log) != 0 {
			t.Errorf("%s: refused = %q, log = %v", name, res.Action.Refused, r2.d.log)
		}
	}
	// An http link is a GET: the methods apply, and this environment allows GET only.
	r3 := newRig(t, rigOptions{})
	res = r3.do(call("o", domain.MobileAction{Service: "shop-app", Do: domain.MobileOpenLink, Link: "http://shop.example.com/a"}, nil))
	if res.Action.Refused != "" || len(r3.d.links) != 1 {
		t.Errorf("a GET to a host of the environment: %q", res.Action.Refused)
	}
}

func TestOpenACustomSchemeNeedsTheServiceToListIt(t *testing.T) {
	r := newRig(t, rigOptions{schemes: []string{"shop"}})
	res := r.do(call("o", domain.MobileAction{Service: "shop-app", Do: domain.MobileOpenLink, Link: "shop://cart/7"}, nil))
	if res.Action.Refused != "" || len(r.d.links) != 1 {
		t.Errorf("a listed scheme: %+v", res.Action)
	}
	for name, c := range map[string]struct{ link, want string }{
		"an unlisted scheme": {"other://x", "the scheme other is not in the schemes of service shop-app"},
		"a file link":        {"file:///data/x", "the scheme file cannot be opened"},
		"an intent link":     {"intent://x#Intent;end", "the scheme intent cannot be opened"},
		"a javascript link":  {"javascript:alert(1)", "the scheme javascript cannot be opened"},
		"a content link":     {"content://sms/inbox", "the scheme content cannot be opened"},
		"no scheme":          {"cart/7", "not a URL with a scheme"},
		"a space":            {"shop://a b", "space or a control character"},
		"a newline":          {"shop://a\nb", "space or a control character"},
		"an empty host":      {"", "needs the link"},
	} {
		r := newRig(t, rigOptions{schemes: []string{"shop", "file", "intent", "javascript", "content"}})
		a := domain.MobileAction{Service: "shop-app", Do: domain.MobileOpenLink, Link: c.link}
		if c.link == "" {
			if _, err := r.e.Do(context.Background(), call("o", a, nil)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: error %v", name, err)
			}
			continue
		}
		res := r.do(call("o", a, nil))
		if !strings.Contains(res.Action.Refused, c.want) || len(r.d.log) != 0 {
			t.Errorf("%s: refused = %q, log = %v", name, res.Action.Refused, r.d.log)
		}
	}
}

func TestEveryActionIsRefusedInAReadOnlyOrProductionEnvironment(t *testing.T) {
	for name, o := range map[string]rigOptions{"read only": {readOnly: true}, "production": {kind: config.KindProduction}} {
		for _, do := range []domain.MobileOp{domain.MobileLaunch, domain.MobileOpenLink, domain.MobileTap, domain.MobileLongTap, domain.MobileDoubleTap, domain.MobileDrag, domain.MobileType,
			domain.MobileSwipe, domain.MobileScroll, domain.MobilePress, domain.MobileWait} {
			r := newRig(t, o)
			r.d.screen = screen(pay())
			a := domain.MobileAction{Service: "shop-app", Do: do}
			switch do {
			case domain.MobileOpenLink:
				a.Link = "https://shop.example.com/"
			case domain.MobileTap, domain.MobileLongTap, domain.MobileDoubleTap, domain.MobileScroll:
				a.Screen, a.Element = "S", "submit"
			case domain.MobileDrag:
				a.Screen, a.Element, a.To = "S", "submit", "submit"
			case domain.MobileType:
				a.Screen, a.Element, a.Value = "S", "submit", "x"
			case domain.MobileSwipe:
				a.Value = "up"
			case domain.MobilePress:
				a.Value = "back"
			}
			res := r.do(call("x", a, locSubmit))
			if res.Action.Refused == "" || res.Action.Err != "" {
				t.Errorf("%s %s: refused = %q, err = %q", name, do, res.Action.Refused, res.Action.Err)
			}
			if len(r.d.log) != 0 || r.p.drivers != 0 {
				t.Errorf("%s %s: the device was used: %v, drivers %d", name, do, r.d.log, r.p.drivers)
			}
			if name == "read only" && !strings.HasPrefix(res.Action.Refused, "read_only: ") {
				t.Errorf("%s %s: refused = %q", name, do, res.Action.Refused)
			}
		}
	}
}

func TestAWriteNeedsAUseCase(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.screen = screen(pay())
	c := call("x", act(domain.MobileTap, "S", "submit"), locSubmit)
	c.Use = domain.UseCase{}
	res := r.do(c)
	if !strings.Contains(res.Action.Refused, "a write must belong to a scenario step") || len(r.d.log) != 0 {
		t.Errorf("refused = %q", res.Action.Refused)
	}
	// A read needs none.
	c = call("x", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil)
	c.Use = domain.UseCase{}
	if res := r.do(c); res.Action.Refused != "" {
		t.Errorf("a launch was refused: %q", res.Action.Refused)
	}
}

func TestTheBudgetEndsTheRun(t *testing.T) {
	r := newRig(t, rigOptions{budget: 2})
	launch := call("x", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil)
	for i := 0; i < 2; i++ {
		if res := r.do(launch); res.Action.Refused != "" {
			t.Fatalf("action %d refused: %q", i, res.Action.Refused)
		}
	}
	if res := r.do(launch); res.Action.Refused != "action budget reached" {
		t.Errorf("refused = %q", res.Action.Refused)
	}
}

func TestRefillingTheBudgetLetsTheNextReplayRun(t *testing.T) {
	r := newRig(t, rigOptions{budget: 1})
	launch := call("x", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil)
	if res := r.do(launch); res.Action.Refused != "" {
		t.Fatalf("first action refused: %q", res.Action.Refused)
	}
	if res := r.do(launch); res.Action.Refused != "action budget reached" {
		t.Fatalf("refused = %q", res.Action.Refused)
	}
	r.e.RefillBudget()
	if res := r.do(launch); res.Action.Refused != "" {
		t.Errorf("an action after a refill was refused: %q", res.Action.Refused)
	}
}

func TestAMalformedActionAndAnUnknownServiceAreErrors(t *testing.T) {
	r := newRig(t, rigOptions{})
	if _, err := r.e.Do(context.Background(), call("x", domain.MobileAction{Service: "shop-app", Do: domain.MobileTap}, nil)); err == nil || !strings.Contains(err.Error(), "tap needs the element") {
		t.Errorf("error = %v", err)
	}
	if _, err := r.e.Do(context.Background(), call("x", domain.MobileAction{Service: "nobody", Do: domain.MobileLaunch}, nil)); err == nil || !strings.Contains(err.Error(), `unknown service "nobody"`) {
		t.Errorf("error = %v", err)
	}
	if len(r.sink.actions) != 0 {
		t.Error("an action was stored")
	}
}

func TestAnUnavailablePlatformBlocksWithItsReason(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.p.err = &mobile.Unavailable{Reason: "adb was not found: install the Android platform tools"}
	_, err := r.e.Do(context.Background(), call("x", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	var un *mobile.Unavailable
	if !errors.As(err, &un) || un.Reason != "adb was not found: install the Android platform tools" {
		t.Fatalf("error = %v", err)
	}
	if len(r.sink.actions) != 0 {
		t.Error("an action was stored for a device that is not there")
	}
	// Any other failure of the platform is an error of the action.
	r.p.err = errors.New("the server crashed\nsecond line")
	res := r.do(call("x", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if res.Action.Err != "the server crashed" {
		t.Errorf("err = %q", res.Action.Err)
	}
}

func TestAnElementThatIsNotThereKeepsTheStateOfBeforeTheAction(t *testing.T) {
	r := newRig(t, rigOptions{})
	// The screen has a button whose text is close to what the locator looks for.
	signIn := button("Sign in", 400, 800)
	r.d.screen = screen(signIn)
	a := act(domain.MobileTap, "SignIn", "submit")
	c := call("go", a, map[string]domain.Locator{"submit": {Role: "button", Name: "Sign-in"}})
	c.Watch = []domain.Watched{{Element: "welcome"}}
	c.Locators["welcome"] = domain.Locator{Desc: "welcome"}
	res := r.do(c)
	if !strings.HasPrefix(res.Action.Err, check.NotFound) || len(r.d.touches) != 0 || res.Action.EffectUnknown {
		t.Fatalf("err = %q, touches %d, effectUnknown %v", res.Action.Err, len(r.d.touches), res.Action.EffectUnknown)
	}
	st, ok := res.Action.Mobile.Elements["submit"]
	if !ok || st.Count != 0 || st.Locator == nil || st.Locator.Name != "Sign-in" {
		t.Errorf("the miss is not in the record: %+v", res.Action.Mobile.Elements)
	}
	if len(res.Action.Mobile.Repair) == 0 || res.Action.Mobile.Repair[0].Element != "submit" ||
		res.Action.Mobile.Repair[0].Locator.Name != "Sign in" || res.Action.Mobile.Repair[0].Locator.Role != "button" {
		t.Errorf("repair = %+v", res.Action.Mobile.Repair)
	}
	if _, has := res.Action.Mobile.Elements["welcome"]; !has {
		t.Error("a watched element was not read")
	}
}

func TestTheMissIsTheStateBeforeAndNotAfterWhenTheScreenChanges(t *testing.T) {
	// A tap that works makes its button disappear. A later read must not make the tap a miss.
	r := newRig(t, rigOptions{})
	r.d.screen = screen(pay())
	r.d.onTouch = func(mobile.Gesture) { r.d.screen = screen() }
	c := call("go", act(domain.MobileTap, "S", "submit"), locSubmit)
	res := r.do(c)
	if res.Action.Err != "" {
		t.Errorf("err = %q", res.Action.Err)
	}
	if _, has := res.Action.Mobile.Elements["submit"]; has {
		t.Errorf("an element that only the action named is in the record: %+v", res.Action.Mobile.Elements)
	}
	if len(res.Action.Mobile.Repair) != 0 {
		t.Errorf("repair = %+v", res.Action.Mobile.Repair)
	}
}

func TestSeveralMatchesAreNotAnElementAndAreNamed(t *testing.T) {
	r := newRig(t, rigOptions{})
	a1, a2 := button("Add", 100, 500), button("Add", 100, 800)
	a1.ID, a2.ID = "com.shop:id/add1", "com.shop:id/add2"
	r.d.screen = screen(a1, a2)
	c := call("go", act(domain.MobileTap, "Cart", "add"), map[string]domain.Locator{"add": {Role: "button", Name: "Add"}})
	res := r.do(c)
	if !strings.Contains(res.Action.Err, "element add matched 2") || len(r.d.touches) != 0 {
		t.Fatalf("err = %q, touches %d", res.Action.Err, len(r.d.touches))
	}
	// Watched elements name the matches, so that a check says which they are.
	c.Watch = []domain.Watched{{Element: "add"}}
	res = r.do(c)
	if st := res.Action.Mobile.Elements["add"]; st.Count != 2 || st.Found {
		t.Errorf("state = %+v", st)
	}
	names := res.Action.Mobile.Repair
	if len(names) != 2 || names[0].Locator.ID != "com.shop:id/add1" || names[1].Locator.ID != "com.shop:id/add2" {
		t.Errorf("repair = %+v", names)
	}
}

func TestWithinLimitsTheSearch(t *testing.T) {
	r := newRig(t, rigOptions{})
	row := func(text string, y int) *mobile.Node { return n("android.widget.TextView", text, 100, y) }
	list := n("androidx.recyclerview.widget.RecyclerView", "", 0, 400, row("Total", 500), row("Row", 700))
	list.Desc = "list"
	list.Bounds = mobile.Rect{Left: 0, Top: 400, Right: 1080, Bottom: 1500}
	footer := n("android.widget.TextView", "Total", 100, 1800)
	r.d.screen = screen(list, footer)
	locs := map[string]domain.Locator{"list": {Desc: "list"}, "total": {Text: "Total", Within: "list"}, "any": {Text: "Total"}}
	c := call("go", domain.MobileAction{Service: "shop-app", Do: domain.MobileWait}, locs)
	c.Watch = []domain.Watched{{Element: "total"}, {Element: "any"}}
	res := r.do(c)
	if st := res.Action.Mobile.Elements["total"]; !st.Found || st.Count != 1 || st.Text != "Total" {
		t.Errorf("total = %+v", st)
	}
	if st := res.Action.Mobile.Elements["any"]; st.Found || st.Count != 2 {
		t.Errorf("without within the text is on two nodes: %+v", st)
	}
	// An element inside one that is not there is not there.
	r.d.screen = screen(footer)
	res = r.do(c)
	if st := res.Action.Mobile.Elements["total"]; st.Count != 0 || st.Found {
		t.Errorf("total without its list = %+v", st)
	}
}

func TestCapturesTakeTheTextOfAFoundElement(t *testing.T) {
	r := newRig(t, rigOptions{})
	total := n("android.widget.TextView", "Total: 12.50", 100, 500)
	total.ID = "com.shop:id/total"
	r.d.screen = screen(total)
	c := call("read", domain.MobileAction{Service: "shop-app", Do: domain.MobileWait}, map[string]domain.Locator{"total": {ID: "total"}, "gone": {ID: "gone"}})
	c.Watch = []domain.Watched{{Element: "total"}, {Element: "gone"}}
	c.Captures = []domain.Capture{{Name: "amount", Element: "total"}, {Name: "nothing", Element: "gone"}}
	res := r.do(c)
	if res.Captured["amount"] != "Total: 12.50" || len(res.Missing) != 1 || res.Missing[0] != "nothing" {
		t.Errorf("captured = %v, missing = %v", res.Captured, res.Missing)
	}
	if res.Action.Captures["amount"] != "Total: 12.50" {
		t.Errorf("the stored action has captures %v", res.Action.Captures)
	}
}

func TestACapturePatternTakesAGroupAndNoMatchIsMissing(t *testing.T) {
	r := newRig(t, rigOptions{})
	ref := n("android.widget.TextView", "Order #A1B2 placed", 100, 500)
	ref.ID = "com.shop:id/ref"
	r.d.screen = screen(ref)
	c := call("read", domain.MobileAction{Service: "shop-app", Do: domain.MobileWait}, map[string]domain.Locator{"ref": {ID: "ref"}})
	c.Watch = []domain.Watched{{Element: "ref"}}
	c.Captures = []domain.Capture{
		{Name: "orderRef", Element: "ref", Pattern: `Order #([A-Z0-9]+)`},
		{Name: "whole", Element: "ref", Pattern: `#[A-Z0-9]+`},
		{Name: "nomatch", Element: "ref", Pattern: `Invoice (\d+)`},
	}
	res := r.do(c)
	if res.Captured["orderRef"] != "A1B2" || res.Captured["whole"] != "#A1B2" || len(res.Missing) != 1 || res.Missing[0] != "nomatch" {
		t.Errorf("captured = %v, missing = %v", res.Captured, res.Missing)
	}
	if res.Action.Captures["orderRef"] != "A1B2" {
		t.Errorf("the stored action has captures %v", res.Action.Captures)
	}
}

func TestACheckWaitsForItsConditionAndTheEngineRecordsTheLastReading(t *testing.T) {
	r := newRig(t, rigOptions{})
	welcome := n("android.widget.TextView", "Hi Ana", 100, 500)
	welcome.Desc = "welcome"
	// The screen settles on the first two reads, and the greeting comes two reads later.
	r.d.sources = []*mobile.Tree{screen(), screen(), screen(), screen(welcome)}
	c := call("go", domain.MobileAction{Service: "shop-app", Do: domain.MobileWait}, map[string]domain.Locator{"welcome": {Desc: "welcome"}})
	c.Watch = []domain.Watched{{Element: "welcome"}}
	c.Until = []domain.Check{{Element: "welcome", Equals: "Hi Ana"}}
	res := r.do(c)
	if st := res.Action.Mobile.Elements["welcome"]; !st.Found || st.Text != "Hi Ana" {
		t.Errorf("welcome = %+v", st)
	}
}

func TestACheckThatNeverHoldsStopsAtTheCheckTimeoutWithTheLastReading(t *testing.T) {
	r := newRig(t, rigOptions{}, func(o *mobile.Options) { o.CheckTimeout = 2 * time.Second })
	welcome := n("android.widget.TextView", "Hi Bob", 100, 500)
	welcome.Desc = "welcome"
	r.d.screen = screen(welcome)
	c := call("go", domain.MobileAction{Service: "shop-app", Do: domain.MobileWait}, map[string]domain.Locator{"welcome": {Desc: "welcome"}})
	c.Watch = []domain.Watched{{Element: "welcome"}}
	c.Until = []domain.Check{{Element: "welcome", Equals: "Hi Ana"}}
	start := r.clk.Now()
	res := r.do(c)
	if res.Action.Err != "" {
		t.Errorf("a check that fails is no error of the action: %q", res.Action.Err)
	}
	if st := res.Action.Mobile.Elements["welcome"]; st.Text != "Hi Bob" {
		t.Errorf("welcome = %+v", st)
	}
	if took := r.clk.Now().Sub(start); took < 2*time.Second || took > 4*time.Second {
		t.Errorf("the check waited %s", took)
	}
}

func TestAnAbsenceCheckSettlesOnceMoreBeforeItReads(t *testing.T) {
	r := newRig(t, rigOptions{})
	spinner := n("android.widget.ProgressBar", "", 100, 500)
	spinner.Desc = "spinner"
	r.d.screen = screen()
	c := call("go", domain.MobileAction{Service: "shop-app", Do: domain.MobileWait}, map[string]domain.Locator{"spinner": {Desc: "spinner"}})
	c.Watch = []domain.Watched{{Element: "spinner", Presence: true}}
	no := false
	c.Until = []domain.Check{{Element: "spinner", Exists: &no}}
	before := r.d.sourceCalls
	res := r.do(c)
	if st := res.Action.Mobile.Elements["spinner"]; st.Count != 0 || !st.Presence {
		t.Errorf("spinner = %+v", st)
	}
	// reset aside: settle (2 reads), read until (0 more sources: it reuses the settled tree), settle again (2 reads).
	if got := r.d.sourceCalls - before; got < 4 {
		t.Errorf("source calls = %d, want the extra settle after an absence check", got)
	}
}

func TestAWriteThatFailedAfterItWasSentHasAnUnknownEffect(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.screen = screen(pay())
	r.d.touchErr = errors.New("the server dropped the connection\nsecond line with the body {\"secret\":1}")
	res := r.do(call("go", act(domain.MobileTap, "S", "submit"), locSubmit))
	if res.Action.Err != "the server dropped the connection" || !res.Action.EffectUnknown {
		t.Errorf("err = %q, effectUnknown = %v", res.Action.Err, res.Action.EffectUnknown)
	}
	// A failure before anything was sent is known.
	r2 := newRig(t, rigOptions{})
	r2.d.sourceErr = errors.New("source failed")
	res = r2.do(call("go", act(domain.MobileTap, "S", "submit"), locSubmit))
	if res.Action.Err == "" || res.Action.EffectUnknown {
		t.Errorf("err = %q, effectUnknown = %v", res.Action.Err, res.Action.EffectUnknown)
	}
	// A read has no write to be unknown about.
	r3 := newRig(t, rigOptions{})
	r3.d.launchErr = errors.New("am start failed")
	res = r3.do(call("go", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if res.Action.Err != "am start failed" || res.Action.EffectUnknown {
		t.Errorf("err = %q, effectUnknown = %v", res.Action.Err, res.Action.EffectUnknown)
	}
	// A type that failed after the tap went out is unknown too.
	r4 := newRig(t, rigOptions{})
	field := n("android.widget.EditText", "", 100, 300)
	field.Desc = "email"
	r4.d.screen = screen(field)
	r4.d.typeErr = errors.New("no focused element")
	a := act(domain.MobileType, "S", "email")
	a.Value = "x"
	res = r4.do(call("go", a, map[string]domain.Locator{"email": {Desc: "email"}}))
	if !res.Action.EffectUnknown || res.Action.Err != "no focused element" {
		t.Errorf("err = %q, effectUnknown = %v", res.Action.Err, res.Action.EffectUnknown)
	}
}

func TestAnInterruptedRunRecordsTheActionAndReturnsTheError(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.screen = screen(pay())
	ctx, cancel := context.WithCancel(context.Background())
	r.d.onTouch = func(mobile.Gesture) { cancel() }
	res, err := r.e.Do(ctx, call("go", act(domain.MobileTap, "S", "submit"), locSubmit))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if res.Action.Err != "the run was interrupted" {
		t.Errorf("action = %+v", res.Action)
	}
	if len(r.sink.actions) != 1 {
		t.Errorf("stored %d actions", len(r.sink.actions))
	}
	// A context that is cancelled already stores nothing and touches nothing.
	r2 := newRig(t, rigOptions{})
	_, err = r2.e.Do(ctx, call("go", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if !errors.Is(err, context.Canceled) || len(r2.d.log) != 0 || len(r2.sink.actions) != 0 {
		t.Errorf("error = %v, log = %v", err, r2.d.log)
	}
}

func TestAnActionThatTakesTooLongIsAnError(t *testing.T) {
	r := newRig(t, rigOptions{}, func(o *mobile.Options) { o.ActionTimeout = 20 * time.Millisecond })
	r.d.sourceErr = nil
	slow := &slowDriver{fakeDriver: r.d}
	r.p.d = r.d
	r.e = mobile.New(mobile.Options{Platform: &slowPlatform{slow}, Recorder: recorderOf(t, r), Files: r.files, Services: map[string]mobile.Service{"shop-app": {}},
		ActionTimeout: 20 * time.Millisecond, Policy: policyOf(t)})
	res := r.do(call("x", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if res.Action.Err != "the action took too long" {
		t.Errorf("err = %q", res.Action.Err)
	}
}

func TestClosePassesOnToThePlatformAndIsSafeTwice(t *testing.T) {
	r := newRig(t, rigOptions{})
	if err := r.e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.p.closeErr = errors.New("cleanup failed")
	if err := r.e.Close(context.Background()); err == nil || r.p.closed != 2 {
		t.Errorf("error = %v, closed %d", err, r.p.closed)
	}
}

// diagPlatform is a platform that can say why the app stopped.
type diagPlatform struct {
	*fakePlatform
	mu          sync.Mutex
	crashes     []mobile.Crash
	crashErr    error
	front       string
	frontErr    error
	tail        string
	asked       int
	tails       int
	foregrounds int
	since       time.Time
	pkg         string
}

func (p *diagPlatform) Crashes(_ context.Context, pkg string, since time.Time) ([]mobile.Crash, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked++
	p.since, p.pkg = since, pkg
	return p.crashes, p.crashErr
}

func (p *diagPlatform) LogTail(_ context.Context, _ string, lines int) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tails++
	if lines != 200 {
		return "", errors.New("wrong number of lines")
	}
	return p.tail, nil
}

func (p *diagPlatform) Foreground(context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.foregrounds++
	return p.front, p.frontErr
}

const crashSecret = "s3cr3t-token-value"

func diagRig(t *testing.T, tune ...func(*mobile.Options)) (*rig, *diagPlatform) {
	t.Helper()
	var dp *diagPlatform
	tune = append([]func(*mobile.Options){func(o *mobile.Options) {
		dp.fakePlatform = o.Platform.(*fakePlatform)
		o.Platform = dp
	}}, tune...)
	dp = &diagPlatform{front: "com.example.shop", crashes: []mobile.Crash{{
		ID: "data_app_crash@1", Process: "com.example.shop", Time: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
		Summary: "java.lang.IllegalStateException: Cart is empty, token=" + crashSecret,
		Body:    "java.lang.IllegalStateException: Cart is empty\n\tat com.example.shop.Cart.checkout(Cart.kt:88)\nAuthorization: Bearer " + crashSecret,
	}}}
	r := newRig(t, rigOptions{secrets: []string{crashSecret}}, tune...)
	return r, dp
}

func TestAFailedActionGetsTheCrashOfTheApp(t *testing.T) {
	r, dp := diagRig(t)
	r.d.screen = screen(pay())
	r.d.touchErr = errors.New("the server stopped")
	res := r.do(call("pay", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	if res.Action.Err == "" {
		t.Fatal("the action did not fail")
	}
	m := res.Action.Mobile
	if m.Crash == nil || m.Crash.File != "crash-pay.txt" || !strings.Contains(m.Crash.Summary, "IllegalStateException: Cart is empty") {
		t.Fatalf("crash = %+v", m.Crash)
	}
	if strings.Contains(m.Crash.Summary, crashSecret) {
		t.Errorf("the summary holds the secret: %q", m.Crash.Summary)
	}
	body := string(r.files.files["crash-pay.txt"])
	if !strings.Contains(body, "Cart.checkout(Cart.kt:88)") || !strings.Contains(body, "IllegalStateException") {
		t.Errorf("crash file = %q", body)
	}
	if strings.Contains(body, crashSecret) {
		t.Errorf("the crash file holds the secret: %q", body)
	}
	if m.LogcatFile != "" || dp.tails != 0 {
		t.Errorf("the log was read without the option: %q, %d", m.LogcatFile, dp.tails)
	}
	if want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); !dp.since.Equal(want) {
		t.Errorf("since = %s, want the start of the run %s", dp.since, want)
	}
	if dp.pkg != "com.example.shop" {
		t.Errorf("pkg = %q", dp.pkg)
	}
	if dp.foregrounds != 0 {
		t.Error("the foregroundsd was read for an action that failed")
	}
}

func TestACrashIsReportedOnlyOnce(t *testing.T) {
	r, dp := diagRig(t)
	r.d.screen = screen(pay())
	r.d.touchErr = errors.New("the server stopped")
	first := r.do(call("one", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	second := r.do(call("two", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	if first.Action.Mobile.Crash == nil {
		t.Fatal("no crash on the first step")
	}
	if second.Action.Mobile.Crash != nil {
		t.Errorf("the same crash was reported twice: %+v", second.Action.Mobile.Crash)
	}
	if _, ok := r.files.files["crash-two.txt"]; ok {
		t.Error("a file was written for no crash")
	}
	dp.crashes = append(dp.crashes, mobile.Crash{ID: "data_app_crash@2", Summary: "second", Body: "second body"})
	third := r.do(call("one", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	if c := third.Action.Mobile.Crash; c == nil || c.Summary != "second" || c.File != "crash-one-2.txt" {
		t.Errorf("third = %+v", c)
	}
}

func TestAMissingTargetLooksForACrashOnlyWhenTheAppLeftTheFront(t *testing.T) {
	// The app is in front: the screen differs, and nothing is asked of the device log.
	r, dp := diagRig(t)
	r.d.screen = screen()
	res := r.do(call("go", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	if !strings.HasPrefix(res.Action.Err, "an element was not found") {
		t.Fatalf("err = %q", res.Action.Err)
	}
	if res.Action.Mobile.Crash != nil || dp.asked != 0 || dp.foregrounds != 1 {
		t.Errorf("crash = %+v, asked %d, foregroundsd %d", res.Action.Mobile.Crash, dp.asked, dp.foregrounds)
	}

	// The launcher is in front: the app is gone, and the crash is the evidence.
	r2, dp2 := diagRig(t)
	dp2.front = ""
	r2.d.screen = screen()
	res = r2.do(call("go", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	if res.Action.Mobile.Crash == nil || dp2.asked != 1 {
		t.Fatalf("crash = %+v, asked %d", res.Action.Mobile.Crash, dp2.asked)
	}
	if !strings.HasPrefix(res.Action.Err, "an element was not found") {
		t.Errorf("the crash changed the error: %q", res.Action.Err)
	}

	// A foregroundsd that cannot be read is no proof that the app left.
	r3, dp3 := diagRig(t)
	dp3.frontErr = errors.New("dumpsys failed")
	r3.d.screen = screen()
	res = r3.do(call("go", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	if res.Action.Mobile.Crash != nil || dp3.asked != 0 {
		t.Errorf("crash = %+v, asked %d", res.Action.Mobile.Crash, dp3.asked)
	}
}

func TestASuccessfulStepAsksNothing(t *testing.T) {
	r, dp := diagRig(t)
	r.d.screen = screen(pay())
	res := r.do(call("go", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	if res.Action.Err != "" || res.Action.Mobile.Crash != nil || dp.asked != 0 || dp.foregrounds != 0 {
		t.Errorf("err %q crash %+v asked %d foregroundsd %d", res.Action.Err, res.Action.Mobile.Crash, dp.asked, dp.foregrounds)
	}
}

func TestLogcatOnFailureWritesTheTail(t *testing.T) {
	r, dp := diagRig(t, func(o *mobile.Options) { o.LogcatOnFailure = true })
	dp.tail = "11-23 10:15:30.120  1  1 E AndroidRuntime: FATAL EXCEPTION\n11-23 10:15:30.121  1  1 I Auth: bearer " + crashSecret
	r.d.screen = screen(pay())
	r.d.touchErr = errors.New("the server stopped")
	res := r.do(call("pay", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	m := res.Action.Mobile
	if m.LogcatFile != "logcat-pay.txt" || m.Crash == nil {
		t.Fatalf("record = %+v", m)
	}
	got := string(r.files.files["logcat-pay.txt"])
	if !strings.Contains(got, "FATAL EXCEPTION") || strings.Contains(got, crashSecret) {
		t.Errorf("log file = %q", got)
	}

	// With no crash the log is still written: it is the evidence that there is.
	r2, dp2 := diagRig(t, func(o *mobile.Options) { o.LogcatOnFailure = true })
	dp2.crashes = nil
	dp2.tail = "I App: nothing special"
	r2.d.screen = screen(pay())
	r2.d.touchErr = errors.New("the server stopped")
	res = r2.do(call("pay", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	if res.Action.Mobile.Crash != nil || res.Action.Mobile.LogcatFile != "logcat-pay.txt" {
		t.Errorf("record = %+v", res.Action.Mobile)
	}
}

func TestADiagnosisThatFailsLeavesTheStepAlone(t *testing.T) {
	r, dp := diagRig(t)
	dp.crashErr = errors.New("adb is gone")
	r.d.screen = screen(pay())
	r.d.touchErr = errors.New("the server stopped")
	res := r.do(call("pay", act(domain.MobileTap, "SignIn", "submit"), locSubmit))
	if res.Action.Mobile.Crash != nil || res.Action.Err == "" {
		t.Errorf("record = %+v, err %q", res.Action.Mobile, res.Action.Err)
	}
}
