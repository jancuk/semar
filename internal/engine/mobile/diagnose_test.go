package mobile_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
)

// recovering is a platform that can restart its server. The first driver is the one that the server
// that died gave, and Recover swaps it for a working one.
type recovering struct {
	mu          sync.Mutex
	dead, alive *fakeDriver
	restarted   bool
	healthy     bool
	healthCalls int
	recovers    int
	recoverErr  error
}

func (p *recovering) Surface() domain.Surface { return domain.SurfaceAndroid }
func (p *recovering) Driver(context.Context, string) (mobile.Driver, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.restarted {
		return p.alive, nil
	}
	return p.dead, nil
}
func (p *recovering) Close(context.Context) error { return nil }
func (p *recovering) Healthy(context.Context) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.healthCalls++
	return p.healthy
}
func (p *recovering) Recover(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recovers++
	if p.recoverErr != nil {
		return p.recoverErr
	}
	p.restarted = true
	return nil
}

// recoverRig is a rig whose platform restarts. The screen shows a Sign in button.
func recoverRig(t *testing.T, healthy bool) (*rig, *recovering) {
	t.Helper()
	about := mobile.About{Device: "emulator-5554", Package: "com.example.shop"}
	rp := &recovering{
		dead:    &fakeDriver{screen: screen(pay()), about: about},
		alive:   &fakeDriver{screen: screen(pay()), about: about},
		healthy: healthy,
	}
	r := newRig(t, rigOptions{}, func(o *mobile.Options) { o.Platform = rp })
	return r, rp
}

func tapSubmit(r *rig, step string) domain.Action {
	r.t.Helper()
	return r.do(call(step, act(domain.MobileTap, "SignIn", "submit"), locSubmit)).Action
}

func TestAMissRecordsTheClosestVisibleElements(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.screen = screen(button("Sign in", 100, 100), button("Sign up", 100, 300), button("Help", 100, 500), button("About", 100, 700),
		button("Terms", 100, 900), button("Privacy", 100, 1100), button("Contact", 100, 1300))
	locs := map[string]domain.Locator{"submit": {Role: "button", Name: "Sign inn"}}
	a := r.do(call("go", act(domain.MobileTap, "SignIn", "submit"), locs)).Action
	near := a.Mobile.Nearby
	if len(near) != 5 {
		t.Fatalf("nearby = %d elements, want 5: %+v", len(near), near)
	}
	if near[0].Locator.Name != "Sign in" || near[0].Element != "submit" {
		t.Errorf("the closest element is %+v, want the Sign in button named for submit", near[0])
	}
	if !strings.Contains(near[0].Why, `"Sign in"`) {
		t.Errorf("why = %q", near[0].Why)
	}
	if a.Mobile.Elements["submit"].Count != 0 {
		t.Errorf("the miss is not in the elements: %+v", a.Mobile.Elements)
	}
}

func TestACheckOnAnElementThatIsMissingRecordsTheClosestToo(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.screen = screen(button("Sign in", 100, 100), button("Help", 100, 300))
	c := call("go", domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileWait, Screen: "Home"}, map[string]domain.Locator{"welcome": {Text: "Sign in now"}})
	c.Watch = []domain.Watched{{Element: "welcome"}}
	a := r.do(c).Action
	if len(a.Mobile.Nearby) == 0 || a.Mobile.Nearby[0].Element != "welcome" {
		t.Fatalf("nearby = %+v", a.Mobile.Nearby)
	}
}

func TestASecretIsNeverInTheNearbyElements(t *testing.T) {
	const secretValue = "s3cr3t-token-77"
	r := newRig(t, rigOptions{secrets: []string{secretValue}})
	leak := button("Welcome "+secretValue, 100, 100)
	field := n("android.widget.EditText", secretValue, 100, 300)
	field.Password = true
	field.Role = "textbox"
	r.d.screen = screen(leak, field, button("Sign in", 100, 500))
	locs := map[string]domain.Locator{"submit": {Role: "button", Name: "Welcome " + secretValue + "x"}}
	a := r.do(call("go", act(domain.MobileTap, "SignIn", "submit"), locs)).Action
	if len(a.Mobile.Nearby) == 0 {
		t.Fatal("the miss has no nearby elements, so the test proves nothing")
	}
	if got := toJSON(t, a); strings.Contains(got, secretValue) {
		t.Errorf("the stored action holds the secret: %s", got)
	}
}

