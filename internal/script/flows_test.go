package script_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/script"
)

func login() domain.Flow {
	return domain.Flow{
		APIVersion: "semar/v1", Kind: "Flow", ID: "login", Surface: domain.SurfaceAndroid, Service: "shop-app", App: "shop-app",
		Params: []domain.FlowParam{{Name: "email"}},
		Steps: []domain.ScriptStep{
			{Step: "start", Mobile: &domain.MobileAction{Do: domain.MobileLaunch}},
			{Step: "email", Mobile: &domain.MobileAction{Do: domain.MobileType, Screen: "SignIn", Element: "email", Value: "{{email}}"}},
			{Step: "go", Mobile: &domain.MobileAction{Do: domain.MobileTap, Screen: "SignIn", Element: "submit"}},
		},
	}
}

func flowsOf(fl ...domain.Flow) script.FlowResolver {
	return func(id string) (domain.Flow, error) {
		for _, f := range fl {
			if f.ID == id {
				return f, nil
			}
		}
		return domain.Flow{}, fmt.Errorf("the flow file was not found")
	}
}

// useScenario signs in with a flow, then waits for the welcome text.
func useScenario() (domain.Scenario, domain.Script) {
	sc := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "task", Ticket: "T-1", Intent: "i", Surface: domain.SurfaceAndroid, AuthProfile: "alice",
		Steps: []domain.Step{
			{ID: "login", Action: "Sign in"},
			{ID: "seen", Action: "See the welcome text", Expect: []domain.Expectation{
				{Check: domain.Check{Element: "welcome", Equals: "Hi Ana"}, Source: domain.SourcePerson},
			}},
		},
	}
	s := domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc),
		Steps: []domain.ScriptStep{
			{Step: "login", Use: "login", With: map[string]string{"email": "ana@example.com"}},
			{Step: "seen", Mobile: &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileWait, Screen: "SignIn", Element: "welcome"}},
		},
	}
	return sc, s
}

func TestAFlowStepFitsAScenario(t *testing.T) {
	sc, s := useScenario()
	if ps := script.ValidateWithFlows(sc, s, nil, flowsOf(login())); len(ps) > 0 {
		t.Fatalf("problems:\n%s", problemText(ps))
	}
}

// The flow starts the app, so the first mobile step of the script after it needs no launch.
func TestTheFlowSatisfiesTheFirstStepRule(t *testing.T) {
	sc, s := useScenario()
	if ps := script.ValidateWithFlows(sc, s, nil, flowsOf(login())); len(ps) > 0 {
		t.Fatalf("problems:\n%s", problemText(ps))
	}
	// A flow that does not start the app does not satisfy it.
	f := login()
	f.Steps = f.Steps[1:]
	ps := script.ValidateWithFlows(sc, s, nil, flowsOf(f))
	if !strings.Contains(problemText(ps), "the first mobile step of the service shop-app must be launch or open, and flow login starts with type") {
		t.Errorf("problems:\n%s", problemText(ps))
	}
}

func TestWithoutAResolverAUseStepIsAccepted(t *testing.T) {
	sc, s := useScenario()
	for name, check := range map[string]func(domain.Scenario, domain.Script) []script.Problem{
		"Validate":     script.Validate,
		"ValidateWith": func(sc domain.Scenario, s domain.Script) []script.Problem { return script.ValidateWith(sc, s, nil) },
		"ValidateWithFlows(nil)": func(sc domain.Scenario, s domain.Script) []script.Problem {
			return script.ValidateWithFlows(sc, s, nil, nil)
		},
	} {
		if ps := check(sc, s); len(ps) > 0 {
			t.Errorf("%s:\n%s", name, problemText(ps))
		}
	}
	shape := s
	shape.ExpectHash = ""
	shape.ShapeHash = domain.ShapeHash(sc)
	if shape.ShapeHash != "" {
		if ps := script.ValidateShapeWith(sc, shape, nil); len(ps) > 0 {
			t.Errorf("ValidateShapeWith:\n%s", problemText(ps))
		}
	}
}

