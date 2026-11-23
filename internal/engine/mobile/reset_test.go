package mobile_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

// plainDriver hides the optional reset of a driver, as a platform that has one mode only does.
type plainDriver struct{ mobile.Driver }

func launchRig(t *testing.T, d mobile.Driver) *rig {
	return newRig(t, rigOptions{}, func(o *mobile.Options) { o.Platform = &mobiletest.FakePlatform{D: d} })
}

// A launch that names a reset overrides the one of the service: a scenario that must start from a clean
// install and one that must start signed in live in the same repository.
func TestALaunchResetsWithItsOwnMode(t *testing.T) {
	for _, mode := range []string{"clear", "restart", "none"} {
		t.Run(mode, func(t *testing.T) {
			fd := mobiletest.NewFakeDriver()
			r := launchRig(t, fd)
			res := r.do(call("start", domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileLaunch, Reset: mode}, nil))
			if res.Action.Err != "" || res.Action.Refused != "" {
				t.Fatalf("action = %+v", res.Action)
			}
			if got := strings.Join(fd.Calls(), ","); got != "reset:"+mode+",launch" {
				t.Errorf("calls = %q, want the reset with the mode of the launch and then the launch", got)
			}
		})
	}
}

func TestALaunchWithNoModeUsesTheResetOfTheService(t *testing.T) {
	fd := mobiletest.NewFakeDriver()
	r := launchRig(t, fd)
	r.do(call("start", domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileLaunch}, nil))
	if got := strings.Join(fd.Calls(), ","); got != "reset,launch" || len(fd.ResetModes()) != 0 {
		t.Errorf("calls = %q", got)
	}
}

func TestOnlyTheFirstLaunchOfAScenarioResets(t *testing.T) {
	fd := mobiletest.NewFakeDriver()
	r := launchRig(t, fd)
	r.do(call("a", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch, Reset: "clear"}, nil))
	r.do(call("b", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch, Reset: "clear"}, nil))
	if got := strings.Join(fd.Calls(), ","); got != "reset:clear,launch,launch" {
		t.Errorf("calls = %q", got)
	}
}

func TestADriverWithNoResetModesSaysSoAndLaunchesNothing(t *testing.T) {
	fd := mobiletest.NewFakeDriver()
	r := launchRig(t, plainDriver{fd})
	res := r.do(call("start", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch, Reset: "clear"}, nil))
	if !strings.Contains(res.Action.Err, "cannot reset with the mode clear") {
		t.Errorf("err = %q", res.Action.Err)
	}
	if got := strings.Join(fd.Calls(), ","); got != "" {
		t.Errorf("calls = %q", got)
	}
}

func TestAResetOnAnotherActionIsRefusedBeforeTheDevice(t *testing.T) {
	fd := mobiletest.NewFakeDriver()
	r := launchRig(t, fd)
	_, err := r.e.Do(context.Background(), call("go", domain.MobileAction{Service: "shop-app", Do: domain.MobileTap, Screen: "S", Element: "e", Reset: "clear"}, nil))
	if err == nil || !strings.Contains(err.Error(), "tap takes no reset") {
		t.Errorf("err = %v", err)
	}
}

// A Flutter label can hold line breaks. A locator by its desc matches however the breaks and the spaces
// fall, and both of them collapse to one space.
func TestADescWithLineBreaksMatchesTheCollapsedLocator(t *testing.T) {
	b := mobiletest.Button("", 0, 0)
	b.Desc = "Add to\ncart\n  now"
	for _, want := range []string{"Add to cart now", "Add to\ncart now", "Add   to cart\nnow"} {
		st, node, err := mobile.Resolve(mobiletest.Screen(b), map[string]domain.Locator{"add": {Desc: want}}, "add")
		if err != nil || !st.Found || node != b {
			t.Errorf("desc %q: state %+v, err %v", want, st, err)
		}
	}
}