func TestADeadServerIsRestartedOnceAndTheStepKeepsOneAction(t *testing.T) {
	r, rp := recoverRig(t, false)
	rp.dead.sourceErr = errors.New("the server could not be reached")
	a := tapSubmit(r, "go")
	if a.Err != "" {
		t.Fatalf("err = %q", a.Err)
	}
	if rp.recovers != 1 || !a.Mobile.Recovered {
		t.Errorf("recovers = %d, recovered = %v", rp.recovers, a.Mobile.Recovered)
	}
	if len(r.sink.actions) != 1 {
		t.Errorf("the step has %d actions, want 1", len(r.sink.actions))
	}
	if len(rp.alive.touches) != 1 || len(rp.dead.touches) != 0 {
		t.Errorf("touches on the new driver %d, on the dead one %d", len(rp.alive.touches), len(rp.dead.touches))
	}
	if a.EffectUnknown {
		t.Error("nothing was sent before the restart, so the effect is known")
	}
}

func TestATapThatWasSentBeforeTheServerDiedHasAnUnknownEffect(t *testing.T) {
	r, rp := recoverRig(t, false)
	rp.dead.touchErr = errors.New("the server could not be reached")
	a := tapSubmit(r, "go")
	if !a.EffectUnknown {
		t.Error("a tap that was sent and failed must have an unknown effect")
	}
	if !a.Mobile.Recovered || rp.recovers != 1 {
		t.Errorf("recovered = %v, recovers = %d", a.Mobile.Recovered, rp.recovers)
	}
	if !strings.Contains(a.Err, "restarted") {
		t.Errorf("err = %q, want it to say that the server was restarted", a.Err)
	}
	if len(rp.alive.touches) != 0 {
		t.Error("the tap was sent again to the new server")
	}
}

func TestTheServerIsRestartedOnlyOncePerRun(t *testing.T) {
	r, rp := recoverRig(t, false)
	rp.dead.sourceErr = errors.New("the server could not be reached")
	tapSubmit(r, "one")
	// The new server fails as well. The run has used its restart.
	rp.alive.sourceErr = errors.New("the server could not be reached")
	a := tapSubmit(r, "two")
	if rp.recovers != 1 {
		t.Errorf("recovers = %d, want 1", rp.recovers)
	}
	if a.Mobile.Recovered || a.Err == "" {
		t.Errorf("the second step: recovered = %v, err = %q", a.Mobile.Recovered, a.Err)
	}
}

func TestAHealthyServerIsNotRestarted(t *testing.T) {
	r, rp := recoverRig(t, true)
	rp.dead.sourceErr = errors.New("the screen is not readable now")
	a := tapSubmit(r, "go")
	if rp.recovers != 0 || a.Mobile.Recovered {
		t.Errorf("recovers = %d, recovered = %v", rp.recovers, a.Mobile.Recovered)
	}
	if a.Err == "" || rp.healthCalls == 0 {
		t.Errorf("err = %q, health calls = %d", a.Err, rp.healthCalls)
	}
}

func TestAMissingElementDoesNotAskWhetherTheServerIsHealthy(t *testing.T) {
	r, rp := recoverRig(t, false)
	rp.dead.screen = screen(button("Help", 100, 100))
	a := tapSubmit(r, "go")
	if rp.healthCalls != 0 || rp.recovers != 0 || a.Mobile.Recovered {
		t.Errorf("health calls = %d, recovers = %d", rp.healthCalls, rp.recovers)
	}
}

func TestARestartThatFailsKeepsTheOriginalError(t *testing.T) {
	r, rp := recoverRig(t, false)
	rp.recoverErr = errors.New("the instrumentation did not start")
	rp.dead.sourceErr = errors.New("the server could not be reached")
	a := tapSubmit(r, "go")
	if a.Mobile.Recovered || !strings.Contains(a.Err, "the screen could not be read") {
		t.Errorf("recovered = %v, err = %q", a.Mobile.Recovered, a.Err)
	}
	if rp.recovers != 1 {
		t.Errorf("recovers = %d", rp.recovers)
	}
}