func TestWithoutAResolverACaptureOfAFlowMayBeUsedLater(t *testing.T) {
	sc, s := useScenario()
	s.Steps[1].Mobile = &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileType, Screen: "SignIn", Element: "email", Value: "{{token}}"}
	if ps := script.Validate(sc, s); len(ps) > 0 {
		t.Errorf("problems:\n%s", problemText(ps))
	}
	if ps := script.ValidateWithFlows(sc, s, nil, flowsOf(login())); !strings.Contains(problemText(ps), "token is used before any step captures it") {
		t.Errorf("a strict check must refuse a name that no step captures:\n%s", problemText(ps))
	}
}

func TestAUseStepProblems(t *testing.T) {
	cases := map[string]struct {
		edit func(*domain.Scenario, *domain.Script)
		flow domain.Flow
		want string
	}{
		"no such flow": {func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].Use = "gone" }, login(), "use: flow gone: the flow file was not found"},
		"a flow for the web": {func(*domain.Scenario, *domain.Script) {}, func() domain.Flow { f := login(); f.Surface = domain.SurfaceWeb; return f }(),
			"a scenario of the surface android needs an android flow"},
		"a request as well": {func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request = &domain.APIRequest{Service: "x", Method: "GET", Path: "/"}
		}, login(), "a request, a web action, a mobile action, or a use, and not more than one"},
		"a mobile action as well": {func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Mobile = &domain.MobileAction{Do: domain.MobileLaunch}
		}, login(), "not more than one"},
		"with and no use": {func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[1].With = map[string]string{"a": "b"}
		}, login(), "with belongs to a step that has use"},
		"a param that is not there": {func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].With["color"] = "red"
		}, login(), "flow login has no param color; its params are email"},
		"a param that is not given": {func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].With = nil }, login(), "flow login needs the param email"},
		"a with value of an unknown variable": {func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].With["email"] = "{{nobody}}"
		}, login(), "with: nobody is used before any step captures it"},
		"a malformed with value": {func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].With["email"] = "{{oops"
		}, login(), "holds a malformed variable"},
		"a web scenario":  {func(sc *domain.Scenario, _ *domain.Script) { sc.Surface = domain.SurfaceWeb }, login(), "the scenario needs the surface android"},
		"another account": {func(sc *domain.Scenario, _ *domain.Script) { sc.Steps[0].As = "bob" }, login(), "a device has one signed-in account, so as cannot be bob"},
		// F4: v1 refuses an expected result and captures on a use step.
		"captures on a use step": {func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Captures = []domain.Capture{{Name: "x", Element: "welcome"}}
		}, login(), "takes no captures in this version"},
		"an expected result on a use step": {func(sc *domain.Scenario, _ *domain.Script) {
			sc.Steps[0].Expect = []domain.Expectation{{Check: domain.Check{Element: "welcome", Equals: "x"}, Source: domain.SourcePerson}}
		}, login(), "takes no expected result in this version"},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			sc, s := useScenario()
			tt.edit(&sc, &s)
			s.ExpectHash = domain.ExpectHash(sc)
			ps := script.ValidateWithFlows(sc, s, nil, flowsOf(tt.flow))
			if !strings.Contains(problemText(ps), tt.want) {
				t.Errorf("problems:\n%s\nwant one holding %q", problemText(ps), tt.want)
			}
		})
	}
}

func TestAnExpectedResultOnAUseStepIsRefusedWithoutAResolverToo(t *testing.T) {
	sc, s := useScenario()
	sc.Steps[0].Expect = []domain.Expectation{{Check: domain.Check{Element: "welcome", Equals: "x"}, Source: domain.SourcePerson}}
	s.ExpectHash = domain.ExpectHash(sc)
	if ps := script.Validate(sc, s); !strings.Contains(problemText(ps), "takes no expected result in this version") {
		t.Errorf("problems:\n%s", problemText(ps))
	}
}

func capturing() domain.Flow {
	f := login()
	f.Steps[2].Captures = []domain.Capture{{Name: "shown", Element: "welcome"}}
	return f
}

func TestAFlowThatCapturesJoinsTheScope(t *testing.T) {
	sc, s := useScenario()
	s.Steps[1].Mobile = &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileType, Screen: "SignIn", Element: "email", Value: "{{shown}}"}
	if ps := script.ValidateWithFlows(sc, s, nil, flowsOf(capturing())); len(ps) > 0 {
		t.Errorf("problems:\n%s", problemText(ps))
	}
}

