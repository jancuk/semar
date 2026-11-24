package flow_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/screens"
)

const shopScreens = `apiVersion: semar/v1
kind: Screens
app: shop-app
screens:
  SignIn:
    email: { id: email }
    password: { id: password }
    submit: { text: Sign in }
    welcome: { id: welcome }
`

type screenFiles struct{ text string }

func (s screenFiles) Load(app string) (domain.Screens, error) {
	return screens.Parse([]byte(s.text), screens.Path(app), app)
}

type sink struct {
	mu      sync.Mutex
	actions []domain.Action
}

func (s *sink) WriteAction(a domain.Action) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, a)
	return "actions/" + string(a.ID) + ".json", nil
}
func (s *sink) AppendActivity(domain.Activity) error { return nil }

type memFiles struct{}

func (memFiles) WriteFile(string, []byte) error { return nil }

type secrets map[string]string

func (s secrets) Resolve(_ context.Context, ref string) (string, error) {
	if v, ok := s[ref]; ok {
		return v, nil
	}
	return "", errors.New("no such secret " + ref)
}
func (secrets) Loaded() []string { return nil }

// engine builds the real engine over a fake device, with a clock that only a sleep moves.
func engine(t *testing.T, d *mobiletest.FakeDriver, sec secrets, readOnly bool) (*mobile.Engine, *mobiletest.FakePlatform, *sink) {
	t.Helper()
	mode := "test_data"
	if readOnly {
		mode = "read_only"
	}
	pol, err := effects.New(config.Environment{Hosts: []string{"shop.example.com"}, Methods: []string{"GET"}, Effects: config.Effects{Mode: mode}})
	if err != nil {
		t.Fatal(err)
	}
	var known []string
	for _, v := range sec {
		known = append(known, v)
	}
	red, err := redact.New(config.Redact{}, func() []string { return known }, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	sk := &sink{}
	rec := recorder.New(sk, red, func(string, *url.URL) effects.Class { return effects.Read }, "run-1")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := &mobiletest.FakePlatform{D: d}
	e := mobile.New(mobile.Options{
		Policy: pol, Recorder: rec, Files: memFiles{}, Platform: p, Secrets: sec, Run: "run-1",
		Services: map[string]mobile.Service{"shop-app": {}},
		Clock:    func() time.Time { return now },
		Sleep:    func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil },
		TraceID:  func() string { return "0123456789abcdef0123456789abcdef" },
	})
	return e, p, sk
}

// shop is a device that shows the sign in screen, and the home screen after a tap on Sign in.
func shop() *mobiletest.FakeDriver {
	d := mobiletest.NewFakeDriver()
	signIn := func() *mobile.Tree {
		return mobiletest.Screen(
			mobiletest.Input("email", "Email", 100, 100),
			mobiletest.Input("password", "Password", 100, 250),
			mobiletest.Button("Sign in", 100, 400),
		)
	}
	home := mobiletest.Screen(withID(mobiletest.Node("android.widget.TextView", "Hi Ana", 100, 100), "welcome"))
	d.Show(signIn())
	d.OnLaunch = func() { d.Show(signIn()) }
	d.OnTouch = func(g mobile.Gesture) {
		if g.From.Y >= 400 && g.From.Y < 500 {
			d.Show(home)
		}
	}
	return d
}

func withID(n *mobile.Node, id string) *mobile.Node {
	n.ID = id
	n.Attrs["resource-id"] = id
	return n
}

func TestRunCompletesAndCapturesThroughTheEngine(t *testing.T) {
	d := shop()
	eng, _, sk := engine(t, d, secrets{"env:SHOP_PASSWORD": "s3cret-pass"}, false)
	f := validFlow(t)
	res, err := flow.Run(t.Context(), eng, screenFiles{shopScreens}, f, map[string]string{"email": "ana@example.com"}, map[string]string{"run.id": "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != flow.StatusCompleted || res.Status == "passed" || res.Error != "" {
		t.Fatalf("status = %q, error = %q", res.Status, res.Error)
	}
	if res.Captured["shown"] != "Hi Ana" {
		t.Errorf("captured = %v", res.Captured)
	}
	if got := d.Typed(); len(got) != 2 || got[0] != "ana@example.com" || got[1] != "s3cret-pass" {
		t.Errorf("typed = %q", got)
	}
	if len(res.Steps) != 4 || res.Steps[1].Name != "email" || !strings.Contains(res.Steps[1].Action.Why, "flow login, step email") {
		t.Errorf("steps = %+v", res.Steps)
	}
	if calls := d.Calls(); calls[0] != "reset" {
		t.Errorf("calls = %v: a flow starts from a reset app", calls)
	}
	// The recorded actions never hold the secret, and the captured value stays out of the evidence text.
	for _, a := range sk.actions {
		if strings.Contains(toText(a), "s3cret-pass") {
			t.Errorf("an action holds the secret: %+v", a)
		}
	}
}

func toText(a domain.Action) string {
	var b strings.Builder
	b.WriteString(a.Err + a.Refused + a.Why)
	if a.Mobile != nil {
		b.WriteString(a.Mobile.Link)
		for _, e := range a.Mobile.Elements {
			b.WriteString(e.Text)
		}
	}
	for _, v := range a.Captures {
		b.WriteString(v)
	}
	return b.String()
}

func TestRunStopsAtTheFirstStepThatFails(t *testing.T) {
	d := shop()
	d.Show(mobiletest.Screen(mobiletest.Button("Sign in", 100, 400))) // no email field
	d.OnLaunch = nil
	eng, _, _ := engine(t, d, secrets{"env:SHOP_PASSWORD": "pw"}, false)
	res, err := flow.Run(t.Context(), eng, screenFiles{shopScreens}, validFlow(t), map[string]string{"email": "a@b.c"}, map[string]string{"run.id": "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != flow.StatusFailed || !strings.Contains(res.Error, "step email") {
		t.Fatalf("status = %q, error = %q", res.Status, res.Error)
	}
	if len(res.Steps) != 2 {
		t.Errorf("%d steps ran, want the start and the failed email step only", len(res.Steps))
	}
	if len(d.Typed()) != 0 {
		t.Errorf("typed = %q: nothing is typed when the field is not there", d.Typed())
	}
}

func TestRunReportsARefusalAndNeverPasses(t *testing.T) {
	d := shop()
	eng, _, _ := engine(t, d, secrets{"env:SHOP_PASSWORD": "pw"}, true)
	res, err := flow.Run(t.Context(), eng, screenFiles{shopScreens}, validFlow(t), map[string]string{"email": "a@b.c"}, map[string]string{"run.id": "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != flow.StatusRefused || res.Error == "" || len(d.Typed()) != 0 {
		t.Errorf("status = %q, error = %q, typed = %q", res.Status, res.Error, d.Typed())
	}
}

func TestRunNeedsItsParamsAndItsScreens(t *testing.T) {
	d := shop()
	eng, _, _ := engine(t, d, secrets{"env:SHOP_PASSWORD": "pw"}, false)
	vars := map[string]string{"run.id": "run-1"}
	if _, err := flow.Run(t.Context(), eng, screenFiles{shopScreens}, validFlow(t), nil, vars); err == nil || !strings.Contains(err.Error(), "needs the param email") {
		t.Errorf("a missing param: err = %v", err)
	}
	if _, err := flow.Run(t.Context(), eng, screenFiles{shopScreens}, validFlow(t), map[string]string{"email": "x", "color": "red"}, vars); err == nil || !strings.Contains(err.Error(), "no param color") {
		t.Errorf("an unknown param: err = %v", err)
	}
	bare := strings.Replace(shopScreens, "    submit: { text: Sign in }\n", "", 1)
	if _, err := flow.Run(t.Context(), eng, screenFiles{bare}, validFlow(t), map[string]string{"email": "x"}, vars); err == nil || !strings.Contains(err.Error(), "element submit is not on screen SignIn") {
		t.Errorf("a missing element: err = %v", err)
	}
	if len(d.Calls()) != 0 {
		t.Errorf("calls = %v: nothing is sent when the flow does not fit", d.Calls())
	}
}

func TestRunRendersWithInTheScopeOfTheCaller(t *testing.T) {
	d := shop()
	eng, _, _ := engine(t, d, secrets{"env:SHOP_PASSWORD": "pw"}, false)
	_, err := flow.Run(t.Context(), eng, screenFiles{shopScreens}, validFlow(t), map[string]string{"email": "{{who}}@example.com"}, map[string]string{"run.id": "run-1", "who": "ana"})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Typed(); len(got) == 0 || got[0] != "ana@example.com" {
		t.Errorf("typed = %q", got)
	}
}

func TestBindUsesDefaultsAndRefusesWhatIsNotAParam(t *testing.T) {
	f := validFlow(t)
	scope, err := flow.Bind(f, map[string]string{"email": "a@b.c"}, map[string]string{"run.id": "r1"})
	if err != nil || scope["email"] != "a@b.c" || scope["password_env"] != "SHOP_PASSWORD" || scope["run.id"] != "r1" {
		t.Errorf("scope = %v, err = %v", scope, err)
	}
	if _, err := flow.Bind(f, map[string]string{"email": "{{missing}}"}, nil); err == nil {
		t.Error("a with value that names an unknown variable was accepted")
	}
}

func TestFlowDriverIsTheFakeThatOtherPackagesShare(t *testing.T) {
	// The recorder and the studio reuse this driver: it must satisfy the engine's interface.
	var _ mobile.Driver = (*mobiletest.FakeDriver)(nil)
	var _ mobile.Platform = (*mobiletest.FakePlatform)(nil)
}

func outerFlow(expect ...domain.Expectation) domain.Flow {
	return domain.Flow{
		APIVersion: "semar/v1", Kind: "Flow", ID: "outer", Surface: domain.SurfaceAndroid, Service: "shop-app", App: "shop-app",
		Params: []domain.FlowParam{{Name: "who"}},
		Steps: []domain.ScriptStep{
			{Step: "sign-in", Use: "login", With: map[string]string{"email": "{{who}}@example.com"}},
			{
				Step:   "home-ready",
				Mobile: &domain.MobileAction{Do: domain.MobileWait, Screen: "SignIn", Element: "welcome"},
				Expect: expect,
			},
			{
				Step:   "greeting",
				Mobile: &domain.MobileAction{Do: domain.MobileWait, Screen: "SignIn", Element: "welcome"},
				Captures: []domain.Capture{
					{Name: "greeted", Element: "welcome", Pattern: `Hi (\w+)`},
				},
			},
		},
	}
}

func nestedFlows(outer domain.Flow) func(string) (domain.Flow, string, error) {
	login := validFlowForRun()
	return func(id string) (domain.Flow, string, error) {
		switch id {
		case "login":
			return login, "sha256:login", nil
		case "outer":
			return outer, "sha256:outer", nil
		}
		return domain.Flow{}, "", flow.ErrNotFound
	}
}

func validFlowForRun() domain.Flow {
	f, err := flow.Parse([]byte(loginYAML), "login.yaml", "login")
	if err != nil {
		panic(err)
	}
	return f
}

func TestRunRunsAFlowInsideAFlowWithItsOwnScope(t *testing.T) {
	d := shop()
	eng, _, _ := engine(t, d, secrets{"env:SHOP_PASSWORD": "pw"}, false)
	outer := outerFlow(domain.Expectation{Check: domain.Check{Element: "welcome", Contains: "Hi"}, Source: domain.SourcePerson})
	res, err := flow.RunWith(t.Context(), eng, screenFiles{shopScreens}, outer, map[string]string{"who": "ana"}, map[string]string{"run.id": "run-1"}, flow.Options{Flows: nestedFlows(outer)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != flow.StatusCompleted || res.Error != "" || res.Checked != 1 {
		t.Fatalf("status = %q, error = %q, checked = %d", res.Status, res.Error, res.Checked)
	}
	// The inner flow got its param from with, rendered in the scope of the outer flow.
	if got := d.Typed(); len(got) == 0 || got[0] != "ana@example.com" {
		t.Errorf("typed = %q", got)
	}
	// What the inner flow captured, and what the outer one captured through a pattern, is kept.
	if res.Captured["shown"] != "Hi Ana" || res.Captured["greeted"] != "Ana" {
		t.Errorf("captured = %v", res.Captured)
	}
	if !strings.Contains(res.Steps[1].Action.Why, "flow outer > flow login, step email") {
		t.Errorf("why = %q", res.Steps[1].Action.Why)
	}
	if len(res.Steps) != 6 {
		t.Errorf("%d steps ran, want 4 of login and 2 of outer", len(res.Steps))
	}
}

func TestRunFailsWhenACheckOfAFlowIsFalse(t *testing.T) {
	d := shop()
	eng, _, _ := engine(t, d, secrets{"env:SHOP_PASSWORD": "pw"}, false)
	outer := outerFlow(domain.Expectation{Check: domain.Check{Element: "welcome", Equals: "Hi Bob"}, Source: domain.SourcePerson})
	res, err := flow.RunWith(t.Context(), eng, screenFiles{shopScreens}, outer, map[string]string{"who": "ana"}, map[string]string{"run.id": "run-1"}, flow.Options{Flows: nestedFlows(outer)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != flow.StatusFailed || !strings.Contains(res.Error, "step home-ready: expected") || !strings.Contains(res.Error, "observed") {
		t.Fatalf("status = %q, error = %q", res.Status, res.Error)
	}
	if res.Captured["greeted"] != "" {
		t.Errorf("a step after the false check ran: %v", res.Captured)
	}
}

func TestRunNeedsTheFlowsItUses(t *testing.T) {
	d := shop()
	eng, _, _ := engine(t, d, secrets{"env:SHOP_PASSWORD": "pw"}, false)
	outer := outerFlow()
	_, err := flow.Run(t.Context(), eng, screenFiles{shopScreens}, outer, map[string]string{"who": "ana"}, map[string]string{"run.id": "run-1"})
	if err == nil || !strings.Contains(err.Error(), "no flows") {
		t.Errorf("err = %v", err)
	}
}