func TestAFlowThatCapturesCannotBeUsedTwice(t *testing.T) {
	sc, s := useScenario()
	sc.Steps = append(sc.Steps, domain.Step{ID: "again", Action: "Sign in again"})
	s.ExpectHash = domain.ExpectHash(sc)
	s.Steps = append(s.Steps, domain.ScriptStep{Step: "again", Use: "login", With: map[string]string{"email": "bob@example.com"}})
	ps := script.ValidateWithFlows(sc, s, nil, flowsOf(capturing()))
	if !strings.Contains(problemText(ps), "flow login captures shown, which an earlier step already captured: a flow that captures can be used once in a scenario") {
		t.Errorf("problems:\n%s", problemText(ps))
	}
	// A flow that captures nothing can be used as often as the scenario likes.
	if ps := script.ValidateWithFlows(sc, s, nil, flowsOf(login())); len(ps) > 0 {
		t.Errorf("a flow with no capture, used twice:\n%s", problemText(ps))
	}
}

func TestAFlowCaptureCannotCollideWithAScenarioCapture(t *testing.T) {
	sc, s := useScenario()
	sc.Steps = append(sc.Steps, domain.Step{ID: "more", Action: "x"})
	s.ExpectHash = domain.ExpectHash(sc)
	s.Steps = append(s.Steps, domain.ScriptStep{Step: "more", Mobile: &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileWait, Screen: "SignIn", Element: "welcome"},
		Captures: []domain.Capture{{Name: "shown", Element: "welcome"}}})
	if ps := script.ValidateWithFlows(sc, s, nil, flowsOf(capturing())); !strings.Contains(problemText(ps), "capture shown is defined more than once") {
		t.Errorf("problems:\n%s", problemText(ps))
	}
}

func TestAUseStepRoundTripsThroughYAML(t *testing.T) {
	_, s := useScenario()
	data, err := script.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "use: login") || !strings.Contains(string(data), "with:\n      email: ana@example.com") {
		t.Errorf("yaml:\n%s", data)
	}
	got, err := script.Parse(data, "s.yaml")
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(got.Steps[0], s.Steps[0]) || got.Steps[0].Use != "login" || got.Steps[0].With["email"] != "ana@example.com" {
		t.Errorf("step = %+v", got.Steps[0])
	}
	// A use step with no with map writes none.
	s.Steps[0].With = nil
	data, err = script.Marshal(s)
	if err != nil || strings.Contains(string(data), "with:") {
		t.Errorf("err = %v, yaml:\n%s", err, data)
	}
	// An empty with map reads back as none, so it is still the value that Marshal compared against.
	s.Steps[0].With = map[string]string{}
	if _, err := script.Marshal(s); err != nil {
		t.Errorf("an empty with: %v", err)
	}
}

func TestRenderWithFillsTheValuesInTheCallersScope(t *testing.T) {
	got, err := script.RenderWith(map[string]string{"email": "{{who}}@example.com", "plain": "x"}, map[string]string{"who": "ana"})
	if err != nil || got["email"] != "ana@example.com" || got["plain"] != "x" {
		t.Errorf("got = %v, err = %v", got, err)
	}
	if _, err := script.RenderWith(map[string]string{"email": "{{nobody}}"}, nil); err == nil || !strings.Contains(err.Error(), "with email") {
		t.Errorf("err = %v", err)
	}
	if got, err := script.RenderWith(nil, nil); got != nil || err != nil {
		t.Errorf("nil with: %v, %v", got, err)
	}
	if vars := script.WithVariables(map[string]string{"a": "{{x}}{{y}}", "b": "{{x}}"}); !reflect.DeepEqual(vars, []string{"x", "y"}) {
		t.Errorf("vars = %v", vars)
	}
}

// The reset of a launch is written to the file and read back, so that a recorded flow keeps it.
func TestTheResetOfALaunchSurvivesMarshalAndParse(t *testing.T) {
	s := domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: "AND-1",
		AuthoredBy: domain.ProviderRef{Instance: "person", Harness: "record"},
		Steps:      []domain.ScriptStep{{Step: "start", Mobile: &domain.MobileAction{Service: "shop-app", App: "shop-app", Do: domain.MobileLaunch, Reset: "clear"}}},
	}
	data, err := script.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "reset: clear") {
		t.Errorf("script:\n%s", data)
	}
	got, err := script.Parse(data, "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[0].Mobile.Reset != "clear" {
		t.Errorf("step = %+v", got.Steps[0].Mobile)
	}
}
